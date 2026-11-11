package main

import (
	"fmt"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

// Note is one native notification, before rate limiting.
type Note struct {
	Kind  string
	ID    string // stable per cause, so a repeat replaces instead of stacking
	Title string
	Body  string
	Link  string // dashboard path opened on click, e.g. "/inbox/iris"
}

// detector turns successive Data reads into notifications. The first read only
// seeds what already exists, so launching the app never replays old items.
type detector struct {
	seeded  bool
	asks    map[string]bool // last-message id of asks already announced
	failed  map[string]bool
	pending map[string]int
}

func newDetector() *detector {
	return &detector{asks: map[string]bool{}, failed: map[string]bool{}, pending: map[string]int{}}
}

func (d *detector) Observe(data Data) []Note {
	var out []Note
	add := func(n Note) {
		if d.seeded {
			out = append(out, n)
		}
	}
	for _, c := range data.Convs {
		if !c.IsAsk() {
			continue
		}
		if d.asks[c.Last.ID] {
			continue
		}
		d.asks[c.Last.ID] = true
		who := firstNonEmpty(c.Display, c.Peer)
		add(Note{Kind: kindAsk, ID: "ask-" + c.Last.ID, Title: who + " asks", Body: clip(firstNonEmpty(c.Last.Subject, c.Last.Body), 140),
			Link: "/inbox/" + neturl.PathEscape(c.Peer)})
	}
	nowFailed := map[string]bool{}
	for _, a := range data.Agents {
		if a.State != "failed" {
			continue
		}
		nowFailed[a.Name] = true
		if !d.failed[a.Name] {
			add(Note{Kind: kindFailed, ID: "failed-" + a.Name, Title: firstNonEmpty(a.Display, a.Name) + " failed",
				Body: "The agent stopped and could not restart.", Link: "/agents/" + neturl.PathEscape(a.Name)})
		}
	}
	d.failed = nowFailed // leaving failed and failing again notifies again
	for _, c := range data.Conns {
		if c.Pending > d.pending[c.Provider] {
			add(Note{Kind: kindFederation, ID: "fed-" + c.Provider, Title: "A sender needs approval",
				Body: fmt.Sprintf("%s: %s waiting for you.", firstNonEmpty(c.Display, c.Provider), plural(c.Pending, "sender", "senders")),
				Link: "/connections"})
		}
		d.pending[c.Provider] = c.Pending
	}
	d.seeded = true
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// batcher collapses bursts: notes arriving within window are delivered
// together, as themselves when there is one and as a single summary when there
// are several. At most maxPerHour deliveries go out; the rest are dropped,
// because the tray icon and inbox still show them.
type batcher struct {
	window     time.Duration
	maxPerHour int
	emit       func(Note)
	now        func() time.Time

	mu    sync.Mutex
	buf   []Note
	timer *time.Timer
	sent  []time.Time
}

func newBatcher(window time.Duration, maxPerHour int, emit func(Note)) *batcher {
	return &batcher{window: window, maxPerHour: maxPerHour, emit: emit, now: time.Now}
}

func (b *batcher) Add(n Note) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, n)
	if b.timer == nil {
		b.timer = time.AfterFunc(b.window, b.flush)
	}
}

func (b *batcher) flush() {
	b.mu.Lock()
	buf := b.buf
	b.buf, b.timer = nil, nil
	cut := b.now().Add(-time.Hour)
	keep := b.sent[:0]
	for _, t := range b.sent {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	b.sent = keep
	ok := len(b.sent) < b.maxPerHour
	if ok {
		b.sent = append(b.sent, b.now())
	}
	b.mu.Unlock()
	if !ok || len(buf) == 0 {
		return
	}
	b.emit(collapse(buf))
}

// collapse returns the single note, or one summary for the burst.
func collapse(buf []Note) Note {
	if len(buf) == 1 {
		return buf[0]
	}
	kind := buf[0].Kind
	for _, n := range buf {
		if n.Kind != kind {
			return Note{Kind: "mixed", ID: "burst", Title: fmt.Sprintf("%d things need you", len(buf)), Body: "Open Lolad to see them.", Link: "/"}
		}
	}
	switch kind {
	case kindAsk:
		return Note{Kind: kind, ID: "burst-ask", Title: plural(len(buf), "question", "questions") + " waiting", Body: buf[0].Title + " and others", Link: "/"}
	case kindFailed:
		return Note{Kind: kind, ID: "burst-failed", Title: plural(len(buf), "agent", "agents") + " failed", Body: buf[0].Title + " and others", Link: "/agents"}
	}
	return Note{Kind: kind, ID: "burst-" + kind, Title: fmt.Sprintf("%d new requests", len(buf)), Body: buf[0].Body, Link: buf[0].Link}
}
