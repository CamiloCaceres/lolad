//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func (u *Updater) platformApply(installer string) error {
	exe := u.cfg.AppPath
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return fmt.Errorf("cannot find the running executable: %w", err)
		}
	}
	return u.applyWin(installer, exe, startDetached)
}

func startDetached(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000008} // NEW_PROCESS_GROUP | DETACHED_PROCESS
	return cmd.Start()
}
