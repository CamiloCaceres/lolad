//go:build !windows

package tmux

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/creack/pty"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

type terminal struct {
	*os.File
	cmd      *exec.Cmd
	once     sync.Once
	err      error
	readOnly bool
	sessions *Sessions
	name     string
	done     chan struct{}
}

func (t *terminal) Resize(cols, rows uint16) error {
	if t.readOnly {
		return nil
	}
	if err := pty.Setsize(t.File, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return err
	}
	_, err := t.sessions.run("resize-window", "-t", windowTarget(t.name), "-x", strconv.Itoa(int(cols)), "-y", strconv.Itoa(int(rows)))
	return err
}
func (t *terminal) Close() error {
	t.once.Do(func() {
		t.err = t.File.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		<-t.done
	})
	return t.err
}
func (s *Sessions) Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error) {
	args := []string{"attach"}
	if readOnly {
		args = append(args, "-r", "-f", "ignore-size")
	}
	args = append(args, "-t", "="+name)
	cmd := s.command(ctx, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, e := pty.Start(cmd)
	if e != nil {
		return nil, e
	}
	t := &terminal{File: f, cmd: cmd, readOnly: readOnly, sessions: s, name: name, done: make(chan struct{})}
	go func() {
		defer close(t.done)
		_ = cmd.Wait()
		if !readOnly {
			s.restoreSize(name)
		}
	}()
	return t, nil
}
