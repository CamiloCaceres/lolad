// Package binpath finds the agent harness executables (claude, codewhale,
// codex). Onboarding (internal/setup) and the launcher (internal/harness) share
// it, so a binary that setup reports as installed is one the launcher finds.
package binpath

import (
	"os"
	"path/filepath"
	"strings"
)

// programs are the folders under %LOCALAPPDATA%\Programs that installers use.
var programs = []string{"Claude Code", "claude", "Codex", "codex", "Codewhale", "codewhale"}

// WellKnown is every per-user directory installers put a harness in, beyond
// PATH: Anthropic's native installer uses ~/.local/bin (claude.exe on
// Windows), npm and friends their own. getenv reads the environment.
func WellKnown(goos, home string, getenv func(string) string) []string {
	var dirs []string
	add := func(d string) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	if home != "" {
		for _, d := range [][]string{{".local", "bin"}, {".claude", "local"}, {".npm-global", "bin"}, {".bun", "bin"}, {".volta", "bin"}, {".cargo", "bin"}} {
			add(filepath.Join(append([]string{home}, d...)...))
		}
	}
	if goos == "windows" {
		local := getenv("LOCALAPPDATA")
		if local == "" && home != "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		if local != "" {
			add(filepath.Join(local, "Programs"))
			for _, p := range programs {
				add(filepath.Join(local, "Programs", p))
			}
		}
		if a := getenv("APPDATA"); a != "" {
			add(filepath.Join(a, "npm"))
		}
	}
	return dirs
}

// System is the shared system directories a package manager installs into.
func System(goos string) []string {
	if goos == "windows" {
		return nil
	}
	return []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
}

// Find returns the first executable called bin in dirs, or "". On Windows
// bin.exe, bin.cmd and bin.bat are tried, in that order, before bin.
func Find(dirs []string, bin, goos string) string {
	names := []string{bin}
	if goos == "windows" {
		names = []string{bin + ".exe", bin + ".cmd", bin + ".bat", bin}
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for _, n := range names {
			if p := filepath.Join(d, n); Executable(p, goos) {
				return p
			}
		}
	}
	return ""
}

// Executable reports a regular file that may be run: on Unix one with an
// execute bit, on Windows any (the extension decides).
func Executable(p, goos string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return goos == "windows" || fi.Mode().Perm()&0o111 != 0
}

// Merge joins PATH lists, keeping the first of each directory. On Windows
// directories compare without case and trailing separators.
func Merge(goos string, sep string, lists ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, d := range strings.Split(l, sep) {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			k := d
			if goos == "windows" {
				k = strings.ToLower(strings.TrimRight(d, `\/`))
			}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, sep)
}
