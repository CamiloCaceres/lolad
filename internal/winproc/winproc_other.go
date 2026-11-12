//go:build !windows

// Package winproc keeps helper processes from opening console windows on
// Windows. It does nothing elsewhere.
package winproc

import "os/exec"

func Hide(c *exec.Cmd) *exec.Cmd { return c }
