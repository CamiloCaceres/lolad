package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"github.com/CamiloCaceres/lolad/internal/tmux"
)

// DoctorCheck is one element of the `lolad doctor --json` array.
type DoctorCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // ok | warn | fail
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// doctorDeps are the machine-facing parts of the checks, replaceable in tests.
type doctorDeps struct {
	GOOS    string
	Home    string
	Find    func(name, extraPath string) (string, error) // binary lookup
	Version func(ctx context.Context, bin string) string
	Health  func(listen string) (api.Health, bool)
	Listen  func(addr string) error // nil when the address can be bound
	Service func() ServiceStatus
}

func realDoctorDeps(c registry.Config) doctorDeps {
	home, _ := os.UserHomeDir()
	env := newServiceEnv(c)
	return doctorDeps{
		GOOS: runtime.GOOS, Home: home, Find: findBinary, Version: binaryVersion, Health: probeHealth,
		Listen: func(addr string) error {
			l, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			return l.Close()
		},
		Service: func() ServiceStatus {
			return newServiceManager(env).Status()
		},
	}
}

// findBinary resolves name (or an absolute/explicit path) using the configured
// `path`, then the process PATH, then the usual per-user install locations a
// login-launched daemon with a minimal PATH would miss.
func findBinary(name, extraPath string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			return name, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
	var dirs []string
	dirs = append(dirs, filepath.SplitList(extraPath)...)
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".cargo", "bin"))
	}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ""}
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(d, name+ext)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

func binaryVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out []byte
	for _, flag := range []string{"--version", "-V"} { // tmux only knows -V
		cmd := exec.CommandContext(ctx, bin, flag)
		hideWindow(cmd)
		var err error
		if out, err = cmd.Output(); err == nil {
			break
		}
		out = nil
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// protectedFolder names the macOS privacy-protected (TCC) or cloud-synced
// folder that contains dir, or "".
func protectedFolder(goos, home, dir string) string {
	if goos != "darwin" || home == "" || dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	for _, p := range []string{"Desktop", "Documents", "Downloads", "Library/Mobile Documents", "Library/CloudStorage"} {
		root := filepath.Join(home, p)
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			if strings.HasPrefix(p, "Library") {
				return "iCloud/cloud-synced folder ~/" + p
			}
			return "~/" + p
		}
	}
	return ""
}

// doctorChecks runs every check; it never fails as a whole.
func doctorChecks(ctx context.Context, c registry.Config, cfgErr error, d doctorDeps) []DoctorCheck {
	var out []DoctorCheck
	add := func(id, status, msg, fix string) { out = append(out, DoctorCheck{id, status, msg, fix}) }

	// config
	switch {
	case cfgErr != nil:
		add("config", "fail", "config.yaml is invalid: "+cfgErr.Error(), "fix or remove "+registry.ConfigPath())
	default:
		if e := validateListeners(c); e != nil {
			add("config", "fail", "config.yaml: "+e.Error(), "use distinct numeric loopback addresses and an HTTPS public_url")
		} else {
			add("config", "ok", "config is valid ("+registry.ConfigPath()+")", "")
		}
	}

	// tmux / psmux
	mux := c.TmuxBin
	if mux == "" {
		mux = tmux.DefaultBinary()
	}
	if p, err := d.Find(mux, c.Path); err != nil {
		fix := "install tmux (brew install tmux)"
		if d.GOOS == "windows" {
			fix = "winget install psmux"
		}
		add("tmux", "fail", mux+" not found", fix+" or set tmux_bin in config.yaml")
	} else {
		add("tmux", "ok", p+versionSuffix(d.Version(ctx, p)), "")
	}

	// harnesses
	type harnessSpec struct{ id, name, configured string }
	specs := []harnessSpec{{"claude", "claude", c.ClaudeBin}, {"codex", "codex", c.CodexBin}, {"codewhale", "codewhale", c.CodewhaleBin}}
	found := 0
	var missing []int
	for i, h := range specs {
		name := h.name
		if h.configured != "" {
			name = h.configured
		}
		p, err := d.Find(name, c.Path)
		if err != nil {
			missing = append(missing, i)
			continue
		}
		found++
		add(h.id, "ok", p+versionSuffix(d.Version(ctx, p)), "")
	}
	for _, i := range missing {
		h := specs[i]
		status := "warn"
		if found == 0 && h.id == "claude" {
			status = "fail"
		}
		add(h.id, status, h.name+" not found on PATH", "install "+h.name+" or set "+h.id+"_bin / path in config.yaml")
	}

	// port / daemon
	if h, ok := d.Health(c.Listen); ok {
		add("port", "ok", fmt.Sprintf("%s is served by lolad %s (pid %d)", c.Listen, h.Version, h.PID), "")
	} else if e := d.Listen(c.Listen); e != nil {
		add("port", "fail", c.Listen+" is in use by another program", "set listen: in config.yaml to a free 127.0.0.1 port")
	} else {
		add("port", "ok", c.Listen+" is free", "")
	}

	// macOS-protected agent folders
	var dirs []string
	dirs = append(dirs, c.AgentsDirs...)
	dirs = append(dirs, c.Agents...)
	if c.NewAgentsDir != "" {
		dirs = append(dirs, c.NewAgentsDir)
	}
	seen := map[string]bool{}
	bad := 0
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if where := protectedFolder(d.GOOS, d.Home, dir); where != "" {
			bad++
			add("agent-dir:"+dir, "warn", dir+" is inside "+where+"; macOS asks for permission and agents may stall or lose access", "move agents to ~/agents or grant Lolad access in System Settings > Privacy & Security > Files and Folders")
		}
	}
	if bad == 0 {
		add("agent-dirs", "ok", "no agent directory is in a macOS-protected or cloud-synced folder", "")
	}

	// start at login
	st := d.Service()
	switch {
	case st.Mechanism == "none":
		add("service", "warn", "start at login is not supported on this platform", "run 'lolad serve' from your own init system")
	case !st.Installed:
		add("service", "warn", "lolad does not start at login", "run 'lolad service install'")
	case !st.Running:
		add("service", "warn", "start-at-login is installed ("+st.Mechanism+") but lolad is not running", "run 'lolad service start'")
	default:
		add("service", "ok", "starts at login ("+st.Mechanism+") and is running", "")
	}
	return out
}

func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return " (" + v + ")"
}

func validateListeners(c registry.Config) error {
	return appValidateListeners(c)
}

func doctorJSON(ctx context.Context, out io.Writer) error {
	c, cfgErr := registry.LoadConfig("")
	if cfgErr != nil {
		c = registry.DefaultConfig()
	}
	checks := doctorChecks(ctx, c, cfgErr, realDoctorDeps(c))
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(checks)
}
