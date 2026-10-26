package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func testSessions(t *testing.T) *Sessions {
	t.Helper()
	if _, e := exec.LookPath("tmux"); e != nil {
		t.Skip(e)
	}
	s, e := New(t.TempDir(), fmt.Sprintf("lolad-test-%d", time.Now().UnixNano()))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", s.Socket, "kill-server").Run() })
	return s
}
func until(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}
func TestRealSessions(t *testing.T) {
	s := testSessions(t)
	if s.Exists("agent") {
		t.Fatal("unexpected session")
	}
	if e := s.Start("agent", t.TempDir(), append(os.Environ(), "LOLAD_TEST_ARG=literal value"), []string{"/bin/sh", "-c", `printf '%s\n' "$LOLAD_TEST_ARG" "$1"; exit 7`, "sh", "[1m] $(not-a-command)"}, 80, 24); e != nil {
		t.Fatal(e)
	}
	until(t, func() bool { d, _, _ := s.PaneDead("agent"); return d })
	d, status, e := s.PaneDead("agent")
	if e != nil || !d || status != 7 {
		t.Fatalf("dead %v status %d err %v", d, status, e)
	}
	capture, e := s.Capture("agent", 40, true)
	if e != nil || !strings.Contains(capture, "literal value") || !strings.Contains(capture, "[1m] $(not-a-command)") {
		t.Fatalf("capture %q %v", capture, e)
	}
	if pid, e := s.PanePID("agent"); e != nil || pid <= 0 {
		t.Fatalf("pid %d %v", pid, e)
	}
	list, e := s.List()
	if e != nil || len(list) != 1 || list[0] != "agent" {
		t.Fatalf("list %v %v", list, e)
	}
	if e := s.Kill("agent"); e != nil {
		t.Fatal(e)
	}
}
func TestAttachAndKeys(t *testing.T) {
	s := testSessions(t)
	if e := s.Start("live", t.TempDir(), os.Environ(), []string{"/bin/cat"}, 80, 24); e != nil {
		t.Fatal(e)
	}
	if e := s.SendLiteral("live", "hello"); e != nil {
		t.Fatal(e)
	}
	if e := s.SendKeys("live", "Enter"); e != nil {
		t.Fatal(e)
	}
	until(t, func() bool { p, _ := s.Capture("live", 40, false); return strings.Count(p, "hello") >= 2 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	term, e := s.Attach(ctx, "live", true)
	if e != nil {
		t.Fatal(e)
	}
	if e := term.Resize(100, 30); e != nil {
		t.Fatal(e)
	}
	until(t, func() bool {
		flags, _ := s.run("list-clients", "-F", "#{client_flags}")
		return strings.Contains(flags, "ignore-size")
	})
	if size, e := s.run("display-message", "-p", "-t", "=live:0.0", "#{window_width}x#{window_height}"); e != nil || strings.TrimSpace(size) != "80x24" {
		t.Fatalf("read-only viewer resized the session: %q %v", size, e)
	}
	if e := term.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestEnvironmentIsolation(t *testing.T) {
	s := testSessions(t)
	t.Setenv("LOLAD_TEST_DAEMON_SECRET", "must-not-leak")
	if e := s.Start("seed", t.TempDir(), os.Environ(), []string{"/bin/sleep", "30"}, 80, 24); e != nil {
		t.Fatal(e)
	}
	if e := s.Start("safe", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, []string{"/bin/sh", "-c", `printf 'secret=%s\n' "${LOLAD_TEST_DAEMON_SECRET-unset}"`}, 80, 24); e != nil {
		t.Fatal(e)
	}
	until(t, func() bool {
		p, _ := s.Capture("safe", 40, false)
		return strings.Contains(p, "secret=") && !strings.Contains(p, "must-not-leak")
	})
}

func TestLoneExecutableWithSpaces(t *testing.T) {
	s := testSessions(t)
	path := t.TempDir() + "/cat with spaces"
	if e := os.Symlink("/bin/cat", path); e != nil {
		t.Fatal(e)
	}
	if e := s.Start("lone", t.TempDir(), os.Environ(), []string{path}, 80, 24); e != nil {
		t.Fatal(e)
	}
	if e := s.SendLiteral("lone", "direct"); e != nil {
		t.Fatal(e)
	}
	s.SendKeys("lone", "Enter")
	until(t, func() bool { p, _ := s.Capture("lone", 40, false); return strings.Count(p, "direct") >= 2 })
}

func TestWritableAttachRestoresLaunchSize(t *testing.T) {
	s := testSessions(t)
	if err := s.Start("live", t.TempDir(), os.Environ(), []string{"/bin/cat"}, 220, 50); err != nil {
		t.Fatal(err)
	}
	term, err := s.Attach(t.Context(), "live", false)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	until(t, func() bool {
		clients, _ := s.run("list-clients", "-F", "#{client_pid}")
		return strings.TrimSpace(clients) != ""
	})
	if err := term.Resize(90, 28); err != nil {
		t.Fatal(err)
	}
	size := func() string {
		v, _ := s.run("display-message", "-p", "-t", target("live"), "#{window_width}x#{window_height}")
		return strings.TrimSpace(v)
	}
	until(t, func() bool { return size() == "90x28" })
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return size() == "220x50" })
}
