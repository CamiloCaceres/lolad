package setup

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Demo is an in-memory Service for the fake dashboard and tests: it detects
// nothing, installs nothing, and lets a state be walked through by hand. Its
// terminals print a short script.
type Demo struct {
	mu     sync.Mutex
	st     Status
	needed bool
	live   map[string]time.Time // session -> when it ends
}

var _ Service = (*Demo)(nil)

// NewDemo returns a Demo that starts like a fresh machine with nothing
// installed. Set up its state with Set.
func NewDemo() *Demo {
	d := &Demo{needed: true, live: map[string]time.Time{}}
	d.st = Status{
		Harnesses:   demoHarnesses("linux"),
		AgentsDir:   "/home/you/Lolad Agents",
		UserName:    "Camilo Caceres",
		CapturePath: true,
		Platform:    "darwin",
		Service:     ServiceStatus{Available: true},
	}
	d.st = recompute(d.st)
	return d
}

func demoHarnesses(goos string) []Harness {
	var hs []Harness
	for _, s := range harnessSpecs {
		hs = append(hs, Harness{ID: s.id, Name: s.name, InstallHint: installHint(goos, s.id), CanInstall: canInstall(s.id), CanLogin: canLogin(s.id)})
	}
	return hs
}

// Set replaces the demo's state through fn.
func (d *Demo) Set(fn func(*Status)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn(&d.st)
	d.st = recompute(d.st)
}

func recompute(s Status) Status {
	for i, h := range s.Harnesses {
		s.Harnesses[i].Ready = h.Installed && (h.LoggedIn || !h.LoginChecked) && h.Problem == ""
	}
	s.Settle()
	s.AgentsDirWarning = AgentsDirWarning(s.Platform, "/Users/you", s.AgentsDir)
	return s
}

func (d *Demo) Status(context.Context) (Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.st
	s.Harnesses = slices.Clone(s.Harnesses)
	s.Terminals = nil
	for name, end := range d.live {
		if time.Now().Before(end) {
			s.Terminals = append(s.Terminals, TerminalInfo{Session: name, Action: strings.TrimPrefix(name, sessionPrefix)})
		}
	}
	return s, nil
}

func (d *Demo) Save(ctx context.Context, req SaveRequest) (Status, error) {
	d.Set(func(s *Status) {
		if req.AgentsDir != nil {
			s.AgentsDir, s.FolderSet = *req.AgentsDir, true
		}
		if req.UserName != nil && strings.TrimSpace(*req.UserName) != "" {
			s.UserName, s.NameSet = strings.TrimSpace(*req.UserName), true
		}
		if req.CapturePath != nil {
			s.CapturePath = *req.CapturePath
		}
		if req.ServiceDone != nil {
			s.ServiceDone = *req.ServiceDone
		}
		if req.SkipAgent != nil {
			s.SkippedAgent = *req.SkipAgent
		}
		if req.Dismiss != nil {
			s.Dismissed = *req.Dismiss
		}
	})
	return d.Status(ctx)
}

func (d *Demo) Rescan(ctx context.Context) (Status, error) { return d.Status(ctx) }

func (d *Demo) InstallService(ctx context.Context) (Status, error) {
	d.Set(func(s *Status) { s.Service.Installed, s.Service.Running = true, true })
	return d.Status(ctx)
}

func (d *Demo) Needed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.needed && !d.st.Dismissed && !d.st.Done
}

func (d *Demo) CheckFolder(dir string) (string, string) {
	p := ExpandDir("/Users/you", dir)
	return p, AgentsDirWarning(d.st.Platform, "/Users/you", p)
}

func (d *Demo) StartTerminal(_ context.Context, action string) (TerminalInfo, error) {
	if !slices.Contains(Actions, action) {
		return TerminalInfo{}, invalid("unknown action %q", action)
	}
	id := strings.TrimPrefix(strings.TrimPrefix(action, "login-"), "install-")
	d.mu.Lock()
	d.live[sessionPrefix+action] = time.Now().Add(5 * time.Second)
	d.mu.Unlock()
	// The script "finishes" a few seconds after it starts.
	go func() {
		time.Sleep(4 * time.Second)
		d.Set(func(s *Status) {
			for i := range s.Harnesses {
				if s.Harnesses[i].ID != id {
					continue
				}
				if strings.HasPrefix(action, "install-") {
					s.Harnesses[i].Installed, s.Harnesses[i].Path, s.Harnesses[i].Version, s.Harnesses[i].LoginChecked = true, "/Users/you/.local/bin/"+id, "1.0.0", true
				} else {
					s.Harnesses[i].LoggedIn, s.Harnesses[i].LoginChecked = true, true
				}
			}
		})
	}()
	return TerminalInfo{Session: sessionPrefix + action, TerminalURL: "/welcome?terminal=" + sessionPrefix + action, Action: action, Title: actionTitle(action)}, nil
}

func (d *Demo) Attach(_ context.Context, session string, _ bool) (core.Terminal, error) {
	if !strings.HasPrefix(session, sessionPrefix) {
		return nil, core.ErrNotFound
	}
	pr, pw := io.Pipe()
	go func() {
		for _, l := range []string{"$ " + strings.TrimPrefix(session, sessionPrefix) + "\r\n", "Working...\r\n", "Done.\r\n\r\nFinished (exit 0). You can close this panel and press Re-scan.\r\n"} {
			time.Sleep(600 * time.Millisecond)
			if _, err := pw.Write([]byte(l)); err != nil {
				return
			}
		}
		time.Sleep(time.Hour)
	}()
	return demoTerm{pr, pw}, nil
}

type demoTerm struct {
	*io.PipeReader
	pw *io.PipeWriter
}

func (t demoTerm) Write(p []byte) (int, error) { return len(p), nil }
func (t demoTerm) Close() error                { t.pw.Close(); return t.PipeReader.Close() }
func (t demoTerm) Resize(uint16, uint16) error { return nil }
