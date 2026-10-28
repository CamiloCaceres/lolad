package supervisor

import (
	"context"
	"database/sql"
	"github.com/CamiloCaceres/lolad/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingRestartAndPersistence(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	f := setup(t, func(o *Options) { o.DB = db; o.Agents[0].Dir = dir; o.Agents[0].Runtime.Model = "haiku" })
	if e := f.s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	status, e := f.s.Get(ctx, "agent")
	if e != nil || status.PendingRestart {
		t.Fatalf("initial pending: %v %v", status.PendingRestart, e)
	}
	if e = os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=never-print-this\n"), 0600); e != nil {
		t.Fatal(e)
	}
	f.s.mu.Lock()
	f.s.agents["agent"].Status.Runtime.Model = "sonnet"
	f.s.mu.Unlock()
	status, e = f.s.Get(ctx, "agent")
	if e != nil || !status.PendingRestart || status.PendingReason != "model changed" {
		t.Fatalf("claude re-reads .env per command, model needs a launch: %v %q %v", status.PendingRestart, status.PendingReason, e)
	}
	// A session launched before live env existed still needs the restart.
	f.s.mu.Lock()
	f.s.agents["agent"].LiveEnv = false
	f.s.mu.Unlock()
	status, e = f.s.Get(ctx, "agent")
	if e != nil || !strings.Contains(status.PendingReason, "model changed") || !strings.Contains(status.PendingReason, "env file changed") {
		t.Fatalf("pending missing: %v %s %v", status.PendingRestart, status.PendingReason, e)
	}
	if strings.Contains(status.PendingReason, "never-print") {
		t.Fatal("secret in reason")
	}
	// Hashes persist across daemon restarts while the same pane survives.
	f.s.mu.Lock()
	e = f.s.persist(f.s.agents["agent"])
	f.s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	o := f.o
	o.Agents[0].Runtime.Model = "sonnet"
	restored, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	status, e = restored.Get(ctx, "agent")
	if e != nil || !status.PendingRestart {
		t.Fatalf("lost pending drift on adoption: %v", e)
	}
	if e = f.s.Restart(ctx, "agent", false); e != nil {
		t.Fatal(e)
	}
	f.c.advance(11 * time.Second)
	if e = f.s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = f.s.Get(ctx, "agent")
	if e != nil || status.PendingRestart {
		t.Fatalf("pending not cleared after launch: %s %v", status.PendingReason, e)
	}
	if e = os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=changed\n"), 0600); e != nil {
		t.Fatal(e)
	}
	f.s.mu.Lock()
	f.s.agents["agent"].LiveEnv = false
	f.s.mu.Unlock()
	if status, e = f.s.Get(ctx, "agent"); e != nil || status.PendingReason != "env file changed" {
		t.Fatalf("running pane drift: %q %v", status.PendingReason, e)
	}
	// Status reads trust the reconciled state instead of asking tmux, so a
	// vanished pane stops being pending once a tick has seen it.
	f.sessions.exists = false
	if e = f.s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	status, e = f.s.Get(ctx, "agent")
	if e != nil || status.PendingRestart {
		t.Fatal("stopped pane marked pending", e)
	}
}

type exactLauncher struct {
	fakeLauncher
	resumed []string
}

func (l *exactLauncher) PrepareResume(a core.Agent, o core.LaunchOpts, id string) (core.LaunchSpec, error) {
	l.resumed = append(l.resumed, id)
	return l.Prepare(a, o)
}
func TestExactSessionSurvivesLaunchAndDaemonRestart(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "sessions.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	launcher := &exactLauncher{}
	f := setup(t, func(o *Options) { o.DB = db; o.Launchers[core.HarnessClaude] = launcher })
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "session_start", SessionID: "owned-session", Seq: 1})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", SessionID: "owned-session", Seq: 2})
	// A later unrelated conversation in the same dir cannot replace this ID.
	restored, e := New(f.o)
	if e != nil {
		t.Fatal(e)
	}
	if e = restored.Restart(ctx, "agent", false); e != nil {
		t.Fatal(e)
	}
	f.c.advance(11 * time.Second)
	if e = restored.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if len(launcher.resumed) != 1 || launcher.resumed[0] != "owned-session" {
		t.Fatal("lost exact resume", launcher.resumed)
	}
	// If the new process dies before any hook, retain the last known session ID.
	restored.mu.Lock()
	r := restored.agents["agent"]
	if r.HookSession != "" || r.HarnessSession != "owned-session" {
		t.Fatal("launch lost resume ID")
	}
	restored.mu.Unlock()
	if e = restored.Restart(ctx, "agent", false); e != nil {
		t.Fatal(e)
	}
	f.c.advance(11 * time.Second)
	if e = restored.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if len(launcher.resumed) != 2 || launcher.resumed[1] != "owned-session" {
		t.Fatal(launcher.resumed)
	}
	if e = restored.Restart(ctx, "agent", true); e != nil {
		t.Fatal(e)
	}
	f.c.advance(11 * time.Second)
	if e = restored.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if len(launcher.resumed) != 2 {
		t.Fatal("fresh launch resumed")
	}
}

func TestLoladSideRuntimeNeedsNoRestart(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	no := false
	f.s.mu.Lock()
	rt := &f.s.agents["agent"].Status.Runtime
	rt.OnWake = "new wake prompt"
	rt.Rotate = "04:00"
	rt.Autostart = &no
	rt.WakeOnMessage = &no
	rt.Triggers = []core.Trigger{{Cron: "0 9 * * *", Prompt: "standup"}}
	f.s.mu.Unlock()
	status, e := f.s.Get(ctx, "agent")
	if e != nil || status.PendingRestart {
		t.Fatalf("lolad-side fields marked pending: %q %v", status.PendingReason, e)
	}
	f.s.mu.Lock()
	f.s.agents["agent"].Status.Runtime.Permissions = core.PermDefault
	f.s.mu.Unlock()
	if status, _ = f.s.Get(ctx, "agent"); status.PendingReason != "runtime changed" {
		t.Fatalf("permissions are launch-only: %q", status.PendingReason)
	}
}

type commandNotifier struct {
	fakeNotifier
	commands []string
	result   core.NotifyResult
}

func (c *commandNotifier) Command(_ context.Context, _ core.Agent, text string) (core.NotifyResult, error) {
	c.commands = append(c.commands, text)
	return c.result, nil
}

func TestSkillChangesHotReloadIdleClaude(t *testing.T) {
	dir := t.TempDir()
	cn := &commandNotifier{result: core.NotifyConfirmed}
	// barebones: only the agent's own skills count, never the real ~/.claude.
	f := setup(t, func(o *Options) {
		o.Agents[0].Dir = dir
		o.Agents[0].Runtime.Skills = core.SkillsBarebones
		o.Notifier = cn
	})
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.tick(t)
	if len(cn.commands) != 0 {
		t.Fatalf("reloaded with nothing changed: %v", cn.commands)
	}
	if e := os.MkdirAll(filepath.Join(dir, ".claude/skills/new"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, ".claude/skills/new/SKILL.md"), []byte("# new\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if status, _ := f.s.Get(ctx, "agent"); status.PendingRestart {
		t.Fatalf("skill change asked for a restart: %q", status.PendingReason)
	}
	f.c.advance(16 * time.Second)
	f.tick(t)
	if len(cn.commands) != 1 || cn.commands[0] != "/reload-skills" {
		t.Fatalf("want one /reload-skills, got %v", cn.commands)
	}
	f.c.advance(16 * time.Second)
	f.tick(t)
	if len(cn.commands) != 1 {
		t.Fatalf("reloaded twice for one change: %v", cn.commands)
	}
	// A busy agent is never typed into; the change waits for idle.
	if e := os.WriteFile(filepath.Join(dir, ".claude/skills/new/extra.md"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 2})
	f.c.advance(16 * time.Second)
	f.tick(t)
	if len(cn.commands) != 1 {
		t.Fatalf("typed into a busy agent: %v", cn.commands)
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 3})
	f.c.advance(16 * time.Second)
	f.tick(t)
	if len(cn.commands) != 2 || cn.commands[1] != "/reload-skills" {
		t.Fatalf("change not applied once idle: %v", cn.commands)
	}
}
