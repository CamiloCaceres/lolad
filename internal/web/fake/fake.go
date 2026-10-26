// Package fake is an in-memory core.Service with a small seeded company, so
// the dashboard can be built, tested and judged without the daemon. With
// Options.Simulate it also changes agent states, sends mail and answers the
// user on its own, which keeps the live (SSE) parts of the UI moving.
package fake

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand/v2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Options configure the fake.
type Options struct {
	Simulate bool             // background activity; off for deterministic tests
	Empty    bool             // a fresh install: just the operator, no agents, no mail
	Now      func() time.Time // default time.Now
}

// Service is the fake. The zero value is not usable; call New.
type Service struct {
	now      func() time.Time
	simulate bool

	mu     sync.Mutex
	org    core.Org
	agents []*core.AgentStatus
	mail   []core.Mail // oldest first
	events []core.Event
	seq    int64
	subs   map[chan core.Event]struct{}

	stop chan struct{}
	once sync.Once
}

var _ core.Service = (*Service)(nil)

// New returns a fake seeded with a small company.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Service{
		now:      opts.Now,
		simulate: opts.Simulate,
		subs:     map[chan core.Event]struct{}{},
		stop:     make(chan struct{}),
	}
	if opts.Empty {
		s.org = core.Org{Tenant: "home", User: core.User{Name: "milo", Display: "Camilo Caceres"},
			Defaults: core.Defaults{UserToAgents: "all", AgentsToUser: "all", Vertical: true, Independents: "user-only"}}
	} else {
		s.seed()
	}
	if s.simulate {
		go s.run()
	}
	return s
}

// Close stops the simulation.
func (s *Service) Close() { s.once.Do(func() { close(s.stop) }) }

func (s *Service) addr(name string) string { return name + "@" + s.org.Tenant + ".lolad.local" }

func newID(t time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("msg_%d_%s", t.Unix(), hex.EncodeToString(b))
}

// ---------------------------------------------------------------- fleet

func (s *Service) find(name string) *core.AgentStatus {
	for _, a := range s.agents {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func (s *Service) status(a *core.AgentStatus) core.AgentStatus {
	st := *a
	st.Unread = 0
	for _, m := range s.mail {
		if m.ToName == a.Name && m.Status != core.MailRead {
			st.Unread++
		}
	}
	st.Team, st.IsHead = "", false
	if t := teamOf(s.org, a.Name); t != nil {
		st.Team, st.IsHead = t.ID, t.Head == a.Name
	}
	return st
}

func (s *Service) List(ctx context.Context) ([]core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.AgentStatus, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, s.status(a))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, name string) (core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(name)
	if a == nil {
		return core.AgentStatus{}, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	return s.status(a), nil
}

// setState changes an agent's state and records it. Caller holds mu.
func (s *Service) setState(a *core.AgentStatus, st core.State, detail string) {
	if a.State == st && a.Detail == detail {
		return
	}
	a.State, a.Detail = st, detail
	a.LastActivity = s.now()
	d := string(st)
	if detail != "" {
		d += ": " + detail
	}
	s.emit(a.Name, "state", d)
}

// later runs fn under the lock after d when simulating, right away otherwise.
func (s *Service) later(d time.Duration, fn func()) {
	if !s.simulate {
		fn()
		return
	}
	time.AfterFunc(d, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		fn()
	})
}

func (s *Service) lifecycle(name string, fn func(a *core.AgentStatus)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(name)
	if a == nil {
		return fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	fn(a)
	return nil
}

func (s *Service) boot(a *core.AgentStatus, fresh bool) {
	now := s.now()
	a.Desired, a.PID, a.StartedAt, a.LastError, a.NextRestart = "up", 40000+mrand.IntN(9999), now, "", time.Time{}
	if fresh || a.SessionSince.IsZero() {
		a.SessionSince = now
	}
	s.setState(a, core.StateStarting, "")
	s.emit(a.Name, "started", fmt.Sprintf("pid %d, %s", a.PID, map[bool]string{true: "fresh conversation", false: "resumed last conversation"}[fresh]))
	name := a.Name
	s.later(2500*time.Millisecond, func() {
		if a := s.find(name); a != nil && a.State == core.StateStarting {
			s.setState(a, core.StateIdle, "")
		}
	})
}

func (s *Service) Start(ctx context.Context, name string) error {
	return s.lifecycle(name, func(a *core.AgentStatus) {
		switch a.State {
		case core.StateStopped, core.StateFailed, core.StateCrashed:
			a.Restarts = 0
			s.boot(a, false)
		default:
			a.Desired = "up"
		}
	})
}

func (s *Service) Stop(ctx context.Context, name string) error {
	return s.lifecycle(name, func(a *core.AgentStatus) {
		a.Desired = "down"
		if a.State == core.StateStopped {
			return
		}
		s.emit(a.Name, "exited", "stopped by user (graceful)")
		a.PID, a.StartedAt, a.NextRestart = 0, time.Time{}, time.Time{}
		s.setState(a, core.StateStopped, "")
	})
}

func (s *Service) Restart(ctx context.Context, name string, fresh bool) error {
	return s.lifecycle(name, func(a *core.AgentStatus) {
		if a.State != core.StateStopped {
			s.emit(a.Name, "exited", "restart requested")
		}
		a.Restarts = 0
		s.boot(a, fresh)
		if fresh {
			s.emit(a.Name, "rotated", "fresh conversation with on_wake")
		}
	})
}

func (s *Service) Interrupt(ctx context.Context, name string) error {
	return s.lifecycle(name, func(a *core.AgentStatus) {
		s.emit(a.Name, "state", "interrupt: Escape sent")
		if a.State == core.StateBusy || a.State == core.StateWaiting {
			s.setState(a, core.StateIdle, "")
		}
	})
}

func (s *Service) Events(ctx context.Context, name string, limit int) ([]core.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []core.Event
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		if name == "" || s.events[i].Agent == name {
			out = append(out, s.events[i])
		}
	}
	return out, nil
}

// emit records an event and fans it out. Caller holds mu.
func (s *Service) emit(agent, kind, detail string) {
	s.seq++
	ev := core.Event{ID: s.seq, Agent: agent, At: s.now(), Kind: kind, Detail: detail}
	s.events = append(s.events, ev)
	for ch := range s.subs {
		select {
		case ch <- ev:
		default: // slow subscriber; it will catch up on the next refresh
		}
	}
}

func (s *Service) Subscribe(ctx context.Context) <-chan core.Event {
	ch := make(chan core.Event, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subs, ch)
		close(ch)
		s.mu.Unlock()
	}()
	return ch
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Service) register(slug, display, role, dir string, rt core.Runtime) (*core.AgentStatus, error) {
	if !slugRE.MatchString(slug) {
		return nil, fmt.Errorf("%w: %q is not a valid slug (lowercase letters, digits, dashes)", core.ErrInvalid, slug)
	}
	if s.find(slug) != nil {
		return nil, fmt.Errorf("agent %q: %w", slug, core.ErrExists)
	}
	if rt.Harness != core.HarnessClaude && rt.Harness != core.HarnessCodewhale {
		return nil, fmt.Errorf("%w: harness must be claude or codewhale", core.ErrInvalid)
	}
	a := &core.AgentStatus{
		Agent:   core.Agent{Name: slug, Display: display, Role: role, Dir: dir, Runtime: rt},
		Address: s.addr(slug), State: core.StateStopped, Desired: "down",
	}
	s.agents = append(s.agents, a)
	return a, nil
}

func (s *Service) Create(ctx context.Context, req core.CreateRequest) (core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(req.Name) == "" {
		return core.AgentStatus{}, fmt.Errorf("%w: name is required", core.ErrInvalid)
	}
	slug := cmp.Or(req.Slug, slugify(req.Name))
	dir := cmp.Or(req.Dir, filepath.Join("/Users/you/agents", slug))
	rt := core.Runtime{Harness: req.Harness, Model: req.Model, Permissions: cmp.Or(req.Permissions, core.PermBypass), OnWake: req.OnWake}
	if req.Team != "" && teamByID(s.org, req.Team) == nil {
		return core.AgentStatus{}, fmt.Errorf("%w: no team %q", core.ErrInvalid, req.Team)
	}
	a, err := s.register(slug, req.Name, cmp.Or(req.Role, "Personal Assistant"), dir, rt)
	if err != nil {
		return core.AgentStatus{}, err
	}
	s.emit(slug, "created", "setup.sh in "+dir)
	if t := teamByID(s.org, req.Team); t != nil {
		if req.Head {
			if t.Head != "" {
				t.Members = append(t.Members, t.Head)
			}
			t.Head = slug
		} else {
			t.Members = append(t.Members, slug)
		}
	}
	if req.Mission != "" {
		s.deliver(s.org.User.Name, slug, core.Outgoing{To: slug, Subject: "Your mission", Body: req.Mission, Type: "task",
			Context: map[string]any{"userchat": map[string]any{"kind": "say"}}})
	}
	if req.Start {
		s.boot(a, true)
	}
	return s.status(a), nil
}

func (s *Service) Adopt(ctx context.Context, req core.AdoptRequest) (core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(req.Dir) {
		return core.AgentStatus{}, fmt.Errorf("%w: dir must be an absolute path", core.ErrInvalid)
	}
	base := filepath.Base(req.Dir)
	slug := cmp.Or(req.Slug, slugify(base))
	if req.Runtime.Permissions == "" {
		req.Runtime.Permissions = core.PermBypass
	}
	a, err := s.register(slug, strings.ToUpper(base[:1])+base[1:], "Personal Assistant", req.Dir, req.Runtime)
	if err != nil {
		return core.AgentStatus{}, err
	}
	s.emit(slug, "created", "adopted "+req.Dir)
	return s.status(a), nil
}

func (s *Service) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit("", "state", "registry reloaded: no changes")
	return nil
}

func (s *Service) Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error) {
	s.mu.Lock()
	a := s.find(name)
	var st core.AgentStatus
	if a != nil {
		st = s.status(a)
	}
	s.mu.Unlock()
	if a == nil {
		return nil, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	if st.State == core.StateStopped || st.State == core.StateFailed {
		return nil, fmt.Errorf("agent %q: %w", name, core.ErrNotRunning)
	}
	return newTerminal(ctx, st, readOnly), nil
}

func (s *Service) Capture(ctx context.Context, name string, lines int) (string, error) {
	st, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}
	return strings.Join(screen(st), "\n"), nil
}

// ---------------------------------------------------------------- mail

func (s *Service) Me() core.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.org.User
}

func (s *Service) display(name string) string {
	if name == s.org.User.Name {
		return s.org.User.Display
	}
	if name == "lolad" {
		return "lolad"
	}
	if a := s.find(name); a != nil && a.Display != "" {
		return a.Display
	}
	return name
}

func chatKind(m core.Mail) string {
	if uc, ok := m.Context["userchat"].(map[string]any); ok {
		k, _ := uc["kind"].(string)
		return k
	}
	return ""
}

func (s *Service) Conversations(ctx context.Context) ([]core.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	me := s.org.User.Name
	by := map[string]*core.Conversation{}
	for _, m := range s.mail {
		peer := ""
		switch me {
		case m.FromName:
			peer = m.ToName
		case m.ToName:
			peer = m.FromName
		default:
			continue
		}
		c := by[peer]
		if c == nil {
			c = &core.Conversation{Peer: peer, Display: s.display(peer)}
			by[peer] = c
		}
		c.Last, c.LastAt = m, m.CreatedAt
		c.Pending = m.FromName == peer && chatKind(m) == "ask"
		if m.FromName == peer && m.Status != core.MailRead {
			c.Unread++
		}
	}
	out := make([]core.Conversation, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b core.Conversation) int { return b.LastAt.Compare(a.LastAt) })
	return out, nil
}

func (s *Service) Conversation(ctx context.Context, peer string, limit int, before time.Time) ([]core.Mail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	me := s.org.User.Name
	var out []core.Mail
	for i := len(s.mail) - 1; i >= 0 && len(out) < limit; i-- {
		m := s.mail[i]
		if !before.IsZero() && !m.CreatedAt.Before(before) {
			continue
		}
		if (m.FromName == me && m.ToName == peer) || (m.FromName == peer && m.ToName == me) {
			out = append(out, m)
		}
	}
	slices.Reverse(out)
	return out, nil
}

// deliver stores a message after the policy check. Caller holds mu.
func (s *Service) deliver(from, to string, out core.Outgoing) (core.Mail, error) {
	if d := check(s.org, from, to); !d.Allow {
		s.emit(from, "policy", fmt.Sprintf("denied %s → %s: %s", from, to, d.Reason))
		return core.Mail{}, fmt.Errorf("%w: %s", core.ErrDenied, d.Reason)
	}
	now := s.now()
	m := core.Mail{
		ID: newID(now), InReplyTo: out.InReplyTo,
		From: s.addr(from), To: s.addr(to), FromName: from, ToName: to,
		Subject: out.Subject, Priority: cmp.Or(out.Priority, "normal"), Type: cmp.Or(out.Type, "notification"),
		Body: out.Body, Context: out.Context, Signed: true, Verified: true, CreatedAt: now, Status: core.MailUnread,
	}
	m.ThreadID = m.ID
	if out.InReplyTo != "" {
		for _, p := range s.mail {
			if p.ID == out.InReplyTo {
				m.ThreadID = p.ThreadID
			}
		}
	}
	s.mail = append(s.mail, m)
	if a := s.find(from); a != nil {
		a.LastActivity = now
	}
	s.emit(cmp.Or(s.agentOf(to), s.agentOf(from)), "mail", fmt.Sprintf("%s → %s: %s", from, to, m.Subject))
	return m, nil
}

func (s *Service) agentOf(name string) string {
	if s.find(name) != nil {
		return name
	}
	return ""
}

func (s *Service) UserSend(ctx context.Context, out core.Outgoing) (core.Mail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	to := strings.SplitN(strings.ToLower(out.To), "@", 2)[0]
	if s.find(to) == nil {
		return core.Mail{}, fmt.Errorf("recipient %q: %w", to, core.ErrNotFound)
	}
	if strings.TrimSpace(out.Body) == "" {
		return core.Mail{}, fmt.Errorf("%w: empty message", core.ErrInvalid)
	}
	// Same limit as the real provider.
	if n := utf8.RuneCountInString(out.Subject); n < 1 || n > 256 {
		return core.Mail{}, fmt.Errorf("%w: Subject must have 1–256 characters", core.ErrInvalid)
	}
	m, err := s.deliver(s.org.User.Name, to, out)
	if err == nil && s.simulate {
		s.scheduleReply(to, m)
	}
	return m, err
}

// AgentSend sends mail as an agent, the way `lolad msg` does, subject to the
// policy. For tests and demos.
func (s *Service) AgentSend(ctx context.Context, from string, out core.Outgoing) (core.Mail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.find(from) == nil {
		return core.Mail{}, fmt.Errorf("agent %q: %w", from, core.ErrNotFound)
	}
	return s.deliver(from, strings.ToLower(out.To), out)
}

func (s *Service) MarkRead(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.mail {
		if s.mail[i].ID == id {
			if s.mail[i].ToName != s.org.User.Name {
				return fmt.Errorf("%w: not addressed to the user", core.ErrInvalid)
			}
			if s.mail[i].Status != core.MailRead {
				s.mail[i].Status, s.mail[i].ReadAt = core.MailRead, s.now()
			}
			return nil
		}
	}
	return fmt.Errorf("message %q: %w", id, core.ErrNotFound)
}

func (s *Service) Traffic(ctx context.Context, limit int, before time.Time) ([]core.Mail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []core.Mail
	for i := len(s.mail) - 1; i >= 0 && len(out) < limit; i-- {
		if before.IsZero() || s.mail[i].CreatedAt.Before(before) {
			out = append(out, s.mail[i])
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- org

func cloneOrg(o core.Org) core.Org {
	o.Teams = slices.Clone(o.Teams)
	for i := range o.Teams {
		o.Teams[i].Members = slices.Clone(o.Teams[i].Members)
	}
	o.Rules = slices.Clone(o.Rules)
	return o
}

func (s *Service) Org(ctx context.Context) (core.Org, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneOrg(s.org), nil
}

func (s *Service) SaveOrg(ctx context.Context, org core.Org) error {
	if err := validate(org); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.org = cloneOrg(org)
	s.emit("", "state", "org saved: policy reloaded")
	return nil
}

func (s *Service) Check(ctx context.Context, from, to string) core.Decision {
	s.mu.Lock()
	defer s.mu.Unlock()
	return check(s.org, from, to)
}
