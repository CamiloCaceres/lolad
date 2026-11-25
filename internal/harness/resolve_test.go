package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The other tests launch fixture paths such as /bin/claude that need not
// exist; and no test may find the real claude in this machine's install
// folders. The tests below swap in their own.
func TestMain(m *testing.M) {
	isExecutable = func(p string) bool { return strings.HasPrefix(p, "/bin/") || exe(p) }
	installDirs = func() []string { return nil }
	os.Exit(m.Run())
}

func exe(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0)
}

func fakeBin(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func withHooks(t *testing.T, dirs []string, reg string) {
	t.Helper()
	oldDirs, oldReg := installDirs, registryPATH
	installDirs = func() []string { return dirs }
	registryPATH = func() string { return reg }
	t.Cleanup(func() { installDirs, registryPATH = oldDirs, oldReg })
}

func prepareIn(t *testing.T, a *Adapter) (core.LaunchSpec, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return a.Prepare(core.Agent{Name: "ultron", Dir: t.TempDir(), Runtime: core.Runtime{Harness: a.Kind}},
		core.LaunchOpts{Fresh: true, StateDir: t.TempDir(), LoladBin: "/opt/lolad/lolad"})
}

func envPATH(s core.LaunchSpec) string {
	for _, v := range s.Env {
		if k, val, _ := strings.Cut(v, "="); k == "PATH" {
			return val
		}
	}
	return ""
}

// Fresh Windows install: claude is in ~/.local/bin, which is neither on the
// daemon's PATH nor configured. Setup finds it there, so must the launcher.
func TestResolveFromInstallFolders(t *testing.T) {
	stale := t.TempDir()
	t.Setenv("PATH", stale)
	local := filepath.Join(t.TempDir(), ".local", "bin")
	bin := fakeBin(t, local, "claude")
	withHooks(t, []string{filepath.Join(t.TempDir(), "nothing"), local}, "")
	s, err := prepareIn(t, New(core.HarnessClaude, ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Argv[0] != bin {
		t.Fatalf("argv0 %q, want %q", s.Argv[0], bin)
	}
	sep := string(os.PathListSeparator)
	if want := "/opt/lolad" + sep + local + sep + stale; envPATH(s) != want {
		t.Fatalf("PATH %q, want %q: lolad, then the harness's folder, then the rest", envPATH(s), want)
	}
}

// The installer added its folder to the registry Path after lolad started:
// the agent's PATH includes it, so the harness is found and runs.
func TestResolveFromRegistryPATH(t *testing.T) {
	stale := t.TempDir()
	t.Setenv("PATH", stale)
	fresh := t.TempDir()
	bin := fakeBin(t, fresh, "claude")
	withHooks(t, nil, stale+string(os.PathListSeparator)+fresh)
	s, err := prepareIn(t, New(core.HarnessClaude, ""))
	if err != nil {
		t.Fatal(err)
	}
	if s.Argv[0] != bin || !strings.Contains(envPATH(s), fresh) || strings.Count(envPATH(s), stale) != 1 {
		t.Fatalf("argv0 %q PATH %q", s.Argv[0], envPATH(s))
	}
}

// Onboarding writes claude_bin after the daemon started; the configured
// Binary from startup is gone. The path config.yaml names now wins.
func TestResolveConfiguredNow(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withHooks(t, nil, "")
	bin := fakeBin(t, t.TempDir(), "claude")
	a := New(core.HarnessClaude, filepath.Join(t.TempDir(), "gone", "claude"))
	a.Configured = func() string { return bin }
	s, err := prepareIn(t, a)
	if err != nil || s.Argv[0] != bin {
		t.Fatalf("%v %v", err, s.Argv)
	}
}

func TestResolveNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withHooks(t, []string{t.TempDir()}, "")
	gone := filepath.Join(t.TempDir(), "claude")
	_, err := prepareIn(t, New(core.HarnessClaude, gone))
	var ni *NotInstalledError
	if !errors.Is(err, core.ErrHarnessMissing) || !errors.As(err, &ni) || ni.Configured != gone || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
}
