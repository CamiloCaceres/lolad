//go:build darwin

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func (u *Updater) platformApply(zipPath string) error {
	app := u.cfg.AppPath
	if app == "" {
		app = runningBundle()
	}
	if app == "" {
		return fmt.Errorf("cannot find the running Lolad.app")
	}
	return u.applyMac(zipPath, app, os.Getuid(), relaunch)
}

// runningBundle walks up from the executable to the enclosing .app.
func runningBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	for p := exe; p != "/" && p != "."; p = filepath.Dir(p) {
		if strings.HasSuffix(p, ".app") {
			return p
		}
	}
	return ""
}

// relaunch opens the new app after this process has quit.
func relaunch(app string) error {
	cmd := exec.Command("/bin/sh", "-c", `sleep 1; exec /usr/bin/open -n "$1"`, "sh", app)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
