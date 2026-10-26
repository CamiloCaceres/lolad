package amp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

func TestMigrationKeepsOldMail(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// This is the v3 schema, with a pre-existing message and no remote columns.
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	m := core.Mail{ID: "old", FromName: "milo", ToName: "agent", Subject: "Existing", Body: "Existing body", Status: core.MailUnread}
	raw, _ := json.Marshal(m)
	if _, e = db.Exec(`INSERT INTO amp_messages(id,from_name,to_name,thread_id,created_at,status,mail,wire) VALUES('old','milo','agent','old',?,'unread',?,'{}')`, time.Now().Format(time.RFC3339Nano), string(raw)); e != nil {
		t.Fatal(e)
	}
	p, e := New(db, t.TempDir(), "home", policy.New(policy.DefaultOrg(), "agent"))
	if e != nil {
		t.Fatal(e)
	}
	got, e := p.Get(t.Context(), "old")
	if e != nil || got.Subject != m.Subject || got.Body != m.Body || got.Remote != "" || got.Delivery != "" {
		t.Fatal("migration lost old mail", e)
	}
	if _, e = db.Exec(`UPDATE amp_messages SET remote='peer.lolad.local',delivery='queued',delivery_error='' WHERE id='old'`); e != nil {
		t.Fatal(e)
	}
	p, e = New(db, t.TempDir(), "home", policy.New(policy.DefaultOrg(), "agent"))
	if e != nil {
		t.Fatal("repeated migration", e)
	}
	got, e = p.Get(t.Context(), "old")
	if e != nil || got.Delivery != core.DeliveryQueued {
		t.Fatal("migration repeated destructively", e)
	}
}
func TestRemoteHeldIsolationAndAtomicRelease(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	payload := json.RawMessage(`{"message":"held","type":"request"}`)
	env := Envelope{ID: "remote_one", ThreadID: "remote_one", From: "remote@peer.lolad.local", To: "b@home.lolad.local", Subject: "Held", Priority: "normal", Timestamp: time.Now()}
	env.Signature, _ = Sign(key, env, payload)
	m, created, e := f.p.AcceptRemote(ctx, "peer.lolad.local", Message{Envelope: env, Payload: payload}, pub, true)
	if e != nil || !created || m.Status != core.MailHeld {
		t.Fatal("accept held", e)
	}
	notices := make(chan core.Mail, 4)
	f.p.OnMail(func(m core.Mail) { notices <- m })
	if e = f.p.MarkNotified(ctx, m.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.p.MarkRead(ctx, "b", m.ID); e != core.ErrDenied {
		t.Fatal("held marked read")
	}
	for _, path := range []string{"/v1/messages/pending", "/v1/messages?box=all", "/v1/messages/pending/count"} {
		code, b := f.request(t, "b", "GET", path, nil)
		if code != 200 {
			t.Fatal("held query", code)
		}
		var res map[string]any
		_ = json.Unmarshal(b, &res)
		if count, ok := res["count"].(float64); ok && count != 0 {
			t.Fatal("held leaked", path)
		}
	}
	if code, _ := f.request(t, "b", "GET", "/v1/messages/"+m.ID, nil); code != 404 {
		t.Fatal("held fetch exposed")
	}
	if code, _ := f.request(t, "b", "POST", "/v1/messages/pending/ack", map[string]any{"ids": []string{m.ID}}); code != 404 {
		t.Fatal("held ack accepted")
	}
	fail := func(context.Context, *sql.Tx, core.Mail) error { return core.ErrInvalid }
	if _, e = f.p.ResolveHeld(ctx, "peer.lolad.local", m.FromName, "b", true, fail); e == nil {
		t.Fatal("failed release succeeded")
	}
	got, _ := f.p.Get(ctx, m.ID)
	if got.Status != core.MailHeld {
		t.Fatal("release not rolled back")
	}
	select {
	case <-notices:
		t.Fatal("held notified before commit")
	default:
	}
	released, e := f.p.ResolveHeld(ctx, "peer.lolad.local", m.FromName, "b", true, nil)
	if e != nil || len(released) != 1 {
		t.Fatal("release", e)
	}
	select {
	case got := <-notices:
		if got.Status != core.MailUnread {
			t.Fatal("released status")
		}
	case <-time.After(time.Second):
		t.Fatal("release did not notify")
	}
	if _, e = f.p.ResolveHeld(ctx, "peer.lolad.local", m.FromName, "b", true, nil); e != nil {
		t.Fatal(e)
	}
	select {
	case <-notices:
		t.Fatal("release notified twice")
	default:
	}
	// A stale held response cannot overwrite a faster receipt.
	if _, e = f.p.db.Exec(`UPDATE amp_messages SET delivery='queued' WHERE id=?`, m.ID); e != nil {
		t.Fatal(e)
	}
	_, _ = f.p.SetDelivery(ctx, m.ID, core.DeliveryDelivered, "")
	got, e = f.p.SetDelivery(ctx, m.ID, core.DeliveryHeld, "")
	if e != nil || got.Delivery != core.DeliveryDelivered {
		t.Fatal("receipt regressed", e)
	}
	wire, e := f.p.Wire(ctx, m.ID)
	if e != nil || wire.Remote != "peer.lolad.local" || wire.Delivery != core.DeliveryDelivered {
		t.Fatal("wire state missing", e)
	}
}
