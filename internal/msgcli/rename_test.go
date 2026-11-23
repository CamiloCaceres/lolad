package msgcli

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

// An MCP server started before a rename keeps running in the agent's session
// after it: lolad restarts, the session does not. Its message_user and
// ask_user calls must still go through.
func TestMCPServerOutlivesRename(t *testing.T) {
	_, before, state := mailbox(t) // tenant "home", agent a, human milo
	db, err := sql.Open("sqlite", state+"/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The rename, as applyPendingRename does it, then lolad starts as milo-mac.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = amp.RenameAddresses(t.Context(), tx, "home", "milo-mac"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = amp.RenameIdentityFiles(state, "home", "milo-mac"); err != nil {
		t.Fatal(err)
	}
	o := policy.DefaultOrg()
	o.Tenant = "milo-mac"
	o.Defaults.Independents = "all"
	var last *amp.Provider
	start := func(former ...string) string {
		p, err := amp.New(db, state, "milo-mac", policy.New(o, "a"), amp.WithOrg(func() core.Org { return o }), amp.WithFormerNames(former))
		if err != nil {
			t.Fatal(err)
		}
		last = p
		s := httptest.NewServer(p)
		t.Cleanup(s.Close)
		return s.URL
	}
	after := start("home")
	afterP := last
	call := func(c *client, text string) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "message_user", "arguments": map[string]any{"text": text}}})
		var out bytes.Buffer
		if err := c.serveMCP(strings.NewReader(string(b)), &out); err != nil {
			t.Fatal(err)
		}
		var r struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Result
	}
	sent := func(name string, r map[string]any) string {
		t.Helper()
		text := r["content"].([]any)[0].(map[string]any)["text"].(string)
		if r["isError"].(bool) || !strings.HasPrefix(text, "Sent") {
			t.Fatalf("%s: %s", name, text)
		}
		return strings.TrimSuffix(text[strings.Index(text, "msg_"):], ").")
	}
	check := func(name, id string) {
		t.Helper()
		w, err := last.Wire(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		creds, key, _ := amp.ReadCredentials(state, "a")
		if w.Envelope.From != "a@milo-mac.lolad.local" || creds.Address != w.Envelope.From || !amp.Verify(key.Public().(ed25519.PublicKey), w.Envelope, w.Payload) {
			t.Fatalf("%s: stored wire must be signed under the new address: %+v", name, w.Envelope)
		}
	}

	// A server from the old binary: it never re-reads its credentials and
	// signs as a@home.lolad.local. The daemon accepts and re-signs.
	old := *before
	old.base, old.state = after, ""
	last = afterP
	check("old binary", sent("old binary", call(&old, "from before the rename")))

	// The fixed server re-reads its identity per call and signs as the new
	// address, even against a daemon that knows no former names.
	fixed := *before
	fixed.base, fixed.state, fixed.name = start(), state, "a"
	check("re-read", sent("re-read", call(&fixed, "after the rename")))
	if fixed.credentials.Address != "a@milo-mac.lolad.local" {
		t.Fatalf("credentials not refreshed: %s", fixed.credentials.Address)
	}

	// Without former names, the old signature is refused, as before.
	stale := *before
	stale.base, stale.state = start(), ""
	if r := call(&stale, "x"); !r["isError"].(bool) {
		t.Fatalf("an old-address signature must need a former name: %v", r)
	}
	// A signature under the old address with another key is refused.
	forged := *before
	forged.base, forged.state = after, ""
	_, forged.key, _ = amp.ReadCredentials(state, "milo")
	if r := call(&forged, "x"); !r["isError"].(bool) {
		t.Fatalf("another key must be refused: %v", r)
	}
}
