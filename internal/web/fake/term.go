package fake

import (
	"context"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

const (
	esc   = "\x1b["
	reset = esc + "0m"
	dim   = esc + "2m"
	bold  = esc + "1m"
	ok    = esc + "38;5;108m"
	info  = esc + "38;5;110m"
	warn  = esc + "38;5;179m"
	muted = esc + "38;5;245m"
)

// screen is the static part of a harness TUI for one agent.
func screen(a core.AgentStatus) []string {
	h := string(a.Runtime.Harness)
	model := a.Runtime.Model
	if model == "" {
		model = "default model"
	}
	return []string{
		fmt.Sprintf("%s✻%s %s%s%s  %s%s%s", warn, reset, bold, h, reset, dim, model, reset),
		fmt.Sprintf("  %s%s%s", dim, a.Dir, reset),
		fmt.Sprintf("%s%s%s", muted, strings.Repeat("─", 56), reset),
		"",
		fmt.Sprintf("%s>%s %slolad msg inbox%s", muted, reset, bold, reset),
		fmt.Sprintf("%s⏺%s Bash(lolad msg inbox)", ok, reset),
		fmt.Sprintf("  %s⎿  2 unread · newest: \"Ship pricing today?\" from milo%s", muted, reset),
		"",
	}
}

var script = [][]string{
	{ok + "⏺" + reset + " Read(internal/billing/invoice.go)", "  " + muted + "⎿  Read 214 lines" + reset},
	{ok + "⏺" + reset + " Bash(go test ./internal/billing/...)", "  " + muted + "⎿  ok   lumio/internal/billing  0.412s" + reset},
	{info + "⏺" + reset + " Thinking about the rounding rule for annual plans…"},
	{ok + "⏺" + reset + " Edit(internal/billing/invoice.go)", "  " + muted + "⎿  Updated with 3 additions and 1 removal" + reset},
	{ok + "⏺" + reset + " Bash(lolad msg send forge \"Invoice rounding\" \"Fixed; tests green.\")", "  " + muted + "⎿  sent msg_…a91f to forge" + reset},
	{warn + "⏺" + reset + " Grep(\"price_\" in web/)", "  " + muted + "⎿  Found 12 matches in 4 files" + reset},
	{ok + "⏺" + reset + " Write(drafts/changelog.md)", "  " + muted + "⎿  Wrote 18 lines" + reset},
}

// terminal streams a plausible harness session. Writes are echoed onto the
// prompt line when it is not read-only.
type terminal struct {
	pr       *io.PipeReader
	pw       *io.PipeWriter
	readOnly bool
	mu       sync.Mutex
	input    []byte
	sent     []string // submitted lines not yet echoed
	done     chan struct{}
	once     sync.Once
}

func newTerminal(ctx context.Context, a core.AgentStatus, readOnly bool) *terminal {
	pr, pw := io.Pipe()
	t := &terminal{pr: pr, pw: pw, readOnly: readOnly, done: make(chan struct{})}
	go t.play(ctx, a)
	return t
}

func (t *terminal) out(s string) bool {
	_, err := t.pw.Write([]byte(s))
	return err == nil
}

func (t *terminal) prompt() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return "\r" + esc + "2K" + muted + "> " + reset + string(t.input)
}

func (t *terminal) play(ctx context.Context, a core.AgentStatus) {
	defer t.pw.Close()
	if !t.out(esc + "2J" + esc + "H" + strings.Join(screen(a), "\r\n") + "\r\n") {
		return
	}
	spin := []string{"✻", "✽", "✢", "·"}
	i := 0
	tick := time.NewTicker(180 * time.Millisecond)
	defer tick.Stop()
	next := time.Now().Add(time.Second)
	since := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		case now := <-tick.C:
			if now.After(next) {
				block := script[i%len(script)]
				i++
				if !t.out("\r" + esc + "2K" + strings.Join(block, "\r\n") + "\r\n\r\n") {
					return
				}
				next = now.Add(time.Duration(1200+mrand.IntN(1800)) * time.Millisecond)
				since = now
			}
			secs := int(now.Sub(since).Seconds())
			line := fmt.Sprintf("\r%s2K%s%s%s %sWorking… (%ds · esc to interrupt)%s", esc, warn, spin[int(now.UnixMilli()/180)%len(spin)], reset, muted, secs, reset)
			if !t.readOnly {
				t.mu.Lock()
				sent := t.sent
				t.sent = nil
				t.mu.Unlock()
				for _, l := range sent {
					if !t.out("\r" + esc + "2K" + muted + "> " + reset + bold + l + reset + "\r\n  " + muted + "⎿  (typed by the operator)" + reset + "\r\n\r\n") {
						return
					}
				}
				line = t.prompt()
			}
			if !t.out(line) {
				return
			}
		}
	}
}

func (t *terminal) Read(p []byte) (int, error) { return t.pr.Read(p) }

func (t *terminal) Write(p []byte) (int, error) {
	if t.readOnly {
		return len(p), nil // tmux attach -r ignores input
	}
	t.mu.Lock()
	for _, b := range p {
		switch b {
		case '\r', '\n':
			t.sent = append(t.sent, string(t.input))
			t.input = t.input[:0]
		case 0x7f, 0x08:
			if len(t.input) > 0 {
				t.input = t.input[:len(t.input)-1]
			}
		case 0x1b:
			t.input = t.input[:0]
		default:
			if b >= 0x20 {
				t.input = append(t.input, b)
			}
		}
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *terminal) Resize(cols, rows uint16) error { return nil }

func (t *terminal) Close() error {
	t.once.Do(func() { close(t.done) })
	return t.pr.Close()
}
