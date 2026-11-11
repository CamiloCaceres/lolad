package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file holds the Windows logic (HKCU Run value via reg.exe, detached
// start, kill by pid). It is deliberately free of Windows-only imports so the
// command construction is unit-tested on every OS; only the process flags
// live in proc_windows.go.

const (
	runKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "lolad"
)

type windowsManager struct {
	env   serviceEnv
	Stat  func(string) (os.FileInfo, error)
	Kill  func(pid int) error
	Spawn func(exe string, args []string, env []string) error
}

// daemonExe is lolad-daemon.exe next to lolad.exe when present (the console-less
// build), else lolad.exe itself.
func daemonExe(cli string, stat func(string) (os.FileInfo, error)) string {
	dir, base := filepath.Split(cli)
	if strings.EqualFold(base, "lolad.exe") {
		cand := dir + "lolad-daemon.exe"
		if _, err := stat(cand); err == nil {
			return cand
		}
	}
	return cli
}

// runCommandLine is the value stored under the Run key.
func runCommandLine(daemon string) string { return `"` + daemon + `" serve --supervised` }

// windowsSpawnEnv is the fixed environment of a daemon started by `service
// start`: OS-provided locations only, no caller variables such as tokens.
func windowsSpawnEnv(get func(string) string, install string) []string {
	var env []string
	for _, k := range strings.Fields("SystemRoot SystemDrive windir ComSpec PATHEXT PATH USERNAME USERDOMAIN USERPROFILE HOMEDRIVE HOMEPATH APPDATA LOCALAPPDATA TEMP TMP ProgramData ProgramFiles ProgramFiles(x86) ProgramW6432 COMPUTERNAME NUMBER_OF_PROCESSORS PROCESSOR_ARCHITECTURE") {
		if v := get(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "LOLAD_INSTALL="+install)
}

func (m *windowsManager) registered() (string, bool) {
	out, err := m.env.Run("reg", "query", runKey, "/v", runValue)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(f), runValue+" ") || strings.HasPrefix(strings.ToLower(f), runValue+"\t") {
			if i := strings.Index(f, "REG_SZ"); i >= 0 {
				return strings.TrimSpace(f[i+len("REG_SZ"):]), true
			}
		}
	}
	return "", true
}

func (m *windowsManager) Install(exe string) error {
	daemon := daemonExe(exe, m.Stat)
	if out, err := m.env.Run("reg", "add", runKey, "/v", runValue, "/t", "REG_SZ", "/d", runCommandLine(daemon), "/f"); err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return m.startDaemon(daemon)
}

func (m *windowsManager) Uninstall() error {
	if err := m.Stop(); err != nil {
		return err
	}
	if _, ok := m.registered(); !ok {
		return nil
	}
	if out, err := m.env.Run("reg", "delete", runKey, "/v", runValue, "/f"); err != nil {
		return fmt.Errorf("reg delete: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *windowsManager) Status() ServiceStatus {
	_, installed := m.registered()
	_, running := m.env.Health()
	return ServiceStatus{Installed: installed, Running: running, Mechanism: "registry-run", Path: runKey + `\` + runValue}
}

func (m *windowsManager) Start() error {
	cmdline, ok := m.registered()
	daemon := ""
	if ok && strings.HasPrefix(cmdline, `"`) {
		if j := strings.Index(cmdline[1:], `"`); j >= 0 {
			daemon = cmdline[1 : 1+j]
		}
	}
	if daemon == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		daemon = daemonExe(exe, m.Stat)
	}
	return m.startDaemon(daemon)
}

func (m *windowsManager) startDaemon(daemon string) error {
	if _, running := m.env.Health(); running {
		return nil
	}
	if err := m.Spawn(daemon, []string{"serve", "--supervised"}, windowsSpawnEnv(os.Getenv, installKindFor(daemon))); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		if _, running := m.env.Health(); running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("lolad did not come up within 10s; see %s", filepath.Join(m.env.StateDir, "logs", "daemon.log"))
}

// Stop ends the watchdog first (or it would restart the daemon), then the
// daemon. Agents live in the tmux/psmux server and are not touched.
func (m *windowsManager) Stop() error {
	h, running := m.env.Health()
	if b, err := os.ReadFile(filepath.Join(m.env.StateDir, "supervisor.pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
			_ = m.Kill(pid)
		}
	}
	if running && h.PID > 0 {
		if err := m.Kill(h.PID); err != nil {
			return err
		}
		for i := 0; i < 50; i++ {
			if _, up := m.env.Health(); !up {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("lolad did not stop within 10s")
	}
	return nil
}

func killPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func spawnDetached(exe string, args []string, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
