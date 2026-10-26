package fake

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func (s *Service) seedFederation() {
	now := s.now()
	s.connections = []core.Connection{
		{Provider: "jugui.lolad.local", Display: "Jugui", URL: "https://jugui.example.ts.net", Fingerprint: "SHA256:demo-jugui", UserReachable: true, Shares: []core.Share{{Local: "ultron"}}, Pending: 1, Queued: 1, LastSeen: now.Add(-2 * time.Minute), CreatedAt: now.Add(-24 * time.Hour)},
		{Provider: "studio.lolad.local", Display: "Studio", URL: "https://studio.example.ts.net", Fingerprint: "SHA256:demo-studio", Paused: true, LastSeen: now.Add(-24 * time.Hour), LastError: "Could not reach Studio. Messages will wait until the connection resumes.", Queued: 3},
	}
	s.grants = []core.Grant{
		{Peer: "jugui.lolad.local", Remote: "jugui@jugui.lolad.local", RemoteDisplay: "Jugui", Local: "ultron", State: core.GrantPending, Held: 2, FirstSubject: "Can Ultron help plan the release?", CreatedAt: now.Add(-8 * time.Minute)},
		{Peer: "jugui.lolad.local", Remote: "nova@jugui.lolad.local", RemoteDisplay: "Nova", Local: "ultron", State: core.GrantAllowed, Origin: "approved", DecidedAt: now.Add(-time.Hour)},
	}
	for i := 0; i < 2; i++ {
		s.mail = append(s.mail, core.Mail{ID: fmt.Sprintf("fed-held-%d", i), FromName: "jugui@jugui.lolad.local", ToName: "ultron", From: "jugui@jugui.lolad.local", To: s.addr("ultron"), Remote: "jugui.lolad.local", Subject: "Can Ultron help plan the release?", Body: "Could you help us plan the release checklist?", Status: core.MailHeld, Signed: true, Verified: true, CreatedAt: now.Add(-8 * time.Minute)})
	}
	for i, state := range []string{core.DeliveryDelivered, core.DeliveryHeld, core.DeliveryQueued, core.DeliveryFailed} {
		m := core.Mail{ID: fmt.Sprintf("fed-demo-%d", i), FromName: s.org.User.Name, ToName: "jugui@jugui.lolad.local", From: s.addr(s.org.User.Name), To: "jugui@jugui.lolad.local", Subject: "Planning together", Body: []string{"The release notes are ready to review.", "Could Nova review the examples?", "I will send the final checklist shortly.", "Here is the old draft for reference."}[i], Signed: true, Verified: true, Remote: "jugui.lolad.local", Delivery: state, Status: core.MailRead, CreatedAt: now.Add(time.Duration(i-6) * time.Minute)}
		if state == core.DeliveryFailed {
			m.DeliveryError = "The recipient is no longer shared."
		}
		s.mail = append(s.mail, m)
	}
	s.mail = append(s.mail, core.Mail{ID: "fed-demo-in", FromName: "jugui@jugui.lolad.local", ToName: s.org.User.Name, From: "jugui@jugui.lolad.local", To: s.addr(s.org.User.Name), Body: "Thanks! Nova and I will take a look this afternoon.", Signed: true, Verified: true, Remote: "jugui.lolad.local", Status: core.MailUnread, CreatedAt: now.Add(-time.Minute)})
	slices.SortStableFunc(s.mail, func(a, b core.Mail) int { return a.CreatedAt.Compare(b.CreatedAt) })
}
func (s *Service) Info(context.Context) core.FederationInfo {
	return core.FederationInfo{Enabled: true, Provider: "milo.lolad.local", Display: "Milo", URL: "https://milo.example.ts.net", Fingerprint: "SHA256:demo-provider-fingerprint"}
}
func cloneConnections(cs []core.Connection) []core.Connection {
	out := slices.Clone(cs)
	for i := range out {
		out[i].Shares = slices.Clone(out[i].Shares)
		for j := range out[i].Shares {
			out[i].Shares[j].Remote = slices.Clone(out[i].Shares[j].Remote)
		}
	}
	return out
}
func (s *Service) Connections(context.Context) ([]core.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneConnections(s.connections), nil
}
func (s *Service) CreateInvite(context.Context) (core.Invite, error) {
	return core.Invite{Code: "lolad-invite:demo-only-not-a-real-invite", ExpiresAt: s.now().Add(72 * time.Hour)}, nil
}
func (s *Service) AcceptInvite(ctx context.Context, code string) (core.Connection, error) {
	if code == "" {
		return core.Connection{}, core.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := core.Connection{Provider: "friend.lolad.local", Display: "Friend", CreatedAt: s.now()}
	for _, existing := range s.connections {
		if existing.Provider == c.Provider {
			return existing, nil
		}
	}
	s.connections = append(s.connections, c)
	s.emit("", "federation", "Connection added")
	return c, nil
}
func (s *Service) UpdateConnection(ctx context.Context, p string, u core.ConnectionUpdate) (core.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.connections {
		c := &s.connections[i]
		if c.Provider != p {
			continue
		}
		if u.Display != nil {
			c.Display = *u.Display
		}
		if u.Paused != nil {
			c.Paused = *u.Paused
		}
		if u.UserReachable != nil {
			c.UserReachable = *u.UserReachable
		}
		if u.Shares != nil {
			c.Shares = cloneConnections([]core.Connection{{Shares: u.Shares}})[0].Shares
		}
		s.emit("", "federation", "Connection updated")
		return cloneConnections([]core.Connection{*c})[0], nil
	}
	return core.Connection{}, core.ErrNotFound
}
func (s *Service) RemoveConnection(ctx context.Context, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.connections, func(c core.Connection) bool { return c.Provider == p })
	if i < 0 {
		return core.ErrNotFound
	}
	s.connections = slices.Delete(s.connections, i, i+1)
	s.grants = slices.DeleteFunc(s.grants, func(g core.Grant) bool { return g.Peer == p })
	for j := range s.mail {
		m := &s.mail[j]
		if m.Remote == p && (m.Delivery == core.DeliveryQueued || m.Delivery == core.DeliveryHeld) {
			m.Delivery = core.DeliveryFailed
			m.DeliveryError = "Connection removed"
		}
	}
	s.emit("", "mail", "Connection removed; queued mail failed")
	s.emit("", "federation", "Connection removed")
	return nil
}
func (s *Service) Grants(context.Context) ([]core.Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.grants), nil
}
func (s *Service) DecideGrant(ctx context.Context, p, r, l string, allow bool) (core.Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.grants {
		g := &s.grants[i]
		if g.Peer == p && g.Remote == r && g.Local == l {
			g.State = core.GrantBlocked
			if allow {
				g.State = core.GrantAllowed
			}
			g.Held = 0
			g.Origin = "approved"
			g.DecidedAt = s.now()
			if allow {
				for j := range s.mail {
					m := &s.mail[j]
					if m.Remote == p && m.FromName == r && m.ToName == l && m.Status == core.MailHeld {
						m.Status = core.MailUnread
					}
				}
				s.emit(l, "mail", "Held messages released")
			} else {
				s.mail = slices.DeleteFunc(s.mail, func(m core.Mail) bool {
					return m.Remote == p && m.FromName == r && m.ToName == l && m.Status == core.MailHeld
				})
			}
			for j := range s.connections {
				if s.connections[j].Provider == p && s.connections[j].Pending > 0 {
					s.connections[j].Pending--
				}
			}
			s.emit(l, "federation", "Sender approval changed")
			return *g, nil
		}
	}
	return core.Grant{}, core.ErrNotFound
}
func (s *Service) RemoteContacts(ctx context.Context, local string) ([]core.RemoteContact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.ContainsFunc(s.connections, func(c core.Connection) bool { return c.Provider == "jugui.lolad.local" && !c.Paused }) {
		return nil, nil
	}
	contacts := []core.RemoteContact{{Address: "jugui@jugui.lolad.local", Name: "jugui", Display: "Jugui", Kind: "human", Peer: "jugui.lolad.local", PeerDisplay: "Jugui"}, {Address: "nova@jugui.lolad.local", Name: "nova", Display: "Nova", Kind: "agent", Role: "Research assistant", Peer: "jugui.lolad.local", PeerDisplay: "Jugui"}}
	if local == s.org.User.Name {
		return contacts, nil
	}
	return slices.DeleteFunc(contacts, func(rc core.RemoteContact) bool {
		return !slices.ContainsFunc(s.grants, func(g core.Grant) bool {
			return g.Local == local && g.Remote == rc.Address && g.State == core.GrantAllowed
		})
	}), nil
}
