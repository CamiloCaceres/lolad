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
	"sync"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/creack/pty"
)

type Sessions struct{ Socket, Config, Binary string }

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
	return &Sessions{Socket: socket, Config: path, Binary: "tmux"}, nil
}
func (s *Sessions) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, s.Binary, append([]string{"-L", s.Socket, "-f", s.Config}, args...)...)
}
func (s *Sessions) run(args ...string) (string, error) {
	b, e := s.command(context.Background(), args...).CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], e, b)
	}
	return string(b), nil
}
func target(name string) string { return "=" + name + ":0.0" }
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
	provided := make(map[string]bool)
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		provided[k] = true
		args = append(args, "-e", v)
	}
	// Existing private servers retain their original environment. Explicitly
	// blank absent values per session so daemon credentials cannot leak through.
	inherited := os.Environ()
	if global, err := s.run("show-environment", "-g"); err == nil {
		inherited = append(inherited, strings.Split(global, "\n")...)
	}
	removed := make(map[string]bool)
	for _, v := range inherited {
		k, _, ok := strings.Cut(v, "=")
		if ok && !provided[k] && !removed[k] {
			args = append(args, "-e", k+"=")
			removed[k] = true
		}
	}
	args = append(args, "--")
	// tmux treats a lone command argument as shell text. env execs a lone
	// executable directly without changing its argv or invoking a shell.
	if len(argv) == 1 {
		args = append(args, "/usr/bin/env", "--")
	}
	args = append(args, argv...)
	cmd := s.command(context.Background(), args...)
	cmd.Env = append([]string{}, env...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("tmux new-session: %w: %s", e, out)
	}
	return nil
}
func (s *Sessions) PanePID(name string) (int, error) {
	v, e := s.run("display-message", "-p", "-t", target(name), "#{pane_pid}")
	if e != nil {
		return 0, e
	}
	return strconv.Atoi(strings.TrimSpace(v))
}
func (s *Sessions) PaneDead(name string) (bool, int, error) {
	v, e := s.run("display-message", "-p", "-t", target(name), "#{pane_dead}:#{pane_dead_status}")
	if e != nil {
		return false, 0, e
	}
	p := strings.Split(strings.TrimSpace(v), ":")
	status, _ := strconv.Atoi(p[1])
	return p[0] == "1", status, nil
}
func (s *Sessions) Kill(name string) error {
	_, e := s.run("kill-session", "-t", "="+name)
	return e
}
func (s *Sessions) SendLiteral(name, text string) error {
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
	args := []string{"capture-pane", "-p", "-t", target(name), "-S", "-" + strconv.Itoa(lines)}
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

type terminal struct {
	*os.File
	cmd  *exec.Cmd
	once sync.Once
	err  error
}

func (t *terminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.File, &pty.Winsize{Cols: cols, Rows: rows})
}
func (t *terminal) Close() error {
	t.once.Do(func() {
		t.err = t.File.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		_ = t.cmd.Wait()
	})
	return t.err
}
func (s *Sessions) Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error) {
	args := []string{"attach"}
	if readOnly {
		args = append(args, "-r")
	}
	args = append(args, "-t", "="+name)
	cmd := s.command(ctx, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, e := pty.Start(cmd)
	if e != nil {
		return nil, e
	}
	return &terminal{File: f, cmd: cmd}, nil
}
