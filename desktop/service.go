package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ServiceStatus is the contract of `lolad service status --json`
// (docs/desktop.md §5, §8).
type ServiceStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Mechanism string `json:"mechanism"` // smappservice | launchd | run-key | ...
	Path      string `json:"path"`
}

func parseServiceStatus(b []byte) (ServiceStatus, error) {
	var s ServiceStatus
	// Tolerate log lines before the JSON object.
	if i := bytes.IndexByte(b, '{'); i > 0 {
		b = b[i:]
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("unreadable service status: %w", err)
	}
	return s, nil
}

// runLolad runs the lolad CLI with a clean environment and no console window.
func runLolad(timeout time.Duration, args ...string) ([]byte, error) {
	bin, err := findLolad()
	if err != nil {
		return nil, fmt.Errorf("lolad executable not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = cleanEnv()
	hideWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), errors.New(clip(msg, 300))
	}
	return out.Bytes(), nil
}

// serviceStatus asks the CLI. An error means the CLI is missing or too old to
// know `service`; callers treat that as "unknown", not as "not installed".
func serviceStatus() (ServiceStatus, error) {
	b, err := runLolad(15*time.Second, "service", "status", "--json")
	if err != nil {
		return ServiceStatus{}, err
	}
	return parseServiceStatus(b)
}

func serviceInstall() error {
	_, err := runLolad(60*time.Second, "service", "install")
	return err
}

func serviceUninstall() error {
	_, err := runLolad(60*time.Second, "service", "uninstall")
	return err
}

func serviceStart() error {
	_, err := runLolad(60*time.Second, "service", "start")
	return err
}

func serviceStop() error {
	_, err := runLolad(60*time.Second, "service", "stop")
	return err
}

// runValueName is the HKCU Run value (Windows) for the app's own login item.
const runValueName = "LoladApp" // HKCU value names are case-insensitive: must differ from the daemon's "lolad"

// runValue is the Windows Run command: tray only, no window.
func runValue(exe string) string { return `"` + exe + `" --background` }
