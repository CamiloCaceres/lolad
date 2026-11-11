package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

// ServiceStatus is the shape of `lolad service status --json`.
type ServiceStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Mechanism string `json:"mechanism"` // launchd | registry-run | systemd-user | none
	Path      string `json:"path"`      // plist, unit, or registry value location
}

var errUnsupported = errors.New("start at login is not supported on this platform; run 'lolad serve' yourself")

// serviceManager registers lolad to start at login. Implementations never read
// or forward the caller's environment: the definition they write is fixed.
type serviceManager interface {
	Install(exe string) error
	Uninstall() error
	Status() ServiceStatus
	Start() error
	Stop() error
}

// serviceEnv is everything a manager needs from the machine. Tests replace it.
type serviceEnv struct {
	Home     string // account home directory (from the OS user database, not $HOME)
	UID      string
	StateDir string
	Listen   string
	Path     string // PATH written into the service definition
	Run      func(name string, args ...string) ([]byte, error)
	Health   func() (api.Health, bool) // probes the daemon's /api/health
}

func runCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	return cmd.CombinedOutput()
}

// probeHealth asks the daemon on listen whether a lolad is answering.
func probeHealth(listen string) (api.Health, bool) {
	var h api.Health
	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(app.URL(listen) + "/api/health")
	if err != nil {
		return h, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&h) != nil || h.Product != "lolad" {
		return api.Health{}, false
	}
	return h, true
}

func defaultServicePath(home string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return strings.Join([]string{filepath.Join(home, ".local/bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
}

func newServiceEnv(c registry.Config) serviceEnv {
	home := ""
	uid := ""
	if u, err := user.Current(); err == nil {
		home, uid = u.HomeDir, u.Uid
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	path := c.Path
	if path == "" {
		path = defaultServicePath(home)
	}
	listen := c.Listen
	return serviceEnv{Home: home, UID: uid, StateDir: c.StateDir, Listen: listen, Path: path, Run: runCommand, Health: func() (api.Health, bool) { return probeHealth(listen) }}
}

func newServiceManager(env serviceEnv) serviceManager {
	switch runtime.GOOS {
	case "darwin":
		return &launchdManager{env}
	case "windows":
		return &windowsManager{env: env, Stat: os.Stat, Kill: killPID, Spawn: spawnDetached}
	case "linux":
		return &systemdManager{env}
	}
	return unsupportedManager{}
}

type unsupportedManager struct{}

func (unsupportedManager) Install(string) error { return errUnsupported }
func (unsupportedManager) Uninstall() error     { return errUnsupported }
func (unsupportedManager) Status() ServiceStatus {
	return ServiceStatus{Mechanism: "none"}
}
func (unsupportedManager) Start() error { return errUnsupported }
func (unsupportedManager) Stop() error  { return errUnsupported }

func serviceCommand(ctx context.Context, args []string, out io.Writer) error {
	asJSON := false
	var rest []string
	exe := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--exe" && i+1 < len(args):
			i++
			exe = args[i]
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: lolad service install|uninstall|status|start|stop [--json]")
	}
	c, err := registry.LoadConfig("")
	if err != nil {
		return err
	}
	m := newServiceManager(newServiceEnv(c))
	var action string
	switch rest[0] {
	case "install":
		if exe == "" {
			if exe, err = os.Executable(); err != nil {
				return err
			}
		}
		err = m.Install(exe)
		action = "installed"
	case "uninstall":
		err = m.Uninstall()
		action = "uninstalled"
	case "start":
		err = m.Start()
		action = "started"
	case "stop":
		err = m.Stop()
		action = "stopped"
	case "status":
		st := m.Status()
		if asJSON {
			return json.NewEncoder(out).Encode(st)
		}
		fmt.Fprintf(out, "mechanism: %s\ninstalled: %v\nrunning:   %v\npath:      %s\n", st.Mechanism, st.Installed, st.Running, st.Path)
		return nil
	default:
		return fmt.Errorf("usage: lolad service install|uninstall|status|start|stop [--json]")
	}
	st := m.Status()
	if asJSON {
		if err != nil {
			_ = json.NewEncoder(out).Encode(map[string]any{"ok": false, "error": err.Error()})
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "installed": st.Installed, "running": st.Running, "mechanism": st.Mechanism, "path": st.Path})
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "lolad service", action)
	return nil
}

func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}

// installKindFor is the LOLAD_INSTALL value a service definition carries.
func installKindFor(exe string) string { return api.InstallKind(exe, "") }

func appValidateListeners(c registry.Config) error {
	return app.ValidateListeners(c.Listen, c.Remote.Listen, c.Remote.PublicURL)
}
