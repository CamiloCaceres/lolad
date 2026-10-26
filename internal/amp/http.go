package amp

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

var _ http.Handler = (*Provider)(nil)

func (p *Provider) Handler() http.Handler { return p }
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code, msg string) {
	respond(w, status, map[string]string{"error": code, "message": msg})
}
func httpError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, errSignature):
		failure(w, 403, "invalid_signature", e.Error())
	case errors.Is(e, core.ErrDenied):
		failure(w, 403, "forbidden", strings.TrimPrefix(e.Error(), core.ErrDenied.Error()+": "))
	case errors.Is(e, core.ErrNotFound):
		failure(w, 404, "not_found", e.Error())
	case errors.Is(e, core.ErrExists):
		failure(w, 409, "already_exists", e.Error())
	case errors.Is(e, core.ErrInvalid):
		failure(w, 400, "invalid_request", e.Error())
	case errors.Is(e, errTooLarge):
		failure(w, 413, "request_too_large", e.Error())
	default:
		failure(w, 500, "internal_error", "internal provider error")
	}
}
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	b, e := io.ReadAll(r.Body)
	if e != nil {
		var limit *http.MaxBytesError
		if errors.As(e, &limit) {
			return nil, errTooLarge
		}
	}
	return b, e
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	b, e := readBody(w, r)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, v); e != nil {
		return fmt.Errorf("%w: malformed JSON", core.ErrInvalid)
	}
	return nil
}
func (p *Provider) authenticate(ctx context.Context, r *http.Request) (core.Identity, error) {
	var i core.Identity
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || len(h) <= 7 {
		return i, core.ErrDenied
	}
	var name string
	e := p.db.QueryRowContext(ctx, `SELECT name FROM amp_identities WHERE api_key_hash=?`, keyHash(strings.TrimPrefix(h, "Bearer "))).Scan(&name)
	if e != nil {
		return i, core.ErrDenied
	}
	i, _, e = p.identity(ctx, name)
	return i, e
}
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/v1/health" && r.Method == "GET" {
		ids, e := p.Identities(ctx)
		if e != nil {
			httpError(w, e)
			return
		}
		respond(w, 200, map[string]any{"status": "healthy", "version": "amp/0.1", "provider": p.Name(), "federation": p.FederationEnabled(), "agents_online": len(ids), "uptime_seconds": int(time.Since(p.started).Seconds())})
		return
	}
	if path == "/v1/info" && r.Method == "GET" {
		v := map[string]any{"provider": p.Name(), "federation": p.FederationEnabled(), "version": "amp/0.1", "capabilities": []string{"threading", "priority"}, "registration_modes": []string{"preapproved"}}
		if id, pub, e := p.identity(ctx, "lolad"); e == nil {
			v["public_key"] = publicPEM(pub)
			v["fingerprint"] = id.Fingerprint
		}
		respond(w, 200, v)
		return
	}
	if path == "/v1/register" && r.Method == "POST" {
		p.register(w, r)
		return
	}
	caller, e := p.authenticate(ctx, r)
	if e != nil {
		failure(w, 401, "unauthorized", "valid Bearer API key required")
		return
	}
	switch {
	case path == "/v1/route" && r.Method == "POST":
		p.route(w, r, caller)
	case path == "/v1/agents" && r.Method == "GET":
		p.directory(w, r, caller)
	case path == "/v1/agents/me" && r.Method == "GET":
		v := p.directoryEntry(caller, caller.Name)
		v["registered_at"] = caller.CreatedAt
		respond(w, 200, v)
	case strings.HasPrefix(path, "/v1/agents/resolve/") && r.Method == "GET":
		n, e := p.local(strings.TrimPrefix(path, "/v1/agents/resolve/"))
		if e != nil {
			httpError(w, e)
			return
		}
		i, pub, e := p.identity(ctx, n)
		if e != nil {
			httpError(w, e)
			return
		}
		v := p.directoryEntry(i, caller.Name)
		v["public_key"] = publicPEM(pub)
		v["key_algorithm"] = "Ed25519"
		respond(w, 200, v)
	case path == "/v1/messages/pending/count" && r.Method == "GET":
		var count int
		e := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM amp_messages WHERE to_name=? AND status<>? AND status<>'held'`, caller.Name, core.MailRead).Scan(&count)
		if e != nil {
			httpError(w, e)
			return
		}
		respond(w, 200, map[string]any{"count": count})
	case path == "/v1/messages/pending" && r.Method == "GET":
		p.pending(w, r, caller)
	case path == "/v1/messages/pending/ack" && r.Method == "POST":
		var req struct {
			IDs []string `json:"ids"`
		}
		if e := decodeBody(w, r, &req); e != nil {
			httpError(w, e)
			return
		}
		p.ack(w, r, caller, req.IDs, false)
	case strings.HasPrefix(path, "/v1/messages/pending/") && r.Method == "DELETE":
		p.ack(w, r, caller, []string{strings.TrimPrefix(path, "/v1/messages/pending/")}, true)
	// Authenticated history extensions support msg read, thread and inbox --all.
	case path == "/v1/messages" && r.Method == "GET":
		p.history(w, r, caller)
	case strings.HasPrefix(path, "/v1/messages/"):
		id := strings.TrimPrefix(path, "/v1/messages/")
		read := strings.HasSuffix(id, "/read")
		if read {
			id = strings.TrimSuffix(id, "/read")
		}
		if (!read && r.Method != "GET") || (read && r.Method != "POST") {
			failure(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		m, e := p.Get(ctx, id)
		if e != nil {
			httpError(w, e)
			return
		}
		if m.Status == core.MailHeld || (m.ToName != caller.Name && m.FromName != caller.Name) {
			httpError(w, core.ErrNotFound)
			return
		}
		if read {
			if e = p.MarkRead(ctx, caller.Name, id); e != nil {
				httpError(w, e)
				return
			}
		}
		wire, e := p.wire(ctx, id)
		if e != nil {
			httpError(w, e)
			return
		}
		respond(w, 200, wire)
	default:
		failure(w, 404, "not_found", "unknown endpoint")
	}
}
func (p *Provider) route(w http.ResponseWriter, r *http.Request, caller core.Identity) {
	b, e := readBody(w, r)
	if e != nil {
		httpError(w, e)
		return
	}
	var req RouteRequest
	if e = json.Unmarshal(b, &req); e != nil {
		httpError(w, fmt.Errorf("%w: malformed route JSON", core.ErrInvalid))
		return
	}
	// Ignore any supplied from/id/timestamp: authentication alone determines from.
	to, e := ExpandAddress(req.To, p.tenant)
	if e != nil {
		httpError(w, e)
		return
	}
	priority := req.Priority
	if priority == "" {
		priority = "normal"
	}
	env := Envelope{From: caller.Address, To: to, Subject: req.Subject, Priority: priority, InReplyTo: req.InReplyTo, Signature: req.Signature}
	if req.Signature == "" {
		if caller.Kind == "agent" {
			httpError(w, errSignature)
			return
		}
		_, key, e := ReadCredentials(p.stateDir, caller.Name)
		if e != nil {
			httpError(w, e)
			return
		}
		env.Signature, e = Sign(key, env, req.Payload)
		if e != nil {
			httpError(w, e)
			return
		}
	}
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	hash := keyHash(string(b))
	now := time.Now().UTC()
	if req.IdempotencyKey != "" {
		var oldHash, response, expires string
		e = p.db.QueryRowContext(r.Context(), `SELECT request_hash,response,expires_at FROM amp_idempotency WHERE owner=? AND key=?`, caller.Name, req.IdempotencyKey).Scan(&oldHash, &response, &expires)
		if e == nil {
			expiry, _ := time.Parse(time.RFC3339Nano, expires)
			if now.Before(expiry) {
				if hash != oldHash {
					failure(w, 409, "duplicate_idempotency_key", "idempotency key already used with a different request")
					return
				}
				var v any
				_ = json.Unmarshal([]byte(response), &v)
				respond(w, 200, v)
				return
			}
		}
	}

	var response map[string]any
	persist := func(ctx context.Context, m core.Mail, wire Message) error {
		response = map[string]any{"id": m.ID, "status": "delivered", "method": "local", "delivered_at": m.CreatedAt, "thread_id": m.ThreadID}
		if m.Remote != "" {
			response["status"] = "queued"
			response["method"] = "federation"
		}
		tx, e := p.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = p.PersistMail(ctx, tx, m, wire); e != nil {
			return e
		}
		if req.IdempotencyKey != "" {
			data, _ := json.Marshal(response)
			_, e = tx.ExecContext(ctx, `INSERT INTO amp_idempotency(owner,key,request_hash,response,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(owner,key) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,expires_at=excluded.expires_at`, caller.Name, req.IdempotencyKey, hash, string(data), now.Add(24*time.Hour).Format(time.RFC3339Nano))
			if e != nil {
				return e
			}
		}
		return tx.Commit()
	}
	_, e = p.acceptWith(r.Context(), caller.Name, env, req.Payload, persist)
	if e != nil {
		httpError(w, e)
		return
	}

	respond(w, 200, response)
}
func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		n = 100
	}
	if n > 1000 {
		n = 1000
	}
	return n
}
func (p *Provider) pending(w http.ResponseWriter, r *http.Request, caller core.Identity) {
	mails, e := p.Unread(r.Context(), caller.Name)
	if e != nil {
		httpError(w, e)
		return
	}
	total := len(mails)
	if total > limit(r) {
		mails = mails[:limit(r)]
	}
	msgs := []Message{}
	for _, m := range mails {
		v, e := p.wire(r.Context(), m.ID)
		if e != nil {
			httpError(w, e)
			return
		}
		msgs = append(msgs, v)
	}
	respond(w, 200, map[string]any{"messages": msgs, "count": len(msgs), "remaining": total - len(msgs)})
}
func (p *Provider) ack(w http.ResponseWriter, r *http.Request, caller core.Identity, ids []string, single bool) {
	// Validate the whole batch before changing any message, then commit atomically.
	tx, e := p.db.BeginTx(r.Context(), nil)
	if e != nil {
		httpError(w, e)
		return
	}
	defer tx.Rollback()
	for _, id := range ids {
		var owner string
		if e = tx.QueryRowContext(r.Context(), `SELECT to_name FROM amp_messages WHERE id=? AND status<>'held'`, id).Scan(&owner); e != nil || owner != caller.Name {
			httpError(w, core.ErrNotFound)
			return
		}
	}
	for _, id := range ids {
		_, e = tx.ExecContext(r.Context(), `UPDATE amp_messages SET status=?,read_at=CASE WHEN read_at='' THEN ? ELSE read_at END WHERE id=?`, core.MailRead, time.Now().UTC().Format(time.RFC3339Nano), id)
		if e != nil {
			httpError(w, e)
			return
		}
	}
	if e = tx.Commit(); e != nil {
		httpError(w, e)
		return
	}
	if single {
		respond(w, 200, map[string]bool{"acknowledged": true})
	} else {
		respond(w, 200, map[string]int{"acknowledged": len(ids)})
	}
}
func (p *Provider) history(w http.ResponseWriter, r *http.Request, caller core.Identity) {
	f := core.MailFilter{Box: r.URL.Query().Get("box"), Limit: limit(r), Peer: r.URL.Query().Get("peer")}
	if f.Box == "" {
		f.Box = "all"
	}
	if b := r.URL.Query().Get("before"); b != "" {
		var e error
		f.Before, e = time.Parse(time.RFC3339Nano, b)
		if e != nil {
			httpError(w, core.ErrInvalid)
			return
		}
	}
	mails, e := p.List(r.Context(), caller.Name, f)
	if e != nil {
		httpError(w, e)
		return
	}
	messages := []Message{}
	thread := r.URL.Query().Get("thread")
	for _, m := range mails {
		if thread != "" && thread != m.ThreadID {
			continue
		}
		v, e := p.wire(r.Context(), m.ID)
		if e != nil {
			httpError(w, e)
			return
		}
		messages = append(messages, v)
	}
	respond(w, 200, map[string]any{"messages": messages, "count": len(messages)})
}
func (p *Provider) directoryEntry(i core.Identity, from string) map[string]any {
	display, role, team := "", "", ""
	isHead := false
	if p.agents != nil {
		for _, a := range p.agents() {
			if a.Name == i.Name {
				display = a.Display
				role = a.Role
				break
			}
		}
	}
	if p.org != nil {
		o := p.org()
		if i.Name == o.User.Name {
			display = o.User.Display
		}
		for _, t := range o.Teams {
			if t.Head == i.Name {
				team = t.ID
				isHead = true
			}
			for _, n := range t.Members {
				if n == i.Name {
					team = t.ID
				}
			}
		}
	}
	d := p.policy.Check(from, i.Name)
	return map[string]any{"name": i.Name, "address": i.Address, "kind": i.Kind, "display": display, "alias": display, "role": role, "team": team, "is_head": isHead, "may_message": d.Allow, "reason": d.Reason, "fingerprint": i.Fingerprint}
}
func (p *Provider) directory(w http.ResponseWriter, r *http.Request, caller core.Identity) {
	ids, e := p.Identities(r.Context())
	if e != nil {
		httpError(w, e)
		return
	}
	entries := []map[string]any{}
	for _, i := range ids {
		entries = append(entries, p.directoryEntry(i, caller.Name))
	}
	if remote := p.remoteHook(); remote != nil {
		contacts, e := remote.RemoteContacts(r.Context(), caller.Name)
		if e == nil {
			for _, c := range contacts {
				entries = append(entries, map[string]any{"name": c.Address, "address": c.Address, "display": c.Display, "kind": c.Kind, "role": c.Role, "remote": true, "peer": c.Peer, "peer_display": c.PeerDisplay, "may_message": true})
			}
		}
	}
	respond(w, 200, map[string]any{"agents": entries, "total": len(entries)})
}
func (p *Provider) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Tenant    string `json:"tenant"`
		PublicKey string `json:"public_key"`
		Algorithm string `json:"key_algorithm"`
	}
	if e := decodeBody(w, r, &req); e != nil {
		httpError(w, e)
		return
	}
	n, e := p.local(req.Name)
	if e != nil {
		httpError(w, e)
		return
	}
	known := false
	if p.agents != nil {
		for _, a := range p.agents() {
			if a.Name == n {
				known = true
			}
		}
	}
	if !known {
		failure(w, 409, "registration_not_allowed", "name must already exist as a lolad agent")
		return
	}
	if req.Tenant != "" && strings.ToLower(req.Tenant) != p.tenant {
		httpError(w, core.ErrInvalid)
		return
	}
	if req.Algorithm != "" && req.Algorithm != "Ed25519" {
		httpError(w, core.ErrInvalid)
		return
	}
	pub, e := ParsePublicKey(req.PublicKey)
	if e != nil {
		httpError(w, fmt.Errorf("%w: %s", core.ErrInvalid, e))
		return
	}
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	if _, _, e = p.identity(r.Context(), n); !errors.Is(e, core.ErrNotFound) {
		if e == nil {
			e = core.ErrExists
		}
		httpError(w, e)
		return
	}
	token, e := randomHex(32)
	if e != nil {
		httpError(w, e)
		return
	}
	key := "amp_live_sk_" + token
	id := core.Identity{Name: n, Address: n + "@" + p.Name(), Kind: "agent", Fingerprint: fingerprint(ed25519.PublicKey(pub)), CreatedAt: time.Now().UTC()}
	if e = p.insertIdentity(r.Context(), id, pub, key); e != nil {
		httpError(w, e)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	endpoint := scheme + "://" + r.Host + "/v1"
	respond(w, 201, map[string]any{"address": id.Address, "short_address": id.Address, "local_name": n, "agent_id": n, "tenant_id": p.tenant, "tenant": p.tenant, "api_key": key, "provider": map[string]string{"name": p.Name(), "endpoint": endpoint, "route_url": endpoint + "/route"}, "fingerprint": id.Fingerprint, "registered_at": id.CreatedAt})
}
