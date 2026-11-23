package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/hub"
)

// Hub membership (SPEC.md "Hub (v5)"): hub.yaml next to config.yaml, a tunnel
// client that serves only the federation handler, and the effective
// federation URL <hub>/p/<handle> while joined.

var _ core.Hubber = (*App)(nil)

type hubRuntime struct {
	mu     sync.Mutex
	runCtx context.Context // set by Run; nil until the daemon runs
	member *hub.Membership
	client *hub.Client
	cancel context.CancelFunc
	done   chan struct{}
	last   string // last recorded state, to avoid an event per retry
}

func (a *App) hubFile() string { return hub.MembershipPath(filepath.Dir(a.configPath)) }

// initHub loads hub.yaml and, when joined, makes the member URL the
// federation URL. The tunnel starts in Run.
func (a *App) initHub() error {
	a.hub = &hubRuntime{}
	a.Engine.SetHub(hubMember{a}, a.Config.Federation.HubURL, a.Config.Federation.JoinURL)
	m, err := hub.LoadMembership(a.hubFile())
	if err != nil {
		return err
	}
	if m == nil {
		return nil
	}
	if err = a.Engine.SetURL(hub.MemberURL(m.URL, m.Handle)); err != nil {
		return err
	}
	a.hub.member = m
	return nil
}

// runHub starts the tunnel (if joined) for the lifetime of ctx.
func (a *App) runHub(ctx context.Context) {
	h := a.hub
	h.mu.Lock()
	h.runCtx = ctx
	m := h.member
	h.mu.Unlock()
	if m != nil && a.Engine.Enabled() {
		if err := a.startTunnel(*m); err != nil {
			a.RecordEvent(core.Event{Kind: "error", Detail: "hub: " + err.Error()})
		}
	}
	<-ctx.Done()
	a.stopTunnel()
}

func (a *App) startTunnel(m hub.Membership) error {
	h := a.hub
	c, err := hub.NewClient(hub.ClientOptions{
		HubURL: m.URL, Handle: m.Handle,
		Signer:  hub.Signer{PublicKey: a.Engine.ProviderPublicKey(), Sign: a.Engine.ProviderSign},
		Handler: a.Engine.Handler(), // federation only, never the daemon mux
		Peers:   a.Engine.PeerURLs,
		OnPeerOnline: func(u string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.Engine.RetryURL(ctx, u)
		},
		OnState: a.hubStateChanged,
		Logger:  slog.Default(),
	})
	if err != nil {
		return err
	}
	// Stop any old tunnel without holding mu: its OnState callback takes mu.
	a.stopTunnel()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runCtx == nil { // not running yet; Run starts it
		return nil
	}
	ctx, cancel := context.WithCancel(h.runCtx)
	done := make(chan struct{})
	h.client, h.cancel, h.done = c, cancel, done
	go func() { defer close(done); c.Run(ctx) }()
	return nil
}

func (a *App) stopTunnel() {
	h := a.hub
	h.mu.Lock()
	cancel, done := h.cancel, h.done
	h.client, h.cancel, h.done = nil, nil, nil
	h.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (a *App) hubStateChanged(s core.HubStatus) {
	h := a.hub
	h.mu.Lock()
	prev := h.last
	h.last = s.State
	h.mu.Unlock()
	switch {
	case s.State == core.HubConnected && prev != core.HubConnected:
		a.RecordEvent(core.Event{Kind: "federation", Detail: "Hub connected: " + hub.MemberURL(s.URL, s.Handle)})
	case s.State == core.HubDisabled && prev != core.HubDisabled:
		a.RecordEvent(core.Event{Kind: "error", Detail: "Hub: " + s.Error})
	case s.State == core.HubError && prev == core.HubConnected:
		a.RecordEvent(core.Event{Kind: "federation", Detail: "Hub disconnected: " + s.Error})
	}
}

// Hub reports membership; nil when not joined.
func (a *App) Hub(context.Context) (*core.HubStatus, error) {
	h := a.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.member == nil {
		return nil, nil
	}
	if h.client != nil {
		s := h.client.Status()
		return &s, nil
	}
	s := core.HubStatus{URL: h.member.URL, Handle: h.member.Handle, State: core.HubConnecting}
	if !a.Engine.Enabled() {
		s.State, s.Error = core.HubError, "federation is disabled"
	}
	return &s, nil
}

// Info adds hub status to the engine's federation info.
func (a *App) Info(ctx context.Context) core.FederationInfo {
	v := a.Engine.Info(ctx)
	if s, _ := a.Hub(ctx); s != nil {
		v.Hub = s
		if v.Problem == "" && s.State != core.HubConnected && s.Error != "" {
			v.Problem = "Hub: " + s.Error
		}
	}
	return v
}

// JoinHub registers with the hub using a sign-up code and starts the tunnel.
func (a *App) JoinHub(ctx context.Context, rawURL, handle, code string) (*core.HubStatus, error) {
	if !a.Engine.Enabled() {
		return nil, fmt.Errorf("%w: federation is disabled", core.ErrDenied)
	}
	u, err := hub.NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !hub.ValidHandle(handle) {
		return nil, fmt.Errorf("%w: handle must be 3-32 of a-z, 0-9 and - (not at the ends)", core.ErrInvalid)
	}
	if err = a.checkNotOtherHub(u, handle); err != nil {
		return nil, err
	}
	if _, err = hub.Register(ctx, nil, u, handle, a.Engine.Info(ctx).Display, code, a.hubSigner()); err != nil {
		var he *hub.Error
		if errors.As(err, &he) {
			return nil, fmt.Errorf("%w: %s", core.ErrInvalid, he.Error())
		}
		return nil, err
	}
	if err = a.joined(ctx, u, handle); err != nil {
		return nil, err
	}
	return a.Hub(ctx)
}

func (a *App) hubSigner() hub.Signer {
	return hub.Signer{PublicKey: a.Engine.ProviderPublicKey(), Sign: a.Engine.ProviderSign}
}

// checkNotOtherHub refuses joining while already on another hub or handle.
func (a *App) checkNotOtherHub(u, handle string) error {
	h := a.hub
	h.mu.Lock()
	cur := h.member
	h.mu.Unlock()
	if cur != nil && (cur.URL != u || handle != "" && cur.Handle != handle) {
		return fmt.Errorf("%w: already on %s as %s; leave first", core.ErrInvalid, cur.URL, cur.Handle)
	}
	return nil
}

// joined records membership after a successful registration: hub.yaml, the
// member URL as the federation URL, and the tunnel.
func (a *App) joined(ctx context.Context, u, handle string) error {
	h := a.hub
	h.mu.Lock()
	cur := h.member
	h.mu.Unlock()
	m := hub.Membership{URL: u, Handle: handle, JoinedAt: time.Now().UTC()}
	if cur != nil {
		m.JoinedAt = cur.JoinedAt
	}
	if err := hub.SaveMembership(a.hubFile(), m); err != nil {
		return err
	}
	if err := a.Engine.SetURL(hub.MemberURL(u, handle)); err != nil {
		return err
	}
	h.mu.Lock()
	h.member = &m
	h.mu.Unlock()
	if err := a.startTunnel(m); err != nil {
		return err
	}
	a.RecordEvent(core.Event{Kind: "federation", Detail: "Joined hub " + u + " as " + handle})
	return nil
}

// hubMember lets fed join a hub on its own for friend links (open sign-up)
// without importing the hub package. See docs/friend-connect-design.md §3.3.
type hubMember struct{ a *App }

func (m hubMember) Membership() (string, string, bool) {
	h := m.a.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.member == nil {
		return "", "", false
	}
	return h.member.URL, h.member.Handle, true
}

// joinError is a hub refusal written for people (fed shows UserMessage).
type joinError struct{ msg string }

func (e joinError) Error() string       { return core.ErrInvalid.Error() + ": " + e.msg }
func (e joinError) Unwrap() error       { return core.ErrInvalid }
func (e joinError) UserMessage() string { return e.msg }

// Join signs up on hubURL with no code, picking a handle from preferred:
// the hub's suggestion when taken, the handle this key already holds when it
// has one, up to four tries.
func (m hubMember) Join(ctx context.Context, rawURL, preferred string) (string, error) {
	a := m.a
	if !a.Engine.Enabled() {
		return "", joinError{"Connections are turned off in this Lolad's settings."}
	}
	u, err := hub.NormalizeURL(rawURL)
	if err != nil {
		return "", err
	}
	if cur, handle, ok := m.Membership(); ok {
		if cur == u {
			return hub.MemberURL(u, handle), nil
		}
		return "", joinError{"This Lolad is already on another network. Leave it under Sharing, Advanced, first."}
	}
	unreachable := joinError{"Lolad couldn't reach its network. Check your internet connection and try again."}
	info, err := hub.Info(ctx, nil, u)
	if err != nil {
		return "", unreachable
	}
	switch info.Signup {
	case hub.SignupInvite:
		return "", joinError{"This network is invite-only. Join it under Sharing, Advanced, with a sign-up code from its operator."}
	case hub.SignupClosed:
		return "", joinError{"The Lolad network isn't taking new people right now. Try again later."}
	}
	handle := autoHandle(preferred)
	display := a.Engine.Info(ctx).Display
	for try := 0; try < 4; try++ {
		_, err = hub.Register(ctx, nil, u, handle, display, "", a.hubSigner())
		if err == nil {
			if err = a.joined(ctx, u, handle); err != nil {
				return "", err
			}
			return hub.MemberURL(u, handle), nil
		}
		var he *hub.Error
		if !errors.As(err, &he) {
			return "", unreachable
		}
		switch {
		case he.Code == "key_registered" && he.Handle != "":
			handle = he.Handle // this lolad already has a place here
		case (he.Code == "name_taken" || he.Code == "invalid_handle") && he.Suggestion != "":
			handle = he.Suggestion
		case he.Code == "name_taken" || he.Code == "invalid_handle":
			handle = fmt.Sprintf("%s-%d", autoHandle(preferred)[:min(len(autoHandle(preferred)), 28)], try+2)
		case he.Code == "signup_closed":
			return "", joinError{"The Lolad network isn't taking new people right now. Try again later."}
		case he.Code == "rate_limited":
			return "", joinError{"Too many new Lolads joined from your internet connection today. Try again tomorrow."}
		case he.Code == "signup_refused":
			return "", joinError{"This Lolad can't join the network. Contact its operator if you think this is a mistake."}
		case he.Code == "invalid_invite":
			return "", joinError{"This network is invite-only. Join it under Sharing, Advanced, with a sign-up code from its operator."}
		default:
			return "", joinError{"The Lolad network refused to add this Lolad: " + he.Message}
		}
	}
	return "", joinError{"Lolad couldn't find a free name on the network. Try again."}
}

// autoHandle makes a hub handle from a tenant: kept when valid, otherwise
// cleaned up and padded; reserved names get "-1".
func autoHandle(tenant string) string {
	var b []byte
	for _, r := range tenant {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b = append(b, byte(r))
		case r >= 'A' && r <= 'Z':
			b = append(b, byte(r-'A'+'a'))
		}
	}
	h := strings.Trim(string(b), "-")
	if len(h) > 30 {
		h = strings.TrimRight(h[:30], "-")
	}
	for len(h) < 3 {
		h += "0"
	}
	if !hub.ValidHandle(h) {
		h = strings.TrimRight(h[:min(len(h), 29)], "-") + "-1"
	}
	return h
}

// LeaveHub releases the handle (best effort when the hub is unreachable),
// closes the tunnel and restores the configured federation URL.
func (a *App) LeaveHub(ctx context.Context) error {
	h := a.hub
	h.mu.Lock()
	m := h.member
	h.mu.Unlock()
	if m == nil {
		return core.ErrNotFound
	}
	a.stopTunnel()
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := hub.Leave(lctx, nil, m.URL, m.Handle, hub.Signer{PublicKey: a.Engine.ProviderPublicKey(), Sign: a.Engine.ProviderSign})
	cancel()
	if e := hub.RemoveMembership(a.hubFile()); e != nil {
		return e
	}
	h.mu.Lock()
	h.member = nil
	h.last = ""
	h.mu.Unlock()
	_ = a.Engine.SetURL(a.Config.Federation.URL)
	detail := "Left hub " + m.URL
	if err != nil {
		detail += " (the hub did not confirm: " + err.Error() + "; its operator can remove " + m.Handle + ")"
	}
	a.RecordEvent(core.Event{Kind: "federation", Detail: detail})
	return nil
}
