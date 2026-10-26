package api

import (
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/mailprov"
	"net/http"
)

func emailRoutes(m *http.ServeMux, s core.Service) {
	svc, ok := s.(mailprov.Service)
	if !ok {
		return
	}
	m.HandleFunc("GET /api/email", func(w http.ResponseWriter, r *http.Request) { v, e := svc.EmailFleet(r.Context()); write(w, v, e) })
	m.HandleFunc("GET /api/email/provider", func(w http.ResponseWriter, r *http.Request) {
		v, e := svc.EmailProvider(r.Context(), nil)
		write(w, v, e)
	})
	m.HandleFunc("PUT /api/email/provider", func(w http.ResponseWriter, r *http.Request) {
		var b mailprov.ProviderUpdate
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := svc.EmailProvider(r.Context(), &b)
		write(w, v, e)
	})
	for _, op := range []string{"provision", "rotate", "detach"} {
		m.HandleFunc("POST /api/agents/{name}/email/"+op, func(w http.ResponseWriter, r *http.Request) {
			var b mailprov.Request
			if e := decode(w, r, &b); e != nil {
				write(w, nil, e)
				return
			}
			var v mailprov.Result
			var e error
			switch op {
			case "provision":
				v, e = svc.ProvisionEmail(r.Context(), r.PathValue("name"), b)
			case "rotate":
				v, e = svc.RotateEmail(r.Context(), r.PathValue("name"), b)
			case "detach":
				v, e = svc.DetachEmail(r.Context(), r.PathValue("name"), b)
			}
			write(w, v, e)
		})
	}
}
