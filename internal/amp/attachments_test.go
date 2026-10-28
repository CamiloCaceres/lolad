package amp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)

func TestAttachmentSendAndAccess(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	a, e := f.p.UploadAttachment(ctx, "milo", "../Página Web.png", "", bytes.NewReader(pngBytes))
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(pngBytes)
	if a.Filename != "Página Web.png" || a.ContentType != "image/png" || a.ScanStatus != "basic_clean" || a.Size != int64(len(pngBytes)) || a.Digest != "sha256:"+hex.EncodeToString(sum[:]) || a.URL != "/v1/attachments/"+a.ID+"/download" {
		t.Fatalf("%+v", a)
	}
	if a.ExpiresAt.Sub(a.UploadedAt) < 7*24*time.Hour {
		t.Fatal("spec: expires at least 7 days after upload")
	}
	if _, e = f.p.Attachment(ctx, "a", a.ID); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("unsent upload visible to others")
	}
	m, e := f.p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "logo", Body: "here", Attachments: []string{a.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Digest != a.Digest {
		t.Fatalf("mail lost its attachment: %+v", m.Attachments)
	}
	got, e := f.p.Get(ctx, m.ID)
	if e != nil || len(got.Attachments) != 1 {
		t.Fatalf("stored mail: %+v %v", got.Attachments, e)
	}
	// The attachment is inside the signed payload.
	w, e := f.p.Wire(ctx, m.ID)
	if e != nil || !bytes.Contains(w.Payload, []byte(a.Digest)) {
		t.Fatalf("payload does not carry the attachment: %s", w.Payload)
	}
	if _, file, e := f.p.OpenAttachment(ctx, "a", a.ID); e != nil {
		t.Fatal("recipient cannot open", e)
	} else {
		b, _ := io.ReadAll(file)
		file.Close()
		if !bytes.Equal(b, pngBytes) {
			t.Fatal("bytes changed")
		}
	}
	if _, e = f.p.Attachment(ctx, "b", a.ID); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("bystander can read the attachment")
	}
	if _, e = f.p.Send(ctx, "milo", core.Outgoing{To: "b", Subject: "again", Body: "x", Attachments: []string{a.ID}}); e == nil {
		t.Fatal("spec: an attachment ID belongs to one message")
	}
	if _, e = f.p.Send(ctx, "a", core.Outgoing{To: "b", Subject: "steal", Body: "x", Attachments: []string{a.ID}}); e == nil {
		t.Fatal("sent someone else's upload")
	}
	if _, e = f.p.Send(ctx, "milo", core.Outgoing{To: "a@other.example", Subject: "x", Body: "x"}); e == nil {
		// federation is off in this fixture; just make sure the attachment rule is explicit
	}
	b2, _ := f.p.UploadAttachment(ctx, "milo", "x.png", "", bytes.NewReader(pngBytes))
	if e = f.p.checkAttachments(ctx, "milo", true, []core.Attachment{b2}); e == nil || !strings.Contains(e.Error(), "another lolad") {
		t.Fatalf("federated attachments must be refused for now: %v", e)
	}
}

func TestAttachmentChecks(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	if a, _ := f.p.UploadAttachment(ctx, "milo", "photo.png", "image/png", strings.NewReader("MZ\x90\x00 not an image")); a.ScanStatus != "suspicious" {
		t.Fatalf("magic bytes contradict the type: %+v", a)
	}
	if a, _ := f.p.UploadAttachment(ctx, "milo", "notes.md", "", strings.NewReader("# hi\n")); a.ScanStatus != "basic_clean" || !strings.HasPrefix(a.ContentType, "text/") {
		t.Fatalf("%+v", a)
	}
	for in, want := range map[string]string{"a/b\\c.txt": "c.txt", "..%2f..%2fetc": "_.._etc", "con.txt": "_con.txt", "\x00\x07": "file", "  spaced   name .pdf ": "spaced name .pdf"} {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	small := setupWith(t, WithLocalAttachmentLimit(10))
	if _, e := small.p.UploadAttachment(ctx, "milo", "big.bin", "", strings.NewReader(strings.Repeat("x", 11))); !errors.Is(e, errAttachmentTooLarge) {
		t.Fatalf("limit: %v", e)
	}
	if _, e := small.p.UploadAttachment(ctx, "milo", "empty.txt", "", strings.NewReader("")); e == nil {
		t.Fatal("empty upload accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(small.state, "attachments"))
	for _, en := range entries {
		t.Fatalf("rejected upload left %s behind", en.Name())
	}
}

func TestAttachmentPayloadMustMatchUpload(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	a, _ := f.p.UploadAttachment(ctx, "a", "doc.txt", "", strings.NewReader("hello"))
	forged := a
	forged.Size = 1
	if e := f.p.checkAttachments(ctx, "a", false, []core.Attachment{forged}); e == nil {
		t.Fatal("payload metadata must match the stored file")
	}
	if e := f.p.checkAttachments(ctx, "a", false, make([]core.Attachment, MaxAttachments+1)); e == nil {
		t.Fatal("more than 10 attachments")
	}
}

func TestAttachmentPrune(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	kept, _ := f.p.UploadAttachment(ctx, "milo", "kept.txt", "", strings.NewReader("k"))
	if _, e := f.p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "s", Body: "b", Attachments: []string{kept.ID}}); e != nil {
		t.Fatal(e)
	}
	orphan, _ := f.p.UploadAttachment(ctx, "milo", "orphan.txt", "", strings.NewReader("o"))
	f.p.lastPrune = time.Time{}
	f.p.pruneAttachments(ctx, time.Now().Add(3*time.Hour))
	if _, e := os.Stat(filepath.Join(f.state, "attachments", orphan.ID)); !os.IsNotExist(e) {
		t.Fatal("unsent upload older than two hours kept")
	}
	if _, e := os.Stat(filepath.Join(f.state, "attachments", kept.ID)); e != nil {
		t.Fatal("sent attachment pruned early")
	}
	f.p.lastPrune = time.Time{}
	f.p.pruneAttachments(ctx, time.Now().Add(31*24*time.Hour))
	if _, e := os.Stat(filepath.Join(f.state, "attachments", kept.ID)); !os.IsNotExist(e) {
		t.Fatal("expired attachment kept")
	}
}

func TestAttachmentHTTP(t *testing.T) {
	f := setup(t)
	upload := func(name string, body io.Reader, contentType string) (int, core.Attachment) {
		t.Helper()
		req, _ := http.NewRequest("POST", f.server.URL+"/v1/attachments/upload?filename="+name, body)
		req.Header.Set("Authorization", "Bearer "+f.key(t, "a").APIKey)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var a core.Attachment
		_ = json.NewDecoder(resp.Body).Decode(&a)
		return resp.StatusCode, a
	}
	code, raw := upload("raw.txt", strings.NewReader("raw body"), "text/plain")
	if code != 200 || raw.Filename != "raw.txt" || raw.Size != 8 {
		t.Fatalf("raw upload %d %+v", code, raw)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "logo.png")
	part.Write(pngBytes)
	mw.Close()
	code, multi := upload("", &buf, mw.FormDataContentType())
	if code != 200 || multi.Filename != "logo.png" || multi.ContentType != "image/png" {
		t.Fatalf("multipart upload %d %+v", code, multi)
	}
	// Send through /v1/route as an agent would: sign the payload with the attachments in it.
	payload, _ := CanonicalJSON(Payload{Type: "notification", Message: "files", Attachments: []core.Attachment{raw, multi}})
	env := Envelope{From: f.key(t, "a").Address, To: f.key(t, "b").Address, Subject: "files", Priority: "normal"}
	_, key, _ := ReadCredentials(f.state, "a")
	sig, _ := Sign(key, env, json.RawMessage(payload))
	if code, body := f.request(t, "a", "POST", "/v1/route", RouteRequest{To: env.To, Subject: env.Subject, Priority: "normal", Signature: sig, Payload: payload}); code != 200 {
		t.Fatalf("route %d %s", code, body)
	}
	get := func(name, path string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", f.server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+f.key(t, name).APIKey)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}
	resp, b := get("b", multi.URL)
	if resp.StatusCode != 200 || !bytes.Equal(b, pngBytes) || resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Disposition"), "logo.png") {
		t.Fatalf("download %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ = get("c", multi.URL); resp.StatusCode != 404 {
		t.Fatalf("bystander download %d", resp.StatusCode)
	}
	if resp, _ = get("b", "/v1/attachments/../../test.db/download"); resp.StatusCode != 404 {
		t.Fatalf("traversal %d", resp.StatusCode)
	}
}
