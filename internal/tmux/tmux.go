// Package tmux owns lolad's private tmux server.
package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type Sessions struct {
	Socket, Config, Binary string
	// Exec is the lolad binary used as `lolad exec-env` to hand agents their
	// environment through a private file instead of argv. New defaults to the
	// current executable; tests provide the same exec-env entry point.
	Exec   string
	RunDir string // where the private env files go
}

var _ core.Sessions = (*Sessions)(nil)

func New(stateDir, socket string) (*Sessions, error) {
	if socket == "" {
		socket = "lolad"
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "tmux.conf")
	conf := "set -g base-index 0\nset -g pane-base-index 0\nset -g remain-on-exit on\nset -g history-limit 50000\nset -g status off\nset -g mouse off\nset -s escape-time 0\n"
	if err := os.WriteFile(path, []byte(conf), 0600); err != nil {
		return nil, err
	}
	return &Sessions{Socket: socket, Config: path, Binary: DefaultBinary(), Exec: currentExecutable(), RunDir: filepath.Join(stateDir, "run")}, nil
}
func (s *Sessions) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, s.Binary, append([]string{"-L", s.Socket, "-f", s.Config}, args...)...)
}
func (s *Sessions) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b, e := s.command(ctx, args...).CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], e, b)
	}
	return string(b), nil
}
func windowTarget(name string) string { return "=" + name + ":0" }
func target(name string) string       { return "=" + name + ":0.0" }
func (s *Sessions) Exists(name string) bool {
	_, e := s.run("has-session", "-t", "="+name)
	return e == nil
}
func (s *Sessions) List() ([]string, error) {
	out, e := s.run("list-sessions", "-F", "#{session_name}")
	if e != nil {
		if strings.Contains(e.Error(), "no server running") || strings.Contains(e.Error(), "error connecting") {
			return nil, nil
		}
		return nil, e
	}
	return strings.Fields(out), nil
}
func (s *Sessions) Start(name, dir string, env, argv []string, cols, rows int) error {
	if len(argv) == 0 {
		return fmt.Errorf("%w: empty argv", core.ErrInvalid)
	}
	if cols <= 0 {
		cols = 220
	}
	if rows <= 0 {
		rows = 50
	}
	args := []string{"new-session", "-d", "-s", name, "-c", dir, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)}
	return s.startPrivate(name, args, env, argv, cols, rows)
}

// startPrivate launches through `lolad exec-env` so no environment value
// reaches argv or the tmux server. The env file is removed by the helper, or
// here when tmux fails before running it.
func (s *Sessions) startPrivate(name string, args, env, argv []string, cols, rows int) error {
	if s.Exec == "" {
		return fmt.Errorf("%w: no exec-env helper (a test binary must call tmux.ServeExecEnv in TestMain)", core.ErrInvalid)
	}
	file, err := writeEnvFile(s.RunDir, env)
	if err != nil {
		return fmt.Errorf("agent env: %w", err)
	}
	args = append(args, "--", s.Exec, "exec-env", file)
	args = append(args, exitStatusArgs(s.RunDir, name)...)
	args = append(args, "--")
	args = append(args, argv...)
	cmd := s.command(context.Background(), args...)
	cmd.Env = baseEnv(os.Getenv)
	if out, e := cmd.CombinedOutput(); e != nil {
		os.Remove(file)
		return fmt.Errorf("tmux new-session: %w: %s", e, out)
	}
	return s.size(name, cols, rows)
}

// baseEnv is the environment the tmux client (and so a newly started tmux
// server) gets: enough to run, nothing secret.
func baseEnv(getenv func(string) string) []string {
	var out []string
	for _, k := range []string{"HOME", "USER", "LOGNAME", "PATH", "SHELL", "LANG", "LC_ALL", "TMPDIR", "TMUX_TMPDIR", "USERPROFILE", "SystemRoot", "SYSTEMROOT", "COMSPEC", "TEMP", "TMP", "PATHEXT", "APPDATA", "LOCALAPPDATA"} {
		if v := getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func (s *Sessions) size(name string, cols, rows int) error {
	// Keep detached windows independent of the last attached client's dimensions.
	for _, option := range [][2]string{{"window-size", "manual"}, {"@lolad-cols", strconv.Itoa(cols)}, {"@lolad-rows", strconv.Itoa(rows)}} {
		if _, err := s.run("set-option", "-w", "-t", windowTarget(name), option[0], option[1]); err != nil {
			return err
		}
	}
	_, e := s.run("resize-window", "-t", windowTarget(name), "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	return e
}

func (s *Sessions) PanePID(name string) (int, error) {
	v, e := s.run("display-message", "-p", "-t", target(name), "#{pane_pid}")
	if e != nil {
		return 0, e
	}
	return strconv.Atoi(strings.TrimSpace(v))
}
func (s *Sessions) PaneDead(name string) (bool, int, error) {
	v, e := s.run("display-message", "-p", "-t", target(name), "#{pane_dead}:#{pane_dead_status}:#{pane_dead_signal}")
	if e != nil {
		return false, 0, e
	}
	p := strings.Split(strings.TrimSpace(v), ":")
	if len(p) != 3 {
		return false, 0, fmt.Errorf("invalid pane state %q", v)
	}
	if p[0] != "1" {
		return false, 0, nil
	}
	status, err := strconv.Atoi(p[1])
	if err != nil {
		if signal, e := strconv.Atoi(p[2]); e == nil && signal > 0 {
			status = -signal
		} else {
			status = -1 // killed by a signal or exit metadata not yet available
		}
	}
	return true, s.exitStatus(name, status), nil
}
func (s *Sessions) Kill(name string) error {
	_, e := s.run("kill-session", "-t", "="+name)
	return e
}
func (s *Sessions) SendLiteral(name, text string) error {
	// The tmux command-list parser consumes a trailing semicolon even after
	// -- and -l. Hex transport avoids interpreting pasted text as a command.
	if strings.HasSuffix(text, ";") {
		args := []string{"send-keys", "-t", target(name), "-H"}
		for _, b := range []byte(text) {
			args = append(args, fmt.Sprintf("%02x", b))
		}
		_, err := s.run(args...)
		return err
	}
	_, e := s.run("send-keys", "-t", target(name), "-l", "--", text)
	return e
}
func (s *Sessions) SendKeys(name string, keys ...string) error {
	_, e := s.run(append([]string{"send-keys", "-t", target(name), "--"}, keys...)...)
	return e
}
func (s *Sessions) Capture(name string, lines int, esc bool) (string, error) {
	if lines <= 0 {
		lines = 40
	}
	args := []string{"capture-pane", "-p", "-J", "-t", target(name), "-S", "-" + strconv.Itoa(lines)}
	if esc {
		args = append(args, "-e")
	}
	out, e := s.run(args...)
	if e != nil {
		return "", e
	}
	all := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func (s *Sessions) restoreSize(name string) {
	cols, rows := "220", "50"
	if v, err := s.run("show-option", "-wqv", "-t", windowTarget(name), "@lolad-cols"); err == nil && strings.TrimSpace(v) != "" {
		cols = strings.TrimSpace(v)
	}
	if v, err := s.run("show-option", "-wqv", "-t", windowTarget(name), "@lolad-rows"); err == nil && strings.TrimSpace(v) != "" {
		rows = strings.TrimSpace(v)
	}
	_, _ = s.run("set-option", "-w", "-t", windowTarget(name), "window-size", "manual")
	_, _ = s.run("resize-window", "-t", windowTarget(name), "-x", cols, "-y", rows)
}
