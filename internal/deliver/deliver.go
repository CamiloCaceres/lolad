// Package deliver submits one-line mail notices and checks pane evidence.
package deliver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/harness"
)

type StateFunc func(string) (core.State, time.Time)
type attempt struct {
	at     time.Time
	result core.NotifyResult
}
type Notifier struct {
	sessions core.Sessions
	launcher core.Launcher
	state    StateFunc
	mu       sync.Mutex
	recent   map[string]attempt
}

var _ core.Notifier = (*Notifier)(nil)

func New(s core.Sessions, l core.Launcher, state StateFunc) *Notifier {
	return &Notifier{sessions: s, launcher: l, state: state, recent: map[string]attempt{}}
}
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
}
func Ref(id string) string {
	r := []rune(id)
	if len(r) > 8 {
		r = r[len(r)-8:]
	}
	return "[#" + string(r) + "]"
}
func Notice(m core.Mail) string {
	body := []rune(oneLine(m.Body))
	if len(body) > 300 {
		body = body[:300]
	}
	return fmt.Sprintf("%s [MESSAGE] from %s: %s — %s. Read: lolad msg read %s", Ref(m.ID), oneLine(m.FromName), oneLine(m.Subject), string(body), oneLine(m.ID))
}
func classify(pane, ref string) core.NotifyResult {
	p := harness.Plain(pane)
	lines := strings.Split(p, "\n")
	i := harness.InputLine(p)
	if i < 0 {
		return core.NotifyFailed
	}
	if strings.Contains(strings.Join(lines[i:], "\n"), ref) {
		return core.NotifyStaged
	}
	if strings.Contains(strings.Join(lines[:i], "\n"), ref) {
		return core.NotifyConfirmed
	}
	return core.NotifyFailed
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (n *Notifier) ready(a core.Agent, pane string) bool {
	if n.state == nil {
		return false
	}
	s, at := n.state(a.Name)
	age := time.Since(at)
	return s == core.StateIdle && !at.IsZero() && age >= 0 && age < 15*time.Minute && n.launcher.PromptReady(pane)
}
func (n *Notifier) Notify(ctx context.Context, a core.Agent, m core.Mail) (result core.NotifyResult, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return core.NotifyFailed, e
	}
	key := a.Name + "\x00" + m.ID
	now := time.Now()
	for k, v := range n.recent {
		if now.Sub(v.at) >= 10*time.Second {
			delete(n.recent, k)
		}
	}
	if v, ok := n.recent[key]; ok {
		return v.result, nil
	}
	pane, e := n.sessions.Capture(a.Name, 40, true)
	if e != nil {
		return core.NotifyFailed, e
	}
	if !n.ready(a, pane) {
		return core.NotifyBusy, nil
	}
	text := Notice(m)
	result = core.NotifyFailed
	defer func() { n.recent[key] = attempt{now, result} }()
	for retry := 0; retry < 2; retry++ {
		if e := ctx.Err(); e != nil {
			return core.NotifyFailed, e
		}
		if e := n.sessions.SendLiteral(a.Name, text); e != nil {
			return core.NotifyFailed, e
		}
		if e := pause(ctx, 150*time.Millisecond); e != nil {
			return core.NotifyFailed, e
		}
		if e := n.sessions.SendKeys(a.Name, "Enter"); e != nil {
			return core.NotifyFailed, e
		}
		// Rendering is asynchronous; wait briefly for a submitted echo to replace staging.
		for poll := 0; poll < 5; poll++ {
			if e := pause(ctx, 50*time.Millisecond); e != nil {
				return core.NotifyFailed, e
			}
			pane, e = n.sessions.Capture(a.Name, 40, true)
			if e != nil {
				return core.NotifyFailed, e
			}
			result = classify(pane, Ref(m.ID))
			if result == core.NotifyConfirmed {
				return result, nil
			}
		}
		if result != core.NotifyStaged {
			return result, nil
		}
		// Clear only known staged text; never send a shell editing command like C-u.
		if !n.ready(a, pane) {
			return result, nil
		}
		keys := make([]string, utf8.RuneCountInString(text))
		for i := range keys {
			keys[i] = "BSpace"
		}
		if e := n.sessions.SendKeys(a.Name, keys...); e != nil {
			return core.NotifyFailed, e
		}
		if e := pause(ctx, 50*time.Millisecond); e != nil {
			return core.NotifyFailed, e
		}
		pane, e = n.sessions.Capture(a.Name, 40, true)
		if e != nil {
			return core.NotifyFailed, e
		}
		if retry == 1 {
			return core.NotifyStaged, nil
		}
		if !n.ready(a, pane) || strings.Contains(harness.Plain(pane), Ref(m.ID)) {
			return core.NotifyStaged, nil
		}
	}
	return result, nil
}
