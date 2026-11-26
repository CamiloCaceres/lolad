package fake

import (
	"context"
	"slices"

	"github.com/CamiloCaceres/lolad/internal/web"
)

var _ web.FriendChats = (*Service)(nil)

// AddFriendChat records a friend who started talking to one of our agents,
// as the daemon does on the pair's first message. For tests and webdev.
func (s *Service) AddFriendChat(c web.FriendChat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.At.IsZero() {
		c.At = s.now()
	}
	s.friendChats = append(s.friendChats, c)
	s.emit(c.Local, "federation", "friend chat")
}

func (s *Service) FriendChats(context.Context) ([]web.FriendChat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.DeleteFunc(slices.Clone(s.friendChats), func(c web.FriendChat) bool { return c.Seen })
	slices.SortFunc(out, func(a, b web.FriendChat) int { return b.At.Compare(a.At) })
	return out, nil
}

func (s *Service) DismissFriendChat(_ context.Context, peer, remote, local string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.friendChats {
		if c.Peer == peer && c.Remote == remote && c.Local == local && !c.Seen {
			s.friendChats[i].Seen = true
			s.emit(local, "federation", "friend chat dismissed")
		}
	}
	return nil
}
