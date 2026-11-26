package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hub"
)

// lolad is one test install: its own HOME, state, config and org.yaml, with
// the given agents (slug -> display), running like the daemon does.
func lolad(t *testing.T, hubURL, tenant, user string, agents map[string]string) *app.App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	t.Setenv("LOLAD_HUB_URL", hubURL)
	var paths []string
	for slug, display := range agents {
		p := filepath.Join(dir, slug)
		os.MkdirAll(p, 0700)
		os.WriteFile(filepath.Join(p, "lolabot.yaml"), []byte("agent:\n  name: "+display+"\nruntime:\n  harness: claude\n  autostart: false\n  wake_on_message: false\n"), 0600)
		paths = append(paths, p)
	}
	config := filepath.Join(dir, "config.yaml")
	os.WriteFile(config, []byte("agents_dirs: []\nagents: ["+strings.Join(paths, ", ")+"]\nnew_agents_dir: "+filepath.Join(dir, "agents")+"\ntmux_socket: lolad-test-fo-"+tenant+"-"+filepath.Base(dir)+"\n"), 0600)
	os.WriteFile(filepath.Join(dir, "org.yaml"), []byte("tenant: "+tenant+"\nuser:\n  name: "+user+"\n"), 0600)
	a, e := app.New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	return a
}

// browser is a cookie-less client that doesn't follow redirects, so each
// step's answer can be checked.
type browser struct {
	t    *testing.T
	base string
}

func (b browser) do(method, path string, form url.Values) (int, string, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := c.Do(req)
	if e != nil {
		b.t.Fatal(e)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(out)
}

// TestFriendWithoutClaude is Camilo's core requirement: a friend with no
// Claude (no harness, no assistants) installs lolad, chooses "I just want to
// chat with a friend's assistant" on the first screen, pastes Milo's invite,
// lands in the chat with Milo's assistant, writes to it and reads its reply.
// Setup counts as done, so nothing sends them back to onboarding.
func TestFriendWithoutClaude(t *testing.T) {
	_, hubURL := testHubServer(t, hub.SignupOpen)
	milo := lolad(t, hubURL, "milo", "milo", map[string]string{"ultron": "Ultron"})
	inv, e := milo.CreateFriendInvite(t.Context(), core.FriendInviteRequest{Agents: []string{"ultron"}})
	if e != nil {
		t.Fatal(e)
	}

	// The friend's computer: no claude anywhere it looks.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")
	jugui := lolad(t, hubURL, "jugui", "jugui", nil)
	srv := httptest.NewServer(jugui.Handler(false, "test", slog.New(slog.DiscardHandler)))
	defer srv.Close()
	b := browser{t, srv.URL}

	var st struct {
		Done      bool `json:"done"`
		Harnesses []struct {
			ID    string `json:"id"`
			Ready bool   `json:"ready"`
		} `json:"harnesses"`
	}
	setupStatus := func() {
		t.Helper()
		_, _, body := b.do("GET", "/api/setup", nil)
		if e := json.Unmarshal([]byte(body), &st); e != nil {
			t.Fatal(body, e)
		}
	}
	setupStatus()
	for _, h := range st.Harnesses {
		if h.ID == "claude" && h.Ready {
			t.Skip("this machine has a usable claude outside PATH and HOME")
		}
	}
	if st.Done {
		t.Fatal("a fresh install without Claude is already set up")
	}

	// 1. The first screen offers the friend path.
	if code, loc, _ := b.do("GET", "/", nil); code != http.StatusSeeOther || loc != "/welcome" {
		t.Fatalf("home on a fresh install: %d %q", code, loc)
	}
	if _, _, body := b.do("GET", "/welcome", nil); !strings.Contains(body, "I just want to chat with a friend’s assistant") || !strings.Contains(body, `href="/welcome?step=invite"`) {
		t.Fatal("no friend path on the first screen")
	}
	if code, _, body := b.do("GET", "/welcome?step=invite", nil); code != http.StatusOK || !strings.Contains(body, "Chat with a friend’s assistant") || !strings.Contains(body, `name="from" value="invite"`) {
		t.Fatalf("invite screen: %d", code)
	}

	// 2. Paste the link and a name: the preview names Milo and Ultron.
	code, _, body := b.do("POST", "/welcome/friend", url.Values{"from": {"invite"}, "link": {inv.Link}, "name": {"Jugui"}})
	if code != http.StatusOK || !strings.Contains(body, "Connect with") || !strings.Contains(body, "Ultron") {
		t.Fatalf("preview: %d %s", code, excerptHTML(body))
	}
	if strings.Contains(body, "share your assistants") {
		t.Fatal("the friend path talks about assistants they don't have")
	}
	token := between(body, `name="token" value="`, `"`)
	if token == "" || !strings.Contains(body, `name="from" value="invite"`) {
		t.Fatal("preview form lost the token or the path")
	}

	// 3. Connect: straight into the chat with Ultron.
	agent := "ultron@" + milo.Info(t.Context()).Provider
	code, loc, body := b.do("POST", "/welcome/friend/accept", url.Values{"token": {token}, "from": {"invite"}})
	if code != http.StatusSeeOther || loc != "/inbox/"+url.PathEscape(agent) {
		t.Fatalf("accept: %d %q %s", code, loc, excerptHTML(body))
	}
	setupStatus()
	if !st.Done {
		t.Fatal("connected with a friend but setup isn't done")
	}
	if code, loc, _ := b.do("GET", "/", nil); code != http.StatusOK {
		t.Fatalf("home after connecting: %d %q (sent back to onboarding?)", code, loc)
	}
	if org, _ := jugui.Org(t.Context()); org.User.Display != "Jugui" {
		t.Fatalf("name not saved: %+v", org.User)
	}

	// 4. Write to Ultron from the chat.
	if code, _, body := b.do("GET", loc, nil); code != http.StatusOK || !strings.Contains(body, "Ultron") {
		t.Fatalf("chat with Ultron: %d", code)
	}
	if code, _, body := b.do("POST", loc, url.Values{"body": {"Hi Ultron, Milo said you could help me plan a trip."}}); code >= 400 {
		t.Fatalf("send: %d %s", code, excerptHTML(body))
	}
	var in core.Mail
	deadline := time.Now().Add(15 * time.Second)
	for in.ID == "" && time.Now().Before(deadline) {
		list, _ := milo.Provider.List(t.Context(), "ultron", core.MailFilter{Box: "all", Limit: 10})
		for _, m := range list {
			if strings.Contains(m.Body, "plan a trip") {
				in = m
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if in.ID == "" || in.FromName != "jugui@"+jugui.Info(t.Context()).Provider || in.Remote == "" {
		t.Fatalf("Ultron's copy: %+v", in)
	}

	// 5. Ultron answers with `lolad msg reply`; Jugui reads it in the chat.
	rep, e := milo.Provider.Send(t.Context(), "ultron", core.Outgoing{To: in.FromName, Subject: "Re: " + in.Subject, Body: "Happy to help. Where to?", Type: "response", InReplyTo: in.ID})
	if e != nil {
		t.Fatalf("Ultron's reply was refused: %v", e)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		_, _, body := b.do("GET", loc, nil)
		if strings.Contains(body, "Happy to help. Where to?") {
			break
		}
		if time.Now().After(deadline) {
			m, _ := milo.Provider.Get(t.Context(), rep.ID)
			t.Fatalf("the reply never showed in Jugui's chat (delivery %s %s)", m.Delivery, m.DeliveryError)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func between(s, a, b string) string {
	_, rest, ok := strings.Cut(s, a)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, b)
	return v
}

func excerptHTML(s string) string {
	if i := strings.Index(s, `role="alert"`); i >= 0 {
		s = s[i:]
	}
	if len(s) > 600 {
		s = s[:600]
	}
	return s
}
