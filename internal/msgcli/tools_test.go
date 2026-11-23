package msgcli

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
	_ "modernc.org/sqlite"
)

func TestBuildUserMessage(t *testing.T) {
	dns := []core.Block{
		{Type: "link", Label: "Open Hostinger DNS", URL: "https://hpanel.hostinger.com/domain/example.com/dns"},
		{Type: "values", Title: "DNS record", Rows: []core.ValueRow{{Label: "Type", Value: "A"}, {Label: "Name", Value: "app"}, {Label: "Points to", Value: "203.0.113.7"}}},
	}
	m, err := buildUserMessage(true, toolInput{Title: "Add one DNS record", Text: "Leave the other records alone.", Blocks: dns, Task: &toolTask{Done: "Added"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Chat.Kind != "ask" || !m.Chat.Task || strings.Join(m.Chat.Options, "|") != "Added|I couldn't do it" || len(m.Chat.Blocks) != 3 || m.Type != "request" || m.Subject != "Add one DNS record" {
		t.Fatalf("task: %+v", m)
	}
	for _, want := range []string{"Leave the other records alone.", "Open Hostinger DNS: https://hpanel.hostinger.com/", "DNS record:\nType: A\nName: app\nPoints to: 203.0.113.7"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
	// Plain text stays plain: no blocks, the body says it all.
	if m, err = buildUserMessage(false, toolInput{Text: "Deployed.\nAll green."}); err != nil || m.Chat.Blocks != nil || m.Body != "Deployed.\nAll green." || m.Type != "notification" || m.Subject != "Deployed." {
		t.Fatalf("plain: %+v %v", m, err)
	}
	m, err = buildUserMessage(false, toolInput{Kind: "update", Blocks: []core.Block{{Type: "command", Code: "sudo caddy reload", Where: "on the server"}}})
	if err != nil || m.Type != "update" || !strings.Contains(m.Body, "Run on the server:\n```\nsudo caddy reload\n```") {
		t.Fatalf("command: %+v %v", m, err)
	}
	m, err = buildUserMessage(true, toolInput{Text: "Send me what you see", Fields: []toolField{{Label: "Screenshot", Type: "file"}, {Label: "Error output", Type: "logs", Optional: true}, {Name: "CF_TOKEN", Label: "Cloudflare token", Type: "secret"}}})
	if err != nil || len(m.Chat.Fields) != 3 || m.Chat.Fields[0].Name != "screenshot" || m.Chat.Fields[1].Name != "error_output" || m.Chat.Fields[2].Name != "CF_TOKEN" {
		t.Fatalf("fields: %+v %v", m.Chat.Fields, err)
	}
	for _, c := range []struct {
		ask  bool
		in   toolInput
		want string
	}{
		{false, toolInput{}, "give text or blocks"},
		{false, toolInput{Text: "x", Options: []string{"a", "b"}}, "ask_user"},
		{false, toolInput{Text: "x", Kind: "ask"}, "kind must be"},
		{true, toolInput{Text: "x", Options: []string{"a"}}, "2 to 6"},
		{true, toolInput{Text: "x", Options: []string{"a", "b"}, Task: &toolTask{}}, "one of options, fields or task"},
		{true, toolInput{Text: "x", Fields: []toolField{{Label: "Key", Type: "secret"}}}, "env var"},
		{true, toolInput{Text: "x", Fields: []toolField{{Label: "Size", Type: "text", Options: []string{"S", "M"}}}}, "only a choice"},
		{true, toolInput{Blocks: []core.Block{{Type: "link", URL: "javascript:alert(1)"}}}, "http(s) url"},
		{true, toolInput{Blocks: []core.Block{{Type: "values", Rows: []core.ValueRow{{Label: "Type", Value: ""}}}}}, "label and a value"},
		{true, toolInput{Blocks: []core.Block{{Type: "table"}}}, "unknown type"},
	} {
		if _, err := buildUserMessage(c.ask, c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.in, err, c.want)
		}
	}
	if _, err := decodeToolInput([]byte(`{"text":"x","blokcs":[]}`)); err == nil || !strings.Contains(err.Error(), "blokcs") {
		t.Errorf("unknown keys must be refused: %v", err)
	}
}

// mailbox is a provider with agent a and human milo, and a client for a.
func mailbox(t *testing.T) (*amp.Provider, *client, string) {
	t.Helper()
	state := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(state, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	o := policy.DefaultOrg()
	o.Defaults.Independents = "all"
	p, err := amp.New(db, state, "home", policy.New(o, "a"), amp.WithOrg(func() core.Org { return o }))
	if err != nil {
		t.Fatal(err)
	}
	for name, kind := range map[string]string{"a": "agent", "milo": "human"} {
		if _, err = p.EnsureIdentity(t.Context(), name, kind); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(p)
	t.Cleanup(server.Close)
	creds, key, err := amp.ReadCredentials(state, "a")
	if err != nil {
		t.Fatal(err)
	}
	return p, &client{ctx: context.Background(), base: server.URL, credentials: creds, key: key, http: &http.Client{Timeout: 10 * time.Second}, out: &bytes.Buffer{}}, state
}

func TestMCPServer(t *testing.T) {
	p, c, _ := mailbox(t)
	call := func(id int, name string, args any) string {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		return string(b)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, "ask_user", map[string]any{"title": "Add the DNS record", "blocks": []any{
			map[string]any{"type": "values", "rows": []any{map[string]any{"label": "Type", "value": "A"}, map[string]any{"label": "Points to", "value": "203.0.113.7"}}},
		}, "task": map[string]any{"done": "Added"}}),
		call(4, "message_user", map[string]any{"text": "x", "fields": []any{}, "options": []string{"a", "b"}}),
		call(5, "message_user", map[string]any{"blocks": []any{map[string]any{"type": "command", "code": "brew install caddy"}}}),
		`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := c.serveMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	res := map[int]map[string]any{}
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad line %q", sc.Text())
		}
		id, _ := r["id"].(float64)
		res[int(id)] = r
	}
	if len(res) != 7 { // 1-6 and the parse error (id null → 0)
		t.Fatalf("responses: %v", res)
	}
	init := res[1]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" || init["serverInfo"].(map[string]any)["name"] != "lolad" {
		t.Fatalf("initialize: %v", init)
	}
	tools := res[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "message_user" || tools[1].(map[string]any)["name"] != "ask_user" {
		t.Fatalf("tools: %v", tools)
	}
	if schema, _ := json.Marshal(tools[1].(map[string]any)["inputSchema"]); !strings.Contains(string(schema), `"task"`) || !strings.Contains(string(schema), `"values"`) {
		t.Fatalf("ask_user schema: %s", schema)
	}
	text := func(id int) (string, bool) {
		r := res[id]["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
	}
	if s, bad := text(3); bad || !strings.Contains(s, "waiting for the user") {
		t.Fatalf("ask_user: %s", s)
	}
	if s, bad := text(4); !bad || !strings.Contains(s, "ask_user") {
		t.Fatalf("message_user with options must fail with a fix: %s", s)
	}
	if s, bad := text(5); bad || !strings.Contains(s, "Sent") {
		t.Fatalf("message_user: %s", s)
	}
	if res[6]["error"].(map[string]any)["code"].(float64) != -32601 || res[0]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("errors: %v %v", res[6], res[0])
	}
	mail, err := p.Unread(t.Context(), "milo")
	if err != nil || len(mail) != 2 {
		t.Fatalf("mail: %v %v", mail, err)
	}
	b, _ := json.Marshal(mail[0].Context["userchat"])
	var uc core.UserChat
	if err = json.Unmarshal(b, &uc); err != nil || uc.Kind != "ask" || !uc.Task || len(uc.Blocks) != 1 || uc.Blocks[0].Rows[1].Value != "203.0.113.7" {
		t.Fatalf("ask mail: %s %v", b, err)
	}
	if mail[0].Subject != "Add the DNS record" || !strings.Contains(mail[0].Body, "Points to: 203.0.113.7") {
		t.Fatalf("ask mail: %+v", mail[0])
	}
}

func TestTellUserJSON(t *testing.T) {
	p, _, state := mailbox(t)
	t.Setenv("LOLAD_STATE", state)
	t.Setenv("LOLAD_AGENT", "a")
	server := httptest.NewServer(p)
	defer server.Close()
	t.Setenv("LOLAD_URL", server.URL)
	var out, errb bytes.Buffer
	code := Run(context.Background(), []string{"tell-user", "ask", "--json", `{"text":"Run this on the server","blocks":[{"type":"command","code":"sudo systemctl restart caddy"}],"task":{}}`}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	m, err := p.Get(t.Context(), strings.TrimSpace(out.String()))
	if err != nil || !strings.Contains(m.Body, "sudo systemctl restart caddy") {
		t.Fatal(m, err)
	}
	errb.Reset()
	if code = Run(context.Background(), []string{"tell-user", "say", "--json", `{"text":"x","task":{}}`}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "ask_user") {
		t.Fatalf("say with a task: %d %s", code, errb.String())
	}
}
