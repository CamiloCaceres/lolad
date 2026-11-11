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

	// 1. start at login
	_, body, _ := w.get("/welcome")
	for _, want := range []string{"Keep lolad running in the background", `aria-current="step"`, "Start at login", "Not now", "Set up later"} {
		if !strings.Contains(body, want) {
			t.Errorf("login step lacks %q", want)
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
	if !strings.Contains(frag, `hx-swap-oob="true"`) || !strings.Contains(frag, `href="/welcome?step=folder"`) || !strings.Contains(frag, "Ready") {
		t.Fatalf("fragment:\n%s", frag)
	}
	// Re-scan comes back to the same step.
	if code, _, h = w.post("/welcome/harnesses/rescan", url.Values{"capture_path_present": {"1"}}); code != 303 || !strings.Contains(h.Get("Location"), "step=harnesses") {
		t.Fatalf("rescan: %d %v", code, h)
	}

	// 3. folder: the default is offered, protected places warn.
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "Where should your agents live?") || !strings.Contains(body, "Lolad Agents") {
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
	if !strings.Contains(body, "What should your agents call you?") || !strings.Contains(body, `value="Camilo Caceres"`) {
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

	// 5. first agent: defaults are filled in; problems are explained.
	_, body, _ = w.get("/welcome")
	for _, want := range []string{"Bring on your first agent", `value="Maple"`, `name="harness" value="claude"`, "Create and start", "I’ll add an agent later"} {
		if !strings.Contains(body, want) {
			t.Errorf("agent step lacks %q", want)
		}
	}
	if code, body, _ = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "mission": {""}, "harness": {"claude"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Describe the job") || !strings.Contains(body, `value="Maple"`) {
		t.Fatalf("empty mission: %d\n%s", code, body)
	}
	if code, body, _ = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "mission": {"Help"}, "harness": {"codewhale"}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "installed and signed in") {
		t.Fatalf("unready harness: %d", code)
	}
	code, _, h = w.post("/welcome/agent", url.Values{"name": {"Maple"}, "mission": {"Keep my projects moving."}, "harness": {"claude"}})
	if code != 303 || !strings.Contains(h.Get("Location"), "step=done") || !strings.Contains(h.Get("Location"), "agent=maple") {
		t.Fatalf("create: %d %v", code, h)
	}
	a, err := w.svc.Get(context.Background(), "maple")
	if err != nil || a.Dir != "/Users/you/Lolad Agents/maple" || a.Runtime.Harness != "claude" {
		t.Fatalf("agent = %+v %v", a, err)
	}

	// 6. done, and "/" no longer redirects.
	_, body, _ = w.get(h.Get("Location"))
	if !strings.Contains(body, "Maple is on it") || !strings.Contains(body, `href="/inbox/maple?created=1"`) || !strings.Contains(body, "data-notify") {
		t.Fatalf("done:\n%s", body)
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
	if !strings.Contains(body, "Where should your agents live?") {
		t.Error("cannot go back")
	}
	_, body, _ = w.get("/welcome")
	if !strings.Contains(body, "Bring on your first agent") {
		t.Error("should be on the agent step")
	}
	if _, body, _ = w.get("/welcome?step=login"); !strings.Contains(body, "Keep lolad running") {
		t.Error("login step is reachable from the end")
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
	// Only Codex is ready: it can't run lolad agents yet, and the screen says so.
	w.demo.Set(func(s *onboard.Status) {
		for i := range s.Harnesses {
			if s.Harnesses[i].ID == "codex" {
				s.Harnesses[i].Installed, s.Harnesses[i].LoggedIn, s.Harnesses[i].LoginChecked = true, true, true
			}
		}
	})
	w.post("/welcome/service", url.Values{"op": {"skip"}})
	w.post("/welcome/folder", url.Values{"dir": {"/Users/you/Lolad Agents"}})
	w.post("/welcome/name", url.Values{"name": {"Ada"}})
	_, body, _ := w.get("/welcome")
	if !strings.Contains(body, "None of the tools that are ready can run lolad agents yet") {
		t.Fatalf("agent step:\n%s", body)
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
