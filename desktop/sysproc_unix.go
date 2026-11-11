//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// hideWindow keeps a child from opening a console window. Only Windows has one.
func hideWindow(*exec.Cmd) {}
