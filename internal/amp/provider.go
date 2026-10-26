package amp

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

//go:embed schema.sql
var schema string

// Provider owns only amp_* tables. The caller owns the DB lifecycle.
type Provider struct {
	db               *sql.DB
	stateDir, tenant string
	policy           core.Policy
	agents           func() []core.Agent
	org              func() core.Org
	event            func(core.Event)
	identityMu       sync.Mutex
	sendMu           sync.Mutex
	subsMu           sync.RWMutex
	subs             []func(core.Mail)
	started          time.Time
}
type Option func(*Provider)

func WithAgents(fn func() []core.Agent) Option { return func(p *Provider) { p.agents = fn } }
func WithOrg(fn func() core.Org) Option        { return func(p *Provider) { p.org = fn } }
func WithEventSink(fn func(core.Event)) Option { return func(p *Provider) { p.event = fn } }
func New(db *sql.DB, stateDir, tenant string, policy core.Policy, opts ...Option) (*Provider, error) {
	if db == nil || policy == nil || !tenantRE.MatchString(tenant) {
		return nil, fmt.Errorf("%w: database, policy and tenant required", core.ErrInvalid)
	}
	p := &Provider{db: db, stateDir: stateDir, tenant: strings.ToLower(tenant), policy: policy, started: time.Now()}
	for _, o := range opts {
		o(p)
	}
	if _, e := db.Exec(schema); e != nil {
		return nil, e
	}
	return p, nil
}

var _ core.Mailer = (*Provider)(nil)

// Name is fixed for the provider's lifetime; changing tenant requires restart.
func (p *Provider) Name() string { return ProviderName(p.tenant) }
func (p *Provider) OnMail(fn func(core.Mail)) {
	if fn == nil {
		return
	}
	p.subsMu.Lock()
	p.subs = append(p.subs, fn)
	p.subsMu.Unlock()
}
func (p *Provider) notify(m core.Mail) {
	p.subsMu.RLock()
	subs := append([]func(core.Mail){}, p.subs...)
	p.subsMu.RUnlock()
	raw, _ := json.Marshal(m)
	for _, fn := range subs {
		var copy core.Mail
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		_ = dec.Decode(&copy)
		go fn(copy)
	}
}
func (p *Provider) Send(ctx context.Context, from string, out core.Outgoing) (core.Mail, error) {
	n, e := p.local(from)
	if e != nil {
		return core.Mail{}, e
	}
	_, key, e := ReadCredentials(p.stateDir, n)
	if e != nil {
		return core.Mail{}, e
	}
	if out.Priority == "" {
		out.Priority = "normal"
	}
	if out.Type == "" {
		out.Type = "notification"
	}
	payload, e := CanonicalJSON(Payload{Type: out.Type, Message: out.Body, Context: out.Context})
	if e != nil {
		return core.Mail{}, e
	}
	to, e := ExpandAddress(out.To, p.tenant)
	if e != nil {
		return core.Mail{}, e
	}
	env := Envelope{From: n + "@" + p.Name(), To: to, Subject: out.Subject, Priority: out.Priority, InReplyTo: out.InReplyTo}
	env.Signature, e = Sign(key, env, json.RawMessage(payload))
	if e != nil {
		return core.Mail{}, e
	}
	return p.accept(ctx, n, env, json.RawMessage(payload))
}

// accept is shared by Send and HTTP routing: both enforce policy and verify the
// exact signed payload. No path writes mail outside the database.
func (p *Provider) accept(ctx context.Context, from string, env Envelope, payload json.RawMessage) (core.Mail, error) {
	return p.acceptWith(ctx, from, env, payload, nil)
}

func (p *Provider) acceptWith(ctx context.Context, from string, env Envelope, payload json.RawMessage, persist func(context.Context, core.Mail, Message) error) (core.Mail, error) {
	var zero core.Mail
	sender, pub, e := p.identity(ctx, from)
	if e != nil {
		return zero, e
	}
	to, e := p.local(env.To)
	if e != nil {
		return zero, e
	}
	recipient, _, e := p.identity(ctx, to)
	if e != nil {
		return zero, e
	}
	env.From = sender.Address
	env.To = recipient.Address
	if env.Priority == "" {
		env.Priority = "normal"
	}
	if !Verify(pub, env, payload) {
		return zero, errSignature
	}
	d := p.policy.Check(from, to)
	if !d.Allow {
		if p.event != nil {
			p.event(core.Event{At: time.Now().UTC(), Agent: from, Kind: "policy", Detail: from + " → " + to + ": " + d.Reason})
		}
		return zero, fmt.Errorf("%w: %s", core.ErrDenied, d.Reason)
	}
	if utf8.RuneCountInString(env.Subject) > 256 || env.Subject == "" {
		return zero, fmt.Errorf("%w: subject must have 1–256 characters", core.ErrInvalid)
	}
	switch env.Priority {
	case "normal", "low", "high", "urgent":
	default:
		return zero, fmt.Errorf("%w: invalid priority", core.ErrInvalid)
	}
	var body Payload
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if e = dec.Decode(&body); e != nil || body.Type == "" {
		return zero, fmt.Errorf("%w: payload.type and payload.message required", core.ErrInvalid)
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(payload, &fields); e != nil || fields["message"] == nil || string(fields["message"]) == "null" {
		return zero, fmt.Errorf("%w: payload.message required", core.ErrInvalid)
	}
	if _, has := fields["attachments"]; has {
		return zero, fmt.Errorf("%w: attachments unsupported", core.ErrInvalid)
	}
	token, e := randomHex(12)
	if e != nil {
		return zero, e
	}
	now := time.Now().UTC()
	env.ID = fmt.Sprintf("msg_%d_%s", now.Unix(), token)
	env.Timestamp = now
	env.Version = "amp/0.1"
	env.ThreadID = env.ID
	if env.InReplyTo != "" {
		parent, e := p.Get(ctx, env.InReplyTo)
		if e != nil {
			return zero, e
		}
		if !((parent.FromName == from && parent.ToName == to) || (parent.FromName == to && parent.ToName == from)) {
			return zero, fmt.Errorf("%w: reply must be between the same participants", core.ErrInvalid)
		}
		env.ThreadID = parent.ThreadID
	}
	m := core.Mail{ID: env.ID, ThreadID: env.ThreadID, InReplyTo: env.InReplyTo, From: env.From, To: env.To, FromName: from, ToName: to, Subject: env.Subject, Priority: env.Priority, Type: body.Type, Body: body.Message, Context: body.Context, Signed: true, Verified: true, CreatedAt: now, Status: core.MailUnread}
	wire := Message{ID: m.ID, Envelope: env, Payload: payload, QueuedAt: now, Status: string(m.Status)}
	if persist == nil {
		persist = func(ctx context.Context, m core.Mail, wire Message) error { return insertMail(ctx, p.db, m, wire) }
	}
	if e = persist(ctx, m, wire); e != nil {
		return zero, e
	}
	p.notify(m)
	return m, nil
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertMail(ctx context.Context, db sqlExecer, m core.Mail, wire Message) error {
	mb, e := json.Marshal(m)
	if e != nil {
		return e
	}
	wb, e := json.Marshal(wire)
	if e != nil {
		return e
	}
	if len(wb) > 1<<20 {
		return errTooLarge
	}
	_, e = db.ExecContext(ctx, `INSERT INTO amp_messages(id,from_name,to_name,thread_id,created_at,status,mail,wire) VALUES(?,?,?,?,?,?,?,?)`, m.ID, m.FromName, m.ToName, m.ThreadID, m.CreatedAt.Format(time.RFC3339Nano), m.Status, string(mb), string(wb))
	return e
}

var errSignature = errors.New("invalid signature")
var errTooLarge = errors.New("request exceeds 1 MB")

func scanMail(row interface{ Scan(...any) error }) (core.Mail, error) {
	var m core.Mail
	var raw, status, notified, read string
	var attempts int
	e := row.Scan(&raw, &status, &notified, &read, &attempts)
	if errors.Is(e, sql.ErrNoRows) {
		return m, core.ErrNotFound
	}
	if e != nil {
		return m, e
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	e = dec.Decode(&m)
	m.Status = core.MailStatus(status)
	m.NotifiedAt, _ = time.Parse(time.RFC3339Nano, notified)
	m.ReadAt, _ = time.Parse(time.RFC3339Nano, read)
	m.Attempts = attempts
	return m, e
}

const mailCols = `mail,status,notified_at,read_at,attempts`

func (p *Provider) Get(ctx context.Context, id string) (core.Mail, error) {
	return scanMail(p.db.QueryRowContext(ctx, `SELECT `+mailCols+` FROM amp_messages WHERE id=?`, id))
}
func (p *Provider) List(ctx context.Context, owner string, f core.MailFilter) ([]core.Mail, error) {
	n, e := p.local(owner)
	if e != nil {
		return nil, e
	}
	q := `SELECT ` + mailCols + ` FROM amp_messages WHERE `
	args := []any{}
	switch f.Box {
	case "", "inbox":
		q += `to_name=?`
		args = append(args, n)
	case "sent":
		q += `from_name=?`
		args = append(args, n)
	case "all":
		q += `(to_name=? OR from_name=?)`
		args = append(args, n, n)
	default:
		return nil, core.ErrInvalid
	}
	if f.Peer != "" {
		peer, e := p.local(f.Peer)
		if e != nil {
			return nil, e
		}
		q += ` AND (to_name=? OR from_name=?)`
		args = append(args, peer, peer)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	if !f.Before.IsZero() {
		q += ` AND created_at<?`
		args = append(args, f.Before.UTC().Format(time.RFC3339Nano))
	}
	q += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	return p.queryMail(ctx, q, args...)
}
func (p *Provider) queryMail(ctx context.Context, q string, args ...any) ([]core.Mail, error) {
	rows, e := p.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.Mail{}
	for rows.Next() {
		m, e := scanMail(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (p *Provider) Traffic(ctx context.Context, limit int, before time.Time) ([]core.Mail, error) {
	q := `SELECT ` + mailCols + ` FROM amp_messages`
	args := []any{}
	if !before.IsZero() {
		q += ` WHERE created_at<?`
		args = append(args, before.UTC().Format(time.RFC3339Nano))
	}
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	return p.queryMail(ctx, q, args...)
}
func (p *Provider) Unread(ctx context.Context, owner string) ([]core.Mail, error) {
	n, e := p.local(owner)
	if e != nil {
		return nil, e
	}
	return p.queryMail(ctx, `SELECT `+mailCols+` FROM amp_messages WHERE to_name=? AND status<>? ORDER BY created_at,id`, n, core.MailRead)
}
func (p *Provider) MarkNotified(ctx context.Context, id string) error {
	r, e := p.db.ExecContext(ctx, `UPDATE amp_messages SET status=?,notified_at=?,attempts=attempts+1 WHERE id=? AND status<>?`, core.MailNotified, time.Now().UTC().Format(time.RFC3339Nano), id, core.MailRead)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		_, e = p.Get(ctx, id)
	}
	return e
}
func (p *Provider) MarkRead(ctx context.Context, owner, id string) error {
	n, e := p.local(owner)
	if e != nil {
		return e
	}
	m, e := p.Get(ctx, id)
	if e != nil {
		return e
	}
	if m.ToName != n {
		return core.ErrDenied
	}
	_, e = p.db.ExecContext(ctx, `UPDATE amp_messages SET status=?,read_at=CASE WHEN read_at='' THEN ? ELSE read_at END WHERE id=? AND to_name=?`, core.MailRead, time.Now().UTC().Format(time.RFC3339Nano), id, n)
	return e
}
func (p *Provider) wire(ctx context.Context, id string) (Message, error) {
	var m Message
	var raw, status string
	e := p.db.QueryRowContext(ctx, `SELECT wire,status FROM amp_messages WHERE id=?`, id).Scan(&raw, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return m, core.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &m)
		m.Status = status
	}
	return m, e
}
