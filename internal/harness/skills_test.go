package harness

import (
	"encoding/json"
	"github.com/CamiloCaceres/lolad/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSkillsModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := t.TempDir()
	state := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(home, ".claude/settings.json"):              `{"enabledPlugins":{"user@market":true,"disabled@market":true},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"do-not-copy"}]}]},"env":{"SECRET":"do-not-copy"}}`,
		filepath.Join(home, ".claude/skills/user-skill/SKILL.md"): "user skill",
		filepath.Join(dir, ".claude/settings.json"):               `{"enabledPlugins":{"project@market":true,"disabled@market":false}}`,
		filepath.Join(dir, ".claude/settings.local.json"):         `{"enabledPlugins":{"local@market":true}}`,
	} {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	a := core.Agent{Name: "test", Dir: dir, Runtime: core.Runtime{Harness: core.HarnessClaude}}
	for _, mode := range []core.SkillsMode{core.SkillsInherit, core.SkillsBarebones} {
		a.Runtime.Skills = mode
		spec, e := New(core.HarnessClaude, "/bin/claude").Prepare(a, core.LaunchOpts{StateDir: state, LoladBin: "/bin/lolad"})
		if e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(filepath.Join(state, "agents/test/claude-settings.json"))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), "do-not-copy") {
			t.Fatal("user hooks/env leaked")
		}
		var overlay struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if e = json.Unmarshal(raw, &overlay); e != nil {
			t.Fatal(e)
		}
		argv := strings.Join(spec.Argv, "|")
		if mode == core.SkillsInherit {
			if !strings.Contains(argv, "--add-dir|") || !overlay.EnabledPlugins["user@market"] || !overlay.EnabledPlugins["project@market"] || !overlay.EnabledPlugins["local@market"] || overlay.EnabledPlugins["disabled@market"] {
				t.Fatalf("inherit overlay/argv: %s %s", raw, argv)
			}
			link := filepath.Join(state, "agents/test/user-skills/.claude/skills")
			got, e := filepath.EvalSymlinks(link)
			expected, _ := filepath.EvalSymlinks(filepath.Join(home, ".claude/skills"))
			if e != nil || got != expected {
				t.Fatalf("projection %s %v", got, e)
			}
		} else {
			if len(overlay.EnabledPlugins) != 0 || strings.Contains(argv, "--add-dir") {
				t.Fatalf("barebones inherited user customization: %s %s", raw, argv)
			}
			if _, e := os.Lstat(filepath.Join(state, "agents/test/user-skills/.claude/skills")); !os.IsNotExist(e) {
				t.Fatal("barebones retained prior skills projection", e)
			}
		}
		if !strings.Contains(argv, "--system-prompt-snapshot|off") {
			t.Fatal("resume must refresh skills context")
		}
	}
}
func TestCodewhaleSkillsOverlay(t *testing.T) {
	for _, original := range []string{"provider = \"deepseek\"\n", "[skills]\nscan_codewhale_only = false\ncustom = [\"x\"]\n[other]\nname = \"keep\"\n", "skills.scan_codewhale_only = false\n[other]\nname = \"keep\"\n"} {
		for _, mode := range []core.SkillsMode{core.SkillsInherit, core.SkillsBarebones} {
			got := codewhaleSkills(original, mode)
			want := "scan_codewhale_only = false"
			if mode == core.SkillsBarebones {
				want = "scan_codewhale_only = true"
			}
			if strings.Count(got, "scan_codewhale_only") != 1 || strings.Count(got, "[skills]") != 1 || !strings.Contains(got, want) {
				t.Fatal(got)
			}
			if strings.Contains(original, "custom =") && !strings.Contains(got, "custom = [\"x\"]") {
				t.Fatal(got)
			}
		}
	}
}
func TestQuotedEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=\"line\\nquote\\\"slash\\\\\"\nSINGLE='value # literal'\nPLAIN=abc # comment\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := New(core.HarnessClaude, "/bin/claude").Prepare(core.Agent{Name: "test", Dir: dir}, core.LaunchOpts{StateDir: t.TempDir(), LoladBin: "/bin/lolad"})
	if e != nil {
		t.Fatal(e)
	}
	env := strings.Join(s.Env, "\x00")
	for _, want := range []string{"TOKEN=line\nquote\"slash\\", "SINGLE=value # literal", "PLAIN=abc"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing decoded variable %q", strings.Split(want, "=")[0])
		}
	}
}
func TestModels(t *testing.T) {
	if len(Models(core.HarnessClaude)) != 9 || len(Models(core.HarnessCodewhale)) != 2 || len(Models("missing")) != 0 {
		t.Fatal("model choices missing")
	}
}

func TestResumeExactSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, kind := range []core.Harness{core.HarnessClaude, core.HarnessCodewhale} {
		a := core.Agent{Name: "test", Dir: t.TempDir(), Runtime: core.Runtime{Harness: kind}}
		adapter := New(kind, "/bin/fake")
		opts := core.LaunchOpts{StateDir: t.TempDir(), LoladBin: "/bin/lolad"}
		spec, e := adapter.PrepareResume(a, opts, "exact-session")
		if e != nil {
			t.Fatal(e)
		}
		argv := strings.Join(spec.Argv, "|")
		if !strings.Contains(argv, "--resume|exact-session") || strings.Contains(argv, "--continue") {
			t.Fatal(argv)
		}
		opts.Fresh = true
		spec, e = adapter.PrepareResume(a, opts, "exact-session")
		if e != nil {
			t.Fatal(e)
		}
		argv = strings.Join(spec.Argv, "|")
		if strings.Contains(argv, "--resume") || strings.Contains(argv, "--continue") {
			t.Fatal("fresh resumed", argv)
		}
	}
}
