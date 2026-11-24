package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Health is the body of GET /api/health. It is cheap and carries no secrets,
// so the desktop app and `lolad service status` can tell which daemon is
// answering on a port.
type Health struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	Exe       string `json:"exe"`
	Install   string `json:"install"`
	StateDir  string `json:"state_dir"`
	// State is "ready" once the daemon serves everything. While it starts it
	// is "starting", or "waiting_for_macos_permission" when reading the agent
	// folders has blocked for a while: macOS holds the read until the user
	// answers a privacy dialog (removable volumes, Documents, ...). Older
	// daemons leave it out; read a missing state as "ready".
	State string `json:"state,omitempty"`
	// WaitingFor is the folder the daemon is stuck reading, and Drive the
	// name of its volume ("DevSSD" for /Volumes/DevSSD/...), while State is
	// waiting_for_macos_permission.
	WaitingFor string `json:"waiting_for,omitempty"`
	Drive      string `json:"drive,omitempty"`
}

// Health states.
const (
	StateReady      = "ready"
	StateStarting   = "starting"
	StateMacOSAllow = "waiting_for_macos_permission"
)

// NewHealth is this process's health body in the ready state.
func NewHealth(version, stateDir string, started time.Time) Health {
	if started.IsZero() {
		started = time.Now()
	}
	exe, _ := os.Executable()
	if exe != "" {
		exe = filepath.Clean(exe)
	}
	return Health{Product: "lolad", Version: version, PID: os.Getpid(), StartedAt: started.UTC().Format(time.RFC3339), Exe: exe, Install: InstallKind(exe, os.Getenv("LOLAD_INSTALL")), StateDir: stateDir, State: StateReady}
}

// InstallKind reports "app" when forced by LOLAD_INSTALL=app or when exe lives
// inside a *.app bundle or a \Programs\Lolad\ directory, else "cli".
func InstallKind(exe, forced string) string {
	if strings.EqualFold(strings.TrimSpace(forced), "app") {
		return "app"
	}
	p := strings.ToLower(strings.ReplaceAll(exe, "\\", "/"))
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".app") {
			return "app"
		}
	}
	if strings.Contains(p, "/programs/lolad/") {
		return "app"
	}
	return "cli"
}

func healthRoute(m *http.ServeMux, o Options) {
	h := NewHealth(o.Version, o.StateDir, o.StartedAt)
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, h, nil)
	})
}
