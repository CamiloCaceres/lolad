package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// superviseOptions drive the `serve --supervised` watchdog. Everything that
// touches the OS or the clock is injectable so the policy is testable.
type superviseOptions struct {
	// Start runs the child to completion and returns its exit code.
	Start func(ctx context.Context) (int, error)
	Log   io.Writer
	Now   func() time.Time
	// Sleep waits d; it returns false when ctx ended first.
	Sleep      func(ctx context.Context, d time.Duration) bool
	BaseDelay  time.Duration // first restart delay (1s)
	MaxDelay   time.Duration // cap (60s)
	MaxCrashes int           // give up at this many crashes... (10)
	Window     time.Duration // ...inside this window (10 min)
	StableRun  time.Duration // a run this long resets the backoff
}

func defaultSuperviseOptions(start func(context.Context) (int, error), log io.Writer) superviseOptions {
	return superviseOptions{
		Start: start, Log: log, Now: time.Now,
		Sleep: func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return true
			case <-ctx.Done():
				return false
			}
		},
		BaseDelay: time.Second, MaxDelay: 60 * time.Second, MaxCrashes: 10, Window: 10 * time.Minute, StableRun: 60 * time.Second,
	}
}

// supervise restarts the child after crashes. It returns 0 after a clean child
// exit or cancellation and 1 once it gives up.
func supervise(ctx context.Context, o superviseOptions) int {
	logf := func(format string, a ...any) {
		fmt.Fprintf(o.Log, "%s lolad supervisor: %s\n", o.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
	}
	delay := o.BaseDelay
	var crashes []time.Time
	for {
		started := o.Now()
		code, err := o.Start(ctx)
		if ctx.Err() != nil {
			logf("stopping")
			return 0
		}
		if err == nil && code == 0 {
			logf("serve exited cleanly; not restarting")
			return 0
		}
		now := o.Now()
		if now.Sub(started) >= o.StableRun {
			delay = o.BaseDelay
		}
		crashes = append(crashes, now)
		keep := crashes[:0]
		for _, t := range crashes {
			if now.Sub(t) <= o.Window {
				keep = append(keep, t)
			}
		}
		crashes = keep
		reason := fmt.Sprintf("exit code %d", code)
		if err != nil {
			reason = err.Error()
		}
		if len(crashes) >= o.MaxCrashes {
			logf("giving up: serve crashed %d times in %s (last: %s); fix the cause and start lolad again", len(crashes), o.Window, reason)
			return 1
		}
		logf("serve crashed (%s); restarting in %s (%d of %d crashes in %s)", reason, delay, len(crashes), o.MaxCrashes, o.Window)
		if !o.Sleep(ctx, delay) {
			logf("stopping")
			return 0
		}
		if delay *= 2; delay > o.MaxDelay {
			delay = o.MaxDelay
		}
	}
}

// serveChild runs `<this executable> serve` and waits for it.
func serveChild(ctx context.Context) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, exe, "serve")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	hideWindow(cmd)
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil { // Windows cannot signal
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 20 * time.Second
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func writePIDFile(path string) func() {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
	return func() { _ = os.Remove(path) }
}
