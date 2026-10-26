package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/registry"
	"github.com/CamiloCaceres/lolad/internal/tmux"
)

// tmuxProbeAgent is a hidden, shell-free subprocess used only by doctor.
func tmuxProbeAgent(in io.Reader, out io.Writer) int {
	fmt.Fprintln(out, "LOLAD_PROBE_READY")
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		if scanner.Text() == "exit" {
			return 7
		}
		fmt.Fprintln(out, "ECHO:"+scanner.Text())
	}
	return 0
}

// doctorTmux never attaches to or changes the configured live server.
func doctorTmux(ctx context.Context, c registry.Config, out io.Writer) {
	bin := c.TmuxBin
	if bin == "" {
		bin = tmux.DefaultBinary()
	}
	fail := func(op string) {
		fmt.Fprintf(out, "FIX  tmux conformance %s: install a compatible tmux/psmux or set tmux_bin\n", op)
	}
	if _, err := exec.LookPath(bin); err != nil {
		fail("binary")
		return
	}
	dir, err := os.MkdirTemp("", "lolad-v4-windows-doctor-")
	if err != nil {
		fail("private state")
		return
	}
	defer os.RemoveAll(dir)
	s, err := tmux.New(dir, fmt.Sprintf("lolad-v4-windows-doctor-%d", time.Now().UnixNano()))
	if err != nil {
		fail("private server")
		return
	}
	s.Binary = bin
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, bin, "-L", s.Socket, "kill-server").Run()
	}()
	await := func(fn func() bool) bool {
		deadline := time.Now().Add(4 * time.Second)
		for {
			if fn() {
				return true
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return false
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err := s.Start("probe", dir, nil, []string{s.Exec, "tmux-probe-agent"}, 80, 24); err != nil {
		fail("StartDetachedDirSizeEnv")
		return
	}
	if !await(func() bool {
		pane, _ := s.Capture("probe", 40, true)
		return strings.Contains(pane, "LOLAD_PROBE_READY")
	}) {
		fail("CaptureEscapesJoinedHistory")
		return
	}
	if !s.Exists("probe") || s.Exists("prob") {
		fail("ExistsExactName")
		return
	}
	if pid, err := s.PanePID("probe"); err != nil || pid <= 0 {
		fail("PanePID")
		return
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0] != "probe" {
		fail("List")
		return
	}
	if err := s.SendLiteral("probe", "héllo; 'probe'X"); err != nil {
		fail("SendLiteral")
		return
	}
	if err := s.SendKeys("probe", "BSpace", "Enter"); err != nil {
		fail("SendNamedKeys")
		return
	}
	if !await(func() bool {
		pane, _ := s.Capture("probe", 40, true)
		return strings.Contains(pane, "ECHO:héllo; 'probe'")
	}) {
		fail("SendLiteralUnicodeQuotesSemicolons")
		return
	}
	if err := s.SendLiteral("probe", "exit"); err != nil {
		fail("SendLiteral")
		return
	}
	if err := s.SendKeys("probe", "Enter"); err != nil {
		fail("SendNamedKeys")
		return
	}
	if !await(func() bool { dead, status, err := s.PaneDead("probe"); return err == nil && dead && status == 7 }) {
		fail("RemainOnExitPaneDeadStatus")
		return
	}
	if err := s.Kill("probe"); err != nil || s.Exists("probe") {
		fail("KillExactSession")
		return
	}
	fmt.Fprintln(out, "OK   tmux conformance (isolated server)")
}
