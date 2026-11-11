package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/create"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"github.com/CamiloCaceres/lolad/internal/setup"
)

// setupManager builds the first-run onboarding manager (/welcome, /api/setup).
// It is only mounted on the loopback listener.
func (a *App) setupManager() *setup.Manager {
	o := setup.Options{
		StateDir:    a.Config.StateDir,
		ConfigPath:  a.configPath,
		Service:     execService{},
		CountAgents: func(ctx context.Context) (int, error) { l, err := a.List(ctx); return len(l), err },
		Reload:      a.Reload,
	}
	if a.sessions != nil {
		o.Sessions = a.sessions
	}
	return setup.New(o)
}

// createTemplate resolves the lolabot template for new agents: a user-set or
// existing checkout wins, otherwise the copy bundled with the app.
func (a *App) createTemplate() (create.Template, error) {
	return create.ResolveTemplate(create.TemplateConfig{Configured: a.Config.LolabotTemplate, StateDir: a.Config.StateDir})
}

// execService drives `lolad service`, so the daemon and the CLI share one
// implementation of login registration.
type execService struct{}

func (execService) run(ctx context.Context, args ...string) ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, exe, append([]string{"service"}, args...)...).Output()
}

func (s execService) Install(ctx context.Context) error {
	_, err := s.run(ctx, "install", "--json")
	return err
}

func (s execService) Status(ctx context.Context) (installed, running bool, err error) {
	out, err := s.run(ctx, "status", "--json")
	if err != nil {
		return false, false, err
	}
	var st struct{ Installed, Running bool }
	if err := json.Unmarshal(out, &st); err != nil {
		return false, false, err
	}
	return st.Installed, st.Running, nil
}

// cliBinary is what agents run as `lolad`. On Windows the background daemon is
// lolad-daemon.exe (no console), so agents get the console lolad.exe beside it.
func cliBinary(cfg registry.Config, exe string) string {
	if cfg.CLIBin == "" {
		sib := filepath.Join(filepath.Dir(exe), "lolad.exe")
		if strings.EqualFold(filepath.Base(exe), "lolad-daemon.exe") {
			if _, err := os.Stat(sib); err == nil {
				return sib
			}
		}
	}
	return cfg.CLIPath(exe)
}

// bundledMultiplexer returns the tmux (macOS app: next to the daemon in
// Contents/Helpers) or psmux (Windows installer: bin\psmux\) shipped with the
// desktop app, or "" for a plain CLI install, which uses PATH as before.
func bundledMultiplexer() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, c := range []string{
		filepath.Join(dir, "tmux"),
		filepath.Join(dir, "psmux", "psmux.exe"),
		filepath.Join(dir, "..", "bin", "psmux", "psmux.exe"),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}
