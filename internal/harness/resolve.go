package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/CamiloCaceres/lolad/internal/binpath"
	"github.com/CamiloCaceres/lolad/internal/core"
)

// Test hooks: what counts as an executable, and the install folders searched
// after PATH.
var (
	isExecutable = func(p string) bool { return binpath.Executable(p, runtime.GOOS) }
	installDirs  = func() []string {
		home, _ := os.UserHomeDir()
		return append(binpath.WellKnown(runtime.GOOS, home, os.Getenv), binpath.System(runtime.GOOS)...)
	}
)

// NotInstalledError says the harness was found nowhere lolad looked.
type NotInstalledError struct {
	Kind core.Harness
	// Configured is the configured path that no longer exists, if any.
	Configured string
}

func (e *NotInstalledError) Error() string {
	if e.Configured != "" {
		return fmt.Sprintf("%s is not installed: %s does not exist and %s is not on PATH or in the usual install folders", e.Kind, e.Configured, e.Kind)
	}
	return fmt.Sprintf("%s is not installed: not on PATH or in the usual install folders", e.Kind)
}
func (e *NotInstalledError) Unwrap() error { return core.ErrHarnessMissing }

// resolve finds the harness executable, the same way on every launch:
//  1. the configured Binary, then what config.yaml names now (Configured);
//  2. path, the agent's PATH (the daemon's, plus on Windows the registry's);
//  3. the folders installers use (binpath.WellKnown), which onboarding also
//     searches, so a harness setup shows as installed is found here too.
func (a *Adapter) resolve(path string) (string, error) {
	missing := ""
	for _, c := range []string{a.Binary, a.configured()} {
		if c == "" {
			continue
		}
		if isExecutable(c) {
			return c, nil
		}
		if missing == "" {
			missing = c
		}
	}
	if p := binpath.Find(filepath.SplitList(path), string(a.Kind), runtime.GOOS); p != "" {
		return p, nil
	}
	if p := binpath.Find(installDirs(), string(a.Kind), runtime.GOOS); p != "" {
		return p, nil
	}
	return "", &NotInstalledError{Kind: a.Kind, Configured: missing}
}

func (a *Adapter) configured() string {
	if a.Configured == nil {
		return ""
	}
	return a.Configured()
}
