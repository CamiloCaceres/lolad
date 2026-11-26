//go:build windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000
const detachedProcess = 0x00000008

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | detachedProcess}
}

// hideWindow makes a child process start without a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
}

// terminate is only used by the macOS updater (stopAppDaemon).
func terminate(int) error { return errors.New("not supported on Windows") }
