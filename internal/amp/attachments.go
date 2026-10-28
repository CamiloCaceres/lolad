package amp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// AMP spec 04 limits. Federated mail keeps them; mail that stays on this lolad
// may carry larger files (WithLocalAttachmentLimit), because the bytes never
// leave the machine.
const (
	MaxAttachments       = 10
	maxRemoteAttachment  = 25 << 20
	maxRemoteAttachments = 100 << 20
	// DefaultLocalAttachmentLimit is the per-file cap for local delivery.
	DefaultLocalAttachmentLimit = 500 << 20
	attachmentTTL               = 30 * 24 * time.Hour
	unreferencedTTL             = 2 * time.Hour
)

// WithLocalAttachmentLimit sets the per-file cap for mail between identities
// on this lolad; a message may carry up to twice that in total.
func WithLocalAttachmentLimit(n int64) Option {
	return func(p *Provider) {
		if n > 0 {
			p.localAttachmentLimit = n
		}
	}
}

var attachmentID = regexp.MustCompile(`^att_[0-9]+_[0-9a-f]{24}$`)
var windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$`)

// SanitizeFilename keeps letters (any script), digits and a few punctuation
// marks. Path separators, encoded separators, control characters and Windows
// reserved names are removed (spec 04).
func SanitizeFilename(name string) string {
	for _, enc := range []string{"%2f", "%2F", "%5c", "%5C"} {
		name = strings.ReplaceAll(name, enc, "_")
	}
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			b.WriteRune(r)
		case r == ' ' || strings.ContainsRune("._-()[]+,@#&=~'", r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
	}
	out := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if windowsReserved.MatchString(out) {
		out = "_" + out
	}
	for len(out) > 255 {
		_, size := utf8.DecodeLastRuneInString(out)
		out = out[:len(out)-size]
	}
	if out == "" {
		out = "file"
	}
	return out
}

// basicScan is the spec's basic_clean check (size, digest, MIME type and magic
// bytes): a file whose name or declared type claims a format its first bytes
// contradict is suspicious. Nothing here is an antivirus scan.
func basicScan(declared, filename string, head []byte) (contentType, status string) {
	sniffed := http.DetectContentType(head)
	byName := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	contentType = strings.TrimSpace(declared)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = byName
	}
	if contentType == "" {
		contentType = sniffed
	}
	if mt, _, e := mime.ParseMediaType(contentType); e == nil {
		contentType = mt
	} else {
		contentType = "application/octet-stream"
	}
	base, _, _ := strings.Cut(sniffed, ";")
	for _, claim := range []string{contentType, byName} {
		claim, _, _ = strings.Cut(claim, ";")
		switch {
		case claim == "image/svg+xml":
			if !strings.HasPrefix(base, "text/") {
				return contentType, "suspicious"
			}
		case strings.HasPrefix(claim, "image/"), claim == "application/pdf", claim == "application/zip":
			if base != claim && !(claim == "application/zip" && base == "application/x-gzip") && !(strings.HasPrefix(claim, "image/") && strings.HasPrefix(base, "image/")) {
				return contentType, "suspicious"
			}
		}
	}
	return contentType, "basic_clean"
}

func (p *Provider) attachmentDir() string { return filepath.Join(p.stateDir, "attachments") }

// UploadAttachment stores one file for owner. The returned object is final
// (stored, digested, checked): put it, or its ID via core.Outgoing, in one
// message within two hours or it is deleted.
func (p *Provider) UploadAttachment(ctx context.Context, owner, filename, contentType string, r io.Reader) (core.Attachment, error) {
	var zero core.Attachment
	n, e := p.local(owner)
	if e != nil {
		return zero, e
	}
	if _, _, e = p.identity(ctx, n); e != nil {
		return zero, e
	}
	p.pruneAttachments(ctx, time.Now())
	dir := p.attachmentDir()
	if e = os.MkdirAll(dir, 0700); e != nil {
		return zero, e
	}
	token, e := randomHex(12)
	if e != nil {
		return zero, e
	}
	now := time.Now().UTC()
	a := core.Attachment{ID: fmt.Sprintf("att_%d_%s", now.Unix(), token), Filename: SanitizeFilename(filename), UploadedAt: now, ExpiresAt: now.Add(attachmentTTL)}
	a.URL = "/v1/attachments/" + a.ID + "/download"
	tmp, e := os.CreateTemp(dir, ".upload-*")
	if e != nil {
		return zero, e
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	limit := p.localLimit()
	sum := sha256.New()
	head := make([]byte, 512)
	nh, _ := io.ReadFull(r, head)
	head = head[:nh]
	written, e := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(io.MultiReader(bytes.NewReader(head), r), limit+1))
	if e != nil {
		return zero, e
	}
	if written > limit {
		return zero, fmt.Errorf("%w: %s is larger than %d MB", errAttachmentTooLarge, a.Filename, limit>>20)
	}
	if written == 0 {
		return zero, fmt.Errorf("%w: %s is empty", core.ErrInvalid, a.Filename)
	}
	if e = tmp.Close(); e != nil {
		return zero, e
	}
	a.Size = written
	a.Digest = "sha256:" + hex.EncodeToString(sum.Sum(nil))
	a.ContentType, a.ScanStatus = basicScan(contentType, a.Filename, head)
	if e = os.Rename(tmp.Name(), filepath.Join(dir, a.ID)); e != nil {
		return zero, e
	}
	meta, _ := json.Marshal(a)
	if _, e = p.db.ExecContext(ctx, `INSERT INTO amp_attachments(id,owner,meta,message_id,created_at,expires_at) VALUES(?,?,?,'',?,?)`, a.ID, n, string(meta), now.Format(time.RFC3339Nano), a.ExpiresAt.Format(time.RFC3339Nano)); e != nil {
		os.Remove(filepath.Join(dir, a.ID))
		return zero, e
	}
	return a, nil
}

var errAttachmentTooLarge = fmt.Errorf("attachment %w", core.ErrTooLarge)

func (p *Provider) localLimit() int64 {
	if p.localAttachmentLimit > 0 {
		return p.localAttachmentLimit
	}
	return DefaultLocalAttachmentLimit
}

type storedAttachment struct {
	core.Attachment
	owner, message string
}

func (p *Provider) storedAttachment(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (storedAttachment, error) {
	var s storedAttachment
	if !attachmentID.MatchString(id) {
		return s, core.ErrNotFound
	}
	var meta string
	e := q.QueryRowContext(ctx, `SELECT owner,meta,message_id FROM amp_attachments WHERE id=?`, id).Scan(&s.owner, &meta, &s.message)
	if errors.Is(e, sql.ErrNoRows) {
		return s, core.ErrNotFound
	}
	if e != nil {
		return s, e
	}
	return s, json.Unmarshal([]byte(meta), &s.Attachment)
}

// Attachment returns metadata for the uploader, or for a participant of the
// message that carries it.
func (p *Provider) Attachment(ctx context.Context, caller, id string) (core.Attachment, error) {
	s, e := p.storedAttachment(ctx, p.db, id)
	if e != nil {
		return core.Attachment{}, e
	}
	if s.owner == caller {
		return s.Attachment, nil
	}
	if s.message != "" {
		if m, e := p.Get(ctx, s.message); e == nil && m.Status != core.MailHeld && (m.ToName == caller || m.FromName == caller) {
			return s.Attachment, nil
		}
	}
	return core.Attachment{}, core.ErrNotFound
}

// OpenAttachment opens the stored bytes for caller (see Attachment).
func (p *Provider) OpenAttachment(ctx context.Context, caller, id string) (core.Attachment, *os.File, error) {
	a, e := p.Attachment(ctx, caller, id)
	if e != nil {
		return a, nil, e
	}
	f, e := os.Open(filepath.Join(p.attachmentDir(), a.ID))
	if errors.Is(e, os.ErrNotExist) {
		return a, nil, core.ErrNotFound
	}
	return a, f, e
}

// resolveAttachments turns a sender's uploaded IDs into the objects that go
// into the signed payload.
func (p *Provider) resolveAttachments(ctx context.Context, from string, ids []string) ([]core.Attachment, error) {
	var out []core.Attachment
	for _, id := range ids {
		s, e := p.storedAttachment(ctx, p.db, id)
		if e != nil || s.owner != from {
			return nil, fmt.Errorf("%w: attachment %s not found", core.ErrInvalid, id)
		}
		out = append(out, s.Attachment)
	}
	return out, nil
}

// checkAttachments validates payload.attachments against the store before a
// message is accepted. Binding (one message per attachment) happens in the
// same transaction that stores the mail.
func (p *Provider) checkAttachments(ctx context.Context, from string, remote bool, list []core.Attachment) error {
	if len(list) == 0 {
		return nil
	}
	if remote {
		return fmt.Errorf("%w: files cannot be sent to another lolad yet", core.ErrInvalid)
	}
	if len(list) > MaxAttachments {
		return fmt.Errorf("%w: at most %d attachments per message", core.ErrInvalid, MaxAttachments)
	}
	seen := map[string]bool{}
	var total int64
	for _, a := range list {
		if a.Storage != "" && a.Storage != "provider" {
			return fmt.Errorf("%w: %s attachments are not supported", core.ErrInvalid, a.Storage)
		}
		s, e := p.storedAttachment(ctx, p.db, a.ID)
		if e != nil || s.owner != from || seen[a.ID] {
			return fmt.Errorf("%w: attachment %s not found", core.ErrInvalid, a.ID)
		}
		seen[a.ID] = true
		if s.message != "" {
			return fmt.Errorf("%w: attachment %s was already sent", core.ErrConflict, a.ID)
		}
		if a.Filename != s.Filename || a.Size != s.Size || a.Digest != s.Digest || a.ContentType != s.ContentType || a.ScanStatus != s.ScanStatus || a.URL != s.URL {
			return fmt.Errorf("%w: attachment %s does not match the upload", core.ErrInvalid, a.ID)
		}
		if s.ScanStatus == "rejected" {
			return fmt.Errorf("%w: attachment %s was rejected by the scan", core.ErrDenied, a.ID)
		}
		total += s.Size
	}
	if total > 2*p.localLimit() {
		return fmt.Errorf("%w: attachments total more than %d MB", errAttachmentTooLarge, (2*p.localLimit())>>20)
	}
	return nil
}

func bindAttachments(ctx context.Context, tx *sql.Tx, m core.Mail) error {
	for _, a := range m.Attachments {
		res, e := tx.ExecContext(ctx, `UPDATE amp_attachments SET message_id=? WHERE id=? AND owner=? AND message_id=''`, m.ID, a.ID, m.FromName)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: attachment %s was already sent", core.ErrConflict, a.ID)
		}
	}
	return nil
}

// pruneAttachments deletes expired files and uploads never sent within two
// hours. It runs at most every ten minutes, on upload.
func (p *Provider) pruneAttachments(ctx context.Context, now time.Time) {
	p.attachMu.Lock()
	if now.Sub(p.lastPrune) < 10*time.Minute {
		p.attachMu.Unlock()
		return
	}
	p.lastPrune = now
	p.attachMu.Unlock()
	rows, e := p.db.QueryContext(ctx, `SELECT id FROM amp_attachments WHERE expires_at<? OR (message_id='' AND created_at<?)`, now.UTC().Format(time.RFC3339Nano), now.Add(-unreferencedTTL).UTC().Format(time.RFC3339Nano))
	if e != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && attachmentID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if e := os.Remove(filepath.Join(p.attachmentDir(), id)); e == nil || errors.Is(e, os.ErrNotExist) {
			_, _ = p.db.ExecContext(ctx, `DELETE FROM amp_attachments WHERE id=?`, id)
		}
	}
}
