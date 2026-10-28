package deliver

import (
	"context"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/harness"
	tm "github.com/CamiloCaceres/lolad/internal/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealDelivery(t *testing.T) {
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip(e)
	}
	if _, e = exec.LookPath("tmux"); e != nil {
		t.Skip(e)
	}
	script, _ := filepath.Abs("testdata/tui.py")
	for _, mode := range []string{"confirmed", "retry", "staged", "failed"} {
		t.Run(mode, func(t *testing.T) {
			s, e := tm.New(t.TempDir(), fmt.Sprintf("lolad-test-%d", time.Now().UnixNano()))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { exec.Command("tmux", "-L", s.Socket, "kill-server").Run() })
			if e := s.Start("a", t.TempDir(), os.Environ(), []string{python, script, mode}, 220, 50); e != nil {
				t.Fatal(e)
			}
			l := harness.New(core.HarnessClaude, "")
			deadline := time.Now().Add(3 * time.Second)
			for {
				p, _ := s.Capture("a", 40, true)
				if l.PromptReady(p) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no composer", p)
				}
				time.Sleep(20 * time.Millisecond)
			}
			n := New(s, l, func(string) (core.State, time.Time) { return core.StateIdle, time.Now().Add(-time.Second) })
			m := core.Mail{ID: "msg_12345678", FromName: "milo", Subject: "Hi", Body: "hello"}
			want := core.NotifyConfirmed
			if mode == "staged" {
				want = core.NotifyStaged
			}
			if mode == "failed" {
				want = core.NotifyFailed
			}
			result, e := n.Notify(context.Background(), core.Agent{Name: "a"}, m)
			if e != nil || result != want {
				p, _ := s.Capture("a", 40, false)
				t.Fatalf("got %s want %s err %v pane %q", result, want, e, p)
			}
			p, _ := s.Capture("a", 40, false)
			result, e = n.Notify(context.Background(), core.Agent{Name: "a"}, m)
			q, _ := s.Capture("a", 40, false)
			if e != nil || result != want || p != q {
				t.Fatalf("dedupe %s %v", result, e)
			}
		})
	}
}
func TestNoticeCountsFiles(t *testing.T) {
	m := core.Mail{ID: "msg_1_abcdef12", FromName: "milo", Subject: "Assets", Body: "here", Attachments: []core.Attachment{{ID: "att_1"}, {ID: "att_2"}}}
	if n := Notice(m); !strings.Contains(n, "here (2 files attached). Read: lolad msg read msg_1_abcdef12") {
		t.Fatal(n)
	}
}
func TestNotice(t *testing.T) {
	m := core.Mail{ID: "msg_12345678", FromName: "a\nb", Subject: "s\x1b\n", Body: strings.Repeat("世", 301) + "\n"}
	n := Notice(m)
	if strings.ContainsAny(n, "\n\x1b") || strings.Count(n, "世") != 300 || !strings.HasPrefix(n, "[#12345678] [MESSAGE]") {
		t.Fatal(n)
	}
}
func TestClassification(t *testing.T) {
	for _, tc := range []struct {
		pane string
		want core.NotifyResult
	}{{"[#12345678]\n──\n❯ \n──", core.NotifyConfirmed}, {"──\n❯ [#12345678]\n──", core.NotifyStaged}, {"──\n❯ \n──", core.NotifyFailed}} {
		if got := classify(tc.pane, "[#12345678]"); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}

func TestReadinessGuards(t *testing.T) {
	l := harness.New(core.HarnessClaude, "")
	pane := "──\n❯ \n──"
	for _, tc := range []struct {
		state core.State
		at    time.Time
	}{{core.StateBusy, time.Now()}, {core.StateWaiting, time.Now()}, {core.StateIdle, time.Time{}}} {
		n := New(nil, l, func(string) (core.State, time.Time) { return tc.state, tc.at })
		if n.ready(core.Agent{Name: "a"}, pane) {
			t.Fatal("unsafe state accepted", tc)
		}
	}
	n := New(nil, l, func(string) (core.State, time.Time) { return core.StateIdle, time.Now().Add(-time.Second) })
	if n.ready(core.Agent{Name: "a"}, pane+"\nEnter to confirm") {
		t.Fatal("permission pane accepted")
	}
	// An agent idle for hours must still wake on mail; the pane is the guard.
	n = New(nil, l, func(string) (core.State, time.Time) { return core.StateIdle, time.Now().Add(-3 * time.Hour) })
	if !n.ready(core.Agent{Name: "a"}, pane) {
		t.Fatal("long-idle agent not woken")
	}
	if n.ready(core.Agent{Name: "a"}, "✻ Working… (esc to interrupt)\n"+pane) {
		t.Fatal("busy pane accepted")
	}
}

func TestTrackerSequence(t *testing.T) {
	tracker := NewTracker(nil)
	now := time.Now()
	tracker.HandleHook(context.Background(), core.HookEvent{Agent: "a", Seq: 2, State: core.StateIdle, At: now})
	tracker.HandleHook(context.Background(), core.HookEvent{Agent: "a", Seq: 1, State: core.StateBusy, At: now.Add(-time.Second)})
	state, at := tracker.State("a")
	if state != core.StateIdle || !at.Equal(now) {
		t.Fatal(state, at)
	}
}

func TestTrackerSeed(t *testing.T) {
	tr := NewTracker(nil)
	now := time.Now()
	tr.Seed(core.HookEvent{Agent: "a", SessionID: "s", Seq: 5, State: core.StateIdle, At: now.Add(-time.Hour)})
	if state, at := tr.State("a"); state != core.StateIdle || !at.Equal(now.Add(-time.Hour)) {
		t.Fatal("seed lost", state, at)
	}
	tr.HandleHook(context.Background(), core.HookEvent{Agent: "a", SessionID: "s", Seq: 4, State: core.StateBusy, At: now})
	if state, _ := tr.State("a"); state != core.StateIdle {
		t.Fatal("older hook replaced seed")
	}
	tr.HandleHook(context.Background(), core.HookEvent{Agent: "a", SessionID: "s", Seq: 6, State: core.StateBusy, At: now})
	tr.Seed(core.HookEvent{Agent: "a", Seq: 1, State: core.StateIdle, At: now})
	if state, _ := tr.State("a"); state != core.StateBusy {
		t.Fatal("seed replaced a live hook")
	}
}

func TestTrackerSessionBoundary(t *testing.T) {
	tr := NewTracker(nil)
	now := time.Now()
	for _, e := range []core.HookEvent{{Agent: "a", Event: "session_start", SessionID: "old", Seq: 100, State: core.StateIdle, At: now}, {Agent: "a", Event: "session_start", SessionID: "new", Seq: 1, State: core.StateBusy, At: now}, {Agent: "a", Event: "stop", SessionID: "old", Seq: 200, State: core.StateIdle, At: now}} {
		tr.HandleHook(context.Background(), e)
	}
	s, _ := tr.State("a")
	if s != core.StateBusy {
		t.Fatal(s)
	}
}

func TestExternalNotice(t *testing.T) {
	m := core.Mail{ID: "msg_12345678", FromName: "jugui@jugui.lolad.local", Remote: "jugui.lolad.local", Subject: "Review", Body: "hello"}
	if text := Notice(m); !strings.Contains(text, "[MESSAGE] external from jugui@jugui.lolad.local:") {
		t.Fatal(text)
	}
	m.Remote = ""
	if text := Notice(m); !strings.Contains(text, "[MESSAGE] from jugui@jugui.lolad.local:") || strings.Contains(text, "external") {
		t.Fatal(text)
	}
}

// composer is a fake claude pane: typed text sits in the input row until
// Enter submits it, unless stuck.
type composer struct {
	core.Sessions
	input, history string
	stuck          bool
	keys           []string
}

func (c *composer) Capture(string, int, bool) (string, error) {
	return c.history + "──\n❯ " + c.input + "\n──", nil
}
func (c *composer) SendLiteral(_, text string) error { c.input += text; return nil }
func (c *composer) SendKeys(_ string, keys ...string) error {
	for _, k := range keys {
		c.keys = append(c.keys, k)
		switch k {
		case "Enter":
			if !c.stuck {
				c.history += "❯ " + c.input + "\n  ⎿  Reloaded skills\n"
				c.input = ""
			}
		case "BSpace":
			if r := []rune(c.input); len(r) > 0 {
				c.input = string(r[:len(r)-1])
			}
		}
	}
	return nil
}

func TestCommand(t *testing.T) {
	l := harness.New(core.HarnessClaude, "")
	idle := func(string) (core.State, time.Time) { return core.StateIdle, time.Now().Add(-time.Hour) }
	ctx := context.Background()
	c := &composer{history: "❯ /reload-skills\n  ⎿  Reloaded skills\n"}
	if r, e := New(c, l, idle).Command(ctx, core.Agent{Name: "a"}, "/reload-skills"); e != nil || r != core.NotifyConfirmed || c.input != "" {
		t.Fatalf("submit: %s %v %q", r, e, c.input)
	}
	c = &composer{input: "half-typed by the user"}
	if r, _ := New(c, l, idle).Command(ctx, core.Agent{Name: "a"}, "/reload-skills"); r != core.NotifyBusy || len(c.keys) != 0 {
		t.Fatalf("typed over a draft: %s %v", r, c.keys)
	}
	c = &composer{stuck: true}
	if r, _ := New(c, l, idle).Command(ctx, core.Agent{Name: "a"}, "/reload-skills"); r != core.NotifyStaged || c.input != "" {
		t.Fatalf("stuck command left in input: %s %q", r, c.input)
	}
	c = &composer{input: `Try "edit <filepath> to..."`}
	busy := func(string) (core.State, time.Time) { return core.StateBusy, time.Now() }
	if r, _ := New(c, l, busy).Command(ctx, core.Agent{Name: "a"}, "/reload-skills"); r != core.NotifyBusy {
		t.Fatalf("busy agent typed into: %s", r)
	}
	if !inputEmpty("──\n❯ " + `Try "edit <filepath> to..."` + "\n──") {
		t.Fatal("placeholder is an empty composer")
	}
	// Captured from a live agent: a dimmed suggested prompt after the glyph.
	if !inputEmpty("──\n\x1b[39m❯\u00a0\x1b[2mCheck lolad msg inbox for new mail\x1b[0m\n──") {
		t.Fatal("dimmed suggestion is an empty composer")
	}
	if inputEmpty("──\n\x1b[39m❯\u00a0check my inbox\x1b[2m please\x1b[0m\n──") {
		t.Fatal("typed text before a dimmed hint is not empty")
	}
}
