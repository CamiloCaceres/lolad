package tmux

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// The test binary doubles as `lolad exec-env` so Start can be tested end to end.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "exec-env" {
		err := ExecEnv(os.Args[2:])
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(127)
	}
	os.Exit(m.Run())
}

func TestEnvFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	env := []string{"A=1", "MULTI=line one\nline two", "EMPTY=", "EQ=a=b"}
	file, err := writeEnvFile(dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	got, err := readEnvFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, env) {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("env file not removed after reading")
	}
}

func TestChildEnvKeepsPaneVariables(t *testing.T) {
	get := func(k string) string {
		return map[string]string{"TERM": "tmux-256color", "TMUX_PANE": "%1", "SECRET": "x"}[k]
	}
	got := childEnv([]string{"PATH=/bin", "TERM=xterm"}, get)
	if !slices.Contains(got, "TERM=xterm") || !slices.Contains(got, "TMUX_PANE=%1") || slices.Contains(got, "TERM=tmux-256color") {
		t.Fatalf("got %q", got)
	}
	for _, v := range got {
		if strings.HasPrefix(v, "SECRET=") {
			t.Fatal("non-pane variable passed through")
		}
	}
}

func TestPrivateStartKeepsSecretsOffArgvAndServer(t *testing.T) {
	s := testSessions(t)
	s.Exec, _ = os.Executable()
	t.Setenv("LOLAD_TEST_DAEMON_SECRET", "daemon-value")
	const secret = "agent-secret-value-4242"
	env := []string{"PATH=/usr/bin:/bin", "AGENT_TOKEN=" + secret}
	if e := s.Start("priv", t.TempDir(), env, []string{"/bin/sh", "-c", `printf 'tok=%s daemon=%s\n' "$AGENT_TOKEN" "${LOLAD_TEST_DAEMON_SECRET-unset}"; sleep 30`}, 80, 24); e != nil {
		t.Fatal(e)
	}
	until(t, func() bool {
		p, _ := s.Capture("priv", 40, false)
		return strings.Contains(p, "tok="+secret+" daemon=unset")
	})
	cmd, err := s.run("list-panes", "-t", "=priv", "-F", "#{pane_start_command}")
	if err != nil || strings.Contains(cmd, secret) || !strings.Contains(cmd, "exec-env") {
		t.Fatalf("start command %q err %v", cmd, err)
	}
	global, _ := s.run("show-environment", "-g")
	session, _ := s.run("show-environment", "-t", "=priv")
	if strings.Contains(global+session, secret) || strings.Contains(global+session, "daemon-value") {
		t.Fatal("secret reached the tmux environment")
	}
	if left, _ := os.ReadDir(s.RunDir); len(left) != 0 {
		t.Fatalf("env files left behind: %d", len(left))
	}
}

func TestPrivateStartLoneExecutable(t *testing.T) {
	s := testSessions(t)
	s.Exec, _ = os.Executable()
	path := t.TempDir() + "/cat with spaces"
	if e := os.Symlink("/bin/cat", path); e != nil {
		t.Fatal(e)
	}
	if e := s.Start("lone", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, []string{path}, 80, 24); e != nil {
		t.Fatal(e)
	}
	if e := s.SendLiteral("lone", "direct"); e != nil {
		t.Fatal(e)
	}
	s.SendKeys("lone", "Enter")
	until(t, func() bool { p, _ := s.Capture("lone", 40, false); return strings.Count(p, "direct") >= 2 })
}
