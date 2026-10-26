package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// streamEvent is one SSE message. app.js turns it into DOM events that
// htmx-enhanced elements refresh on: lolad:mail, lolad:agent and
// lolad:a-<agent>.
type streamEvent struct {
	core.Event
	Class string `json:"class"` // mail | agent
}

func classify(ev core.Event) string {
	switch ev.Kind {
	case "mail", "notified":
		return "mail"
	}
	return "agent"
}

func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // live for as long as the tab is open
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	events := s.svc.Subscribe(ctx)
	if _, err := fmt.Fprint(w, "retry: 3000\n: hello\n\n"); err != nil {
		return
	}
	_ = rc.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			b, err := json.Marshal(streamEvent{Event: ev, Class: classify(ev)})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: lolad\ndata: %s\n\n", ev.ID, b); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
