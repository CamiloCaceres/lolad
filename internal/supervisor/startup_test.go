package supervisor

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type dialogLauncher struct{ fakeLauncher }

func (*dialogLauncher) StartupDialog(pane string) (string, []string) {
	if pane == "trust" {
		return "workspace trust", []string{"Down", "Enter"}
	}
	return "", nil
}
func TestStartupDialogWatch(t *testing.T) {
	f := setup(t, func(o *Options) { o.Launchers[core.HarnessClaude] = &dialogLauncher{} })
	f.tick(t)
	f.sessions.pane = "trust"
	f.c.advance(2 * time.Second)
	f.tick(t)
	if strings.Join(f.sessions.keys, ",") != "Down,Enter" {
		t.Fatal(f.sessions.keys)
	}
	f.tick(t)
	if len(f.sessions.keys) != 2 {
		t.Fatal("repeated blind acceptance")
	}
	f.sessions.pane = "\n───\nChoose your model provider\nneeds key"
	f.c.advance(117 * time.Second)
	f.tick(t)
	if f.state(t).State != core.StateStarting {
		t.Fatal("early timeout")
	}
	f.c.advance(time.Second)
	f.tick(t)
	status := f.state(t)
	if status.State != core.StateWaiting || status.Detail != "startup blocked: Choose your model provider" {
		t.Fatal(status)
	}
	events, _ := f.s.Events(ctx, "agent", 100)
	dialogs := 0
	for _, e := range events {
		if e.Kind == "dialog" {
			dialogs++
		}
	}
	if dialogs != 2 {
		t.Fatal(events)
	}
	f.c.advance(20 * time.Minute)
	f.tick(t)
	if f.state(t).State != core.StateWaiting {
		t.Fatal("blocked detail lost to TTL")
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	if f.state(t).State != core.StateIdle || f.state(t).Detail != "" {
		t.Fatal("hook did not recover")
	}
}
func TestStartupReadyAndBusy(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.sessions.pane = "ready"
	f.tick(t)
	if f.state(t).State != core.StateIdle {
		t.Fatal("ready startup not idle")
	}
	f = setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "session_start", State: core.StateStarting, Seq: 1})
	f.c.advance(2 * time.Minute)
	f.sessions.pane = "Login required"
	f.tick(t)
	if f.state(t).State != core.StateWaiting {
		t.Fatal("session_start hid blocker")
	}
	f = setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", State: core.StateBusy, Seq: 1})
	f.c.advance(3 * time.Minute)
	f.tick(t)
	if f.state(t).State != core.StateBusy {
		t.Fatal("active turn misclassified")
	}
}

func TestAdoptionKeepsPersistedActiveTurn(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := setup(t, func(o *Options) { o.DB = db })
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", State: core.StateBusy, Seq: 1})
	adopted, err := New(f.o)
	if err != nil {
		t.Fatal(err)
	}
	f.s = adopted
	if f.state(t).State != core.StateBusy {
		t.Fatal("adoption lost persisted busy turn")
	}
	f.c.advance(4 * time.Minute)
	f.sessions.pane = "running a tool"
	f.tick(t)
	if f.state(t).State != core.StateBusy {
		t.Fatal("active turn treated as startup")
	}
}

func TestStartupWithoutSignalFallsBackToUnknown(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.sessions.pane = "Loading runtime..."
	f.c.advance(119 * time.Second)
	f.tick(t)
	if f.state(t).State != core.StateStarting {
		t.Fatal(f.state(t))
	}
	f.c.advance(time.Second)
	f.tick(t)
	if got := f.state(t); got.State != core.StateUnknown || !strings.Contains(got.Detail, "no hook signal") {
		t.Fatal(got)
	}
	f.c.advance(20 * time.Minute)
	f.tick(t)
	if !strings.Contains(f.state(t).Detail, "no hook signal") {
		t.Fatal("lost detail")
	}
	f.sessions.pane = "ready"
	f.tick(t)
	if f.state(t).State != core.StateIdle {
		t.Fatal(f.state(t))
	}
}

func TestReloadHonorsNewAgentAutostart(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			f := setup(t, func(o *Options) { o.Agents = nil })
			f.s.opts.ReloadAgents = func(context.Context) ([]core.Agent, error) {
				return []core.Agent{{Name: "new", Runtime: core.Runtime{Harness: core.HarnessClaude, Autostart: &enabled}}}, nil
			}
			if err := f.s.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := f.s.Get(ctx, "new")
			if err != nil {
				t.Fatal(err)
			}
			want := "down"
			if enabled {
				want = "up"
			}
			if got.Desired != want {
				t.Fatal(got)
			}
			f.tick(t)
			if (f.sessions.starts > 0) != enabled {
				t.Fatal("autostart ignored after reload")
			}
		})
	}
}

func TestAdoptionEmitsSystemSummary(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	s, err := New(f.o)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(ctx, "", 100)
	if len(events) != 1 || events[0].Agent != "" || events[0].Detail != "adopted 1 sessions" {
		t.Fatal(events)
	}
	events, _ = s.Events(ctx, "agent", 100)
	if len(events) != 0 {
		t.Fatal(events)
	}
}
