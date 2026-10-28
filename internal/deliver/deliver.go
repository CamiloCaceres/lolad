// Package deliver submits one-line mail notices and checks pane evidence.
package deliver

import (
	"context"
	"fmt"
	"regexp"
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
	external := ""
	if m.Remote != "" {
		external = "external "
	}
	files := ""
	switch n := len(m.Attachments); {
	case n == 1:
		files = " (1 file attached)"
	case n > 1:
		files = fmt.Sprintf(" (%d files attached)", n)
	}
	return fmt.Sprintf("%s [MESSAGE] %sfrom %s: %s — %s%s. Read: lolad msg read %s", Ref(m.ID), external, oneLine(m.FromName), oneLine(m.Subject), string(body), files, oneLine(m.ID))
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

// ready needs the last hook-reported state to be idle and the pane to show the
// input box now. Idle never expires: any later turn reports busy first, and the
// pane check rejects spinners and permission prompts.
func (n *Notifier) ready(a core.Agent, pane string) bool {
	if n.state == nil {
		return false
	}
	s, at := n.state(a.Name)
	return s == core.StateIdle && !at.IsZero() && n.launcher.PromptReady(pane)
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

// dimmed matches SGR-dim spans: Claude Code draws its placeholder and
// suggested prompts dim; typed text never is.
var dimmed = regexp.MustCompile(`\x1b\[2m[^\x1b]*(\x1b\[(0|22)?m)?`)

// inputEmpty reports whether the composer row holds nothing typed: only the
// prompt glyph, or a dimmed placeholder/suggestion (the "Try …" text is also
// recognised in captures without escapes).
func inputEmpty(pane string) bool {
	i := harness.InputLine(pane)
	if i < 0 {
		return false
	}
	v := strings.TrimSpace(harness.Plain(dimmed.ReplaceAllString(strings.Split(pane, "\n")[i], "")))
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "│"), "❯"))
	return v == "" || strings.HasPrefix(v, `Try "`)
}

// Command types a harness-local slash command (one that starts no model turn,
// such as /reload-skills) into an idle session whose input box is empty. It is
// confirmed once the command has left the input box; text that stays staged
// is removed again with BSpace.
func (n *Notifier) Command(ctx context.Context, a core.Agent, text string) (core.NotifyResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return core.NotifyFailed, e
	}
	pane, e := n.sessions.Capture(a.Name, 40, true)
	if e != nil {
		return core.NotifyFailed, e
	}
	if !n.ready(a, pane) || !inputEmpty(pane) {
		return core.NotifyBusy, nil
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
	for poll := 0; poll < 10; poll++ {
		if e := pause(ctx, 50*time.Millisecond); e != nil {
			return core.NotifyFailed, e
		}
		if pane, e = n.sessions.Capture(a.Name, 40, true); e != nil {
			return core.NotifyFailed, e
		}
		if classify(pane, text) != core.NotifyStaged {
			return core.NotifyConfirmed, nil
		}
	}
	keys := make([]string, utf8.RuneCountInString(text))
	for i := range keys {
		keys[i] = "BSpace"
	}
	if e := n.sessions.SendKeys(a.Name, keys...); e != nil {
		return core.NotifyFailed, e
	}
	return core.NotifyStaged, nil
}
