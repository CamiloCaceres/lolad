package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func execEnvProcess(bin string, argv, env []string, statusPath string) error {
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		code = exit.ExitCode()
	}
	if statusPath != "" {
		if err := os.WriteFile(statusPath, []byte(strconv.Itoa(code)), 0600); err != nil {
			return err
		}
	}
	os.Exit(code)
	return nil
}

// psmux 3.4 reports pane_dead_status=0 for every process. Keep the child's
// actual status in private state, including across daemon restarts.
func exitStatusArgs(dir, name string) []string {
	path := filepath.Join(dir, name+".exit")
	_ = os.Remove(path)
	return []string{"--exit-status", path}
}
func (s *Sessions) exitStatus(name string, fallback int) int {
	b, err := os.ReadFile(filepath.Join(s.RunDir, name+".exit"))
	if err != nil {
		return -1 // interrupted wrapper: the real exit status is unknown
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1 // interrupted wrapper: the real exit status is unknown
	}
	return code
}
