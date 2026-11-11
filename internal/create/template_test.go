package create

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func noExe() (string, error) { return "", errors.New("no executable") }

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveTemplatePrecedenceAndFirstUseCopy(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state dir")
	user := syntheticTemplate(t)
	bundle := syntheticTemplate(t)
	writeFile(t, filepath.Join(bundle, "VERSION"), "1.2.3\n", 0o644)
	writeFile(t, filepath.Join(bundle, ".git", "config"), "skipped\n", 0o644)
	cfg := TemplateConfig{Configured: user, StateDir: state, Executable: noExe, Getenv: envOf(map[string]string{BundleEnv: bundle})}

	// A user-set template wins over everything, and nothing is copied.
	got, err := ResolveTemplate(cfg)
	if err != nil || got.Dir != user || !got.UserSet {
		t.Fatal(got, err)
	}
	if _, err := os.Stat(filepath.Join(state, "lolabot")); err == nil {
		t.Fatal("copied despite a user-set template")
	}

	// Unset, or pointing at nothing usable: the bundle is copied once.
	for _, configured := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		cfg.Configured = configured
		got, err = ResolveTemplate(cfg)
		want := filepath.Join(state, "lolabot", "1.2.3")
		if err != nil || got.Dir != want || got.UserSet {
			t.Fatal(got, err)
		}
	}
	if string(mustRead(t, filepath.Join(want(state), "tools", "run.sh"))) != "#!/bin/sh\necho {{AGENT_NAME}}\n" {
		t.Fatal("tools not copied")
	}
	if info, _ := os.Stat(filepath.Join(want(state), "tools", "run.sh")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatal("mode lost", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(want(state), ".git")); err == nil {
		t.Fatal("copied .git")
	}
	// Second use reuses the copy even if the bundle changes or disappears.
	writeFile(t, filepath.Join(bundle, "CLAUDE.TEMPLATE.md"), "changed\n", 0o644)
	os.RemoveAll(bundle)
	cfg.Getenv = envOf(nil)
	got, err = ResolveTemplate(cfg)
	if err != nil || got.Dir != want(state) {
		t.Fatal(got, err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(got.Dir, "CLAUDE.TEMPLATE.md"))), "{{AGENT_NAME}}") {
		t.Fatal("copy was overwritten")
	}
}

func want(state string) string { return filepath.Join(state, "lolabot", "1.2.3") }

func TestResolveTemplateNeverWritesBundleAndDerivesVersion(t *testing.T) {
	bundle := syntheticTemplate(t)
	before := snapshot(t, bundle)
	state := t.TempDir()
	got, err := ResolveTemplate(TemplateConfig{StateDir: state, Executable: noExe, Getenv: envOf(map[string]string{BundleEnv: bundle})})
	if err != nil {
		t.Fatal(err)
	}
	if v := filepath.Base(got.Dir); len(v) != 12 {
		t.Fatalf("derived version %q", v)
	}
	compareTrees(t, before, snapshot(t, bundle))
	if err := Scaffold(got.Dir, fixture{name: "A", user: "B"}.params(filepath.Join(t.TempDir(), "x"))); err != nil {
		t.Fatal(err)
	}
	compareTrees(t, before, snapshot(t, bundle))
}

func TestResolveTemplateRelativeToExecutable(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Lolad.app", "Contents")
	writeFile(t, filepath.Join(app, "MacOS", "lolad"), "bin", 0o755)
	tpl := syntheticTemplate(t)
	copyTree(t, tpl, filepath.Join(app, "Resources", "lolabot"))
	exe := func() (string, error) { return filepath.Join(app, "MacOS", "lolad"), nil }
	got, err := ResolveTemplate(TemplateConfig{StateDir: filepath.Join(root, "state"), Executable: exe, Getenv: envOf(nil)})
	if err != nil || !strings.HasPrefix(got.Dir, filepath.Join(root, "state", "lolabot")) {
		t.Fatal(got, err)
	}
	// Windows layout: lolabot\ next to the executable.
	win := filepath.Join(root, "Program Files", "Lolad")
	writeFile(t, filepath.Join(win, "lolad.exe"), "bin", 0o755)
	copyTree(t, tpl, filepath.Join(win, "lolabot"))
	exe = func() (string, error) { return filepath.Join(win, "lolad.exe"), nil }
	if got, err = ResolveTemplate(TemplateConfig{StateDir: filepath.Join(root, "state2"), Executable: exe, Getenv: envOf(nil)}); err != nil {
		t.Fatal(got, err)
	}
}

func TestResolveTemplateClearError(t *testing.T) {
	_, err := ResolveTemplate(TemplateConfig{Configured: filepath.Join(t.TempDir(), "nope"), StateDir: t.TempDir(), Executable: noExe, Getenv: envOf(nil)})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatal(err)
	}
	for _, want := range []string{"nope", BundleEnv, "../Resources/lolabot"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// Create with the native scaffold works with no bash and no tools on PATH.
func TestCreateWithoutShellFromBundle(t *testing.T) {
	t.Setenv("PATH", "")
	bundle := syntheticTemplate(t)
	state := t.TempDir()
	c, _ := newTestCreator(t, "")
	c.deps.Template = func() (Template, error) {
		return ResolveTemplate(TemplateConfig{StateDir: state, Executable: noExe, Getenv: envOf(map[string]string{BundleEnv: bundle})})
	}
	a, err := c.Create(context.Background(), core.CreateRequest{Name: "No Shell", Harness: core.HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(a.Dir, "CLAUDE.md"))), "No Shell|") {
		t.Fatal("not scaffolded from the template")
	}
}

// A user-set template whose setup.sh is not a known revision runs that script;
// a recognized or bundled one never does.
func TestUnknownSetupScriptFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script fallback is POSIX only")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("bash unavailable")
	}
	tpl := syntheticTemplate(t)
	writeFile(t, filepath.Join(tpl, "setup.sh"), "#!/usr/bin/env bash\nmkdir -p \"${@: -1}\"; echo custom > \"${@: -1}/CLAUDE.md\"; echo 'agent: {}' > \"${@: -1}/lolabot.yaml\"\n", 0o755)

	c, _ := newTestCreator(t, tpl)
	a, err := c.Create(context.Background(), core.CreateRequest{Name: "Custom", Harness: core.HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(mustRead(t, filepath.Join(a.Dir, "CLAUDE.md"))), "custom") {
		t.Fatal("unknown setup.sh was not used")
	}

	// The same template reached through the bundle path is scaffolded natively.
	c, _ = newTestCreator(t, "")
	c.deps.Template = func() (Template, error) { return Template{Dir: tpl}, nil }
	a, err = c.Create(context.Background(), core.CreateRequest{Name: "Native", Harness: core.HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(a.Dir, "CLAUDE.md"))), "Native|") {
		t.Fatal("bundled template must use the Go port")
	}

	// Script path refuses what sed/YAML cannot carry; the Go port accepts it.
	c, _ = newTestCreator(t, tpl)
	if _, err = c.Create(context.Background(), core.CreateRequest{Name: "A & B", Harness: core.HarnessClaude}); !errors.Is(err, core.ErrInvalid) {
		t.Fatal(err)
	}
}
