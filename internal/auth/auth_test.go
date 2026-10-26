package auth

import (
	"context"
	"errors"
	"github.com/CamiloCaceres/lolad/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s, e := New(db)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func issue(t *testing.T, s *Store) (string, Device) {
	t.Helper()
	p, e := s.Pair(t.Context(), "")
	if e != nil {
		t.Fatal(e)
	}
	token, d, e := s.Redeem(t.Context(), p.Code, "Pixel", "android", "1.2.3.4", "")
	if e != nil {
		t.Fatal(e)
	}
	return token, d
}
func TestPairingAtomicAndExpiry(t *testing.T) {
	s := newStore(t)
	p, e := s.Pair(t.Context(), "My phone")
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Code) != 8 {
		t.Fatal("code length")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for range 8 {
		wg.Go(func() {
			token, d, e := s.Redeem(t.Context(), p.Code, "phone", "android", "ip", "")
			if e == nil {
				mu.Lock()
				success++
				mu.Unlock()
				if d.Name != "My phone" || len(token) != 68 {
					t.Error("bad token/device")
				}
			}
		})
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("successful redemptions %d", success)
	}
	p, _ = s.Pair(t.Context(), "")
	s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, _, e = s.Redeem(t.Context(), p.Code, "phone", "android", "ip", ""); !errors.Is(e, ErrCredentials) {
		t.Fatalf("expired: %v", e)
	}
}
func TestTokenStorageAndSessionRevocation(t *testing.T) {
	s := newStore(t)
	token, d := issue(t, s)
	var persisted string
	if e := s.db.QueryRow(`SELECT token_hash FROM auth_devices`).Scan(&persisted); e != nil {
		t.Fatal(e)
	}
	if persisted == token || persisted != hash(token) {
		t.Fatal("plaintext persisted")
	}
	cookie, e := s.NewSession(t.Context(), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Session(t.Context(), cookie, "ip"); e != nil {
		t.Fatal(e)
	}
	ctx, done := s.track(t.Context(), d.ID)
	defer done()
	if e = s.Revoke(t.Context(), d.ID); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("active session not cancelled")
	}
	if _, e = s.Token(t.Context(), token, "ip"); !errors.Is(e, ErrCredentials) {
		t.Fatal("revoked token accepted")
	}
	if _, e = s.Session(t.Context(), cookie, "ip"); !errors.Is(e, ErrCredentials) {
		t.Fatal("revoked session accepted")
	}
	devices, e := s.Devices(t.Context())
	if e != nil || len(devices) != 1 || !devices[0].Revoked {
		t.Fatalf("devices %v %v", devices, e)
	}
}
func TestSessionSlidingAndExpiry(t *testing.T) {
	s := newStore(t)
	_, d := issue(t, s)
	now := time.Now()
	s.now = func() time.Time { return now }
	cookie, _ := s.NewSession(t.Context(), d.ID)
	now = now.Add(29 * 24 * time.Hour)
	if _, e := s.Session(t.Context(), cookie, "ip"); e != nil {
		t.Fatal(e)
	}
	now = now.Add(29 * 24 * time.Hour)
	if _, e := s.Session(t.Context(), cookie, "ip"); e != nil {
		t.Fatal("session didn't slide")
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, e := s.Session(t.Context(), cookie, "ip"); !errors.Is(e, ErrCredentials) {
		t.Fatal("expired session accepted")
	}
}
func TestRemoteMiddlewareMatrix(t *testing.T) {
	s := newStore(t)
	token, d := issue(t, s)
	cookie, _ := s.NewSession(t.Context(), d.ID)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	remote := s.Remote(next, []string{"owner@example.com"})
	tests := []struct {
		name, path, bearer, cookie, login, ip string
		want                                  int
	}{
		{name: "none API", path: "/api/agents", want: 401}, {name: "browser redirect", path: "/", want: 303},
		{name: "bearer", path: "/api/agents", bearer: token, want: 204}, {name: "cookie", path: "/", cookie: cookie, want: 204},
		{name: "owner Serve", path: "/", login: "owner@example.com", ip: "127.0.0.1:123", want: 204},
		{name: "spoof from LAN", path: "/", login: "owner@example.com", ip: "100.64.0.1:123", want: 303},
		{name: "shared in device", path: "/api/agents", login: "brother@example.com", want: 401},
		{name: "bad bearer no fallback", path: "/api/agents", bearer: "bad", cookie: cookie, login: "owner@example.com", want: 401},
		{name: "AMP", path: "/v1/messages/pending", bearer: token, want: 404}, {name: "harness hook", path: "/api/hook", bearer: token, want: 404},
		{name: "health", path: "/healthz", want: 204}, {name: "version", path: "/api/version", want: 204},
		{name: "login", path: "/login", want: 204}, {name: "static", path: "/static/app.css", want: 204}, {name: "download", path: "/download/lolad.apk", want: 204},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			r.RemoteAddr = tt.ip
			if r.RemoteAddr == "" {
				r.RemoteAddr = "127.0.0.1:" + strings.Repeat("1", i%4+1)
			}
			if tt.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: tt.cookie})
			}
			r.Header.Set("Tailscale-User-Login", tt.login)
			w := httptest.NewRecorder()
			remote.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
			for _, c := range w.Result().Cookies() {
				if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != int(SessionLifetime.Seconds()) {
					t.Fatal("unsafe cookie")
				}
			}
		})
	}
	// The local handler does not inspect or exchange a spoofed Tailscale header.
	r := httptest.NewRequest("GET", "/api/agents", nil)
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	w := httptest.NewRecorder()
	next.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("local header altered auth")
	}
}
func TestFailureRateLimits(t *testing.T) {
	s := newStore(t)
	h := s.Remote(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), nil)
	for i := range 11 {
		r := httptest.NewRequest("GET", "/api/agents", nil)
		r.RemoteAddr = "127.0.0.2:1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d got %d", i, w.Code)
		}
	}
	s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	_, _, e := s.PairRequest(context.Background(), "bad", "phone", "android", "127.0.0.2")
	if errors.Is(e, ErrRateLimit) {
		t.Fatal("limit never expires")
	}
	for range 10 {
		_, _, _ = s.PairRequest(t.Context(), "bad", "phone", "android", "other")
	}
	if _, _, e := s.PairRequest(t.Context(), "bad", "phone", "android", "other"); !errors.Is(e, ErrRateLimit) {
		t.Fatal("pair not limited")
	}
	s.failed("login:owner")
	for range 9 {
		s.failed("login:owner")
	}
	if !s.blocked("login:owner") {
		t.Fatal("login not limited")
	}
}
