package amp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
	_ "modernc.org/sqlite"
)

type fixture struct {
	p      *Provider
	server *httptest.Server
	state  string
	events []core.Event
	mu     sync.Mutex
}

func setup(t *testing.T) *fixture {
	t.Helper()
	state := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(state, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	o := policy.DefaultOrg()
	o.Defaults.Independents = "all"
	o.Rules = []core.Rule{{From: "a", To: "c", Allow: false, Note: "restricted"}}
	f := &fixture{state: state}
	p, e := New(db, state, o.Tenant, policy.New(o, "a", "b", "c"), WithOrg(func() core.Org { return o }), WithAgents(func() []core.Agent {
		return []core.Agent{{Name: "a", Display: "Agent A", Role: "engineer"}, {Name: "b"}, {Name: "c"}, {Name: "unkeyed"}}
	}), WithEventSink(func(e core.Event) { f.mu.Lock(); f.events = append(f.events, e); f.mu.Unlock() }))
	if e != nil {
		t.Fatal(e)
	}
	f.p = p
	for _, n := range []string{"a", "b", "c", "milo", "lolad"} {
		kind := "agent"
		if n == "milo" {
			kind = "human"
		}
		if n == "lolad" {
			kind = "system"
		}
		if _, e = p.EnsureIdentity(t.Context(), n, kind); e != nil {
			t.Fatal(e)
		}
	}
	f.server = httptest.NewServer(p)
	t.Cleanup(f.server.Close)
	return f
}
func (f *fixture) key(t *testing.T, name string) Credentials {
	t.Helper()
	c, _, e := ReadCredentials(f.state, name)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func (f *fixture) request(t *testing.T, name, method, path string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if b, ok := body.([]byte); ok {
		raw = b
	} else if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	req, e := http.NewRequest(method, f.server.URL+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if name != "" {
		req.Header.Set("Authorization", "Bearer "+f.key(t, name).APIKey)
	}
	res, e := f.server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}
func (f *fixture) route(t *testing.T, name, to string) RouteRequest {
	t.Helper()
	c, key, e := ReadCredentials(f.state, name)
	if e != nil {
		t.Fatal(e)
	}
	address, e := ExpandAddress(to, "home")
	if e != nil {
		t.Fatal(e)
	}
	payload := json.RawMessage(`{"context":{"precise":9007199254740993},"message":"Hello","type":"request"}`)
	env := Envelope{From: c.Address, To: address, Subject: "Hi", Priority: "normal"}
	signature, e := Sign(key, env, payload)
	if e != nil {
		t.Fatal(e)
	}
	return RouteRequest{To: to, Subject: env.Subject, Priority: env.Priority, Signature: signature, Payload: payload}
}
func TestIdentitySecretsAndRecovery(t *testing.T) {
	f := setup(t)
	c, key, e := ReadCredentials(f.state, "a")
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range []string{"identity.json", "private.pem", "public.pem"} {
		s, e := os.Stat(filepath.Join(f.state, "identities", "a", file))
		if e != nil || s.Mode().Perm() != 0600 {
			t.Fatalf("permissions %s %v", file, e)
		}
	}
	var hash string
	if e = f.p.db.QueryRow(`SELECT api_key_hash FROM amp_identities WHERE name='a'`).Scan(&hash); e != nil || hash != keyHash(c.APIKey) || strings.Contains(hash, c.APIKey) {
		t.Fatal("key hash", e)
	}
	i, e := f.p.EnsureIdentity(t.Context(), "A", "agent")
	if e != nil || i != c.Identity {
		t.Fatal("ensure changed identity", e)
	}
	if _, e = f.p.EnsureIdentity(t.Context(), "../escape", "agent"); e == nil {
		t.Fatal("unsafe identity")
	}
	if _, e = f.p.db.Exec(`DELETE FROM amp_identities WHERE name='a'`); e != nil {
		t.Fatal(e)
	}
	i, e = f.p.EnsureIdentity(t.Context(), "a", "agent")
	if e != nil || i.Fingerprint != fingerprint(key.Public().(ed25519.PublicKey)) {
		t.Fatal("recovery", e)
	}
}
func TestRouteAuthPolicyLimitsIdempotency(t *testing.T) {
	f := setup(t)
	req := f.route(t, "a", "b")
	if code, _ := f.request(t, "", "POST", "/v1/route", req); code != 401 {
		t.Fatal(code)
	}
	unsigned := req
	unsigned.Signature = ""
	if code, b := f.request(t, "a", "POST", "/v1/route", unsigned); code != 403 || !bytes.Contains(b, []byte("invalid_signature")) {
		t.Fatalf("unsigned %d %s", code, b)
	}
	if code, b := f.request(t, "b", "POST", "/v1/route", req); code != 403 || !bytes.Contains(b, []byte("invalid_signature")) {
		t.Fatalf("wrong sender %d %s", code, b)
	}
	denied := f.route(t, "a", "c")
	if code, b := f.request(t, "a", "POST", "/v1/route", denied); code != 403 || !bytes.Contains(b, []byte("restricted")) {
		t.Fatalf("policy %d %s", code, b)
	}
	f.mu.Lock()
	events := append([]core.Event{}, f.events...)
	f.mu.Unlock()
	if len(events) != 1 || events[0].Kind != "policy" {
		t.Fatal("denial event missing")
	}
	if _, e := f.p.Send(t.Context(), "a", core.Outgoing{To: "c", Subject: "denied", Body: "x"}); !errors.Is(e, core.ErrDenied) {
		t.Fatalf("in-process bypass %v", e)
	}
	if code, _ := f.request(t, "a", "POST", "/v1/route", bytes.Repeat([]byte("x"), (1<<20)+1)); code != 413 {
		t.Fatal("limit", code)
	}
	tamper := req
	tamper.Payload = json.RawMessage(`{"message":"Changed","type":"request"}`)
	if code, _ := f.request(t, "a", "POST", "/v1/route", tamper); code != 403 {
		t.Fatal("tamper", code)
	}
	req.IdempotencyKey = "idk_test"
	raw, _ := json.Marshal(req)
	var forged map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&forged)
	forged["from"] = "c@home.lolad.local"
	forged["id"] = "client-id"
	forged["timestamp"] = "yesterday"
	code, b := f.request(t, "a", "POST", "/v1/route", forged)
	if code != 200 {
		t.Fatalf("route %d %s", code, b)
	}
	var res struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &res)
	m, e := f.p.Get(t.Context(), res.ID)
	if e != nil || m.FromName != "a" || m.ID == "client-id" || !m.Verified {
		t.Fatalf("from derivation %+v %v", m, e)
	}
	if got := fmt.Sprint(m.Context["precise"]); got != "9007199254740993" {
		t.Fatalf("number changed %s", got)
	}
	code, again := f.request(t, "a", "POST", "/v1/route", forged)
	if code != 200 || !bytes.Equal(b, again) {
		t.Fatalf("retry %d %s", code, again)
	}
	forged["subject"] = "different"
	if code, _ = f.request(t, "a", "POST", "/v1/route", forged); code != 409 {
		t.Fatal("idempotency conflict", code)
	}
	traffic, e := f.p.Traffic(t.Context(), 100, time.Time{})
	if e != nil || len(traffic) != 1 {
		t.Fatal("duplicate mail", len(traffic), e)
	}
	// Keys are scoped to the authenticated sender and expire after 24h.
	other := f.route(t, "b", "a")
	other.IdempotencyKey = "idk_test"
	if code, _ = f.request(t, "b", "POST", "/v1/route", other); code != 200 {
		t.Fatal("owner scope", code)
	}
	if _, e = f.p.db.Exec(`UPDATE amp_idempotency SET expires_at=?`, time.Now().Add(-time.Hour).Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	forged["subject"] = "Hi"
	if code, _ = f.request(t, "a", "POST", "/v1/route", forged); code != 200 {
		t.Fatal("expired key", code)
	}
}
func TestPendingAckLifecycleAndIsolation(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	m, e := f.p.Send(ctx, "a", core.Outgoing{To: "b", Subject: "First", Body: "one"})
	if e != nil {
		t.Fatal(e)
	}
	m2, e := f.p.Send(ctx, "a", core.Outgoing{To: "b", Subject: "Second", Body: "two"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.p.MarkNotified(ctx, m.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.p.MarkNotified(ctx, m.ID); e != nil {
		t.Fatal(e)
	}
	got, _ := f.p.Get(ctx, m.ID)
	if got.Status != core.MailNotified || got.Attempts != 2 {
		t.Fatalf("notify %+v", got)
	}
	code, b := f.request(t, "b", "GET", "/v1/messages/pending?limit=1", nil)
	var pending struct {
		Messages         []Message `json:"messages"`
		Count, Remaining int
	}
	_ = json.Unmarshal(b, &pending)
	if code != 200 || pending.Count != 1 || pending.Remaining != 1 {
		t.Fatalf("pending %d %s", code, b)
	}
	if pending.Messages[0].Envelope.Signature == "" {
		t.Fatal("signature lost")
	}
	if code, _ = f.request(t, "c", "GET", "/v1/messages/"+m.ID, nil); code != 404 {
		t.Fatal("other mailbox exposed", code)
	}
	if code, _ = f.request(t, "a", "POST", "/v1/messages/"+m.ID+"/read", nil); code != 403 {
		t.Fatal("sender marked recipient mail", code)
	}
	// Invalid batch must leave the valid message unread.
	if code, _ = f.request(t, "b", "POST", "/v1/messages/pending/ack", map[string]any{"ids": []string{m.ID, "missing"}}); code != 404 {
		t.Fatal(code)
	}
	got, _ = f.p.Get(ctx, m.ID)
	if got.Status == core.MailRead {
		t.Fatal("partial batch acknowledged")
	}
	if code, _ = f.request(t, "b", "DELETE", "/v1/messages/pending/"+m.ID, nil); code != 200 {
		t.Fatal(code)
	}
	if e = f.p.MarkNotified(ctx, m.ID); e != nil {
		t.Fatal(e)
	}
	got, _ = f.p.Get(ctx, m.ID)
	if got.Status != core.MailRead || got.ReadAt.IsZero() || got.Attempts != 2 {
		t.Fatal("read regressed", got)
	}
	if code, _ = f.request(t, "b", "POST", "/v1/messages/pending/ack", map[string]any{"ids": []string{m2.ID}}); code != 200 {
		t.Fatal(code)
	}
	code, b = f.request(t, "b", "GET", "/v1/messages/pending/count", nil)
	if code != 200 || !bytes.Contains(b, []byte(`"count":0`)) {
		t.Fatal(string(b))
	}
	if u, e := f.p.Unread(ctx, "b"); e != nil || len(u) != 0 {
		t.Fatal("unread lifecycle", u, e)
	}
}
func TestRegistrationDirectoryAndSubscribers(t *testing.T) {
	f := setup(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	req := map[string]string{"name": "invented", "public_key": publicPEM(pub), "key_algorithm": "Ed25519"}
	if code, _ := f.request(t, "", "POST", "/v1/register", req); code != 409 {
		t.Fatal(code)
	}
	req["name"] = "a"
	if code, _ := f.request(t, "", "POST", "/v1/register", req); code != 409 {
		t.Fatal(code)
	}
	req["name"] = "unkeyed"
	if code, b := f.request(t, "", "POST", "/v1/register", req); code != 201 || !bytes.Contains(b, []byte("amp_live_sk_")) {
		t.Fatalf("registration %d %s", code, b)
	}
	if code, b := f.request(t, "a", "GET", "/v1/agents/resolve/b", nil); code != 200 || !bytes.Contains(b, []byte("PUBLIC KEY")) {
		t.Fatal(code, string(b))
	}
	if code, b := f.request(t, "a", "GET", "/v1/agents", nil); code != 200 || !bytes.Contains(b, []byte("engineer")) {
		t.Fatal(code, string(b))
	}
	if code, _ := f.request(t, "a", "GET", "/v1/agents/me", nil); code != 200 {
		t.Fatal(code)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	f.p.OnMail(func(core.Mail) { close(entered); <-release })
	done := make(chan error, 1)
	go func() {
		_, e := f.p.Send(context.Background(), "a", core.Outgoing{To: "b", Subject: "test", Body: "test"})
		done <- e
	}()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber blocked Send")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("subscriber not called")
	}
}

func TestIdempotencyFailureRollsBackMail(t *testing.T) {
	f := setup(t)
	if _, e := f.p.db.Exec(`CREATE TRIGGER amp_fail_idempotency BEFORE INSERT ON amp_idempotency BEGIN SELECT RAISE(ABORT,'test failure'); END;`); e != nil {
		t.Fatal(e)
	}
	req := f.route(t, "a", "b")
	req.IdempotencyKey = "idk_rollback"
	if code, _ := f.request(t, "a", "POST", "/v1/route", req); code != 500 {
		t.Fatal(code)
	}
	messages, e := f.p.Traffic(t.Context(), 100, time.Time{})
	if e != nil || len(messages) != 0 {
		t.Fatal("failed key insertion left mail", messages, e)
	}
	if _, e = f.p.db.Exec(`DROP TRIGGER amp_fail_idempotency`); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			code, b := f.request(t, "a", "POST", "/v1/route", req)
			if code != 200 {
				t.Errorf("retry %d %s", code, b)
				return
			}
			var res struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(b, &res)
			ids <- res.ID
		})
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("concurrent retries duplicated", id, got)
		}
		id = got
	}
	messages, e = f.p.Traffic(t.Context(), 100, time.Time{})
	if e != nil || len(messages) != 1 {
		t.Fatal("concurrent duplicate", len(messages), e)
	}
}
