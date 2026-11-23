package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	onboard "github.com/CamiloCaceres/lolad/internal/setup"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

type welcome struct {
	t    *testing.T
	svc  *fake.Service
	demo *onboard.Demo
	srv  *httptest.Server
	cli  *http.Client
}

func newWelcome(t *testing.T, empty bool) *welcome {
	t.Helper()
	f := fake.New(fake.Options{Empty: empty})
	t.Cleanup(f.Close)
	d := onboard.NewDemo()
	srv := httptest.NewServer(web.New(f, web.Options{Setup: d, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)
	return &welcome{t: t, svc: f, demo: d, srv: srv, cli: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (w *welcome) get(path string) (int, string, http.Header) {
	w.t.Helper()
	resp, err := w.cli.Get(w.srv.URL + path)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (w *welcome) post(path string, form url.Values) (int, string, http.Header) {
	w.t.Helper()
	resp, err := w.cli.PostForm(w.srv.URL+path, form)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// ready makes Claude Code installed and signed in.
func (w *welcome) ready() {
	w.demo.Set(func(s *onboard.Status) {
		for i := range s.Harnesses {
			if s.Harnesses[i].ID == "claude" {
				s.Harnesses[i].Installed, s.Harnesses[i].LoggedIn, s.Harnesses[i].LoginChecked = true, true, true
				s.Harnesses[i].Path, s.Harnesses[i].Version = "/Users/you/.local/bin/claude", "2.1.7"
			}
		}
	})
}

func TestWelcomeRedirectOnlyWhenFirstRun(t *testing.T) {
	// No agents and setup not finished: "/" goes to the welcome flow.
	w := newWelcome(t, true)
	code, _, h := w.get("/")
	if code != http.StatusSeeOther || h.Get("Location") != "/welcome" {
		t.Fatalf("empty install: %d %v", code, h)
	}
	// An htmx request gets a full navigation.
	req, _ := http.NewRequest("GET", w.srv.URL+"/", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := w.cli.Do(req)
	if err != nil || resp.Header.Get("HX-Redirect") != "/welcome" {
		t.Fatalf("htmx redirect: %v %v", err, resp.Header)
	}
	resp.Body.Close()
	// Leaving setup stops the redirect.
	if code, _, _ = w.post("/welcome/leave", nil); code != http.StatusSeeOther {
		t.Fatalf("leave: %d", code)
	}
	if code, _, _ = w.get("/"); code != http.StatusOK {
		t.Fatalf("after leaving, / = %d", code)
	}
	// Someone with agents is never redirected, finished setup or not.
	w2 := newWelcome(t, false)
	if code, _, _ = w2.get("/"); code != http.StatusOK {
		t.Fatalf("with agents, / = %d", code)
	}
	// Without a setup service the dashboard is unchanged and /welcome goes home.
	f := fake.New(fake.Options{Empty: true})
	t.Cleanup(f.Close)
	srv := httptest.NewServer(web.New(f, web.Options{}))
	t.Cleanup(srv.Close)
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, _ := cli.Get(srv.URL + "/"); r.StatusCode != 200 {
		t.Fatalf("no setup service: / = %d", r.StatusCode)
	}
	if r, _ := cli.Get(srv.URL + "/welcome"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("no setup service: /welcome = %d", r.StatusCode)
	}
}

func TestWelcomeWalkThrough(t *testing.T) {
	w := newWelcome(t, true)

	// 1. hello (Ob-Welcome): Get started skips the login step; "Keep
	// running" is offered on Ready instead.
	_, body, _ := w.get("/welcome")
	for _, want := range []string{"Let’s wake up your first assistant.", "Get started", `name="op" value="skip"`, "I’ve used lolad before"} {
		if !strings.Contains(body, want) {
			t.Errorf("hello lacks %q", want)
		}
	}
	if strings.Contains(body, `aria-label="Main"`) {
		t.Error("the main navigation should not show during setup")
	}
	if code, _, h := w.post("/welcome/service", url.Values{"op": {"install"}}); code != 303 || !strings.Contains(h.Get("Location"), "step=harnesses") {
		t.Fatalf("service: %d %v", code, h)
	}

	// 2. harnesses: nothing is ready, so Continue is disabled and Install is offered.
	_, body, _ = w.get("/welcome?step=harnesses")
	if !strings.Contains(body, "Not installed") || !strings.Contains(body, `value="install-claude"`) || !strings.Contains(body, "disabled") {
		t.Fatalf("harness step:\n%s", body)
	}
	if strings.Contains(body, `value="install-codewhale"`) {
		t.Error("codewhale has no installer to offer")
	}
	// Install opens the embedded terminal.
	code, _, h := w.post("/welcome/terminal", url.Values{"action": {"install-claude"}})
	if code != 303 || !strings.Contains(h.Get("Location"), "terminal=setup-install-claude") {
		t.Fatalf("terminal start: %d %v", code, h)
	}
	_, body, _ = w.get(h.Get("Location"))
	for _, want := range []string{`data-terminal`, `data-term-path="/welcome/terminal/setup-install-claude"`, `data-term-control="on"`, "Installing Claude Code"} {
		if !strings.Contains(body, want) {
			t.Errorf("terminal panel lacks %q", want)
		}
	}
	if code, _, _ = w.post("/welcome/terminal", url.Values{"action": {"rm-rf"}}); code != http.StatusUnprocessableEntity {
		t.Errorf("bad action: %d", code)
	}
	// Once Claude is ready the live fragment enables Continue.
	w.ready()
	_, frag, _ := w.get("/welcome/harnesses")
	if !strings.Contains(frag, `hx-swap-oob="true"`) || !strings.Contains(frag, `action="/welcome/folder"`) || !strings.Contains(frag, `name="dir" value="`) || !strings.Contains(frag, "Ready") {
		t.Fatalf("fragment:\n%s", frag)
	}
	// Re-scan comes back to the same step.
	if code, _, h = w.post("/welcome/harnesses/rescan", url.Values{"capture_path_present": {"1"}}); code != 303 || !strings.Contains(h.Get("Location"), "step=harnesses") {
		t.Fatalf("rescan: %d %v", code, h)
	}

	// 3. folder: Continue keeps the default (no screen of its own); the
	// folder screen is still there if saving it fails. Protected places warn.
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "Where should your assistants live?") || !strings.Contains(body, "Lolad Agents") {
		t.Fatalf("folder step:\n%s", body)
	}
	_, chk, _ := w.get("/welcome/folder/check?dir=" + url.QueryEscape("/Volumes/Backup/agents"))
	if !strings.Contains(chk, "external or network drive") || !strings.Contains(chk, "is-warn") {
		t.Errorf("no TCC warning:\n%s", chk)
	}
	_, chk, _ = w.get("/welcome/folder/check?dir=" + url.QueryEscape("~/Lolad Agents"))
	if strings.Contains(chk, "is-warn") {
		t.Errorf("warning for the default:\n%s", chk)
	}
	if code, _, _ = w.post("/welcome/folder", url.Values{"dir": {"/Users/you/Lolad Agents"}}); code != 303 {
		t.Fatalf("folder: %d", code)
	}

	// 4. name goes through the daemon's org.
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "What should your assistants call you?") || !strings.Contains(body, `value="Camilo"`) || !strings.Contains(body, "you@camilo.lolad.local") || !strings.Contains(body, "Claude connected") {
		t.Fatalf("name step:\n%s", body)
	}
	if code, body, _ = w.post("/welcome/name", url.Values{"name": {" "}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "what to call you") {
		t.Fatalf("blank name: %d\n%s", code, body)
	}
	if code, _, _ = w.post("/welcome/name", url.Values{"name": {"Ada Lovelace"}}); code != 303 {
		t.Fatalf("name: %d", code)
	}
	org, _ := w.svc.Org(context.Background())
	if org.User.Display != "Ada Lovelace" {
		t.Fatalf("org user = %+v", org.User)
	}

	// 5a. Ob-Job: what to help with first.
	_, body, _ = w.get("/welcome")
	for _, want := range []string{"Nice to meet you, Ada.", "What would you like help with first?", `name="job" value="assistant" checked`, `name="job" value="other"`, "I’ll do this later"} {
		if !strings.Contains(body, want) {
			t.Errorf("job step lacks %q", want)
		}
	}
	// 5b. Ob-Shape: a name (with ideas), how it works, Advanced.
	_, body, _ = w.get("/welcome?step=shape&job=writer")
	for _, want := range []string{"A writer. Good choice.", `value="Quill"`, `data-shape-pick="Poe"`, `name="work" value="ask" checked`, `name="harness" value="claude"`, `name="dir"`, "Wake&#32;<span data-shape-name>Quill</span>"} {
		if !strings.Contains(body, want) {
			t.Errorf("shape step lacks %q", want)
		}
	}
	if code, body, _ = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "job": {"other"}, "mission": {""}, "harness": {"claude"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Describe the job") || !strings.Contains(body, `value="Maple"`) {
		t.Fatalf("empty mission: %d\n%s", code, body)
	}
	if code, body, _ = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "mission": {"Help"}, "harness": {"codewhale"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "installed and signed in") {
		t.Fatalf("unready harness: %d", code)
	}
	code, _, h = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "job": {"writer"}, "work": {"ask"}, "harness": {"claude"}})
	if code != 303 || !strings.Contains(h.Get("Location"), "step=waking") || !strings.Contains(h.Get("Location"), "agent=maple") || !strings.Contains(h.Get("Location"), "job=writer") {
		t.Fatalf("create: %d %v", code, h)
	}
	a, err := w.svc.Get(context.Background(), "maple")
	if err != nil || a.Dir != "/Users/you/Lolad Agents/maple" || a.Runtime.Harness != "claude" || a.Role != "Writer" || !strings.Contains(a.Runtime.OnWake, "ask_user") || a.Runtime.Permissions != "bypass" || !strings.Contains(w.lastMission(t, "maple"), "anything that costs money") {
		t.Fatalf("agent = %+v %v", a, err)
	}

	// 6. Ob-Waking polls until the assistant is up, then Ready.
	_, body, _ = w.get(h.Get("Location"))
	if !strings.Contains(body, "Waking Maple up…") || !strings.Contains(body, `hx-get="/welcome/waking?agent=maple&amp;job=writer`) {
		t.Fatalf("waking:\n%s", body)
	}
	if code, _, h := w.get("/welcome/waking?agent=maple&job=writer&since=1"); code != http.StatusNoContent || h.Get("HX-Redirect") != "/welcome?step=ready&agent=maple&job=writer" {
		t.Errorf("waking should move on to Ready: %d %v", code, h)
	}
	_, body, _ = w.get("/welcome?step=ready&agent=maple&job=writer")
	for _, want := range []string{"Maple", `action="/inbox/maple"`, `value="Draft this week&#39;s newsletter"`, "Keep Maple running", "Connect your phone", "Go to Home"} {
		if !strings.Contains(body, want) {
			t.Errorf("ready lacks %q", want)
		}
	}
	if code, _, _ = w.get("/"); code != http.StatusOK {
		t.Fatalf("/ after setup = %d", code)
	}
}

func TestWelcomeLaterAndBack(t *testing.T) {
	w := newWelcome(t, true)
	w.ready()
	w.post("/welcome/service", url.Values{"op": {"skip"}})
	w.post("/welcome/folder", url.Values{"dir": {"/Users/you/Lolad Agents"}})
	w.post("/welcome/name", url.Values{"name": {"Ada"}})
	// An earlier step can be revisited; a later one cannot be jumped to.
	_, body, _ := w.get("/welcome?step=folder")
	if !strings.Contains(body, "Where should your assistants live?") {
		t.Error("cannot go back")
	}
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "What would you like help with first?") {
		t.Error("should be on the job step")
	}
	if _, body, _ = w.get("/welcome?step=login"); !strings.Contains(body, "Get started") {
		t.Error("the hello screen is reachable from the end")
	}
	code, _, h := w.post("/welcome/later", nil)
	if code != 303 || !strings.Contains(h.Get("Location"), "step=done") {
		t.Fatalf("later: %d %v", code, h)
	}
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "You’re all set") {
		t.Errorf("done without an agent:\n%s", body)
	}
	if code, _, _ = w.get("/"); code != 200 {
		t.Errorf("/ = %d after skipping", code)
	}
}

func TestWelcomeNoUsableHarnessForAgents(t *testing.T) {
	w := newWelcome(t, true)
	// Only Codex is ready: lolad can't run assistants on it yet, so it is not
	// offered and does not get the user past the brain step.
	w.demo.Set(func(s *onboard.Status) {
		for i := range s.Harnesses {
			if s.Harnesses[i].ID == "codex" {
				s.Harnesses[i].Installed, s.Harnesses[i].LoggedIn, s.Harnesses[i].LoginChecked = true, true, true
			}
		}
	})
	w.post("/welcome/service", url.Values{"op": {"skip"}})
	_, body, _ := w.get("/welcome")
	if strings.Contains(body, `data-harness="codex"`) {
		t.Errorf("codex offered:\n%s", body)
	}
	if strings.Contains(body, `data-harness="codewhale"`) {
		t.Errorf("codewhale offered although not installed:\n%s", body)
	}
	if !strings.Contains(body, "Get Claude to Ready to continue") {
		t.Fatalf("brain step should still be waiting:\n%s", body)
	}
}

func TestWelcomeTerminalSocket(t *testing.T) {
	w := newWelcome(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(session string) (*websocket.Conn, error) {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.srv.URL, "http")+"/welcome/terminal/"+session+"?mode=control", nil)
		return c, err
	}
	c, err := dial("setup-install-claude")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, data, err := c.Read(ctx)
	if err != nil || len(data) == 0 {
		t.Fatalf("no terminal output: %v", err)
	}
	// Anything that is not a setup session is refused (here: an agent's).
	c2, err := dial("forge")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	if _, _, err = c2.Read(ctx); websocket.CloseStatus(err) != 4404 {
		t.Fatalf("agent session over the setup socket: %v", err)
	}
}

// lastMission is the newest mail the user sent the agent: its mission.
func (w *welcome) lastMission(t *testing.T, agent string) string {
	t.Helper()
	msgs, err := w.svc.Conversation(context.Background(), agent, 20, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ToName == agent {
			return msgs[i].Body
		}
	}
	return ""
}

// App-Settings board: Claude's state, "Keep lolad running" and where the
// assistants live, from setup.
func TestAppSettingsFromSetup(t *testing.T) {
	w := newWelcome(t, false)
	w.ready()
	_, body, _ := w.get("/settings")
	for _, want := range []string{"Claude <span class=\"set-state\">· Connected</span>", "Keep lolad running", `action="/settings/service"`, "Where assistants live"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	if code, _, h := w.post("/settings/service", nil); code != 303 || h.Get("Location") != "/settings" {
		t.Fatalf("service: %d %v", code, h)
	}
	if _, body, _ = w.get("/settings"); strings.Contains(body, `action="/settings/service"`) || !strings.Contains(body, "<i></i>On</span>") {
		t.Error("keep running should now be on")
	}
}
