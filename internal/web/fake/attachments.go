package fake

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The real provider's limits (internal/amp): 500 MB per file for local mail,
// 10 files per message, unsent uploads kept two hours, files thirty days.
const (
	DefaultMaxAttachment = 500 << 20
	maxAttachments       = 10
	unsentTTL            = 2 * time.Hour
	attachmentTTL        = 30 * 24 * time.Hour
)

// ErrTooLarge is wrapped by UploadAttachment when a file is over the limit,
// like the provider's own "attachment too large" error.
var ErrTooLarge = fmt.Errorf("attachment %w", core.ErrTooLarge)

type stored struct {
	meta    core.Attachment
	data    []byte
	owner   string
	message string // ID of the message it went out in; "" while unsent
}

// UploadAttachment keeps the file in memory, the way the provider stores it
// on disk: sanitized name, sha256 digest, basic scan.
func (s *Service) UploadAttachment(ctx context.Context, filename, contentType string, r io.Reader) (core.Attachment, error) {
	s.mu.Lock()
	limit := s.maxAttachment
	s.mu.Unlock()
	name := sanitizeFilename(filename)
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, limit+1))
	if err != nil {
		return core.Attachment{}, err
	}
	if n > limit {
		return core.Attachment{}, fmt.Errorf("%w: %s is larger than %d MB", ErrTooLarge, name, limit>>20)
	}
	if n == 0 {
		return core.Attachment{}, fmt.Errorf("%w: %s is empty", core.ErrInvalid, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneUploads(now)
	a := s.storeAttachment(now, s.org.User.Name, name, contentType, buf.Bytes())
	return a, nil
}

// storeAttachment records bytes as an upload. Caller holds mu.
func (s *Service) storeAttachment(now time.Time, owner, name, contentType string, data []byte) core.Attachment {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	sum := sha256.Sum256(data)
	a := core.Attachment{
		Storage: "provider", ID: fmt.Sprintf("att_%d_%s", now.Unix(), hex.EncodeToString(b)), Filename: name,
		Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
		UploadedAt: now.UTC(), ExpiresAt: now.UTC().Add(attachmentTTL),
	}
	a.URL = "/v1/attachments/" + a.ID + "/download"
	a.ContentType, a.ScanStatus = basicScan(contentType, name, data)
	if s.files == nil {
		s.files = map[string]*stored{}
	}
	s.files[a.ID] = &stored{meta: a, data: data, owner: owner}
	return a
}

// pruneUploads drops uploads that never went out. Caller holds mu.
func (s *Service) pruneUploads(now time.Time) {
	for id, f := range s.files {
		if f.message == "" && now.Sub(f.meta.UploadedAt) > unsentTTL {
			delete(s.files, id)
		}
	}
}

// resolveAttachments turns the sender's upload IDs into the metadata the
// message carries, and binds them to message id. Caller holds mu.
func (s *Service) resolveAttachments(from string, remote bool, ids []string) ([]core.Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if remote {
		return nil, fmt.Errorf("%w: files cannot be sent to another lolad yet", core.ErrInvalid)
	}
	if len(ids) > maxAttachments {
		return nil, fmt.Errorf("%w: at most %d attachments per message", core.ErrInvalid, maxAttachments)
	}
	out := make([]core.Attachment, 0, len(ids))
	for _, id := range ids {
		f := s.files[id]
		if f == nil || f.owner != from {
			return nil, fmt.Errorf("%w: attachment %s not found", core.ErrInvalid, id)
		}
		if f.message != "" {
			return nil, fmt.Errorf("%w: attachment %s was already sent", core.ErrConflict, id)
		}
		out = append(out, f.meta)
	}
	return out, nil
}

func (s *Service) bindAttachments(m core.Mail) {
	for _, a := range m.Attachments {
		if f := s.files[a.ID]; f != nil {
			f.message = m.ID
		}
	}
}

// OpenAttachment reads one the user uploaded, or one on a message the user
// sent or received.
func (s *Service) OpenAttachment(ctx context.Context, id string) (core.Attachment, io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[id]
	if f == nil {
		return core.Attachment{}, nil, fmt.Errorf("attachment %q: %w", id, core.ErrNotFound)
	}
	me := s.org.User.Name
	ok := f.owner == me
	for _, m := range s.mail {
		if ok {
			break
		}
		if m.ID == f.message && (m.FromName == me || m.ToName == me) {
			ok = true
		}
	}
	if !ok {
		return core.Attachment{}, nil, fmt.Errorf("attachment %q: %w", id, core.ErrNotFound)
	}
	return f.meta, readSeekCloser{bytes.NewReader(f.data)}, nil
}

// readSeekCloser is seekable like the provider's *os.File, so Range works.
type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

// sanitizeFilename is a smaller amp.SanitizeFilename: no directories, no
// control characters, never empty.
func sanitizeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(strings.Join(strings.Fields(name), " "), ". ")
	if name == "" {
		return "file"
	}
	return name
}

// basicScan mirrors the provider's check: a name or declared type that claims
// a format the first bytes contradict is suspicious.
func basicScan(declared, filename string, data []byte) (contentType, status string) {
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	byName := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	contentType = strings.TrimSpace(declared)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = byName
	}
	if contentType == "" {
		contentType = sniffed
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = mt
	} else {
		contentType = "application/octet-stream"
	}
	for _, claim := range []string{contentType, byName} {
		claim, _, _ = strings.Cut(claim, ";")
		switch {
		case claim == "image/svg+xml":
			if !strings.HasPrefix(sniffed, "text/") {
				return contentType, "suspicious"
			}
		case strings.HasPrefix(claim, "image/"), claim == "application/pdf", claim == "application/zip":
			if sniffed != claim && !(strings.HasPrefix(claim, "image/") && strings.HasPrefix(sniffed, "image/")) {
				return contentType, "suspicious"
			}
		}
	}
	return contentType, "basic_clean"
}

// seedAttachments puts a few files on the seeded mail: a screenshot the user
// sent, a deliverable with a preview and a CSV, and one file whose contents
// contradict its name. Caller holds mu (or is New).
func (s *Service) seedAttachments() {
	attach := func(subject, from, name, contentType string, data []byte) {
		for i := range s.mail {
			m := &s.mail[i]
			if m.Subject != subject || m.FromName != from {
				continue
			}
			a := s.storeAttachment(m.CreatedAt, from, name, contentType, data)
			s.files[a.ID].message = m.ID
			m.Attachments = append(m.Attachments, a)
			return
		}
	}
	attach("Ledger + pricing", "milo", "ledger-crash.png", "image/png", mockPNG(720, 420, false))
	attach("Pricing page draft", "lola", "pricing-draft.png", "image/png", mockPNG(1200, 760, true))
	attach("Pricing page draft", "lola", "Página de precios.zip", "application/zip", mockZip())
	attach("Invoice export", "forge", "invoices-2026-10.csv", "text/csv",
		[]byte("invoice,customer,amount,currency,issued\nINV-1041,Acme Co,290.00,USD,2026-10-01\nINV-1042,Globex,1450.00,USD,2026-10-03\nINV-1043,Initech,87.50,USD,2026-10-08\n"))
	attach("Stuck on a permission prompt", "relay", "lockfile-report.pdf", "application/pdf",
		[]byte("npm ERR! code EINTEGRITY\nnpm ERR! sha512 integrity checksum failed when using sha512\n"))
}

// mockPNG draws a quiet wireframe: a header bar and three cards (or a crash
// log when cards is false).
func mockPNG(w, h int, cards bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := max(y0, 0); y < min(y1, h); y++ {
			for x := max(x0, 0); x < min(x1, w); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	bg, card, ink, line, honey := color.RGBA{246, 246, 246, 255}, color.RGBA{255, 255, 255, 255}, color.RGBA{40, 40, 40, 255}, color.RGBA{220, 220, 220, 255}, color.RGBA{232, 190, 92, 255}
	fill(0, 0, w, h, bg)
	fill(0, 0, w, h/9, ink)
	if cards {
		cw := (w - 4*w/24) / 3
		for i := 0; i < 3; i++ {
			x := w/24 + i*(cw+w/24)
			fill(x-1, h/5-1, x+cw+1, h-h/10+1, line)
			fill(x, h/5, x+cw, h-h/10, card)
			fill(x+cw/8, h/5+h/14, x+cw/2, h/5+h/14+h/30, ink)
			for j := 0; j < 4; j++ {
				y := h/5 + h/5 + j*h/12
				fill(x+cw/8, y, x+cw-cw/6, y+h/60, line)
			}
			c := ink
			if i == 1 {
				c = honey
			}
			fill(x+cw/8, h-h/10-h/9, x+cw-cw/8, h-h/10-h/24, c)
		}
	} else {
		fill(w/20, h/6, w-w/20, h-h/12, ink)
		for j := 0; j < 9; j++ {
			y := h/6 + h/16 + j*h/14
			c := color.RGBA{150, 150, 150, 255}
			if j == 6 {
				c = color.RGBA{220, 110, 90, 255}
			}
			fill(w/20+w/24, y, w/20+w/24+(w/3+(j*97)%(w/3)), y+h/60, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// mockZip is a small site export.
func mockZip() []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"index.html": "<!doctype html><title>Pricing</title><h1>Pricing without seat math</h1>\n",
		"styles.css": "body { font: 16px/1.5 system-ui; }\n",
	} {
		w, _ := z.Create(name)
		_, _ = io.WriteString(w, body)
	}
	_ = z.Close()
	return b.Bytes()
}
