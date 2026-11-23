package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type agentSender interface {
	AgentSend(context.Context, string, core.Outgoing) (core.Mail, error)
}

func askFrom(t *testing.T, e *env, agent, subject, body string, uc map[string]any) core.Mail {
	t.Helper()
	uc["kind"] = "ask"
	m, err := e.svc.Service.(agentSender).AgentSend(context.Background(), agent, core.Outgoing{To: "milo", Subject: subject, Body: body, Type: "request",
		Context: map[string]any{"userchat": uc}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func stage(t *testing.T, e *env, peer, id string, form url.Values) (int, string) {
	t.Helper()
	resp, body := e.do("POST", "/inbox/"+peer+"/requests/"+id, form, true)
	return resp.StatusCode, body
}

func TestRequestsStageThenSendOnce(t *testing.T) {
	e := setup(t)
	pick := askFrom(t, e, "ultron", "Dinner", "Thai or pizza tonight?", map[string]any{"options": []string{"Thai", "Pizza"}})
	words := askFrom(t, e, "ultron", "Gift", "What should I get Ana for her birthday?", map[string]any{})
	form := askFrom(t, e, "ultron", "Shipping", "Where do I send the books?", map[string]any{"fields": []map[string]any{
		{"name": "city", "label": "City", "type": "text"},
		{"name": "copies", "label": "Copies", "type": "number"},
		{"name": "note", "label": "Note", "type": "longtext", "optional": true},
	}})

	_, page := e.get("/inbox/ultron")
	for _, want := range []string{`id="req-bar"`, "3 questions waiting", `data-ask="`, `value="Thai"`, "Or in your own words", `name="f.city"`} {
		if !strings.Contains(page, want) {
			t.Errorf("thread missing %q", want)
		}
	}

	if code, body := stage(t, e, "ultron", pick.ID, url.Values{"option": {"Thai"}}); code != 200 || !strings.Contains(body, "1 ready to send") || !strings.Contains(body, "not sent yet") {
		t.Fatalf("stage option: %d %s", code, body)
	}
	if code, body := stage(t, e, "ultron", words.ID, url.Values{"do": {"save"}, "text": {"  A good novel.  "}}); code != 200 || !strings.Contains(body, "2 ready to send") || !strings.Contains(body, "1 question waiting") {
		t.Fatalf("stage words: %d %s", code, body)
	}
	// A refused card keeps what was typed and says why.
	code, body := stage(t, e, "ultron", form.ID, url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"two"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Copies needs a number") || !strings.Contains(body, `value="Bogotá"`) {
		t.Fatalf("invalid number: %d %s", code, body)
	}
	if code, _ := stage(t, e, "ultron", form.ID, url.Values{"do": {"save"}, "f.city": {"Bogotá"}, "f.copies": {"2"}}); code != 200 {
		t.Fatalf("stage form: %d", code)
	}
	if n := len(e.svc.Sent()); n != 0 {
		t.Fatalf("staging sent %d messages; nothing goes out before Send", n)
	}

	// Chatting meanwhile doesn't answer a request, even "replying" to it.
	if resp, _ := e.do("POST", "/inbox/ultron", url.Values{"body": {"Back in 5"}, "reply_to": {words.ID}}, true); resp.StatusCode != 200 {
		t.Fatal("chat failed")
	}
	chat := e.svc.Sent()[0]
	if chat.InReplyTo != "" || userchat(t, chat)["kind"] != "say" {
		t.Errorf("a chat message must not answer a request: %+v", chat)
	}
	if _, page = e.get("/inbox/ultron"); !strings.Contains(page, "3 ready to send") {
		t.Error("requests should stay open while chatting")
	}

	resp, body := e.do("POST", "/inbox/ultron/requests/send", url.Values{}, true)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("HX-Trigger"), "lolad:mail") || !strings.Contains(body, "Answered 3 requests") {
		t.Fatalf("send: %d %s", resp.StatusCode, body)
	}
	sent := e.svc.Sent()
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want the chat plus one answers mail", len(sent))
	}
	out := sent[1]
	uc := userchat(t, out)
	answers, _ := uc["answers"].([]any)
	if uc["kind"] != "answers" || len(answers) != 3 || out.To != "ultron" || out.Subject != "Answers to 3 requests" || out.InReplyTo != form.ID {
		t.Fatalf("answers mail: %+v", out)
	}
	for _, want := range []string{"3 answered, 0 skipped", "Choice: Thai", "Answer: A good novel.", "City: Bogotá", "Copies: 2", "(request "} {
		if !strings.Contains(out.Body, want) {
			t.Errorf("answers body missing %q:\n%s", want, out.Body)
		}
	}
	_, page = e.get("/inbox/ultron")
	if strings.Contains(page, `action="/inbox/ultron/requests/`) || !strings.Contains(page, `id="req-bar" class="req-bar" hidden`) {
		t.Error("answered requests still open")
	}
	for _, want := range []string{"<span>Thai</span>", "Answered 3 requests", `<span class="answers-a">A good novel.</span>`, "City: Bogotá · Copies: 2"} {
		if !strings.Contains(page, want) {
			t.Errorf("thread missing %q", want)
		}
	}
}

func TestRequestsSecretNeverInMail(t *testing.T) {
	e := setup(t)
	const value = "sk-test-0123456789abcdef"
	ask := askFrom(t, e, "ultron", "", "I need your OpenAI key to make the images.", map[string]any{"fields": []map[string]any{
		{"name": "OPENAI_API_KEY", "label": "OpenAI API key", "type": "secret"},
	}})
	_, page := e.get("/inbox/ultron")
	if !strings.Contains(page, `type="password" name="f.OPENAI_API_KEY"`) || !strings.Contains(page, "Add the key") {
		t.Fatal("secret field not rendered as a password input")
	}
	if code, body := stage(t, e, "ultron", ask.ID, url.Values{"do": {"save"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Paste OpenAI API key") {
		t.Fatalf("empty secret: %d %s", code, body)
	}
	code, body := stage(t, e, "ultron", ask.ID, url.Values{"do": {"save"}, "f.OPENAI_API_KEY": {value + "\n"}})
	if code != 200 || strings.Contains(body, value) || !strings.Contains(body, "OPENAI_API_KEY saved") {
		t.Fatalf("stage secret: %d %s", code, body)
	}
	vars, _ := e.svc.Env(context.Background(), "ultron")
	saved := false
	for _, v := range vars {
		saved = saved || (v.Key == "OPENAI_API_KEY" && v.Set && v.Length == len(value))
	}
	if !saved {
		t.Fatalf("key not saved to the env file: %+v", vars)
	}
	if resp, _ := e.do("POST", "/inbox/ultron/requests/send", url.Values{}, true); resp.StatusCode != 200 {
		t.Fatal("send failed")
	}
	out := e.svc.Sent()[0]
	if strings.Contains(out.Body, value) || strings.Contains(out.Subject, value) {
		t.Fatal("secret value travelled in mail")
	}
	if !strings.Contains(out.Body, "OPENAI_API_KEY: saved in your .env file") {
		t.Errorf("body: %s", out.Body)
	}
	if a := userchat(t, out)["answers"].([]any)[0].(map[string]any); a["secrets"].([]any)[0] != "OPENAI_API_KEY" {
		t.Errorf("answer: %v", a)
	}
}

func TestRequestsSkipAndHome(t *testing.T) {
	e := setup(t)
	a1 := askFrom(t, e, "ultron", "Q1", "First?", map[string]any{"options": []string{"Yes", "No"}})
	a2 := askFrom(t, e, "ultron", "Q2", "Second?", map[string]any{})
	_, home := e.get("/")
	if !strings.Contains(home, "asked you 2 things") || !strings.Contains(home, `href="/inbox/ultron#m-`) || strings.Contains(home, "<form class=\"req-form") {
		t.Fatal("home should list Ultron's questions as one row that opens the conversation")
	}
	for _, id := range []string{a1.ID, a2.ID} {
		if code, body := stage(t, e, "ultron", id, url.Values{"do": {"skip"}}); code != 200 || !strings.Contains(body, "You’ll skip this") {
			t.Fatalf("skip: %d %s", code, body)
		}
	}
	resp, _ := e.do("POST", "/inbox/ultron/requests/send", url.Values{}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/inbox/ultron" {
		t.Errorf("plain send: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if out := e.svc.Sent()[0]; !strings.Contains(out.Body, "0 answered, 2 skipped") || !strings.Contains(out.Body, "Skipped: the user chose not to answer") {
		t.Errorf("body: %s", out.Body)
	}
	// Nothing staged: Send refuses, in the bar.
	askFrom(t, e, "ultron", "Q3", "Third?", map[string]any{})
	if resp, body := e.do("POST", "/inbox/ultron/requests/send", url.Values{}, true); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Answer or skip a request first") {
		t.Errorf("empty send: %d", resp.StatusCode)
	}
}
