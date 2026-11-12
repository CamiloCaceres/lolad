package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// validBase accepts an https:// base URL, or http:// on loopback (tests).
func validBase(s string) bool {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	return u.Scheme == "http" && (strings.EqualFold(h, "localhost") || ip != nil && ip.IsLoopback())
}

// NormalizeURL trims and validates a hub base URL.
func NormalizeURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if !validBase(s) {
		return "", fmt.Errorf("%w: hub URL must be https://host (no path)", core.ErrInvalid)
	}
	return s, nil
}

// MemberURL is the federation URL a member advertises.
func MemberURL(hubURL, handle string) string { return strings.TrimRight(hubURL, "/") + "/p/" + handle }

// Signer is the provider key, kept inside fed.
type Signer struct {
	PublicKey string              // PEM
	Sign      func([]byte) []byte // Ed25519 over the message
}

// Error is a hub refusal with its AMP-style code.
type Error struct {
	Status     int
	Code       string
	Message    string
	Suggestion string // set for name_taken
}

func (e *Error) Error() string {
	if e.Suggestion != "" {
		return fmt.Sprintf("hub: %s (try %q)", e.Message, e.Suggestion)
	}
	return "hub: " + e.Message
}

func call(ctx context.Context, hc *http.Client, method, u, handle string, body []byte, s Signer) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	signRequest(req, handle, body, time.Now(), s.Sign)
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := c.Do(req)
	if e != nil {
		return nil, fmt.Errorf("hub unreachable")
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != 200 {
		var v struct{ Error, Message, Suggestion string }
		_ = json.Unmarshal(b, &v)
		if v.Message == "" || len(v.Message) > 256 {
			v.Message = fmt.Sprintf("request rejected (HTTP %d)", res.StatusCode)
		}
		return nil, &Error{Status: res.StatusCode, Code: v.Error, Message: v.Message, Suggestion: v.Suggestion}
	}
	return b, nil
}

// Register claims handle on the hub with a single-use sign-up code and
// returns the member URL. The code is sent only in the request body.
func Register(ctx context.Context, hc *http.Client, hubURL, handle, display, code string, s Signer) (string, error) {
	hubURL, e := NormalizeURL(hubURL)
	if e != nil {
		return "", e
	}
	if !ValidHandle(handle) {
		return "", fmt.Errorf("%w: handle must be 3-32 of a-z, 0-9 and - (not at the ends)", core.ErrInvalid)
	}
	body, _ := json.Marshal(registerBody{Code: strings.TrimSpace(code), Handle: handle, Display: display, PublicKey: s.PublicKey})
	b, e := call(ctx, hc, "POST", hubURL+"/v1/hub/register", handle, body, s)
	if e != nil {
		return "", e
	}
	var v struct{ Handle, URL string }
	if json.Unmarshal(b, &v) != nil || v.Handle != handle || v.URL != MemberURL(hubURL, handle) {
		return "", errors.New("hub: unexpected registration response")
	}
	return v.URL, nil
}

// Leave releases handle on the hub.
func Leave(ctx context.Context, hc *http.Client, hubURL, handle string, s Signer) error {
	hubURL, e := NormalizeURL(hubURL)
	if e != nil {
		return e
	}
	_, e = call(ctx, hc, "DELETE", hubURL+"/v1/hub/account", handle, nil, s)
	return e
}

// ClientOptions configure the tunnel client.
type ClientOptions struct {
	HubURL, Handle string
	Signer         Signer
	// Handler serves tunneled requests. It must be the federation handler
	// only, never the daemon mux.
	Handler http.Handler
	// Peers lists peer federation URLs; those on this hub are watched.
	Peers func() []string
	// OnPeerOnline is called with a watched peer's member URL when it connects.
	OnPeerOnline func(url string)
	// OnState is called on every state change.
	OnState func(core.HubStatus)
	Logger  *slog.Logger
	Backoff []time.Duration
}

// Client keeps the tunnel to the hub open.
type Client struct {
	o       ClientOptions
	mu      sync.Mutex
	st      core.HubStatus
	refresh chan struct{}
}

// Refresh re-reads Peers and updates the hub's watch list now (call it after
// pairing). Without it the list is re-checked every 2 seconds.
func (c *Client) Refresh() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// NewClient validates options.
func NewClient(o ClientOptions) (*Client, error) {
	u, e := NormalizeURL(o.HubURL)
	if e != nil {
		return nil, e
	}
	o.HubURL = u
	if !ValidHandle(o.Handle) || o.Handler == nil || o.Signer.Sign == nil {
		return nil, fmt.Errorf("%w: hub client needs a handle, handler and signer", core.ErrInvalid)
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Peers == nil {
		o.Peers = func() []string { return nil }
	}
	if len(o.Backoff) == 0 {
		o.Backoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	}
	return &Client{o: o, refresh: make(chan struct{}, 1), st: core.HubStatus{URL: o.HubURL, Handle: o.Handle, State: core.HubConnecting, Since: time.Now().UTC()}}, nil
}

// Status is the current tunnel state.
func (c *Client) Status() core.HubStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

func (c *Client) set(state, err string) {
	c.mu.Lock()
	changed := c.st.State != state || c.st.Error != err
	if c.st.State != state {
		c.st.Since = time.Now().UTC()
	}
	c.st.State, c.st.Error = state, err
	st := c.st
	c.mu.Unlock()
	if changed && c.o.OnState != nil {
		c.o.OnState(st)
	}
}

// Run keeps the tunnel up until ctx ends, reconnecting with backoff.
func (c *Client) Run(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		c.set(core.HubConnecting, c.Status().Error)
		up, e := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if up {
			attempt = 0
		}
		msg := "connection to the hub lost"
		state := core.HubError
		var ce websocket.CloseError
		if errors.As(e, &ce) {
			switch ce.Code {
			case CloseUnauthorized:
				msg = "the hub rejected this lolad's key or handle"
			case CloseDisabled:
				msg, state = "the hub operator disabled this handle", core.HubDisabled
			case CloseReplaced:
				msg = "another connection with this handle replaced this one"
			}
		} else if !up {
			msg = "hub unreachable"
		}
		c.set(state, msg)
		d := c.o.Backoff[min(attempt, len(c.o.Backoff)-1)]
		attempt++
		d += time.Duration(rand.Int64N(int64(d/4) + 1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}

func (c *Client) watched() []string {
	prefix := c.o.HubURL + "/p/"
	var out []string
	for _, u := range c.o.Peers() {
		u = strings.TrimRight(u, "/")
		if h, ok := strings.CutPrefix(u, prefix); ok && ValidHandle(h) && h != c.o.Handle {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// session runs one tunnel connection. up reports whether it authenticated.
func (c *Client) session(ctx context.Context) (up bool, err error) {
	wsURL := "ws" + strings.TrimPrefix(c.o.HubURL, "http") + "/v1/hub/tunnel"
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, wsURL, nil)
	cancel()
	if err != nil {
		return false, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	var writeMu sync.Mutex
	send := func(f frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return wsjson.Write(wctx, conn, f)
	}
	var ch frame
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(actx, conn, &ch)
	cancel()
	if err != nil || ch.Type != "challenge" || ch.Nonce == "" {
		return false, errors.New("bad hub handshake")
	}
	if err = send(frame{Type: "auth", Handle: c.o.Handle, Sig: c.o.Signer.Sign(tunnelMessage(c.o.HubURL, ch.Nonce))}); err != nil {
		return false, err
	}
	var ready frame
	actx, cancel = context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(actx, conn, &ready)
	cancel()
	if err != nil {
		return false, err
	}
	if ready.Type != "ready" || ready.URL != MemberURL(c.o.HubURL, c.o.Handle) {
		return false, errors.New("bad hub handshake")
	}
	c.set(core.HubConnected, "")
	c.o.Logger.Info("hub tunnel up", "hub", c.o.HubURL, "handle", c.o.Handle)

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	var last []string
	sendWatch := func() {
		w := c.watched()
		if last != nil && slices.Equal(w, last) {
			return
		}
		if send(frame{Type: "watch", Handles: append([]string{}, w...)}) == nil {
			last = w
			if last == nil {
				last = []string{}
			}
		}
	}
	sendWatch()
	go func() {
		ping := time.NewTicker(PingEvery)
		watch := time.NewTicker(2 * time.Second)
		defer ping.Stop()
		defer watch.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-watch.C:
				sendWatch()
			case <-c.refresh:
				sendWatch()
			case <-ping.C:
				pctx, cancel := context.WithTimeout(sctx, SilenceLimit-PingEvery)
				e := conn.Ping(pctx)
				cancel()
				if e != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()
	sem := make(chan struct{}, MaxInFlight)
	for {
		var f frame
		if err = wsjson.Read(sctx, conn, &f); err != nil {
			return true, err
		}
		switch f.Type {
		case "req":
			select {
			case sem <- struct{}{}:
			default:
				_ = send(frame{Type: "res", ID: f.ID, Status: 503, Headers: map[string]string{"Content-Type": "application/json", "Retry-After": "5"}, Body: []byte(`{"error":"provider_busy","message":"Too many requests in flight"}`)})
				continue
			}
			go func(f frame) {
				defer func() { <-sem }()
				_ = send(c.serve(sctx, f))
			}(f)
		case "online":
			if ValidHandle(f.Handle) && c.o.OnPeerOnline != nil {
				go c.o.OnPeerOnline(MemberURL(c.o.HubURL, f.Handle))
			}
		}
	}
}

// serve runs one tunneled request through the federation handler.
func (c *Client) serve(ctx context.Context, f frame) frame {
	notFound := frame{Type: "res", ID: f.ID, Status: 404, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"error":"not_found","message":"Unknown endpoint"}`)}
	u, e := url.Parse(f.Path)
	if e != nil || !strings.HasPrefix(u.Path, "/v1/federation/") || strings.Contains(u.Path, "..") || (f.Method != "GET" && f.Method != "POST") || len(f.Body) > MaxBody {
		return notFound
	}
	rctx, cancel := context.WithTimeout(ctx, ForwardTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(rctx, f.Method, "http://lolad-hub-tunnel"+u.RequestURI(), bytes.NewReader(f.Body))
	if e != nil {
		return notFound
	}
	h := http.Header{}
	for k, v := range f.Headers {
		h.Set(k, v)
	}
	for k, v := range pick(h, requestHeaders) {
		req.Header.Set(k, v)
	}
	ip := f.ClientIP
	if net.ParseIP(ip) == nil {
		ip = "0.0.0.0"
	}
	req.RemoteAddr = net.JoinHostPort(ip, "0")
	w := &recorder{h: http.Header{}}
	c.o.Handler.ServeHTTP(w, req)
	if w.status == 0 {
		w.status = 200
	}
	if w.buf.Len() > MaxBody {
		return frame{Type: "res", ID: f.ID, Status: 502, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"error":"bad_gateway","message":"Response too large"}`)}
	}
	return frame{Type: "res", ID: f.ID, Status: w.status, Headers: pick(w.h, responseHeaders), Body: w.buf.Bytes()}
}

type recorder struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.h }
func (r *recorder) WriteHeader(s int) {
	if r.status == 0 {
		r.status = s
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	if r.buf.Len()+len(b) > MaxBody+1 {
		b = b[:max(0, MaxBody+1-r.buf.Len())]
	}
	return r.buf.Write(b)
}
