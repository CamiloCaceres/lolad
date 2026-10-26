//go:build !windows

package tmux

import (
	"os"
	"strings"
	"testing"
)

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
