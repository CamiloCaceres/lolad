package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestInboxHomePriorityAndLiveRefresh(t *testing.T) {
	e := setup(t)
	_, body := e.get("/")
	waiting, unread, recent := strings.Index(body, "Waiting on you"), strings.Index(body, "New messages"), strings.Index(body, "Recent conversations")
	if waiting < 0 || unread <= waiting || recent <= unread {
		t.Fatalf("home must order questions, unread, recent: %d %d %d", waiting, unread, recent)
	}
	if strings.Contains(body, `<aside class="inbox-list"`) {
		t.Error("home should not duplicate conversations in a rail")
	}
	if !strings.Contains(body, `hx-select="#inbox-home"`) {
		t.Error("home must refresh on live mail")
	}
	_, thread := e.get("/inbox/lola")
	if !strings.Contains(thread, `<aside class="inbox-list"`) {
		t.Error("open thread needs conversation navigation")
	}
	// Opening the unread conversation moves it to recent while keeping its thread available.
	e.get("/inbox/forge")
	_, body = e.get("/")
	recent = strings.Index(body, "Recent conversations")
	if strings.Index(body[recent:], `href="/inbox/forge"`) < 0 {
		t.Error("read conversation missing from recent")
	}
}

func TestInboxMarkdownDeliverable(t *testing.T) {
	e := setup(t)
	type sender interface {
		AgentSend(context.Context, string, core.Outgoing) (core.Mail, error)
	}
	_, err := e.svc.Service.(sender).AgentSend(context.Background(), "forge", core.Outgoing{To: "milo", Subject: "Release notes", Body: "## Ready\n\n- **Shipped**\n\n[Open release](https://example.com/release)\n\n<script>alert(1)</script>", Context: map[string]any{"userchat": map[string]any{"kind": "deliver"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, body := e.get("/inbox/forge")
	for _, want := range []string{"<h2>Ready</h2>", "<li><strong>Shipped</strong></li>", `href="https://example.com/release"`, "data-copy-message", "data-deliver-source", "&lt;script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("thread missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("unsafe Markdown")
	}
}

func TestInboxHomeOneTapAnswer(t *testing.T) {
	e := setup(t)
	ask := lastFrom(t, e.svc, "lola", "ask")
	resp, _ := e.do("POST", "/inbox/lola/decide", url.Values{"id": {ask.ID}, "option": {"Hold until Thursday"}}, false)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/inbox/lola" {
		t.Error("home answer should open the conversation")
	}
	_, home := e.get("/")
	if strings.Contains(home, `name="id" value="`+ask.ID+`"`) {
		t.Error("answered question remains on home")
	}
}

func TestInboxArrivalBanner(t *testing.T) {
	e := setup(t)
	for _, tc := range []struct{ query, want string }{{"created=1", "Scout is ready"}, {"adopted=1", "Scout was added"}} {
		code, body := e.get("/inbox/scout?" + tc.query)
		if code != 200 {
			t.Fatalf("landing: %d", code)
		}
		for _, want := range []string{tc.want, "data-dismiss-arrival", `href="/agents/scout/settings/email"`, "Add to a team"} {
			if !strings.Contains(body, want) {
				t.Errorf("landing lacks %q", want)
			}
		}
	}
	_, body := e.get("/inbox/scout")
	if strings.Contains(body, "data-dismiss-arrival") {
		t.Error("ordinary thread must not show creation welcome")
	}
}
