package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// renameRoutes: renaming this lolad (SPEC "Rename"). The rename itself runs
// at the next start; POST answers 202 and the daemon then exits so its
// service manager starts it again.
func renameRoutes(m *http.ServeMux, s core.LoladRenamer) {
	m.HandleFunc("GET /api/lolad/name", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.LoladName(r.Context())
		write(w, v, e)
	})
	m.HandleFunc("GET /api/lolad/name/check", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.CheckLoladName(r.Context(), r.URL.Query().Get("name"))
		write(w, v, e)
	})
	m.HandleFunc("POST /api/lolad/name", func(w http.ResponseWriter, r *http.Request) {
		var b core.RenameRequest
		if e := decode(w, r, &b); e != nil {
			write(w, nil, e)
			return
		}
		v, e := s.RenameLolad(r.Context(), b)
		if e == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		renameWrite(w, e)
	})
}

// renameWrite answers a refused rename with its message for people: 409 for
// a conflict (with "outbox" when remote mail is still on the way), 422 for a
// name that cannot be used.
func renameWrite(w http.ResponseWriter, e error) {
	var um interface{ UserMessage() string }
	if !errors.As(e, &um) {
		write(w, nil, e)
		return
	}
	status := 422
	if errors.Is(e, core.ErrConflict) {
		status = 409
	}
	body := map[string]any{"error": http.StatusText(status), "message": um.UserMessage()}
	var ob interface{ Outbox() int }
	if errors.As(e, &ob) {
		body["outbox"] = ob.Outbox()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
