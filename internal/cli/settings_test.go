package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestSettingsCommands(t *testing.T) {
	t.Setenv("LOLAD_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	rt := core.Runtime{Harness: core.HarnessClaude, Model: "haiku"}
	var method, path, value string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/a/runtime" && r.Method == "GET" {
			json.NewEncoder(w).Encode(rt)
			return
		}
		if r.URL.Path == "/api/agents/a" {
			json.NewEncoder(w).Encode(core.AgentStatus{Agent: core.Agent{Name: "a"}, PendingRestart: true, PendingReason: "env file changed"})
			return
		}
		method, path = r.Method, r.URL.Path
		switch path {
		case "/api/agents/a/runtime":
			if e := json.NewDecoder(r.Body).Decode(&rt); e != nil {
				t.Error(e)
			}
			json.NewEncoder(w).Encode(core.AgentStatus{Agent: core.Agent{Name: "a", Runtime: rt}, PendingRestart: true, PendingReason: "runtime changed"})
		case "/api/agents/a/env/TOKEN":
			if r.Method == "PUT" {
				var b map[string]string
				json.NewDecoder(r.Body).Decode(&b)
				value = b["value"]
			}
			w.Write([]byte(`{"ok":true}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	t.Setenv("LOLAD_URL", server.URL)
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"model", "a", "sonnet"}, "PUT", "/api/agents/a/runtime"},
		{[]string{"skills", "a"}, "GET", "/api/agents/a/skills"},
		{[]string{"skills", "a", "mode", "barebones"}, "PUT", "/api/agents/a/runtime"},
		{[]string{"skills", "a", "add", "user:example"}, "POST", "/api/agents/a/skills"},
		{[]string{"skills", "a", "rm", "agent:example"}, "DELETE", "/api/agents/a/skills/agent:example"},
		{[]string{"env", "a", "set", "TOKEN"}, "PUT", "/api/agents/a/env/TOKEN"},
		{[]string{"env", "a", "rm", "TOKEN"}, "DELETE", "/api/agents/a/env/TOKEN"},
		{[]string{"env", "a", "ls"}, "GET", "/api/agents/a/env"},
		{[]string{"email", "a", "show"}, "GET", "/api/agents/a/email"},
		{[]string{"email", "a", "test", "a@example.com"}, "POST", "/api/agents/a/email/test"},
	} {
		var out, err bytes.Buffer
		if code := Run(t.Context(), tc.args, strings.NewReader("write-only-test-value\n"), &out, &err); code != 0 {
			t.Fatalf("%v: %s", tc.args, &err)
		}
		if method != tc.method || path != tc.path {
			t.Fatalf("%v: %s %s", tc.args, method, path)
		}
		if strings.Contains(out.String()+err.String(), "write-only-test-value") {
			t.Fatal("secret printed")
		}
		if tc.method != "GET" && tc.args[0] != "email" && !strings.Contains(out.String(), "lolad restart a") {
			t.Fatal("missing restart hint")
		}
	}
	if value != "write-only-test-value" || rt.Model != "sonnet" || rt.Skills != core.SkillsBarebones {
		t.Fatal("settings lost")
	}
	var out, err bytes.Buffer
	if Run(t.Context(), []string{"env", "a", "set", "TOKEN", "must-never-be-argv"}, strings.NewReader(""), &out, &err) != 1 {
		t.Fatal("env accepted secret argument")
	}
	if strings.Contains(err.String(), "must-never-be-argv") {
		t.Fatal("secret argument echoed")
	}
}
