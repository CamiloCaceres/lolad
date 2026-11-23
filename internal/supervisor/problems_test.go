package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestClassifyFailure(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		harness  core.Harness
		kind     string
		msg      string
		want     string
		needsYou bool
		retry    bool
	}{
		{core.HarnessClaude, "authentication_failed", "OAuth token has expired · Please run /login", "login", true, false},
		{core.HarnessClaude, "billing_error", "Credit balance is too low", "billing", true, false},
		{core.HarnessClaude, "account_on_hold", "", "account", true, false},
		{core.HarnessClaude, "model_not_found", "", "model", true, false},
		{core.HarnessClaude, "rate_limit", "You've hit your limit · resets 3pm (UTC)", "usage_limit", false, true},
		{core.HarnessClaude, "rate_limit", "API Error: 429 rate_limit_error", "rate_limit", false, true},
		{core.HarnessClaude, "overloaded", "API Error: 529 Overloaded", "overloaded", false, true},
		{core.HarnessClaude, "server_error", "API Error: 500", "overloaded", false, true},
		{core.HarnessClaude, "unknown", "API Error: Connection error.", "network", false, true},
		{core.HarnessClaude, "unknown", "something odd", "error", false, true},
		{core.HarnessCodewhale, "", "HTTP 401 Unauthorized: invalid api key", "login", true, false},
		{core.HarnessCodewhale, "", "402 Insufficient Balance", "billing", true, false},
		{core.HarnessCodewhale, "", "503 service unavailable", "overloaded", false, true},
	} {
		p, ok := classifyFailure(tc.harness, tc.kind, tc.msg, now)
		if !ok || p.Kind != tc.want || p.NeedsYou != tc.needsYou || p.Title == "" || p.Hint == "" || p.Until.IsZero() == tc.retry {
			t.Errorf("%s %q %q: %+v ok=%v", tc.harness, tc.kind, tc.msg, p, ok)
		}
	}
	if _, ok := classifyFailure(core.HarnessClaude, "max_output_tokens", "", now); ok {
		t.Error("a reply cut short is not a problem")
	}
	p, _ := classifyFailure(core.HarnessClaude, "authentication_failed", "", now)
	if strings.Contains(p.Title+p.Hint, "harness") || strings.Contains(p.Title+p.Hint, "API") {
		t.Errorf("jargon in %+v", p)
	}
}

func TestResetTime(t *testing.T) {
	bogota, _ := time.LoadLocation("America/Bogota")
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC) // 05:00 in Bogota
	for msg, want := range map[string]time.Time{
		"You've hit your limit · resets 3pm (America/Bogota)": time.Date(2026, 11, 23, 15, 0, 0, 0, bogota),
		"5-hour limit reached ∙ resets 9:30am":                time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC), // 9:30 UTC passed: tomorrow
		"5-hour limit reached ∙ resets 11am":                  time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC),
		"Weekly limit reached · resets Nov 25, 2pm":           time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC),
		"Claude AI usage limit reached|1795432800":            time.Unix(1795432800, 0),
	} {
		got, ok := resetTime(msg, now)
		if !ok || !got.Equal(want) {
			t.Errorf("%q: %v %v, want %v", msg, got, ok, want)
		}
	}
	for _, msg := range []string{"no time here", "resets 13pm", "Claude AI usage limit reached|1000000000"} {
		if _, ok := resetTime(msg, now); ok {
			t.Errorf("%q parsed", msg)
		}
	}
}

// An API error that ends a turn holds mail, explains itself, and clears when
// the agent works again; a usage limit retries by itself at the reset.
func TestStopFailurePausesAndRetries(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 2})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop_failure", Error: "rate_limit", Detail: "You've hit your limit · resets 11am (UTC)", Seq: 3})
	a := f.state(t)
	if a.State != core.StatePaused || a.Problem == nil || a.Problem.Kind != "usage_limit" || a.Problem.Until.IsZero() {
		t.Fatalf("%+v %+v", a, a.Problem)
	}
	// Mail waits: no notice is typed and no attempt is spent.
	f.m.add(f.c)
	f.sessions.pane = "ready"
	f.tick(t)
	if f.n.calls != 0 {
		t.Fatal("typed into a paused agent")
	}
	// A late idle notification from the failed turn keeps the problem.
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 4})
	if f.state(t).State != core.StatePaused {
		t.Fatal("stop after the failure cleared the pause")
	}
	// The limit resets at 11:00 (the fixture starts at 08:00 UTC).
	f.c.advance(3*time.Hour + 2*time.Minute)
	f.tick(t)
	if a = f.state(t); a.Problem != nil || a.State == core.StatePaused {
		t.Fatalf("not retried after the reset: %+v", a)
	}
	f.tick(t)
	if f.n.calls != 1 {
		t.Fatalf("mail not delivered after the reset: %d", f.n.calls)
	}
}

func TestLoginProblemNeedsYouUntilWorkResumes(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop_failure", Error: "authentication_failed", Detail: "Please run /login", Seq: 1})
	a := f.state(t)
	if a.State != core.StateWaiting || a.Problem == nil || !a.Problem.NeedsYou || a.Detail != a.Problem.Title {
		t.Fatalf("%+v", a)
	}
	f.c.advance(24 * time.Hour)
	f.tick(t)
	if f.state(t).Problem == nil {
		t.Fatal("a login problem must not clear by itself")
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 2})
	if a = f.state(t); a.Problem != nil || a.State != core.StateBusy {
		t.Fatalf("not cleared by new work: %+v", a)
	}
}

func TestRestartClearsProblem(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop_failure", Error: "billing_error", Seq: 1})
	f.s.Restart(ctx, "agent", false)
	f.tick(t)
	f.c.advance(11 * time.Second) // the graceful stop's grace period
	for i := 0; i < 3; i++ {
		f.tick(t)
	}
	if a := f.state(t); a.Problem != nil {
		t.Fatalf("restart kept the problem: %+v", a)
	}
}

func TestStuckTurnFlaggedAndCleared(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "pre_tool", Seq: 1})
	f.sessions.pane = "busy"
	f.c.advance(30 * time.Minute)
	f.tick(t)
	if f.state(t).Problem != nil {
		t.Fatal("flagged too early")
	}
	f.c.advance(16 * time.Minute)
	f.tick(t)
	a := f.state(t)
	if a.Problem == nil || a.Problem.Kind != "stuck" || a.State != core.StateBusy || a.Problem.NeedsYou {
		t.Fatalf("%+v", a)
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "post_tool", Seq: 2})
	if f.state(t).Problem != nil {
		t.Fatal("progress did not clear stuck")
	}
}

func TestStartupDialogExplained(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.sessions.pane = "╭──╮\n│ Claude Code │\n Select login method:\n ❯ 1. Claude account\n"
	f.c.advance(3 * time.Minute)
	f.tick(t)
	a := f.state(t)
	if a.State != core.StateWaiting || a.Problem == nil || a.Problem.Kind != "login" || !strings.Contains(a.Detail, "Select login method") {
		t.Fatalf("%+v %+v", a, a.Problem)
	}
	f.sessions.pane = "ready"
	f.tick(t)
	if a = f.state(t); a.Problem != nil || a.State != core.StateIdle {
		t.Fatalf("not cleared once the prompt showed: %+v", a)
	}
}

func TestCrashKeepsLastWords(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.sessions.pane = "some output\nError: Invalid API key\n\nPane is dead (status 1)\n"
	f.sessions.dead = true
	f.tick(t)
	if a := f.state(t); !strings.Contains(a.LastError, "Invalid API key") {
		t.Fatalf("%q", a.LastError)
	}
}

// Mail whose every notice failed is announced again later instead of never.
func TestGivenUpMailRetriedLater(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Harness = core.HarnessCodewhale })
	f.n.result = core.NotifyFailed
	f.tick(t)
	f.m.add(f.c)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	for _, d := range []time.Duration{0, 30 * time.Second, 90 * time.Second, 8 * time.Minute} {
		f.c.advance(d)
		f.tick(t)
	}
	if f.n.calls != 4 {
		t.Fatal(f.n.calls)
	}
	f.sessions.pane = "ready" // still idle after half an hour
	f.c.advance(31 * time.Minute)
	f.s.sweep(ctx)
	f.tick(t)
	if f.n.calls != 5 {
		t.Fatalf("given-up mail not retried: %d", f.n.calls)
	}
}
