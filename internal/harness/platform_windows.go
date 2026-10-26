package harness

import (
	"os"
	"strings"
)

// Windows environment keys are case-insensitive. Normalize keys that the
// harness reads explicitly and supply HOME for Unix-oriented agent tooling.
func normalizeLaunchEnv(env map[string]string) {
	for key, value := range env {
		if strings.EqualFold(key, "PATH") {
			delete(env, key)
			env["PATH"] = value
			break
		}
	}
	if env["HOME"] == "" {
		env["HOME"], _ = os.UserHomeDir()
	}
}
