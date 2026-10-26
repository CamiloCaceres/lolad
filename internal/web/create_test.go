package web_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

func TestNewAgentPreviewAndDiscovery(t *testing.T) {
	e := setup(t)
	for _, path := range []string{"/new", "/new?mode=adopt"} {
		code, body := e.get(path)
		for _, want := range []string{"Before you create", "Your first message", "Inherit my skills", "Barebones", "/Users/you/agents", "Discovered directories", "Atlas", "Ready to adopt", "Already in lolad", "CLAUDE.md is missing", "data-adopt-dir=", "new-progress", `src="/static/new.js`} {
			if code != 200 || !strings.Contains(body, want) {
				t.Errorf("%s missing %q (%d)", path, want, code)
			}
		}
	}
}
func TestNewAgentPurposeSkillsAndStart(t *testing.T) {
	for _, start := range []bool{true, false} {
		t.Run(map[bool]string{true: "start", false: "later"}[start], func(t *testing.T) {
			e := setup(t)
			form := url.Values{"flow": {"v3"}, "name": {"Maple"}, "mission": {"Track my commitments.\nAsk when priorities conflict."}, "skills": {"barebones"}}
			if start {
				form.Set("start", "on")
			}
			resp, _ := e.do("POST", "/new", form, true)
			if resp.StatusCode != 204 || resp.Header.Get("HX-Redirect") != "/inbox/maple?created=1" {
				t.Fatalf("create %d %s", resp.StatusCode, resp.Header.Get("HX-Redirect"))
			}
			a, err := e.svc.Get(context.Background(), "maple")
			if err != nil || a.Runtime.Harness != core.HarnessClaude || a.Runtime.Skills != core.SkillsBarebones || !strings.Contains(a.Runtime.OnWake, "Track my commitments.") {
				t.Fatalf("agent %+v %v", a, err)
			}
			if a.Runtime.Autostart == nil || *a.Runtime.Autostart != start {
				t.Fatalf("autostart %+v", a.Runtime.Autostart)
			}
			mail, err := e.svc.Conversation(context.Background(), "maple", 10, time.Time{})
			if err != nil || len(mail) != 1 || mail[0].Body != form.Get("mission") {
				t.Fatalf("mission mail %+v %v", mail, err)
			}
		})
	}
}
func TestNewAgentInlineValidationDoesNotCreate(t *testing.T) {
	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"purpose", url.Values{"flow": {"v3"}, "name": {"Maple"}}, "Describe the job"},
		{"collision", url.Values{"name": {"Lola"}}, "This slug already exists"},
		{"invalid slug", url.Values{"name": {"Maple"}, "slug": {"../maple"}}, "Use 1–63"},
		{"unsafe setup", url.Values{"name": {"Maple & Co"}}, "Setup cannot use"},
		{"existing directory", url.Values{"name": {"Atlas"}}, "This directory already contains"},
		{"relative directory", url.Values{"name": {"Maple"}, "dir": {"relative/path"}}, "absolute directory path"},
		{"invalid skills", url.Values{"name": {"Maple"}, "skills": {"unexpected"}}, "Choose inherited skills"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			before, _ := e.svc.List(context.Background())
			resp, body := e.do("POST", "/new", tc.form, true)
			after, _ := e.svc.List(context.Background())
			if resp.StatusCode != 422 || !strings.Contains(body, tc.want) || len(before) != len(after) {
				t.Fatalf("%d: %s", resp.StatusCode, body)
			}
		})
	}
}

type failedCreator struct{ core.Service }

func (f failedCreator) Create(context.Context, core.CreateRequest) (core.AgentStatus, error) {
	return core.AgentStatus{Agent: core.Agent{Name: "maple"}}, errors.New("identity registration failed")
}
func TestNewAgentPartialFailureKeepsInputAndRecovery(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	srv := httptest.NewServer(web.New(failedCreator{f}, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)
	e := &env{t: t, srv: srv}
	resp, body := e.do("POST", "/new", url.Values{"name": {"Maple"}, "mission": {"Own the calendar"}, "harness": {"codewhale"}, "skills": {"barebones"}, "dir": {"/tmp/maple"}}, true)
	for _, want := range []string{`value="Maple"`, `Own the calendar`, `value="codewhale" selected`, `value="barebones" selected`, `Recover by adopting`, `dir=%2Ftmp%2Fmaple`, `/agents/maple/settings`} {
		if resp.StatusCode != 422 || !strings.Contains(body, want) {
			t.Errorf("missing %q: %d", want, resp.StatusCode)
		}
	}
}
func TestAdoptionPreservesRuntimeSelectionOnFailure(t *testing.T) {
	e := setup(t)
	resp, body := e.do("POST", "/adopt", url.Values{"dir": {"relative/agent"}, "harness": {"codewhale"}, "model": {"custom-model"}, "permissions": {"default"}}, true)
	for _, want := range []string{`value="relative/agent"`, `value="custom-model"`, `value="default" selected`, `value="codewhale" selected`, `Enter an absolute directory path`} {
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Errorf("missing %q (%d)", want, resp.StatusCode)
		}
	}
}

func TestAdoptionRejectsRegisteredDirectory(t *testing.T) {
	e := setup(t)
	agents, _ := e.svc.List(context.Background())
	a := agents[0]
	resp, body := e.do("POST", "/adopt", url.Values{"dir": {a.Dir}, "slug": {"other-slug"}, "harness": {"claude"}}, true)
	if resp.StatusCode != 422 || !strings.Contains(body, "already registered") || !strings.Contains(body, "/agents/"+a.Name+"/settings") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}
