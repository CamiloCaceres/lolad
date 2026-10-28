package amp

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Remote is attached once at startup. Enqueue runs inside the mail transaction;
// implementations must use tx rather than the DB and must not do network I/O.
type Remote interface {
	CheckOutbound(context.Context, string, string, string) core.Decision
	Enqueue(context.Context, *sql.Tx, core.Mail, Message) error
	RemoteContacts(context.Context, string) ([]core.RemoteContact, error)
	Enabled() bool
}

func (p *Provider) SetRemote(r Remote) { p.remoteMu.Lock(); p.remote = r; p.remoteMu.Unlock() }
func (p *Provider) remoteHook() Remote {
	p.remoteMu.RLock()
	defer p.remoteMu.RUnlock()
	return p.remote
}
func (p *Provider) FederationEnabled() bool { r := p.remoteHook(); return r != nil && r.Enabled() }
func (p *Provider) Check(from, to string) core.Decision {
	_, fromDomain, _ := ParseAddress(from)
	_, toDomain, _ := ParseAddress(to)
	if (fromDomain != "" && fromDomain != p.Name()) || (toDomain != "" && toDomain != p.Name()) {
		if r := p.remoteHook(); r != nil {
			if checker, ok := r.(interface {
				Check(string, string) core.Decision
			}); ok {
				return checker.Check(from, to)
			}
		}
	}

	n, d, e := ParseAddress(to)
	if e != nil {
		return core.Decision{Reason: "invalid address"}
	}
	if d != "" && d != p.Name() {
		if r := p.remoteHook(); r != nil {
			return r.CheckOutbound(context.Background(), from, n+"@"+d, "")
		}
		return core.Decision{Reason: "federation disabled"}
	}
	localFrom, e := p.local(from)
	if e != nil {
		return core.Decision{Reason: "invalid local sender"}
	}
	return p.policy.Check(localFrom, n)
}
func (p *Provider) participant(address string) (string, error) {
	n, d, e := ParseAddress(address)
	if e != nil {
		return "", e
	}
	if d != "" && d != p.Name() {
		return n + "@" + d, nil
	}
	return n, nil
}
func (p *Provider) migrateRemote() error {
	rows, e := p.db.Query(`PRAGMA table_info(amp_messages)`)
	if e != nil {
		return e
	}
	found := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		found[name] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, c := range []string{"remote", "delivery", "delivery_error"} {
		if !found[c] {
			if _, e = p.db.Exec(`ALTER TABLE amp_messages ADD COLUMN ` + c + ` TEXT`); e != nil {
				return e
			}
		}
	}
	return nil
}

// PersistMail inserts mail and its remote outbox entry atomically. Used by route's
// idempotency transaction as well as in-process Send.
func (p *Provider) PersistMail(ctx context.Context, tx *sql.Tx, m core.Mail, w Message) error {
	if e := insertMail(ctx, tx, m, w); e != nil {
		return e
	}
	if e := bindAttachments(ctx, tx, m); e != nil {
		return e
	}
	if m.Delivery == core.DeliveryQueued {
		r := p.remoteHook()
		if r == nil {
			return core.ErrDenied
		}
		return r.Enqueue(ctx, tx, m, w)
	}
	return nil
}
func (p *Provider) Wire(ctx context.Context, id string) (Message, error) { return p.wire(ctx, id) }
func (p *Provider) PublicKey(ctx context.Context, name string) (string, error) {
	_, pub, e := p.identity(ctx, name)
	if e != nil {
		return "", e
	}
	return publicPEM(pub), nil
}

// AcceptRemote verifies the original envelope, preserves its ID and thread and
// atomically deduplicates. A duplicate with different participants is rejected.
// Authorization and sender-key pinning belong to the federation engine.
func (p *Provider) AcceptRemote(ctx context.Context, peer string, w Message, pub ed25519.PublicKey, held bool) (core.Mail, bool, error) {
	p.inboundMu.Lock()
	defer p.inboundMu.Unlock()
	env := w.Envelope
	if env.ID == "" || env.ThreadID == "" || env.Timestamp.IsZero() || len(env.ID) > 256 {
		return core.Mail{}, false, core.ErrInvalid
	}
	from, domain, e := ParseAddress(env.From)
	if e != nil || domain != peer {
		return core.Mail{}, false, core.ErrDenied
	}
	from += "@" + domain
	to, e := p.local(env.To)
	if e != nil {
		return core.Mail{}, false, e
	}
	if _, _, e = p.identity(ctx, to); e != nil {
		return core.Mail{}, false, e
	}
	if !Verify(pub, env, w.Payload) {
		return core.Mail{}, false, errSignature
	}
	old, e := p.Get(ctx, env.ID)
	if e == nil {
		if old.FromName != from || old.ToName != to {
			return core.Mail{}, false, fmt.Errorf("%w: message id has different participants", core.ErrConflict)
		}
		return old, false, nil
	}
	if e != core.ErrNotFound {
		return core.Mail{}, false, e
	}
	if env.Subject == "" || len([]rune(env.Subject)) > 256 {
		return core.Mail{}, false, core.ErrInvalid
	}
	switch env.Priority {
	case "normal", "low", "high", "urgent":
	default:
		return core.Mail{}, false, core.ErrInvalid
	}
	var body Payload
	dec := json.NewDecoder(strings.NewReader(string(w.Payload)))
	dec.UseNumber()
	if e = dec.Decode(&body); e != nil || body.Type == "" {
		return core.Mail{}, false, core.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(w.Payload, &fields); e != nil || fields["message"] == nil || string(fields["message"]) == "null" || fields["attachments"] != nil {
		return core.Mail{}, false, core.ErrInvalid
	}
	if env.InReplyTo != "" {
		if parent, err := p.Get(ctx, env.InReplyTo); err == nil {
			if !((parent.FromName == to && parent.ToName == from) || (parent.FromName == from && parent.ToName == to)) || parent.ThreadID != env.ThreadID {
				return core.Mail{}, false, core.ErrInvalid
			}
		}
	}
	m := core.Mail{ID: env.ID, ThreadID: env.ThreadID, InReplyTo: env.InReplyTo, From: from, To: to + "@" + p.Name(), FromName: from, ToName: to, Subject: env.Subject, Priority: env.Priority, Type: body.Type, Body: body.Message, Context: body.Context, Signed: true, Verified: true, CreatedAt: env.Timestamp, Status: core.MailUnread, Remote: peer}
	if held {
		m.Status = core.MailHeld
	}
	w.ID = m.ID
	w.Status = string(m.Status)
	w.QueuedAt = m.CreatedAt
	if e = insertMail(ctx, p.db, m, w); e != nil {
		return core.Mail{}, false, e
	}
	if !held {
		p.notify(m)
	}
	return m, true, nil
}
func (p *Provider) Held(ctx context.Context, peer, remote, local string) ([]core.Mail, error) {
	return p.queryMail(ctx, `SELECT `+mailCols+` FROM amp_messages WHERE remote=? AND from_name=? AND to_name=? AND status='held' ORDER BY created_at,id`, peer, remote, local)
}
func (p *Provider) ReleaseHeld(ctx context.Context, id string, allow bool) error {
	m, e := p.Get(ctx, id)
	if e != nil {
		return e
	}
	if m.Status != core.MailHeld {
		return nil
	}
	if !allow {
		_, e = p.db.ExecContext(ctx, `DELETE FROM amp_messages WHERE id=? AND status='held'`, id)
		return e
	}
	res, e := p.db.ExecContext(ctx, `UPDATE amp_messages SET status='unread' WHERE id=? AND status='held'`, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		m.Status = core.MailUnread
		p.notify(m)
	}
	return nil
}
func (p *Provider) SetDelivery(ctx context.Context, id, state, detail string) (core.Mail, error) {
	_, e := p.db.ExecContext(ctx, `UPDATE amp_messages SET delivery=?,delivery_error=? WHERE id=? AND remote IS NOT NULL AND remote<>'' AND (delivery IN ('queued','held') OR delivery=?) AND NOT (delivery='held' AND ?='queued')`, state, detail, id, state, state)
	if e != nil {
		return core.Mail{}, e
	}
	return p.Get(ctx, id)
}

// ResolveHeld changes a whole sender/recipient pair in one transaction. The
// optional persist callback records delivery receipts in that same transaction.
// Notifications fire only after commit, so a crash cannot release mail while
// losing its receipt. Federation serializes grant decisions around this call.
func (p *Provider) ResolveHeld(ctx context.Context, peer, remote, local string, allow bool, persist func(context.Context, *sql.Tx, core.Mail) error) ([]core.Mail, error) {
	p.inboundMu.Lock()
	defer p.inboundMu.Unlock()
	held, e := p.Held(ctx, peer, remote, local)
	if e != nil {
		return nil, e
	}
	if len(held) == 0 {
		return held, nil
	}
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	for _, m := range held {
		if allow {
			_, e = tx.ExecContext(ctx, `UPDATE amp_messages SET status='unread' WHERE id=? AND status='held'`, m.ID)
		} else {
			_, e = tx.ExecContext(ctx, `DELETE FROM amp_messages WHERE id=? AND status='held'`, m.ID)
		}
		if e != nil {
			return nil, e
		}
		if persist != nil {
			if e = persist(ctx, tx, m); e != nil {
				return nil, e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if allow {
		for _, m := range held {
			m.Status = core.MailUnread
			p.notify(m)
		}
	}
	return held, nil
}
