package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Health is GET /api/health (docs/desktop.md §8). Older daemons answer 404, in
// which case Health is nil and the daemon is treated as "unknown install".
type Health struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	Exe       string `json:"exe"`
	Install   string `json:"install"` // "app" | "cli"
	StateDir  string `json:"state_dir"`
	StartedAt string `json:"started_at"`
}

// SetupState is GET /api/setup. Known is false when the daemon has no such
// endpoint (404) or it could not be read.
type SetupState struct {
	Known bool
	Done  bool
}

var plainClient = &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func getJSON(base, path string, v any) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := plainClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK && v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func fetchHealth(base string) *Health {
	var h Health
	code, err := getJSON(base, "/api/health", &h)
	if err != nil || code != http.StatusOK || h.Product != "lolad" {
		return nil
	}
	return &h
}

func fetchSetup(base string) SetupState {
	var v struct {
		Done *bool `json:"done"`
	}
	code, err := getJSON(base, "/api/setup", &v)
	if err != nil || code != http.StatusOK || v.Done == nil {
		return SetupState{}
	}
	return SetupState{Known: true, Done: *v.Done}
}

// pageExists reports whether the dashboard serves path (used for /welcome,
// which the setup agent is still building).
func pageExists(base, path string) bool {
	code, err := getJSON(base, path, nil)
	return err == nil && code == http.StatusOK
}

// firstRunTarget returns the dashboard path to open on launch, or "" to leave
// the window where it is. Existing installs (docs/desktop.md §12) skip
// onboarding: when the setup endpoint is missing, agents already existing means
// there is nothing to set up.
func firstRunTarget(s SetupState, agents int, welcomeExists bool) string {
	switch {
	case agents > 0: // an install that already runs agents is set up
		return ""
	case s.Known && s.Done:
		return ""
	}
	if welcomeExists {
		return "/welcome"
	}
	return "/"
}

// onboardingDone says whether the user has finished (or never needs) setup.
// Notification permission and the login-item defaults wait for it.
func onboardingDone(s SetupState, agents int) bool {
	if agents > 0 {
		return true
	}
	return s.Known && s.Done
}

// connectedBanner is the status line for a daemon that is not the app's own:
// docs/desktop.md §6.1 and §12.
func connectedBanner(h *Health) string {
	if h == nil {
		return "lolad is running"
	}
	line := "lolad " + strings.TrimPrefix(h.Version, "v")
	if h.Install == "cli" {
		line = "Connected to " + line
		if h.Exe != "" {
			line += " at " + h.Exe
		}
		return line
	}
	return line + " is running"
}

// migrationText is the "Switch to the app's lolad" explanation, with the exact
// commands that stop the usual service managers. The app never runs them and
// never edits another manager's config.
func migrationText(h *Health, launchdLabels []string) string {
	var b strings.Builder
	b.WriteString("The lolad that is running")
	if h != nil && h.Exe != "" {
		fmt.Fprintf(&b, " (%s)", h.Exe)
	}
	b.WriteString(" was not installed by this app.\n\n")
	b.WriteString("To let the app manage lolad, stop it first. Your agents keep running: the new lolad adopts them, using the same config and data.\n\n")
	b.WriteString("Run the line that matches how you started it:\n\n")
	b.WriteString("  PM2:      pm2 stop lolad && pm2 save\n")
	if len(launchdLabels) == 0 {
		b.WriteString("  launchd:  launchctl bootout gui/$(id -u)/<label>\n")
	}
	for _, l := range launchdLabels {
		fmt.Fprintf(&b, "  launchd:  launchctl bootout gui/$(id -u)/%s\n", l)
	}
	b.WriteString("  Terminal: press Ctrl-C in the window running `lolad serve`\n\n")
	b.WriteString("Then choose Continue. If you would rather keep it as it is, choose Keep; the app then just connects to it.")
	return b.String()
}

// launchdLabels lists loaded launchd jobs whose label mentions lolad. It only
// reads. (PM2 is deliberately not queried: asking it can start its daemon.)
func launchdLabels() []string {
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return nil
	}
	return parseLaunchdLabels(string(out))
}

func parseLaunchdLabels(list string) []string {
	var labels []string
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		l := f[len(f)-1]
		if strings.Contains(strings.ToLower(l), "lolad") && !strings.HasPrefix(l, "application.") && l != "dev.caceres.lolad.desktop" {
			labels = append(labels, l)
		}
	}
	return labels
}
