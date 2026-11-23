package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// friendRoutes: friend links (docs/friend-connect-design.md §6). Links are
// secrets: they travel in POST bodies only and are never logged. Refusals
// carry the service's message written for people.
func friendRoutes(m *http.ServeMux, f core.FriendInviter) {
	m.HandleFunc("POST /api/friends/invites", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var b core.FriendInviteRequest
		if e := decode(w, r, &b); e != nil {
			friendWrite(w, nil, e)
			return
		}
		v, e := f.CreateFriendInvite(r.Context(), b)
		friendWrite(w, v, e)
	})
	m.HandleFunc("GET /api/friends/invites", func(w http.ResponseWriter, r *http.Request) {
		v, e := f.FriendInvites(r.Context())
		friendWrite(w, v, e)
	})
	m.HandleFunc("DELETE /api/friends/invites/{id}", func(w http.ResponseWriter, r *http.Request) {
		friendWrite(w, nil, f.CancelFriendInvite(r.Context(), r.PathValue("id")))
	})
	m.HandleFunc("POST /api/friends/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var b struct {
			Link string `json:"link"`
		}
		if e := decode(w, r, &b); e != nil {
			friendWrite(w, nil, e)
			return
		}
		v, e := f.PreviewFriendInvite(r.Context(), b.Link)
		friendWrite(w, v, e)
	})
	m.HandleFunc("POST /api/friends/accept", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var b struct {
			Link   string   `json:"link"`
			Agents []string `json:"agents"`
		}
		if e := decode(w, r, &b); e != nil {
			friendWrite(w, nil, e)
			return
		}
		v, e := f.AcceptFriendInvite(r.Context(), b.Link, core.AcceptRequest{Agents: b.Agents})
		friendWrite(w, v, e)
	})
}

// friendWrite is write, with the message written for people when the error
// has one. A decode error never echoes the body, so a link cannot leak.
func friendWrite(w http.ResponseWriter, v any, e error) {
	var um interface{ UserMessage() string }
	if e == nil || !errors.As(e, &um) {
		if errors.Is(e, core.ErrInvalid) {
			e = core.ErrInvalid // drop details that might quote the request
		}
		write(w, v, e)
		return
	}
	status := 422
	switch {
	case errors.Is(e, core.ErrNotFound):
		status = 404
	case errors.Is(e, core.ErrDenied):
		status = 403
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status), "message": um.UserMessage()})
}
