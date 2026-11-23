package fake

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
)

// Friend links (docs/friend-connect-design.md §2, §6), faked: the link has the
// real shape (https://…/join#2.<base64url JSON>) so the /join page and the
// paste box can be tried with it, but nothing leaves the process.

var _ web.FriendInviter = (*Service)(nil)

// JoinBase is where friend links point (the site's /join page).
const JoinBase = "https://lolad.caceresdev.com/join"

type friendState struct {
	invites  []web.FriendInviteInfo
	used     map[string]bool                 // pairing secrets already accepted here
	contacts map[string][]core.RemoteContact // per provider, for connections made by a link
	waiting  map[string]bool                 // providers whose lolad has not answered yet
	seq      int
}

// linkPayload is the v2 payload (short keys, §2.2). X is fake-only: "offline"
// makes accepting wait, as if the inviter's lolad were off.
type linkPayload struct {
	T string      `json:"t"`
	D string      `json:"d,omitempty"`
	N string      `json:"n"`
	K string      `json:"k"`
	S string      `json:"s"`
	H string      `json:"h,omitempty"`
	A [][2]string `json:"a,omitempty"`
	E int64       `json:"e"`
	X string      `json:"x,omitempty"`
}

// FriendLink builds a link as if another lolad (tenant, shown as display)
// had invited us, offering agents as [slug, display] pairs. offline makes
// accepting it wait. For tests, screenshots and the dev server.
func FriendLink(display, tenant string, agents [][2]string, expires time.Time, offline bool) string {
	p := linkPayload{T: tenant, D: display, N: strings.ToLower(display), K: "demo-key", S: secret(), H: tenant, A: agents, E: expires.Unix()}
	if offline {
		p.X = "offline"
	}
	b, _ := json.Marshal(p)
	return JoinBase + "#2." + base64.RawURLEncoding.EncodeToString(b)
}

func secret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// refusal is an error written for people (UserMessage), like fed's.
type refusal string

func (r refusal) Error() string       { return "invalid request: " + string(r) }
func (r refusal) Unwrap() error       { return core.ErrInvalid }
func (r refusal) UserMessage() string { return string(r) }

// renameRefusal is the same-name refusal; Fix names the action the dashboard
// offers ("Rename yours?"), as fed's does.
type renameRefusal struct{ refusal }

func (renameRefusal) Fix() string { return "rename_lolad" }

const notALink = "That doesn’t look like a Lolad invite link. Copy the whole link your friend sent and paste it again."

func parseLink(in string) (linkPayload, bool, error) {
	var p linkPayload
	if len(in) > 4096 {
		return p, false, refusal(notALink)
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, in)
	if strings.HasPrefix(s, "lolad-invite:") { // v1 code: no name or agents to show
		return p, true, nil
	}
	if i := strings.LastIndexByte(s, '#'); i >= 0 {
		s = s[i+1:]
	}
	raw, ok := strings.CutPrefix(s, "2.")
	if !ok {
		return p, false, refusal(notALink)
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &p) != nil || p.T == "" || p.S == "" {
		return p, false, refusal(notALink)
	}
	return p, false, nil
}

func (s *Service) CreateFriendInvite(ctx context.Context, r web.FriendInviteRequest) (web.FriendInvite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var offer [][2]string
	for _, slug := range r.Agents {
		a := s.find(slug)
		if a == nil {
			return web.FriendInvite{}, fmt.Errorf("agent %q: %w", slug, core.ErrNotFound)
		}
		offer = append(offer, [2]string{a.Name, cmp.Or(a.Display, a.Name)})
	}
	now := s.now()
	s.friends.seq++
	id := fmt.Sprintf("fi_%d", s.friends.seq)
	exp := now.Add(7 * 24 * time.Hour)
	p := linkPayload{T: s.org.Tenant, D: "Milo", N: s.org.User.Name, K: "demo-key", S: secret(), H: s.org.Tenant, A: offer, E: exp.Unix()}
	b, _ := json.Marshal(p)
	s.friends.invites = append(s.friends.invites, web.FriendInviteInfo{ID: id, For: strings.TrimSpace(r.For), Agents: slices.Clone(r.Agents), CreatedAt: now, ExpiresAt: exp})
	return web.FriendInvite{ID: id, Link: JoinBase + "#2." + base64.RawURLEncoding.EncodeToString(b), ExpiresAt: exp}, nil
}

func (s *Service) FriendInvites(context.Context) ([]web.FriendInviteInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.friends.invites)
	slices.Reverse(out) // newest first
	return out, nil
}

func (s *Service) CancelFriendInvite(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.friends.invites, func(f web.FriendInviteInfo) bool { return f.ID == id && f.UsedAt.IsZero() })
	if i < 0 {
		return core.ErrNotFound
	}
	s.friends.invites = slices.Delete(s.friends.invites, i, i+1)
	return nil
}

func (s *Service) PreviewFriendInvite(ctx context.Context, link string) (web.InvitePreview, error) {
	p, v1, err := parseLink(link)
	if err != nil {
		return web.InvitePreview{}, err
	}
	if v1 {
		return web.InvitePreview{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pv := web.InvitePreview{FromDisplay: p.D, ExpiresAt: time.Unix(p.E, 0), JoinsNetwork: true}
	for _, a := range p.A {
		pv.Agents = append(pv.Agents, web.AgentOffer{Slug: a[0], Display: cmp.Or(a[1], a[0])})
	}
	pv.Expired = s.now().After(pv.ExpiresAt)
	pv.SameName = p.T == s.org.Tenant
	pv.AlreadyConnected = slices.ContainsFunc(s.connections, func(c core.Connection) bool { return c.Provider == p.T+".lolad.local" })
	return pv, nil
}

func (s *Service) AcceptFriendInvite(ctx context.Context, link string, r web.AcceptRequest) (web.AcceptResult, error) {
	p, v1, err := parseLink(link)
	if err != nil {
		return web.AcceptResult{}, err
	}
	if v1 {
		c, err := s.AcceptInvite(ctx, link)
		return web.AcceptResult{Connection: c, State: web.FriendConnected}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	who := cmp.Or(p.D, "your friend")
	switch {
	case s.now().After(time.Unix(p.E, 0)):
		return web.AcceptResult{}, refusal("This invite has expired. Ask " + who + " for a new link.")
	case p.T == s.org.Tenant:
		return web.AcceptResult{}, renameRefusal{refusal("Your Lolad and " + who + "’s have the same name, so they can’t connect. Rename yours under Settings, then paste the link again.")}
	case s.friends.used[p.S]:
		return web.AcceptResult{}, refusal("This invite was already used. If that wasn’t you, tell " + who + ".")
	}
	for _, slug := range r.Agents {
		if s.find(slug) == nil {
			return web.AcceptResult{}, fmt.Errorf("agent %q: %w", slug, core.ErrNotFound)
		}
	}
	if s.friends.used == nil {
		s.friends.used, s.friends.contacts, s.friends.waiting = map[string]bool{}, map[string][]core.RemoteContact{}, map[string]bool{}
	}
	s.friends.used[p.S] = true
	provider := p.T + ".lolad.local"
	now := s.now()
	c := core.Connection{Provider: provider, Display: cmp.Or(p.D, p.T), URL: "https://lolad-hub.caceresdev.com/p/" + cmp.Or(p.H, p.T), Fingerprint: "SHA256:demo-" + p.T, UserReachable: true, CreatedAt: now}
	for _, slug := range r.Agents {
		c.Shares = append(c.Shares, core.Share{Local: slug})
	}
	state := web.FriendConnected
	if p.X == "offline" {
		state = web.FriendWaiting
		s.friends.waiting[provider] = true
		c.Waiting, c.WaitingUntil = true, time.Unix(p.E, 0)
	} else {
		c.LastSeen = now
	}
	human := p.N + "@" + provider
	contacts := []core.RemoteContact{{Address: human, Name: p.N, Display: c.Display, Kind: "human", Peer: provider, PeerDisplay: c.Display}}
	for _, a := range p.A {
		contacts = append(contacts, core.RemoteContact{Address: a[0] + "@" + provider, Name: a[0], Display: cmp.Or(a[1], a[0]), Kind: "agent", Peer: provider, PeerDisplay: c.Display})
	}
	s.friends.contacts[provider] = contacts
	for _, local := range append([]string{s.org.User.Name}, r.Agents...) {
		s.grants = append(s.grants, core.Grant{Peer: provider, Remote: human, RemoteDisplay: c.Display, Local: local, State: core.GrantAllowed, Origin: "invited", CreatedAt: now, DecidedAt: now})
	}
	s.connections = slices.DeleteFunc(s.connections, func(x core.Connection) bool { return x.Provider == provider })
	s.connections = append(s.connections, c)
	s.emit("", "federation", "Connected with "+c.Display)
	return web.AcceptResult{Connection: cloneConnections([]core.Connection{c})[0], State: state}, nil
}

// FinishWaiting makes a waiting friend connection connect, as when their
// lolad comes online. For tests and the dev server.
func (s *Service) FinishWaiting(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.friends.waiting[provider] {
		return
	}
	delete(s.friends.waiting, provider)
	for i := range s.connections {
		if s.connections[i].Provider == provider {
			s.connections[i].LastSeen = s.now()
			s.connections[i].Waiting, s.connections[i].WaitingUntil = false, time.Time{}
		}
	}
	s.emit("", "federation", "Friend connected")
}

// friendContacts are the people and agents reachable on connections made by
// a link (caller holds s.mu).
func (s *Service) friendContacts() []core.RemoteContact {
	var out []core.RemoteContact
	for _, c := range s.connections {
		if c.Paused || s.friends.waiting[c.Provider] {
			continue
		}
		out = append(out, s.friends.contacts[c.Provider]...)
	}
	return out
}
