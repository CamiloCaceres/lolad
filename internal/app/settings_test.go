package app_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func settingsApp(t *testing.T) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	agent := filepath.Join(dir, "agent")
	if e := os.MkdirAll(agent, 0700); e != nil {
		t.Fatal(e)
	}
	for path, content := range map[string]string{
		"lolabot.yaml":                  "# untouched\nagent:\n  name: Test\n  role: Helper\ncustom: preserved\nruntime:\n  harness: claude\n  model: haiku\n  autostart: false\n  triggers:\n    - cron: '0 8 * * *'\n      prompt: daily\n",
		"CLAUDE.md":                     "# Agent instructions\nKeep this byte for byte.\n",
		"brain/note.md":                 "original",
		".claude/skills/local/SKILL.md": "---\nname: local\ndescription: local skill\n---\nLocal\n",
	} {
		p := filepath.Join(agent, path)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	p := filepath.Join(home, ".claude/skills/library/SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("---\nname: library\ndescription: library skill\n---\nLibrary\n"), 0600)
	config := filepath.Join(dir, "config.yaml")
	os.WriteFile(config, fmt.Appendf(nil, "listen: 127.0.0.1:4196\ntmux_socket: lolad-test-%d\nagents_dirs: []\nagents: [%s]\n", time.Now().UnixNano(), agent), 0600)
	a, e := app.New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	return a, agent
}
func TestSettingsService(t *testing.T) {
	a, dir := settingsApp(t)
	ctx := t.Context()
	original, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	status, e := a.Get(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	rt := status.Runtime
	rt.Model = "custom-free-text[1m]"
	rt.Skills = core.SkillsBarebones
	updated, e := a.UpdateRuntime(ctx, "test", rt)
	if e != nil || updated.Runtime.Model != rt.Model || updated.PendingRestart {
		t.Fatalf("update: %+v %v", updated, e)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "lolabot.yaml"))
	if !bytes.HasPrefix(raw, []byte("# untouched\nagent:\n  name: Test\n  role: Helper\ncustom: preserved\n")) {
		t.Fatal("unrelated bytes changed")
	}
	bad := rt
	bad.Skills = "invalid"
	if _, e = a.UpdateRuntime(ctx, "test", bad); !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	bad = rt
	bad.Harness = core.HarnessCodewhale
	if _, e = a.UpdateRuntime(ctx, "test", bad); !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	bad = rt
	bad.Slug = "rename"
	if _, e = a.UpdateRuntime(ctx, "test", bad); !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	if e = a.SetEnv(ctx, "test", "TOKEN", "never-return-this"); e != nil {
		t.Fatal(e)
	}
	env, e := a.Env(ctx, "test")
	if e != nil || len(env) != 1 || !env[0].Set || env[0].Length != 17 {
		t.Fatalf("env metadata: %+v %v", env, e)
	}
	if e = a.SetEnv(ctx, "test", "BAD-KEY", "secret"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	if _, e = a.ReadFile(ctx, "test", ".env"); e == nil {
		t.Fatal("env exposed")
	}
	os.Symlink(".env", filepath.Join(dir, "secret.txt"))
	if _, e = a.ReadFile(ctx, "test", "secret.txt"); e == nil {
		t.Fatal("env alias exposed")
	}
	if e = a.DeleteEnv(ctx, "test", "TOKEN"); e != nil {
		t.Fatal(e)
	}
	skills, e := a.Skills(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	seen := false
	for _, s := range skills {
		if s.Source == "user" {
			seen = true
			if s.Active {
				t.Fatal("barebones user active")
			}
		}
	}
	if !seen {
		t.Fatal("library missing")
	}
	added, e := a.AddSkill(ctx, "test", "user:library")
	if e != nil || added.Source != "agent" {
		t.Fatalf("add %v %v", added, e)
	}
	if e = a.RemoveSkill(ctx, "test", "user:library"); e == nil {
		t.Fatal("removed user skill")
	}
	if e = a.RemoveSkill(ctx, "test", added.ID); e != nil {
		t.Fatal(e)
	}
	f, e := a.ReadFile(ctx, "test", "brain/note.md")
	if e != nil {
		t.Fatal(e)
	}
	f, e = a.WriteFile(ctx, "test", "brain/note.md", "changed", f.Hash)
	if e != nil || f.Content != "changed" {
		t.Fatal(e)
	}
	if _, e = a.WriteFile(ctx, "test", "brain/note.md", "stale", "wrong"); !errors.Is(e, core.ErrConflict) {
		t.Fatal(e)
	}
	yaml, e := a.ReadFile(ctx, "test", "lolabot.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.WriteFile(ctx, "test", "lolabot.yaml", strings.Replace(yaml.Content, "harness: claude", "harness: invalid", 1), yaml.Hash); !errors.Is(e, core.ErrInvalid) {
		t.Fatal(e)
	}
	rt.EnvFile = "secrets.txt"
	if _, e = a.UpdateRuntime(ctx, "test", rt); e != nil {
		t.Fatal(e)
	}
	if e = a.SetEnv(ctx, "test", "CUSTOM", "hidden-value"); e != nil {
		t.Fatal(e)
	}
	if _, e = a.ReadFile(ctx, "test", "secrets.txt"); e == nil {
		t.Fatal("custom env exposed")
	}
	files, e := a.Files(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range files {
		if f.Path == "secrets.txt" || f.Path == "secret.txt" {
			t.Fatal("secret file listed")
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !bytes.Equal(original, after) {
		t.Fatal("settings regenerated instructions")
	}
	if len(a.Models(core.HarnessClaude)) == 0 {
		t.Fatal("models missing")
	}
	if _, e = a.Env(ctx, "missing"); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
}
func TestEmailWatchOwnership(t *testing.T) {
	a, dir := settingsApp(t)
	ctx := t.Context()
	settings := core.EmailSettings{Watch: "lolad", WatchAccount: "test@example.com", Every: "3m", Accounts: []core.EmailAccount{{Address: "test@example.com", ConfigKey: "test", IMAPServer: "127.0.0.1", SMTPServer: "127.0.0.1", Password: "write-only-password"}}}
	saved, e := a.SaveEmail(ctx, "test", settings)
	if e != nil || saved.Watch != "lolad" || saved.Accounts[0].Password != "" || !saved.Accounts[0].PasswordSet {
		t.Fatalf("save %+v %v", saved, e)
	}
	if _, e = a.SaveEmail(ctx, "test", settings); e != nil {
		t.Fatal(e)
	}
	status, e := a.Get(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	mail, cron := 0, 0
	for _, tr := range status.Runtime.Triggers {
		if tr.Mail != "" {
			mail++
		}
		if tr.Cron != "" {
			cron++
		}
	}
	if mail != 1 || cron != 1 {
		t.Fatal("wrong triggers", mail, cron)
	}
	read, e := a.Email(ctx, "test")
	if e != nil || read.WatchAccount != "test@example.com" || read.Every != "3m" || read.Accounts[0].Password != "" {
		t.Fatal("email read", e)
	}
	settings.Watch = "none"
	if _, e = a.SaveEmail(ctx, "test", settings); e != nil {
		t.Fatal(e)
	}
	status, e = a.Get(ctx, "test")
	if e != nil || len(status.Runtime.Triggers) != 1 || status.Runtime.Triggers[0].Cron == "" {
		t.Fatal("removed unrelated trigger", e)
	}
	os.WriteFile(filepath.Join(dir, ".claude/settings.json"), []byte(`{"enabledPlugins":{"test-mail@local":true}}`), 0600)
	settings.Watch = "lolad"
	if _, e = a.SaveEmail(ctx, "test", settings); !errors.Is(e, core.ErrInvalid) || !strings.Contains(e.Error(), "disable") {
		t.Fatal("mail mod conflict not refused", e)
	}
	settings.Watch = "mod"
	if _, e = a.SaveEmail(ctx, "test", settings); e != nil {
		t.Fatal(e)
	}
	if _, e = a.TestEmail(ctx, "test", "missing@example.com"); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
}
