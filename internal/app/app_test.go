package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestCompositionAndHTTP(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	t.Setenv("LOLAD_STATE", state)
	agents := filepath.Join(dir, "agents")
	if e := os.MkdirAll(filepath.Join(agents, "head"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(agents, "head", "lolabot.yaml"), []byte("agent:\n  name: Head\nruntime:\n  harness: claude\n  autostart: false\n"), 0600); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(agents, "head", "CLAUDE.md"), []byte("# Head\n"), 0600)
	config := filepath.Join(dir, "config.yaml")
	if e := os.WriteFile(config, fmt.Appendf(nil, "listen: 127.0.0.1:4198\ntmux_socket: lolad-test-%d\nagents_dirs: [%s]\n", time.Now().UnixNano(), agents), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := app.New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	server := httptest.NewServer(api.New(a, api.Options{HookSink: a.Tracker, StateDir: state}))
	defer server.Close()
	call := func(method, path string, v any, status int) []byte {
		t.Helper()
		b, _ := json.Marshal(v)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(b))
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, data)
		}
		return data
	}
	call("GET", "/api/agents", nil, 200)
	ids, e := a.Provider.Identities(t.Context())
	if e != nil || len(ids) != 3 {
		t.Fatalf("identities: %v %v", ids, e)
	}
	mail, e := a.UserSend(t.Context(), core.Outgoing{To: "head", Subject: "hello", Body: "test"})
	if e != nil {
		t.Fatal(e)
	}
	call("POST", "/api/mail/"+mail.ID+"/read", nil, 403)
	_, e = a.Provider.Send(t.Context(), "head", core.Outgoing{To: a.Me().Name, Subject: "ask", Body: "choose", Context: map[string]any{"userchat": core.UserChat{Kind: "ask", Options: []string{"a", "b"}}}})
	if e != nil {
		t.Fatal(e)
	}
	c, e := a.Conversations(t.Context())
	if e != nil || len(c) != 1 || !c[0].Pending || c[0].Unread != 1 {
		t.Fatalf("conversations: %+v %v", c, e)
	}
	call("POST", "/api/mail/"+c[0].Last.ID+"/read", nil, 200)
	call("GET", "/api/mail/conversation/head", nil, 200)
	call("POST", "/api/agents/missing/start", nil, 404)
	call("POST", "/api/mail/send", map[string]string{"to": "missing", "body": "test"}, 404)
	org, _ := a.Org(t.Context())
	org.Rules = []core.Rule{{From: org.User.Name, To: "head", Allow: false, Note: "test"}}
	call("PUT", "/api/org", org, 200)
	call("POST", "/api/mail/send", core.Outgoing{To: "head", Body: "denied"}, 403)
	events, e := a.Events(t.Context(), "", 100)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range events {
		found = found || v.Kind == "policy"
	}
	if !found {
		t.Fatal("policy denial not recorded")
	}
	call("POST", "/api/hook", core.HookEvent{Agent: "head", Event: "stop", State: core.StateIdle, Seq: 1, At: time.Now()}, 200)
	s, _ := a.Tracker.State("head")
	if s != core.StateIdle {
		t.Fatalf("tracker: %s", s)
	}
	// Adoption outside scan roots must survive a daemon reload.
	adopted := filepath.Join(dir, "elsewhere")
	os.MkdirAll(adopted, 0700)
	os.WriteFile(filepath.Join(adopted, "lolabot.yaml"), []byte("agent:\n  name: Outside\n"), 0600)
	os.WriteFile(filepath.Join(adopted, "CLAUDE.md"), []byte("# Outside\n"), 0600)
	no := false
	call("POST", "/api/adopt", core.AdoptRequest{Dir: adopted, Runtime: core.Runtime{Harness: core.HarnessClaude, Autostart: &no}}, 200)
	if e = a.Reload(context.Background()); e != nil {
		t.Fatal(e)
	}
	call("GET", "/api/agents/outside", nil, 200)
	org, _ = a.Org(t.Context())
	org.Rules = append(org.Rules, core.Rule{From: "head", To: "outside", Allow: true})
	if e = a.SaveOrg(t.Context(), org); e != nil {
		t.Fatal(e)
	}
	busCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bus := a.Subscribe(busCtx)
	for _, edge := range [][2]string{{a.Me().Name, "outside"}, {"head", "outside"}, {"outside", a.Me().Name}, {"lolad", "outside"}} {
		mail, err := a.Provider.Send(t.Context(), edge[0], core.Outgoing{To: edge[1], Subject: "bus proof", Body: "test"})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(2 * time.Second)
		found := false
		for !found {
			select {
			case ev := <-bus:
				found = ev.Kind == "mail" && ev.Detail == mail.ID && ev.Agent != ""
			case <-deadline.C:
				t.Fatalf("missing mail event for %s -> %s", edge[0], edge[1])
			}
		}
		deadline.Stop()
	}
	if b, _ := os.ReadFile(config); !strings.Contains(string(b), adopted) {
		t.Fatal("adoption not persisted")
	}
	// Traversal cannot read another webhook token because the agent must exist.
	call("POST", "/hook/missing/test", nil, 404)
}
