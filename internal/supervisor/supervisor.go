// Package supervisor owns desired state, lifecycle reconciliation and mail scheduling.
package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}
type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options contains only injected runtime capabilities. DB must be an opened SQLite database.
// ReloadAgents lets the integrator adapt its registry without coupling runtime to discovery.
type Options struct {
	DB           *sql.DB
	Agents       []core.Agent
	Sessions     core.Sessions
	Launchers    map[core.Harness]core.Launcher
	Notifier     core.Notifier
	Mailer       core.Mailer
	Clock        Clock
	Launch       core.LaunchOpts
	ReloadAgents func(context.Context) ([]core.Agent, error)
	MailWatch    MailWatch
}
type record struct {
	Status         core.AgentStatus
	EverStarted    bool
	Fresh          bool
	Crashes        []time.Time
	RestartTimes   []time.Time
	Seq            int64
	HookSession    string
	LastHook       string
	StopAt         time.Time
	Restarting     bool
	RotationDue    time.Time
	StartupAt      time.Time
	StartupDialogs map[string]bool
	RotatedDay     string
}
type pending struct {
	Mail          core.Mail
	Attempts      int
	Next          time.Time
	Confirmations int
	LastNotice    time.Time
	LastBlock     time.Time
}
type Supervisor struct {
	mu          sync.Mutex
	triggerWG   sync.WaitGroup
	opts        Options
	agents      map[string]*record
	queues      map[string]map[string]*pending
	subscribers map[chan core.Event]struct{}
	history     []core.Event
	nextEvent   int64
	lastSweep   time.Time
	triggers    map[string]*triggerState
}

var _ core.HookSink = (*Supervisor)(nil)

func New(o Options) (*Supervisor, error) {
	if o.Sessions == nil {
		return nil, fmt.Errorf("sessions required")
	}
	if o.Clock == nil {
		o.Clock = wallClock{}
	}
	if o.MailWatch == nil {
		o.MailWatch = ExecMailWatch{}
	}
	s := &Supervisor{opts: o, agents: map[string]*record{}, queues: map[string]map[string]*pending{}, subscribers: map[chan core.Event]struct{}{}, triggers: map[string]*triggerState{}}
	if o.DB != nil {
		if _, e := o.DB.Exec(`CREATE TABLE IF NOT EXISTS sup_agents (name TEXT PRIMARY KEY, desired TEXT NOT NULL, data TEXT NOT NULL); CREATE TABLE IF NOT EXISTS sup_events (id INTEGER PRIMARY KEY AUTOINCREMENT, agent TEXT NOT NULL, at TEXT NOT NULL, kind TEXT NOT NULL, detail TEXT NOT NULL); CREATE TABLE IF NOT EXISTS sup_mail (id TEXT PRIMARY KEY, agent TEXT NOT NULL, data TEXT NOT NULL)`); e != nil {
			return nil, e
		}
	}
	for _, a := range o.Agents {
		r := &record{Status: core.AgentStatus{Agent: a, State: core.StateStopped, Desired: "down"}}
		if a.Runtime.AutostartEnabled() {
			r.Status.Desired = "up"
		}
		if o.DB != nil {
			var data string
			e := o.DB.QueryRow(`SELECT data FROM sup_agents WHERE name=?`, a.Name).Scan(&data)
			if e == nil {
				if e = json.Unmarshal([]byte(data), r); e != nil {
					return nil, e
				}
				r.Status.Agent = a
			} else if !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
		}
		s.agents[a.Name] = r
		s.queues[a.Name] = map[string]*pending{}
		if o.Sessions.Exists(a.Name) {
			dead, _, e := o.Sessions.PaneDead(a.Name)
			if e != nil {
				return nil, e
			}
			if !dead {
				r.EverStarted = true
				r.StopAt = time.Time{}
				// A surviving active turn is not a first-run blocker. Keep its
				// persisted busy signal; inspect other adopted panes promptly.
				if r.Status.State != core.StateBusy {
					r.Status.State = core.StateStarting
				}
				r.StartupAt = o.Clock.Now()
				r.StartupDialogs = nil
				r.Status.NextRestart = time.Time{}
				r.Status.PID, _ = o.Sessions.PanePID(a.Name)
				s.event(a.Name, "adopted", "adopted existing session")
			}
		}
		if e := s.persist(r); e != nil {
			return nil, e
		}
	}
	if e := s.configureTriggers(); e != nil {
		return nil, e
	}
	if o.Mailer != nil {
		o.Mailer.OnMail(s.OnMail)
	}
	return s, nil
}
func (s *Supervisor) persist(r *record) error {
	if s.opts.DB == nil {
		return nil
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.opts.DB.Exec(`INSERT INTO sup_agents(name,desired,data) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET desired=excluded.desired,data=excluded.data`, r.Status.Name, r.Status.Desired, string(b))
	return e
}
func (s *Supervisor) event(name, kind, detail string) {
	ev := core.Event{Agent: name, At: s.opts.Clock.Now(), Kind: kind, Detail: detail}
	s.nextEvent++
	ev.ID = s.nextEvent
	if s.opts.DB != nil {
		res, e := s.opts.DB.Exec(`INSERT INTO sup_events(agent,at,kind,detail) VALUES(?,?,?,?)`, name, ev.At.Format(time.RFC3339Nano), kind, detail)
		if e == nil {
			ev.ID, _ = res.LastInsertId()
		}
	}
	s.history = append(s.history, ev)
	if len(s.history) > 1000 {
		s.history = s.history[len(s.history)-1000:]
	}
	for ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}
func (s *Supervisor) setState(r *record, state core.State, detail string) {
	if r.Status.State != state {
		r.Status.State = state
		s.event(r.Status.Name, "state", string(state))
	}
	r.Status.Detail = detail
}
func (s *Supervisor) Run(ctx context.Context) error {
	defer s.triggerWG.Wait()
	for {
		if e := s.Reconcile(ctx); e != nil {
			s.mu.Lock()
			s.event("", "error", "reconcile: "+e.Error())
			s.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.opts.Clock.After(2 * time.Second):
		}
	}
}

// Reconcile executes one deterministic tick; Run supplies the ordinary two-second loop.
func (s *Supervisor) Reconcile(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	now := s.opts.Clock.Now()
	var first error
	for _, r := range s.agents {
		if e := s.reconcileAgent(ctx, r, now); e != nil {
			r.Status.LastError = e.Error()
			s.event(r.Status.Name, "error", e.Error())
			if first == nil {
				first = e
			}
		}
		if e := s.persist(r); e != nil && first == nil {
			first = e
		}
	}
	sweep := s.lastSweep.IsZero() || now.Sub(s.lastSweep) >= time.Minute
	if sweep {
		s.lastSweep = now
	}
	s.mu.Unlock()
	if sweep {
		s.sweep(ctx)
	}
	s.runTriggers(ctx, now)
	return first
}
func (s *Supervisor) reconcileAgent(ctx context.Context, r *record, now time.Time) error {
	name := r.Status.Name
	exists := s.opts.Sessions.Exists(name)
	if !r.StopAt.IsZero() {
		if !exists || !now.Before(r.StopAt) {
			if exists {
				if e := s.opts.Sessions.Kill(name); e != nil {
					return e
				}
			}
			r.StopAt = time.Time{}
			s.setState(r, core.StateStopped, "")
			s.event(name, "stopped", "graceful stop complete")
			exists = false
		} else {
			return nil
		}
	}
	if r.Status.Desired == "down" {
		if exists {
			return s.beginStop(r)
		}
		s.setState(r, core.StateStopped, "")
		return nil
	}
	if r.Status.State == core.StateFailed {
		return nil
	}
	if exists {
		dead, exit, e := s.opts.Sessions.PaneDead(name)
		if e != nil {
			return e
		}
		if dead {
			if e = s.opts.Sessions.Kill(name); e != nil {
				return e
			}
			s.crash(r, now, fmt.Sprintf("exit %d", exit))
			return nil
		}
	}
	if !exists {
		if r.EverStarted && r.Status.State != core.StateCrashed && r.Status.State != core.StateStopped && !r.Restarting {
			s.crash(r, now, "session disappeared")
			return nil
		}
		if now.Before(r.Status.NextRestart) {
			return nil
		}
		return s.launch(r)
	}
	if r.Restarting {
		return s.beginStop(r)
	}
	if r.Status.State == core.StateStarting {
		if err := s.checkStartup(r, now); err != nil {
			return err
		}
	}
	if r.Status.State != core.StateStarting && !strings.HasPrefix(r.Status.Detail, "startup blocked:") && now.Sub(r.Status.LastActivity) > 15*time.Minute {
		pane, e := s.opts.Sessions.Capture(name, 40, false)
		if e != nil {
			return e
		}
		l := s.opts.Launchers[r.Status.Runtime.Harness]
		if l != nil && l.PromptReady(pane) {
			s.setState(r, core.StateIdle, "")
		} else if r.LastHook != "prompt_submit" && r.LastHook != "pre_tool" {
			s.setState(r, core.StateUnknown, "")
		}
	}
	if r.Status.Runtime.Rotate != "" {
		day := now.Format("2006-01-02")
		at, e := time.ParseInLocation("2006-01-02 15:04", day+" "+r.Status.Runtime.Rotate, now.Location())
		if e == nil && !now.Before(at) && r.RotatedDay != day {
			if r.RotationDue.IsZero() {
				r.RotationDue = now
			}
			if r.Status.State == core.StateIdle || now.Sub(r.RotationDue) >= 30*time.Minute {
				r.RotatedDay = day
				r.RotationDue = time.Time{}
				r.Fresh = true
				r.Restarting = true
				s.event(name, "rotated", "daily fresh conversation")
				return s.beginStop(r)
			}
		}
	}
	s.deliver(ctx, r, now)
	return nil
}
func (s *Supervisor) crash(r *record, now time.Time, detail string) {
	recent := r.Crashes[:0]
	for _, t := range r.Crashes {
		if now.Sub(t) < 30*time.Minute {
			recent = append(recent, t)
		}
	}
	r.Crashes = append(recent, now)
	r.RestartTimes = append(r.RestartTimes, now)
	recentRestarts := r.RestartTimes[:0]
	for _, at := range r.RestartTimes {
		if now.Sub(at) < 24*time.Hour {
			recentRestarts = append(recentRestarts, at)
		}
	}
	r.RestartTimes = recentRestarts
	r.Status.Restarts = len(recentRestarts)
	r.Status.LastError = detail
	s.event(r.Status.Name, "crashed", detail)
	if len(r.Crashes) >= 6 {
		s.setState(r, core.StateFailed, "six crashes in 30 minutes")
		r.Status.NextRestart = time.Time{}
		return
	}
	delay := 5 * time.Second * time.Duration(1<<min(len(r.Crashes)-1, 6))
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	r.Status.NextRestart = now.Add(delay)
	s.setState(r, core.StateCrashed, detail)
}
func (s *Supervisor) launch(r *record) error {
	l := s.opts.Launchers[r.Status.Runtime.Harness]
	if l == nil {
		return fmt.Errorf("no launcher for %s", r.Status.Runtime.Harness)
	}
	opts := s.opts.Launch
	opts.Fresh = r.Fresh || !r.EverStarted
	if !opts.Fresh && opts.FirstPrompt == "" {
		// Both TUIs can resume at a blank composer without firing an idle
		// hook. Start one short turn so queued mail and hook-only delivery
		// become live again without discarding the conversation.
		opts.FirstPrompt = "Continue this conversation under lolad supervision. Check lolad msg inbox and read any waiting mail; if none, wait quietly for new mail."
	}
	if opts.Fresh {
		opts.FirstPrompt = r.Status.Runtime.OnWake
		if q := s.queues[r.Status.Name]; len(q) > 0 {
			opts.FirstPrompt += "\nUnread mail is waiting. Run lolad msg inbox and read the waiting messages."
		}
	}
	spec, e := l.Prepare(r.Status.Agent, opts)
	if e != nil {
		s.crash(r, s.opts.Clock.Now(), e.Error())
		return e
	}
	if e = s.opts.Sessions.Start(r.Status.Name, spec.Dir, spec.Env, spec.Argv, 220, 50); e != nil {
		s.crash(r, s.opts.Clock.Now(), e.Error())
		return e
	}
	now := s.opts.Clock.Now()
	r.EverStarted = true
	r.Fresh = false
	r.Restarting = false
	r.Seq = 0
	r.HookSession = ""
	r.LastHook = ""
	r.StartupAt = now
	r.StartupDialogs = nil
	r.Status.StartedAt = now
	r.Status.LastActivity = now
	if opts.Fresh {
		r.Status.SessionSince = now
	}
	r.Status.NextRestart = time.Time{}
	r.Status.LastError = ""
	r.Status.PID, _ = s.opts.Sessions.PanePID(r.Status.Name)
	s.setState(r, core.StateStarting, "")
	s.event(r.Status.Name, "started", fmt.Sprintf("fresh=%t", opts.Fresh))
	return nil
}
func (s *Supervisor) beginStop(r *record) error {
	if !r.StopAt.IsZero() {
		return nil
	}
	if e := s.opts.Sessions.SendKeys(r.Status.Name, "Escape", "C-c", "C-c"); e != nil {
		return e
	}
	r.StopAt = s.opts.Clock.Now().Add(10 * time.Second)
	s.event(r.Status.Name, "stopping", "graceful stop requested")
	return nil
}
func (s *Supervisor) Start(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.agents[name]
	if r == nil {
		return core.ErrNotFound
	}
	r.Status.Desired = "up"
	r.Crashes = nil
	r.Status.NextRestart = time.Time{}
	if r.Status.State == core.StateFailed {
		r.Status.State = core.StateStopped
	}
	s.event(name, "desired", "up")
	return s.persist(r)
}
func (s *Supervisor) Stop(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.agents[name]
	if r == nil {
		return core.ErrNotFound
	}
	r.Status.Desired = "down"
	if e := s.persist(r); e != nil {
		return e
	}
	s.event(name, "desired", "down")
	if s.opts.Sessions.Exists(name) {
		return s.beginStop(r)
	}
	s.setState(r, core.StateStopped, "")
	return s.persist(r)
}
func (s *Supervisor) Restart(ctx context.Context, name string, fresh bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.agents[name]
	if r == nil {
		return core.ErrNotFound
	}
	r.Status.Desired = "up"
	r.Crashes = nil
	r.Fresh = fresh
	r.Restarting = true
	r.Status.NextRestart = time.Time{}
	if r.Status.State == core.StateFailed {
		r.Status.State = core.StateStopped
	}
	if s.opts.Sessions.Exists(name) {
		if e := s.beginStop(r); e != nil {
			return e
		}
	}
	s.event(name, "restarted", fmt.Sprintf("fresh=%t", fresh))
	return s.persist(r)
}
func (s *Supervisor) Interrupt(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents[name] == nil {
		return core.ErrNotFound
	}
	if !s.opts.Sessions.Exists(name) {
		return core.ErrNotRunning
	}
	return s.opts.Sessions.SendKeys(name, "Escape")
}
func (s *Supervisor) List(ctx context.Context) ([]core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.AgentStatus, 0, len(s.agents))
	for _, r := range s.agents {
		a := r.Status
		a.Restarts = 0
		for _, at := range r.RestartTimes {
			if s.opts.Clock.Now().Sub(at) < 24*time.Hour {
				a.Restarts++
			}
		}
		if s.opts.Mailer != nil {
			mail, e := s.opts.Mailer.Unread(ctx, a.Name)
			if e != nil {
				return nil, e
			}
			a.Unread = len(mail)
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *Supervisor) Get(ctx context.Context, name string) (core.AgentStatus, error) {
	all, e := s.List(ctx)
	if e != nil {
		return core.AgentStatus{}, e
	}
	for _, a := range all {
		if a.Name == name {
			return a, nil
		}
	}
	return core.AgentStatus{}, core.ErrNotFound
}
func (s *Supervisor) Capture(ctx context.Context, name string, lines int) (string, error) {
	if _, e := s.Get(ctx, name); e != nil {
		return "", e
	}
	return s.opts.Sessions.Capture(name, lines, true)
}
func (s *Supervisor) Attach(ctx context.Context, name string, readOnly bool) (core.Terminal, error) {
	if _, e := s.Get(ctx, name); e != nil {
		return nil, e
	}
	return s.opts.Sessions.Attach(ctx, name, readOnly)
}
func (s *Supervisor) Events(ctx context.Context, name string, limit int) ([]core.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if s.opts.DB != nil {
		rows, e := s.opts.DB.QueryContext(ctx, `SELECT id,agent,at,kind,detail FROM sup_events WHERE (?='' OR agent=?) ORDER BY id DESC LIMIT ?`, name, name, limit)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []core.Event{}
		for rows.Next() {
			var ev core.Event
			var at string
			if e = rows.Scan(&ev.ID, &ev.Agent, &at, &ev.Kind, &ev.Detail); e != nil {
				return nil, e
			}
			ev.At, e = time.Parse(time.RFC3339Nano, at)
			if e != nil {
				return nil, e
			}
			out = append(out, ev)
		}
		return out, rows.Err()
	}
	out := []core.Event{}
	for i := len(s.history) - 1; i >= 0 && len(out) < limit; i-- {
		ev := s.history[i]
		if name == "" || ev.Agent == name {
			out = append(out, ev)
		}
	}
	return out, nil
}
func (s *Supervisor) Subscribe(ctx context.Context) <-chan core.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan core.Event, 64)
	s.subscribers[ch] = struct{}{}
	go func() { <-ctx.Done(); s.mu.Lock(); delete(s.subscribers, ch); close(ch); s.mu.Unlock() }()
	return ch
}
func (s *Supervisor) Reload(ctx context.Context) error {
	if s.opts.ReloadAgents == nil {
		return nil
	}
	agents, e := s.opts.ReloadAgents(ctx)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, a := range agents {
		seen[a.Name] = true
		if r := s.agents[a.Name]; r != nil {
			r.Status.Agent = a
		} else {
			r := &record{Status: core.AgentStatus{Agent: a, State: core.StateStopped, Desired: "down"}}
			if a.Runtime.AutostartEnabled() {
				r.Status.Desired = "up"
			}
			s.agents[a.Name] = r
			s.queues[a.Name] = map[string]*pending{}
		}
		if e = s.persist(s.agents[a.Name]); e != nil {
			return e
		}
	}
	for name, r := range s.agents {
		if !seen[name] { // Discovery changes must not kill an existing process.
			s.event(name, "removed", "removed from registry; session left running")
			delete(s.agents, name)
			delete(s.queues, name)
			_ = r
		}
	}
	return s.configureTriggers()
}
func (s *Supervisor) HandleHook(ctx context.Context, ev core.HookEvent) core.HookReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.agents[ev.Agent]
	if r == nil {
		return core.HookReply{}
	}
	if ev.SessionID != "" && r.HookSession != "" && ev.SessionID != r.HookSession && ev.Event != "session_start" {
		return core.HookReply{}
	}
	newSession := ev.Event == "session_start" && ev.SessionID != "" && ev.SessionID != r.HookSession
	if !newSession && ev.Seq > 0 && ev.Seq <= r.Seq {
		return core.HookReply{}
	}
	if newSession {
		r.Seq = 0
	}
	if ev.Seq > 0 {
		r.Seq = ev.Seq
	}
	if ev.SessionID != "" {
		r.HookSession = ev.SessionID
	}
	r.LastHook = ev.Event
	r.Status.LastActivity = s.opts.Clock.Now()
	state := ev.State
	if state == "" {
		switch ev.Event {
		case "prompt_submit", "pre_tool":
			state = core.StateBusy
		case "stop":
			state = core.StateIdle
		case "permission":
			state = core.StateWaiting
		case "session_end":
			state = core.StateUnknown
		}
	}
	if state != "" {
		s.setState(r, state, ev.Detail)
	}
	s.event(ev.Agent, "hook", ev.Event)
	_ = s.persist(r)
	reply := core.HookReply{}
	if s.opts.Mailer == nil {
		return reply
	}
	mail, e := s.opts.Mailer.Unread(ctx, ev.Agent)
	if e != nil {
		return reply
	}
	if ev.Event == "session_start" {
		reply.Context = mailSummary(mail)
		return reply
	}
	if ev.Event == "stop" && r.Status.Runtime.Harness == core.HarnessClaude {
		var eligible []core.Mail
		now := s.opts.Clock.Now()
		for _, m := range mail {
			p := s.enqueue(m)
			if p == nil {
				continue
			}
			if p.LastBlock.IsZero() || now.Sub(p.LastBlock) >= 2*time.Minute {
				p.LastBlock = now
				eligible = append(eligible, m)
				p.LastNotice = now
				s.savePending(ev.Agent, p)
				if s.opts.Mailer.MarkNotified(ctx, m.ID) == nil {
					s.event(ev.Agent, "notified", "stop hook: "+m.ID)
				}
			}
		}
		if len(eligible) > 0 {
			reply.Block = true
			reply.Reason = mailSummary(eligible)
			s.setState(r, core.StateBusy, "reading waiting mail")
		}
	}
	return reply
}
func mailSummary(mail []core.Mail) string {
	if len(mail) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Unread mail is waiting. Read it with lolad msg read <id>:\n")
	for _, m := range mail {
		fmt.Fprintf(&b, "- %s from %s: %s\n", m.ID, m.FromName, m.Subject)
	}
	return b.String()
}

// RecordEvent lets the integrator persist discovery diagnostics on the same
// history and subscription bus as lifecycle events. IDs and timestamps are owned here.
func (s *Supervisor) RecordEvent(ev core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.event(ev.Agent, ev.Kind, ev.Detail)
}
