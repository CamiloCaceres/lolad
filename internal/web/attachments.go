package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Files in the inbox. The composer uploads each file on its own request to
// POST /inbox/{peer}/attachments and sends the returned IDs with the message;
// GET /inbox/{peer}/attachments/{id} downloads (or, for clean raster images,
// previews) a file on a message in that conversation.
const (
	// DefaultMaxUpload is the per-file cap the provider applies to local mail.
	DefaultMaxUpload = 500 << 20
	maxFiles         = 10 // per message (AMP spec 04)
	uploadSlack      = 1 << 20
	pendingTTL       = 2 * time.Hour // the provider deletes unsent uploads after this
	noRemoteFiles    = "Files can't be sent to agents on another lolad yet."
)

// inlineTypes are the image types the dashboard shows in the page. SVG is not
// one of them: it can carry script.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// pending remembers which conversation each upload was made in, so a file the
// user has not sent yet can only be read back from that conversation.
type pending struct {
	mu sync.Mutex
	m  map[string]pendingUpload
}

type pendingUpload struct {
	peer string
	att  core.Attachment
	at   time.Time
}

func (p *pending) add(peer string, a core.Attachment) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]pendingUpload{}
	}
	now := time.Now()
	for id, u := range p.m {
		if now.Sub(u.at) > pendingTTL {
			delete(p.m, id)
		}
	}
	p.m[a.ID] = pendingUpload{peer: peer, att: a, at: now}
}

func (p *pending) get(peer, id string) (core.Attachment, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.m[id]
	if !ok || u.peer != peer || time.Since(u.at) > pendingTTL {
		return core.Attachment{}, false
	}
	return u.att, true
}

func (p *pending) done(ids []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		delete(p.m, id)
	}
}

// attachBlocked says why the user can't send files to peer, "" when they can.
func (s *server) attachBlocked(ctx context.Context, peer string) (string, int) {
	if core.IsRemoteName(peer) {
		return noRemoteFiles, http.StatusUnprocessableEntity
	}
	if _, err := s.agentStatus(ctx, peer); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return "There is no agent called " + peer + ".", http.StatusNotFound
		}
		return err.Error(), statusFor(err)
	}
	if d := s.svc.Check(ctx, s.svc.Me().Name, peer); !d.Allow {
		return "The org policy doesn't let you message " + peer + " (" + d.Reason + ").", http.StatusForbidden
	}
	return "", 0
}

type uploadResult struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	SizeLabel   string `json:"size_label"`
	ContentType string `json:"content_type"`
	ScanStatus  string `json:"scan_status"`
	Image       bool   `json:"image"` // previewable inline
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func uploadFailed(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// upload stores one file from a multipart body (field "file"), streaming it
// to the service.
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	ctx, peer := r.Context(), r.PathValue("peer")
	if msg, code := s.attachBlocked(ctx, peer); msg != "" {
		uploadFailed(w, code, msg)
		return
	}
	// The daemon's ReadTimeout is for forms; a large file over a phone link
	// takes longer. Not every ResponseWriter supports deadlines (tests).
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Minute))
	limit := cmp.Or(s.maxUpload, DefaultMaxUpload)
	r.Body = http.MaxBytesReader(w, r.Body, limit+uploadSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		uploadFailed(w, http.StatusBadRequest, "Send the file as multipart/form-data.")
		return
	}
	var got *core.Attachment
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.uploadError(w, r, "", limit, err)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 64<<10))
			part.Close()
			continue
		}
		if got != nil {
			part.Close()
			uploadFailed(w, http.StatusUnprocessableEntity, "Upload one file per request.")
			return
		}
		name := part.FileName()
		a, err := s.svc.UploadAttachment(ctx, name, part.Header.Get("Content-Type"), part)
		part.Close()
		if err != nil {
			s.uploadError(w, r, name, limit, err)
			return
		}
		got = &a
	}
	if got == nil {
		uploadFailed(w, http.StatusBadRequest, "Choose a file to upload.")
		return
	}
	s.uploads.add(peer, *got)
	writeJSON(w, http.StatusOK, uploadResult{
		ID: got.ID, Filename: got.Filename, Size: got.Size, SizeLabel: bytesLabel(got.Size),
		ContentType: got.ContentType, ScanStatus: got.ScanStatus, Image: previewable(*got),
	})
}

// sizeLimit names a limit the way the provider's errors do: 500 MB.
func sizeLimit(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return bytesLabel(n)
}

func (s *server) uploadError(w http.ResponseWriter, r *http.Request, name string, limit int64, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		uploadFailed(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("%s is larger than %s.", cmp.Or(name, "The file"), sizeLimit(limit)))
	case errors.Is(err, core.ErrTooLarge):
		uploadFailed(w, http.StatusRequestEntityTooLarge, humanize(err, ""))
	default:
		code := statusFor(err)
		if code == http.StatusInternalServerError {
			s.log.Error("upload", "path", r.URL.Path, "err", err)
		}
		uploadFailed(w, code, humanize(err, ""))
	}
}

// attachmentIDs reads the uploads the composer sends with a message.
func attachmentIDs(r *http.Request) ([]string, error) {
	var ids []string
	for _, id := range r.Form["attachment"] {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(ids, id) {
			continue
		}
		if !strings.HasPrefix(id, "att_") || len(id) > 64 {
			return nil, fmt.Errorf("%w: %q is not an attachment", core.ErrInvalid, id)
		}
		ids = append(ids, id)
	}
	if len(ids) > maxFiles {
		return nil, fmt.Errorf("%w: attach at most %d files to one message", core.ErrInvalid, maxFiles)
	}
	return ids, nil
}

// filesBody is what a message with files and no text says: agents read it,
// and the dashboard hides it under the files themselves.
func filesBody(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "Sent a file: " + names[0]
	}
	return fmt.Sprintf("Sent %d files: %s", len(names), strings.Join(names, ", "))
}

func attachmentNames(list []core.Attachment) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Filename
	}
	return out
}

// download serves a file on a message in this conversation, or one the user
// uploaded here and has not sent yet.
func (s *server) download(w http.ResponseWriter, r *http.Request) {
	ctx, peer, id := r.Context(), r.PathValue("peer"), r.PathValue("id")
	if !s.inConversation(ctx, peer, id) {
		if _, ok := s.uploads.get(peer, id); !ok {
			http.Error(w, "no such file in this conversation", http.StatusNotFound)
			return
		}
	}
	a, rc, err := s.svc.OpenAttachment(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		http.Error(w, "this file is no longer available", http.StatusNotFound)
		return
	} else if err != nil {
		s.log.Error("download", "id", id, "err", err)
		http.Error(w, "could not open the file", statusFor(err))
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	if a.Digest != "" {
		h.Set("ETag", strconv.Quote(a.Digest))
	}
	if r.URL.Query().Get("inline") == "1" && previewable(a) {
		h.Set("Content-Type", a.ContentType)
		h.Set("Content-Disposition", contentDisposition("inline", a.Filename))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition("attachment", a.Filename))
	}
	if rs, ok := rc.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", a.UploadedAt, rs)
		return
	}
	if a.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(a.Size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, rc)
}

// inConversation reports whether a message between the user and peer
// carries the attachment, paging back through the whole conversation.
func (s *server) inConversation(ctx context.Context, peer, id string) bool {
	const page = 500
	var before time.Time
	for {
		msgs, err := s.svc.Conversation(ctx, peer, page, before)
		if err != nil || len(msgs) == 0 {
			return false
		}
		for _, m := range msgs {
			for _, a := range m.Attachments {
				if a.ID == id {
					return true
				}
			}
		}
		oldest := msgs[0].CreatedAt
		if len(msgs) < page || (!before.IsZero() && !oldest.Before(before)) {
			return false
		}
		before = oldest
	}
}

// previewable: a raster image that passed the basic scan.
func previewable(a core.Attachment) bool {
	return inlineTypes[strings.ToLower(a.ContentType)] && a.ScanStatus == "basic_clean"
}

// contentDisposition builds `kind; filename="ascii"; filename*=UTF-8”…`
// (RFC 6266 / 5987) so Unicode names survive and old clients get a fallback.
func contentDisposition(kind, name string) string {
	var ascii, ext strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			ascii.WriteByte('_')
		} else {
			ascii.WriteRune(r)
		}
	}
	const attr = "!#$&+-.^_`|~"
	for _, b := range []byte(name) {
		if b < 0x80 && (b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte(attr, b) >= 0) {
			ext.WriteByte(b)
		} else {
			fmt.Fprintf(&ext, "%%%02X", b)
		}
	}
	return kind + `; filename="` + ascii.String() + `"; filename*=UTF-8''` + ext.String()
}

// attView is an attachment as a message shows it.
type attView struct {
	core.Attachment
	Peer string
}

func (a attView) Href() string     { return "/inbox/" + a.Peer + "/attachments/" + a.ID }
func (a attView) Inline() bool     { return previewable(a.Attachment) }
func (a attView) Suspicious() bool { return a.ScanStatus == "suspicious" }

// Kind picks the file's icon.
func (a attView) Kind() string {
	ct := strings.ToLower(a.ContentType)
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(a.Filename), "."))
	switch {
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.HasPrefix(ct, "video/"), strings.HasPrefix(ct, "audio/"):
		return "media"
	case ct == "application/zip", ct == "application/gzip", ct == "application/x-gzip", ct == "application/x-tar",
		ext == "zip", ext == "gz", ext == "tgz", ext == "tar", ext == "7z", ext == "rar":
		return "archive"
	case strings.HasPrefix(ct, "text/"), ct == "application/json", ct == "application/pdf",
		ext == "md", ext == "csv", ext == "txt", ext == "log", ext == "json", ext == "pdf":
		return "doc"
	}
	return "blank"
}

// Ext is a short type label: PDF, CSV, ZIP.
func (a attView) Ext() string {
	ext := strings.TrimPrefix(path.Ext(a.Filename), ".")
	if ext == "" || len(ext) > 5 {
		return ""
	}
	return strings.ToUpper(ext)
}

func attViews(peer string, list []core.Attachment) []attView {
	out := make([]attView, len(list))
	for i, a := range list {
		out[i] = attView{Attachment: a, Peer: peer}
	}
	return out
}
