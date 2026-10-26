// Package hooks translates short-lived harness hooks into daemon events.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func canonical(event string) string {
	switch event {
	case "SessionStart", "session_start":
		return "session_start"
	case "UserPromptSubmit", "message_submit", "prompt_submit":
		return "prompt_submit"
	case "PreToolUse", "tool_call_before", "pre_tool", "session_busy":
		return "pre_tool"
	case "PostToolUse", "tool_call_after", "post_tool":
		return "post_tool"
	case "Stop", "turn_end", "stop", "session_idle":
		return "stop"
	case "Notification", "notification":
		return "notification"
	case "PermissionRequest", "waiting_for_user", "permission":
		return "permission"
	case "SessionEnd", "session_end":
		return "session_end"
	}
	return event
}
func Map(event string, payload map[string]any, agent string) core.HookEvent {
	now := time.Now()
	e := core.HookEvent{Agent: agent, Event: canonical(event), At: now, Seq: now.UnixNano()}
	str := func(k string) string { s, _ := payload[k].(string); return s }
	e.SessionID = str("session_id")
	e.Detail = str("message")
	if e.Detail == "" {
		e.Detail = str("tool_name")
	}
	switch e.Event {
	case "prompt_submit", "pre_tool":
		e.State = core.StateBusy
	case "stop":
		e.State = core.StateIdle
	case "session_start":
		e.State = core.StateStarting
	case "permission":
		e.State = core.StateWaiting
	case "session_end":
		e.State = core.StateStopped
	case "notification":
		switch str("notification_type") {
		case "idle_prompt":
			e.State = core.StateIdle
		case "permission_prompt", "permission":
			e.State = core.StateWaiting
		}
	}
	return e
}

// Run always fails open: a daemon outage must never break an agent turn.
func Run(ctx context.Context, event string, stdin io.Reader, stdout io.Writer, env func(string) string) int {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	type decoded struct {
		p   map[string]any
		err error
	}
	ch := make(chan decoded, 1)
	go func() {
		var p map[string]any
		e := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&p)
		ch <- decoded{p, e}
	}()
	var p map[string]any
	select {
	case <-ctx.Done():
		return 0
	case d := <-ch:
		if d.err != nil && d.err != io.EOF {
			return 0
		}
		p = d.p
	}
	if p == nil {
		p = map[string]any{}
	}
	if p["session_id"] == nil {
		p["session_id"] = env("CODEWHALE_SESSION_ID")
	}
	if p["tool_name"] == nil {
		p["tool_name"] = env("DEEPSEEK_TOOL_NAME")
	}
	if p["message"] == nil {
		p["message"] = env("DEEPSEEK_ERROR")
	}
	ev := Map(event, p, env("LOLAD_AGENT"))
	if ev.Agent == "" || env("LOLAD_URL") == "" {
		return 0
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(env("LOLAD_URL"), "/")+"/api/hook", bytes.NewReader(data))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var reply core.HookReply
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply) != nil {
		return 0
	}
	if env("LOLAD_HARNESS") == "codewhale" {
		return 0
	}
	if ev.Event == "stop" && reply.Block {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"decision": "block", "reason": reply.Reason})
	}
	if ev.Event == "session_start" && reply.Context != "" {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": reply.Context}})
	}
	return 0
}
func NewHandler(sink core.HookSink) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var ev core.HookEvent
		dec := json.NewDecoder(r.Body)
		if dec.Decode(&ev) != nil || ev.Agent == "" || ev.Event == "" {
			http.Error(w, "invalid hook event", 400)
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			http.Error(w, "invalid hook event", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sink.HandleHook(r.Context(), ev))
	})
}
