package tmux

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
)

// DefaultBinary selects the platform multiplexer; explicit config takes precedence.
func DefaultBinary() string {
	if bin := os.Getenv("LOLAD_TMUX_BIN"); bin != "" {
		return bin
	}
	if runtime.GOOS == "windows" {
		if bin, err := exec.LookPath("psmux"); err == nil {
			return bin
		}
	}
	return "tmux"
}

// execEnvServed records that this binary answers `exec-env` (ServeExecEnv ran).
var execEnvServed atomic.Bool

// ServeExecEnv makes the running binary usable as the exec-env helper. Called
// as `<binary> exec-env …` it runs ExecEnv and never returns; otherwise it
// records that the helper is available. A Go test binary must call it first
// thing in TestMain: one that ignores the `exec-env` argument reruns its whole
// suite inside every session it starts, and each of those starts more (a fork
// bomb that once left ~1,800 tmux servers on the host).
func ServeExecEnv() {
	if len(os.Args) > 1 && os.Args[1] == "exec-env" {
		err := ExecEnv(os.Args[2:])
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(127)
	}
	execEnvServed.Store(true)
}

// currentExecutable is the default exec-env helper: lolad itself. A test
// binary qualifies only after ServeExecEnv, so Start fails loudly instead.
func currentExecutable() string {
	bin, _ := os.Executable()
	base := strings.TrimSuffix(strings.ToLower(bin), ".exe")
	if strings.HasSuffix(base, ".test") && !execEnvServed.Load() {
		return ""
	}
	return bin
}
