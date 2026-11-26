//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func (u *Updater) platformApply(installer string) error {
	exe, err := u.appExe()
	if err != nil {
		return err
	}
	return u.applyWin(installer, exe, startDetached)
}

func (u *Updater) platformVerify(installer string) error {
	exe, err := u.appExe()
	if err != nil {
		return err
	}
	return u.verifyWinInstaller(installer, exe)
}

func (u *Updater) appExe() (string, error) {
	if u.cfg.AppPath != "" {
		return u.cfg.AppPath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find the running executable: %w", err)
	}
	return exe, nil
}

func startDetached(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000008} // NEW_PROCESS_GROUP | DETACHED_PROCESS
	return cmd.Start()
}
