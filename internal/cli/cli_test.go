package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestHTTPCommands(t *testing.T) {
	t.Setenv("LOLAD_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var got core.CreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/create":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
				t.Error(e)
			}
		case "/api/agents/worker/restart":
			if r.Method != "POST" || r.URL.Query().Get("fresh") != "true" {
				t.Error("restart", r.Method, r.URL)
			}
		case "/api/mail/send":
			var o core.Outgoing
			json.NewDecoder(r.Body).Decode(&o)
			if o.To != "worker" || o.Body != "hello" || o.Subject != "hello" || o.Context["userchat"] == nil {
				t.Errorf("send: %+v", o)
			}
		default:
			t.Error("unexpected path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("LOLAD_URL", server.URL)
	for _, args := range [][]string{{"new", "Worker", "--harness", "claude", "--model", "haiku", "--start"}, {"restart", "--fresh", "worker"}, {"send", "worker", "hello"}} {
		var out, err bytes.Buffer
		if code := Run(t.Context(), args, strings.NewReader(""), &out, &err); code != 0 {
			t.Fatalf("%v: %d %s", args, code, &err)
		}
	}
	if got.Name != "Worker" || got.Harness != core.HarnessClaude || got.Model != "haiku" || !got.Start {
		t.Fatalf("new: %+v", got)
	}
}
func TestDaemonDown(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := listener.Addr().String()
	listener.Close()
	t.Setenv("LOLAD_URL", "http://"+addr)
	t.Setenv("LOLAD_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	var out, err bytes.Buffer
	if Run(t.Context(), []string{"ls"}, strings.NewReader(""), &out, &err) != 1 || !strings.Contains(err.String(), "lolad serve") {
		t.Fatalf("missing helpful failure: %s", &err)
	}
}

func TestSubjectFromText(t *testing.T) {
	for _, tc := range []struct{ text, want string }{{"  First line  \nSecond", "First line"}, {strings.Repeat("界", 90), strings.Repeat("界", 80)}} {
		if got := subjectFromText(tc.text); got != tc.want {
			t.Fatalf("%q != %q", got, tc.want)
		}
	}
}
func TestSendExplicitSubject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got core.Outgoing
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got.Subject != "Explicit" {
			t.Error(got.Subject)
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	t.Setenv("LOLAD_URL", server.URL)
	t.Setenv("LOLAD_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out, err bytes.Buffer
	if code := Run(t.Context(), []string{"send", "worker", "first\nsecond", "--subject", "Explicit"}, strings.NewReader(""), &out, &err); code != 0 {
		t.Fatal(err.String())
	}
}
