package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type settingsFake struct {
	core.Service
	called                 string
	key, value, path, hash string
}

func (f *settingsFake) Get(context.Context, string) (core.AgentStatus, error) {
	return core.AgentStatus{Agent: core.Agent{Runtime: core.Runtime{Harness: core.HarnessClaude}}}, nil
}
func (f *settingsFake) UpdateRuntime(_ context.Context, n string, rt core.Runtime) (core.AgentStatus, error) {
	f.called = "runtime"
	return core.AgentStatus{Agent: core.Agent{Name: n, Runtime: rt}}, nil
}
func (f *settingsFake) Models(core.Harness) []core.ModelOption {
	f.called = "models"
	return []core.ModelOption{}
}
func (f *settingsFake) Skills(context.Context, string) ([]core.Skill, error) {
	f.called = "skills"
	return []core.Skill{}, nil
}
func (f *settingsFake) AddSkill(_ context.Context, n, id string) (core.Skill, error) {
	f.called = "add-skill"
	f.key = id
	return core.Skill{}, nil
}
func (f *settingsFake) RemoveSkill(_ context.Context, n, id string) error {
	f.called = "rm-skill"
	f.key = id
	return nil
}
func (f *settingsFake) Env(context.Context, string) ([]core.EnvVar, error) {
	f.called = "env"
	return []core.EnvVar{{Key: "TOKEN", Set: true, Length: 8}}, nil
}
func (f *settingsFake) SetEnv(_ context.Context, n, k, v string) error {
	f.called = "set-env"
	f.key = k
	f.value = v
	return nil
}
func (f *settingsFake) DeleteEnv(_ context.Context, n, k string) error {
	f.called = "rm-env"
	f.key = k
	return nil
}
func (f *settingsFake) Files(context.Context, string) ([]core.FileInfo, error) {
	f.called = "files"
	return []core.FileInfo{}, nil
}
func (f *settingsFake) ReadFile(_ context.Context, n, p string) (core.FileContent, error) {
	f.called = "read-file"
	f.path = p
	return core.FileContent{}, nil
}
func (f *settingsFake) WriteFile(_ context.Context, n, p, c, h string) (core.FileContent, error) {
	f.called = "write-file"
	f.path = p
	f.hash = h
	return core.FileContent{}, core.ErrConflict
}
func (f *settingsFake) Email(context.Context, string) (core.EmailSettings, error) {
	f.called = "email"
	return core.EmailSettings{}, nil
}
func (f *settingsFake) SaveEmail(context.Context, string, core.EmailSettings) (core.EmailSettings, error) {
	f.called = "save-email"
	return core.EmailSettings{}, nil
}
func (f *settingsFake) TestEmail(context.Context, string, string) (core.EmailTest, error) {
	f.called = "test-email"
	return core.EmailTest{OK: true}, nil
}

func TestSettingsRoutes(t *testing.T) {
	f := &settingsFake{}
	h := New(f, Options{})
	for _, tc := range []struct {
		method, path, body, called string
		status                     int
	}{
		{"GET", "/api/models?harness=claude", "", "models", 200},
		{"GET", "/api/agents/a/models", "", "models", 200},
		{"PUT", "/api/agents/a/runtime", `{"harness":"claude","model":"haiku"}`, "runtime", 200},
		{"GET", "/api/agents/a/skills", "", "skills", 200},
		{"POST", "/api/agents/a/skills", `{"from":"user:example"}`, "add-skill", 200},
		{"DELETE", "/api/agents/a/skills/agent:example", "", "rm-skill", 200},
		{"GET", "/api/agents/a/env", "", "env", 200},
		{"PUT", "/api/agents/a/env/TOKEN", `{"value":"never-echo"}`, "set-env", 200},
		{"PUT", "/api/agents/a/env", `{"key":"TOKEN","value":"never-echo"}`, "set-env", 200},
		{"DELETE", "/api/agents/a/env/TOKEN", "", "rm-env", 200},
		{"GET", "/api/agents/a/files", "", "files", 200},
		{"GET", "/api/agents/a/files/brain/nested/file.md", "", "read-file", 200},
		{"PUT", "/api/agents/a/files/brain/nested/file.md", `{"content":"text","base_hash":"old"}`, "write-file", 409},
		{"GET", "/api/agents/a/email", "", "email", 200},
		{"PUT", "/api/agents/a/email", `{"watch":"none"}`, "save-email", 200},
		{"POST", "/api/agents/a/email/test", `{"address":"a@example.com"}`, "test-email", 200},
	} {
		t.Run(tc.called+tc.path, func(t *testing.T) {
			f.called = ""
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || f.called != tc.called {
				t.Fatalf("%d %s, called %s", w.Code, w.Body, f.called)
			}
			if strings.Contains(w.Body.String(), "never-echo") {
				t.Fatal("secret echoed")
			}
			var v any
			if json.Unmarshal(w.Body.Bytes(), &v) != nil {
				t.Fatal("invalid JSON response")
			}
		})
	}
	if f.path != "brain/nested/file.md" || f.hash != "old" {
		t.Fatal("file args lost")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/agents/a/env/TOKEN", strings.NewReader(`{"value":`)))
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
}
