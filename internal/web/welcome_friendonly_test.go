package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// TestWelcomeFriendPath: on a fresh install with no Claude, the first screen
// offers "I just want to chat with a friend's assistant". The invite lands in
// the chat with the friend's assistant, setup counts as done, and "/" stops
// sending them to /welcome.
func TestWelcomeFriendPath(t *testing.T) {
	w := newWelcome(t, true)
	if st, _ := w.demo.Status(t.Context()); st.Done {
		t.Fatal("demo starts set up")
	}
	_, body, _ := w.get("/welcome")
	if !strings.Contains(body, "I just want to chat with a friend’s assistant") || !strings.Contains(body, "No Claude account needed") {
		t.Fatal("first screen lacks the friend path")
	}
	code, body, _ := w.get("/welcome?step=invite")
	if code != http.StatusOK || !strings.Contains(body, "Chat with a friend’s assistant") || !strings.Contains(body, `href="/welcome?step=login"`) {
		t.Fatalf("invite screen: %d", code)
	}

	// Errors stay on the friend path.
	code, body, _ = w.post("/welcome/friend", url.Values{"from": {"invite"}, "link": {"nope"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Chat with a friend’s assistant") || !strings.Contains(body, "That doesn’t look like a Lolad invite link.") {
		t.Fatalf("bad link: %d", code)
	}

	link := fake.FriendLink("Milo", "milo", [][2]string{{"ultron", "Ultron"}}, time.Now().Add(time.Hour), false)
	code, body, _ = w.post("/welcome/friend", url.Values{"from": {"invite"}, "link": {link}, "name": {"Jugui"}})
	if code != http.StatusOK || !strings.Contains(body, "Milo invited you") || !strings.Contains(body, `name="from" value="invite"`) || !strings.Contains(body, `href="/welcome?step=invite"`) {
		t.Fatalf("preview: %d", code)
	}
	if strings.Contains(body, "share your assistants") {
		t.Fatal("the friend path mentions assistants they don't have")
	}
	if org, _ := w.svc.Org(t.Context()); org.User.Display != "Jugui" {
		t.Fatalf("name not saved: %+v", org.User)
	}
	tok := previewToken(t, body)
	code, _, h := w.post("/welcome/friend/accept", url.Values{"token": {tok}, "from": {"invite"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/inbox/ultron@milo.lolad.local" {
		t.Fatalf("accept: %d %q", code, h.Get("Location"))
	}
	if st, _ := w.demo.Status(t.Context()); !st.Done || !st.FriendOnly || w.demo.Needed() {
		t.Fatalf("not set up after connecting: %+v", st)
	}
	if code, _, h := w.get("/"); code != http.StatusOK {
		t.Fatalf("home: %d %q", code, h.Get("Location"))
	}
	if code, body, _ := w.get("/inbox/ultron@milo.lolad.local"); code != http.StatusOK || !strings.Contains(body, "Ultron") {
		t.Fatalf("chat: %d", code)
	}
}

// A friend sharing several assistants (or none) gets a choice instead.
func TestWelcomeFriendPathChoice(t *testing.T) {
	w := newWelcome(t, true)
	link := fake.FriendLink("Milo", "milo", [][2]string{{"ultron", "Ultron"}, {"nova", "Nova"}}, time.Now().Add(time.Hour), false)
	_, body, _ := w.post("/welcome/friend", url.Values{"from": {"invite"}, "link": {link}})
	code, body, _ := w.post("/welcome/friend/accept", url.Values{"token": {previewToken(t, body)}, "from": {"invite"}})
	if code != http.StatusOK || !strings.Contains(body, "Chat with Ultron") || !strings.Contains(body, "Chat with Nova") || !strings.Contains(body, "Send Milo a message") {
		t.Fatalf("choice: %d", code)
	}
	if st, _ := w.demo.Status(t.Context()); !st.Done {
		t.Fatal("not set up")
	}
}
