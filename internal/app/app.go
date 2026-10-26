// Package app composes the daemon's services and owns their lifetime.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/create"
	"github.com/CamiloCaceres/lolad/internal/deliver"
	"github.com/CamiloCaceres/lolad/internal/fed"
	"github.com/CamiloCaceres/lolad/internal/harness"
	"github.com/CamiloCaceres/lolad/internal/policy"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"github.com/CamiloCaceres/lolad/internal/store"
	"github.com/CamiloCaceres/lolad/internal/supervisor"
	"github.com/CamiloCaceres/lolad/internal/tmux"
)

type App struct {
	*fed.Engine
	Auth *auth.Store
	*supervisor.Supervisor
	Registry   *registry.Registry
	Provider   *amp.Provider
	Policy     *policy.Store
	Tracker    *deliver.Tracker
	Creator    *create.Creator
	DB         *sql.DB
	Config     registry.Config
	configPath string
	mutate     sync.Mutex
}

var _ core.Service = (*App)(nil)

type deferredStarter struct{}

func (deferredStarter) Start(context.Context, string) error { return nil }

type registrar struct{ a *App }

func (r registrar) Reload(ctx context.Context) error { _, e := r.a.reloadRegistry(ctx); return e }

func New(ctx context.Context, configPath string) (_ *App, err error) {
	if configPath == "" {
		configPath = registry.ConfigPath()
	}
	r, err := registry.New(configPath)
	if err != nil {
		return nil, err
	}
	a := &App{Registry: r, Config: r.Config(), configPath: configPath}
	a.DB, err = store.Open(filepath.Join(a.Config.StateDir, "lolad.db"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			a.DB.Close()
		}
	}()
	a.Auth, err = auth.New(a.DB)
	if err != nil {
		return nil, err
	}
	a.Policy, err = policy.Open(filepath.Join(filepath.Dir(configPath), "org.yaml"), a.names()...)
	if err != nil {
		return nil, err
	}
	a.Provider, err = amp.New(a.DB, a.Config.StateDir, a.Policy.Org().Tenant, a.Policy, amp.WithAgents(r.List), amp.WithOrg(a.Policy.Org), amp.WithEventSink(func(e core.Event) {
		if a.Supervisor != nil {
			a.RecordEvent(e)
		}
	}))
	if err != nil {
		return nil, err
	}
	if err = a.identities(ctx); err != nil {
		return nil, err
	}
	sessions, err := tmux.New(a.Config.StateDir, a.Config.TmuxSocket)
	if err != nil {
		return nil, err
	}
	if a.Config.TmuxBin != "" {
		sessions.Binary = a.Config.TmuxBin
	}
	claudeBin := a.Config.ClaudeBin
	if claudeBin == "" {
		home, _ := os.UserHomeDir()
		p := filepath.Join(home, ".local/bin/claude")
		if _, e := os.Stat(p); e == nil {
			claudeBin = p
		}
	}
	launchers := map[core.Harness]core.Launcher{core.HarnessClaude: harness.New(core.HarnessClaude, claudeBin), core.HarnessCodewhale: harness.New(core.HarnessCodewhale, "")}
	a.Tracker = deliver.NewTracker(nil)
	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sessions.Exec = bin
	a.Supervisor, err = supervisor.New(supervisor.Options{DB: a.DB, Agents: r.List(), Sessions: sessions, Launchers: launchers, Notifier: deliver.NewRouter(sessions, launchers, a.Tracker.State), Mailer: a.Provider, Launch: core.LaunchOpts{StateDir: a.Config.StateDir, LoladBin: bin, LoladURL: URL(a.Config.Listen)}, ReloadAgents: a.reloadRegistry})
	if err != nil {
		return nil, err
	}
	a.Tracker.Sink = a.Supervisor
	if err = a.initFederation(); err != nil {
		return nil, err
	}
	a.Creator = create.New(create.Deps{TemplateDir: a.Config.LolabotTemplate, AgentsDir: a.Config.NewAgentsDir, Runtime: runtimeWriter{a}, Identity: a.Provider, Org: a, Contacts: a.Policy, Mail: a.Provider, Starter: deferredStarter{}, Registrar: registrar{a}})
	for _, e := range r.Diagnostics() {
		a.RecordEvent(e)
	}
	a.Provider.OnMail(func(m core.Mail) {
		if m.ToName == a.Me().Name {
			a.RecordEvent(core.Event{Agent: m.FromName, Kind: "mail", Detail: m.ID})
		}
		u := a.Me()
		if m.ToName == u.Name && u.Ntfy != "" {
			pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, e := http.NewRequestWithContext(pushCtx, http.MethodPost, u.Ntfy, strings.NewReader(m.Body))
			if e == nil {
				req.Header.Set("Title", m.FromName+": "+m.Subject)
				resp, e := http.DefaultClient.Do(req)
				if e == nil {
					resp.Body.Close()
					if resp.StatusCode >= 300 {
						e = fmt.Errorf("ntfy: %s", resp.Status)
					}
				}
				if e != nil {
					a.RecordEvent(core.Event{Kind: "error", Detail: e.Error()})
				}
			}
		}
	})
	return a, nil
}
func URL(listen string) string {
	host, port, e := net.SplitHostPort(listen)
	if e == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		return "http://127.0.0.1:" + port
	}
	return "http://" + listen
}
func (a *App) Close() error { mailManagers.Delete(a); return a.DB.Close() }
func (a *App) names() []string {
	var n []string
	for _, v := range a.Registry.List() {
		n = append(n, v.Name)
	}
	return n
}
func (a *App) identities(ctx context.Context) error {
	for _, n := range a.names() {
		if _, e := a.Provider.EnsureIdentity(ctx, n, "agent"); e != nil {
			return e
		}
	}
	for n, k := range map[string]string{a.Me().Name: "human", "lolad": "system"} {
		if _, e := a.Provider.EnsureIdentity(ctx, n, k); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) reloadRegistry(ctx context.Context) ([]core.Agent, error) {
	d, e := a.Registry.Reload()
	if e != nil {
		return nil, e
	}
	a.Policy.SetKnown(a.names()...)
	if e = a.identities(ctx); e != nil {
		return nil, e
	}
	for _, ev := range d.Rejected {
		a.RecordEvent(ev)
	}
	return a.Registry.List(), nil
}
func (a *App) Reload(ctx context.Context) error {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	if e := a.Supervisor.Reload(ctx); e != nil {
		return e
	}
	if e := a.Policy.Reload(); e != nil {
		return e
	}
	return a.regenerate()
}
func (a *App) Create(ctx context.Context, r core.CreateRequest) (core.AgentStatus, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, e := a.Creator.Create(ctx, r)
	if e != nil {
		return v, e
	}
	if e = a.Supervisor.Reload(ctx); e != nil {
		return v, e
	}
	if unread, err := a.Provider.Unread(ctx, v.Name); err == nil {
		for _, m := range unread {
			a.Supervisor.OnMail(m)
		}
	}
	if r.Start {
		if e = a.Start(ctx, v.Name); e != nil {
			return v, e
		}
	}
	a.RecordEvent(core.Event{Agent: v.Name, Kind: "created", Detail: v.Dir})
	return a.Get(ctx, v.Name)
}
func (a *App) Adopt(ctx context.Context, r core.AdoptRequest) (core.AgentStatus, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, e := a.Creator.Adopt(ctx, r)
	if e != nil {
		return v, e
	}
	if e = a.Supervisor.Reload(ctx); e != nil {
		return v, e
	}
	return a.Get(ctx, v.Name)
}
func (a *App) Me() core.User                         { return a.Policy.Org().User }
func (a *App) Org(context.Context) (core.Org, error) { return a.Policy.Org(), nil }
func (a *App) SaveOrg(ctx context.Context, o core.Org) error {
	old := a.Policy.Org()
	if o.Tenant != old.Tenant || o.User.Name != old.User.Name {
		return fmt.Errorf("%w: tenant and user name require a daemon restart", core.ErrInvalid)
	}
	if _, e := policy.Validate(o, a.names()...); e != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalid, e)
	}
	if e := a.Policy.Save(o); e != nil {
		return e
	}
	return a.regenerate()
}
func (a *App) regenerate() error {
	var errs []error
	for _, v := range a.Registry.List() {
		if e := a.Creator.RegenerateSection(v); e != nil {
			errs = append(errs, e)
			a.RecordEvent(core.Event{Agent: v.Name, Kind: "error", Detail: e.Error()})
		}
	}
	return errors.Join(errs...)
}
func (a *App) Check(_ context.Context, f, t string) core.Decision { return a.Provider.Check(f, t) }
func (a *App) UserSend(ctx context.Context, o core.Outgoing) (core.Mail, error) {
	return a.Provider.Send(ctx, a.Me().Name, o)
}
func (a *App) MarkRead(ctx context.Context, id string) error {
	return a.Provider.MarkRead(ctx, a.Me().Name, id)
}
func (a *App) Traffic(ctx context.Context, n int, b time.Time) ([]core.Mail, error) {
	return a.Provider.Traffic(ctx, n, b)
}
func (a *App) Conversation(ctx context.Context, peer string, n int, b time.Time) ([]core.Mail, error) {
	m, e := a.Provider.List(ctx, a.Me().Name, core.MailFilter{Box: "all", Peer: peer, Limit: n, Before: b})
	if e == nil {
		sort.Slice(m, func(i, j int) bool { return m[i].CreatedAt.Before(m[j].CreatedAt) })
	}
	return m, e
}
func (a *App) Conversations(ctx context.Context) ([]core.Conversation, error) {
	// Page through the user's entire mailbox so quiet peers are never hidden by a busy thread.
	by := map[string]*core.Conversation{}
	before := time.Time{}
	user := a.Me().Name
	for {
		mail, e := a.Provider.List(ctx, user, core.MailFilter{Box: "all", Limit: 1000, Before: before})
		if e != nil {
			return nil, e
		}
		if len(mail) == 0 {
			break
		}
		for _, m := range mail {
			peer := m.FromName
			if peer == user {
				peer = m.ToName
			}
			c := by[peer]
			if c == nil {
				c = &core.Conversation{Peer: peer, Display: peer, Last: m, LastAt: m.CreatedAt}
				if ag, e := a.Registry.Get(peer); e == nil {
					c.Display = ag.Display
				}
				if uc, ok := m.Context["userchat"].(map[string]any); ok {
					c.Pending = m.ToName == user && uc["kind"] == "ask"
				}
				by[peer] = c
			}
			if m.ToName == user && m.Status != core.MailRead {
				c.Unread++
			}
		}
		next := mail[len(mail)-1].CreatedAt
		if len(mail) < 1000 || next.Equal(before) {
			break
		}
		before = next
	}
	out := make([]core.Conversation, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, nil
}
func (a *App) List(ctx context.Context) ([]core.AgentStatus, error) {
	v, e := a.Supervisor.List(ctx)
	for i := range v {
		a.decorate(&v[i])
	}
	return v, e
}
func (a *App) Get(ctx context.Context, n string) (core.AgentStatus, error) {
	v, e := a.Supervisor.Get(ctx, n)
	if e == nil {
		a.decorate(&v)
	}
	return v, e
}
func (a *App) decorate(v *core.AgentStatus) {
	// Runtime environment values are write-only, including on general status routes.
	v.Runtime.Env = nil
	v.Address = v.Name + "@" + a.Provider.Name()
	for _, t := range a.Policy.Org().Teams {
		if t.Head == v.Name {
			v.Team = t.ID
			v.IsHead = true
		}
		for _, m := range t.Members {
			if m == v.Name {
				v.Team = t.ID
			}
		}
	}
}
