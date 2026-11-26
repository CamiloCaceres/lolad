package web

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"time"
)

// FriendChat is a friend (someone on a connected lolad) who started talking
// to one of the user's agents. The home page shows it until the user
// dismisses it, because the user never sees that conversation otherwise.
type FriendChat struct {
	Peer          string    `json:"peer"` // their lolad's provider name
	PeerDisplay   string    `json:"peer_display,omitempty"`
	Remote        string    `json:"remote"` // their full address
	RemoteDisplay string    `json:"remote_display,omitempty"`
	Kind          string    `json:"kind,omitempty"` // human | agent | "" (unknown)
	Local         string    `json:"local"`          // the agent's slug
	Subject       string    `json:"subject"`        // of their first message
	At            time.Time `json:"at"`
	Seen          bool      `json:"seen,omitempty"`
}

// FriendChats is optional on a Service: when present the home page lists new
// friend conversations with the user's agents.
type FriendChats interface {
	FriendChats(ctx context.Context) ([]FriendChat, error) // not yet dismissed, newest first
	DismissFriendChat(ctx context.Context, peer, remote, local string) error
}

type friendChatView struct {
	FriendChat
	Who, Lolad, LocalDisplay, Traffic string
}

func (s *server) friendChats(ctx context.Context, agents map[string]string) []friendChatView {
	f, ok := s.svc.(FriendChats)
	if !ok {
		return nil
	}
	list, err := f.FriendChats(ctx)
	if err != nil {
		return nil
	}
	out := make([]friendChatView, 0, len(list))
	for _, c := range list {
		v := friendChatView{FriendChat: c, Who: cmp.Or(c.RemoteDisplay, c.Remote), Lolad: cmp.Or(c.PeerDisplay, c.Peer), LocalDisplay: cmp.Or(agents[c.Local], c.Local)}
		v.Traffic = "/traffic?peer=" + url.QueryEscape(c.Local) + "&other=" + url.QueryEscape(c.Remote)
		out = append(out, v)
	}
	return out
}

func (s *server) friendChatDismiss(w http.ResponseWriter, r *http.Request) {
	f, ok := s.svc.(FriendChats)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := f.DismissFriendChat(r.Context(), r.FormValue("peer"), r.FormValue("remote"), r.FormValue("local")); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
