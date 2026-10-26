//go:build !windows

package tmux

import "syscall"

func execEnvProcess(bin string, argv, env []string, statusPath string) error {
	return syscall.Exec(bin, argv, env)
}

func exitStatusArgs(dir, name string) []string               { return nil }
func (s *Sessions) exitStatus(name string, fallback int) int { return fallback }
