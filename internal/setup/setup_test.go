package setup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

func TestMergeConfigKeepsEverythingElse(t *testing.T) {
	src := `# lolad config
listen: 127.0.0.1:4100   # keep

# where agents live
agents_dirs:
  - /old/one
  - /old/two

remote:
  public_url: https://x.example
claude_bin: /old/claude
# trailing note
`
	out, err := mergeConfig(src, map[string]yamlValue{
		"agents_dirs":    strs("/old/one", "/new dir"),
		"claude_bin":     str("/usr/local/bin/claude"),
		"path":           str("/a:/b"),
		"new_agents_dir": str("/new dir"),
	}, []string{"agents_dirs", "claude_bin", "path", "new_agents_dir"})
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"# lolad config", "listen: 127.0.0.1:4100   # keep", "# where agents live", "remote:\n  public_url: https://x.example", "# trailing note"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q in:\n%s", keep, out)
		}
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if got := stringList(m["agents_dirs"]); len(got) != 2 || got[1] != "/new dir" {
		t.Errorf("agents_dirs = %v", got)
	}
	if m["claude_bin"] != "/usr/local/bin/claude" || m["path"] != "/a:/b" || m["new_agents_dir"] != "/new dir" {
		t.Errorf("scalars wrong: %v", m)
	}
	if strings.Contains(out, "/old/two") || strings.Contains(out, "/old/claude") {
		t.Errorf("old values survived:\n%s", out)
	}
	// Idempotent.
	again, err := mergeConfig(out, map[string]yamlValue{"claude_bin": str("/usr/local/bin/claude")}, []string{"claude_bin"})
	if err != nil || again != out {
		t.Errorf("second merge changed the file: %v\n%s", err, again)
	}
}

func TestMergeConfigEmptyAndNoTrailingNewline(t *testing.T) {
	out, err := mergeConfig("", map[string]yamlValue{"path": str("/usr/bin")}, []string{"path"})
	if err != nil || out != "path: /usr/bin\n" {
		t.Fatalf("%q %v", out, err)
	}
	out, err = mergeConfig("listen: x", map[string]yamlValue{"path": str("/usr/bin")}, []string{"path"})
	if err != nil || out != "listen: x\npath: /usr/bin\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestOrgDisplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "org.yaml")
	if err := orgDisplay(p, "milo", "Camilo"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "display: Camilo") || !strings.Contains(string(b), "tenant: home") {
		t.Fatalf("fresh org:\n%s", b)
	}
	os.WriteFile(p, []byte("# mine\ntenant: acme\nuser:\n  name: boss\n  display: Old\n"), 0o600)
	if err := orgDisplay(p, "milo", "New Name"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "# mine") || !strings.Contains(s, "name: boss") || !strings.Contains(s, "display: New Name") || !strings.Contains(s, "tenant: acme") {
		t.Fatalf("patched org:\n%s", s)
	}
}

func TestExtractPATHToleratesNoise(t *testing.T) {
	out := "Welcome to zsh\nstty: not a tty\n" + pathOpen + "/opt/homebrew/bin:/usr/bin:/usr/bin:relative:/x" + pathClose + "\nbye\n"
	if got := extractPATH(out); got != "/opt/homebrew/bin:/usr/bin:/x" {
		t.Fatalf("got %q", got)
	}
	if extractPATH("nothing") != "" {
		t.Fatal("no markers should give nothing")
	}
}

func TestLoginShellPATHOnlyCapturesPATH(t *testing.T) {
	var gotEnv []string
	var gotArgs []string
	m := New(Options{StateDir: t.TempDir(), Home: "/home/u", GOOS: "linux",
		Getenv: func(k string) string {
			return map[string]string{"SHELL": "/bin/zsh", "PATH": "/usr/bin", "HOME": "/home/u", "ANTHROPIC_API_KEY": "secret", "CLAUDE_CODE_OAUTH_TOKEN": "tok"}[k]
		},
		Run: func(_ context.Context, env []string, name string, args ...string) (string, error) {
			gotEnv, gotArgs = env, append([]string{name}, args...)
			return "motd\n" + pathOpen + "/home/u/.local/bin:/usr/bin" + pathClose, nil
		}})
	p, err := m.loginShellPATH(context.Background())
	if err != nil || p != "/home/u/.local/bin:/usr/bin" {
		t.Fatalf("%q %v", p, err)
	}
	if gotArgs[0] != "/bin/zsh" || gotArgs[1] != "-lic" {
		t.Errorf("args %v", gotArgs)
	}
	for _, e := range gotEnv {
		if strings.Contains(e, "secret") || strings.Contains(e, "tok") {
			t.Errorf("secret leaked into the shell env: %s", e)
		}
	}
}

func TestParseRegPath(t *testing.T) {
	out := "\r\nHKEY_CURRENT_USER\\Environment\r\n    Path    REG_EXPAND_SZ    %USERPROFILE%\\AppData\\Local\\Programs\\claude;C:\\Tools\r\n\r\n"
	got := parseRegPath(out, func(k string) string {
		if k == "USERPROFILE" {
			return `C:\Users\me`
		}
		return ""
	})
	if got != `C:\Users\me\AppData\Local\Programs\claude;C:\Tools` {
		t.Fatalf("got %q", got)
	}
	if cleanPATH(got, ';') != got {
		t.Fatalf("clean changed it: %q", cleanPATH(got, ';'))
	}
}

func TestAgentsDirWarning(t *testing.T) {
	h := "/Users/me"
	for _, c := range []struct {
		goos, dir string
		warn      bool
	}{
		{"darwin", "/Users/me/Lolad Agents", false},
		{"darwin", "/Users/me/Desktop/agents", true},
		{"darwin", "/Users/me/Documents", true},
		{"darwin", "/Users/me/Downloads/x", true},
		{"darwin", "/Users/me/Library/Mobile Documents/com~apple~CloudDocs/a", true},
		{"darwin", "/Volumes/DevSSD/agents", true},
		{"darwin", "/Users/me/DesktopStuff", false},
		{"linux", "/Users/me/Desktop/agents", false},
	} {
		if got := AgentsDirWarning(c.goos, h, c.dir) != ""; got != c.warn {
			t.Errorf("%s %s: warn=%v want %v", c.goos, c.dir, got, c.warn)
		}
	}
	if ExpandDir(h, "~/x y") != "/Users/me/x y" || ExpandDir(h, "") != "" {
		t.Error("ExpandDir")
	}
}

// fakeBin writes an executable shell script named name into dir.
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type env struct {
	home, state, cfg string
	m                *Manager
	sess             *fakeSessions
}

func newEnv(t *testing.T, mut func(*Options)) *env {
	t.Helper()
	home := t.TempDir()
	e := &env{home: home, state: t.TempDir(), cfg: filepath.Join(home, ".config", "lolad", "config.yaml"), sess: newFakeSessions()}
	o := Options{StateDir: e.state, ConfigPath: e.cfg, Home: home, GOOS: runtime.GOOS, Sessions: e.sess,
		Poll: 10 * time.Millisecond, Grace: 30 * time.Millisecond, MaxAge: 5 * time.Second,
		Getenv: func(k string) string {
			switch k {
			case "HOME":
				return home
			case "PATH":
				return "/usr/bin:/bin"
			case "SHELL":
				return "/bin/sh"
			}
			return ""
		},
		Run: func(ctx context.Context, env []string, name string, args ...string) (string, error) {
			// The login shell: report a PATH holding the fake bin dir.
			if len(args) > 0 && args[0] == "-lic" {
				return pathOpen + filepath.Join(home, "bin") + ":/usr/bin" + pathClose, nil
			}
			return defaultRun(ctx, env, name, args...)
		}}
	if mut != nil {
		mut(&o)
	}
	e.m = New(o)
	e.m.noSystemDirs = true
	return e
}

func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts")
	}
}

func TestDetectFindsHarnessesAndLogin(t *testing.T) {
	skipWindows(t)
	e := newEnv(t, nil)
	bin := filepath.Join(e.home, "bin")
	fakeBin(t, bin, "claude", `case "$1" in --version) echo "2.1.7 (Claude Code)";; auth) echo '{"loggedIn": true}';; esac`)
	// codex: installed, logged out
	fakeBin(t, filepath.Join(e.home, ".local", "bin"), "codex", `echo "codex-cli 0.9.1"`)
	st, err := e.m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := st.Harness("claude")
	if !c.Installed || c.Version != "2.1.7" || !c.LoggedIn || !c.Ready || c.Path != filepath.Join(bin, "claude") {
		t.Errorf("claude: %+v", c)
	}
	x, _ := st.Harness("codex")
	if !x.Installed || x.Version != "0.9.1" || x.LoggedIn || x.Ready || !x.LoginChecked {
		t.Errorf("codex: %+v", x)
	}
	w, _ := st.Harness("codewhale")
	if w.Installed || w.InstallHint == "" || w.Ready {
		t.Errorf("codewhale: %+v", w)
	}
	// Codex logs in: auth.json appears.
	os.MkdirAll(filepath.Join(e.home, ".codex"), 0o755)
	os.WriteFile(filepath.Join(e.home, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o600)
	st, _ = e.m.Rescan(context.Background())
	if x, _ = st.Harness("codex"); !x.Ready {
		t.Errorf("codex after login: %+v", x)
	}
	// What was found is in config.yaml, including only PATH from the shell.
	cfg, _ := os.ReadFile(e.cfg)
	for _, want := range []string{"claude_bin:", "codex_bin:", "path:"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config lacks %s:\n%s", want, cfg)
		}
	}
	if strings.Contains(string(cfg), "codewhale_bin") {
		t.Errorf("wrote a binary that was not found:\n%s", cfg)
	}
}

func TestCodexNeedsNode(t *testing.T) {
	skipWindows(t)
	e := newEnv(t, nil)
	fakeBin(t, filepath.Join(e.home, "bin"), "codex", `echo "0.1.0"`)
	e.m.noSystemDirs = true
	// Make it look like an npm shim.
	p := filepath.Join(e.home, "bin", "codex")
	os.WriteFile(p, []byte("#!/usr/bin/env node\n//\n"), 0o755)
	e.m.run = func(ctx context.Context, env []string, name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "-lic" {
			return pathOpen + filepath.Join(e.home, "bin") + pathClose, nil
		}
		return "0.1.0", nil
	}
	st, _ := e.m.Status(context.Background())
	x, _ := st.Harness("codex")
	if !x.Installed || x.Ready || !strings.Contains(x.Problem, "Node.js") {
		t.Fatalf("%+v", x)
	}
}

func TestSaveFolderAndNameAndProgress(t *testing.T) {
	skipWindows(t)
	var named string
	e := newEnv(t, func(o *Options) {
		o.SaveUserName = func(_ context.Context, n string) error { named = n; return nil }
		o.CountAgents = func(context.Context) (int, error) { return 0, nil }
	})
	os.WriteFile(e.cfg, []byte("# mine\nlisten: 127.0.0.1:4100\n"), 0o600)
	os.MkdirAll(filepath.Dir(e.cfg), 0o755)
	os.WriteFile(e.cfg, []byte("# mine\nlisten: 127.0.0.1:4100\n"), 0o600)
	fakeBin(t, filepath.Join(e.home, "bin"), "claude", `case "$1" in --version) echo 2.0.0;; auth) echo '{"loggedIn":true}';; esac`)
	ctx := context.Background()

	st, err := e.m.Status(ctx)
	if err != nil || st.Step != StepLogin || st.Done || st.AgentsDir != filepath.Join(e.home, "Lolad Agents") || !e.m.Needed() {
		t.Fatalf("fresh: %+v %v", st, err)
	}
	yes := true
	st, _ = e.m.Save(ctx, SaveRequest{ServiceDone: &yes})
	if st.Step != StepFolder {
		t.Fatalf("after login step: %s", st.Step)
	}
	dir := filepath.Join(e.home, "My Agents")
	st, err = e.m.Save(ctx, SaveRequest{AgentsDir: &dir})
	if err != nil || st.Step != StepName || st.AgentsDir != dir {
		t.Fatalf("folder: %+v %v", st, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("folder not created")
	}
	cfg, _ := os.ReadFile(e.cfg)
	if !strings.Contains(string(cfg), "# mine") || !strings.Contains(string(cfg), "listen: 127.0.0.1:4100") || !strings.Contains(string(cfg), "new_agents_dir: "+quote(dir)) {
		t.Fatalf("config:\n%s", cfg)
	}
	n := "  Ada Lovelace "
	st, err = e.m.Save(ctx, SaveRequest{UserName: &n})
	if err != nil || named != "Ada Lovelace" || st.Step != StepAgent || st.UserName != "Ada Lovelace" {
		t.Fatalf("name: %+v %v %q", st, err, named)
	}
	if st.Done {
		t.Fatal("done before an agent or a skip")
	}
	st, _ = e.m.Save(ctx, SaveRequest{SkipAgent: &yes})
	if !st.Done || st.Step != StepDone || e.m.Needed() {
		t.Fatalf("finished: %+v needed=%v", st, e.m.Needed())
	}
	// A new Manager on the same state dir resumes where this one stopped.
	again := New(Options{StateDir: e.state, ConfigPath: e.cfg, Home: e.home, GOOS: runtime.GOOS, Getenv: e.m.getenv, Run: e.m.run})
	if again.Needed() {
		t.Fatal("completed state was not persisted")
	}
	b, _ := os.ReadFile(filepath.Join(e.state, "setup.json"))
	if !strings.Contains(string(b), `"completed": true`) {
		t.Fatalf("setup.json:\n%s", b)
	}
	// Rejections.
	bad := "relative/dir"
	if _, err := e.m.Save(ctx, SaveRequest{AgentsDir: &bad}); err == nil {
		// "relative/dir" has no ~, so it is relative to nothing.
		t.Error("relative folder accepted")
	}
	empty := " "
	if _, err := e.m.Save(ctx, SaveRequest{UserName: &empty}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
}

func TestDismissStopsRedirect(t *testing.T) {
	e := newEnv(t, nil)
	if !e.m.Needed() {
		t.Fatal("fresh install should need setup")
	}
	yes := true
	e.m.Save(context.Background(), SaveRequest{Dismiss: &yes})
	if e.m.Needed() {
		t.Fatal("dismissed setup still needed")
	}
}

type fakeSessions struct {
	mu      sync.Mutex
	started map[string][]string
	envs    map[string][]string
	dead    map[string]bool
	killed  []string
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{started: map[string][]string{}, envs: map[string][]string{}, dead: map[string]bool{}}
}
func (f *fakeSessions) Start(name, dir string, env, argv []string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[name], f.envs[name] = argv, env
	delete(f.dead, name)
	return nil
}
func (f *fakeSessions) Exists(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.started[name]
	return ok
}
func (f *fakeSessions) Kill(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.started, name)
	f.killed = append(f.killed, name)
	return nil
}
func (f *fakeSessions) PaneDead(name string) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dead[name], 0, nil
}
func (f *fakeSessions) Attach(context.Context, string, bool) (core.Terminal, error) { return nil, nil }
func (f *fakeSessions) finish(name string) {
	f.mu.Lock()
	f.dead[name] = true
	f.mu.Unlock()
}
func (f *fakeSessions) wasKilled(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.killed {
		if k == name {
			return true
		}
	}
	return false
}

func TestTerminalActions(t *testing.T) {
	skipWindows(t)
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.m.StartTerminal(ctx, "rm-rf"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := e.m.StartTerminal(ctx, "login-claude"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("login before install: %v", err)
	}
	info, err := e.m.StartTerminal(ctx, "install-claude")
	if err != nil {
		t.Fatal(err)
	}
	if info.Session != "setup-install-claude" || !strings.Contains(info.TerminalURL, "setup-install-claude") {
		t.Fatalf("info %+v", info)
	}
	argv := e.sess.started["setup-install-claude"]
	if len(argv) != 3 || argv[0] != "/bin/sh" || !strings.Contains(argv[2], "https://claude.ai/install.sh") {
		t.Fatalf("argv %v", argv)
	}
	for _, kv := range e.sess.envs["setup-install-claude"] {
		if strings.HasPrefix(kv, "ANTHROPIC") || strings.HasPrefix(kv, "CLAUDE_CODE") {
			t.Errorf("session env leaks %s", kv)
		}
	}
	// A second click reuses the live session.
	if again, err := e.m.StartTerminal(ctx, "install-claude"); err != nil || again.Session != info.Session {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	// The command finishes: the harness is found by the rescan, and the session is cleaned up.
	fakeBin(t, filepath.Join(e.home, "bin"), "claude", `echo 2.0.0`)
	e.sess.finish("setup-install-claude")
	deadline := time.Now().Add(3 * time.Second)
	for !e.sess.wasKilled("setup-install-claude") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !e.sess.wasKilled("setup-install-claude") {
		t.Fatal("finished session was not killed")
	}
	st, _ := e.m.Status(ctx)
	if c, _ := st.Harness("claude"); !c.Installed {
		t.Errorf("rescan after the install did not find claude: %+v", c)
	}
	if len(st.Terminals) != 0 {
		t.Errorf("terminals still listed: %+v", st.Terminals)
	}
	// Login now has a binary to run.
	if _, err := e.m.StartTerminal(ctx, "login-claude"); err != nil {
		t.Fatal(err)
	}
	if a := e.sess.started["setup-login-claude"]; !strings.Contains(a[2], "auth login") || !strings.Contains(a[2], "'"+filepath.Join(e.home, "bin", "claude")+"'") {
		t.Errorf("login argv %v", a)
	}
}

func TestAttachOnlySetupSessions(t *testing.T) {
	e := newEnv(t, nil)
	for _, name := range []string{"maple", "setup-", "setup-evil", "lolad-forge", "setup-install-claude/../x"} {
		if _, err := e.m.Attach(context.Background(), name, true); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := e.m.Attach(context.Background(), "setup-login-claude", true); !errors.Is(err, core.ErrNotRunning) {
		t.Errorf("not started: %v", err)
	}
}

func TestMaxAgeKillsSession(t *testing.T) {
	skipWindows(t)
	e := newEnv(t, func(o *Options) { o.MaxAge = 60 * time.Millisecond })
	if _, err := e.m.StartTerminal(context.Background(), "install-codex"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !e.sess.wasKilled("setup-install-codex") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !e.sess.wasKilled("setup-install-codex") {
		t.Fatal("session outlived MaxAge")
	}
}

func TestHTTP(t *testing.T) {
	skipWindows(t)
	e := newEnv(t, nil)
	h := Handler(e.m)
	do := func(method, path, body string) (int, string) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	code, body := do("GET", "/api/setup", "")
	if code != 200 || !strings.Contains(body, `"harnesses"`) || !strings.Contains(body, `"install_hint"`) || !strings.Contains(body, `"service"`) || !strings.Contains(body, `"agents_dir"`) {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ = do("POST", "/api/setup", `{"agents_dir":"`+filepath.Join(e.home, "A")+`","user_name":"Ada"}`); code != 200 {
		t.Fatalf("save %d", code)
	}
	if code, body = do("POST", "/api/setup", `{"user_name":" "}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("blank name %d %s", code, body)
	}
	if code, _ = do("POST", "/api/setup", `{not json`); code != 400 {
		t.Fatalf("bad json %d", code)
	}
	if code, _ = do("POST", "/api/setup/rescan", ""); code != 200 {
		t.Fatalf("rescan %d", code)
	}
	if code, body = do("POST", "/api/setup/terminal", `{"action":"install-claude"}`); code != 200 || !strings.Contains(body, `"session":"setup-install-claude"`) || !strings.Contains(body, `"terminal_url"`) {
		t.Fatalf("terminal %d %s", code, body)
	}
	if code, _ = do("POST", "/api/setup/terminal", `{"action":"x"}`); code != 422 {
		t.Fatalf("bad action %d", code)
	}
	if code, _ = do("POST", "/api/setup/service", ""); code != http.StatusNotImplemented {
		t.Fatalf("service without a control %d", code)
	}
}

type fakeService struct{ in, run bool }

func (f *fakeService) Install(context.Context) error { f.in, f.run = true, true; return nil }
func (f *fakeService) Status(context.Context) (bool, bool, error) {
	return f.in, f.run, nil
}

func TestServiceStep(t *testing.T) {
	skipWindows(t)
	svc := &fakeService{}
	e := newEnv(t, func(o *Options) { o.Service = svc })
	st, _ := e.m.Status(context.Background())
	if !st.Service.Available || st.Service.Installed || st.Step != StepLogin {
		t.Fatalf("%+v", st)
	}
	st, err := e.m.InstallService(context.Background())
	if err != nil || !st.Service.Installed || st.Step != StepHarnesses {
		t.Fatalf("%+v %v", st, err)
	}
}
