package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// systemdManager is a best-effort systemd user unit for Linux.
type systemdManager struct{ env serviceEnv }

const systemdUnit = "lolad.service"

func (m *systemdManager) unitPath() string {
	return filepath.Join(m.env.Home, ".config", "systemd", "user", systemdUnit)
}

func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func renderUnit(exe, home, path, install, logFile string) string {
	return "[Unit]\nDescription=lolad agent daemon\n\n[Service]\n" +
		"ExecStart=" + systemdQuote(exe) + " serve\n" +
		"Restart=on-failure\nRestartSec=2\n" +
		"Environment=" + systemdQuote("HOME="+home) + "\n" +
		"Environment=" + systemdQuote("PATH="+path) + "\n" +
		"Environment=" + systemdQuote("LOLAD_INSTALL="+install) + "\n" +
		"StandardOutput=append:" + logFile + "\nStandardError=append:" + logFile + "\n\n" +
		"[Install]\nWantedBy=default.target\n"
}

func (m *systemdManager) ctl(args ...string) error {
	out, err := m.env.Run("systemctl", append([]string{"--user"}, args...)...)
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *systemdManager) Install(exe string) error {
	logFile := filepath.Join(m.env.StateDir, "logs", "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.unitPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(m.unitPath(), []byte(renderUnit(exe, m.env.Home, m.env.Path, installKindFor(exe), logFile)), 0o644); err != nil {
		return err
	}
	if err := m.ctl("daemon-reload"); err != nil {
		return err
	}
	return m.ctl("enable", "--now", systemdUnit)
}

func (m *systemdManager) Uninstall() error {
	_ = m.ctl("disable", "--now", systemdUnit)
	if err := os.Remove(m.unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = m.ctl("daemon-reload")
	return nil
}

func (m *systemdManager) Status() ServiceStatus {
	_, err := os.Stat(m.unitPath())
	st := ServiceStatus{Installed: err == nil, Mechanism: "systemd-user", Path: m.unitPath()}
	out, _ := m.env.Run("systemctl", "--user", "is-active", systemdUnit)
	st.Running = strings.TrimSpace(string(out)) == "active"
	if !st.Running {
		_, st.Running = m.env.Health()
	}
	return st
}

func (m *systemdManager) Start() error { return m.ctl("start", systemdUnit) }
func (m *systemdManager) Stop() error  { return m.ctl("stop", systemdUnit) }
