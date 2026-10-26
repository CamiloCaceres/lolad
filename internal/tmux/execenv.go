package tmux

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Agent environments carry secrets (the agent's env file), so they never go on
// a command line, where ps shows them, nor into the tmux server's own
// environment. Start writes them to a 0600 file and launches the agent through
// `lolad exec-env <file> -- argv...`, which reads and deletes the file, sets
// exactly that environment and execs the agent.

// passThrough are the variables tmux sets for the pane; they survive unless
// the agent's environment sets them itself.
var passThrough = []string{"TERM", "COLORTERM", "TMUX", "TMUX_PANE"}

// writeEnvFile stores env as NUL-separated KEY=VALUE records in dir.
func writeEnvFile(dir string, env []string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(dir, "env-"+hex.EncodeToString(b[:])), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	for _, v := range env {
		if strings.ContainsRune(v, 0) {
			continue
		}
		if _, err := f.WriteString(v + "\x00"); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", err
		}
	}
	return f.Name(), f.Close()
}

// readEnvFile reads and removes an env file written by writeEnvFile.
func readEnvFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	var env []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), len(data)+1)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		if v := sc.Text(); strings.Contains(v, "=") {
			env = append(env, v)
		}
	}
	return env, sc.Err()
}

// childEnv is the agent's environment plus the pane variables tmux provides.
func childEnv(env []string, getenv func(string) string) []string {
	set := map[string]bool{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if runtime.GOOS == "windows" {
			k = strings.ToUpper(k)
		}
		set[k] = true
	}
	out := append([]string{}, env...)
	for _, k := range passThrough {
		if v := getenv(k); v != "" && !set[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// ExecEnv implements `lolad exec-env <file> -- argv...`. It only returns on error.
func ExecEnv(args []string) error {
	statusPath := ""
	if len(args) >= 5 && args[1] == "--exit-status" {
		statusPath = args[2]
		args = append([]string{args[0]}, args[3:]...)
	}
	if len(args) < 3 || args[1] != "--" {
		return fmt.Errorf("usage: lolad exec-env <file> -- command [args...]")
	}
	env, err := readEnvFile(args[0])
	if err != nil {
		return fmt.Errorf("exec-env: %w", err)
	}
	env = childEnv(env, os.Getenv)
	argv := args[2:]
	bin := argv[0]
	if !strings.ContainsAny(bin, `/\`) {
		path := ""
		for _, v := range env {
			k, value, _ := strings.Cut(v, "=")
			if k == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(k, "PATH")) {
				path = value
			}
		}
		os.Setenv("PATH", path)
		if bin, err = exec.LookPath(argv[0]); err != nil {
			return fmt.Errorf("exec-env: %w", err)
		}
	}
	return execEnvProcess(bin, argv, env, statusPath)
}
