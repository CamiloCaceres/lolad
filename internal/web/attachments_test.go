package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// uploadSpy tells when the service starts reading an upload.
type uploadSpy struct {
	core.Service
	once    sync.Once
	started chan struct{}
}

func (u *uploadSpy) UploadAttachment(ctx context.Context, name, ct string, r io.Reader) (core.Attachment, error) {
	u.once.Do(func() { close(u.started) })
	return u.Service.UploadAttachment(ctx, name, ct, r)
}

func setupWith(t *testing.T, fo fake.Options, wo web.Options) *env {
	t.Helper()
	f := fake.New(fo)
	t.Cleanup(f.Close)
	sp := &spy{Service: f}
	wo.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(web.New(sp, wo))
	t.Cleanup(srv.Close)
	return &env{t: t, svc: sp, srv: srv}
}

func multipartFile(t *testing.T, name, contentType string, data []byte) (string, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", contentType)
	w, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	mw.Close()
	return mw.FormDataContentType(), &b
}

type uploaded struct {
	ID, Filename, ContentType, ScanStatus, Error string
	Size                                         int64
	Image                                        bool
}

func (e *env) upload(peer, name, contentType string, data []byte) (int, uploaded) {
	e.t.Helper()
	ct, body := multipartFile(e.t, name, contentType, data)
	resp, err := http.Post(e.srv.URL+"/inbox/"+peer+"/attachments", ct, body)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var u struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		ScanStatus  string `json:"scan_status"`
		Size        int64  `json:"size"`
		Image       bool   `json:"image"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		e.t.Fatalf("upload %s: decode: %v", name, err)
	}
	return resp.StatusCode, uploaded{ID: u.ID, Filename: u.Filename, ContentType: u.ContentType, ScanStatus: u.ScanStatus, Error: u.Error, Size: u.Size, Image: u.Image}
}

// A real PNG header, so the basic scan agrees with the name.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func TestUploadThenSendFilesWithoutText(t *testing.T) {
	e := setup(t)
	code, png := e.upload("forge", "screen shot.png", "image/png", pngBytes)
	if code != http.StatusOK || png.ID == "" || !png.Image || png.Filename != "screen shot.png" {
		t.Fatalf("upload png = %d %+v", code, png)
	}
	code, doc := e.upload("forge", "Página Web.zip", "application/zip", []byte("PK\x03\x04 not much of a zip"))
	if code != http.StatusOK || doc.Image {
		t.Fatalf("upload zip = %d %+v", code, doc)
	}

	resp, _ := e.do("POST", "/inbox/forge", url.Values{"body": {""}, "attachment": {png.ID, doc.ID, png.ID}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send = %d", resp.StatusCode)
	}
	sent := e.svc.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	out := sent[0]
	if strings.Join(out.Attachments, ",") != png.ID+","+doc.ID {
		t.Errorf("Outgoing.Attachments = %v (want both, deduplicated)", out.Attachments)
	}
	if out.Body != "Sent 2 files: screen shot.png, Página Web.zip" || out.Subject == "" || len([]rune(out.Subject)) > 256 {
		t.Errorf("derived body/subject = %q / %q", out.Body, out.Subject)
	}

	_, thread := e.get("/inbox/forge")
	if msgs := thread[strings.Index(thread, `id="msgs"`):]; strings.Contains(msgs, "Sent 2 files:") {
		t.Error("a message with only files should not repeat their names as text")
	}
	for _, want := range []string{
		`src="/inbox/forge/attachments/` + png.ID + `?inline=1"`,
		`href="/inbox/forge/attachments/` + doc.ID + `"`,
		`class="atts" hx-boost="false"`,
		"Página Web.zip",
	} {
		if !strings.Contains(thread, want) {
			t.Errorf("thread missing %q", want)
		}
	}
	// Each upload goes in one message only.
	resp, body := e.do("POST", "/inbox/forge", url.Values{"body": {"again"}, "attachment": {png.ID}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "already sent") {
		t.Errorf("resend = %d %q", resp.StatusCode, body)
	}
}

func TestSendTextWithFile(t *testing.T) {
	e := setup(t)
	_, a := e.upload("forge", "notes.txt", "text/plain", []byte("hello\n"))
	e.do("POST", "/inbox/forge", url.Values{"body": {"See the notes"}, "attachment": {a.ID}}, true)
	sent := e.svc.Sent()
	if len(sent) != 1 || sent[0].Body != "See the notes" || len(sent[0].Attachments) != 1 {
		t.Fatalf("sent = %+v", sent)
	}
	_, thread := e.get("/inbox/forge")
	if !strings.Contains(thread, "See the notes") || !strings.Contains(thread, "notes.txt") {
		t.Error("text and file should both show")
	}
}

func TestSendAtMostTenFiles(t *testing.T) {
	e := setup(t)
	form := url.Values{"body": {"many"}}
	for i := 0; i < 11; i++ {
		form.Add("attachment", fmt.Sprintf("att_1_%024d", i))
	}
	resp, body := e.do("POST", "/inbox/forge", form, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "at most 10 files") {
		t.Errorf("11 files = %d %q", resp.StatusCode, body)
	}
	resp, _ = e.do("POST", "/inbox/forge", url.Values{"body": {"x"}, "attachment": {"../etc/passwd"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad id = %d", resp.StatusCode)
	}
	if n := len(e.svc.Sent()); n != 0 {
		t.Errorf("sent %d messages", n)
	}
}

func TestUploadLimits(t *testing.T) {
	// The dashboard's own cap (MaxBytesReader) stops the body.
	e := setupWith(t, fake.Options{}, web.Options{MaxUpload: 1024})
	code, u := e.upload("forge", "big.bin", "application/octet-stream", bytes.Repeat([]byte("x"), 3<<20))
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(u.Error, "larger than") {
		t.Errorf("over the dashboard cap = %d %+v", code, u)
	}
	// The service's limit comes back as its message.
	e = setupWith(t, fake.Options{MaxAttachment: 2 << 20}, web.Options{})
	code, u = e.upload("forge", "big.bin", "application/octet-stream", bytes.Repeat([]byte("x"), 3<<20))
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(u.Error, "big.bin is larger than 2 MB") {
		t.Errorf("over the service limit = %d %+v", code, u)
	}
	code, u = e.upload("forge", "empty.txt", "text/plain", nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(u.Error, "empty") {
		t.Errorf("empty = %d %+v", code, u)
	}
	resp, err := http.Post(e.srv.URL+"/inbox/forge/attachments", "application/x-www-form-urlencoded", strings.NewReader("file=x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("not multipart = %d", resp.StatusCode)
	}
	code, u = e.upload("nobody", "a.txt", "text/plain", []byte("a"))
	if code != http.StatusNotFound {
		t.Errorf("unknown peer = %d %+v", code, u)
	}
	// Two files in one request.
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, n := range []string{"a.txt", "b.txt"} {
		w, _ := mw.CreateFormFile("file", n)
		w.Write([]byte(n))
	}
	mw.Close()
	resp, err = http.Post(e.srv.URL+"/inbox/forge/attachments", mw.FormDataContentType(), &b)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("two files = %d", resp.StatusCode)
	}
}

// The handler hands the file to the service while the body is still
// arriving: it streams instead of buffering the whole request.
func TestUploadStreams(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	us := &uploadSpy{Service: f, started: make(chan struct{})}
	srv := httptest.NewServer(web.New(us, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	done := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Post(srv.URL+"/inbox/forge/attachments", mw.FormDataContentType(), pr)
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	w, _ := mw.CreateFormFile("file", "stream.log")
	w.Write(bytes.Repeat([]byte("line\n"), 20000))
	select {
	case <-us.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not see the upload before the body ended")
	}
	w.Write([]byte("last line\n"))
	mw.Close()
	pw.Close()
	resp := <-done
	if resp == nil {
		return
	}
	defer resp.Body.Close()
	var u struct {
		Size int64 `json:"size"`
	}
	json.NewDecoder(resp.Body).Decode(&u)
	if resp.StatusCode != http.StatusOK || u.Size != 20000*5+10 {
		t.Errorf("stream upload = %d size %d", resp.StatusCode, u.Size)
	}
}

func seededAttachment(t *testing.T, e *env, peer, name string) core.Attachment {
	t.Helper()
	msgs, err := e.svc.Conversation(context.Background(), peer, 200, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		for _, a := range m.Attachments {
			if a.Filename == name {
				return a
			}
		}
	}
	t.Fatalf("no seeded %s with %s", name, peer)
	return core.Attachment{}
}

func TestRenderImagesFilesAndFlagged(t *testing.T) {
	e := setup(t)
	img := seededAttachment(t, e, "lola", "pricing-draft.png")
	zip := seededAttachment(t, e, "lola", "Página de precios.zip")
	_, body := e.get("/inbox/lola")
	if !strings.Contains(body, `<img src="/inbox/lola/attachments/`+img.ID+`?inline=1" alt="pricing-draft.png"`) {
		t.Error("clean PNG should render as an inline thumbnail")
	}
	if !strings.Contains(body, `<a class="att-file" href="/inbox/lola/attachments/`+zip.ID+`" download="Página de precios.zip">`) {
		t.Error("zip should render as a download row")
	}
	if !strings.Contains(body, "ZIP · 355 B") && !strings.Contains(body, "ZIP · ") {
		t.Error("file row needs type and size")
	}

	flagged := seededAttachment(t, e, "relay", "lockfile-report.pdf")
	_, body = e.get("/inbox/relay")
	if !strings.Contains(body, `class="att-file is-suspicious"`) || !strings.Contains(body, "data-confirm=") || !strings.Contains(body, "Flagged: contents") {
		t.Error("suspicious file needs a visible warning and a confirmation")
	}
	if strings.Contains(body, flagged.ID+"?inline=1") {
		t.Error("a suspicious file must not preview inline")
	}
	// Composer: attach controls, limits for the client.
	for _, want := range []string{`data-upload="/inbox/relay/attachments"`, `data-max-files="10"`, `data-max-label="500 MB"`, `aria-label="Attach files"`, `data-drop`} {
		if !strings.Contains(body, want) {
			t.Errorf("composer missing %q", want)
		}
	}
}

func TestRemotePeerCannotAttach(t *testing.T) {
	e := setup(t)
	peer := "jugui@jugui.lolad.local"
	_, body := e.get("/inbox/" + peer)
	if !strings.Contains(body, `data-attach-off="Files can&#39;t be sent to agents on another lolad yet."`) || !strings.Contains(body, `aria-disabled="true"`) {
		t.Error("remote conversation should disable attaching with a reason")
	}
	if strings.Contains(body, "data-attach-input") {
		t.Error("remote conversation should not offer a file picker")
	}
	code, u := e.upload(peer, "a.txt", "text/plain", []byte("a"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(u.Error, "another lolad") {
		t.Errorf("remote upload = %d %+v", code, u)
	}
	resp, msg := e.do("POST", "/inbox/"+peer, url.Values{"body": {"hi"}, "attachment": {"att_1_000000000000000000000000"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(msg, "another lolad") {
		t.Errorf("remote send with file = %d %q", resp.StatusCode, msg)
	}
}

func TestDownloadHeadersAndScope(t *testing.T) {
	e := setup(t)
	zip := seededAttachment(t, e, "lola", "Página de precios.zip")
	img := seededAttachment(t, e, "lola", "pricing-draft.png")
	flagged := seededAttachment(t, e, "relay", "lockfile-report.pdf")

	// From another conversation the file does not exist.
	if code, _ := e.get("/inbox/forge/attachments/" + zip.ID); code != http.StatusNotFound {
		t.Errorf("cross-conversation download = %d", code)
	}
	if code, _ := e.get("/inbox/lola/attachments/att_1_000000000000000000000000"); code != http.StatusNotFound {
		t.Errorf("unknown id = %d", code)
	}

	resp, body := e.do("GET", "/inbox/lola/attachments/"+zip.ID, nil, false)
	h := resp.Header
	if resp.StatusCode != http.StatusOK || int64(len(body)) != zip.Size {
		t.Fatalf("download = %d, %d bytes", resp.StatusCode, len(body))
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"Content-Type":            "application/octet-stream",
		"Content-Disposition":     `attachment; filename="P_gina de precios.zip"; filename*=UTF-8''P%C3%A1gina%20de%20precios.zip`,
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	resp, _ = e.do("GET", "/inbox/lola/attachments/"+img.ID+"?inline=1", nil, false)
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "image/png" || !strings.HasPrefix(cd, "inline;") {
		t.Errorf("inline image = %q %q", ct, cd)
	}
	resp, _ = e.do("GET", "/inbox/lola/attachments/"+img.ID, nil, false)
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("image without inline = %q", ct)
	}
	resp, _ = e.do("GET", "/inbox/relay/attachments/"+flagged.ID+"?inline=1", nil, false)
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "application/octet-stream" || !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("flagged file inline = %q %q", ct, cd)
	}

	req, _ := http.NewRequest("GET", e.srv.URL+"/inbox/lola/attachments/"+img.ID, nil)
	req.Header.Set("Range", "bytes=0-7")
	rr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	if rr.StatusCode != http.StatusPartialContent || string(b) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("range = %d %q", rr.StatusCode, b)
	}

	// An upload not sent yet reads back only where it was made.
	_, up := e.upload("forge", "draft.txt", "text/plain", []byte("draft"))
	if code, got := e.get("/inbox/forge/attachments/" + up.ID); code != http.StatusOK || got != "draft" {
		t.Errorf("own pending upload = %d %q", code, got)
	}
	if code, _ := e.get("/inbox/lola/attachments/" + up.ID); code != http.StatusNotFound {
		t.Errorf("pending upload from another conversation = %d", code)
	}
}

func TestUploadRefusesCrossOrigin(t *testing.T) {
	e := setup(t)
	ct, body := multipartFile(t, "a.txt", "text/plain", []byte("a"))
	req, _ := http.NewRequest("POST", e.srv.URL+"/inbox/forge/attachments", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site upload = %d", resp.StatusCode)
	}
}
