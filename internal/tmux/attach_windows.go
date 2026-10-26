package tmux

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// pollTerminal redraws complete ANSI snapshots; it does not pretend to be a PTY.
// Input is serialized so concurrent websocket writes cannot reorder keys.
type pollTerminal struct {
	sessions *Sessions
	name     string
	reader   *io.PipeReader
	writer   *io.PipeWriter
	cancel   context.CancelFunc
	done     chan struct{}
	readOnly bool
	mu       sync.Mutex
	pending  []byte
	once     sync.Once
}

func (s *Sessions) Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error) {
	if !s.Exists(name) {
		return nil, fmt.Errorf("session %s does not exist", name)
	}
	ctx, cancel := context.WithCancel(ctx)
	r, w := io.Pipe()
	t := &pollTerminal{sessions: s, name: name, reader: r, writer: w, cancel: cancel, done: make(chan struct{}), readOnly: readOnly}
	go func() {
		defer close(t.done)
		defer w.Close()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		previous := ""
		for {
			frame, err := s.Capture(name, 500, true)
			if err != nil {
				_ = w.CloseWithError(err)
				return
			}
			if frame != previous {
				if _, err = io.WriteString(w, "\x1b[H\x1b[2J"+strings.ReplaceAll(frame, "\n", "\r\n")); err != nil {
					return
				}
				previous = frame
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	// Unblock a pending pipe write if the websocket context disappears.
	go func() { <-ctx.Done(); _ = r.CloseWithError(ctx.Err()) }()
	return t, nil
}
func (t *pollTerminal) Read(p []byte) (int, error) { return t.reader.Read(p) }
func (t *pollTerminal) Write(p []byte) (int, error) {
	if t.readOnly {
		return 0, fmt.Errorf("terminal is read-only")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = append(t.pending, p...)
	keys := map[byte]string{13: "Enter", 10: "Enter", 27: "Escape", 127: "BSpace", 8: "BSpace", 3: "C-c", 9: "Tab"}
	for len(t.pending) > 0 {
		// Decode the common xterm named keys before interpreting a bare Escape.
		matched := false
		for seq, key := range map[string]string{"\x1b[A": "Up", "\x1b[B": "Down", "\x1b[C": "Right", "\x1b[D": "Left", "\x1b[H": "Home", "\x1b[F": "End", "\x1b[3~": "DC"} {
			if strings.HasPrefix(string(t.pending), seq) {
				if err := t.sessions.SendKeys(t.name, key); err != nil {
					return 0, err
				}
				t.pending = t.pending[len(seq):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		b := t.pending[0]
		if key, ok := keys[b]; ok {
			if err := t.sessions.SendKeys(t.name, key); err != nil {
				return 0, err
			}
			t.pending = t.pending[1:]
			continue
		}
		n := 0
		for n < len(t.pending) {
			if _, ok := keys[t.pending[n]]; ok {
				break
			}
			if !utf8.FullRune(t.pending[n:]) {
				break
			}
			_, size := utf8.DecodeRune(t.pending[n:])
			n += size
		}
		if n == 0 {
			break
		}
		if err := t.sessions.SendLiteral(t.name, string(t.pending[:n])); err != nil {
			return 0, err
		}
		t.pending = t.pending[n:]
	}
	return len(p), nil
}
func (t *pollTerminal) Resize(cols, rows uint16) error {
	if t.readOnly {
		return nil
	}
	_, err := t.sessions.run("resize-window", "-t", windowTarget(t.name), "-x", strconv.Itoa(int(cols)), "-y", strconv.Itoa(int(rows)))
	return err
}
func (t *pollTerminal) Close() error {
	t.once.Do(func() {
		t.cancel()
		_ = t.reader.Close()
		_ = t.writer.Close()
		<-t.done
		if !t.readOnly {
			t.sessions.restoreSize(t.name)
		}
	})
	return nil
}
