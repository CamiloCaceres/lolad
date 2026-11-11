package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/api"
)

type recorder struct {
	calls []string
	fail  map[string]bool
	out   map[string]string
}

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for k := range r.fail {
		if strings.HasPrefix(line, k) {
			return []byte("boom"), errors.New("exit 1")
		}
	}
	for k, v := range r.out {
		if strings.HasPrefix(line, k) {
			return []byte(v), nil
		}
	}
	return nil, nil
}

func testEnv(t *testing.T, r *recorder, healthy bool) serviceEnv {
	dir := t.TempDir()
	return serviceEnv{Home: dir, UID: "501", StateDir: filepath.Join(dir, "state"), Listen: "127.0.0.1:4100", Path: "/usr/bin:/bin", Run: r.run,
		Health: func() (api.Health, bool) { return api.Health{Product: "lolad", PID: 4242}, healthy }}
}

func TestPlistIsMinimalAndEscaped(t *testing.T) {
	p := renderPlist("/Applications/Lolad.app/Contents/Helpers/lolad", "/Users/a&b", "/usr/bin", "app", "/state/logs/daemon.log")
	for _, want := range []string{"<key>RunAtLoad</key><true/>", "<key>SuccessfulExit</key><false/>", "<string>serve</string>", "/Users/a&amp;b", "<key>LOLAD_INSTALL</key><string>app</string>", "/state/logs/daemon.log"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
	if n := strings.Count(p, "<key>HOME</key>") + strings.Count(p, "<key>PATH</key>") + strings.Count(p, "<key>LOLAD_INSTALL</key>"); n != 3 {
		t.Fatalf("env keys %d", n)
	}
	// The environment dict holds exactly three keys.
	env := p[strings.Index(p, "EnvironmentVariables"):strings.Index(p, "StandardOutPath")]
	if strings.Count(env, "<key>") != 4 { // the dict's own key + 3
		t.Fatalf("unexpected env keys: %s", env)
	}
}

func TestLaunchdLifecycle(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "hunter2")
	r := &recorder{fail: map[string]bool{"launchctl print": true}}
	env := testEnv(t, r, false)
	m := &launchdManager{env}
	if err := m.Install("/usr/local/bin/lolad"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(m.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "SECRET_TOKEN") {
		t.Fatal("caller environment leaked into the plist")
	}
	if !strings.HasSuffix(m.plistPath(), "Library/LaunchAgents/dev.caceres.lolad.plist") {
		t.Fatal(m.plistPath())
	}
	want := "launchctl bootstrap gui/501 " + m.plistPath()
	if r.calls[len(r.calls)-1] != want {
		t.Fatalf("calls %v", r.calls)
	}
	st := m.Status()
	if !st.Installed || st.Mechanism != "launchd" || st.Running || st.Path != m.plistPath() {
		t.Fatalf("%+v", st)
	}
	// Loaded + running.
	r.fail = nil
	r.out = map[string]string{"launchctl print": "state = running\n"}
	if st := m.Status(); !st.Running {
		t.Fatalf("%+v", st)
	}
	if err := m.Start(); err != nil || r.calls[len(r.calls)-1] != "launchctl kickstart gui/501/dev.caceres.lolad" {
		t.Fatalf("%v %v", err, r.calls)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.plistPath()); !os.IsNotExist(err) {
		t.Fatal("plist still there")
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "launchctl bootout gui/501/dev.caceres.lolad") {
		t.Fatalf("%v", r.calls)
	}
}

func TestDaemonExe(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "lolad.exe")
	if got := daemonExe(cli, os.Stat); got != cli {
		t.Fatalf("no daemon exe present: %s", got)
	}
	d := filepath.Join(dir, "lolad-daemon.exe")
	os.WriteFile(d, nil, 0o755)
	if got := daemonExe(cli, os.Stat); got != d {
		t.Fatalf("got %s", got)
	}
	if got := daemonExe(filepath.Join(dir, "other.exe"), os.Stat); got != filepath.Join(dir, "other.exe") {
		t.Fatalf("got %s", got)
	}
}

func TestWindowsManager(t *testing.T) {
	r := &recorder{}
	env := testEnv(t, r, true) // already running: Install must not spawn
	spawned := 0
	var killed []int
	m := &windowsManager{env: env, Stat: os.Stat, Kill: func(p int) error { killed = append(killed, p); return nil }, Spawn: func(string, []string, []string) error { spawned++; return nil }}
	if err := m.Install(`C:\Users\a\AppData\Local\Programs\Lolad\bin\lolad.exe`); err != nil {
		t.Fatal(err)
	}
	want := `reg add HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v lolad /t REG_SZ /d "C:\Users\a\AppData\Local\Programs\Lolad\bin\lolad.exe" serve --supervised /f`
	if r.calls[0] != want || spawned != 0 {
		t.Fatalf("%q spawned=%d", r.calls[0], spawned)
	}
	os.MkdirAll(env.StateDir, 0o755)
	os.WriteFile(filepath.Join(env.StateDir, "supervisor.pid"), []byte("77\n"), 0o600)
	env.Health = func() (api.Health, bool) { return api.Health{Product: "lolad", PID: 4242}, len(killed) < 2 }
	m.env = env
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(killed) != 2 || killed[0] != 77 || killed[1] != 4242 {
		t.Fatalf("supervisor must die before the daemon: %v", killed)
	}
	st := m.Status()
	if st.Mechanism != "registry-run" || st.Path != `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\lolad` {
		t.Fatalf("%+v", st)
	}
}

func TestWindowsSpawnEnvIsAllowlist(t *testing.T) {
	get := func(k string) string {
		return map[string]string{"SystemRoot": `C:\Windows`, "ANTHROPIC_API_KEY": "sk-secret", "PATH": "x"}[k]
	}
	env := strings.Join(windowsSpawnEnv(get, "app"), "\n")
	if strings.Contains(env, "sk-secret") || !strings.Contains(env, "SystemRoot=") || !strings.Contains(env, "LOLAD_INSTALL=app") {
		t.Fatal(env)
	}
}

func TestSystemdUnit(t *testing.T) {
	u := renderUnit("/opt/lolad", "/home/a", "/usr/bin", "cli", "/s/logs/daemon.log")
	for _, w := range []string{`ExecStart="/opt/lolad" serve`, "Restart=on-failure", `Environment="HOME=/home/a"`, "WantedBy=default.target"} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q", w)
		}
	}
}
