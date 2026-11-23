package web_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func sayFrom(t *testing.T, e *env, agent, body string, uc map[string]any) core.Mail {
	t.Helper()
	uc["kind"] = "say"
	m, err := e.svc.Service.(agentSender).AgentSend(context.Background(), agent, core.Outgoing{To: "milo", Subject: "", Body: body, Type: "notification",
		Context: map[string]any{"userchat": uc}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBlocksRender(t *testing.T) {
	e := setup(t)
	sayFrom(t, e, "ultron", "fallback text", map[string]any{"blocks": []map[string]any{
		{"type": "text", "text": "Add this record:\nType first, then the IP."},
		{"type": "link", "label": "Open Hostinger DNS", "url": "https://www.hostinger.com/dns"},
		{"type": "link", "label": "Evil", "url": "javascript:alert(1)"},
		{"type": "values", "title": "DNS record", "rows": []map[string]any{{"label": "Type", "value": "A"}, {"label": "Points to", "value": "203.0.113.7"}}},
		{"type": "command", "code": "sudo caddy reload --config /etc/caddy/Caddyfile", "where": "on the server"},
		{"type": "code", "title": "Caddyfile", "code": "app.example.com {\n  reverse_proxy :3000\n}"},
		{"type": "steps", "items": []string{"Open the zone", "Add the `A` record"}},
	}})
	// A plain message keeps its line breaks.
	sayFrom(t, e, "ultron", "Type: A\nName: app", map[string]any{})
	_, page := e.get("/inbox/ultron")
	for _, want := range []string{
		"has-blocks",
		"Add this record:<br>Type first, then the IP.",
		`<a class="blk-link" href="https://www.hostinger.com/dns"`, `<span class="blk-host">hostinger.com</span>`,
		`data-copy="203.0.113.7"`, "data-copy=\"Type: A\nPoints to: 203.0.113.7\"",
		`<code data-copy-src>sudo caddy reload --config /etc/caddy/Caddyfile</code>`, "blk-admin", "· on the server",
		"Caddyfile</span>", "reverse_proxy :3000",
		`data-step="`, "<code>A</code> record",
		"Type: A<br>Name: app",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("thread missing %q", want)
		}
	}
	if strings.Contains(page, "javascript:") || strings.Contains(page, "fallback text") {
		t.Error("an unsafe link or the fallback body was drawn")
	}
}

func multipartPost(t *testing.T, e *env, path string, fields map[string]string, files map[string][2]string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	for k, f := range files {
		p, _ := w.CreateFormFile(k, f[0])
		io.WriteString(p, f[1])
	}
	w.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTaskLogsAndFiles(t *testing.T) {
	e := setup(t)
	task := askFrom(t, e, "ultron", "Add the DNS record", "Add the record", map[string]any{"task": true, "options": []string{"Added", "I couldn't do it"},
		"blocks": []map[string]any{{"type": "values", "rows": []map[string]any{{"label": "Type", "value": "A"}}}}})
	bug := askFrom(t, e, "ultron", "Show me the bug", "What do you see?", map[string]any{"fields": []map[string]any{
		{"name": "shot", "label": "Screenshot", "type": "file"},
		{"name": "log", "label": "Error output", "type": "logs"},
	}})
	_, page := e.get("/inbox/ultron")
	for _, want := range []string{"needs you to do this", "data-ask-couldnt", "What went wrong?", `name="files"`, `value="Added"`, `enctype="multipart/form-data"`, `type="file" name="f.shot"`, `class="is-mono" name="f.log"`} {
		if !strings.Contains(page, want) {
			t.Errorf("thread missing %q", want)
		}
	}
	if code, body := stage(t, e, "ultron", task.ID, url.Values{"option": {"I couldn't do it"}, "text": {"Hostinger says the record exists"}}); code != 200 || !strings.Contains(body, "I couldn&#39;t do it · Hostinger says") {
		t.Fatalf("task: %d %s", code, body)
	}
	// A missing file is refused; what was pasted stays.
	logs := "Error: EADDRINUSE\n    at listen (net.js:1)"
	if code, body := multipartPost(t, e, "/inbox/ultron/requests/"+bug.ID, map[string]string{"do": "save", "f.log": logs}, nil); code != 422 || !strings.Contains(body, "Choose a file for Screenshot") || !strings.Contains(body, "EADDRINUSE") {
		t.Fatalf("no file: %d %s", code, body)
	}
	code, body := multipartPost(t, e, "/inbox/ultron/requests/"+bug.ID, map[string]string{"do": "save", "f.log": logs}, map[string][2]string{"f.shot": {"bug.png", "\x89PNG fake"}})
	if code != 200 || !strings.Contains(body, "Screenshot: bug.png · Error output: 2 lines") {
		t.Fatalf("files: %d %s", code, body)
	}
	if resp, _ := e.do("POST", "/inbox/ultron/requests/send", url.Values{}, true); resp.StatusCode != 200 {
		t.Fatal("send failed")
	}
	out := e.svc.Sent()[0]
	if len(out.Attachments) != 1 {
		t.Fatalf("attachments: %v", out.Attachments)
	}
	for _, want := range []string{"Result: I couldn't do it", "Answer: Hostinger says the record exists", "Screenshot: attached to this message (bug.png)", "Error output:\n   ```\n   Error: EADDRINUSE\n       at listen (net.js:1)\n   ```"} {
		if !strings.Contains(out.Body, want) {
			t.Errorf("answers body missing %q:\n%s", want, out.Body)
		}
	}
}

// "I couldn't do it" sends what went wrong with a screenshot, at once.
func TestTaskCouldntWithScreenshot(t *testing.T) {
	e := setup(t)
	task := askFrom(t, e, "ultron", "Add the DNS record", "Add the record", map[string]any{"task": true, "options": []string{"Added", "I couldn't do it"}})
	code, body := multipartPost(t, e, "/inbox/ultron/requests/"+task.ID, map[string]string{"send": "1", "option": "I couldn't do it", "text": "The Name field wants a full domain"},
		map[string][2]string{"files": {"hostinger.png", "\x89PNG fake"}})
	if code != 200 || !strings.Contains(body, `class="ask-receipt"`) || !strings.Contains(body, "Sent hostinger.png") {
		t.Fatalf("couldn't: %d %s", code, body)
	}
	out := e.svc.Sent()[0]
	if len(out.Attachments) != 1 {
		t.Fatalf("attachments: %v", out.Attachments)
	}
	for _, want := range []string{"Result: I couldn't do it", "Answer: The Name field wants a full domain", "Screenshots or logs: attached to this message (hostinger.png)"} {
		if !strings.Contains(out.Body, want) {
			t.Errorf("answers body missing %q:\n%s", want, out.Body)
		}
	}
}
