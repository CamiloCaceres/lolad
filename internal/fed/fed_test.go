package fed

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
	_ "modernc.org/sqlite"
)

type fixture struct {
	f             *Engine
	p             *amp.Provider
	db            *sql.DB
	server        *httptest.Server
	state, config string
	offset        atomic.Int64
	online        atomic.Bool
	cancel        context.CancelFunc
}

func setup(t *testing.T, tenant string) *fixture {
	t.Helper()
	x := &fixture{state: t.TempDir(), config: t.TempDir()}
	x.online.Store(true)
	db, e := sql.Open("sqlite", filepath.Join(x.state, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	x.db = db
	org := policy.DefaultOrg()
	org.Tenant = tenant
	org.User = core.User{Name: tenant, Display: strings.ToUpper(tenant[:1]) + tenant[1:]}
	org.Defaults.Independents = "all"
	agents := []core.Agent{{Name: "agent", Display: "Agent"}, {Name: "private", Display: "Private"}}
	x.p, e = amp.New(db, x.state, tenant, policy.New(org, "agent", "private"), amp.WithOrg(func() core.Org { return org }), amp.WithAgents(func() []core.Agent { return agents }))
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range []struct{ n, k string }{{tenant, "human"}, {"agent", "agent"}, {"private", "agent"}, {"lolad", "system"}} {
		if _, e = x.p.EnsureIdentity(t.Context(), i.n, i.k); e != nil {
			t.Fatal(e)
		}
	}
	mux := http.NewServeMux()
	x.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !x.online.Load() {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	x.f, e = New(Options{DB: db, StateDir: x.state, ConfigDir: x.config, Provider: x.p, Enabled: true, Display: org.User.Display, URL: x.server.URL, Org: func() core.Org { return org }, Agents: func() []core.Agent { return agents }, Clock: func() time.Time { return time.Now().Add(time.Duration(x.offset.Load())) }, Backoff: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}})
	if e != nil {
		t.Fatal(e)
	}
	mux.Handle("/v1/federation/", x.f.Handler())
	mux.Handle("/v1/", x.p)
	t.Cleanup(func() {
		if x.cancel != nil {
			x.cancel()
		}
		x.server.Close()
		db.Close()
	})
	return x
}
func (x *fixture) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	x.cancel = cancel
	done := make(chan struct{})
	go func() { defer close(done); x.f.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("outbox did not stop")
		}
	})
}
func pair(t *testing.T, a, b *fixture) {
	t.Helper()
	i, e := a.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	c, e := b.f.AcceptInvite(t.Context(), i.Code)
	if e != nil {
		t.Fatal(e)
	}
	if c.Provider != a.p.Name() {
		t.Fatal("wrong peer")
	}
}
func share(t *testing.T, owner, remote *fixture, pre ...string) {
	t.Helper()
	_, e := owner.f.UpdateConnection(t.Context(), remote.p.Name(), core.ConnectionUpdate{Shares: []core.Share{{Local: "agent", Remote: pre}}})
	if e != nil {
		t.Fatal(e)
	}
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func delivery(t *testing.T, x *fixture, id, state string) {
	t.Helper()
	eventually(t, func() bool { m, e := x.p.Get(t.Context(), id); return e == nil && m.Delivery == state })
}
func send(t *testing.T, x *fixture, from, to, reply string) core.Mail {
	t.Helper()
	m, e := x.p.Send(t.Context(), from, core.Outgoing{To: to, Subject: "Hello", Body: "A private body", InReplyTo: reply})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func signed(t *testing.T, from, to *fixture, path string, body any, timestamp int64, login string) (int, []byte) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, e := http.NewRequest("POST", to.server.URL+"/v1/federation/"+path, bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	ts := strconv.FormatInt(timestamp, 10)
	req.Header.Set("X-AMP-Provider", from.p.Name())
	req.Header.Set("X-AMP-Timestamp", ts)
	req.Header.Set("X-AMP-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(from.f.key, append([]byte(ts+"."), b...))))
	if login != "" {
		req.Header.Set("Tailscale-User-Login", login)
	}
	res, e := to.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, raw
}
func bodyFor(t *testing.T, x *fixture, m core.Mail) deliverBody {
	t.Helper()
	wire, e := x.p.Wire(t.Context(), m.ID)
	if e != nil {
		t.Fatal(e)
	}
	pub, e := x.p.PublicKey(t.Context(), m.FromName)
	if e != nil {
		t.Fatal(e)
	}
	return deliverBody{Envelope: wire.Envelope, Payload: wire.Payload, SenderPublicKey: pub}
}
func TestPairHoldApproveReplyAndPersistence(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	a.run(t)
	b.run(t)
	for _, x := range []*fixture{a, b} {
		cs, e := x.f.Connections(t.Context())
		if e != nil || len(cs) != 1 || cs[0].Fingerprint == "" {
			t.Fatal("pair not pinned", e)
		}
		for _, path := range []string{filepath.Join(x.state, "federation", "provider.key"), filepath.Join(x.config, "connections.yaml")} {
			st, e := os.Stat(path)
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatal("private file permissions", e)
			}
		}
	}
	approved := make(chan core.Grant, 1)
	a.f.OnApproval(func(g core.Grant) { approved <- g })
	changes := make(chan core.Mail, 20)
	b.f.OnChange(func(m core.Mail) { changes <- m })
	m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	if m.ToName != "agent@"+a.p.Name() || m.Delivery != core.DeliveryQueued {
		t.Fatal("outbound not queued")
	}
	delivery(t, b, m.ID, core.DeliveryHeld)
	held, e := a.p.Get(t.Context(), m.ID)
	if e != nil || held.Status != core.MailHeld || held.FromName != "jugui@"+b.p.Name() || held.Remote != b.p.Name() {
		t.Fatal("held remote mail", e)
	}
	if unread, _ := a.p.Unread(t.Context(), "agent"); len(unread) != 0 {
		t.Fatal("held exposed in unread")
	}
	if inbox, _ := a.p.List(t.Context(), "agent", core.MailFilter{}); len(inbox) != 0 {
		t.Fatal("held exposed in inbox")
	}
	select {
	case g := <-approved:
		if g.Remote != m.From || g.Local != "agent" {
			t.Fatal("approval participants")
		}
	case <-time.After(time.Second):
		t.Fatal("approval hook missing")
	}
	if _, e = a.f.DecideGrant(t.Context(), b.p.Name(), m.From, "agent", true); e != nil {
		t.Fatal(e)
	}
	delivery(t, b, m.ID, core.DeliveryDelivered)
	held, _ = a.p.Get(t.Context(), m.ID)
	if held.Status != core.MailUnread {
		t.Fatal("held not released")
	}
	second := send(t, b, "jugui", "agent@"+a.p.Name(), m.ID)
	delivery(t, b, second.ID, core.DeliveryDelivered)
	reply := send(t, a, "agent", m.From, m.ID)
	delivery(t, a, reply.ID, core.DeliveryDelivered)
	received, e := b.p.Get(t.Context(), reply.ID)
	if e != nil || received.ID != reply.ID || received.ThreadID != m.ThreadID || received.InReplyTo != m.ID || received.FromName != "agent@"+a.p.Name() {
		t.Fatal("remote thread did not preserve identity", e)
	}
	contacts, e := a.f.RemoteContacts(t.Context(), "agent")
	if e != nil || len(contacts) != 1 || contacts[0].Address != m.From {
		t.Fatal("approved remote missing", e)
	}
	if e = a.f.Reload(); e != nil {
		t.Fatal(e)
	}
	grants, e := a.f.Grants(t.Context())
	if e != nil || len(grants) != 1 || grants[0].State != core.GrantAllowed {
		t.Fatal("grant did not survive reload", e)
	}
	info := a.f.Info(t.Context())
	if info.Provider != a.p.Name() || info.Fingerprint == "" || !info.Enabled {
		t.Fatal("info")
	}
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("change hook missing")
	}
}
func TestBlockPreapprovedShareAndOutboundRules(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	a.run(t)
	b.run(t)
	if _, e := a.p.Send(t.Context(), "agent", core.Outgoing{To: "jugui@" + b.p.Name(), Subject: "Denied", Body: "x"}); e == nil {
		t.Fatal("agent initiated without approval")
	}
	if a.p.Check("lolad", "jugui@"+b.p.Name()).Allow {
		t.Fatal("system allowed remote")
	}
	m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, m.ID, core.DeliveryHeld)
	if _, e := a.f.DecideGrant(t.Context(), b.p.Name(), m.From, "agent", false); e != nil {
		t.Fatal(e)
	}
	delivery(t, b, m.ID, core.DeliveryBlocked)
	if _, e := a.p.Get(t.Context(), m.ID); e != core.ErrNotFound {
		t.Fatal("blocked held mail retained")
	}
	next := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, next.ID, core.DeliveryBlocked)
	share(t, a, b, "agent@"+b.p.Name())
	pre := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, pre.ID, core.DeliveryBlocked) // Explicit block wins.
	if _, e := a.f.UpdateConnection(t.Context(), b.p.Name(), core.ConnectionUpdate{Shares: []core.Share{{Local: "agent", Remote: []string{"private@" + b.p.Name()}}}}); e != nil {
		t.Fatal(e)
	}
	// Pre-approval allows the local agent to start the conversation too.
	pre = send(t, a, "agent", "private@"+b.p.Name(), "")
	delivery(t, a, pre.ID, core.DeliveryFailed) // Recipient is unshared on b.
	// Let b's user initiate to a pre-approved shared agent.
	if _, e := a.f.DecideGrant(t.Context(), b.p.Name(), m.From, "agent", true); e != nil {
		t.Fatal(e)
	}
	pre = send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, pre.ID, core.DeliveryDelivered)
	private := send(t, b, "jugui", "private@"+a.p.Name(), "")
	delivery(t, b, private.ID, core.DeliveryFailed)
}
func TestSecurityDuplicatesRateAndUnpair(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	body := bodyFor(t, b, m)
	now := time.Now().Unix()
	if code, _ := signed(t, b, a, "deliver", body, now-301, ""); code != 403 {
		t.Fatal("old timestamp accepted", code)
	}
	if code, _ := signed(t, b, a, "deliver", body, now+301, ""); code != 403 {
		t.Fatal("future timestamp accepted", code)
	}
	spoof := body
	spoof.Envelope.From = "jugui@evil.lolad.local"
	if code, _ := signed(t, b, a, "deliver", spoof, now, ""); code != 403 {
		t.Fatal("spoof accepted", code)
	}
	if code, _ := signed(t, b, a, "deliver", body, now, ""); code != 202 {
		t.Fatal("first deliver", code)
	}
	if code, _ := signed(t, b, a, "deliver", body, now, ""); code != 202 {
		t.Fatal("duplicate rejected", code)
	}
	grants, _ := a.f.Grants(t.Context())
	if len(grants) != 1 || grants[0].Held != 1 {
		t.Fatal("duplicate held twice")
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	changed := body
	changed.SenderPublicKey = pubPEM(key.Public().(ed25519.PublicKey))
	changed.Envelope.Signature, _ = amp.Sign(key, changed.Envelope, changed.Payload)
	if code, _ := signed(t, b, a, "deliver", changed, now, ""); code != 403 {
		t.Fatal("sender key change accepted", code)
	}
	collision := body
	collision.Envelope.To = "private@" + a.p.Name()
	_, e := a.f.UpdateConnection(t.Context(), b.p.Name(), core.ConnectionUpdate{Shares: []core.Share{{Local: "agent"}, {Local: "private"}}})
	if e != nil {
		t.Fatal(e)
	}
	_, sender, _ := amp.ReadCredentials(b.state, "jugui")
	collision.Envelope.Signature, _ = amp.Sign(sender, collision.Envelope, collision.Payload)
	if code, _ := signed(t, b, a, "deliver", collision, now, ""); code != 409 {
		t.Fatal("collision accepted", code)
	}
	// Pin login and require it whenever Tailscale provides a login header.
	a.f.mu.Lock()
	a.f.find(b.p.Name()).Login = "jugui@example.test"
	a.f.mu.Unlock()
	if code, _ := signed(t, b, a, "deliver", body, now, "other@example.test"); code != 403 {
		t.Fatal("wrong login accepted", code)
	}
	for i := 0; i < 19; i++ {
		v := body
		v.Envelope.ID = fmt.Sprintf("rate_%d", i)
		if code, _ := signed(t, b, a, "deliver", v, now, ""); code != 202 {
			t.Fatal("rate too early", i, code)
		}
	}
	v := body
	v.Envelope.ID = "rate_over"
	if code, _ := signed(t, b, a, "deliver", v, now, ""); code != 429 {
		t.Fatal("rate not enforced", code)
	}
	if e = b.f.RemoveConnection(t.Context(), a.p.Name()); e != nil {
		t.Fatal(e)
	}
	cs, _ := a.f.Connections(t.Context())
	if len(cs) != 1 || !cs[0].Paused || cs[0].LastError != "removed by them" {
		t.Fatal("remote unpair not retained")
	}
	if b.p.Check("jugui", "agent@"+a.p.Name()).Allow {
		t.Fatal("removed peer allowed")
	}
}
func TestOfflinePauseRetryGiveUpAndDirectory(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b, "jugui@"+b.p.Name())
	a.run(t)
	b.run(t)
	contacts, e := b.f.RemoteContacts(t.Context(), "jugui")
	if e != nil || len(contacts) != 1 || contacts[0].Address != "agent@"+a.p.Name() {
		t.Fatal("directory", e)
	}
	a.online.Store(false)
	m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	eventually(t, func() bool {
		var n int
		_ = b.db.QueryRow(`SELECT attempts FROM fed_outbox WHERE id=?`, m.ID).Scan(&n)
		return n > 0
	})
	queued, _ := b.p.Get(t.Context(), m.ID)
	if queued.Delivery != core.DeliveryQueued {
		t.Fatal("offline mail not queued")
	}
	a.online.Store(true)
	delivery(t, b, m.ID, core.DeliveryDelivered)
	yes, no := true, false
	if _, e = a.f.UpdateConnection(t.Context(), b.p.Name(), core.ConnectionUpdate{Paused: &yes}); e != nil {
		t.Fatal(e)
	}
	m = send(t, b, "jugui", "agent@"+a.p.Name(), "")
	eventually(t, func() bool {
		var n int
		_ = b.db.QueryRow(`SELECT attempts FROM fed_outbox WHERE id=?`, m.ID).Scan(&n)
		return n > 0
	})
	if _, e = a.f.UpdateConnection(t.Context(), b.p.Name(), core.ConnectionUpdate{Paused: &no}); e != nil {
		t.Fatal(e)
	}
	delivery(t, b, m.ID, core.DeliveryDelivered)
	a.online.Store(false)
	old := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	if _, e = b.db.Exec(`UPDATE fed_outbox SET created_at=?,next_at=0 WHERE id=?`, time.Now().Add(-8*24*time.Hour).UnixMilli(), old.ID); e != nil {
		t.Fatal(e)
	}
	delivery(t, b, old.ID, core.DeliveryFailed)
}
func TestInvitesInvalidExpiredReusedAndKeyPersistence(t *testing.T) {
	a, b, c := setup(t, "milo"), setup(t, "jugui"), setup(t, "third")
	invite, e := a.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.f.AcceptInvite(t.Context(), "invalid"); e == nil {
		t.Fatal("invalid invite")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(invite.Code, "lolad-invite:"))
	var code inviteCode
	_ = json.Unmarshal(raw, &code)
	bad := code
	bad.Secret = "wrong"
	v, _ := json.Marshal(bad)
	if _, e = b.f.AcceptInvite(t.Context(), "lolad-invite:"+base64.RawURLEncoding.EncodeToString(v)); e == nil {
		t.Fatal("bad secret")
	}
	if _, e = b.f.AcceptInvite(t.Context(), invite.Code); e != nil {
		t.Fatal(e)
	}
	if _, e = b.f.AcceptInvite(t.Context(), invite.Code); e != nil {
		t.Fatal("same peer retry should be idempotent", e)
	}
	if _, e = c.f.AcceptInvite(t.Context(), invite.Code); e == nil {
		t.Fatal("reused secret")
	}
	expired, e := a.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.db.Exec(`UPDATE fed_invites SET expires=0 WHERE used_at IS NULL`); e != nil {
		t.Fatal(e)
	}
	if _, e = c.f.AcceptInvite(t.Context(), expired.Code); e == nil {
		t.Fatal("expired invite")
	}
	original := a.f.Info(t.Context()).Fingerprint
	if e = a.f.loadKey(); e != nil || a.f.Info(t.Context()).Fingerprint != original {
		t.Fatal("provider key changed", e)
	}
	b.f.mu.Lock()
	before := b.f.find(a.p.Name()).PublicKey
	b.f.mu.Unlock()
	if e = b.f.Reload(); e != nil {
		t.Fatal(e)
	}
	b.f.mu.Lock()
	after := b.f.find(a.p.Name()).PublicKey
	b.f.mu.Unlock()
	if before != after {
		t.Fatal("pin changed after reload")
	}
	if e = os.WriteFile(filepath.Join(b.config, "connections.yaml"), []byte("peers: [not valid"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = b.f.Reload(); e == nil {
		t.Fatal("malformed config accepted")
	}
	cs, _ := b.f.Connections(t.Context())
	if len(cs) != 1 {
		t.Fatal("failed reload discarded peers")
	}
}

func TestRemoteHTTPRouteAtomicityAndHeldWire(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	credentials, key, e := amp.ReadCredentials(b.state, "jugui")
	if e != nil {
		t.Fatal(e)
	}
	payload := json.RawMessage(`{"message":"hello","type":"request"}`)
	env := amp.Envelope{From: credentials.Address, To: "agent@" + a.p.Name(), Subject: "Route", Priority: "normal"}
	signature, _ := amp.Sign(key, env, payload)
	route := amp.RouteRequest{To: env.To, Subject: env.Subject, Priority: env.Priority, Signature: signature, Payload: payload, IdempotencyKey: "federated-route"}
	request := func() (int, []byte) {
		raw, _ := json.Marshal(route)
		req, _ := http.NewRequest("POST", b.server.URL+"/v1/route", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+credentials.APIKey)
		res, e := b.server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		v, _ := io.ReadAll(res.Body)
		return res.StatusCode, v
	}
	if _, e = b.db.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON fed_outbox BEGIN SELECT RAISE(ABORT,'test outbox write failure'); END`); e != nil {
		t.Fatal(e)
	}
	if code, _ := request(); code != 500 {
		t.Fatal("failed outbox accepted", code)
	}
	traffic, e := b.p.Traffic(t.Context(), 100, time.Time{})
	if e != nil || len(traffic) != 0 {
		t.Fatal("mail survived outbox rollback", e)
	}
	if _, e = b.db.Exec(`DROP TRIGGER fail_outbox`); e != nil {
		t.Fatal(e)
	}
	code, raw := request()
	if code != 200 {
		t.Fatal("route", code)
	}
	var response struct{ ID, Status, Method string }
	_ = json.Unmarshal(raw, &response)
	if response.ID == "" || response.Status != "queued" || response.Method != "federation" {
		t.Fatal("route did not queue")
	}
	code, again := request()
	if code != 200 || !bytes.Equal(raw, again) {
		t.Fatal("remote route idempotency")
	}
	var count int
	if e = b.db.QueryRow(`SELECT count(*) FROM fed_outbox`).Scan(&count); e != nil || count != 1 {
		t.Fatal("outbox duplicated", e)
	}
	b.run(t)
	delivery(t, b, response.ID, core.DeliveryHeld)
	// HTTP reads carry external metadata while held mail stays out of pickup.
	req, _ := http.NewRequest("GET", b.server.URL+"/v1/messages/"+response.ID, nil)
	req.Header.Set("Authorization", "Bearer "+credentials.APIKey)
	res, e := b.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	var wire amp.Message
	e = json.NewDecoder(res.Body).Decode(&wire)
	res.Body.Close()
	if e != nil || wire.Remote != a.p.Name() || wire.Delivery != core.DeliveryHeld {
		t.Fatal("wire external metadata", e)
	}
	req, _ = http.NewRequest("GET", b.server.URL+"/v1/info", nil)
	res, e = b.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	var info struct{ Federation bool }
	_ = json.NewDecoder(res.Body).Decode(&info)
	res.Body.Close()
	if !info.Federation {
		t.Fatal("federation flag missing")
	}
}
func TestApprovalReceiptRollbackRecoveryAndPreapprovalRelease(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	b.run(t)
	first := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	second := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, first.ID, core.DeliveryHeld)
	delivery(t, b, second.ID, core.DeliveryHeld)
	if _, e := a.db.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON fed_outbox BEGIN SELECT RAISE(ABORT,'test receipt failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := a.f.DecideGrant(t.Context(), b.p.Name(), first.From, "agent", true); e == nil {
		t.Fatal("receipt failure ignored")
	}
	for _, id := range []string{first.ID, second.ID} {
		m, e := a.p.Get(t.Context(), id)
		if e != nil || m.Status != core.MailHeld {
			t.Fatal("release survived receipt rollback", e)
		}
	}
	if _, e := a.db.Exec(`DROP TRIGGER fail_receipt`); e != nil {
		t.Fatal(e)
	}
	a.run(t)
	delivery(t, b, first.ID, core.DeliveryDelivered)
	delivery(t, b, second.ID, core.DeliveryDelivered)
	// Another participant is held and then approved via an explicit share.
	other := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	delivery(t, b, other.ID, core.DeliveryDelivered)
	// Grant origins remain shared when added through the connection editor.
	a2, b2 := setup(t, "owner"), setup(t, "guest")
	pair(t, a2, b2)
	share(t, a2, b2)
	a2.run(t)
	b2.run(t)
	pending := send(t, b2, "guest", "agent@"+a2.p.Name(), "")
	delivery(t, b2, pending.ID, core.DeliveryHeld)
	share(t, a2, b2, pending.From)
	delivery(t, b2, pending.ID, core.DeliveryDelivered)
	gs, e := a2.f.Grants(t.Context())
	if e != nil || len(gs) != 1 || gs[0].Origin != "shared" {
		t.Fatal("preapproval did not release preserving origin", e)
	}
}
func TestPeerLimitPendingCapAndFailedPairLimit(t *testing.T) {
	t.Run("peer aggregate", func(t *testing.T) {
		a, b := setup(t, "milo"), setup(t, "jugui")
		pair(t, a, b)
		share(t, a, b, "jugui@"+b.p.Name())
		m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
		body := bodyFor(t, b, m)
		for i := 0; i < 100; i++ {
			if i%20 == 0 {
				a.f.mu.Lock()
				delete(a.f.rates, "recipient:"+b.p.Name()+":agent")
				a.f.mu.Unlock()
			}
			v := body
			v.Envelope.ID = fmt.Sprintf("aggregate_%d", i)
			if code, _ := signed(t, b, a, "deliver", v, time.Now().Unix(), ""); code != 202 {
				t.Fatal("aggregate limited early", i, code)
			}
		}
		a.f.mu.Lock()
		delete(a.f.rates, "recipient:"+b.p.Name()+":agent")
		a.f.mu.Unlock()
		body.Envelope.ID = "aggregate_over"
		if code, _ := signed(t, b, a, "deliver", body, time.Now().Unix(), ""); code != 429 {
			t.Fatal("aggregate limit missing", code)
		}
	})
	t.Run("pending cap", func(t *testing.T) {
		a, b := setup(t, "milo"), setup(t, "jugui")
		pair(t, a, b)
		share(t, a, b)
		m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
		body := bodyFor(t, b, m)
		_, key, _ := amp.ReadCredentials(b.state, "jugui")
		for i := 0; i < 51; i++ {
			if i%20 == 0 {
				a.f.mu.Lock()
				delete(a.f.rates, "recipient:"+b.p.Name()+":agent")
				a.f.mu.Unlock()
			}
			v := body
			v.Envelope.ID = fmt.Sprintf("pending_%d", i)
			v.Envelope.From = fmt.Sprintf("sender%d@%s", i, b.p.Name())
			v.Envelope.Signature, _ = amp.Sign(key, v.Envelope, v.Payload)
			want := 202
			if i == 50 {
				want = 429
			}
			if code, _ := signed(t, b, a, "deliver", v, time.Now().Unix(), ""); code != want {
				t.Fatal("pending cap", i, code)
			}
		}
	})
	t.Run("failed pair limit", func(t *testing.T) {
		a, b := setup(t, "milo"), setup(t, "jugui")
		bad := pairBody{Version: 1, Provider: b.p.Name(), URL: b.server.URL, PublicKey: b.f.pub, Secret: "incorrect"}
		for i := 0; i < 11; i++ {
			want := 403
			if i == 10 {
				want = 429
			}
			if code, _ := signed(t, b, a, "pair", bad, time.Now().Unix(), ""); code != want {
				t.Fatal("failed pair rate", i, code)
			}
		}
	})
}
func TestPolicyPreviewNoInitiationAndURLValidation(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	if !b.p.Check("jugui", "agent@"+a.p.Name()).Allow {
		t.Fatal("user policy preview denied")
	}
	gs, _ := b.f.Grants(t.Context())
	if len(gs) != 0 {
		t.Fatal("policy preview created initiation")
	}
	if a.p.Check("jugui@"+b.p.Name(), "agent").Allow {
		t.Fatal("unapproved inbound preview allowed")
	}
	share(t, a, b, "jugui@"+b.p.Name())
	if !a.p.Check("jugui@"+b.p.Name(), "agent").Allow {
		t.Fatal("approved inbound preview denied")
	}
	for _, url := range []string{"http://example.com", "http://100.64.0.2:4100", "https://name:password@example.com", "file:///tmp/x", "https://example.com/?secret=x"} {
		if validURL(url) {
			t.Fatal("invalid federation URL accepted")
		}
	}
	for _, url := range []string{"http://127.0.0.1:4119", "http://[::1]:4119", "https://lolad.example.ts.net"} {
		if !validURL(url) {
			t.Fatal("valid federation URL rejected")
		}
	}
}

func TestWireLimitsSignaturePinAndCanonicalParticipants(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	pair(t, a, b)
	share(t, a, b)
	m := send(t, b, "jugui", "agent@"+a.p.Name(), "")
	body := bodyFor(t, b, m)
	_, key, _ := amp.ReadCredentials(b.state, "jugui")
	attachment := body
	attachment.Payload = json.RawMessage(`{"type":"request","message":"hello","attachments":[]}`)
	attachment.Envelope.Signature, _ = amp.Sign(key, attachment.Envelope, attachment.Payload)
	if code, _ := signed(t, b, a, "deliver", attachment, time.Now().Unix(), ""); code != 422 {
		t.Fatal("attachments accepted", code)
	}
	wrongSignature := body
	wrongSignature.Envelope.Signature = "invalid"
	if code, _ := signed(t, b, a, "deliver", wrongSignature, time.Now().Unix(), ""); code != 403 {
		t.Fatal("bad envelope signature accepted", code)
	}
	canonical := body
	canonical.Envelope.From = strings.ToUpper(body.Envelope.From)
	canonical.Envelope.To = strings.ToUpper(body.Envelope.To)
	canonical.Envelope.Signature, _ = amp.Sign(key, canonical.Envelope, canonical.Payload)
	if code, _ := signed(t, b, a, "deliver", canonical, time.Now().Unix(), ""); code != 202 {
		t.Fatal("case-insensitive participants rejected", code)
	}
	if code, _ := signed(t, b, a, "deliver", body, time.Now().Unix(), ""); code != 202 {
		t.Fatal("same canonical participants conflict", code)
	}
	got, e := a.p.Get(t.Context(), body.Envelope.ID)
	if e != nil || got.From != m.From || got.To != m.To {
		t.Fatal("participant canonicalization", e)
	}
	req, _ := http.NewRequest("POST", a.server.URL+"/v1/federation/deliver", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	res, e := a.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatal("oversized body accepted", res.StatusCode)
	}
	// A valid signature from an unpaired key must not authenticate a pinned peer.
	other := setup(t, "impostor")
	otherBody := bodyFor(t, b, m)
	if code, _ := signed(t, other, a, "deliver", otherBody, time.Now().Unix(), ""); code != 403 {
		t.Fatal("unpaired provider accepted", code)
	}
	req, _ = http.NewRequest("POST", a.server.URL+"/v1/federation/deliver", strings.NewReader("{}"))
	req.Header.Set("X-AMP-Provider", b.p.Name())
	req.Header.Set("X-AMP-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-AMP-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(other.f.key, []byte(req.Header.Get("X-AMP-Timestamp")+".{}"))))
	res, e = a.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("wrong provider key accepted", res.StatusCode)
	}
}

func TestPairWithoutOwnURLAndLoginPin(t *testing.T) {
	a, b := setup(t, "milo"), setup(t, "jugui")
	a.f.o.URL = ""
	if _, e := a.f.CreateInvite(t.Context()); e == nil || a.f.Info(t.Context()).Problem == "" {
		t.Fatal("missing URL not reported")
	}
	invite, e := b.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.f.AcceptInvite(t.Context(), invite.Code); e != nil {
		t.Fatal("cannot accept invite without own URL", e)
	}
	if e = b.f.Reload(); e != nil {
		t.Fatal("blank peer URL cannot reload", e)
	}
	// Test the Tailscale identity observed during pairing on a separate pair.
	owner, guest := setup(t, "owner"), setup(t, "guest")
	invite, e = owner.f.CreateInvite(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(invite.Code, "lolad-invite:"))
	var c inviteCode
	_ = json.Unmarshal(raw, &c)
	body := pairBody{Version: 1, Provider: guest.p.Name(), URL: guest.server.URL, PublicKey: guest.f.pub, Secret: c.Secret}
	if code, _ := signed(t, guest, owner, "pair", body, time.Now().Unix(), "guest@example.test"); code != 200 {
		t.Fatal("pair with login", code)
	}
	cs, e := owner.f.Connections(t.Context())
	if e != nil || len(cs) != 1 || cs[0].Login != "guest@example.test" {
		t.Fatal("pair login not pinned", e)
	}
	if code, _ := signed(t, guest, owner, "pair", body, time.Now().Unix(), "someone@example.test"); code != 403 {
		t.Fatal("pair retry bypassed pinned login", code)
	}
}
