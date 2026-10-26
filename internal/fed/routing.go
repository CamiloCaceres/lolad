package fed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func (f *Engine) CheckOutbound(ctx context.Context, from, to, reply string) core.Decision {
	return f.checkOutbound(ctx, from, to, reply, true)
}
func (f *Engine) checkOutbound(ctx context.Context, from, to, reply string, initiate bool) core.Decision {
	if !f.Enabled() {
		return core.Decision{Reason: "federation disabled"}
	}
	local, d, e := amp.ParseAddress(from)
	if e != nil || d != "" && d != f.o.Provider.Name() {
		return core.Decision{Reason: "invalid local sender"}
	}
	from = local
	_, provider, e := amp.ParseAddress(to)
	if e != nil || provider == "" {
		return core.Decision{Reason: "invalid remote address"}
	}
	to = strings.ToLower(to)
	if from == "lolad" {
		return core.Decision{Reason: "system identity cannot send remotely"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.find(provider)
	if p == nil || p.Removed {
		return core.Decision{Reason: "remote provider is not connected"}
	}
	if p.Paused {
		return core.Decision{Reason: "connection is paused"}
	}
	g := f.grant(provider, to, from)
	if g != nil && g.State == core.GrantBlocked {
		return core.Decision{Reason: "remote participant is blocked"}
	}
	if from == f.o.Org().User.Name {
		if initiate && (g == nil || g.State != core.GrantAllowed) {
			e = f.mutate(func() error {
				if g == nil {
					f.doc.Grants = append(f.doc.Grants, core.Grant{Peer: provider, Remote: to, Local: from, State: core.GrantAllowed, Origin: "initiated", CreatedAt: f.o.Clock().UTC(), DecidedAt: f.o.Clock().UTC()})
				} else {
					g.State = core.GrantAllowed
					g.Origin = "initiated"
					g.DecidedAt = f.o.Clock().UTC()
				}
				return nil
			})
			if e != nil {
				return core.Decision{Reason: "cannot save remote approval"}
			}
		}
		return core.Decision{Allow: true, Reason: "user may initiate on a connected lolad"}
	}
	if g != nil && g.State == core.GrantAllowed {
		return core.Decision{Allow: true, Reason: "remote participant approved"}
	}
	if reply != "" {
		m, e := f.o.Provider.Get(ctx, reply)
		if e == nil && m.FromName == to && m.ToName == from && m.Status != core.MailHeld {
			return core.Decision{Allow: true, Reason: "reply to remote participant"}
		}
	}
	return core.Decision{Reason: "agent needs an approved remote participant"}
}
func (f *Engine) Enqueue(ctx context.Context, tx *sql.Tx, m core.Mail, w amp.Message) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO fed_outbox(id,peer,next_at,created_at) VALUES(?,?,?,?)`, m.ID, m.Remote, f.o.Clock().UnixMilli(), f.o.Clock().UnixMilli())
	if e == nil {
		f.signal()
	}
	return e
}
func (f *Engine) reachable(p peer, local, remote string) bool {
	if local == f.o.Org().User.Name && p.UserReachable {
		return true
	}
	for _, s := range p.Shares {
		if s.Local == local {
			return true
		}
	}
	// The human's explicit first contact authorizes replies even when the human
	// is not advertised in the peer directory.
	g := f.grant(p.Provider, remote, local)
	return g != nil && g.State == core.GrantAllowed && g.Origin == "initiated"
}
func (f *Engine) deliver(w http.ResponseWriter, r *http.Request, p peer, raw []byte) {
	ctx := r.Context()
	var b deliverBody
	if json.Unmarshal(raw, &b) != nil {
		reject(w, 400, "invalid_request", "Invalid deliver request")
		return
	}
	remoteName, remoteDomain, parseErr := amp.ParseAddress(b.Envelope.From)
	remote := remoteName + "@" + remoteDomain
	if parseErr != nil || remoteDomain != p.Provider {
		reject(w, 403, "provider_not_trusted", "Envelope sender does not belong to provider")
		return
	}
	pub, e := amp.ParsePublicKey(b.SenderPublicKey)
	if e != nil || !amp.Verify(pub, b.Envelope, b.Payload) {
		reject(w, 403, "invalid_signature", "Invalid sender signature")
		return
	}
	b.SenderPublicKey = pubPEM(pub)
	local, provider, e := amp.ParseAddress(b.Envelope.To)
	if e != nil || provider != f.o.Provider.Name() {
		reject(w, 404, "recipient_not_found", "Recipient is not reachable")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Re-read after authentication so a concurrent pause/unshare takes effect.
	current := f.find(p.Provider)
	if current == nil || current.Removed {
		reject(w, 403, "provider_not_trusted", "Provider is not connected")
		return
	}
	if current.Paused {
		reject(w, 503, "provider_paused", "Connection is paused")
		return
	}
	p = *current
	var pinned string
	e = f.o.DB.QueryRowContext(ctx, `SELECT public_key FROM fed_sender_keys WHERE peer=? AND address=?`, p.Provider, remote).Scan(&pinned)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		reject(w, 500, "internal_error", "Cannot read sender key")
		return
	}
	if e == nil && pinned != b.SenderPublicKey {
		go f.event("Sender key mismatch for " + remote)
		reject(w, 403, "invalid_signature", "Sender key changed")
		return
	}
	if !f.reachable(p, local, remote) {
		reject(w, 404, "recipient_not_found", "Recipient is not reachable")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b.Payload, &fields) != nil {
		reject(w, 400, "invalid_request", "Invalid payload")
		return
	}
	if _, exists := fields["attachments"]; exists {
		reject(w, 422, "attachments_not_supported", "Attachments are not supported")
		return
	}
	// A tombstone preserves duplicate responses after blocked held mail is deleted.
	var oldPeer, oldFrom, oldTo, oldStatus string
	e = f.o.DB.QueryRowContext(ctx, `SELECT peer,from_addr,to_addr,status FROM fed_received WHERE id=?`, b.Envelope.ID).Scan(&oldPeer, &oldFrom, &oldTo, &oldStatus)
	if e == nil {
		if oldPeer != p.Provider || oldFrom != remote || oldTo != local+"@"+provider {
			reject(w, 409, "duplicate_message", "Message id has different participants")
			return
		}
		if oldStatus == "blocked" {
			reject(w, 403, "forbidden", "Sender is blocked")
			return
		}
		jsonResponse(w, 202, map[string]string{"id": b.Envelope.ID, "status": oldStatus})
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		reject(w, 500, "internal_error", "Cannot read message state")
		return
	}
	if !f.rate("peer:"+p.Provider, 100) || !f.rate("recipient:"+p.Provider+":"+local, 20) {
		reject(w, 429, "rate_limited", "Federation rate limit reached")
		return
	}
	g := f.grant(p.Provider, remote, local)
	if g != nil && g.State == core.GrantBlocked {
		reject(w, 403, "forbidden", "Sender is blocked")
		return
	}
	allowed := g != nil && g.State == core.GrantAllowed
	if !allowed {
		for _, s := range p.Shares {
			if s.Local == local {
				for _, a := range s.Remote {
					if a == remote {
						allowed = true
					}
				}
			}
		}
	}
	if !allowed && b.Envelope.InReplyTo != "" {
		parent, err := f.o.Provider.Get(ctx, b.Envelope.InReplyTo)
		if err == nil && parent.FromName == local && parent.ToName == remote {
			allowed = true
		}
	}
	newGrant := g == nil
	var grant core.Grant
	if g == nil {
		pending := 0
		for _, v := range f.doc.Grants {
			if v.Peer == p.Provider && v.State == core.GrantPending {
				pending++
			}
		}
		if !allowed && pending >= 50 {
			reject(w, 429, "rate_limited", "Too many pending approvals")
			return
		}
		grant = core.Grant{Peer: p.Provider, Remote: remote, Local: local, State: core.GrantPending, FirstSubject: b.Envelope.Subject, CreatedAt: f.o.Clock().UTC()}
	} else {
		grant = *g
	}
	if allowed {
		grant.State = core.GrantAllowed
		if grant.Origin == "" {
			grant.Origin = "shared"
		}
		if grant.DecidedAt.IsZero() {
			grant.DecidedAt = f.o.Clock().UTC()
		}
	}
	if e = f.mutate(func() error {
		if newGrant {
			f.doc.Grants = append(f.doc.Grants, grant)
		} else {
			*g = grant
		}
		return nil
	}); e != nil {
		reject(w, 500, "internal_error", "Cannot save sender approval")
		return
	}
	if pinned == "" {
		if _, e = f.o.DB.ExecContext(ctx, `INSERT INTO fed_sender_keys(peer,address,public_key,first_seen) VALUES(?,?,?,?)`, p.Provider, remote, b.SenderPublicKey, f.o.Clock().Unix()); e != nil {
			reject(w, 500, "internal_error", "Cannot pin sender key")
			return
		}
	}
	m, created, e := f.o.Provider.AcceptRemote(ctx, p.Provider, amp.Message{Envelope: b.Envelope, Payload: b.Payload}, pub, !allowed)
	if e != nil {
		status := 400
		code := "invalid_request"
		if errors.Is(e, core.ErrConflict) {
			status = 409
			code = "duplicate_message"
		}
		if errors.Is(e, core.ErrNotFound) {
			status = 404
			code = "recipient_not_found"
		}
		reject(w, status, code, "Remote mail could not be accepted")
		return
	}
	state := "delivered"
	if m.Status == core.MailHeld {
		state = "held"
	}
	if _, e = f.o.DB.ExecContext(ctx, `INSERT INTO fed_received(id,peer,from_addr,to_addr,status) VALUES(?,?,?,?,?)`, m.ID, p.Provider, remote, local+"@"+provider, state); e != nil {
		reject(w, 500, "internal_error", "Cannot save delivery state")
		return
	}
	if !allowed && created {
		held, _ := f.o.Provider.Held(ctx, p.Provider, remote, local)
		grant.Held = len(held)
		for _, fn := range f.approvals {
			go fn(grant)
		}
		go f.event("Approval requested: " + remote + " → " + local)
	}
	jsonResponse(w, 202, map[string]string{"id": m.ID, "status": state})
}
func (f *Engine) DecideGrant(ctx context.Context, provider, remote, local string, allow bool) (core.Grant, error) {

	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.grant(provider, remote, local)
	if g == nil {
		return core.Grant{}, core.ErrNotFound
	}
	if f.find(provider) == nil {
		return core.Grant{}, core.ErrNotFound
	}
	e := f.mutate(func() error {
		g.State = core.GrantBlocked
		if allow {
			g.State = core.GrantAllowed
		}
		g.Origin = "approved"
		g.DecidedAt = f.o.Clock().UTC()
		return nil
	})
	if e != nil {
		return core.Grant{}, e
	}
	result := *g
	if e = f.resolveGrant(ctx, result); e != nil {
		return result, e
	}
	go f.event("Sender " + result.State + ": " + remote + " → " + local)
	return result, nil
}

// Caller holds mu, keeping delivery/decisions ordered with YAML grant state.
func (f *Engine) resolveGrant(ctx context.Context, g core.Grant) error {
	allow := g.State == core.GrantAllowed
	status := "blocked"
	if allow {
		status = "delivered"
	}
	_, e := f.o.Provider.ResolveHeld(ctx, g.Peer, g.Remote, g.Local, allow, func(ctx context.Context, tx *sql.Tx, m core.Mail) error {
		if _, e := tx.ExecContext(ctx, `UPDATE fed_received SET status=? WHERE id=?`, status, m.ID); e != nil {
			return e
		}
		b, _ := json.Marshal(receiptBody{ID: m.ID, Status: status, At: f.o.Clock().UTC()})
		_, e := tx.ExecContext(ctx, `INSERT INTO fed_outbox(id,peer,next_at,created_at,kind,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,next_at=excluded.next_at`, "receipt:"+m.ID, g.Peer, f.o.Clock().UnixMilli(), f.o.Clock().UnixMilli(), "receipt", b)
		return e
	})
	if e == nil {
		f.signal()
	}
	return e
}

func (f *Engine) receipt(w http.ResponseWriter, r *http.Request, p peer, raw []byte) {
	var b receiptBody
	if json.Unmarshal(raw, &b) != nil || (b.Status != "delivered" && b.Status != "blocked") {
		reject(w, 400, "invalid_request", "Invalid receipt")
		return
	}
	m, e := f.o.Provider.Get(r.Context(), b.ID)
	if e != nil || m.Remote != p.Provider || domain(m.To) != p.Provider {
		reject(w, 404, "not_found", "Message not found")
		return
	}
	f.change(r.Context(), b.ID, b.Status, "")
	_, _ = f.o.DB.ExecContext(r.Context(), `DELETE FROM fed_outbox WHERE id=? AND peer=?`, b.ID, p.Provider)
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (f *Engine) directory(p peer) []core.RemoteContact {
	out := []core.RemoteContact{}
	for _, a := range f.o.Agents() {
		shared := false
		for _, s := range p.Shares {
			if s.Local == a.Name {
				shared = true
			}
		}
		if shared {
			out = append(out, core.RemoteContact{Address: a.Name + "@" + f.o.Provider.Name(), Name: a.Name, Display: a.Display, Role: a.Role, Kind: "agent"})
		}
	}
	if p.UserReachable {
		u := f.o.Org().User
		out = append(out, core.RemoteContact{Address: u.Name + "@" + f.o.Provider.Name(), Name: u.Name, Display: u.Display, Kind: "human"})
	}
	return out
}
func (f *Engine) RemoteContacts(ctx context.Context, local string) ([]core.RemoteContact, error) {
	if !f.Enabled() {
		return []core.RemoteContact{}, nil
	}
	f.mu.Lock()
	var peers []peer
	for _, p := range f.doc.Peers {
		if !p.Paused && !p.Removed {
			peers = append(peers, p)
		}
	}
	grants := append([]core.Grant(nil), f.doc.Grants...)
	f.mu.Unlock()
	out := []core.RemoteContact{}
	seen := map[string]bool{}
	for _, p := range peers {
		f.mu.Lock()
		cached, ok := f.cache[p.Provider]
		f.mu.Unlock()
		contacts := cached.Contacts
		if !ok || f.o.Clock().Sub(cached.At) >= 60*time.Second {
			status, b, e := f.request(ctx, p, "GET", "directory", nil)
			if e == nil && status == 200 {
				var v []core.RemoteContact
				if json.Unmarshal(b, &v) == nil {
					contacts = nil
					for _, c := range v {
						if domain(c.Address) == p.Provider {
							contacts = append(contacts, c)
						}
					}
					f.mu.Lock()
					f.cache[p.Provider] = cacheEntry{At: f.o.Clock(), Contacts: contacts}
					f.mu.Unlock()
				}
			}
		}
		contacts = append([]core.RemoteContact(nil), contacts...)
		for _, g := range grants {
			if g.Peer == p.Provider && g.Local == local && g.State == core.GrantAllowed {
				found := false
				for _, c := range contacts {
					if c.Address == g.Remote {
						found = true
					}
				}
				if !found {
					n, _, _ := amp.ParseAddress(g.Remote)
					contacts = append(contacts, core.RemoteContact{Address: g.Remote, Name: n, Display: g.RemoteDisplay, Kind: "agent"})
				}
			}
		}
		for _, c := range contacts {
			allowed := local == f.o.Org().User.Name
			for _, g := range grants {
				if g.Peer == p.Provider && g.Local == local && g.Remote == c.Address {
					if g.State == core.GrantAllowed {
						allowed = true
					} else if g.State == core.GrantBlocked {
						allowed = false
					}
				}
			}
			if allowed && !seen[c.Address] {
				c.Peer = p.Provider
				c.PeerDisplay = p.Display
				out = append(out, c)
				seen[c.Address] = true
			}
		}
	}
	return out, nil
}
func (f *Engine) change(ctx context.Context, id, state, detail string) {
	m, e := f.o.Provider.SetDelivery(ctx, id, state, detail)
	if e != nil {
		return
	}
	f.mu.Lock()
	hooks := append([]func(core.Mail){}, f.changes...)
	f.mu.Unlock()
	for _, fn := range hooks {
		go fn(m)
	}
}
func (f *Engine) failPeer(ctx context.Context, provider, detail string) error {
	rows, e := f.o.DB.QueryContext(ctx, `SELECT id,kind FROM fed_outbox WHERE peer=?`, provider)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id, kind string
		if e = rows.Scan(&id, &kind); e != nil {
			rows.Close()
			return e
		}
		if kind == "deliver" {
			ids = append(ids, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		f.change(ctx, id, core.DeliveryFailed, detail)
	}
	_, e = f.o.DB.ExecContext(ctx, `DELETE FROM fed_outbox WHERE peer=?`, provider)
	return e
}

// Check previews federation policy without creating an initiated grant.
func (f *Engine) Check(from, to string) core.Decision {
	n, d, e := amp.ParseAddress(from)
	if e != nil {
		return core.Decision{Reason: "invalid sender"}
	}
	if d == "" || d == f.o.Provider.Name() {
		return f.checkOutbound(context.Background(), n, to, "", false)
	}
	if !f.Enabled() {
		return core.Decision{Reason: "federation disabled"}
	}
	local, provider, e := amp.ParseAddress(to)
	if e != nil || provider != "" && provider != f.o.Provider.Name() {
		return core.Decision{Reason: "recipient is not local"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.find(d)
	if p == nil || p.Removed {
		return core.Decision{Reason: "remote provider is not connected"}
	}
	if p.Paused {
		return core.Decision{Reason: "connection is paused"}
	}
	remote := n + "@" + d
	if !f.reachable(*p, local, remote) {
		return core.Decision{Reason: "recipient is not shared with this lolad"}
	}
	if g := f.grant(d, remote, local); g != nil {
		switch g.State {
		case core.GrantAllowed:
			return core.Decision{Allow: true, Reason: "remote participant approved"}
		case core.GrantBlocked:
			return core.Decision{Reason: "remote participant is blocked"}
		}
	}
	for _, s := range p.Shares {
		if s.Local == local {
			for _, r := range s.Remote {
				if r == remote {
					return core.Decision{Allow: true, Reason: "remote participant pre-approved in share"}
				}
			}
		}
	}
	return core.Decision{Reason: "first message needs approval"}
}
