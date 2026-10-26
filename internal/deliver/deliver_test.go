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
	}{{core.StateBusy, time.Now()}, {core.StateWaiting, time.Now()}, {core.StateIdle, time.Now().Add(-16 * time.Minute)}, {core.StateIdle, time.Time{}}} {
		n := New(nil, l, func(string) (core.State, time.Time) { return tc.state, tc.at })
		if n.ready(core.Agent{Name: "a"}, pane) {
			t.Fatal("unsafe state accepted", tc)
		}
	}
	n := New(nil, l, func(string) (core.State, time.Time) { return core.StateIdle, time.Now().Add(-time.Second) })
	if n.ready(core.Agent{Name: "a"}, pane+"\nEnter to confirm") {
		t.Fatal("permission pane accepted")
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
