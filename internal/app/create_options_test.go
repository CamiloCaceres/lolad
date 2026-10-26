package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

func TestCreateOptionsDiscovery(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "ready"), "lolabot.yaml", "agent:\n  name: Atlas\nruntime:\n  slug: navigator\n  harness: codewhale\n  model: custom-model\n  autostart: false\n")
	write(filepath.Join(root, "ready"), "CLAUDE.md", "# Atlas")
	write(filepath.Join(root, "old"), "lolabot.yaml", "agent:\n  name: Archive Helper\n")
	write(filepath.Join(root, "nested", "too-deep"), "lolabot.yaml", "agent:\n  name: Invisible\n")
	write(outside, "lolabot.yaml", "agent:\n  name: Explicit\n")
	write(outside, "CLAUDE.md", "# Explicit")
	config := filepath.Join(t.TempDir(), "config.yaml")
	write(filepath.Dir(config), filepath.Base(config), "agents_dirs: ["+root+"]\nnew_agents_dir: "+root+"\nagents: ["+outside+"]\n")
	r, err := registry.New(config)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "broken"), "lolabot.yaml", "private: [unclosed-test-secret\n")
	a := &app.App{Config: r.Config(), Registry: r}
	got, err := a.CreateOptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultDir != root || len(got.Candidates) != 4 {
		t.Fatalf("options %+v", got)
	}
	byName := map[string]core.AdoptCandidate{}
	for _, candidate := range got.Candidates {
		byName[filepath.Base(candidate.Dir)] = candidate
		if strings.Contains(candidate.Error, "test-secret") {
			t.Fatal("parser leaked source text")
		}
	}
	ready := byName["ready"]
	if ready.Name != "Atlas" || ready.Slug != "navigator" || ready.Harness != core.HarnessCodewhale || ready.Model != "custom-model" || !ready.Registered || ready.Error != "" {
		t.Fatalf("ready %+v", ready)
	}
	if byName["old"].Slug != "archive-helper" || byName["old"].Error == "" || byName["broken"].Error == "" {
		t.Fatal(byName)
	}
	if _, ok := byName[filepath.Base(outside)]; !ok {
		t.Fatal("explicit directory omitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.CreateOptions(ctx); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}
