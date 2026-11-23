package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The operator side of the hub: a JSON admin API under /v1/hub/admin/ and the
// console under /console (console.go). Both sit behind one admin secret
// (Options.AdminToken). The console signs in with it once and then holds a
// session cookie; API callers send it as a Bearer token. Without the secret
// neither exists (404).
//
// What the operator can see is people, invites and traffic metadata. The hub
// stores no mail, and an invite code is shown once, at creation.

const (
	// MinAdminToken is the shortest admin secret NewServer accepts.
	MinAdminToken = 24

	sessionCookie = "__Host-lolad-hub-console"
	sessionTTL    = 12 * time.Hour
	maxSessions   = 64

	signinFailsPerIP = 5 // per signinWindow
	signinFailsAll   = 100
	signinWindow     = 15 * time.Minute
)

type session struct {
	csrf    string
	expires time.Time
}

type admin struct {
	s         *Server
	tokenHash [32]byte

	mu       sync.Mutex
	sessions map[string]session // sha256(session id) → session

	ui *consoleUI
}

func newAdmin(s *Server, token string) *admin {
	return &admin{s: s, tokenHash: sha256.Sum256([]byte(token)), sessions: map[string]session{}, ui: newConsoleUI()}
}

func (a *admin) routes(m *http.ServeMux) {
	api := func(pattern string, h http.HandlerFunc) { m.Handle(pattern, a.guard(a.apiAuth(h))) }
	api("GET /v1/hub/admin/status", a.apiStatus)
	api("GET /v1/hub/admin/accounts", a.apiAccounts)
	api("POST /v1/hub/admin/accounts/{handle}/disable", a.apiSetDisabled(true))
	api("POST /v1/hub/admin/accounts/{handle}/enable", a.apiSetDisabled(false))
	api("GET /v1/hub/admin/invites", a.apiInvites)
	api("POST /v1/hub/admin/invites", a.apiCreateInvite)
	api("DELETE /v1/hub/admin/invites/{id}", a.apiCancelInvite)
	api("GET /v1/hub/admin/logs", a.apiLogs)
	m.Handle("/v1/hub/admin/", a.guard(a.apiAuth(func(w http.ResponseWriter, r *http.Request) {
		fail(w, 404, "not_found", "Unknown endpoint")
	})))
	a.consoleRoutes(m)
}

// guard adds the security headers and refuses cross-origin writes.
func (a *admin) guard(h http.Handler) http.Handler {
	h = http.NewCrossOriginProtection().Handler(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- secrets

func (a *admin) tokenOK(v string) bool {
	h := sha256.Sum256([]byte(v))
	return subtle.ConstantTimeCompare(h[:], a.tokenHash[:]) == 1
}

// signinAllowed reports whether ip may try the admin secret now.
func (a *admin) signinAllowed(ip string) bool {
	l := a.s.limits
	return l.peek("adminfail:"+ip, signinFailsPerIP, signinWindow) && l.peek("adminfail:*", signinFailsAll, signinWindow)
}

func (a *admin) signinFailed(ip string) {
	a.s.limits.hit("adminfail:"+ip, signinWindow)
	a.s.limits.hit("adminfail:*", signinWindow)
	a.s.o.Logger.Warn("admin sign-in refused", "ip", ip)
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sessionKey(id string) string {
	h := sha256.Sum256([]byte(id))
	return string(h[:])
}

// newSession starts a console session and returns its cookie value.
func (a *admin) newSession() (id string, s session) {
	id = randomToken()
	s = session{csrf: randomToken(), expires: a.s.o.Clock().Add(sessionTTL)}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= maxSessions {
		a.pruneLocked()
		for k := range a.sessions { // still full: drop an arbitrary one
			if len(a.sessions) < maxSessions {
				break
			}
			delete(a.sessions, k)
		}
	}
	a.sessions[sessionKey(id)] = s
	return id, s
}

// session returns the request's console session, if it has a live one.
func (a *admin) session(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionKey(c.Value)]
	if !ok || !a.s.o.Clock().Before(s.expires) {
		return session{}, false
	}
	return s, true
}

func (a *admin) endSession(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, sessionKey(c.Value))
		a.mu.Unlock()
	}
}

func (a *admin) prune() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked()
}

func (a *admin) pruneLocked() {
	now := a.s.o.Clock()
	for k, s := range a.sessions {
		if !now.Before(s.expires) {
			delete(a.sessions, k)
		}
	}
}

func setSessionCookie(w http.ResponseWriter, id string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// csrfOK checks the session's CSRF token on a state-changing request.
func csrfOK(r *http.Request, s session) bool {
	v := r.Header.Get("X-CSRF-Token")
	if v == "" {
		v = r.PostFormValue("csrf")
	}
	return v != "" && subtle.ConstantTimeCompare([]byte(v), []byte(s.csrf)) == 1
}

func safeMethod(m string) bool { return m == http.MethodGet || m == http.MethodHead }

// apiAuth admits a Bearer admin secret, or a console session (with its CSRF
// token on writes, as X-CSRF-Token).
func (a *admin) apiAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			ip := a.s.clientIP(r)
			if !a.signinAllowed(ip) {
				w.Header().Set("Retry-After", strconv.Itoa(int(signinWindow.Seconds())))
				fail(w, 429, "rate_limited", "Too many failed sign-in attempts")
				return
			}
			if !a.tokenOK(v) {
				a.signinFailed(ip)
				fail(w, 401, "unauthorized", "Admin sign-in required")
				return
			}
			h(w, r)
			return
		}
		s, ok := a.session(r)
		if !ok {
			fail(w, 401, "unauthorized", "Admin sign-in required")
			return
		}
		if !safeMethod(r.Method) && !csrfOK(r, s) {
			fail(w, 403, "csrf", "Missing or wrong CSRF token")
			return
		}
		h(w, r)
	})
}

// ---------------------------------------------------------------- operations shared by the API and the console

// Person is an account with what the console shows about it.
type Person struct {
	Account
	Online       bool `json:"online"`
	CarriedToday int  `json:"carried_today"`
}

func (a *admin) people(r *http.Request) ([]Person, TrafficSummary, error) {
	list, err := a.s.store.Accounts(r.Context())
	if err != nil {
		return nil, TrafficSummary{}, err
	}
	sum := a.s.traffic.summary()
	out := make([]Person, 0, len(list))
	for _, acc := range list {
		out = append(out, Person{Account: acc, Online: !acc.Disabled && a.s.Online(acc.Handle), CarriedToday: sum.CarriedToday[acc.Handle]})
	}
	return out, sum, nil
}

// setDisabled turns a handle off or on. Off also closes its tunnel now.
func (a *admin) setDisabled(r *http.Request, handle string, off bool) error {
	if err := a.s.store.SetDisabled(r.Context(), handle, off); err != nil {
		return err
	}
	if off {
		a.s.mu.Lock()
		t := a.s.tunnels[handle]
		a.s.mu.Unlock()
		if t != nil {
			t.conn.Close(CloseDisabled, "handle disabled by the hub operator")
		}
	}
	a.s.o.Logger.Info("admin set account", "handle", handle, "disabled", off)
	return nil
}

// InviteTTL is how long a console-made invite is valid.
const InviteTTL = 7 * 24 * time.Hour

func (a *admin) createInvite(r *http.Request, note string, ttl time.Duration) (Invite, string, error) {
	note = strings.TrimSpace(note)
	if len(note) > 120 {
		note = note[:120]
	}
	if ttl <= 0 {
		ttl = InviteTTL
	}
	v, code, err := a.s.store.CreateInviteID(r.Context(), note, ttl)
	if err == nil {
		a.s.o.Logger.Info("admin created invite", "id", v.ID)
	}
	return v, code, err
}

// Status is the hub's health as the console shows it.
type Status struct {
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	PublicURL string         `json:"public_url"`
	StartedAt time.Time      `json:"started_at"`
	UptimeS   int64          `json:"uptime_s"`
	Accounts  int            `json:"accounts"`
	Online    int            `json:"online"`
	Traffic   TrafficSummary `json:"traffic"`
}

func (a *admin) status(r *http.Request) (Status, error) {
	people, sum, err := a.people(r)
	if err != nil {
		return Status{}, err
	}
	st := Status{Name: "lolad-hub", Version: Version, PublicURL: a.s.o.PublicURL, StartedAt: a.s.started.UTC(),
		UptimeS: int64(a.s.o.Clock().Sub(a.s.started).Seconds()), Accounts: len(people), Traffic: sum}
	for _, p := range people {
		if p.Online {
			st.Online++
		}
	}
	return st, nil
}

// ---------------------------------------------------------------- JSON API

func (a *admin) apiStatus(w http.ResponseWriter, r *http.Request) {
	st, err := a.status(r)
	if err != nil {
		a.internal(w, err)
		return
	}
	reply(w, 200, st)
}

func (a *admin) apiAccounts(w http.ResponseWriter, r *http.Request) {
	people, _, err := a.people(r)
	if err != nil {
		a.internal(w, err)
		return
	}
	reply(w, 200, map[string]any{"accounts": people})
}

func (a *admin) apiSetDisabled(off bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle := r.PathValue("handle")
		err := a.setDisabled(r, handle, off)
		if errors.Is(err, ErrNotFound) {
			fail(w, 404, "not_found", "No such account")
			return
		}
		if err != nil {
			a.internal(w, err)
			return
		}
		acc, err := a.s.store.Account(r.Context(), handle)
		if err != nil {
			a.internal(w, err)
			return
		}
		reply(w, 200, Person{Account: acc, Online: !acc.Disabled && a.s.Online(handle)})
	}
}

func (a *admin) apiInvites(w http.ResponseWriter, r *http.Request) {
	pending, used, err := a.s.store.Invites(r.Context())
	if err != nil {
		a.internal(w, err)
		return
	}
	reply(w, 200, map[string]any{"pending": pending, "used": used})
}

func (a *admin) apiCreateInvite(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Note string `json:"note"`
		TTL  string `json:"ttl"` // Go duration, default 168h
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &b)
	}
	if err != nil {
		fail(w, 400, "invalid_request", "Invalid request")
		return
	}
	var ttl time.Duration
	if b.TTL != "" {
		if ttl, err = time.ParseDuration(b.TTL); err != nil || ttl < time.Minute || ttl > 90*24*time.Hour {
			fail(w, 400, "invalid_request", "ttl must be a duration between 1m and 2160h")
			return
		}
	}
	v, code, err := a.createInvite(r, b.Note, ttl)
	if err != nil {
		a.internal(w, err)
		return
	}
	// The code is in this response and nowhere else, ever.
	reply(w, 201, struct {
		Invite
		Code string `json:"code"`
	}{v, code})
}

func (a *admin) apiCancelInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = a.s.store.CancelInvite(r.Context(), id)
	} else {
		err = ErrNotFound
	}
	if errors.Is(err, ErrNotFound) {
		fail(w, 404, "not_found", "No such invite waiting to be used")
		return
	}
	if err != nil {
		a.internal(w, err)
		return
	}
	a.s.o.Logger.Info("admin cancelled invite", "id", id)
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *admin) apiLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > logKeep {
		n = 100
	}
	reply(w, 200, map[string]any{"requests": a.s.traffic.recent(n)})
}

func (a *admin) internal(w http.ResponseWriter, err error) {
	a.s.o.Logger.Error("admin", "err", err)
	fail(w, 500, "internal_error", "Hub error")
}
