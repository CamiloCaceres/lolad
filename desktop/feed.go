package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	backoffMin = time.Second
	backoffMax = 30 * time.Second
)

// nextBackoff doubles up to backoffMax.
func nextBackoff(cur time.Duration) time.Duration {
	if cur < backoffMin {
		return backoffMin
	}
	if cur *= 2; cur > backoffMax {
		return backoffMax
	}
	return cur
}

// Data is one consistent read of the daemon.
type Data struct {
	Agents []Agent
	Convs  []Conversation
	Conns  []Connection
	Snap   Snapshot
}

// feed keeps Data fresh: it subscribes to the loopback /api/stream, and on any
// event (and every few seconds as a safety net) re-reads /api/agents,
// /api/mail/conversations and /api/connections. The stream has no replay, so a
// reconnect always triggers a refresh.
type feed struct {
	base     string // "http://127.0.0.1:4100"
	poll     time.Duration
	debounce time.Duration
	onData   func(Data)

	kick chan struct{}
	api  *http.Client

	mu        sync.Mutex
	connected bool // SSE currently attached (for tests and status)
}

func newFeed(base string, onData func(Data)) *feed {
	return &feed{
		base: strings.TrimSuffix(base, "/"), poll: 5 * time.Second, debounce: 250 * time.Millisecond,
		onData: onData, kick: make(chan struct{}, 1),
		api: &http.Client{Timeout: 5 * time.Second},
	}
}

func (f *feed) wake() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

func (f *feed) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

// Run blocks until ctx ends.
func (f *feed) Run(ctx context.Context) {
	go f.stream(ctx)
	t := time.NewTicker(f.poll)
	defer t.Stop()
	f.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
			// Collapse a burst of events into one refresh.
			select {
			case <-time.After(f.debounce):
			case <-ctx.Done():
				return
			}
			select {
			case <-f.kick:
			default:
			}
			f.refresh(ctx)
		case <-t.C:
			f.refresh(ctx)
		}
	}
}

func (f *feed) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.api.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return b, &statusError{resp.StatusCode}
	}
	return b, nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return http.StatusText(e.code) }

func (f *feed) refresh(ctx context.Context) {
	var d Data
	b, err := f.get(ctx, "/api/agents")
	if err != nil {
		f.onData(d) // zero Snapshot: Up=false
		return
	}
	d.Agents, _ = decodeList[Agent](b)
	if b, err := f.get(ctx, "/api/mail/conversations"); err == nil {
		d.Convs, _ = decodeList[Conversation](b)
	}
	// Federation endpoints may not exist on older daemons: ignore any error.
	if b, err := f.get(ctx, "/api/connections"); err == nil {
		d.Conns, _ = decodeList[Connection](b)
	}
	d.Snap = Compute(d.Agents, d.Convs, d.Conns)
	f.onData(d)
}

// stream holds the SSE connection open, reconnecting with bounded backoff.
func (f *feed) stream(ctx context.Context) {
	back := time.Duration(0)
	for ctx.Err() == nil {
		start := time.Now()
		f.streamOnce(ctx)
		f.mu.Lock()
		f.connected = false
		f.mu.Unlock()
		if time.Since(start) > time.Minute {
			back = 0 // it was healthy; start over
		}
		back = nextBackoff(back)
		select {
		case <-time.After(back):
		case <-ctx.Done():
			return
		}
	}
}

func (f *feed) streamOnce(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.base+"/api/stream", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	// No overall timeout: the stream lives as long as the connection does.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	f.mu.Lock()
	f.connected = true
	f.mu.Unlock()
	f.wake() // no replay: re-read everything after (re)connecting
	// The daemon sends a keepalive comment every 20 s; a silent connection
	// for longer than that (sleep, dead route) is dropped and re-opened.
	idle := time.AfterFunc(70*time.Second, func() { resp.Body.Close() })
	defer idle.Stop()
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		idle.Reset(70 * time.Second)
		if strings.HasPrefix(line, "event:") {
			f.wake()
		}
	}
}
