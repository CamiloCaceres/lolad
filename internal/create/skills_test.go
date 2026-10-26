package create

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

type assertSkillsStarter struct {
	t      *testing.T
	dir    string
	called bool
}

func (s *assertSkillsStarter) Start(context.Context, string) error {
	s.called = true
	data := mustRead(s.t, filepath.Join(s.dir, "lolabot.yaml"))
	var doc struct {
		Runtime core.Runtime `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Runtime.Skills != core.SkillsBarebones {
		s.t.Fatalf("skills absent at first launch: %+v %v", doc, err)
	}
	return nil
}

func TestCreatePersistsSkillsBeforeStart(t *testing.T) {
	template := t.TempDir()
	script := `#!/bin/sh
for dir; do :; done
printf 'agent:\n  name: Worker\n' > "$dir/lolabot.yaml"
printf '# Worker\n' > "$dir/CLAUDE.md"
`
	if err := os.WriteFile(filepath.Join(template, "setup.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, _ := newTestCreator(t, template)
	starter := &assertSkillsStarter{t: t, dir: filepath.Join(c.deps.AgentsDir, "worker")}
	c.deps.Starter = starter
	got, err := c.Create(t.Context(), core.CreateRequest{Name: "Worker", Harness: core.HarnessClaude, Skills: core.SkillsBarebones, Start: true})
	if err != nil || got.Runtime.Skills != core.SkillsBarebones || !starter.called {
		t.Fatalf("%+v %v called=%v", got, err, starter.called)
	}
	stopped, err := c.Create(t.Context(), core.CreateRequest{Name: "Later", Harness: core.HarnessClaude, Mission: "Do this later"})
	if err != nil || stopped.Runtime.WakeOnMessageEnabled() {
		t.Fatalf("stopped mission can wake agent: %+v %v", stopped, err)
	}
	if _, err = c.Create(t.Context(), core.CreateRequest{Name: "Other", Harness: core.HarnessClaude, Skills: "invalid"}); err == nil {
		t.Fatal("invalid skills accepted")
	}
}
