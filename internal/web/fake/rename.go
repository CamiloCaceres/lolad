package fake

import (
	"context"
	"fmt"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

// The fake renames at once (the real daemon restarts and renames at start):
// the tenant changes, connections end, and queued remote mail is marked not
// sent. Restarting is true so the dashboard exercises its wait-and-reload.

var _ core.LoladRenamer = (*Service)(nil)

type fakeConflict struct {
	msg string
	n   int
}

func (e fakeConflict) Error() string       { return core.ErrConflict.Error() + ": " + e.msg }
func (e fakeConflict) Unwrap() error       { return core.ErrConflict }
func (e fakeConflict) UserMessage() string { return e.msg }
func (e fakeConflict) Outbox() int         { return e.n }

func (s *Service) queuedRemote() int {
	n := 0
	for _, m := range s.mail {
		if m.Remote != "" && m.Delivery == core.DeliveryQueued {
			n++
		}
	}
	return n
}

func (s *Service) LoladName(context.Context) (core.LoladName, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.org.Tenant
	return core.LoladName{Name: t, Provider: t + ".lolad.local", Restart: "auto", Connections: cloneConnections(s.connections), OpenLinks: len(s.friends.invites), Outbox: s.queuedRemote()}, nil
}

func (s *Service) CheckLoladName(_ context.Context, in string) (core.NameCheck, error) {
	s.mu.Lock()
	t, me := s.org.Tenant, s.org.User.Name
	s.mu.Unlock()
	name, e := policy.CheckRename(t, in)
	if e != nil {
		return core.NameCheck{Message: e.(policy.RenameError).Msg}, nil
	}
	return core.NameCheck{OK: true, Name: name, Message: "Addresses will look like " + me + "@" + name + ".lolad.local."}, nil
}

func (s *Service) RenameLolad(_ context.Context, r core.RenameRequest) (core.RenameAccepted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, e := policy.CheckRename(s.org.Tenant, r.Name)
	if e != nil {
		return core.RenameAccepted{}, e
	}
	if n := s.queuedRemote(); n > 0 && !r.Anyway {
		return core.RenameAccepted{}, fakeConflict{fmt.Sprintf("%d messages to other lolads are still on the way. Wait until they arrive, or rename anyway and they won't be sent.", n), n}
	}
	for i := range s.mail {
		if s.mail[i].Remote != "" && s.mail[i].Delivery == core.DeliveryQueued {
			s.mail[i].Delivery, s.mail[i].DeliveryError = core.DeliveryFailed, "not sent: this lolad was renamed"
		}
	}
	s.org.Tenant = name
	s.connections, s.grants = nil, nil
	s.friends.invites = nil
	return core.RenameAccepted{Name: name, Restarting: true}, nil
}
