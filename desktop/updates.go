package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

// The update flow behind Settings → Updates and the tray item. One state,
// three views of it: the tray label, the Settings section (pushed through the
// bridge) and the "ready" notification. It has no UI code, so tests drive it
// against a fake update server.

// Update phases, as the Settings page sees them.
const (
	phIdle        = "idle"        // not checked since launch (LastChecked may be from before)
	phChecking    = "checking"    // asking the update server
	phUpToDate    = "uptodate"    // the latest version is installed
	phAvailable   = "available"   // a newer version exists; not downloaded
	phDownloading = "downloading" // fetching and verifying it
	phReady       = "ready"       // downloaded and verified; restart to finish
	phRestarting  = "restarting"  // installing; the app is about to quit
	phFailed      = "failed"      // Error says why; Retry says which step to repeat
	phManual      = "manual"      // too old to update in place: download the installer
	phOnboarding  = "onboarding"  // an update exists, but setup is not finished
	phUpdated     = "updated"     // this launch is the restart that finished an update
	phUnavailable = "unavailable" // this build cannot update itself (Error says why)
)

// UpdateState is what the Settings section renders. Its JSON is the bridge's
// contract with internal/web/static/updates.js.
type UpdateState struct {
	Phase         string `json:"phase"`
	Current       string `json:"current"`
	Version       string `json:"version,omitempty"`
	Notes         string `json:"notes,omitempty"`
	NotesURL      string `json:"notes_url,omitempty"`
	AgentsRestart bool   `json:"agents_restart,omitempty"`
	Percent       int    `json:"percent"`
	Error         string `json:"error,omitempty"`
	Retry         string `json:"retry,omitempty"` // check | download | restart
	LastChecked   string `json:"last_checked,omitempty"`
}

// updateBackend is the part of *updater.Updater the flow uses.
type updateBackend interface {
	Check(ctx context.Context) (*updater.CheckResult, error)
	Download(ctx context.Context, res *updater.CheckResult, progress func(done, total int64)) (string, error)
	Verify(path string) error
	Apply(path string) error
	OnboardingInProgress() bool
}

type updateFlow struct {
	b        updateBackend
	stateDir string // update-check.json and update-reopen live here
	now      func() time.Time
	onChange func(UpdateState)        // called after every change, outside the lock
	notify   func(title, body string) // background "ready" notice

	mu   sync.Mutex
	st   UpdateState
	res  *updater.CheckResult
	path string // the verified download, once ready
	busy bool
}

func newUpdateFlow(b updateBackend, current, stateDir string) *updateFlow {
	f := &updateFlow{b: b, stateDir: stateDir, now: time.Now,
		onChange: func(UpdateState) {}, notify: func(string, string) {}}
	f.st = UpdateState{Phase: phIdle, Current: current, LastChecked: f.loadLastChecked()}
	if b == nil {
		f.st.Phase = phUnavailable
	}
	return f
}

// State returns a copy of the current state.
func (f *updateFlow) State() UpdateState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *updateFlow) set(mut func(*UpdateState)) {
	f.mu.Lock()
	mut(&f.st)
	st := f.st
	f.mu.Unlock()
	f.onChange(st)
}

// begin claims the flow for one step when ok accepts the current state;
// false while another step runs.
func (f *updateFlow) begin(ok func(UpdateState) bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy || f.b == nil || !ok(f.st) {
		return false
	}
	f.busy = true
	return true
}

func (f *updateFlow) end() { f.mu.Lock(); f.busy = false; f.mu.Unlock() }

// Check asks the update server. background is the daily check: it leaves a
// downloaded update alone and, when it finds one, downloads it and sends the
// "ready" notice (docs/desktop.md §9). A manual check stops at "available"
// so the user chooses when to download.
func (f *updateFlow) Check(ctx context.Context, background bool) {
	// A verified update waiting for a restart is kept: checking again would
	// only find the same version.
	if !f.begin(func(s UpdateState) bool { return s.Phase != phReady && s.Phase != phRestarting }) {
		return
	}
	f.set(func(s *UpdateState) {
		*s = UpdateState{Phase: phChecking, Current: s.Current, LastChecked: s.LastChecked}
	})
	res, err := f.b.Check(ctx)
	if errors.Is(err, updater.ErrDowngrade) {
		// The published version is older than this one (a newer beta or a
		// dev build): there is nothing to update to.
		res, err = &updater.CheckResult{}, nil
	}
	if err != nil {
		f.fail("check", err)
		f.end()
		return
	}
	checked := f.now().UTC().Format(time.RFC3339)
	f.saveLastChecked(checked)
	f.mu.Lock()
	f.res = res
	f.mu.Unlock()
	f.set(func(s *UpdateState) {
		s.LastChecked = checked
		s.Version, s.Notes, s.NotesURL, s.AgentsRestart = res.Version, res.Notes, res.NotesURL, res.NeedsAgentRestart
		switch {
		case !res.Available:
			s.Phase, s.Version, s.Notes, s.NotesURL = phUpToDate, "", "", ""
		case res.ManualInstall:
			s.Phase = phManual
		case res.OnboardingBlocked:
			s.Phase = phOnboarding
		default:
			s.Phase = phAvailable
		}
	})
	f.end()
	if background && f.State().Phase == phAvailable {
		f.Download(ctx)
		if st := f.State(); st.Phase == phReady {
			f.notify("Lolad "+st.Version+" is ready", "Restart Lolad to finish updating. Your assistants keep their work.")
		}
	}
}

// Download fetches the update found by Check, checks its hash and code
// signature, and leaves it ready for Restart.
func (f *updateFlow) Download(ctx context.Context) {
	if !f.begin(func(s UpdateState) bool {
		return s.Phase == phAvailable || s.Phase == phOnboarding || (s.Phase == phFailed && s.Retry == "download")
	}) {
		return
	}
	defer f.end()
	f.mu.Lock()
	res := f.res
	f.mu.Unlock()
	if res == nil || !res.Available || res.ManualInstall {
		return
	}
	if f.b.OnboardingInProgress() {
		f.set(func(s *UpdateState) { s.Phase, s.Error, s.Retry = phOnboarding, "", "" })
		return
	}
	f.set(func(s *UpdateState) { s.Phase, s.Percent, s.Error, s.Retry = phDownloading, 0, "", "" })
	last := -1
	path, err := f.b.Download(ctx, res, func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct > 99 {
			pct = 99 // 100 once verified
		}
		if pct != last {
			last = pct
			f.set(func(s *UpdateState) { s.Percent = pct })
		}
	})
	if err == nil {
		if err = f.b.Verify(path); err != nil {
			os.RemoveAll(filepath.Dir(path))
		}
	}
	if err != nil {
		f.fail("download", err)
		return
	}
	f.mu.Lock()
	f.path = path
	f.mu.Unlock()
	f.set(func(s *UpdateState) { s.Phase, s.Percent = phReady, 100 })
}

// Restart installs the verified update. On success the app quits and the
// new version starts (macOS: the daemon is restarted too; Windows: the
// installer restarts both); the reopen marker brings the window back on
// Settings → Updates.
func (f *updateFlow) Restart() {
	if !f.begin(func(s UpdateState) bool {
		return s.Phase == phReady || (s.Phase == phFailed && s.Retry == "restart")
	}) {
		return
	}
	defer f.end()
	f.mu.Lock()
	path, st := f.path, f.st
	f.mu.Unlock()
	if path == "" {
		return
	}
	if f.b.OnboardingInProgress() {
		f.set(func(s *UpdateState) {
			s.Phase, s.Error, s.Retry = phFailed, "Finish setting up Lolad first. The update will wait.", "restart"
		})
		return
	}
	f.set(func(s *UpdateState) { s.Phase, s.Error, s.Retry = phRestarting, "", "" })
	marker := filepath.Join(f.stateDir, reopenMarker)
	_ = os.MkdirAll(f.stateDir, 0o700)
	_ = os.WriteFile(marker, []byte(st.Version+"\n"), 0o600)
	if err := f.b.Apply(path); err != nil {
		os.Remove(marker)
		f.fail("restart", err)
		return
	}
}

// Retry repeats the step that failed.
func (f *updateFlow) Retry(ctx context.Context) {
	switch f.State().Retry {
	case "download":
		f.Download(ctx)
	case "restart":
		f.Restart()
	default:
		f.Check(ctx, false)
	}
}

func (f *updateFlow) fail(step string, err error) {
	f.set(func(s *UpdateState) { s.Phase, s.Error, s.Retry, s.Percent = phFailed, updateReason(err), step, 0 })
}

// updateReason turns an updater error into one plain sentence.
func updateReason(err error) string {
	var ne net.Error
	var dns *net.DNSError
	var op *net.OpError
	msg := err.Error()
	switch {
	case errors.Is(err, updater.ErrBadSignature):
		return "The update didn't pass Lolad's signature check, so it wasn't used."
	case errors.Is(err, updater.ErrChecksum):
		return "The download was damaged or incomplete. Try again."
	case errors.Is(err, updater.ErrSignatureInvalid):
		return "The new version isn't signed by the same publisher, so it wasn't installed."
	case errors.Is(err, updater.ErrNoPlatform):
		return "This version has no build for this computer yet."
	case errors.Is(err, updater.ErrOnboarding):
		return "Finish setting up Lolad first. The update will wait."
	case errors.Is(err, updater.ErrManualInstall):
		return "This version is too old to update in place. Download the new installer."
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &dns), errors.As(err, &op), errors.As(err, &ne):
		return "Couldn't reach the update server. Check your internet connection."
	case strings.Contains(msg, "HTTP "):
		i := strings.Index(msg, "HTTP ")
		return "The update server answered with an error (" + strings.TrimSpace(msg[i:]) + ")."
	case errors.Is(err, os.ErrPermission) || strings.Contains(msg, "permission denied") || strings.Contains(msg, "Access is denied"):
		return "Lolad isn't allowed to replace its own files here. Move Lolad to your Applications folder (or reinstall it), then try again."
	case strings.Contains(msg, "no space left"):
		return "There isn't enough free disk space for the update."
	}
	return "Something went wrong: " + clip(msg, 160)
}

// ---------------------------------------------------------------- persistence

const (
	lastCheckFile = "update-check.json"
	reopenMarker  = "update-reopen"
)

func (f *updateFlow) loadLastChecked() string {
	b, err := os.ReadFile(filepath.Join(f.stateDir, lastCheckFile))
	if err != nil {
		return ""
	}
	var v struct {
		LastChecked string `json:"last_checked"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, v.LastChecked); err != nil {
		return ""
	}
	return v.LastChecked
}

func (f *updateFlow) saveLastChecked(ts string) {
	b, _ := json.Marshal(map[string]string{"last_checked": ts})
	_ = os.MkdirAll(f.stateDir, 0o700)
	_ = os.WriteFile(filepath.Join(f.stateDir, lastCheckFile), b, 0o600)
}

// TakeReopen reports whether this launch finishes an update the user
// started (the marker is fresh), and removes the marker either way. The
// state moves to "updated".
func (f *updateFlow) TakeReopen() bool {
	p := filepath.Join(f.stateDir, reopenMarker)
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	os.Remove(p)
	if f.now().Sub(st.ModTime()) > 15*time.Minute {
		return false
	}
	f.set(func(s *UpdateState) { s.Phase = phUpdated })
	return true
}
