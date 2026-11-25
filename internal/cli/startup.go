package cli

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

// startupWait is how long reading the agent folders may take before the
// daemon says it is waiting for something (on macOS, a privacy dialog).
const startupWait = 5 * time.Second

// startup answers on the dashboard port while the app is built, then hands
// every request to the real handler.
//
// Building the app reads every agent folder. On macOS a folder on another
// drive (or in Documents, Desktop, ...) is behind a privacy permission, and a
// changed signature, as after an ad-hoc signed update, asks again: the read
// blocks until someone clicks Allow, for as long as that takes. Before this
// gate the port accepted connections and never answered, so the app and the
// tray could only say "lolad is not running", and a second start took the
// silent port for another program. Now /api/health answers at once, with a
// state that names the drive macOS is asking about.
type startup struct {
	base  api.Health
	began time.Time
	goos  string
	wait  time.Duration
	now   func() time.Time

	mu      sync.Mutex
	ready   http.Handler
	pending map[string]time.Time // folder -> when its probe started
}

func newStartup(version, stateDir string, began time.Time) *startup {
	h := api.NewHealth(version, stateDir, began)
	return &startup{base: h, began: began, goos: runtime.GOOS, wait: startupWait, now: time.Now, pending: map[string]time.Time{}}
}

// startupFolders is what building the app reads first: the agent roots and
// the listed agent folders (registry.Reload).
func startupFolders(c registry.Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range append(append([]string(nil), c.AgentsDirs...), c.Agents...) {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// probe reads each folder in its own goroutine, alongside the app's own
// reads, and remembers the ones that have not come back. A read that macOS
// holds for a permission stays pending until the user answers; it is the
// same question, so the probe adds no dialog of its own.
func (s *startup) probe(dirs []string, read func(string) error) {
	if read == nil {
		read = func(d string) error { _, err := os.ReadDir(d); return err }
	}
	for _, d := range dirs {
		s.mu.Lock()
		s.pending[d] = s.now()
		s.mu.Unlock()
		go func() {
			_ = read(d)
			s.mu.Lock()
			delete(s.pending, d)
			s.mu.Unlock()
		}()
	}
}

// Ready switches every later request to h.
func (s *startup) Ready(h http.Handler) {
	s.mu.Lock()
	s.ready = h
	s.mu.Unlock()
}

// Health is the body /api/health answers while starting.
func (s *startup) Health() api.Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.base
	h.State, h.WaitingFor, h.Drive = startupState(s.now(), s.began, s.pending, s.goos, s.wait)
	return h
}

// startupState is the health state at now: "starting" until wait has passed,
// then waiting_for_macos_permission on macOS while a folder read is still
// held (the oldest one is named). Elsewhere a stuck read (a slow network
// mount) stays "starting", with the folder named.
func startupState(now, began time.Time, pending map[string]time.Time, goos string, wait time.Duration) (state, folder, drive string) {
	if now.Sub(began) < wait {
		return api.StateStarting, "", ""
	}
	var oldest time.Time
	for d, at := range pending {
		if now.Sub(at) < wait {
			continue
		}
		if folder == "" || at.Before(oldest) || (at.Equal(oldest) && d < folder) {
			folder, oldest = d, at
		}
	}
	if folder == "" {
		return api.StateStarting, "", ""
	}
	if goos != "darwin" {
		return api.StateStarting, folder, ""
	}
	return api.StateMacOSAllow, folder, driveName(folder)
}

// driveName is the volume a macOS path lives on: "DevSSD" for
// /Volumes/DevSSD/x, else the folder's own name ("Documents").
func driveName(p string) string { return api.DriveName(p) }

// Notice is the sentence people see while the daemon waits.
func (h startupNotice) String() string {
	switch {
	case h.State == api.StateMacOSAllow:
		return fmt.Sprintf("macOS is asking whether Lolad may use %q. Click Allow in the macOS dialog (it may be behind other windows). Lolad finishes starting as soon as you do.", h.Drive)
	case h.WaitingFor != "":
		return "Lolad is starting. It is still reading " + h.WaitingFor + "."
	}
	return "Lolad is starting."
}

type startupNotice api.Health

func (s *startup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.ready
	s.mu.Unlock()
	if h != nil {
		h.ServeHTTP(w, r)
		return
	}
	hl := s.Health()
	msg := startupNotice(hl).String()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/health":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(hl)
	case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/hook/") || r.URL.Path == "/healthz" || r.Method != http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "state": hl.State})
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, startingPage(msg))
	}
}

// startingPage reloads itself every two seconds, so the dashboard appears by
// itself once the daemon is ready.
func startingPage(msg string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="2">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>Lolad is starting</title>` +
		`<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;` +
		`font:16px/1.5 -apple-system,system-ui,"Segoe UI",sans-serif;background:#07080F;color:#E6E8F2}` +
		`p{max-width:34em;padding:0 24px;text-align:center}</style></head>` +
		`<body><p>` + html.EscapeString(msg) + `</p></body></html>`
}

// watch logs once when the daemon starts waiting, naming the folder.
func (s *startup) watch(log io.Writer, done <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if h := s.Health(); h.WaitingFor != "" {
				fmt.Fprintf(log, "lolad: reading %s has taken more than %s. %s\n", h.WaitingFor, s.wait, startupNotice(h))
				return
			}
		}
	}
}
