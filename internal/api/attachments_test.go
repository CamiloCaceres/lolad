package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/core"
)

// attachmentsFake is a Service that stores uploads on disk like the provider:
// streamed in, capped, digested, and readable only while unsent or carried by
// mail. It is deliberately small; the real rules live in internal/amp.
type attachmentsFake struct {
	core.Service
	mu      sync.Mutex
	dir     string
	limit   int64
	allow   bool
	why     string
	scan    string // scan status for new uploads; "" = basic_clean
	sink    bool   // hash the bytes but store nothing (streaming test; no disk)
	agents  map[string]bool
	files   map[string]core.Attachment
	message map[string]string // attachment ID -> message that carries it
	msgs    []core.Mail
	sent    []core.Outgoing
}

func newAttachmentsFake(t *testing.T) *attachmentsFake {
	t.Helper()
	return &attachmentsFake{
		dir: t.TempDir(), limit: defaultMaxUpload, allow: true, why: "rule 1",
		agents: map[string]bool{"iris": true, "zed": true},
		files:  map[string]core.Attachment{}, message: map[string]string{},
	}
}

func (f *attachmentsFake) Me() core.User { return core.User{Name: "milo", Display: "Milo"} }

func (f *attachmentsFake) Get(_ context.Context, name string) (core.AgentStatus, error) {
	if !f.agents[name] {
		return core.AgentStatus{}, core.ErrNotFound
	}
	return core.AgentStatus{Agent: core.Agent{Name: name}}, nil
}

func (f *attachmentsFake) Check(context.Context, string, string) core.Decision {
	if f.allow {
		return core.Decision{Allow: true}
	}
	return core.Decision{Allow: false, Reason: f.why}
}

func (f *attachmentsFake) UploadAttachment(_ context.Context, filename, contentType string, r io.Reader) (core.Attachment, error) {
	sum := sha256.New()
	var tmp *os.File
	if !f.sink {
		file, err := os.CreateTemp(f.dir, ".upload-*")
		if err != nil {
			return core.Attachment{}, err
		}
		tmp = file
		defer os.Remove(tmp.Name())
		defer tmp.Close()
	}
	var dst io.Writer = sum
	if tmp != nil {
		dst = io.MultiWriter(tmp, sum)
	}
	n, err := io.Copy(dst, io.LimitReader(r, f.limit+1))
	if err != nil {
		return core.Attachment{}, err
	}
	if n > f.limit {
		return core.Attachment{}, fmt.Errorf("attachment %w: %s is larger than %s", core.ErrTooLarge, filename, sizeLabel(f.limit))
	}
	if n == 0 {
		return core.Attachment{}, fmt.Errorf("%w: %s is empty", core.ErrInvalid, filename)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("att_%d_%06d", time.Now().Unix(), len(f.files))
	if tmp != nil {
		if err := tmp.Close(); err != nil {
			return core.Attachment{}, err
		}
		if err := os.Rename(tmp.Name(), filepath.Join(f.dir, id)); err != nil {
			return core.Attachment{}, err
		}
	}
	now := time.Now().UTC()
	a := core.Attachment{
		Storage: "provider", ID: id, Filename: filename, ContentType: contentType, Size: n,
		Digest: "sha256:" + hex.EncodeToString(sum.Sum(nil)), URL: "/v1/attachments/" + id + "/download",
		ScanStatus: "basic_clean", UploadedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if f.scan != "" {
		a.ScanStatus = f.scan
	}
	f.files[id] = a
	f.message[id] = ""
	return a, nil
}

func (f *attachmentsFake) OpenAttachment(_ context.Context, id string) (core.Attachment, io.ReadCloser, error) {
	f.mu.Lock()
	a, ok := f.files[id]
	f.mu.Unlock()
	if !ok {
		return core.Attachment{}, nil, core.ErrNotFound
	}
	file, err := os.Open(filepath.Join(f.dir, id))
	if err != nil {
		return core.Attachment{}, nil, core.ErrNotFound
	}
	return a, file, nil
}

func (f *attachmentsFake) Conversation(_ context.Context, peer string, _ int, _ time.Time) ([]core.Mail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Mail
	for _, m := range f.msgs {
		if m.FromName == peer || m.ToName == peer {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *attachmentsFake) UserSend(_ context.Context, out core.Outgoing) (core.Mail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if out.Subject == "" || len(out.Subject) > 256 {
		return core.Mail{}, fmt.Errorf("%w: subject must have 1–256 characters", core.ErrInvalid)
	}
	if len(out.Attachments) > maxFiles {
		return core.Mail{}, fmt.Errorf("%w: at most %d attachments per message", core.ErrInvalid, maxFiles)
	}
	var list []core.Attachment
	for _, id := range out.Attachments {
		a, ok := f.files[id]
		if !ok || f.message[id] != "" {
			return core.Mail{}, fmt.Errorf("%w: attachment %s not found", core.ErrInvalid, id)
		}
		list = append(list, a)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("msg_test_%d", len(f.msgs)+1)
	m := core.Mail{
		ID: id, ThreadID: id, From: "milo@test.lolad.local", To: out.To + "@test.lolad.local",
		FromName: "milo", ToName: out.To, Subject: out.Subject, Priority: "normal", Type: out.Type,
		Body: out.Body, Signed: true, Verified: true, CreatedAt: now, Status: core.MailUnread, Attachments: list,
	}
	for _, a := range list {
		f.message[a.ID] = id
	}
	f.msgs = append(f.msgs, m)
	f.sent = append(f.sent, out)
	return m, nil
}

// ---------------------------------------------------------------- helpers

func multipartFile(t *testing.T, field, filename, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &b, mw.FormDataContentType()
}

func do(h http.Handler, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func uploadFile(t *testing.T, h http.Handler, peer, filename, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, formType := multipartFile(t, "file", filename, contentType, data)
	return do(h, "POST", "/api/mail/attachments?peer="+peer, formType, body)
}

func needUpload(t *testing.T, w *httptest.ResponseRecorder) core.Attachment {
	t.Helper()
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var a core.Attachment
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatalf("upload body %q: %v", w.Body, err)
	}
	return a
}

func send(t *testing.T, h http.Handler, to, subject, body string, ids []string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]any{"to": to, "subject": subject, "body": body, "type": "request", "attachments": ids})
	if err != nil {
		t.Fatal(err)
	}
	return do(h, "POST", "/api/mail/send", "application/json", bytes.NewReader(b))
}

// ---------------------------------------------------------------- tests

func TestAttachmentUploadSendDownload(t *testing.T) {
	f := newAttachmentsFake(t)
	h := New(f, Options{})
	png := []byte("\x89PNG\r\n\x1a\nreceipt bytes")

	w := uploadFile(t, h, "iris", "Receipt ✓.png", "image/png", png)
	a := needUpload(t, w)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("upload response cacheable")
	}
	sum := sha256.Sum256(png)
	switch {
	case !strings.HasPrefix(a.ID, "att_"), a.Filename != "Receipt ✓.png", a.ContentType != "image/png":
		t.Fatalf("attachment %+v", a)
	case a.Size != int64(len(png)), a.Digest != "sha256:"+hex.EncodeToString(sum[:]):
		t.Fatalf("size/digest %+v", a)
	case a.ScanStatus != "basic_clean", a.UploadedAt.IsZero(), !a.ExpiresAt.After(a.UploadedAt):
		t.Fatalf("scan/timestamps %+v", a)
	}
	for _, field := range []string{"id", "filename", "content_type", "size", "digest", "scan_status", "uploaded_at", "expires_at"} {
		if !strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Fatalf("upload JSON lacks %s: %s", field, w.Body)
		}
	}

	// A message with a file and no text is allowed.
	w = send(t, h, "iris", "Receipt", "", []string{a.ID})
	if w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	var m core.Mail
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ID != a.ID || m.Body != "" {
		t.Fatalf("sent mail %+v", m)
	}

	w = do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=iris", "", nil)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("download: %d %s", w.Code, w.Body)
	}
	hd := w.Header()
	if got := hd.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment; ") || !strings.Contains(got, `filename="Receipt _.png"`) {
		t.Fatalf("disposition %q", got)
	}
	if hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download headers %v", hd)
	}
	if hd.Get("ETag") != `"`+a.Digest+`"` || hd.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("digest/range headers %v", hd)
	}

	// Range requests work because the reader is a file.
	req := httptest.NewRequest("GET", "/api/mail/attachments/"+a.ID+"?peer=iris", nil)
	req.Header.Set("Range", "bytes=0-3")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), png[:4]) {
		t.Fatalf("range: %d %q", w.Code, w.Body)
	}

	w = do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=iris&inline=1", "", nil)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("inline download: %d", w.Code)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline; ") {
		t.Fatalf("inline disposition %q", got)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("inline type %q", w.Header().Get("Content-Type"))
	}
}

func TestAttachmentUploadPolicy(t *testing.T) {
	f := newAttachmentsFake(t)
	h := New(f, Options{})
	data := []byte("hello")

	f.allow = false
	if w := uploadFile(t, h, "iris", "a.txt", "text/plain", data); w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "denied by policy") {
		t.Fatalf("policy denial: %d %s", w.Code, w.Body)
	}
	f.allow = true

	for _, tc := range []struct {
		peer   string
		status int
	}{
		{"ghost", http.StatusNotFound},
		{"iris@other.lolad.local", http.StatusUnprocessableEntity},
		{"", http.StatusUnprocessableEntity},
	} {
		if w := uploadFile(t, h, tc.peer, "a.txt", "text/plain", data); w.Code != tc.status {
			t.Fatalf("peer %q: %d %s", tc.peer, w.Code, w.Body)
		}
	}

	// Shape problems are invalid requests, and nothing is stored.
	body, formType := multipartFile(t, "upload", "a.txt", "text/plain", data)
	if w := do(h, "POST", "/api/mail/attachments?peer=iris", formType, body); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong part name: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/mail/attachments?peer=iris", "application/json", strings.NewReader(`{}`)); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("not multipart: %d %s", w.Code, w.Body)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, name := range []string{"one.txt", "two.txt"} {
		p, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Write(data)
	}
	_ = mw.Close()
	if w := do(h, "POST", "/api/mail/attachments?peer=iris", mw.FormDataContentType(), &b); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("two files: %d %s", w.Code, w.Body)
	}
	// Empty files are invalid, as the provider reports them.
	if w := uploadFile(t, h, "iris", "empty.txt", "text/plain", nil); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty file: %d %s", w.Code, w.Body)
	}
}

func TestAttachmentUploadTooLarge(t *testing.T) {
	f := newAttachmentsFake(t)
	f.limit = 1 << 10
	h := New(f, Options{})
	w := uploadFile(t, h, "iris", "big.bin", "application/octet-stream", bytes.Repeat([]byte("x"), 2<<10))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over limit: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "too large") {
		t.Fatalf("message %s", w.Body)
	}
}

// TestAttachmentUploadBackstop covers a service that reads past the cap: the
// request body reader still stops the upload at the API's own limit.
func TestAttachmentUploadBackstop(t *testing.T) {
	old := maxUpload
	maxUpload = 1 << 10
	t.Cleanup(func() { maxUpload = old })

	f := newAttachmentsFake(t)
	f.limit = 4 << 30
	h := New(f, Options{})
	body, formType := multipartFile(t, "file", "big.bin", "application/octet-stream", bytes.Repeat([]byte("x"), 1<<20+(1<<10)))
	if w := do(h, "POST", "/api/mail/attachments?peer=iris", formType, body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("backstop: %d %s", w.Code, w.Body)
	}
}

func TestSendAttachmentLimits(t *testing.T) {
	f := newAttachmentsFake(t)
	h := New(f, Options{})

	ids := make([]string, maxFiles+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("att_1_%06d", i)
	}
	if w := send(t, h, "iris", "Too many", "hi", ids); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("too many: %d %s", w.Code, w.Body)
	}
	if w := send(t, h, "iris", "Bogus", "hi", []string{"not-an-attachment"}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bogus id: %d %s", w.Code, w.Body)
	}
	if w := send(t, h, "iris", "Unknown", "hi", []string{"att_1_abcdef"}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown id: %d %s", w.Code, w.Body)
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %d messages", len(f.sent))
	}
}

func TestAttachmentReadableOnlyInItsConversation(t *testing.T) {
	f := newAttachmentsFake(t)
	h := New(f, Options{})
	a := needUpload(t, uploadFile(t, h, "iris", "notes.md", "text/markdown", []byte("# hi")))

	// Unsent: readable from the conversation it was uploaded in, nowhere else.
	if w := do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=zed", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unsent, other peer: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=iris", "", nil); w.Code != http.StatusOK {
		t.Fatalf("unsent, own peer: %d %s", w.Code, w.Body)
	}

	// Sent to zed instead: the iris copy is gone, zed can read it.
	if w := send(t, h, "zed", "Notes", "", []string{a.ID}); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=iris", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("sent elsewhere: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=zed", "", nil); w.Code != http.StatusOK {
		t.Fatalf("recipient conversation: %d %s", w.Code, w.Body)
	}

	// Mail the operator received carries attachments the client can fetch.
	f.mu.Lock()
	incoming := core.Attachment{ID: "att_9_000001", Filename: "from-iris.txt", ContentType: "text/plain", Size: 2, Digest: "sha256:x", ScanStatus: "basic_clean", UploadedAt: time.Now().UTC()}
	f.files[incoming.ID] = incoming
	os.WriteFile(filepath.Join(f.dir, incoming.ID), []byte("ok"), 0o600)
	f.msgs = append(f.msgs, core.Mail{ID: "msg_in", FromName: "iris", ToName: "milo", CreatedAt: time.Now().UTC(), Attachments: []core.Attachment{incoming}})
	f.mu.Unlock()
	if w := do(h, "GET", "/api/mail/attachments/"+incoming.ID+"?peer=iris", "", nil); w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("received attachment: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/mail/attachments/"+incoming.ID+"?peer=zed", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("received attachment, other peer: %d %s", w.Code, w.Body)
	}

	// Unknown attachment IDs are 404, never a 500.
	if w := do(h, "GET", "/api/mail/attachments/att_1_nope?peer=iris", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/mail/attachments/"+a.ID, "", nil); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no peer: %d %s", w.Code, w.Body)
	}
}

func TestAttachmentInlineOnlyForCleanRaster(t *testing.T) {
	cases := []struct {
		name, filename, contentType, scan string
		inline                            bool
	}{
		{"png", "shot.png", "image/png", "basic_clean", true},
		{"jpeg", "shot.jpg", "image/jpeg", "basic_clean", true},
		{"gif", "shot.gif", "image/gif", "basic_clean", true},
		{"webp", "shot.webp", "image/webp", "basic_clean", true},
		{"svg", "logo.svg", "image/svg+xml", "basic_clean", false},
		{"text", "notes.txt", "text/plain", "basic_clean", false},
		{"pdf", "doc.pdf", "application/pdf", "basic_clean", false},
		{"suspicious png", "weird.png", "image/png", "suspicious", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttachmentsFake(t)
			f.scan = tc.scan
			h := New(f, Options{})
			a := needUpload(t, uploadFile(t, h, "iris", tc.filename, tc.contentType, []byte("payload")))
			if w := send(t, h, "iris", "File", "", []string{a.ID}); w.Code != http.StatusOK {
				t.Fatalf("send: %d %s", w.Code, w.Body)
			}
			w := do(h, "GET", "/api/mail/attachments/"+a.ID+"?peer=iris&inline=1", "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("download: %d %s", w.Code, w.Body)
			}
			inline := strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline; ")
			if inline != tc.inline {
				t.Fatalf("inline=%v, disposition %q", inline, w.Header().Get("Content-Disposition"))
			}
			want := "application/octet-stream"
			if tc.inline {
				want = tc.contentType
			}
			if w.Header().Get("Content-Type") != want {
				t.Fatalf("type %q, want %q", w.Header().Get("Content-Type"), want)
			}
		})
	}
}

func TestAttachmentRoutesRequireAuth(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Remote(New(newAttachmentsFake(t), Options{Auth: a}), nil)

	for _, c := range []struct{ method, path string }{
		{"POST", "/api/mail/attachments?peer=iris"},
		{"GET", "/api/mail/attachments/att_1_abcdef?peer=iris"},
	} {
		w := do(h, c.method, c.path, "", nil)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "authentication required") {
			t.Fatalf("%s %s without token: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}

	// A paired device reaches the handler: 404 for a missing file, not 401.
	p, err := a.Pair(t.Context(), "phone")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Redeem(t.Context(), p.Code, "phone", "android", "192.0.2.1", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/mail/attachments/att_1_abcdef?peer=iris", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("authenticated download: %d %s", w.Code, w.Body)
	}
}

// TestAttachmentUploadStreams pushes a multi-hundred-MB file through the
// handler from a generated reader and checks the daemon never held it: a
// buffering implementation would allocate and keep the whole file.
func TestAttachmentUploadStreams(t *testing.T) {
	const size = 256 << 20
	f := newAttachmentsFake(t)
	f.sink = true // no disk: this machine may be nearly full, and the point is memory
	h := New(f, Options{})

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", "huge.bin")
		if err == nil {
			_, err = io.Copy(part, io.LimitReader(zeroReader{}, size))
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()

	req := httptest.NewRequest("POST", "/api/mail/attachments?peer=iris", nil)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	spy := &readSpy{r: pr}
	req.Body = io.NopCloser(spy)
	w := httptest.NewRecorder()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	h.ServeHTTP(w, req)
	runtime.GC()
	runtime.ReadMemStats(&after)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %.300s", w.Code, w.Body)
	}
	var a core.Attachment
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Size != size {
		t.Fatalf("stored %d bytes, want %d", a.Size, size)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Fatalf("allocated %d MB for a %d MB upload: the file was buffered", grown>>20, size>>20)
	}
	if spy.max > 1<<20 {
		t.Fatalf("read the request body in %d byte chunks", spy.max)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// readSpy records the largest read the handler asked the request body for.
type readSpy struct {
	r   io.Reader
	max int
}

func (s *readSpy) Read(p []byte) (int, error) {
	if len(p) > s.max {
		s.max = len(p)
	}
	return s.r.Read(p)
}
