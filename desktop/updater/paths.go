package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OnboardingFlagName is the file whose presence blocks updates.
const OnboardingFlagName = "onboarding-in-progress"

// StateDir is lolad's state dir (LOLAD_STATE_DIR overrides).
func StateDir() string {
	if d := os.Getenv("LOLAD_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "lolad", "state")
		}
	}
	return filepath.Join(home, ".local", "state", "lolad")
}

func DefaultOnboardingFlag() string { return filepath.Join(StateDir(), OnboardingFlagName) }

// DefaultMultiplexerFile finds multiplexer.version next to the executable
// (Windows) or in the bundle's Resources (macOS).
func DefaultMultiplexerFile() string {
	exe, err := os.Executable()
	if err != nil {
		return "multiplexer.version"
	}
	dir := filepath.Dir(exe)
	if runtime.GOOS == "darwin" {
		return filepath.Join(dir, "..", "Resources", "multiplexer.version")
	}
	return filepath.Join(dir, "multiplexer.version")
}

// runner runs an external command and returns combined output.
type runner func(name string, args ...string) ([]byte, error)

func execRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
