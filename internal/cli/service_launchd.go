package cli

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const launchdLabel = "dev.caceres.lolad"

type launchdManager struct{ env serviceEnv }

func (m *launchdManager) plistPath() string {
	return filepath.Join(m.env.Home, "Library", "LaunchAgents", launchdLabel+".plist")
}
func (m *launchdManager) domain() string { return "gui/" + m.env.UID }
func (m *launchdManager) target() string { return m.domain() + "/" + launchdLabel }

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// renderPlist is the whole LaunchAgent definition. The environment is fixed:
// no variable of the caller is ever copied in, so no token can leak into the
// daemon (agents inherit its environment).
func renderPlist(exe, home, path, install, logFile string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key>
	<dict><key>SuccessfulExit</key><false/></dict>
	<key>ProcessType</key><string>Interactive</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key><string>` + xmlEscape(home) + `</string>
		<key>PATH</key><string>` + xmlEscape(path) + `</string>
		<key>LOLAD_INSTALL</key><string>` + xmlEscape(install) + `</string>
	</dict>
	<key>StandardOutPath</key><string>` + xmlEscape(logFile) + `</string>
	<key>StandardErrorPath</key><string>` + xmlEscape(logFile) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func (m *launchdManager) loaded() (running, loaded bool) {
	out, err := m.env.Run("launchctl", "print", m.target())
	if err != nil {
		return false, false
	}
	return strings.Contains(string(out), "state = running"), true
}

func (m *launchdManager) Install(exe string) error {
	if exe == "" {
		return fmt.Errorf("no executable path")
	}
	logFile := filepath.Join(m.env.StateDir, "logs", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.plistPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(m.plistPath(), []byte(renderPlist(exe, m.env.Home, m.env.Path, installKindFor(exe), logFile)), 0o644); err != nil {
		return err
	}
	if _, loaded := m.loaded(); loaded {
		_, _ = m.env.Run("launchctl", "bootout", m.target())
	}
	if out, err := m.env.Run("launchctl", "bootstrap", m.domain(), m.plistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *launchdManager) Uninstall() error {
	if _, loaded := m.loaded(); loaded {
		if out, err := m.env.Run("launchctl", "bootout", m.target()); err != nil {
			return fmt.Errorf("launchctl bootout: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.Remove(m.plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *launchdManager) Status() ServiceStatus {
	_, err := os.Stat(m.plistPath())
	st := ServiceStatus{Installed: err == nil, Mechanism: "launchd", Path: m.plistPath()}
	if running, _ := m.loaded(); running {
		st.Running = true
	} else if _, ok := m.env.Health(); ok {
		st.Running = true // started outside launchd, still the daemon
	}
	return st
}

func (m *launchdManager) Start() error {
	if _, err := os.Stat(m.plistPath()); err != nil {
		return fmt.Errorf("service not installed; run 'lolad service install'")
	}
	if _, loaded := m.loaded(); !loaded {
		if out, err := m.env.Run("launchctl", "bootstrap", m.domain(), m.plistPath()); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil // RunAtLoad starts it
	}
	if out, err := m.env.Run("launchctl", "kickstart", m.target()); err != nil {
		return fmt.Errorf("launchctl kickstart: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop sends SIGTERM; serve exits 0, which KeepAlive {SuccessfulExit:false}
// does not restart. The job stays loaded so Start can kickstart it.
func (m *launchdManager) Stop() error {
	if _, loaded := m.loaded(); !loaded {
		return nil
	}
	if out, err := m.env.Run("launchctl", "kill", "SIGTERM", m.target()); err != nil {
		return fmt.Errorf("launchctl kill: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for i := 0; i < 50; i++ {
		if running, _ := m.loaded(); !running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("lolad did not stop within 10s")
}
