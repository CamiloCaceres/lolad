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
	started := o.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	exe, _ := os.Executable()
	if exe != "" {
		exe = filepath.Clean(exe)
	}
	h := Health{Product: "lolad", Version: o.Version, PID: os.Getpid(), StartedAt: started.UTC().Format(time.RFC3339), Exe: exe, Install: InstallKind(exe, os.Getenv("LOLAD_INSTALL")), StateDir: o.StateDir}
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, h, nil)
	})
}
