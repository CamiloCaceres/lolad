// Package api exposes the daemon service over loopback JSON HTTP.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hooks"
)

type Options struct {
	Auth      *auth.Store
	Version   string
	Remote    bool
	PublicURL string
	HookSink  core.HookSink
	StateDir  string
	Trigger   func(*http.Request, string, string, string) error
}

func write(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		status := 500
		switch {
		case errors.Is(e, core.ErrNotFound):
			status = 404
		case errors.Is(e, core.ErrDenied):
			status = 403
		case errors.Is(e, core.ErrExists), errors.Is(e, core.ErrConflict):
			status = 409
		case errors.Is(e, core.ErrInvalid), errors.Is(e, core.ErrNotRunning):
			status = 422
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status), "message": e.Error()})
		return
	}
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if e := d.Decode(v); e != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalid, e)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("%w: expected one JSON value", core.ErrInvalid)
	}
	return nil
}
func number(r *http.Request, k string, def int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(k))
	if e != nil || v <= 0 {
		return def
	}
	if v > 1000 {
		return 1000
	}
	return v
}
func before(r *http.Request) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before"))
	return t
}
func New(s core.Service, o Options) http.Handler {
	m := http.NewServeMux()
	settingsRoutes(m, s)
	federationRoutes(m, s)
	// Creation metadata stays behind the same authentication boundary as the service.
	m.HandleFunc("GET /api/create/options", func(w http.ResponseWriter, r *http.Request) { v, e := s.CreateOptions(r.Context()); write(w, v, e) })
	remoteRoutes(m, s, o)
	m.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) { v, e := s.List(r.Context()); write(w, v, e) })
	m.HandleFunc("GET /api/agents/{name}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Get(r.Context(), r.PathValue("name"))
		write(w, v, e)
	})
	m.HandleFunc("POST /api/agents/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		n := r.PathValue("name")
		var e error
		switch r.PathValue("action") {
		case "start":
			e = s.Start(r.Context(), n)
		case "stop":
			e = s.Stop(r.Context(), n)
		case "restart":
			e = s.Restart(r.Context(), n, r.URL.Query().Has("fresh") && r.URL.Query().Get("fresh") != "false" && r.URL.Query().Get("fresh") != "0")
		case "interrupt":
			e = s.Interrupt(r.Context(), n)
		default:
			e = core.ErrNotFound
		}
		write(w, nil, e)
	})
	m.HandleFunc("GET /api/agents/{name}/events", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Events(r.Context(), r.PathValue("name"), number(r, "limit", 100))
		write(w, v, e)
	})
	m.HandleFunc("GET /api/agents/{name}/capture", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Capture(r.Context(), r.PathValue("name"), number(r, "lines", 100))
		write(w, map[string]string{"capture": v}, e)
	})
	m.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Events(r.Context(), r.URL.Query().Get("agent"), number(r, "limit", 100))
		write(w, v, e)
	})
	m.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("X-Accel-Buffering", "no")
		f, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		f.Flush()
		ch := s.Subscribe(r.Context())
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, b)
				f.Flush()
			case <-tick.C:
				fmt.Fprint(w, ": keepalive\n\n")
				f.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	m.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) { write(w, s.Me(), nil) })
	m.HandleFunc("GET /api/mail/conversations", func(w http.ResponseWriter, r *http.Request) { v, e := s.Conversations(r.Context()); write(w, v, e) })
	m.HandleFunc("GET /api/mail/conversation/{peer}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Conversation(r.Context(), r.PathValue("peer"), number(r, "limit", 100), before(r))
		write(w, v, e)
	})
	m.HandleFunc("POST /api/mail/send", func(w http.ResponseWriter, r *http.Request) {
		var v core.Outgoing
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		mail, e := s.UserSend(r.Context(), v)
		write(w, mail, e)
	})
	m.HandleFunc("POST /api/mail/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		write(w, nil, s.MarkRead(r.Context(), r.PathValue("id")))
	})
	m.HandleFunc("GET /api/mail/traffic", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Traffic(r.Context(), number(r, "limit", 100), before(r))
		write(w, v, e)
	})
	m.HandleFunc("GET /api/org", func(w http.ResponseWriter, r *http.Request) { v, e := s.Org(r.Context()); write(w, v, e) })
	m.HandleFunc("PUT /api/org", func(w http.ResponseWriter, r *http.Request) {
		var v core.Org
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		write(w, nil, s.SaveOrg(r.Context(), v))
	})
	m.HandleFunc("GET /api/org/check", func(w http.ResponseWriter, r *http.Request) {
		write(w, s.Check(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to")), nil)
	})
	m.HandleFunc("POST /api/create", func(w http.ResponseWriter, r *http.Request) {
		var v core.CreateRequest
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		a, e := s.Create(r.Context(), v)
		write(w, a, e)
	})
	m.HandleFunc("POST /api/adopt", func(w http.ResponseWriter, r *http.Request) {
		var v core.AdoptRequest
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		a, e := s.Adopt(r.Context(), v)
		write(w, a, e)
	})
	m.HandleFunc("POST /api/reload", func(w http.ResponseWriter, r *http.Request) { write(w, nil, s.Reload(r.Context())) })
	if o.HookSink != nil {
		m.Handle("POST /api/hook", hooks.NewHandler(o.HookSink))
	}
	m.HandleFunc("POST /hook/{agent}/{name}", func(w http.ResponseWriter, r *http.Request) {
		a := r.PathValue("agent")
		if _, e := s.Get(r.Context(), a); e != nil {
			write(w, nil, e)
			return
		}
		token, e := os.ReadFile(filepath.Join(o.StateDir, "agents", a, "webhook.token"))
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if given == "" {
			given = r.Header.Get("X-Webhook-Token")
		}
		if e != nil || len(token) == 0 || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(token))), []byte(given)) != 1 {
			write(w, nil, fmt.Errorf("%w: invalid webhook token", core.ErrDenied))
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			write(w, nil, fmt.Errorf("%w: webhook body", core.ErrInvalid))
			return
		}
		if o.Trigger == nil {
			write(w, nil, core.ErrNotFound)
			return
		}
		write(w, nil, o.Trigger(r, a, r.PathValue("name"), string(b)))
	})
	protection := http.NewCrossOriginProtection()
	if o.PublicURL != "" {
		_ = protection.AddTrustedOrigin(o.PublicURL)
	}
	return protection.Handler(m)
}
