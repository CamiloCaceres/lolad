//go:build !windows

package cli

import "os/exec"

// hideWindow and detach only matter on Windows.
func hideWindow(*exec.Cmd) {}
func detach(*exec.Cmd)     {}
