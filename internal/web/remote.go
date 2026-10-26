package web

import (
	"errors"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/coder/websocket"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

type devicesView struct {
	Devices   []auth.Device
	Pair      *auth.Pairing
	QR        template.HTML
	PublicURL string
	Error     string
}

func (s *server) remoteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /login", s.login)
	m.HandleFunc("POST /login", s.login)
	m.HandleFunc("POST /logout", s.logout)
	m.HandleFunc("GET /devices", s.devices)
	m.HandleFunc("POST /devices/pair", s.devicesPair)
	m.HandleFunc("POST /devices/{id}/revoke", s.devicesRevoke)
}
func safeNext(v string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.ContainsAny(v, "\\\r\n") {
		return "/"
	}
	return v
}
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	msg := ""
	status := 200
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if e := r.ParseForm(); e != nil {
			http.Error(w, "invalid login", 400)
			return
		}
		next = safeNext(r.FormValue("next"))
		if s.auth == nil {
			msg = "Remote access is not configured."
			status = 503
		} else {
			value, e := s.auth.Login(r.Context(), strings.TrimSpace(r.FormValue("credential")), "Browser", auth.IP(r))
			if e == nil {
				auth.SetCookie(w, value)
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			msg = "That code or token is invalid or expired. Create a new pairing code on a trusted device."
			status = 401
			if errors.Is(e, auth.ErrRateLimit) {
				status = 429
				msg = "Too many attempts. Try again in ten minutes."
				w.Header().Set("Retry-After", "600")
			}
		}
	}
	_, pages := s.templates()
	s.write(w, status, pages["login"], "layout", map[string]any{"Next": next, "Error": msg})
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if c, e := r.Cookie(auth.CookieName); e == nil {
			_ = s.auth.Logout(r.Context(), c.Value)
		}
	}
	auth.ClearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (s *server) devices(w http.ResponseWriter, r *http.Request) { s.renderDevices(w, r, nil, "") }
func (s *server) renderDevices(w http.ResponseWriter, r *http.Request, p *auth.Pairing, msg string) {
	v := devicesView{Pair: p, Error: msg, PublicURL: s.publicURL}
	if s.auth != nil {
		d, e := s.auth.Devices(r.Context())
		if e != nil {
			s.fail(w, r, e)
			return
		}
		v.Devices = d
	} else {
		v.Error = "Device pairing is available on the daemon."
	}
	if p != nil && s.publicURL != "" {
		svg, e := auth.QRSVG(auth.PairURL(s.publicURL, p.Code))
		if e != nil {
			v.Error = "Could not generate QR code."
		} else {
			v.QR = template.HTML(svg)
		}
	} // only encoder-generated fixed markup
	s.page(w, r, 200, "devices", layout{Title: "Devices", Data: v, Nav: "devices"})
}
func (s *server) devicesPair(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Error(w, "pairing unavailable", 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	p, e := s.auth.Pair(r.Context(), r.FormValue("name"))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	s.renderDevices(w, r, &p, "")
}
func (s *server) devicesRevoke(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Error(w, "pairing unavailable", 503)
		return
	}
	if e := s.auth.Revoke(r.Context(), r.PathValue("id")); e != nil {
		s.fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/devices", http.StatusSeeOther)
}

// websocketOptions trusts exactly the configured Serve origin on the remote handler.
func (s *server) websocketOptions() *websocket.AcceptOptions {
	o := &websocket.AcceptOptions{}
	if u, e := url.Parse(s.trustedOrigin); e == nil && u.Host != "" {
		o.OriginPatterns = []string{u.Host}
	}
	return o
}
