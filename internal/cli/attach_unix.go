//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func attachProcess(bin string, argv []string) error { return syscall.Exec(bin, argv, os.Environ()) }
