package api

import (
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/http"
)

func federationRoutes(m *http.ServeMux, s core.Federator) {
	m.HandleFunc("GET /api/federation", func(w http.ResponseWriter, r *http.Request) { write(w, s.Info(r.Context()), nil) })
	m.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) { v, e := s.Connections(r.Context()); write(w, v, e) })
	m.HandleFunc("POST /api/connections", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Code string `json:"code"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.AcceptInvite(r.Context(), b.Code)
		if e != nil {
			// Pairing errors can originate at the invited server. Never reflect a
			// remote response that could contain the submitted invite secret.
			write(w, nil, fmt.Errorf("%w: invite was not accepted; verify the code, tenant names and peer reachability", core.ErrInvalid))
			return
		}
		write(w, v, nil)
	})
	m.HandleFunc("POST /api/connections/invite", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := s.CreateInvite(r.Context())
		write(w, v, e)
	})
	m.HandleFunc("PATCH /api/connections/{provider}", func(w http.ResponseWriter, r *http.Request) {
		var b core.ConnectionUpdate
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.UpdateConnection(r.Context(), r.PathValue("provider"), b)
		write(w, v, e)
	})
	m.HandleFunc("DELETE /api/connections/{provider}", func(w http.ResponseWriter, r *http.Request) {
		e := s.RemoveConnection(r.Context(), r.PathValue("provider"))
		write(w, nil, e)
	})
	m.HandleFunc("GET /api/grants", func(w http.ResponseWriter, r *http.Request) { v, e := s.Grants(r.Context()); write(w, v, e) })
	m.HandleFunc("POST /api/grants/decide", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Peer   string `json:"peer"`
			Remote string `json:"remote"`
			Local  string `json:"local"`
			Allow  *bool  `json:"allow"`
		}
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		if b.Peer == "" || b.Remote == "" || b.Local == "" || b.Allow == nil {
			write(w, nil, core.ErrInvalid)
			return
		}
		v, e := s.DecideGrant(r.Context(), b.Peer, b.Remote, b.Local, *b.Allow)
		write(w, v, e)
	})
	m.HandleFunc("GET /api/remote-contacts", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.RemoteContacts(r.Context(), r.URL.Query().Get("local"))
		write(w, v, e)
	})
}
