package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/fed"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

type testHub struct {
	srv   *Server
	store *Store
	ts    *httptest.Server
	url   string
}

func newHub(t *testing.T) *testHub {
	t.Helper()
	st, e := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if e != nil {
		t.Fatal(e)
	}
	h := &testHub{store: st}
	var handler http.Handler
	h.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	h.url = h.ts.URL
	h.srv, e = NewServer(Options{Store: st, PublicURL: h.url})
	if e != nil {
		t.Fatal(e)
	}
	handler = h.srv.Handler()
	t.Cleanup(func() { h.srv.closeAll(); h.ts.Close(); st.Close() })
	return h
}

func (h *testHub) invite(t *testing.T) string {
	t.Helper()
	code, _, e := h.store.CreateInvite(t.Context(), "test", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	return code
}

func newSigner(t *testing.T) Signer {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return Signer{PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), Sign: func(m []byte) []byte { return ed25519.Sign(key, m) }}
}

func eventually(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + what)
}

func startClient(t *testing.T, o ClientOptions) (*Client, context.CancelFunc) {
	t.Helper()
	if o.Backoff == nil {
		o.Backoff = []time.Duration{20 * time.Millisecond}
	}
	c, e := NewClient(o)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("client did not stop")
		}
	}
	t.Cleanup(stop)
	return c, stop
}

func TestRegister(t *testing.T) {
	h := newHub(t)
	s := newSigner(t)
	ctx := t.Context()
	if _, e := Register(ctx, nil, h.url, "milo", "Milo", "hubinv_wrong", s); !isCode(e, "invalid_invite") {
		t.Fatal("bad code accepted", e)
	}
	code := h.invite(t)
	u, e := Register(ctx, nil, h.url, "milo", "Milo", code, s)
	if e != nil || u != h.url+"/p/milo" {
		t.Fatal(u, e)
	}
	// Same key + handle again: idempotent, no code needed.
	if _, e = Register(ctx, nil, h.url, "milo", "Milo", "", s); e != nil {
		t.Fatal("idempotent register", e)
	}
	// Same key, other handle.
	if _, e = Register(ctx, nil, h.url, "other", "", h.invite(t), s); !isCode(e, "key_registered") {
		t.Fatal(e)
	}
	// Code is single use.
	if _, e = Register(ctx, nil, h.url, "jugui", "", code, newSigner(t)); !isCode(e, "invalid_invite") {
		t.Fatal("code reused", e)
	}
	// Taken handle suggests another.
	var he *Error
	if _, e = Register(ctx, nil, h.url, "milo", "", h.invite(t), newSigner(t)); !errors.As(e, &he) || he.Code != "name_taken" || he.Suggestion != "milo-2" {
		t.Fatal(e)
	}
	if _, e = Register(ctx, nil, h.url, "admin", "", h.invite(t), newSigner(t)); e == nil {
		t.Fatal("reserved handle accepted")
	}
	// A signature by a different key than the body's is refused.
	other := newSigner(t)
	forged := Signer{PublicKey: newSigner(t).PublicKey, Sign: other.Sign}
	if _, e = Register(ctx, nil, h.url, "eve", "", h.invite(t), forged); !isCode(e, "invalid_signature") {
		t.Fatal(e)
	}
	if e = Leave(ctx, nil, h.url, "milo", s); e != nil {
		t.Fatal(e)
	}
	if _, e = h.store.Account(ctx, "milo"); !errors.Is(e, ErrNotFound) {
		t.Fatal("leave kept the account")
	}
	if e = Leave(ctx, nil, h.url, "jugui", s); e == nil {
		t.Fatal("leave of someone else's handle")
	}
}

func TestRegisterFailureLimit(t *testing.T) {
	h := newHub(t)
	s := newSigner(t)
	for i := 0; i < 10; i++ {
		_, _ = Register(t.Context(), nil, h.url, "milo", "", "hubinv_bad", s)
	}
	if _, e := Register(t.Context(), nil, h.url, "milo", "", h.invite(t), s); !isCode(e, "rate_limited") {
		t.Fatal("no failure limit", e)
	}
}

func isCode(e error, code string) bool {
	var he *Error
	return errors.As(e, &he) && he.Code == code
}

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

func (s *seen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.body = append(s.body, b)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Set-Cookie", "leak=1")
	w.WriteHeader(202)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *seen) last() (*http.Request, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		return nil, nil
	}
	return s.reqs[len(s.reqs)-1], s.body[len(s.body)-1]
}

func TestForwardFiltersPathsAndHeaders(t *testing.T) {
	h := newHub(t)
	s := newSigner(t)
	if _, e := Register(t.Context(), nil, h.url, "milo", "", h.invite(t), s); e != nil {
		t.Fatal(e)
	}
	// Offline first.
	res, _ := http.Post(h.url+"/p/milo/v1/federation/deliver", "application/json", strings.NewReader("{}"))
	if res.StatusCode != 503 || res.Header.Get("Retry-After") == "" {
		t.Fatal("offline status", res.StatusCode)
	}
	res.Body.Close()

	backend := &seen{}
	c, _ := startClient(t, ClientOptions{HubURL: h.url, Handle: "milo", Signer: s, Handler: backend})
	eventually(t, "tunnel", func() bool { return c.Status().State == core.HubConnected })

	req, _ := http.NewRequest("POST", h.url+"/p/milo/v1/federation/deliver", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AMP-Provider", "jugui.lolad.local")
	req.Header.Set("X-AMP-Signature", "sig")
	req.Header.Set("Tailscale-User-Login", "spoof@example.com")
	req.Header.Set("Cookie", "lolad_device=secret")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 202 || string(b) != `{"ok":true}` || res.Header.Get("Set-Cookie") != "" {
		t.Fatal("forwarded response", res.StatusCode, string(b), res.Header)
	}
	got, body := backend.last()
	if got == nil || got.URL.Path != "/v1/federation/deliver" || string(body) != `{"x":1}` {
		t.Fatal("forwarded request", got, string(body))
	}
	if got.Header.Get("X-AMP-Provider") != "jugui.lolad.local" || got.Header.Get("Tailscale-User-Login") != "" || got.Header.Get("Cookie") != "" {
		t.Fatal("header filtering", got.Header)
	}
	if !strings.HasPrefix(got.RemoteAddr, "127.0.0.1:") {
		t.Fatal("client ip not passed", got.RemoteAddr)
	}
	for _, p := range []string{"/p/milo/api/agents", "/p/milo/v1/route", "/p/milo/", "/p/nobody/v1/federation/deliver", "/v1/federation/deliver"} {
		res, e = http.Post(h.url+p, "application/json", strings.NewReader("{}"))
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal(p, res.StatusCode)
		}
	}
	n := len(backend.reqs)
	if n != 1 {
		t.Fatal("non-federation paths reached the lolad", n)
	}
	// The client refuses non-federation paths itself too.
	if f := c.serve(t.Context(), frame{ID: "x", Method: "POST", Path: "/api/agents"}); f.Status != 404 {
		t.Fatal("client path check", f.Status)
	}
	big := bytes.Repeat([]byte("a"), MaxBody+1)
	res, _ = http.Post(h.url+"/p/milo/v1/federation/deliver", "application/json", bytes.NewReader(big))
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatal("body limit", res.StatusCode)
	}
}

func TestDisableAndTakeover(t *testing.T) {
	h := newHub(t)
	s := newSigner(t)
	if _, e := Register(t.Context(), nil, h.url, "milo", "", h.invite(t), s); e != nil {
		t.Fatal(e)
	}
	a, stopA := startClient(t, ClientOptions{HubURL: h.url, Handle: "milo", Signer: s, Handler: &seen{}, Backoff: []time.Duration{time.Hour}})
	eventually(t, "tunnel a", func() bool { return a.Status().State == core.HubConnected })
	b, stopB := startClient(t, ClientOptions{HubURL: h.url, Handle: "milo", Signer: s, Handler: &seen{}, Backoff: []time.Duration{time.Hour}})
	eventually(t, "takeover", func() bool {
		return b.Status().State == core.HubConnected && a.Status().State == core.HubError && strings.Contains(a.Status().Error, "replaced")
	})
	stopA()

	// A wrong key cannot open the tunnel.
	bad, stopBad := startClient(t, ClientOptions{HubURL: h.url, Handle: "milo", Signer: newSigner(t), Handler: &seen{}, Backoff: []time.Duration{time.Hour}})
	eventually(t, "rejected", func() bool { return strings.Contains(bad.Status().Error, "rejected") })
	stopBad()
	if b.Status().State != core.HubConnected {
		t.Fatal("bad key displaced the real tunnel")
	}

	if e := h.store.SetDisabled(t.Context(), "milo", true); e != nil {
		t.Fatal(e)
	}
	res, _ := http.Post(h.url+"/p/milo/v1/federation/deliver", "application/json", strings.NewReader("{}"))
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("disabled forward", res.StatusCode)
	}
	h.srv.Sweep(t.Context())
	eventually(t, "disabled state", func() bool { return b.Status().State == core.HubDisabled })
	stopB()
	if h.srv.Online("milo") {
		t.Fatal("still online")
	}
}

func TestMembershipFile(t *testing.T) {
	p := MembershipPath(t.TempDir())
	if m, e := LoadMembership(p); m != nil || e != nil {
		t.Fatal(m, e)
	}
	if e := SaveMembership(p, Membership{URL: "https://hub.example.com", Handle: "milo", JoinedAt: time.Now()}); e != nil {
		t.Fatal(e)
	}
	m, e := LoadMembership(p)
	if e != nil || m.Handle != "milo" || m.URL != "https://hub.example.com" {
		t.Fatal(m, e)
	}
	if e = RemoveMembership(p); e != nil {
		t.Fatal(e)
	}
	if m, _ = LoadMembership(p); m != nil {
		t.Fatal("not removed")
	}
}

// lolad is one federation engine whose only listener is the hub tunnel.
type lolad struct {
	f      *fed.Engine
	p      *amp.Provider
	client *Client
	stop   context.CancelFunc
	opts   ClientOptions
}

func newLolad(t *testing.T, h *testHub, tenant string) *lolad {
	t.Helper()
	state, config := t.TempDir(), t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(state, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	org := policy.DefaultOrg()
	org.Tenant = tenant
	org.User = core.User{Name: tenant, Display: strings.ToUpper(tenant[:1]) + tenant[1:]}
	org.Defaults.Independents = "all"
	agents := []core.Agent{{Name: "agent", Display: "Agent"}}
	x := &lolad{}
	x.p, e = amp.New(db, state, tenant, policy.New(org, "agent"), amp.WithOrg(func() core.Org { return org }), amp.WithAgents(func() []core.Agent { return agents }))
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range []struct{ n, k string }{{tenant, "human"}, {"agent", "agent"}, {"lolad", "system"}} {
		if _, e = x.p.EnsureIdentity(t.Context(), i.n, i.k); e != nil {
			t.Fatal(e)
		}
	}
	// A long backoff: delivery after reconnect must come from the hub's
	// online signal, not from the outbox timer.
	x.f, e = fed.New(fed.Options{DB: db, StateDir: state, ConfigDir: config, Provider: x.p, Enabled: true, Org: func() core.Org { return org }, Agents: func() []core.Agent { return agents }, Backoff: []time.Duration{time.Hour}})
	if e != nil {
		t.Fatal(e)
	}
	signer := Signer{PublicKey: x.f.ProviderPublicKey(), Sign: x.f.ProviderSign}
	u, e := Register(t.Context(), nil, h.url, tenant, org.User.Display, h.invite(t), signer)
	if e != nil {
		t.Fatal(e)
	}
	if e = x.f.SetURL(u); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); x.f.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		db.Close()
	})
	x.opts = ClientOptions{HubURL: h.url, Handle: tenant, Signer: signer, Handler: x.f.Handler(), Peers: x.f.PeerURLs,
		OnPeerOnline: func(u string) { _ = x.f.RetryURL(context.Background(), u) }}
	x.connect(t)
	return x
}

func (x *lolad) connect(t *testing.T) {
	t.Helper()
	x.client, x.stop = startClient(t, x.opts)
	eventually(t, "tunnel up", func() bool { return x.client.Status().State == core.HubConnected })
}

func TestTwoLoladsThroughHub(t *testing.T) {
	h := newHub(t)
	milo, jugui := newLolad(t, h, "milo"), newLolad(t, h, "jugui")
	ctx := t.Context()

	invite, e := milo.f.CreateInvite(ctx)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := jugui.f.AcceptInvite(ctx, invite.Code)
	if e != nil {
		t.Fatal("pair through hub", e)
	}
	if conn.URL != h.url+"/p/milo" {
		t.Fatal("peer url", conn.URL)
	}
	cs, _ := milo.f.Connections(ctx)
	if len(cs) != 1 || cs[0].URL != h.url+"/p/jugui" {
		t.Fatal("inviter side", cs)
	}
	if _, e = milo.f.UpdateConnection(ctx, "jugui.lolad.local", core.ConnectionUpdate{Shares: []core.Share{{Local: "agent", Remote: []string{"jugui@jugui.lolad.local"}}}}); e != nil {
		t.Fatal(e)
	}

	m, e := jugui.p.Send(ctx, "jugui", core.Outgoing{To: "agent@milo.lolad.local", Subject: "Hi", Body: "through the hub"})
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, "delivered", func() bool { g, e := jugui.p.Get(ctx, m.ID); return e == nil && g.Delivery == core.DeliveryDelivered })

	// Milo goes offline: the message queues (hub answers 503)...
	milo.stop()
	eventually(t, "offline", func() bool { return !h.srv.Online("milo") })
	m2, e := jugui.p.Send(ctx, "jugui", core.Outgoing{To: "agent@milo.lolad.local", Subject: "Later", Body: "queued"})
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, "queued with error", func() bool {
		cs, _ := jugui.f.Connections(ctx)
		return len(cs) == 1 && cs[0].Queued == 1 && cs[0].LastError != ""
	})
	// ...and goes out as soon as Milo reconnects, despite the 1 h backoff.
	milo.connect(t)
	eventually(t, "delivered after reconnect", func() bool { g, e := jugui.p.Get(ctx, m2.ID); return e == nil && g.Delivery == core.DeliveryDelivered })

	// The reply comes back the same way.
	r, e := milo.p.Send(ctx, "agent", core.Outgoing{To: "jugui@jugui.lolad.local", Subject: "Re: Hi", Body: "hello back", InReplyTo: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, "reply delivered", func() bool { g, e := milo.p.Get(ctx, r.ID); return e == nil && g.Delivery == core.DeliveryDelivered })

	// Directory through the hub.
	contacts, e := jugui.f.RemoteContacts(ctx, "jugui")
	if e != nil || len(contacts) == 0 {
		t.Fatal("directory", contacts, e)
	}
}

func TestClientIPTrustedProxies(t *testing.T) {
	trusted, err := ParsePrefixes("10.0.0.0/8, 172.16.0.0/12,192.168.1.7")
	if err != nil || len(trusted) != 3 {
		t.Fatal(trusted, err)
	}
	if _, err := ParsePrefixes("10.0.0.0/33"); err == nil {
		t.Fatal("bad CIDR accepted")
	}
	s := &Server{o: Options{TrustedProxies: trusted}}
	for _, c := range []struct{ remote, xff, want string }{
		{"127.0.0.1:5000", "1.1.1.1, 2.2.2.2", "2.2.2.2"}, // loopback always trusted
		{"10.0.1.5:5000", "9.9.9.9, 3.3.3.3", "3.3.3.3"},  // Traefik on the Docker network
		{"[::ffff:10.0.1.5]:5000", "4.4.4.4", "4.4.4.4"},  // mapped IPv4
		{"192.168.1.7:5000", "5.5.5.5", "5.5.5.5"},        // bare IP entry
		{"192.168.1.8:5000", "5.5.5.5", "192.168.1.8"},    // not listed
		{"8.8.8.8:5000", "6.6.6.6", "8.8.8.8"},            // direct caller: header ignored
		{"10.0.1.5:5000", "not-an-ip", "10.0.1.5"},        // junk header
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		r.Header.Set("X-Forwarded-For", c.xff)
		if got := s.clientIP(r); got != c.want {
			t.Errorf("%s %q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
	// Without the option only loopback is trusted.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.1.5:5000"
	r.Header.Set("X-Forwarded-For", "3.3.3.3")
	if got := (&Server{}).clientIP(r); got != "10.0.1.5" {
		t.Fatal("untrusted proxy believed", got)
	}
}
