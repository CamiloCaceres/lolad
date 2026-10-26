package msgcli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
	_ "modernc.org/sqlite"
)

func TestEndToEnd(t *testing.T) {
	state := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(state, "mail.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	o := policy.DefaultOrg()
	o.Defaults.Independents = "all"
	o.Rules = []core.Rule{{From: "a", To: "blocked", Allow: false, Note: "private department"}}
	p, e := amp.New(db, state, "home", policy.New(o, "a", "b", "blocked"), amp.WithOrg(func() core.Org { return o }))
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a", "b", "milo", "blocked"} {
		kind := "agent"
		if name == "milo" {
			kind = "human"
		}
		if _, e = p.EnsureIdentity(t.Context(), name, kind); e != nil {
			t.Fatal(e)
		}
	}
	server := httptest.NewServer(p)
	defer server.Close()
	t.Setenv("LOLAD_STATE", state)
	t.Setenv("LOLAD_URL", server.URL)
	run := func(name string, args ...string) (int, string, string) {
		t.Helper()
		t.Setenv("LOLAD_AGENT", name)
		var out, err bytes.Buffer
		code := Run(context.Background(), args, &out, &err)
		return code, out.String(), err.String()
	}
	success := func(name string, args ...string) string {
		t.Helper()
		code, out, err := run(name, args...)
		if code != 0 {
			t.Fatalf("%s %v: exit %d: %s", name, args, code, err)
		}
		return out
	}
	aID := strings.TrimSpace(success("a", "send", "b", "Review", "Can you review?", "--type", "request", "--priority", "high", "--context", `{"n":9007199254740993,"nested":{"x":true}}`))
	if !strings.HasPrefix(aID, "msg_") {
		t.Fatal(aID)
	}
	inbox := success("b", "inbox", "--json")
	var messages []amp.Message
	if e = json.Unmarshal([]byte(inbox), &messages); e != nil || len(messages) != 1 || messages[0].ID != aID {
		t.Fatal(inbox, e)
	}
	if !strings.Contains(success("b", "inbox"), "Review") {
		t.Fatal("inbox output")
	}
	read := success("b", "read", aID[len(aID)-8:])
	if !strings.Contains(read, "Can you review?") || !strings.Contains(read, "9007199254740993") {
		t.Fatal(read)
	}
	if !strings.Contains(success("b", "inbox"), "No messages") {
		t.Fatal("read not marked")
	}
	if !strings.Contains(success("b", "inbox", "--all"), aID) {
		t.Fatal("history lost read mail")
	}
	bID := strings.TrimSpace(success("b", "reply", aID, "Looks good"))
	reply, e := p.Get(t.Context(), bID)
	if e != nil || reply.InReplyTo != aID || reply.ThreadID != aID || reply.ToName != "a" || !reply.Verified {
		t.Fatal(reply, e)
	}
	thread := success("a", "thread", bID)
	if !strings.Contains(thread, "Looks good") || !strings.Contains(thread, "Can you review?") {
		t.Fatal(thread)
	}
	askID := strings.TrimSpace(success("a", "tell-user", "ask", "Ship it?", "--option", "Yes", "--option", "No"))
	unread, e := p.Unread(t.Context(), "milo")
	if e != nil || len(unread) != 1 || unread[0].ID != askID || !unread[0].Verified {
		t.Fatal(unread, e)
	}
	chatBytes, _ := json.Marshal(unread[0].Context["userchat"])
	var chat core.UserChat
	if e = json.Unmarshal(chatBytes, &chat); e != nil || chat.Kind != "ask" || strings.Join(chat.Options, ",") != "Yes,No" {
		t.Fatal(chat, e)
	}
	for _, kind := range []string{"say", "update", "deliver"} {
		id := strings.TrimSpace(success("a", "tell-user", kind, "  First line  \nBody"))
		m, err := p.Get(t.Context(), id)
		if err != nil || m.Subject != "First line" {
			t.Fatal(m, err)
		}
	}
	id := strings.TrimSpace(success("a", "tell-user", "say", strings.Repeat("界", 90)))
	m, err := p.Get(t.Context(), id)
	if err != nil || m.Subject != strings.Repeat("界", 80) {
		t.Fatal(m, err)
	}
	id = strings.TrimSpace(success("a", "tell-user", "ask", "Question", "--subject", "Explicit"))
	m, err = p.Get(t.Context(), id)
	if err != nil || m.Subject != "Explicit" {
		t.Fatal(m, err)
	}
	who := success("a", "who")
	if !strings.Contains(who, "b@home.lolad.local") || strings.Contains(who, "blocked@home.lolad.local") {
		t.Fatal(who)
	}
	code, _, reason := run("a", "send", "blocked", "Denied", "hello")
	if code != 2 || !strings.Contains(reason, "private department") {
		t.Fatalf("403: code %d reason %s", code, reason)
	}
	if !strings.Contains(success("", "--as", "a", "inbox"), bID) {
		t.Fatal("--as override")
	}
	if !strings.Contains(success("", "help"), "tell-user") {
		t.Fatal("help")
	}
	code, _, reason = run("a", "send", "b", "Subject", "Body", "--context", "not JSON")
	if code != 1 || !strings.Contains(reason, "context") {
		t.Fatal(code, reason)
	}
	code, _, _ = run("", "inbox")
	if code != 1 {
		t.Fatal("missing identity accepted")
	}
}
