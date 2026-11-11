//go:build !darwin && !windows

package updater

import "errors"

func (u *Updater) platformApply(string) error {
	return errors.New("automatic updates are only supported on macOS and Windows")
}
