package supervisor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	_ "modernc.org/sqlite"
)

var ctx = context.Background()

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time                       { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }
func (c *fakeClock) advance(d time.Duration)              { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

type fakeSessions struct {
	core.Sessions
	exists, dead  bool
	starts, kills int
	keys          []string
	pane          string
}

func (f *fakeSessions) Exists(string) bool                 { return f.exists }
func (f *fakeSessions) PaneDead(string) (bool, int, error) { return f.dead, 1, nil }
func (f *fakeSessions) Start(name, dir string, env, argv []string, cols, rows int) error {
	f.exists = true
	f.dead = false
	f.starts++
	return nil
}
func (f *fakeSessions) PanePID(string) (int, error) { return 123, nil }
func (f *fakeSessions) Kill(string) error           { f.exists = false; f.dead = false; f.kills++; return nil }
func (f *fakeSessions) SendKeys(name string, keys ...string) error {
	f.keys = append(f.keys, keys...)
	return nil
}
func (f *fakeSessions) Capture(string, int, bool) (string, error) { return f.pane, nil }

type fakeLauncher struct{ opts []core.LaunchOpts }

func (f *fakeLauncher) Harness() core.Harness { return core.HarnessClaude }
func (f *fakeLauncher) Prepare(a core.Agent, o core.LaunchOpts) (core.LaunchSpec, error) {
	f.opts = append(f.opts, o)
	return core.LaunchSpec{Dir: a.Dir, Argv: []string{"fake"}}, nil
}
func (f *fakeLauncher) PromptReady(p string) bool { return p == "ready" }

type fakeNotifier struct {
	calls  int
	result core.NotifyResult
}

func (f *fakeNotifier) Notify(context.Context, core.Agent, core.Mail) (core.NotifyResult, error) {
	f.calls++
	return f.result, nil
}

type fakeMailer struct {
	core.Mailer
	mail     map[string]core.Mail
	sent     []core.Outgoing
	from     []string
	callback func(core.Mail)
	marked   int
}

func (f *fakeMailer) OnMail(fn func(core.Mail)) { f.callback = fn }
func (f *fakeMailer) Get(_ context.Context, id string) (core.Mail, error) {
	m, ok := f.mail[id]
	if !ok {
		return m, core.ErrNotFound
	}
	return m, nil
}
func (f *fakeMailer) Unread(_ context.Context, owner string) ([]core.Mail, error) {
	out := []core.Mail{}
	for _, m := range f.mail {
		if m.ToName == owner && m.Status != core.MailRead {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeMailer) MarkNotified(_ context.Context, id string) error {
	m := f.mail[id]
	m.Status = core.MailNotified
	f.mail[id] = m
	f.marked++
	return nil
}
func (f *fakeMailer) Send(_ context.Context, from string, out core.Outgoing) (core.Mail, error) {
	f.sent = append(f.sent, out)
	f.from = append(f.from, from)
	m := core.Mail{ID: fmt.Sprintf("sent-%d", len(f.sent)), FromName: from, ToName: out.To, Body: out.Body, Subject: out.Subject, Status: core.MailUnread}
	f.mail[m.ID] = m
	if f.callback != nil {
		f.callback(m)
	}
	return m, nil
}
func (f *fakeMailer) add(c *fakeClock) {
	m := core.Mail{ID: "msg-test", FromName: "user", ToName: "agent", Subject: "Hello", Status: core.MailUnread, CreatedAt: c.Now()}
	f.mail[m.ID] = m
	f.callback(m)
}

type fixture struct {
	s        *Supervisor
	c        *fakeClock
	sessions *fakeSessions
	l        *fakeLauncher
	n        *fakeNotifier
	m        *fakeMailer
	o        Options
}

func setup(t *testing.T, change func(*Options)) *fixture {
	t.Helper()
	c := &fakeClock{now: time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)}
	sessions := &fakeSessions{}
	l := &fakeLauncher{}
	n := &fakeNotifier{result: core.NotifyConfirmed}
	m := &fakeMailer{mail: map[string]core.Mail{}}
	o := Options{Agents: []core.Agent{{Name: "agent", Dir: t.TempDir(), Runtime: core.Runtime{Harness: core.HarnessClaude}}}, Sessions: sessions, Launchers: map[core.Harness]core.Launcher{core.HarnessClaude: l, core.HarnessCodewhale: l}, Notifier: n, Mailer: m, Clock: c, Launch: core.LaunchOpts{StateDir: t.TempDir()}}
	if change != nil {
		change(&o)
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	return &fixture{s, c, sessions, l, n, m, o}
}
func (f *fixture) tick(t *testing.T) {
	t.Helper()
	if e := f.s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) state(t *testing.T) core.AgentStatus {
	t.Helper()
	a, e := f.s.Get(ctx, "agent")
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestCrashBackoffAndFailure(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	if !f.l.opts[0].Fresh {
		t.Fatal("first launch must be fresh")
	}
	for i := 0; i < 6; i++ {
		f.sessions.dead = true
		f.tick(t)
		a := f.state(t)
		if i == 5 {
			if a.State != core.StateFailed {
				t.Fatalf("%+v", a)
			}
			break
		}
		want := 5 * time.Second * time.Duration(1<<i)
		if a.State != core.StateCrashed || a.NextRestart.Sub(f.c.Now()) != want {
			t.Fatalf("crash %d: %+v", i, a)
		}
		starts := f.sessions.starts
		f.tick(t)
		if f.sessions.starts != starts {
			t.Fatal("ignored backoff")
		}
		f.c.advance(want)
		f.tick(t)
		if f.l.opts[len(f.l.opts)-1].Fresh {
			t.Fatal("crash resumed fresh")
		}
		if !strings.Contains(f.l.opts[len(f.l.opts)-1].FirstPrompt, "lolad msg inbox") {
			t.Fatal("resumed TUI needs a turn to report hook idle and pick up queued mail")
		}
	}
	starts := f.sessions.starts
	f.c.advance(time.Hour)
	f.tick(t)
	if f.sessions.starts != starts {
		t.Fatal("failed agent restarted")
	}
	f.s.Start(ctx, "agent")
	f.tick(t)
	if f.sessions.starts != starts+1 {
		t.Fatal("Start did not clear failure")
	}
}
func TestAdoptAndPersistStopStart(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	f := setup(t, func(o *Options) { o.DB = db; o.Sessions.(*fakeSessions).exists = true })
	if f.state(t).State != core.StateUnknown {
		t.Fatal("not adopted")
	}
	f.tick(t)
	if f.sessions.starts != 0 {
		t.Fatal("duplicate start")
	}
	if e = f.s.Stop(ctx, "agent"); e != nil {
		t.Fatal(e)
	}
	f.tick(t)
	if f.sessions.kills != 0 {
		t.Fatal("killed before grace")
	}
	f.c.advance(10 * time.Second)
	f.tick(t)
	if f.state(t).State != core.StateStopped || f.sessions.kills != 1 {
		t.Fatal("stop failed")
	}
	s, e := New(f.o)
	if e != nil {
		t.Fatal(e)
	}
	if a, _ := s.Get(ctx, "agent"); a.Desired != "down" {
		t.Fatal("desired not persisted")
	}
	if e = s.Start(ctx, "agent"); e != nil {
		t.Fatal(e)
	}
	if e = s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if f.l.opts[len(f.l.opts)-1].Fresh {
		t.Fatal("start should resume")
	}
	ev, e := s.Events(ctx, "agent", 100)
	if e != nil || len(ev) < 3 {
		t.Fatalf("events %v %v", ev, e)
	}
	if strings.Join(f.sessions.keys, ",") != "Escape,C-c,C-c" {
		t.Fatal(f.sessions.keys)
	}
}
func TestRotationAndFreshRestart(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Rotate = "08:01"; o.Agents[0].Runtime.OnWake = "wake up" })
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.c.advance(time.Minute)
	f.tick(t)
	if f.sessions.kills != 0 {
		t.Fatal("rotation skipped grace")
	}
	f.c.advance(10 * time.Second)
	f.tick(t)
	if len(f.l.opts) != 2 || !f.l.opts[1].Fresh || f.l.opts[1].FirstPrompt != "wake up" {
		t.Fatal(f.l.opts)
	}
	f.tick(t)
	if f.sessions.kills != 1 {
		t.Fatal("rotation repeated")
	}
	if e := f.s.Restart(ctx, "agent", true); e != nil {
		t.Fatal(e)
	}
	f.c.advance(10 * time.Second)
	f.tick(t)
	if len(f.l.opts) != 3 || !f.l.opts[2].Fresh {
		t.Fatal("fresh restart failed")
	}
}
func TestBusyRotationDeadline(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Rotate = "08:01" })
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 1})
	f.c.advance(time.Minute)
	f.tick(t)
	if len(f.sessions.keys) > 0 {
		t.Fatal("rotated busy too soon")
	}
	f.c.advance(30 * time.Minute)
	f.tick(t)
	if len(f.sessions.keys) != 3 {
		t.Fatal("rotation deadline missed")
	}
}
func TestHookOrderingTTLAndSubscription(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	subctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := f.s.Subscribe(subctx)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "session_start", SessionID: "one", Seq: 1})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", SessionID: "one", Seq: 3})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", SessionID: "one", Seq: 2})
	if f.state(t).State != core.StateBusy {
		t.Fatal("stale state accepted")
	}
	f.c.advance(16 * time.Minute)
	f.tick(t)
	if f.state(t).State != core.StateBusy {
		t.Fatal("long busy lost")
	}
	f.sessions.pane = "ready"
	f.tick(t)
	if f.state(t).State != core.StateIdle {
		t.Fatal("prompt fallback missing")
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "session_start", SessionID: "two", Seq: 1})
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "permission", SessionID: "one", Seq: 10})
	if f.state(t).State == core.StateWaiting {
		t.Fatal("old session accepted")
	}
	select {
	case e := <-ch:
		if e.Agent != "agent" {
			t.Fatal(e)
		}
	default:
		t.Fatal("no event")
	}
}
func TestMailBusyThenIdle(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Harness = core.HarnessCodewhale })
	f.tick(t)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "prompt_submit", Seq: 1})
	f.m.add(f.c)
	f.tick(t)
	if f.n.calls != 0 {
		t.Fatal("typed while busy")
	}
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 2})
	f.tick(t)
	if f.n.calls != 1 || f.m.marked != 1 {
		t.Fatal("not notified")
	}
	if f.state(t).State != core.StateBusy {
		t.Fatal("notice should start turn")
	}
}
func TestWakeOnMailAndStopHook(t *testing.T) {
	no := false
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Autostart = &no })
	f.tick(t)
	if f.sessions.starts != 0 {
		t.Fatal("autostart ignored")
	}
	f.m.add(f.c)
	f.tick(t)
	if f.sessions.starts != 1 || !strings.Contains(f.l.opts[0].FirstPrompt, "mail") {
		t.Fatal("wake prompt missing")
	}
	reply := f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "session_start", Seq: 1})
	if !strings.Contains(reply.Context, "msg-test") {
		t.Fatal(reply)
	}
	reply = f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 2})
	if !reply.Block || !strings.Contains(reply.Reason, "msg-test") {
		t.Fatal(reply)
	}
	reply = f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 3})
	if reply.Block {
		t.Fatal("stop trapped in loop")
	}
	f.tick(t)
	if f.n.calls != 0 {
		t.Fatal("notifier duplicated hook")
	}
	f.c.advance(2 * time.Minute)
	reply = f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 4})
	if !reply.Block {
		t.Fatal("mail not offered after cooldown")
	}
}
func TestNotifierRetrySchedule(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Harness = core.HarnessCodewhale })
	f.n.result = core.NotifyFailed
	f.tick(t)
	f.m.add(f.c)
	f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1})
	f.tick(t)
	if f.n.calls != 1 {
		t.Fatal(f.n.calls)
	}
	for _, d := range []time.Duration{30 * time.Second, 90 * time.Second, 8 * time.Minute} {
		f.c.advance(d - time.Second)
		f.tick(t)
		before := f.n.calls
		f.c.advance(time.Second)
		f.tick(t)
		if f.n.calls != before+1 {
			t.Fatalf("retry at %v: %d -> %d", d, before, f.n.calls)
		}
	}
	f.c.advance(time.Minute)
	f.tick(t)
	if f.n.calls != 4 {
		t.Fatal("retried after exhaustion")
	}
}
func TestRenotificationsCappedAndReadRemoved(t *testing.T) {
	f := setup(t, func(o *Options) { o.Agents[0].Runtime.Harness = core.HarnessCodewhale })
	f.tick(t)
	f.m.add(f.c)
	for i := 0; i < 6; i++ {
		f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: int64(i + 1)})
		f.c.advance(2 * time.Minute)
		f.tick(t)
		f.tick(t)
	}
	if f.n.calls != 4 {
		t.Fatalf("wanted initial + 3 renotifications, got %d", f.n.calls)
	}
	m := f.m.mail["msg-test"]
	m.Status = core.MailRead
	f.m.mail[m.ID] = m
	f.c.advance(time.Minute)
	f.tick(t)
	if len(f.s.queues["agent"]) != 0 {
		t.Fatal("read mail still queued")
	}
}

type fakeWatch struct {
	calls  int
	output []byte
}

func (f *fakeWatch) Watch(context.Context, string, string) ([]byte, error) {
	f.calls++
	return f.output, nil
}
func TestCronWebhookAndMailTriggers(t *testing.T) {
	w := &fakeWatch{output: []byte("log line\n{\"new\":[{\"id\":\"1\",\"from\":\"operator\",\"subject\":\"help\",\"trust\":\"operator\"},{\"id\":\"2\",\"from\":\"other\",\"subject\":\"spam\",\"trust\":\"external\"}]}\n")}
	f := setup(t, func(o *Options) {
		o.MailWatch = w
		o.Agents[0].Runtime.Triggers = []core.Trigger{{Cron: "* * * * *", Prompt: "scheduled work"}, {Webhook: "deploy", Prompt: "Webhook prefix"}}
	})
	f.tick(t)
	if len(f.m.sent) != 0 {
		t.Fatal("cron ran early")
	}
	f.c.advance(time.Minute)
	f.tick(t)
	if len(f.m.sent) != 1 || f.m.from[0] != "lolad" || f.m.sent[0].Body != "scheduled work" {
		t.Fatal(f.m.sent)
	}
	token, e := os.ReadFile(filepath.Join(f.o.Launch.StateDir, "agents/agent/webhook.token"))
	if e != nil || len(strings.TrimSpace(string(token))) != 64 {
		t.Fatalf("token %q %v", token, e)
	}
	if e = f.s.Trigger(ctx, "agent", "deploy", "body"); e != nil {
		t.Fatal(e)
	}
	if f.m.sent[1].Body != "Webhook prefix\nbody" {
		t.Fatal(f.m.sent[1])
	}
	if e = f.s.Trigger(ctx, "agent", "missing", "body"); e != core.ErrNotFound {
		t.Fatal(e)
	}
	f.s.pollMail(ctx, &triggerState{Agent: f.o.Agents[0], Trigger: core.Trigger{Mail: "test@example.com"}})
	if len(f.m.sent) != 3 || !strings.Contains(f.m.sent[2].Body, "tools/email.sh read test@example.com 1") {
		t.Fatal(f.m.sent)
	}
	events, _ := f.s.Events(ctx, "agent", 100)
	found := false
	for _, ev := range events {
		if ev.Kind == "external_mail" {
			found = true
		}
	}
	if !found {
		t.Fatal("external mail event missing")
	}
}
func TestExecMailWatch(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "tools"), 0755)
	os.WriteFile(filepath.Join(dir, "tools/email.sh"), []byte("#!/bin/sh\n[ \"$1\" = watch ] && [ \"$2\" = account ] || exit 2\nprintf '%s\\n' '{\"new\":[]}'\n"), 0755)
	b, e := (ExecMailWatch{}).Watch(ctx, dir, "account")
	if e != nil || strings.TrimSpace(string(b)) != "{\"new\":[]}" {
		t.Fatalf("%q %v", b, e)
	}
}

type blockingWatch struct{ started chan struct{} }

func (w *blockingWatch) Watch(ctx context.Context, _, _ string) ([]byte, error) {
	w.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestMailPollDoesNotBlockReconcileOrOverlap(t *testing.T) {
	w := &blockingWatch{started: make(chan struct{}, 2)}
	f := setup(t, func(o *Options) {
		o.MailWatch = w
		o.Agents[0].Runtime.Triggers = []core.Trigger{{Mail: "account", Every: "1s"}}
	})
	watchCtx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); f.s.triggerWG.Wait() }()
	if e := f.s.Reconcile(watchCtx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("watch did not start")
	}
	f.c.advance(time.Minute)
	if e := f.s.Reconcile(watchCtx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-w.started:
		t.Fatal("overlapping mail polls")
	default:
	}
}
func TestStopHookCooldownSurvivesDaemonRestart(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	f := setup(t, func(o *Options) { o.DB = db })
	f.tick(t)
	f.m.add(f.c)
	if reply := f.s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 1}); !reply.Block {
		t.Fatal("first hook did not block")
	}
	s, e := New(f.o)
	if e != nil {
		t.Fatal(e)
	}
	if reply := s.HandleHook(ctx, core.HookEvent{Agent: "agent", Event: "stop", Seq: 2}); reply.Block {
		t.Fatal("daemon restart reset hook cooldown")
	}
}
func TestCrashBudgetExpires(t *testing.T) {
	f := setup(t, nil)
	f.tick(t)
	for i := 0; i < 5; i++ {
		f.sessions.dead = true
		f.tick(t)
		f.c.advance(31 * time.Minute)
		f.tick(t)
	}
	if f.state(t).State == core.StateFailed {
		t.Fatal("old crashes counted")
	}
	f.c.advance(24 * time.Hour)
	if f.state(t).Restarts != 0 {
		t.Fatal("restart count is not a 24h window")
	}
}

func TestRestartIntentSurvivesDaemonRestart(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	f := setup(t, func(o *Options) { o.DB = db })
	f.tick(t)
	if e = f.s.Restart(ctx, "agent", true); e != nil {
		t.Fatal(e)
	}
	s, e := New(f.o)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	f.c.advance(10 * time.Second)
	if e = s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	if f.sessions.kills != 1 || len(f.l.opts) != 2 || !f.l.opts[1].Fresh {
		t.Fatalf("kills=%d opts=%+v", f.sessions.kills, f.l.opts)
	}
}
