package deliver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Tracker wraps the daemon hook sink to retain independent hook-only state.
// Its State method can safely be called while the supervisor holds its lock.
// Set Sink during wiring, before serving hooks or starting the supervisor.
type Tracker struct {
	Sink   core.HookSink
	mu     sync.RWMutex
	events map[string]core.HookEvent
}

func NewTracker(sink core.HookSink) *Tracker {
	return &Tracker{Sink: sink, events: map[string]core.HookEvent{}}
}
func (t *Tracker) HandleHook(ctx context.Context, ev core.HookEvent) core.HookReply {
	var reply core.HookReply
	if t.Sink != nil {
		reply = t.Sink.HandleHook(ctx, ev)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old, ok := t.events[ev.Agent]
	if ok && ev.SessionID != "" && old.SessionID != "" && ev.SessionID != old.SessionID && ev.Event != "session_start" {
		return reply
	}
	newSession := ev.Event == "session_start" && ev.SessionID != "" && ev.SessionID != old.SessionID
	if !ok || newSession || ev.Seq > old.Seq {
		if ev.State == "" {
			ev.State = old.State
			ev.At = old.At
		}
		if ev.SessionID == "" {
			ev.SessionID = old.SessionID
		}
		t.events[ev.Agent] = ev
	}
	return reply
}

// Seed restores a hook state persisted before a daemon restart. It never
// replaces a hook already received; any later hook replaces the seed.
func (t *Tracker) Seed(ev core.HookEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.events[ev.Agent]; !ok {
		t.events[ev.Agent] = ev
	}
}
func (t *Tracker) State(name string) (core.State, time.Time) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ev := t.events[name]
	return ev.State, ev.At
}

// Router chooses the notifier matching the recipient's harness.
type Router struct{ notifiers map[core.Harness]*Notifier }

func NewRouter(sessions core.Sessions, launchers map[core.Harness]core.Launcher, state StateFunc) *Router {
	r := &Router{notifiers: map[core.Harness]*Notifier{}}
	for kind, l := range launchers {
		if l == nil {
			continue
		}
		r.notifiers[kind] = New(sessions, l, state)
	}
	return r
}
func (r *Router) Notify(ctx context.Context, a core.Agent, m core.Mail) (core.NotifyResult, error) {
	n := r.notifiers[a.Runtime.Harness]
	if n == nil {
		return core.NotifyFailed, fmt.Errorf("%w: harness %s", core.ErrInvalid, a.Runtime.Harness)
	}
	return n.Notify(ctx, a, m)
}

// Command routes a slash command to the recipient's harness notifier.
func (r *Router) Command(ctx context.Context, a core.Agent, text string) (core.NotifyResult, error) {
	n := r.notifiers[a.Runtime.Harness]
	if n == nil {
		return core.NotifyFailed, fmt.Errorf("%w: harness %s", core.ErrInvalid, a.Runtime.Harness)
	}
	return n.Command(ctx, a, text)
}

var _ core.HookSink = (*Tracker)(nil)
var _ core.Notifier = (*Router)(nil)
