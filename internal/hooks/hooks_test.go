package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sinkFunc func(context.Context, core.HookEvent) core.HookReply

func (f sinkFunc) HandleHook(c context.Context, e core.HookEvent) core.HookReply { return f(c, e) }
func TestRoundTrip(t *testing.T) {
	var got core.HookEvent
	s := httptest.NewServer(NewHandler(sinkFunc(func(c context.Context, e core.HookEvent) core.HookReply {
		got = e
		return core.HookReply{Block: true, Reason: "read mail", Context: "hello"}
	})))
	defer s.Close()
	for _, event := range []string{"Stop", "SessionStart"} {
		var out bytes.Buffer
		env := func(k string) string { return map[string]string{"LOLAD_AGENT": "a", "LOLAD_URL": s.URL}[k] }
		if Run(context.Background(), event, strings.NewReader(`{"session_id":"sid"}`), &out, env) != 0 {
			t.Fatal("exit")
		}
		if got.Agent != "a" || got.SessionID != "sid" {
			t.Fatal(got)
		}
		var p map[string]any
		if json.Unmarshal(out.Bytes(), &p) != nil {
			t.Fatal(out.String())
		}
		if event == "Stop" && p["decision"] != "block" {
			t.Fatal(p)
		}
		if event == "SessionStart" && p["hookSpecificOutput"] == nil {
			t.Fatal(p)
		}
	}
}
func TestMappings(t *testing.T) {
	for _, tc := range []struct {
		event, kind string
		state       core.State
	}{{"message_submit", "", core.StateBusy}, {"tool_call_before", "", core.StateBusy}, {"turn_end", "", core.StateIdle}, {"waiting_for_user", "", core.StateWaiting}, {"Notification", "idle_prompt", core.StateIdle}, {"Notification", "permission_prompt", core.StateWaiting}, {"SessionEnd", "", core.StateStopped}} {
		e := Map(tc.event, map[string]any{"notification_type": tc.kind}, "a")
		if e.State != tc.state {
			t.Fatalf("%s => %s", tc.event, e.State)
		}
	}
}
func TestFailOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	Run(ctx, "Stop", strings.NewReader("{}"), &out, func(k string) string {
		return map[string]string{"LOLAD_URL": "http://127.0.0.1:1", "LOLAD_AGENT": "a"}[k]
	})
	if out.Len() != 0 || time.Since(start) > time.Second {
		t.Fatal("fail-open budget")
	}
}
func TestCodewhaleNoReply(t *testing.T) {
	s := httptest.NewServer(NewHandler(sinkFunc(func(c context.Context, e core.HookEvent) core.HookReply {
		if e.SessionID != "cw" {
			t.Error(e)
		}
		return core.HookReply{Block: true, Reason: "mail"}
	})))
	defer s.Close()
	var out bytes.Buffer
	Run(context.Background(), "turn_end", strings.NewReader(""), &out, func(k string) string {
		return map[string]string{"LOLAD_URL": s.URL, "LOLAD_AGENT": "a", "LOLAD_HARNESS": "codewhale", "CODEWHALE_SESSION_ID": "cw"}[k]
	})
	if out.Len() != 0 {
		t.Fatal(out.String())
	}
}
func TestHandlerBadRequest(t *testing.T) {
	h := NewHandler(sinkFunc(func(context.Context, core.HookEvent) core.HookReply { t.Fatal("called"); return core.HookReply{} }))
	for _, body := range []string{"{}", "{", `{"agent":"a","event":"stop"} {}`} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
