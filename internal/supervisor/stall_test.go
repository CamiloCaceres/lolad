package supervisor

import (
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The live incident (beta.5): idle agent, a notification hook a minute later,
// mail at 12:00 that the notifier refused because the screen did not read as
// ready, "unknown" from 12:04, and nothing more until a manual restart over
// an hour later.
func TestStalledMailRaisesProblemAndRecovers(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.c.advance(time.Minute)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "notification", Seq: 2})
	f.sessions.pane = "a screen that does not read as ready"
	f.n.result = core.NotifyBusy // what the notifier answers for such a screen
	f.c.advance(11 * time.Minute)
	f.m.add(f.c)
	f.tick(t)
	if f.n.calls != 1 || f.state(t).State != core.StateIdle {
		t.Fatalf("calls=%d state=%s", f.n.calls, f.state(t).State)
	}
	f.c.advance(4*time.Minute + time.Second) // hooks now over 15 minutes old: unsure
	f.tick(t)
	if f.state(t).State != core.StateUnknown {
		t.Fatal(f.state(t).State)
	}
	f.c.advance(6 * time.Minute) // the message has waited 10 minutes
	f.tick(t)
	a := f.state(t)
	if a.Problem == nil || a.Problem.Kind != unresponsive || a.Problem.Title != "Isn't responding to messages" || !a.Problem.NeedsYou || a.State != core.StateWaiting {
		t.Fatalf("no problem raised: %+v %s", a.Problem, a.State)
	}
	// It stays raised while the screen does not change.
	f.c.advance(time.Minute)
	f.tick(t)
	if f.state(t).Problem == nil {
		t.Fatal("problem dropped while the screen is the same")
	}
	// The prompt shows again: back to idle, and the notice goes out.
	f.sessions.pane = "ready"
	f.n.result = core.NotifyConfirmed
	f.c.advance(31 * time.Second)
	f.tick(t)
	if a = f.state(t); a.Problem != nil || f.m.marked != 1 || a.State != core.StateBusy {
		t.Fatalf("not recovered: problem=%+v marked=%d state=%s", a.Problem, f.m.marked, a.State)
	}
}

// An idle agent whose prompt shows but whose notices never get through (the
// notifier keeps answering busy) is retried once, then flagged.
func TestStalledMailRetriesThenFlags(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.sessions.pane = "ready"
	f.n.result = core.NotifyFailed
	f.m.add(f.c)
	for range 13 { // every scheduled attempt fails (0, 30s, 2m, 10m)
		f.tick(t)
		f.c.advance(time.Minute)
	}
	f.tick(t)
	if f.n.calls < 5 {
		t.Fatalf("no retry once the schedule gave up: %d calls", f.n.calls)
	}
	if f.state(t).Problem != nil {
		t.Fatal("flagged before the retry had a chance")
	}
	for range 12 {
		f.c.advance(time.Minute)
		f.tick(t)
	}
	if p := f.state(t).Problem; p == nil || p.Kind != unresponsive {
		t.Fatalf("not flagged after the retry failed: %+v", p)
	}
	// Mail that gets read clears the reason to wait; a hook moves it on.
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 2})
	if f.state(t).Problem != nil {
		t.Fatal("a new turn must clear the problem")
	}
}

// Announced mail the agent leaves unread is not a stall.
func TestAnnouncedMailIsNotAStall(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.sessions.pane = "ready"
	f.m.add(f.c)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 2})
	f.sessions.pane = "something else"
	f.c.advance(time.Hour)
	f.tick(t)
	if p := f.state(t).Problem; p != nil {
		t.Fatalf("flagged an agent that was told: %+v", p)
	}
}
