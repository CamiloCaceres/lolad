package hub

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/fed"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

// Friend connect end to end (docs/friend-connect-design.md §3): two fresh
// lolads that are on no hub, one link, open sign-up.

type friendLolad struct {
	*lolad
	h      *testHub
	tenant string
	mu     sync.Mutex
	hubURL string
	handle string
	events []core.Event
}

// Membership and Join implement fed.HubMember the way the app does.
func (x *friendLolad) Membership() (string, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.hubURL, x.handle, x.hubURL != ""
}

func (x *friendLolad) Join(ctx context.Context, hubURL, preferred string) (string, error) {
	signer := Signer{PublicKey: x.f.ProviderPublicKey(), Sign: x.f.ProviderSign}
	handle := preferred
	for range 4 {
		u, e := Register(ctx, nil, hubURL, handle, handle, "", signer)
		var he *Error
		if errors.As(e, &he) && he.Suggestion != "" {
			handle = he.Suggestion
			continue
		}
		if errors.As(e, &he) && he.Handle != "" {
			handle = he.Handle
			continue
		}
		if e != nil {
			return "", e
		}
		if e = x.f.SetURL(u); e != nil {
			return "", e
		}
		x.mu.Lock()
		x.hubURL, x.handle = hubURL, handle
		x.mu.Unlock()
		x.opts = ClientOptions{HubURL: hubURL, Handle: handle, Signer: signer, Handler: x.f.Handler(), Peers: x.f.PeerURLs,
			OnPeerOnline: func(u string) { _ = x.f.RetryURL(context.Background(), u) }, Backoff: []time.Duration{20 * time.Millisecond}}
		x.client, x.stop = startClientNoCleanupWait(x.opts)
		return u, nil
	}
	return "", errors.New("no handle")
}

// startClientNoCleanupWait runs a tunnel client from inside Join; the test's
// cleanup stops it through x.stop.
func startClientNoCleanupWait(o ClientOptions) (*Client, context.CancelFunc) {
	c, _ := NewClient(o)
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	return c, cancel
}

func (x *friendLolad) eventKinds() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	var k []string
	for _, e := range x.events {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func newFriendLolad(t *testing.T, h *testHub, tenant string, agentNames ...string) *friendLolad {
	t.Helper()
	state, config := t.TempDir(), t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(state, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	org := policy.DefaultOrg()
	org.Tenant = tenant
	org.User = core.User{Name: tenant, Display: strings.ToUpper(tenant[:1]) + tenant[1:]}
	org.Defaults.Independents = "all"
	var agents []core.Agent
	for _, n := range agentNames {
		agents = append(agents, core.Agent{Name: n, Display: strings.ToUpper(n[:1]) + n[1:]})
	}
	x := &friendLolad{lolad: &lolad{}, h: h, tenant: tenant}
	x.p, e = amp.New(db, state, tenant, policy.New(org, agentNames...), amp.WithOrg(func() core.Org { return org }), amp.WithAgents(func() []core.Agent { return agents }))
	if e != nil {
		t.Fatal(e)
	}
	ids := []struct{ n, k string }{{tenant, "human"}, {"lolad", "system"}}
	for _, n := range agentNames {
		ids = append(ids, struct{ n, k string }{n, "agent"})
	}
	for _, i := range ids {
		if _, e = x.p.EnsureIdentity(t.Context(), i.n, i.k); e != nil {
			t.Fatal(e)
		}
	}
	x.f, e = fed.New(fed.Options{DB: db, StateDir: state, ConfigDir: config, Provider: x.p, Enabled: true, Org: func() core.Org { return org },
		Agents: func() []core.Agent { return agents }, Backoff: []time.Duration{time.Hour},
		EventSink: func(ev core.Event) { x.mu.Lock(); x.events = append(x.events, ev); x.mu.Unlock() }})
	if e != nil {
		t.Fatal(e)
	}
	x.f.SetHub(x, h.url, "https://lolad.example/join")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); x.f.Run(ctx) }()
	t.Cleanup(func() {
		if x.stop != nil {
			x.stop()
		}
		cancel()
		<-done
		db.Close()
	})
	return x
}

func userMessageOf(e error) string {
	var um interface{ UserMessage() string }
	if errors.As(e, &um) {
		return um.UserMessage()
	}
	return ""
}

func delivered(t *testing.T, x *friendLolad, from, to string) {
	t.Helper()
	m, e := x.p.Send(t.Context(), from, core.Outgoing{To: to, Subject: "hi", Body: from + " to " + to})
	if e != nil {
		t.Fatal(from, "→", to, e)
	}
	eventually(t, from+" → "+to+" delivered", func() bool {
		g, e := x.p.Get(t.Context(), m.ID)
		return e == nil && g.Delivery == core.DeliveryDelivered
	})
}

func TestFriendLinkEndToEnd(t *testing.T) {
	h := newHub(t)
	ana := newFriendLolad(t, h, "ana", "maple", "owl")
	ben := newFriendLolad(t, h, "ben", "bee")
	ctx := t.Context()

	// Ana is on no hub: making the link joins it on its own.
	inv, e := ana.f.CreateFriendInvite(ctx, core.FriendInviteRequest{Agents: []string{"maple"}, For: "Ben"})
	if e != nil {
		t.Fatal(e)
	}
	if _, handle, ok := ana.Membership(); !ok || handle != "ana" {
		t.Fatal("ana did not join", handle)
	}
	if !strings.HasPrefix(inv.Link, "https://lolad.example/join#2.") || len(inv.Link) > 400 || time.Until(inv.ExpiresAt) < 6*24*time.Hour {
		t.Fatal(inv)
	}

	// Ben reads it before accepting: no network, no side effects.
	p, e := ben.f.PreviewFriendInvite(ctx, "  Look: "+inv.Link+"\n")
	if e != nil || p.FromDisplay != "Ana" || len(p.Agents) != 1 || p.Agents[0].Slug != "maple" || p.Agents[0].Display != "Maple" || !p.JoinsNetwork || p.Expired || p.SameName || p.AlreadyConnected {
		t.Fatalf("%+v %v", p, e)
	}
	if _, _, ok := ben.Membership(); ok {
		t.Fatal("preview joined the hub")
	}

	// One call: join, pair, pre-approve.
	res, e := ben.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{Agents: []string{"bee"}})
	if e != nil || res.State != core.FriendConnected || res.Connection.Provider != "ana.lolad.local" || res.Connection.URL != h.url+"/p/ana" {
		t.Fatalf("%+v %v", res, e)
	}
	if _, handle, ok := ben.Membership(); !ok || handle != "ben" {
		t.Fatal("ben did not join", handle)
	}
	cs, _ := ana.f.Connections(ctx)
	if len(cs) != 1 || cs[0].Provider != "ben.lolad.local" || !cs[0].UserReachable || len(cs[0].Shares) != 1 || cs[0].Shares[0].Local != "maple" || len(cs[0].Shares[0].Remote) != 0 {
		t.Fatalf("ana side %+v", cs)
	}
	cs, _ = ben.f.Connections(ctx)
	if len(cs) != 1 || !cs[0].UserReachable || len(cs[0].Shares) != 1 || cs[0].Shares[0].Local != "bee" {
		t.Fatalf("ben side %+v", cs)
	}
	gs, _ := ana.f.Grants(ctx)
	if len(gs) != 4 { // {ben, bee} × {ana, maple}
		t.Fatalf("grants %+v", gs)
	}
	for _, g := range gs {
		if g.State != core.GrantAllowed || g.Origin != "invited" {
			t.Fatalf("%+v", g)
		}
	}

	// Humans and offered agents talk both ways with no approval card.
	delivered(t, ben, "ben", "ana@ana.lolad.local")
	delivered(t, ben, "ben", "maple@ana.lolad.local")
	delivered(t, ben, "bee", "maple@ana.lolad.local")
	delivered(t, ana, "maple", "bee@ben.lolad.local")
	delivered(t, ana, "ana", "ben@ben.lolad.local")
	// An agent Ana did not offer is not reachable.
	if m, e := ben.p.Send(ctx, "ben", core.Outgoing{To: "owl@ana.lolad.local", Subject: "x", Body: "x"}); e == nil {
		time.Sleep(300 * time.Millisecond)
		if g, _ := ben.p.Get(ctx, m.ID); g.Delivery == core.DeliveryDelivered {
			t.Fatal("unoffered agent reached")
		}
	}

	// Ana's list shows the link used, by Ben; both sides were told.
	list, _ := ana.f.FriendInvites(ctx)
	if len(list) != 1 || list[0].For != "Ben" || list[0].UsedByDisplay != "Ben" || list[0].UsedAt.IsZero() || list[0].Agents[0] != "maple" {
		t.Fatalf("%+v", list)
	}
	eventually(t, "events", func() bool {
		return strings.Contains(ana.eventKinds(), "friend_connected") && strings.Contains(ben.eventKinds(), "friend_connected")
	})

	// The link works once: someone else is refused by Ana's lolad. (Carl
	// passes the local checks, so he joins the hub before pairing is refused.)
	carl := newFriendLolad(t, h, "carl")
	_, e = carl.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{})
	if msg := userMessageOf(e); !strings.Contains(msg, "already used") {
		t.Fatal(e)
	}
	// A used link cannot be cancelled.
	if e = ana.f.CancelFriendInvite(ctx, inv.ID); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestFriendLinkRefusalsHaveNoSideEffects(t *testing.T) {
	h := newHub(t)
	ana := newFriendLolad(t, h, "ana", "maple")
	ctx := t.Context()
	inv, e := ana.f.CreateFriendInvite(ctx, core.FriendInviteRequest{})
	if e != nil {
		t.Fatal(e)
	}
	// Same name: refused before joining the hub.
	twin := newFriendLolad(t, h, "ana")
	if p, _ := twin.f.PreviewFriendInvite(ctx, inv.Link); !p.SameName {
		t.Fatal("preview same name")
	}
	if _, e = twin.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{}); !strings.Contains(userMessageOf(e), "Both lolads are called") {
		t.Fatal(e)
	}
	if _, _, ok := twin.Membership(); ok {
		t.Fatal("joined the hub before refusing")
	}
	// An unknown offered agent: refused, nothing joined.
	ben := newFriendLolad(t, h, "ben")
	if _, e = ben.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{Agents: []string{"ghost"}}); !strings.Contains(userMessageOf(e), "no assistant called") {
		t.Fatal(e)
	}
	if _, _, ok := ben.Membership(); ok {
		t.Fatal("joined the hub before refusing")
	}
	// Garbage is refused with a plain message.
	if _, e = ben.f.AcceptFriendInvite(ctx, "https://lolad.example/join#2.AAAA", core.AcceptRequest{}); !strings.Contains(userMessageOf(e), "doesn't look like a Lolad invite") {
		t.Fatal(e)
	}
	// A cancelled link stops working.
	if e = ana.f.CancelFriendInvite(ctx, inv.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = ben.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{}); !strings.Contains(userMessageOf(e), "no longer valid") {
		t.Fatal(e)
	}
}

func TestFriendLinkWaitsForInviter(t *testing.T) {
	h := newHub(t)
	ana := newFriendLolad(t, h, "ana", "maple")
	ctx := t.Context()
	inv, e := ana.f.CreateFriendInvite(ctx, core.FriendInviteRequest{Agents: []string{"maple"}})
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, "ana online", func() bool { return h.srv.Online("ana") })
	ana.stop()
	eventually(t, "ana offline", func() bool { return !h.srv.Online("ana") })

	ben := newFriendLolad(t, h, "ben")
	res, e := ben.f.AcceptFriendInvite(ctx, inv.Link, core.AcceptRequest{})
	if e != nil || res.State != core.FriendWaiting || !strings.Contains(res.Message, "Waiting for Ana") {
		t.Fatalf("%+v %v", res, e)
	}
	if !strings.Contains(ben.eventKinds(), "friend_waiting") {
		t.Fatal(ben.eventKinds())
	}
	// Ana comes back: the hub tells Ben, and the connection finishes alone.
	ana.client, ana.stop = startClientNoCleanupWait(ana.opts)
	eventually(t, "connected after inviter returned", func() bool {
		cs, _ := ben.f.Connections(ctx)
		return len(cs) == 1 && cs[0].UserReachable
	})
	delivered(t, ben, "ben", "maple@ana.lolad.local")
}

func TestFriendAutoHandleCollision(t *testing.T) {
	h := newHub(t)
	// Someone else already holds "ben" on this hub.
	if _, e := Register(t.Context(), nil, h.url, "ben", "", "", newSigner(t)); e != nil {
		t.Fatal(e)
	}
	ana := newFriendLolad(t, h, "ana")
	ben := newFriendLolad(t, h, "ben")
	inv, e := ana.f.CreateFriendInvite(t.Context(), core.FriendInviteRequest{})
	if e != nil {
		t.Fatal(e)
	}
	if res, e := ben.f.AcceptFriendInvite(t.Context(), inv.Link, core.AcceptRequest{}); e != nil || res.State != core.FriendConnected {
		t.Fatal(res, e)
	}
	if _, handle, _ := ben.Membership(); handle != "ben-2" {
		t.Fatal(handle)
	}
	cs, _ := ana.f.Connections(t.Context())
	if len(cs) != 1 || cs[0].URL != h.url+"/p/ben-2" {
		t.Fatal(cs)
	}
}
