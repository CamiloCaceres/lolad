package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const CookieName = "lolad_session"

type deviceKey struct{}

func WithRequestIdentity(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), deviceKey{}, new(string)))
}
func DeviceID(ctx context.Context) string {
	if id, ok := ctx.Value(deviceKey{}).(*string); ok {
		return *id
	}
	return ""
}
func IP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return h
}
func SetCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(SessionLifetime.Seconds()), Expires: time.Now().Add(SessionLifetime)})
}
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func public(r *http.Request) bool {
	p := r.URL.Path
	return p == "/login" || p == "/healthz" || p == "/api/version" || (p == "/api/pair" && r.Method == "POST") || strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/download/") || (strings.HasPrefix(p, "/hook/") && r.Method == "POST")
}

// Remote must only be mounted on the separate loopback listener behind Serve.
// No forwarded headers (including X-Forwarded-For) are trusted for identity/IP.
func (s *Store) Remote(next http.Handler, logins []string) http.Handler {
	allowed := map[string]bool{}
	for _, l := range logins {
		if l != "" {
			allowed[l] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/v1" || strings.HasPrefix(p, "/v1/") || p == "/api/hook" || strings.HasPrefix(p, "/api/hook/") {
			http.NotFound(w, r)
			return
		}
		if public(r) {
			next.ServeHTTP(w, r)
			return
		}
		ip, login := IP(r), r.Header.Get("Tailscale-User-Login")
		keys := []string{"ip:" + ip}
		if login != "" {
			keys = append(keys, "login:"+login)
		}
		if s.blocked(keys...) {
			w.Header().Set("Retry-After", "600")
			jsonError(w, 429, "too many authentication failures")
			return
		}
		var d Device
		var err error
		fresh := false
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			token, ok := strings.CutPrefix(authorization, "Bearer ")
			if !ok {
				err = ErrCredentials
			} else {
				d, err = s.Token(r.Context(), token, ip)
			}
		} else if c, e := r.Cookie(CookieName); e == nil {
			d, err = s.Session(r.Context(), c.Value, ip)
			if err == nil {
				SetCookie(w, c.Value)
			}
		} else if addr := net.ParseIP(ip); addr != nil && addr.IsLoopback() && allowed[login] {
			d, err = s.TailscaleDevice(r.Context(), login, ip)
			fresh = true
		} else {
			err = ErrCredentials
		}
		if err != nil {
			s.failed(keys...)
			if !errors.Is(err, ErrCredentials) {
				jsonError(w, 503, "authentication unavailable")
				return
			}
			if p == "/api" || strings.HasPrefix(p, "/api/") {
				jsonError(w, 401, "authentication required")
				return
			}
			if r.Method == "GET" || r.Method == "HEAD" {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			} else {
				jsonError(w, 401, "authentication required")
			}
			return
		}
		if fresh {
			value, e := s.NewSession(r.Context(), d.ID)
			if e != nil {
				jsonError(w, 503, "authentication unavailable")
				return
			}
			SetCookie(w, value)
		}
		ctx, done := s.track(r.Context(), d.ID)
		defer done()
		// Recheck after registering cancellation to close the revoke/register race.
		var revoked bool
		if e := s.db.QueryRowContext(ctx, `SELECT revoked FROM auth_devices WHERE id=?`, d.ID).Scan(&revoked); e != nil || revoked {
			jsonError(w, 401, "authentication required")
			return
		}
		if id, ok := ctx.Value(deviceKey{}).(*string); ok {
			*id = d.ID
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Login consumes a code or validates a token. It returns only a session cookie value.
func (s *Store) Login(ctx context.Context, credential, name, ip string) (string, error) {
	if s.blocked("ip:" + ip) {
		return "", ErrRateLimit
	}
	var d Device
	var err error
	if strings.HasPrefix(credential, "lol_") {
		d, err = s.Token(ctx, credential, ip)
	} else {
		_, d, err = s.Redeem(ctx, credential, name, "browser", ip, "")
	}
	if err != nil {
		s.failed("ip:" + ip)
		return "", err
	}
	return s.NewSession(ctx, d.ID)
}

var ErrRateLimit = errors.New("too many authentication failures")

// PairRequest rate limits the public redemption surface as well as normal auth.
func (s *Store) PairRequest(ctx context.Context, code, name, kind, ip string) (string, Device, error) {
	if s.blocked("ip:" + ip) {
		return "", Device{}, ErrRateLimit
	}
	token, d, e := s.Redeem(ctx, code, name, kind, ip, "")
	if e != nil {
		s.failed("ip:" + ip)
	}
	return token, d, e
}
