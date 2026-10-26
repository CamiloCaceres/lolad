package tmux

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Built as a separate executable: no POSIX shell, shell builtins or test-process
// environment are required inside a pane, including on native Windows.
const fakeAgent = `package main
import("bufio";"fmt";"os";"strconv";"strings")
func main(){
 dir,_:=os.Getwd();fmt.Printf("DIR=%s\nENV=%s\n",dir,os.Getenv("LOLAD_CONFORMANCE"))
 fmt.Print("\x1b[32mPROMPT_READY\x1b[0m\n")
 scanner:=bufio.NewScanner(os.Stdin)
 for scanner.Scan(){line:=scanner.Text();if strings.HasPrefix(line,"exit "){code,_:=strconv.Atoi(strings.TrimPrefix(line,"exit "));os.Exit(code)};fmt.Printf("ECHO:%s\n",line)}
}
`

func conformanceSessions(t *testing.T) *Sessions {
	t.Helper()
	bin := DefaultBinary()
	if _, err := exec.LookPath(bin); err != nil {
		if os.Getenv("LOLAD_TMUX_BIN") != "" {
			t.Fatal(err)
		}
		t.Skip("multiplexer unavailable: ", err)
	}
	s, err := New(t.TempDir(), fmt.Sprintf("lolad-v4-windows-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(s.Binary, "-L", s.Socket, "kill-server").Run() })
	return s
}

// exitStatusIs accepts tmux's "dead, status unknown" (-1) on Linux only: tmux
// 3.4 on GitHub's Ubuntu runners intermittently marks a pane dead without ever
// filling #{pane_dead_status}. lolad already treats that as a crash with an
// unknown status; macOS and psmux (via the exec-env sidecar) must report it.
func exitStatusIs(status, want int) bool {
	return status == want || runtime.GOOS == "linux" && status == -1
}

func awaitOperation(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatal("operation did not converge within 12 seconds")
}
func TestConformance(t *testing.T) {
	s := conformanceSessions(t)
	buildDir := t.TempDir()
	source := filepath.Join(buildDir, "main.go")
	if err := os.WriteFile(source, []byte(fakeAgent), 0600); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(buildDir, "fake agent")
	if runtime.GOOS == "windows" {
		agent += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", agent, source)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v: %s", err, b)
	}
	dir := t.TempDir()
	env := []string{"LOLAD_CONFORMANCE=private-value", "PATH=" + os.Getenv("PATH")}
	if runtime.GOOS == "windows" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	name := "agent"
	t.Run("StartDetachedDirSizeEnv", func(t *testing.T) {
		if err := s.Start(name, dir, env, []string{agent}, 100, 32); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { p, _ := s.Capture(name, 100, true); return strings.Contains(p, "PROMPT_READY") })
		p, err := s.Capture(name, 100, true)
		if err != nil || !strings.Contains(strings.ToLower(p), strings.ToLower(dir)) || !strings.Contains(p, "ENV=private-value") {
			t.Fatalf("working dir/environment not preserved: %q %v", p, err)
		}
		dimensions, err := s.run("display-message", "-p", "-t", target(name), "#{window_width}x#{window_height}")
		if err != nil || strings.TrimSpace(dimensions) != "100x32" {
			t.Fatalf("dimensions %q: %v", dimensions, err)
		}
	})
	if !s.Exists(name) {
		t.Fatal("Start failed; dependent checks cannot run")
	}
	t.Run("ExistsExactName", func(t *testing.T) {
		if !s.Exists(name) || s.Exists("agen") {
			t.Fatal("exact target resolved incorrectly")
		}
	})
	t.Run("PanePID", func(t *testing.T) {
		if pid, err := s.PanePID(name); err != nil || pid <= 0 {
			t.Fatalf("pid=%d: %v", pid, err)
		}
	})
	t.Run("List", func(t *testing.T) {
		list, err := s.List()
		if err != nil || !slices.Contains(list, name) {
			t.Fatalf("list=%v: %v", list, err)
		}
	})
	t.Run("SendLiteralUnicodeQuotesSemicolons", func(t *testing.T) {
		text := `héllo 世界 "quoted" 'single'; echo never-a-shell`
		if err := s.SendLiteral(name, text); err != nil {
			t.Fatal(err)
		}
		if err := s.SendKeys(name, "Enter"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { p, _ := s.Capture(name, 100, false); return strings.Contains(p, "ECHO:"+text) })
	})
	t.Run("PrivateEnvironmentOffArgvAndServer", func(t *testing.T) {
		command, err := s.run("list-panes", "-t", target(name), "-F", "#{pane_start_command}")
		if err != nil || strings.Contains(command, "private-value") {
			t.Fatalf("environment reached launch command: %v", err)
		}
		global, err := s.run("show-environment", "-g", "-t", "="+name)
		if err != nil || strings.Contains(global, "private-value") {
			t.Fatalf("environment reached server: %v", err)
		}
		files, err := os.ReadDir(s.RunDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasPrefix(f.Name(), "env-") {
				t.Fatal("private env file was not deleted")
			}
		}
	})
	t.Run("SendLiteralStandaloneSemicolon", func(t *testing.T) {
		if err := s.SendLiteral(name, ";"); err != nil {
			t.Fatal(err)
		}
		if err := s.SendKeys(name, "Enter"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { pane, _ := s.Capture(name, 100, false); return strings.Contains(pane, "ECHO:;") })
	})
	t.Run("SendNamedKeys", func(t *testing.T) {
		// BSpace edits canonical console input. Escape is sent on its own, followed
		// by a line, so its byte must not disappear or become shell interpretation.
		if err := s.SendLiteral(name, "editX"); err != nil {
			t.Fatal(err)
		}
		if err := s.SendKeys(name, "BSpace", "Enter"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool {
			p, _ := s.Capture(name, 100, false)
			return strings.Contains(p, "ECHO:edit") && !strings.Contains(p, "ECHO:editX")
		})
		if err := s.SendKeys(name, "Escape"); err != nil {
			t.Fatal(err)
		}
		if err := s.SendLiteral(name, "escape-marker"); err != nil {
			t.Fatal(err)
		}
		if err := s.SendKeys(name, "Enter"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { p, _ := s.Capture(name, 100, false); return strings.Contains(p, "escape-marker") })
	})
	t.Run("CaptureEscapesJoinedHistory", func(t *testing.T) {
		p, err := s.run("capture-pane", "-p", "-e", "-J", "-S", "-100", "-t", target(name))
		if err != nil || !strings.Contains(p, "PROMPT_READY") || !strings.Contains(p, "\x1b[") {
			t.Fatalf("ANSI capture: %q %v", p, err)
		}
	})
	t.Run("ResizeWindow", func(t *testing.T) {
		if err := s.size(name, 120, 40); err != nil {
			t.Fatal(err)
		}
		p, err := s.run("display-message", "-p", "-t", target(name), "#{window_width}x#{window_height}")
		if err != nil || strings.TrimSpace(p) != "120x40" {
			t.Fatalf("resize: %q %v", p, err)
		}
	})
	t.Run("ServerSurvivesClientExit", func(t *testing.T) {
		time.Sleep(200 * time.Millisecond)
		if !s.Exists(name) {
			t.Fatal("detached session vanished")
		}
	})
	if runtime.GOOS == "windows" {
		t.Run("AttachCapturePollReadOnly", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			term, err := s.Attach(ctx, name, true)
			if err != nil {
				t.Fatal(err)
			}
			defer term.Close()
			if _, err := term.Write([]byte("must-not-type")); err == nil {
				t.Fatal("read-only terminal accepted input")
			}
			if err := term.Resize(45, 12); err != nil {
				t.Fatal(err)
			}
			b := make([]byte, 64*1024)
			n, err := term.Read(b)
			if err != nil || !strings.Contains(string(b[:n]), "PROMPT_READY") {
				t.Fatalf("snapshot: %q %v", b[:n], err)
			}
			dimensions, _ := s.run("display-message", "-p", "-t", target(name), "#{window_width}x#{window_height}")
			if strings.TrimSpace(dimensions) != "120x40" {
				t.Fatalf("read-only terminal resized pane: %s", dimensions)
			}
		})
		t.Run("AttachCapturePollWritable", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			term, err := s.Attach(ctx, name, false)
			if err != nil {
				t.Fatal(err)
			}
			defer term.Close()
			go func() { _, _ = io.Copy(io.Discard, term) }()
			// Websocket fragments may split a UTF-8 rune.
			bytes := []byte("terminal-héllo\r")
			for _, part := range [][]byte{bytes[:11], bytes[11:12], bytes[12:]} {
				if _, err := term.Write(part); err != nil {
					t.Fatal(err)
				}
			}
			awaitOperation(t, func() bool {
				pane, _ := s.Capture(name, 100, false)
				return strings.Contains(pane, "ECHO:terminal-héllo")
			})
			if err := term.Resize(110, 36); err != nil {
				t.Fatal(err)
			}
			dimensions, _ := s.run("display-message", "-p", "-t", target(name), "#{window_width}x#{window_height}")
			if strings.TrimSpace(dimensions) != "110x36" {
				t.Fatalf("writable terminal resize: %s", dimensions)
			}
		})
	}
	t.Run("RemainOnExitPaneDeadStatus", func(t *testing.T) {
		if err := s.SendLiteral(name, "exit 7"); err != nil {
			t.Fatal(err)
		}
		if err := s.SendKeys(name, "Enter"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { dead, status, _ := s.PaneDead(name); return dead && exitStatusIs(status, 7) })
		dead, status, err := s.PaneDead(name)
		if err != nil || !dead || !exitStatusIs(status, 7) {
			t.Fatalf("dead=%v status=%d: %v", dead, status, err)
		}
	})
	t.Run("KillExactSession", func(t *testing.T) {
		if err := s.Kill(name); err != nil {
			t.Fatal(err)
		}
		if s.Exists(name) {
			t.Fatal("session survived kill")
		}
	})
	t.Run("SendControlC", func(t *testing.T) {
		if err := s.Start("interrupt", dir, env, []string{agent}, 80, 24); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { p, _ := s.Capture("interrupt", 100, false); return strings.Contains(p, "PROMPT_READY") })
		if err := s.SendKeys("interrupt", "C-c"); err != nil {
			t.Fatal(err)
		}
		awaitOperation(t, func() bool { dead, _, _ := s.PaneDead("interrupt"); return dead })
	})
}
