//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

const (
	createNoWindow        = 0x08000000
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// hideWindow keeps helper processes (reg.exe, taskkill) from flashing a console.
func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// detach starts c outside the caller's console and process group so it
// survives the caller (the app or a terminal) exiting.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | detachedProcess | createNewProcessGroup}
}
