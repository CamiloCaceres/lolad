package setup

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Options configure a Manager.
type Options struct {
	StateDir   string // setup.json lives here
	ConfigPath string // default: $LOLAD_CONFIG, else ~/.config/lolad/config.yaml
	OrgPath    string // default: org.yaml beside the config

	Sessions Sessions       // terminal actions; nil disables them
	Service  ServiceControl // start at login; nil hides it
	// CountAgents reports how many agents lolad already manages. When nil,
	// agent directories under the agents folder are counted instead.
	CountAgents func(ctx context.Context) (int, error)
	// SaveUserName, when set, stores the operator's display name (the daemon
	// owns org.yaml while it runs); otherwise org.yaml is edited directly.
	SaveUserName func(ctx context.Context, display string) error
	// Reload is called after config.yaml changes, so the daemon can pick the
	// new agents folder and paths up. Optional.
	Reload func(ctx context.Context) error

	// For tests.
	Home   string
	GOOS   string
	Getenv func(string) string
	Run    func(ctx context.Context, env []string, name string, args ...string) (string, error)
	Now    func() time.Time
	Poll   time.Duration // session watcher period; default 2s
	Grace  time.Duration // how long a finished session stays; default 30s
	MaxAge time.Duration // hard cap on a session; default 30m
}

// Manager is the real Service.
type Manager struct {
	opts   Options
	home   string
	goos   string
	getenv func(string) string
	run    runFunc
	now    func() time.Time
	cfg    string
	org    string
	st     stateFile

	mu           sync.Mutex
	scanned      bool
	captured     string // login-shell PATH of the last scan
	harness      []Harness
	sessions     map[string]*session
	noSystemDirs bool // tests: do not probe /usr/local/bin and friends
}

var _ Service = (*Manager)(nil)

// New returns a Manager. Nothing is probed until the first Status or Rescan.
func New(o Options) *Manager {
	m := &Manager{opts: o, home: o.Home, goos: o.GOOS, getenv: o.Getenv, now: o.Now, sessions: map[string]*session{}}
	if m.home == "" {
		m.home = osHome()
	}
	if m.goos == "" {
		m.goos = runtime.GOOS
	}
	if m.getenv == nil {
		m.getenv = os.Getenv
	}
	if m.now == nil {
		m.now = time.Now
	}
	m.run = o.Run
	if m.run == nil {
		m.run = defaultRun
	}
	m.cfg = o.ConfigPath
	if m.cfg == "" {
		if m.cfg = m.getenv("LOLAD_CONFIG"); m.cfg == "" {
			m.cfg = filepath.Join(m.home, ".config", "lolad", "config.yaml")
		}
	}
	m.org = o.OrgPath
	if m.org == "" {
		m.org = filepath.Join(filepath.Dir(m.cfg), "org.yaml")
	}
	m.st.path = filepath.Join(o.StateDir, "setup.json")
	return m
}

// OSUserName is the operator's name according to the OS account, for
// pre-filling "your name".
func (m *Manager) OSUserName() string {
	if u, err := user.Current(); err == nil {
		if n := strings.TrimSpace(u.Name); n != "" {
			return n
		}
		if u.Username != "" {
			return u.Username[strings.LastIndexAny(u.Username, `\/`)+1:]
		}
	}
	if n := m.getenv("USER"); n != "" {
		return n
	}
	return ""
}

func (m *Manager) capturing(st state) bool { return st.CapturePath == nil || *st.CapturePath }

// configured reads the harness paths config.yaml already names.
func (m *Manager) configured() (paths map[string]string, keys map[string]any) {
	_, keys, err := readConfig(m.cfg)
	paths = map[string]string{}
	if err != nil {
		return paths, map[string]any{}
	}
	for _, s := range harnessSpecs {
		if v, ok := keys[configBinKey(s.id)].(string); ok {
			paths[s.id] = v
		}
	}
	return paths, keys
}

// Rescan repeats detection and, when the PATH capture is on, the login shell
// query. What it finds is written to config.yaml.
func (m *Manager) Rescan(ctx context.Context) (Status, error) {
	m.mu.Lock()
	err := m.rescanLocked(ctx)
	m.mu.Unlock()
	if err != nil {
		return Status{}, err
	}
	return m.Status(ctx)
}

func (m *Manager) rescanLocked(ctx context.Context) error {
	st := m.st.load()
	var warn error
	if m.capturing(st) {
		p, err := m.loginShellPATH(ctx)
		if err == nil {
			m.captured = p
		} else {
			warn = err
		}
	}
	configured, _ := m.configured()
	hs := m.detect(ctx, m.captured, configured)
	m.harness, m.scanned = hs, true
	_ = warn // a failed capture is not fatal: the well-known places still get probed
	return m.writeDetected(ctx, hs)
}

// writeDetected stores the captured PATH and the binaries that were found.
func (m *Manager) writeDetected(ctx context.Context, hs []Harness) error {
	set := map[string]yamlValue{}
	order := []string{}
	if m.captured != "" {
		set["path"] = str(m.captured)
		order = append(order, "path")
	}
	for _, h := range hs {
		if h.Installed && h.Path != "" {
			k := configBinKey(h.ID)
			set[k] = str(h.Path)
			order = append(order, k)
		}
	}
	if len(set) == 0 {
		return nil
	}
	changed, err := writeConfigKeys(m.cfg, set, order)
	if err != nil {
		return fmt.Errorf("write %s: %w", m.cfg, err)
	}
	if changed {
		m.reload(ctx)
	}
	return nil
}

func (m *Manager) reload(ctx context.Context) {
	if m.opts.Reload != nil {
		_ = m.opts.Reload(ctx)
	}
}

// Status builds the current picture. The first call scans; later ones reuse
// that scan until Rescan (or a finished terminal action) refreshes it.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.Lock()
	if !m.scanned {
		if err := m.rescanLocked(ctx); err != nil {
			m.mu.Unlock()
			return Status{}, err
		}
	}
	hs := append([]Harness(nil), m.harness...)
	terms := m.liveTerminalsLocked()
	m.mu.Unlock()

	st := m.st.load()
	dir := m.agentsDir(st)
	s := Status{
		Harnesses:        hs,
		AgentsDir:        dir,
		AgentsDirWarning: AgentsDirWarning(m.goos, m.home, dir),
		UserName:         st.UserName,
		CapturePath:      m.capturing(st),
		Dismissed:        st.Dismissed,
		SkippedAgent:     st.SkippedAgent,
		FolderSet:        st.FolderSet,
		NameSet:          st.NameSet,
		ServiceDone:      st.ServiceDone,
		Platform:         m.goos,
		ConfigPath:       m.cfg,
		Terminals:        terms,
	}
	if s.UserName == "" {
		s.UserName = m.OSUserName()
	}
	s.HasAgents = m.hasAgents(ctx, dir)
	if m.opts.Service != nil {
		in, run, err := m.opts.Service.Status(ctx)
		s.Service = ServiceStatus{Available: true, Installed: in && err == nil, Running: run && err == nil}
	}

	s.Settle()
	if s.Done && !st.Completed {
		_, _ = m.st.update(m.now(), func(x *state) { x.Completed = true })
	}
	return s, nil
}

// agentsDir is the folder in use: the one the user chose, else the one the
// config names, else the default.
func (m *Manager) agentsDir(st state) string {
	if st.AgentsDir != "" {
		return st.AgentsDir
	}
	_, keys := m.configured()
	if d, ok := keys["new_agents_dir"].(string); ok && d != "" {
		return ExpandDir(m.home, d)
	}
	return DefaultAgentsDir(m.home)
}

func (m *Manager) hasAgents(ctx context.Context, dir string) bool {
	if m.opts.CountAgents != nil {
		if n, err := m.opts.CountAgents(ctx); err == nil {
			return n > 0
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "lolabot.yaml")); err == nil {
				return true
			}
		}
	}
	return false
}

// Needed is true while onboarding should take over "/".
func (m *Manager) Needed() bool {
	st := m.st.load()
	return !st.Completed && !st.Dismissed
}

// CheckFolder expands dir and warns about places background agents cannot use.
func (m *Manager) CheckFolder(dir string) (string, string) {
	p := ExpandDir(m.home, dir)
	return p, AgentsDirWarning(m.goos, m.home, p)
}

// Save applies the request.
func (m *Manager) Save(ctx context.Context, req SaveRequest) (Status, error) {
	now := m.now()
	if req.CapturePath != nil {
		v := *req.CapturePath
		if _, err := m.st.update(now, func(s *state) { s.CapturePath = &v }); err != nil {
			return Status{}, err
		}
		if !v {
			m.mu.Lock()
			m.captured = ""
			m.mu.Unlock()
		}
	}
	if req.AgentsDir != nil {
		if err := m.saveFolder(ctx, *req.AgentsDir); err != nil {
			return Status{}, err
		}
	}
	if req.UserName != nil {
		if err := m.saveName(ctx, *req.UserName, req.NameSaved != nil && *req.NameSaved); err != nil {
			return Status{}, err
		}
	}
	if _, err := m.st.update(now, func(s *state) {
		if req.ServiceDone != nil {
			s.ServiceDone = *req.ServiceDone
		}
		if req.SkipAgent != nil {
			s.SkippedAgent = *req.SkipAgent
		}
		if req.Dismiss != nil {
			s.Dismissed = *req.Dismiss
		}
	}); err != nil {
		return Status{}, err
	}
	return m.Status(ctx)
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", core.ErrInvalid, fmt.Sprintf(format, a...))
}

func (m *Manager) saveFolder(ctx context.Context, dir string) error {
	dir = ExpandDir(m.home, dir)
	if dir == "" {
		dir = DefaultAgentsDir(m.home)
	}
	if !filepath.IsAbs(dir) {
		return invalid("enter a full folder path, or leave it blank to use %s", DefaultAgentsDir(m.home))
	}
	if strings.ContainsAny(dir, "\n\r\"|&") {
		return invalid("the folder path cannot contain quotes, | or &")
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return invalid("%s is a file, not a folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return invalid("cannot create %s: %v", dir, err)
	}
	_, keys := m.configured()
	dirs := stringList(keys["agents_dirs"])
	have := false
	for _, d := range dirs {
		if filepath.Clean(ExpandDir(m.home, d)) == filepath.Clean(dir) {
			have = true
		}
	}
	if !have {
		dirs = append(dirs, dir)
	}
	changed, err := writeConfigKeys(m.cfg, map[string]yamlValue{"agents_dirs": strs(dirs...), "new_agents_dir": str(dir)}, []string{"agents_dirs", "new_agents_dir"})
	if err != nil {
		return err
	}
	if _, err := m.st.update(m.now(), func(s *state) { s.AgentsDir, s.FolderSet = dir, true }); err != nil {
		return err
	}
	if changed {
		m.reload(ctx)
	}
	return nil
}

func (m *Manager) saveName(ctx context.Context, name string, stored bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return invalid("tell lolad what to call you")
	}
	if len(name) > 100 || strings.ContainsAny(name, "\n\r\x00") {
		return invalid("that name is too long or has a line break in it")
	}
	if stored {
		// the caller wrote it
	} else if m.opts.SaveUserName != nil {
		if err := m.opts.SaveUserName(ctx, name); err != nil {
			return err
		}
	} else {
		handle := strings.ToLower(strings.Split(strings.Fields(name + " x")[0], " ")[0])
		if u, err := user.Current(); err == nil && u.Username != "" {
			handle = strings.ToLower(u.Username[strings.LastIndexAny(u.Username, `\/`)+1:])
		}
		if err := orgDisplay(m.org, handle, name); err != nil {
			return err
		}
	}
	_, err := m.st.update(m.now(), func(s *state) { s.UserName, s.NameSet = name, true })
	return err
}

// InstallService registers lolad to start at login.
func (m *Manager) InstallService(ctx context.Context) (Status, error) {
	if m.opts.Service == nil {
		return Status{}, ErrUnavailable
	}
	if err := m.opts.Service.Install(ctx); err != nil {
		return Status{}, err
	}
	t := true
	return m.Save(ctx, SaveRequest{ServiceDone: &t})
}
