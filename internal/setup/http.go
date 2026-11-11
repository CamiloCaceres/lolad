package setup

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Handler serves the setup JSON API (docs/setup-api.md). Mount it for
// "/api/setup" and "/api/setup/" ahead of the general "/api/" handler.
func Handler(svc Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.Status(r.Context())
		reply(w, st, err)
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var req SaveRequest
		if !decode(w, r, &req) {
			return
		}
		st, err := svc.Save(r.Context(), req)
		reply(w, st, err)
	})
	mux.HandleFunc("POST /api/setup/rescan", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.Rescan(r.Context())
		reply(w, st, err)
	})
	mux.HandleFunc("POST /api/setup/service", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.InstallService(r.Context())
		reply(w, st, err)
	})
	mux.HandleFunc("POST /api/setup/terminal", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Action string `json:"action"`
		}
		if !decode(w, r, &req) {
			return
		}
		info, err := svc.StartTerminal(r.Context(), req.Action)
		reply(w, info, err)
	})
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, core.ErrInvalid):
			code = http.StatusUnprocessableEntity
		case errors.Is(err, core.ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, ErrUnavailable):
			code = http.StatusNotImplemented
		}
		fail(w, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
