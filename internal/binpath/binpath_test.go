package binpath

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFindWindowsPrefersExe(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"claude", "claude.cmd", "claude.exe"} {
		os.WriteFile(filepath.Join(d, n), []byte("x"), 0o644)
	}
	if got := Find([]string{t.TempDir(), d}, "claude", "windows"); got != filepath.Join(d, "claude.exe") {
		t.Fatal(got)
	}
	// Unix needs the execute bit.
	if got := Find([]string{d}, "claude", "linux"); got != "" {
		t.Fatal(got)
	}
	os.Chmod(filepath.Join(d, "claude"), 0o755)
	if got := Find([]string{d}, "claude", "linux"); got != filepath.Join(d, "claude") {
		t.Fatal(got)
	}
}

func TestWellKnownWindows(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": `C:\Users\j\AppData\Local`, "APPDATA": `C:\Users\j\AppData\Roaming`}
	got := WellKnown("windows", `C:\Users\j`, func(k string) string { return env[k] })
	for _, want := range []string{
		filepath.Join(`C:\Users\j`, ".local", "bin"),
		filepath.Join(env["LOCALAPPDATA"], "Programs", "Claude Code"),
		filepath.Join(env["APPDATA"], "npm"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if System("windows") != nil || len(System("darwin")) == 0 {
		t.Fatal("system dirs")
	}
}

func TestMerge(t *testing.T) {
	got := Merge("windows", ";", `C:\Windows;C:\Users\j\.local\bin\`, `c:\windows;C:\Users\J\.local\bin;D:\new`)
	if got != `C:\Windows;C:\Users\j\.local\bin\;D:\new` {
		t.Fatal(got)
	}
	if got := Merge("linux", ":", "/a:/b", "/B:/a::/c"); got != "/a:/b:/B:/c" {
		t.Fatal(got)
	}
}
