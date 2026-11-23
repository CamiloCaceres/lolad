package hub

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newHubWith is newHub with options adjusted and a settable clock.
func newHubWith(t *testing.T, mut func(*Options)) (*testHub, *fakeClock) {
	t.Helper()
	st, e := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if e != nil {
		t.Fatal(e)
	}
	clk := &fakeClock{t: time.Now()}
	h := &testHub{store: st}
	var handler http.Handler
	h.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	h.url = h.ts.URL
	o := Options{Store: st, PublicURL: h.url, Clock: clk.now}
	if mut != nil {
		mut(&o)
	}
	h.srv, e = NewServer(o)
	if e != nil {
		t.Fatal(e)
	}
	handler = h.srv.Handler()
	t.Cleanup(func() { h.srv.closeAll(); h.ts.Close(); st.Close() })
	return h, clk
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// signup posts a signed open sign-up as if from ip (behind the loopback
// proxy, which the hub trusts for X-Forwarded-For).
func signup(t *testing.T, h *testHub, s Signer, handle, ip, hubField string) (int, map[string]string) {
	t.Helper()
	body, _ := json.Marshal(registerBody{Hub: hubField, Handle: handle, Display: handle, PublicKey: s.PublicKey})
	req, _ := http.NewRequest("POST", h.url+"/v1/hub/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	signRequest(req, handle, body, h.srv.o.Clock(), s.Sign)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var v map[string]string
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &v)
	return res.StatusCode, v
}

func info(t *testing.T, h *testHub) string {
	t.Helper()
	v, e := Info(t.Context(), nil, h.url)
	if e != nil {
		t.Fatal(e)
	}
	return v.Signup
}

func TestOpenSignup(t *testing.T) {
	h, _ := newHubWith(t, nil)
	if info(t, h) != SignupOpen {
		t.Fatal("default mode is not open")
	}
	s := newSigner(t)
	u, e := Register(t.Context(), nil, h.url, "ana", "Ana", "", s)
	if e != nil || u != h.url+"/p/ana" {
		t.Fatal(u, e)
	}
	a, _ := h.store.Account(t.Context(), "ana")
	if a.JoinedVia != SignupOpen || !a.FirstTunnel.IsZero() {
		t.Fatalf("%+v", a)
	}
	// The same key again is idempotent and does not count as a new sign-up.
	for range 5 {
		if _, e = Register(t.Context(), nil, h.url, "ana", "Ana", "", s); e != nil {
			t.Fatal(e)
		}
	}
	// The same key under another handle: refused, naming the one it holds.
	var he *Error
	if _, e = Register(t.Context(), nil, h.url, "ana-two", "", "", s); !errors.As(e, &he) || he.Code != "key_registered" || he.Handle != "ana" {
		t.Fatal(e)
	}
	// A taken handle comes with a suggestion.
	if _, e = Register(t.Context(), nil, h.url, "ana", "", "", newSigner(t)); !errors.As(e, &he) || he.Code != "name_taken" || he.Suggestion != "ana-2" {
		t.Fatal(e)
	}
	// Operator codes still work in open mode, and are consumed.
	code := h.invite(t)
	if _, e = Register(t.Context(), nil, h.url, "ben", "", code, newSigner(t)); e != nil {
		t.Fatal(e)
	}
	if b, _ := h.store.Account(t.Context(), "ben"); b.JoinedVia != SignupInvite {
		t.Fatal(b.JoinedVia)
	}
}

func TestSignupProofAndHubBinding(t *testing.T) {
	h, _ := newHubWith(t, nil)
	s := newSigner(t)
	// No code and no hub field: not bound to this hub, refused.
	if st, v := signup(t, h, s, "ana", "", ""); st != 400 || v["error"] != "wrong_hub" {
		t.Fatal(st, v)
	}
	// Signed for another hub: refused (a replay from elsewhere).
	if st, v := signup(t, h, s, "ana", "", "https://other.example.com"); st != 400 || v["error"] != "wrong_hub" {
		t.Fatal(st, v)
	}
	// Signed by another key than the one in the body: refused.
	body, _ := json.Marshal(registerBody{Hub: h.url, Handle: "ana", PublicKey: s.PublicKey})
	req, _ := http.NewRequest("POST", h.url+"/v1/hub/register", bytes.NewReader(body))
	signRequest(req, "ana", body, time.Now(), newSigner(t).Sign)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("foreign signature:", res.StatusCode)
	}
	if st, _ := signup(t, h, s, "ana", "", h.url); st != 200 {
		t.Fatal(st)
	}
}

func TestInviteMode(t *testing.T) {
	h, _ := newHubWith(t, func(o *Options) { o.Signup = SignupInvite })
	if info(t, h) != SignupInvite {
		t.Fatal("mode")
	}
	var he *Error
	if _, e := Register(t.Context(), nil, h.url, "ana", "", "", newSigner(t)); !errors.As(e, &he) || he.Code != "invalid_invite" {
		t.Fatal(e)
	}
	// The operator opens it (console or CLI setting): it applies at once.
	if e := h.srv.SetSignupMode(t.Context(), SignupOpen); e != nil {
		t.Fatal(e)
	}
	if info(t, h) != SignupOpen {
		t.Fatal("override not applied")
	}
	if _, e := Register(t.Context(), nil, h.url, "ana", "", "", newSigner(t)); e != nil {
		t.Fatal(e)
	}
	if _, e := NewServer(Options{Store: h.store, PublicURL: h.url, Signup: "anyone"}); e == nil {
		t.Fatal("bad mode accepted")
	}
}

func TestInvalidHandleSuggestion(t *testing.T) {
	h, _ := newHubWith(t, nil)
	for in, want := range map[string]string{"admin": "admin-2", "support": "support-2", "Ana": "ana", "a": "a00", "lolad": "lolad-2"} {
		st, v := signup(t, h, newSigner(t), in, "", h.url)
		if st != 400 || v["error"] != "invalid_handle" || v["suggestion"] != want {
			t.Errorf("%q: %d %v", in, st, v)
		}
	}
}

func TestSignupLimitsPerSource(t *testing.T) {
	h, _ := newHubWith(t, func(o *Options) { o.Limits = &Limits{SignupsPerIP: 2, SignupsPerNet: 3} })
	ok := func(handle, ip string) int { st, _ := signup(t, h, newSigner(t), handle, ip, h.url); return st }
	if ok("a01", "198.51.100.7") != 200 || ok("a02", "198.51.100.7") != 200 {
		t.Fatal("first two")
	}
	if st := ok("a03", "198.51.100.7"); st != 429 {
		t.Fatal("per IP:", st)
	}
	if ok("a04", "198.51.100.8") != 200 { // same /24, third in the net
		t.Fatal("net third")
	}
	if st := ok("a05", "198.51.100.9"); st != 429 {
		t.Fatal("per /24:", st)
	}
	if ok("a06", "203.0.113.1") != 200 {
		t.Fatal("other net")
	}
	// IPv6 groups by /56.
	if ok("b01", "2001:db8:0:1::1") != 200 || ok("b02", "2001:db8:0:2::1") != 200 || ok("b03", "2001:db8:0:3::1") != 200 {
		t.Fatal("v6")
	}
	if st := ok("b04", "2001:db8:0:4::1"); st != 429 {
		t.Fatal("per /56:", st)
	}
}

func TestSignupCaps(t *testing.T) {
	h, clk := newHubWith(t, func(o *Options) { o.Limits = &Limits{SignupsHourly: 2, SignupsDaily: 3, MaxAccounts: 4} })
	reg := func(handle string) int { st, _ := signup(t, h, newSigner(t), handle, "", h.url); return st }
	// Operator invites do not count toward the sign-up caps, only the total.
	if _, e := Register(t.Context(), nil, h.url, "inv", "", h.invite(t), newSigner(t)); e != nil {
		t.Fatal(e)
	}
	if reg("a01") != 200 || reg("a02") != 200 {
		t.Fatal("first")
	}
	if st := reg("a03"); st != 503 || info(t, h) != SignupClosed {
		t.Fatal("hourly:", st)
	}
	clk.add(61 * time.Minute)
	if reg("a03") != 200 {
		t.Fatal("next hour")
	}
	if st := reg("a04"); st != 503 {
		t.Fatal("daily:", st)
	}
	clk.add(25 * time.Hour)
	if st := reg("a05"); st != 503 { // 4 accounts: full
		t.Fatal("total:", st)
	}
}

func TestBlockAndReap(t *testing.T) {
	h, clk := newHubWith(t, nil)
	ctx := t.Context()
	s := newSigner(t)
	if _, e := Register(ctx, nil, h.url, "spam", "", "", s); e != nil {
		t.Fatal(e)
	}
	if e := h.store.Block(ctx, "spam"); e != nil {
		t.Fatal(e)
	}
	var he *Error
	if _, e := Register(ctx, nil, h.url, "spam-again", "", "", s); !errors.As(e, &he) || he.Code != "signup_refused" {
		t.Fatal(e)
	}
	if e := h.store.Block(ctx, "nobody"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}

	// Reaping: open sign-ups that never connect go after 72 h; one that
	// connected once stays, and so do invited accounts.
	idle, used := newSigner(t), newSigner(t)
	Register(ctx, nil, h.url, "idle", "", "", idle)
	Register(ctx, nil, h.url, "used", "", "", used)
	Register(ctx, nil, h.url, "invited", "", h.invite(t), newSigner(t))
	_, stop := startClient(t, ClientOptions{HubURL: h.url, Handle: "used", Signer: used, Handler: &seen{}})
	eventually(t, "tunnel", func() bool { return h.srv.Online("used") })
	stop()
	clk.add(71 * time.Hour)
	h.srv.Sweep(ctx)
	if _, e := h.store.Account(ctx, "idle"); e != nil {
		t.Fatal("reaped too early")
	}
	clk.add(2 * time.Hour)
	h.srv.Sweep(ctx)
	if _, e := h.store.Account(ctx, "idle"); !errors.Is(e, ErrNotFound) {
		t.Fatal("not reaped")
	}
	for _, keep := range []string{"used", "invited"} {
		if _, e := h.store.Account(ctx, keep); e != nil {
			t.Fatal(keep, "reaped")
		}
	}
}

// A database made by the hub before open sign-up opens, gains the new
// columns, and its accounts are neither reaped nor misreported.
func TestMigrateOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, e := sql.Open("sqlite", "file:"+path)
	if e != nil {
		t.Fatal(e)
	}
	old := `CREATE TABLE hub_accounts(handle TEXT PRIMARY KEY, public_key TEXT NOT NULL UNIQUE, display TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, last_seen INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE hub_invites(code_hash TEXT PRIMARY KEY, note TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_by TEXT, used_at INTEGER);
INSERT INTO hub_accounts VALUES('milo','k1','Milo',1000,2000,0),('never','k2','',1000,0,0);`
	if _, e = db.Exec(old); e != nil {
		t.Fatal(e)
	}
	db.Close()
	for range 2 { // twice: idempotent
		st, e := OpenStore(path)
		if e != nil {
			t.Fatal(e)
		}
		list, e := st.Accounts(t.Context())
		if e != nil || len(list) != 2 {
			t.Fatal(list, e)
		}
		for _, a := range list {
			if a.JoinedVia != SignupInvite {
				t.Fatal(a)
			}
			if a.Handle == "milo" && a.FirstTunnel.Unix() != 2000 {
				t.Fatal("first_tunnel not backfilled", a)
			}
		}
		if gone, e := st.reap(t.Context(), time.Nanosecond); e != nil || len(gone) != 0 {
			t.Fatal("old accounts reaped", gone, e)
		}
		st.Close()
	}
}

func TestBytesPerDayCap(t *testing.T) {
	h, _ := newHubWith(t, func(o *Options) { l := DefaultLimits(); l.BytesPerDay = 90; o.Limits = &l })
	s := newSigner(t)
	Register(t.Context(), nil, h.url, "ana", "", "", s)
	startClient(t, ClientOptions{HubURL: h.url, Handle: "ana", Signer: s, Handler: &seen{}})
	eventually(t, "tunnel", func() bool { return h.srv.Online("ana") })
	post := func() int {
		r, e := http.Post(h.url+"/p/ana/v1/federation/deliver", "application/json", strings.NewReader(strings.Repeat("x", 80)))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if st := post(); st != 202 {
		t.Fatal(st)
	}
	if st := post(); st != 429 { // 80 sent + the 11-byte reply = 91, over the 90 cap
		t.Fatal("cap:", st)
	}
}

func TestAdminSignupControls(t *testing.T) {
	h, _ := newAdminHub(t, testToken)
	if _, e := Register(t.Context(), nil, h.url, "ana", "", "", newSigner(t)); e != nil {
		t.Fatal(e)
	}
	_, body := h.do(t, areq{path: "/v1/hub/admin/status", bearer: testToken})
	var st Status
	json.Unmarshal([]byte(body), &st)
	if st.SignupMode != SignupOpen || st.SignupState != SignupOpen || st.Signups24h != 1 || st.AccountsMax != 2000 {
		t.Fatal(body)
	}
	if res, _ := h.do(t, areq{method: "PUT", path: "/v1/hub/admin/signup", bearer: testToken, body: `{"mode":"invite"}`}); res.StatusCode != 200 || info(t, h) != SignupInvite {
		t.Fatal(res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "PUT", path: "/v1/hub/admin/signup", bearer: testToken, body: `{"mode":"wide"}`}); res.StatusCode != 400 {
		t.Fatal(res.StatusCode)
	}
	_, body = h.do(t, areq{path: "/v1/hub/admin/accounts", bearer: testToken})
	if !strings.Contains(body, `"joined_via":"open"`) || !strings.Contains(body, `"bytes_24h"`) {
		t.Fatal(body)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/v1/hub/admin/accounts/ana/block", bearer: testToken}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if _, e := h.store.Account(t.Context(), "ana"); !errors.Is(e, ErrNotFound) {
		t.Fatal("not removed")
	}

	// Console: Health shows the mode and switches it with CSRF.
	cookie := signIn(t, h)
	_, page := h.do(t, areq{path: "/console/health", cookie: cookie})
	if !strings.Contains(page, "Invite only") || !strings.Contains(page, "Open sign-ups") || !strings.Contains(page, "New sign-ups, 24 hours") {
		t.Fatal(page)
	}
	csrf := csrfField.FindStringSubmatch(page)[1]
	if res, _ := h.do(t, areq{method: "POST", path: "/console/signup", cookie: cookie, body: "mode=open", ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 403 {
		t.Fatal("no csrf:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/signup", cookie: cookie, body: "mode=open&csrf=" + csrf, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 303 || info(t, h) != SignupOpen {
		t.Fatal(res.StatusCode)
	}
	// People: a new open sign-up is badged and says how it joined.
	Register(t.Context(), nil, h.url, "ben", "", "", newSigner(t))
	if _, page = h.do(t, areq{path: "/console/people", cookie: cookie}); !strings.Contains(page, ">New<") || !strings.Contains(page, "open sign-up") || !strings.Contains(page, "Remove and block") {
		t.Fatal(page)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/people/ben/block", cookie: cookie, body: "csrf=" + csrf, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}
	if _, e := h.store.Account(t.Context(), "ben"); !errors.Is(e, ErrNotFound) {
		t.Fatal("console block")
	}
}
