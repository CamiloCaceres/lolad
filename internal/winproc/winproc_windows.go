// Package winproc keeps helper processes from opening console windows on
// Windows. lolad's daemon has no console, so every console program it starts
// would otherwise get a visible window of its own.
package winproc

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// Hide makes c start without a console window. It keeps any flags already set.
func Hide(c *exec.Cmd) *exec.Cmd {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= createNoWindow
	return c
}
