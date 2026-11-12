package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Version is reported by /v1/hub/info.
const Version = "1"

// Options configure a Server.
type Options struct {
	Store     *Store
	PublicURL string // e.g. "https://hub.example.com"; the base of every member URL
	Logger    *slog.Logger
	Clock     func() time.Time
	// TrustedProxies are the reverse proxies whose X-Forwarded-For is
	// believed. Loopback is always trusted (Caddy on the same host). In a
	// container the proxy (Traefik under Coolify) is on a private network.
	TrustedProxies []netip.Prefix
}

// Server is the hub. Mount Handler behind a TLS-terminating proxy and run Run
// for housekeeping.
type Server struct {
	o     Options
	store *Store

	mu      sync.Mutex
	tunnels map[string]*tunnel // handle → current tunnel

	limits *limiter
}

type tunnel struct {
	handle  string
	conn    *websocket.Conn
	writeMu sync.Mutex

	mu       sync.Mutex
	pending  map[string]chan frame
	inflight int
	watch    map[string]bool
	closed   bool
}

func (t *tunnel) send(ctx context.Context, f frame) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, t.conn, f)
}

// NewServer validates options and returns a hub.
func NewServer(o Options) (*Server, error) {
	if o.Store == nil {
		return nil, errors.New("hub: store required")
	}
	o.PublicURL = strings.TrimRight(o.PublicURL, "/")
	if !validBase(o.PublicURL) {
		return nil, errors.New("hub: public URL must be https:// (http only for loopback)")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	o.Store.now = o.Clock
	return &Server{o: o, store: o.Store, tunnels: map[string]*tunnel{}, limits: newLimiter(o.Clock)}, nil
}

// Handler serves the hub API, the tunnel and the public member URLs.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/hub/info", s.info)
	m.HandleFunc("POST /v1/hub/register", s.register)
	m.HandleFunc("DELETE /v1/hub/account", s.deleteAccount)
	m.HandleFunc("GET /v1/hub/tunnel", s.serveTunnel)
	m.HandleFunc("/p/", s.forward)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "Unknown endpoint") })
	return m
}

// Run does housekeeping until ctx ends: it closes tunnels of disabled or
// removed accounts (the admin CLI edits the DB directly) and prunes limits.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closeAll()
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep closes tunnels whose account is gone or disabled.
func (s *Server) Sweep(ctx context.Context) {
	s.limits.prune()
	s.mu.Lock()
	list := make([]*tunnel, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		list = append(list, t)
	}
	s.mu.Unlock()
	for _, t := range list {
		a, e := s.store.Account(ctx, t.handle)
		if errors.Is(e, ErrNotFound) || e == nil && a.Disabled {
			t.conn.Close(CloseDisabled, "account disabled or removed")
		}
	}
}

func (s *Server) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tunnels {
		t.conn.Close(websocket.StatusGoingAway, "hub shutting down")
	}
}

// Online reports whether handle has a live tunnel.
func (s *Server) Online(handle string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunnels[handle] != nil
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]string{"name": "lolad-hub", "version": Version, "public_url": s.o.PublicURL, "signup": "invite"})
}

type registerBody struct {
	Code      string `json:"code"`
	Handle    string `json:"handle"`
	Display   string `json:"display"`
	PublicKey string `json:"public_key"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.limits.peek("fail:"+ip, 10, time.Hour) {
		fail(w, 429, "rate_limited", "Too many failed sign-up attempts")
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if e != nil || len(raw) > 64<<10 {
		fail(w, 413, "request_too_large", "Request too large")
		return
	}
	var b registerBody
	if json.Unmarshal(raw, &b) != nil {
		fail(w, 400, "invalid_request", "Invalid request")
		return
	}
	if !ValidHandle(b.Handle) {
		fail(w, 400, "invalid_handle", "Handle must be 3-32 of a-z, 0-9 and -, not starting or ending with -")
		return
	}
	if len(b.Display) > 64 {
		fail(w, 400, "invalid_request", "Display name too long")
		return
	}
	key, e := parsePublicKey(b.PublicKey)
	if e != nil || r.Header.Get("X-AMP-Provider") != b.Handle || !verifyRequest(r, raw, key, s.o.Clock()) {
		s.limits.hit("fail:"+ip, time.Hour)
		fail(w, 403, "invalid_signature", "Invalid signature or timestamp")
		return
	}
	a, e := s.store.register(r.Context(), b.Code, b.Handle, strings.TrimSpace(b.Display), b.PublicKey)
	var re *registerError
	if errors.As(e, &re) {
		if re.code == "invalid_invite" {
			s.limits.hit("fail:"+ip, time.Hour)
		}
		body := map[string]string{"error": re.code, "message": re.msg}
		if re.suggestion != "" {
			body["suggestion"] = re.suggestion
		}
		reply(w, re.status, body)
		return
	}
	if e != nil {
		s.o.Logger.Error("register failed", "err", e)
		fail(w, 500, "internal_error", "Cannot register")
		return
	}
	s.o.Logger.Info("registered", "handle", a.Handle)
	reply(w, 200, map[string]string{"handle": a.Handle, "url": s.o.PublicURL + "/p/" + a.Handle})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	handle := r.Header.Get("X-AMP-Provider")
	a, e := s.store.Account(r.Context(), handle)
	if e != nil {
		fail(w, 404, "not_found", "Unknown handle")
		return
	}
	key, e := parsePublicKey(a.PublicKey)
	if e != nil || !verifyRequest(r, raw, key, s.o.Clock()) {
		fail(w, 403, "invalid_signature", "Invalid signature or timestamp")
		return
	}
	if e = s.store.Remove(r.Context(), handle); e != nil {
		fail(w, 500, "internal_error", "Cannot remove account")
		return
	}
	s.mu.Lock()
	t := s.tunnels[handle]
	s.mu.Unlock()
	if t != nil {
		t.conn.Close(websocket.StatusNormalClosure, "account removed")
	}
	s.o.Logger.Info("account removed", "handle", handle)
	reply(w, 200, map[string]bool{"ok": true})
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) serveTunnel(w http.ResponseWriter, r *http.Request) {
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	c.SetReadLimit(4 << 20)
	ctx := r.Context()
	nonce := randomID(32)
	t := &tunnel{conn: c, pending: map[string]chan frame{}, watch: map[string]bool{}}
	if e = t.send(ctx, frame{Type: "challenge", Nonce: nonce}); e != nil {
		c.CloseNow()
		return
	}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var auth frame
	e = wsjson.Read(actx, c, &auth)
	cancel()
	if e != nil || auth.Type != "auth" {
		c.Close(CloseUnauthorized, "expected auth")
		return
	}
	a, e := s.store.Account(ctx, auth.Handle)
	if e != nil {
		c.Close(CloseUnauthorized, "unknown handle")
		return
	}
	key, e := parsePublicKey(a.PublicKey)
	if e != nil || !ed25519.Verify(key, tunnelMessage(s.o.PublicURL, nonce), auth.Sig) {
		c.Close(CloseUnauthorized, "bad signature")
		return
	}
	if a.Disabled {
		c.Close(CloseDisabled, "handle disabled by the hub operator")
		return
	}
	t.handle = a.Handle
	s.mu.Lock()
	old := s.tunnels[t.handle]
	s.tunnels[t.handle] = t
	s.mu.Unlock()
	if old != nil {
		old.conn.Close(CloseReplaced, "replaced by a newer tunnel")
	}
	s.store.touch(ctx, t.handle)
	s.o.Logger.Info("tunnel up", "handle", t.handle)
	defer func() {
		s.mu.Lock()
		if s.tunnels[t.handle] == t {
			delete(s.tunnels, t.handle)
		}
		s.mu.Unlock()
		t.mu.Lock()
		t.closed = true
		for id, ch := range t.pending {
			close(ch)
			delete(t.pending, id)
		}
		t.mu.Unlock()
		c.CloseNow()
		s.o.Logger.Info("tunnel down", "handle", t.handle)
	}()
	if e = t.send(ctx, frame{Type: "ready", URL: s.o.PublicURL + "/p/" + t.handle}); e != nil {
		return
	}
	s.announce(ctx, t.handle)

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		tick := time.NewTicker(PingEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				pctx, cancel := context.WithTimeout(ctx, SilenceLimit-PingEvery)
				e := c.Ping(pctx)
				cancel()
				if e != nil {
					c.CloseNow()
					return
				}
			}
		}
	}()
	for {
		var f frame
		if e := wsjson.Read(ctx, c, &f); e != nil {
			return
		}
		switch f.Type {
		case "res":
			t.mu.Lock()
			ch := t.pending[f.ID]
			delete(t.pending, f.ID)
			t.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "watch":
			if len(f.Handles) > 1000 {
				f.Handles = f.Handles[:1000]
			}
			set := map[string]bool{}
			for _, h := range f.Handles {
				if ValidHandle(h) && h != t.handle {
					set[h] = true
				}
			}
			t.mu.Lock()
			t.watch = set
			t.mu.Unlock()
			// Tell it who is already online, so its queued mail goes now.
			for h := range set {
				if s.Online(h) {
					_ = t.send(ctx, frame{Type: "online", Handle: h})
				}
			}
		}
	}
}

// announce tells every tunnel watching handle that it is online.
func (s *Server) announce(ctx context.Context, handle string) {
	s.mu.Lock()
	var to []*tunnel
	for _, t := range s.tunnels {
		t.mu.Lock()
		if t.watch[handle] {
			to = append(to, t)
		}
		t.mu.Unlock()
	}
	s.mu.Unlock()
	for _, t := range to {
		go t.send(context.WithoutCancel(ctx), frame{Type: "online", Handle: handle})
	}
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request) {
	start := s.o.Clock()
	handle, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/p/"), "/")
	path := "/" + rest
	if !ValidHandle(handle) || !strings.HasPrefix(path, "/v1/federation/") || strings.Contains(path, "..") {
		fail(w, 404, "not_found", "Unknown endpoint")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "Only GET and POST")
		return
	}
	status := 0
	defer func() {
		s.o.Logger.Info("forward", "handle", handle, "path", path, "status", status, "ms", s.o.Clock().Sub(start).Milliseconds())
	}()
	a, e := s.store.Account(r.Context(), handle)
	if errors.Is(e, ErrNotFound) {
		status = 404
		fail(w, 404, "recipient_not_found", "No such lolad on this hub")
		return
	} else if e != nil {
		status = 500
		fail(w, 500, "internal_error", "Hub error")
		return
	}
	if a.Disabled {
		status = 403
		fail(w, 403, "provider_disabled", "This lolad is disabled on the hub")
		return
	}
	ip := s.clientIP(r)
	if !s.limits.allow("h:"+handle, 300, time.Minute) || !s.limits.allow("ip:"+ip, 600, time.Minute) {
		status = 429
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited", "Too many requests")
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if e != nil || len(body) > MaxBody {
		status = 413
		fail(w, 413, "request_too_large", "Request exceeds 1 MB")
		return
	}
	s.mu.Lock()
	t := s.tunnels[handle]
	s.mu.Unlock()
	offline := func() {
		status = 503
		w.Header().Set("Retry-After", "30")
		fail(w, 503, "provider_offline", "That lolad is not connected to the hub right now")
	}
	if t == nil {
		offline()
		return
	}
	id := randomID(12)
	ch := make(chan frame, 1)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		offline()
		return
	}
	if t.inflight >= MaxInFlight {
		t.mu.Unlock()
		status = 503
		w.Header().Set("Retry-After", "5")
		fail(w, 503, "provider_busy", "Too many requests in flight")
		return
	}
	t.inflight++
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.inflight--
		delete(t.pending, id)
		t.mu.Unlock()
	}()
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	if e = t.send(r.Context(), frame{Type: "req", ID: id, Method: r.Method, Path: path, Headers: pick(r.Header, requestHeaders), Body: body, ClientIP: ip}); e != nil {
		offline()
		return
	}
	timer := time.NewTimer(ForwardTimeout)
	defer timer.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			offline()
			return
		}
		if res.Status < 100 || res.Status > 599 || len(res.Body) > MaxBody {
			status = 502
			fail(w, 502, "bad_gateway", "Invalid response from that lolad")
			return
		}
		h := http.Header{}
		for k, v := range res.Headers {
			h.Set(k, v)
		}
		for k, v := range pick(h, responseHeaders) {
			w.Header().Set(k, v)
		}
		status = res.Status
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
	case <-timer.C:
		status = 504
		fail(w, 504, "timeout", "That lolad did not answer in time")
	case <-r.Context().Done():
		status = 499
	}
}

// clientIP is the caller's address. Behind a trusted reverse proxy it is the
// rightmost X-Forwarded-For entry, which the proxy itself appended.
func (s *Server) clientIP(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	if s.trusted(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if v := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(v) != nil {
				return v
			}
		}
	}
	return host
}

func (s *Server) trusted(host string) bool {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	for _, p := range s.o.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ParsePrefixes reads a comma-separated list of CIDRs or bare IPs.
func ParsePrefixes(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, map[string]string{"error": code, "message": msg})
}

// limiter is a fixed-window counter per key.
type limiter struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]window
}
type window struct {
	at    time.Time
	n     int
	limit time.Duration
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, m: map[string]window{}}
}

func (l *limiter) current(key string, per time.Duration) window {
	v := l.m[key]
	if v.at.IsZero() || l.now().Sub(v.at) >= per {
		v = window{at: l.now(), limit: per}
	}
	return v
}

// allow counts one event and reports whether it is within limit.
func (l *limiter) allow(key string, limit int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.current(key, per)
	if v.n >= limit {
		return false
	}
	v.n++
	l.m[key] = v
	return true
}

// peek reports whether key is under limit without counting.
func (l *limiter) peek(key string, limit int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current(key, per).n < limit
}

// hit counts one event.
func (l *limiter) hit(key string, per time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.current(key, per)
	v.n++
	l.m[key] = v
}

func (l *limiter) prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, v := range l.m {
		if l.now().Sub(v.at) >= v.limit {
			delete(l.m, k)
		}
	}
}
