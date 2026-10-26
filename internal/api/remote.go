package api

import (
	"errors"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/http"
)

func remoteRoutes(m *http.ServeMux, s core.Service, o Options) {
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"version": o.Version, "api": 1, "remote": o.Remote}, nil)
	})
	if o.Auth == nil {
		return
	}
	m.HandleFunc("POST /api/pair", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Code   string `json:"code"`
			Device string `json:"device"`
			Kind   string `json:"kind"`
		}
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		token, d, e := o.Auth.PairRequest(r.Context(), v.Code, v.Device, v.Kind, auth.IP(r))
		if e != nil {
			authFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, map[string]any{"token": token, "device_id": d.ID, "me": s.Me()}, nil)
	})
	m.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) { v, e := o.Auth.Devices(r.Context()); write(w, v, e) })
	m.HandleFunc("POST /api/devices/pair", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name string `json:"name"`
		}
		if e := decode(w, r, &v); e != nil {
			write(w, nil, e)
			return
		}
		p, e := o.Auth.Pair(r.Context(), v.Name)
		w.Header().Set("Cache-Control", "no-store")
		write(w, p, e)
	})
	m.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		e := o.Auth.Revoke(r.Context(), r.PathValue("id"))
		if errors.Is(e, auth.ErrCredentials) {
			e = core.ErrNotFound
		}
		write(w, nil, e)
	})
}
func authFailure(w http.ResponseWriter, e error) {
	status, msg := 401, "invalid or expired credentials"
	if errors.Is(e, auth.ErrRateLimit) {
		status, msg = 429, "too many authentication failures"
		w.Header().Set("Retry-After", "600")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// Deliberately never echo credentials or storage errors.
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}` + "\n"))
}
