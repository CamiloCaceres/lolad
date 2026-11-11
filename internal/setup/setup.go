// Package setup is lolad's first-run onboarding: it finds the agent harnesses
// (claude, codex, codewhale) on the machine, captures the login shell's PATH
// (and only PATH), writes the handful of config.yaml keys that make the daemon
// find them, remembers how far the user got, and runs the vendors' official
// installers and logins in throwaway lolad-managed terminal sessions.
//
// The dashboard (/welcome) and the JSON API (/api/setup) are both thin layers
// over Service; Manager is the real implementation and Demo is a canned one
// for the fake dashboard.
package setup

import (
	"context"
	"errors"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Steps, in order. Status.Step is the first one that is not complete.
const (
	StepLogin     = "login"     // start at login
	StepHarnesses = "harnesses" // at least one harness ready
	StepFolder    = "folder"    // agents folder
	StepName      = "name"      // the user's name
	StepAgent     = "agent"     // first agent
	StepDone      = "done"
)

// StepOrder lists the steps the user walks through.
var StepOrder = []string{StepLogin, StepHarnesses, StepFolder, StepName, StepAgent}

// Harness is one supported agent runtime as found on this machine.
type Harness struct {
	ID          string `json:"id"` // claude | codex | codewhale
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	LoggedIn    bool   `json:"logged_in"`
	InstallHint string `json:"install_hint"`

	// Extras for the UI.
	LoginChecked bool   `json:"login_checked"`     // false: lolad cannot tell whether it is signed in
	Ready        bool   `json:"ready"`             // installed, signed in (or unknowable) and nothing missing
	Problem      string `json:"problem,omitempty"` // human sentence when installed but not usable
	CanInstall   bool   `json:"can_install"`       // an install-<id> terminal action exists
	CanLogin     bool   `json:"can_login"`         // a login-<id> terminal action exists
}

// ServiceStatus is the start-at-login registration.
type ServiceStatus struct {
	Available bool `json:"available"` // this build can register the service
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
}

// TerminalInfo describes a throwaway setup session.
type TerminalInfo struct {
	Session     string `json:"session"`
	TerminalURL string `json:"terminal_url"`
	Action      string `json:"action"`
	Title       string `json:"title,omitempty"`
}

// Status is GET /api/setup.
type Status struct {
	Done             bool          `json:"done"`
	Step             string        `json:"step"`
	Harnesses        []Harness     `json:"harnesses"`
	AgentsDir        string        `json:"agents_dir"`
	AgentsDirWarning string        `json:"agents_dir_warning,omitempty"`
	UserName         string        `json:"user_name"`
	HasAgents        bool          `json:"has_agents"`
	Service          ServiceStatus `json:"service"`

	// Extras.
	CapturePath  bool           `json:"capture_path"` // keep the login shell's PATH in config.yaml
	Dismissed    bool           `json:"dismissed"`    // the user left setup for later
	SkippedAgent bool           `json:"skipped_agent"`
	ServiceDone  bool           `json:"service_done"` // step 1 finished or skipped
	FolderSet    bool           `json:"folder_set"`
	NameSet      bool           `json:"name_set"`
	Platform     string         `json:"platform"`
	ConfigPath   string         `json:"config_path"`
	Terminals    []TerminalInfo `json:"terminals,omitempty"` // sessions still running
}

// FirstReady returns the first harness that is ready, in claude, codex,
// codewhale order. ok is false when none is.
func (s Status) FirstReady() (Harness, bool) {
	for _, h := range s.Harnesses {
		if h.Ready {
			return h, true
		}
	}
	return Harness{}, false
}

// Harness returns one harness by ID.
func (s Status) Harness(id string) (Harness, bool) {
	for _, h := range s.Harnesses {
		if h.ID == id {
			return h, true
		}
	}
	return Harness{}, false
}

// SaveRequest is POST /api/setup. Nil fields are left alone.
type SaveRequest struct {
	AgentsDir *string `json:"agents_dir,omitempty"`
	UserName  *string `json:"user_name,omitempty"`
	// NameSaved says the caller already stored UserName in org.yaml (through
	// the daemon), so only the progress is recorded.
	NameSaved   *bool `json:"name_saved,omitempty"`
	CapturePath *bool `json:"capture_path,omitempty"`

	// Progress marks, for the screens that have no data of their own.
	ServiceDone *bool `json:"service_done,omitempty"` // step 1 finished or skipped
	SkipAgent   *bool `json:"skip_agent,omitempty"`   // "I'll add an agent later"
	Dismiss     *bool `json:"dismiss,omitempty"`      // leave setup; stop redirecting to it
}

// Service is what the dashboard and the API need from onboarding.
type Service interface {
	Status(ctx context.Context) (Status, error)
	Save(ctx context.Context, req SaveRequest) (Status, error)
	// Rescan repeats detection (and the PATH capture when it is on).
	Rescan(ctx context.Context) (Status, error)
	// StartTerminal starts one of Actions in a throwaway session.
	StartTerminal(ctx context.Context, action string) (TerminalInfo, error)
	// Attach is a websocket's view of a session StartTerminal made. It
	// refuses any other session, so it cannot reach an agent.
	Attach(ctx context.Context, session string, control bool) (core.Terminal, error)
	// InstallService registers lolad to start at login.
	InstallService(ctx context.Context) (Status, error)
	// Needed is true while first-run onboarding should take over "/": it has
	// not finished and the user has not left it. It is cheap and does no I/O
	// beyond a cached read.
	Needed() bool
	// CheckFolder returns the absolute form of dir and the warning that
	// applies to it (empty when none).
	CheckFolder(dir string) (path, warning string)
}

// ServiceControl registers the daemon to start at login. The integrator wires
// it to `lolad service`.
type ServiceControl interface {
	Install(ctx context.Context) error
	Status(ctx context.Context) (installed, running bool, err error)
}

// Sessions is the part of internal/tmux the terminal actions use;
// *tmux.Sessions satisfies it.
type Sessions interface {
	Start(name, dir string, env, argv []string, cols, rows int) error
	Exists(name string) bool
	Kill(name string) error
	PaneDead(name string) (dead bool, exit int, err error)
	Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error)
}

// ErrUnavailable is returned when a feature has nothing behind it in this build.
var ErrUnavailable = errors.New("setup: not available")

// settle works out Step and Done from the rest of the status.
func (s *Status) Settle() {
	_, ready := s.FirstReady()
	switch {
	case !s.ServiceDone && !s.Service.Installed:
		s.Step = StepLogin
	case !ready:
		s.Step = StepHarnesses
	case !s.FolderSet:
		s.Step = StepFolder
	case !s.NameSet:
		s.Step = StepName
	case !s.HasAgents && !s.SkippedAgent:
		s.Step = StepAgent
	default:
		s.Step = StepDone
	}
	// done: a harness is ready, a folder is set, and agents exist or the user
	// chose to add them later. The login and name steps are friendly, not required.
	// An install that already has agents is set up, whether or not a folder was
	// ever chosen in this flow (existing users must never be sent to /welcome).
	s.Done = ready && (s.HasAgents || (s.FolderSet && s.SkippedAgent))
}
