package main

import (
	"path/filepath"
	"strings"
	"time"
)

// stopAppDaemon is the updater's StopDaemon (macOS, after the new bundle is
// in place): launchd does not manage this daemon, so stop it and let the
// relaunched app start the new one. Only a daemon running from this app's
// bundle is touched; one started from the CLI (Homebrew, a terminal, PM2)
// is not ours to stop. Agents keep running in the tmux server either way.
func stopAppDaemon() error {
	if sh == nil {
		return nil
	}
	return stopDaemonFrom(sh.baseURL(), currentBundle(), fetchHealth, terminate, 10*time.Second)
}

func stopDaemonFrom(base, bundle string, health func(string) *Health, kill func(int) error, wait time.Duration) error {
	h := health(base)
	if h == nil || h.PID <= 0 || bundle == "" || !insideBundle(h.Exe, bundle) {
		return nil
	}
	if err := kill(h.PID); err != nil {
		return err
	}
	for end := time.Now().Add(wait); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if health(base) == nil {
			return nil
		}
	}
	return nil // the relaunched app copes with a slow exit: it waits for the port
}

func insideBundle(exe, bundle string) bool {
	exe, bundle = filepath.Clean(exe), filepath.Clean(bundle)
	return strings.HasPrefix(exe, bundle+string(filepath.Separator))
}
