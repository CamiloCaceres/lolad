package hub

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "test-admin-secret-0123456789abcdef"

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// newAdminHub is newHub with the console on and logs captured.
func newAdminHub(t *testing.T, token string) (*testHub, *syncBuf) {
	t.Helper()
	st, e := OpenStore(filepath.Join(t.TempDir(), "hub.db"))
	if e != nil {
		t.Fatal(e)
	}
	logs := &syncBuf{}
	h := &testHub{store: st}
	var handler http.Handler
	h.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	h.url = h.ts.URL
	h.srv, e = NewServer(Options{Store: st, PublicURL: h.url, AdminToken: token, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	if e != nil {
		t.Fatal(e)
	}
	handler = h.srv.Handler()
	t.Cleanup(func() { h.srv.closeAll(); h.ts.Close(); st.Close() })
	return h, logs
}

// noRedirect is a client that shows redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type areq struct {
	method, path, body, bearer, cookie, csrf, ctype string
	header                                          map[string]string
}

func (h *testHub) do(t *testing.T, c areq) (*http.Response, string) {
	t.Helper()
	if c.method == "" {
		c.method = "GET"
	}
	req, _ := http.NewRequest(c.method, h.url+c.path, strings.NewReader(c.body))
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.cookie})
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.ctype != "" {
		req.Header.Set("Content-Type", c.ctype)
	}
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	res, e := noRedirect.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestConsoleOffWithoutToken(t *testing.T) {
	h := newHub(t)
	for _, p := range []string{"/console", "/console/", "/console/signin", "/console/people", "/console/static/console.css", "/v1/hub/admin/status", "/v1/hub/admin/accounts"} {
		res, _ := h.do(t, areq{path: p, bearer: testToken})
		if res.StatusCode != 404 {
			t.Errorf("%s: %d, want 404 with the console off", p, res.StatusCode)
		}
	}
	res, _ := h.do(t, areq{method: "POST", path: "/console/signin", body: "token=" + testToken, ctype: "application/x-www-form-urlencoded"})
	if res.StatusCode != 404 {
		t.Errorf("POST signin: %d", res.StatusCode)
	}
	if _, e := NewServer(Options{Store: h.store, PublicURL: h.url, AdminToken: "short"}); e == nil {
		t.Error("short admin token accepted")
	}
}

func TestAdminAPI(t *testing.T) {
	h, _ := newAdminHub(t, testToken)

	if res, _ := h.do(t, areq{path: "/v1/hub/admin/status"}); res.StatusCode != 401 {
		t.Fatal("no auth:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{path: "/v1/hub/admin/status", bearer: "wrong"}); res.StatusCode != 401 {
		t.Fatal("wrong token:", res.StatusCode)
	}

	// Create an invite: the code is in this response only.
	res, body := h.do(t, areq{method: "POST", path: "/v1/hub/admin/invites", bearer: testToken, body: `{"note":"Ana, from the studio"}`})
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode, body)
	}
	var made struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
		Note string `json:"note"`
	}
	json.Unmarshal([]byte(body), &made)
	if !strings.HasPrefix(made.Code, "hubinv_") || made.Note != "Ana, from the studio" || made.ID == 0 {
		t.Fatal(body)
	}
	_, list := h.do(t, areq{path: "/v1/hub/admin/invites", bearer: testToken})
	if strings.Contains(list, made.Code) || strings.Contains(list, hashCode(made.Code)) || !strings.Contains(list, "Ana, from the studio") {
		t.Fatal("invite list leaks the code or misses the invite:", list)
	}

	// A second invite, cancelled: gone, and cannot be used.
	_, body = h.do(t, areq{method: "POST", path: "/v1/hub/admin/invites", bearer: testToken, body: `{"note":"Dad","ttl":"48h"}`})
	var other struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	json.Unmarshal([]byte(body), &other)
	if res, _ := h.do(t, areq{method: "DELETE", path: "/v1/hub/admin/invites/" + itoa(other.ID), bearer: testToken}); res.StatusCode != 200 {
		t.Fatal("cancel:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "DELETE", path: "/v1/hub/admin/invites/" + itoa(other.ID), bearer: testToken}); res.StatusCode != 404 {
		t.Fatal("cancel twice:", res.StatusCode)
	}
	if _, e := Register(t.Context(), nil, h.url, "dad", "", other.Code, newSigner(t)); !isCode(e, "invalid_invite") {
		t.Fatal("cancelled invite still works:", e)
	}

	// Join with the first invite and connect.
	s := newSigner(t)
	if _, e := Register(t.Context(), nil, h.url, "ana", "Ana", made.Code, s); e != nil {
		t.Fatal(e)
	}
	srv := &seen{}
	startClient(t, ClientOptions{HubURL: h.url, Handle: "ana", Signer: s, Handler: srv})
	eventually(t, "tunnel", func() bool { return h.srv.Online("ana") })

	// A used invite cannot be cancelled.
	if res, _ := h.do(t, areq{method: "DELETE", path: "/v1/hub/admin/invites/" + itoa(made.ID), bearer: testToken}); res.StatusCode != 404 {
		t.Fatal("cancelled a used invite:", res.StatusCode)
	}
	_, list = h.do(t, areq{path: "/v1/hub/admin/invites", bearer: testToken})
	if !strings.Contains(list, `"used_by":"ana"`) {
		t.Fatal(list)
	}

	// Traffic: one carried request (with a query that must not be recorded).
	r, e := http.Post(h.url+"/p/ana/v1/federation/deliver?k=private-query", "application/json", strings.NewReader("{}"))
	if e != nil || r.StatusCode != 202 {
		t.Fatal(e, r)
	}
	r.Body.Close()

	_, body = h.do(t, areq{path: "/v1/hub/admin/accounts", bearer: testToken})
	var acc struct{ Accounts []Person }
	json.Unmarshal([]byte(body), &acc)
	if len(acc.Accounts) != 1 || !acc.Accounts[0].Online || acc.Accounts[0].Note != "Ana, from the studio" || acc.Accounts[0].CarriedToday != 1 {
		t.Fatal(body)
	}
	_, body = h.do(t, areq{path: "/v1/hub/admin/logs?limit=5", bearer: testToken})
	if !strings.Contains(body, `"path":"/p/ana/v1/federation/deliver"`) || strings.Contains(body, "private-query") || !strings.Contains(body, `"path":"/v1/hub/register"`) {
		t.Fatal(body)
	}
	_, body = h.do(t, areq{path: "/v1/hub/admin/status", bearer: testToken})
	var st Status
	json.Unmarshal([]byte(body), &st)
	if st.Accounts != 1 || st.Online != 1 || st.Traffic.Carried24h != 1 || len(st.Traffic.Hours) != 24 || st.Version != Version {
		t.Fatal(body)
	}

	// Disable closes the tunnel at once and refuses requests; enable undoes it.
	if res, body := h.do(t, areq{method: "POST", path: "/v1/hub/admin/accounts/ana/disable", bearer: testToken}); res.StatusCode != 200 || !strings.Contains(body, `"disabled":true`) {
		t.Fatal(res.StatusCode, body)
	}
	eventually(t, "tunnel closed", func() bool { return !h.srv.Online("ana") })
	r, _ = http.Post(h.url+"/p/ana/v1/federation/deliver", "application/json", strings.NewReader("{}"))
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("disabled handle:", r.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/v1/hub/admin/accounts/ana/enable", bearer: testToken}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	eventually(t, "reconnect", func() bool { return h.srv.Online("ana") })
	if res, _ := h.do(t, areq{method: "POST", path: "/v1/hub/admin/accounts/nobody/disable", bearer: testToken}); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := h.do(t, areq{path: "/v1/hub/admin/nothing", bearer: testToken}); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
}

func TestAdminSigninRateLimit(t *testing.T) {
	h, _ := newAdminHub(t, testToken)
	for i := range signinFailsPerIP {
		if res, _ := h.do(t, areq{path: "/v1/hub/admin/status", bearer: "wrong"}); res.StatusCode != 401 {
			t.Fatal(i, res.StatusCode)
		}
	}
	// Locked out, even with the right secret, on the API and the console.
	if res, _ := h.do(t, areq{path: "/v1/hub/admin/status", bearer: testToken}); res.StatusCode != 429 {
		t.Fatal("api:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/signin", body: "token=" + testToken, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 429 {
		t.Fatal("console:", res.StatusCode)
	}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func signIn(t *testing.T, h *testHub) (cookie string) {
	t.Helper()
	res, _ := h.do(t, areq{method: "POST", path: "/console/signin", body: "token=" + url.QueryEscape(testToken), ctype: "application/x-www-form-urlencoded"})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/console/people" {
		t.Fatal("sign-in:", res.StatusCode, res.Header.Get("Location"))
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
				t.Fatalf("session cookie flags: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

func TestConsole(t *testing.T) {
	h, _ := newAdminHub(t, testToken)

	// Signed out: pages send you to sign-in; posts are refused.
	res, _ := h.do(t, areq{path: "/console"})
	if res.StatusCode != 303 || res.Header.Get("Location") != "/console/signin" {
		t.Fatal(res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/invites", body: "note=x", ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	res, body := h.do(t, areq{path: "/console/signin"})
	if res.StatusCode != 200 || !strings.Contains(body, `type="password"`) || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal(res.StatusCode, body)
	}
	if res, body := h.do(t, areq{method: "POST", path: "/console/signin", body: "token=nope", ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 401 || len(res.Cookies()) != 0 || !strings.Contains(body, "not this hub") {
		t.Fatal(res.StatusCode, body)
	}

	cookie := signIn(t, h)
	for _, p := range []string{"/console/people", "/console/invites", "/console/health", "/console/people?show=on&q=x"} {
		res, body := h.do(t, areq{path: p, cookie: cookie})
		if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" || !strings.Contains(body, "lolad hub") {
			t.Fatal(p, res.StatusCode)
		}
	}
	if res, body := h.do(t, areq{path: "/console/static/console.css"}); res.StatusCode != 200 || !strings.Contains(body, "Sora") {
		t.Fatal("static:", res.StatusCode)
	}
	_, page := h.do(t, areq{path: "/console/invites", cookie: cookie})
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no csrf field")
	}
	csrf := m[1]

	// Forms need the CSRF token, and cross-site posts are refused outright.
	if res, _ := h.do(t, areq{method: "POST", path: "/console/invites", cookie: cookie, body: "note=Ana", ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 403 {
		t.Fatal("no csrf:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/invites", cookie: cookie, body: "note=Ana&csrf=" + csrf, ctype: "application/x-www-form-urlencoded", header: map[string]string{"Sec-Fetch-Site": "cross-site"}}); res.StatusCode != 403 {
		t.Fatal("cross-site:", res.StatusCode)
	}
	res, body = h.do(t, areq{method: "POST", path: "/console/invites", cookie: cookie, body: "note=Ana&csrf=" + csrf, ctype: "application/x-www-form-urlencoded"})
	code := regexp.MustCompile(`hubinv_[A-Za-z0-9_-]+`).FindString(body)
	if res.StatusCode != 200 || code == "" || !strings.Contains(body, "Invite for Ana is ready") || !strings.Contains(body, "this hub’s address, "+h.url) {
		t.Fatal(res.StatusCode, body)
	}
	// Shown once: the page afterwards does not have it.
	if _, body := h.do(t, areq{path: "/console/invites", cookie: cookie}); strings.Contains(body, code) || !strings.Contains(body, "Ana") {
		t.Fatal("code shown again")
	}

	// Join, then turn off and back on from the People page.
	s := newSigner(t)
	if _, e := Register(t.Context(), nil, h.url, "ana", "", code, s); e != nil {
		t.Fatal(e)
	}
	startClient(t, ClientOptions{HubURL: h.url, Handle: "ana", Signer: s, Handler: &seen{}})
	eventually(t, "tunnel", func() bool { return h.srv.Online("ana") })
	if _, body := h.do(t, areq{path: "/console/people", cookie: cookie}); !strings.Contains(body, "Connected") || !strings.Contains(body, "1 person has joined this hub. 1 is connected right now.") {
		t.Fatal(body)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/people/ana/disable", cookie: cookie, body: "csrf=" + csrf, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}
	eventually(t, "tunnel closed", func() bool { return !h.srv.Online("ana") })
	if _, body := h.do(t, areq{path: "/console/people?show=disabled", cookie: cookie}); !strings.Contains(body, "Turn back on") {
		t.Fatal("not shown as turned off")
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/console/people/ana/enable", cookie: cookie, body: "csrf=" + csrf, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}

	// The console session also works for the JSON API, with CSRF on writes.
	if res, _ := h.do(t, areq{path: "/v1/hub/admin/status", cookie: cookie}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/v1/hub/admin/invites", cookie: cookie, body: "{}"}); res.StatusCode != 403 {
		t.Fatal("api write without csrf:", res.StatusCode)
	}
	if res, _ := h.do(t, areq{method: "POST", path: "/v1/hub/admin/invites", cookie: cookie, csrf: csrf, body: "{}"}); res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}

	// Signing out ends the session.
	if res, _ := h.do(t, areq{method: "POST", path: "/console/signout", cookie: cookie, body: "csrf=" + csrf, ctype: "application/x-www-form-urlencoded"}); res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := h.do(t, areq{path: "/console/people", cookie: cookie}); res.StatusCode != 303 {
		t.Fatal("session survived sign-out:", res.StatusCode)
	}
}

func TestSessionExpires(t *testing.T) {
	h, _ := newAdminHub(t, testToken)
	now := time.Now()
	h.srv.o.Clock = func() time.Time { return now }
	cookie := signIn(t, h)
	if res, _ := h.do(t, areq{path: "/console/people", cookie: cookie}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	now = now.Add(sessionTTL + time.Second)
	if res, _ := h.do(t, areq{path: "/console/people", cookie: cookie}); res.StatusCode != 303 {
		t.Fatal("expired session accepted:", res.StatusCode)
	}
}

// The hub logs metadata only: never the admin secret, an invite code, a
// session, a body or a query string. The request log the console shows
// follows the same rule.
func TestNoSecretsInLogs(t *testing.T) {
	h, logs := newAdminHub(t, testToken)
	cookie := signIn(t, h)
	h.do(t, areq{path: "/v1/hub/admin/status", bearer: "wrong-secret-attempt"})
	_, body := h.do(t, areq{method: "POST", path: "/v1/hub/admin/invites", bearer: testToken, body: `{"note":"n"}`})
	var made struct{ Code string }
	json.Unmarshal([]byte(body), &made)
	_, page := h.do(t, areq{path: "/console/invites", cookie: cookie})
	csrf := csrfField.FindStringSubmatch(page)[1]
	_, page = h.do(t, areq{method: "POST", path: "/console/invites", cookie: cookie, body: "note=m&csrf=" + csrf, ctype: "application/x-www-form-urlencoded"})
	code2 := regexp.MustCompile(`hubinv_[A-Za-z0-9_-]+`).FindString(page)
	if made.Code == "" || code2 == "" {
		t.Fatal("no codes made")
	}
	s := newSigner(t)
	if _, e := Register(t.Context(), nil, h.url, "ana", "", made.Code, s); e != nil {
		t.Fatal(e)
	}
	Register(t.Context(), nil, h.url, "bob", "", code2+"x", newSigner(t)) // a failed attempt
	startClient(t, ClientOptions{HubURL: h.url, Handle: "ana", Signer: s, Handler: &seen{}})
	eventually(t, "tunnel", func() bool { return h.srv.Online("ana") })
	r, _ := http.Post(h.url+"/p/ana/v1/federation/deliver?token=query-secret", "application/json", strings.NewReader(`{"body":"private message body"}`))
	r.Body.Close()
	h.srv.Sweep(t.Context())

	_, reqlog := h.do(t, areq{path: "/v1/hub/admin/logs", bearer: testToken})
	_, health := h.do(t, areq{path: "/console/health", cookie: cookie})
	out := logs.String()
	if !strings.Contains(out, `"msg":"forward"`) || !strings.Contains(out, `"msg":"admin signed in"`) {
		t.Fatal("expected log lines missing:", out)
	}
	for name, text := range map[string]string{"log": out, "request log": reqlog, "health page": health} {
		for _, secret := range []string{testToken, "wrong-secret-attempt", made.Code, code2, cookie, csrf, "query-secret", "private message body", "hubinv_"} {
			if secret == csrf && name == "health page" {
				continue // the page's own forms carry it
			}
			if strings.Contains(text, secret) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
	}
}

func TestTrafficSummary(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	tr := newTraffic(func() time.Time { return now })
	tr.record(Request{Time: now.Add(-30 * time.Hour), Handle: "old", Path: "/p/old/v1/federation/deliver", Status: 200})
	tr.record(Request{Time: now.Add(-2 * time.Hour), Handle: "ana", Path: "/p/ana/v1/federation/deliver", Status: 200, Ms: 40})
	tr.record(Request{Time: now, Handle: "ana", Path: "/p/ana/v1/federation/deliver", Status: 200, Ms: 80})
	tr.record(Request{Time: now, Handle: "bob", Path: "/p/bob/v1/federation/deliver", Status: 503})
	tr.record(Request{Time: now, Path: "/v1/hub/register", Status: 429, ip: "198.51.100.7"})
	tr.record(Request{Time: now, Path: "/v1/hub/register", Status: 429, ip: "198.51.100.7"})
	s := tr.summary()
	if s.Carried24h != 2 || s.LastHour != 1 || s.Limited24h != 2 || s.LimitedIPs != 1 || s.TypicalMs != 80 || s.CarriedToday["ana"] != 2 || s.CarriedToday["old"] != 0 || len(s.Hours) != 24 {
		t.Fatalf("%+v", s)
	}
	if got := tr.recent(2); len(got) != 2 || got[0].Status != 429 {
		t.Fatal(got)
	}
	for range logKeep + 10 {
		tr.record(Request{Time: now, Handle: "ana", Status: 200})
	}
	if len(tr.recent(0)) != logKeep {
		t.Fatal("ring not capped")
	}
	b, _ := json.Marshal(tr.recent(1))
	if strings.Contains(string(b), "198.51") {
		t.Fatal("ip exposed")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
