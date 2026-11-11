package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Files the operator sends from a native client. Each file goes up on its own
// request to POST /api/mail/attachments?peer=<agent>; the returned IDs travel
// in Outgoing.Attachments. GET /api/mail/attachments/{id}?peer=<agent>
// downloads (or, for clean raster images, previews) a file on a message in
// that conversation. The rules match the dashboard's (internal/web).
const (
	defaultMaxUpload = int64(500 << 20) // per file, as DefaultMaxUpload in the dashboard
	maxFiles         = 10               // per message (AMP spec 04)
	uploadSlack      = 1 << 20          // multipart framing; MaxBytesReader is a backstop
	uploadTimeout    = 30 * time.Minute // a large file over a phone link outlasts ReadTimeout
	pendingTTL       = 2 * time.Hour    // the provider deletes unsent uploads after this
	noRemoteFiles    = "files cannot be sent to another lolad yet"
)

// maxUpload is the per-file cap the API enforces; a variable so tests can
// shrink it instead of pushing half a gigabyte to exercise 413.
var maxUpload = defaultMaxUpload

// inlineTypes are the image types a client may preview in place. SVG is not
// one of them: it can carry script.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// attachmentState remembers which conversation each upload was made in, so a
// file the operator has not sent yet can only be downloaded from that
// conversation. core.Attachment does not record its peer; the dashboard keeps
// the same map in its own server.
type attachmentState struct {
	mu sync.Mutex
	m  map[string]pendingUpload
}

type pendingUpload struct {
	peer string
	at   time.Time
}

func (st *attachmentState) add(peer, id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.m == nil {
		st.m = map[string]pendingUpload{}
	}
	now := time.Now()
	for k, u := range st.m {
		if now.Sub(u.at) > pendingTTL {
			delete(st.m, k)
		}
	}
	st.m[id] = pendingUpload{peer: peer, at: now}
}

func (st *attachmentState) has(peer, id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	u, ok := st.m[id]
	return ok && u.peer == peer && time.Since(u.at) <= pendingTTL
}

// done forgets uploads that went out in a message: from then on they are
// readable only from the conversation that carries them.
func (st *attachmentState) done(ids []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, id := range ids {
		delete(st.m, id)
	}
}

func attachmentRoutes(m *http.ServeMux, s core.Service) *attachmentState {
	st := &attachmentState{}
	m.HandleFunc("POST /api/mail/attachments", func(w http.ResponseWriter, r *http.Request) { st.upload(w, r, s) })
	m.HandleFunc("GET /api/mail/attachments/{id}", func(w http.ResponseWriter, r *http.Request) { st.download(w, r, s) })
	return st
}

// attachBlocked says why the operator can't send files to peer, nil when they
// can. Same gate as the dashboard: local agents only, the agent exists, and
// org policy allows the operator to message it.
func attachBlocked(ctx context.Context, s core.Service, peer string) error {
	if peer == "" {
		return fmt.Errorf("%w: peer is required", core.ErrInvalid)
	}
	if core.IsRemoteName(peer) {
		return fmt.Errorf("%w: %s", core.ErrInvalid, noRemoteFiles)
	}
	if _, err := s.Get(ctx, peer); err != nil {
		return err
	}
	if d := s.Check(ctx, s.Me().Name, peer); !d.Allow {
		return fmt.Errorf("%w: the org policy does not allow messaging %s (%s)", core.ErrDenied, peer, d.Reason)
	}
	return nil
}

// checkSendAttachments validates the IDs a send request carries before they
// reach the service, which re-checks ownership and the per-message cap.
func checkSendAttachments(ids []string) error {
	if len(ids) > maxFiles {
		return fmt.Errorf("%w: attach at most %d files to one message", core.ErrInvalid, maxFiles)
	}
	for _, id := range ids {
		if !strings.HasPrefix(id, "att_") || len(id) > 64 {
			return fmt.Errorf("%w: %q is not an attachment", core.ErrInvalid, id)
		}
	}
	return nil
}

// upload stores one file from a multipart body (part "file"), streaming it to
// the service: the request is never read into memory whole.
func (st *attachmentState) upload(w http.ResponseWriter, r *http.Request, s core.Service) {
	ctx, peer := r.Context(), strings.TrimSpace(r.URL.Query().Get("peer"))
	if err := attachBlocked(ctx, s, peer); err != nil {
		write(w, nil, err)
		return
	}
	// The server's ReadTimeout is sized for JSON; a large file on a phone link
	// takes longer. Not every ResponseWriter supports deadlines (tests).
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadTimeout))
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+uploadSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		write(w, nil, fmt.Errorf("%w: send one file as multipart/form-data in a part named file", core.ErrInvalid))
		return
	}
	var got *core.Attachment
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			write(w, nil, uploadFailure(err))
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 64<<10))
			part.Close()
			continue
		}
		if got != nil {
			part.Close()
			write(w, nil, fmt.Errorf("%w: upload one file per request", core.ErrInvalid))
			return
		}
		a, err := s.UploadAttachment(ctx, part.FileName(), part.Header.Get("Content-Type"), part)
		part.Close()
		if err != nil {
			write(w, nil, uploadFailure(err))
			return
		}
		got = &a
	}
	if got == nil {
		write(w, nil, fmt.Errorf("%w: add a file in the file part", core.ErrInvalid))
		return
	}
	st.add(peer, got.ID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(got)
}

// uploadFailure names a limit the way the provider's errors do: 500 MB.
func uploadFailure(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return fmt.Errorf("%w: the file is larger than %s", core.ErrTooLarge, sizeLabel(maxUpload))
	}
	return err
}

func sizeLabel(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// download serves a file on a message in this conversation, or one the
// operator uploaded here and has not sent yet. Nothing else is readable.
func (st *attachmentState) download(w http.ResponseWriter, r *http.Request, s core.Service) {
	ctx, id := r.Context(), r.PathValue("id")
	peer := strings.TrimSpace(r.URL.Query().Get("peer"))
	if peer == "" || core.IsRemoteName(peer) {
		write(w, nil, fmt.Errorf("%w: peer is required", core.ErrInvalid))
		return
	}
	if !st.has(peer, id) && !inConversation(ctx, s, peer, id) {
		write(w, nil, core.ErrNotFound)
		return
	}
	a, rc, err := s.OpenAttachment(ctx, id)
	if err != nil {
		write(w, nil, err)
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
	if r.URL.Query().Get("inline") == "1" && inlineable(a) {
		h.Set("Content-Type", a.ContentType)
		h.Set("Content-Disposition", contentDisposition("inline", a.Filename))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition("attachment", a.Filename))
	}
	// A *os.File serves Range, If-Range and HEAD without buffering the file.
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

// inConversation reports whether a message between the operator and peer
// carries the attachment, paging back through the whole conversation.
func inConversation(ctx context.Context, s core.Service, peer, id string) bool {
	const page = 500
	var before time.Time
	for {
		msgs, err := s.Conversation(ctx, peer, page, before)
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

// inlineable: a raster image that passed the basic scan, the dashboard's rule.
func inlineable(a core.Attachment) bool {
	return inlineTypes[strings.ToLower(a.ContentType)] && a.ScanStatus == "basic_clean"
}

// contentDisposition builds `kind; filename="ascii"; filename*=UTF-8”…`
// (RFC 6266 / 5987) so Unicode names survive and old clients get a fallback.
func contentDisposition(kind, name string) string {
	var ascii, escaped strings.Builder
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
			escaped.WriteByte(b)
		} else {
			fmt.Fprintf(&escaped, "%%%02X", b)
		}
	}
	return kind + `; filename="` + ascii.String() + `"; filename*=UTF-8''` + escaped.String()
}
