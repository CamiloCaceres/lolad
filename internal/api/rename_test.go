package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

type renameFake struct {
	federationFake
	got core.RenameRequest
	err error
}

type outboxErr struct{}

func (outboxErr) Error() string       { return "changed since it was loaded: 2 messages" }
func (outboxErr) Unwrap() error       { return core.ErrConflict }
func (outboxErr) UserMessage() string { return "2 messages to other lolads are still on the way." }
func (outboxErr) Outbox() int         { return 2 }

func (f *renameFake) LoladName(context.Context) (core.LoladName, error) {
	return core.LoladName{Name: "home", Provider: "home.lolad.local", Restart: "auto", Connections: []core.Connection{}}, nil
}
func (f *renameFake) CheckLoladName(_ context.Context, in string) (core.NameCheck, error) {
	n, e := policy.CheckRename("home", in)
	if e != nil {
		return core.NameCheck{Message: e.(policy.RenameError).Msg}, nil
	}
	return core.NameCheck{OK: true, Name: n}, nil
}
func (f *renameFake) RenameLolad(_ context.Context, r core.RenameRequest) (core.RenameAccepted, error) {
	f.got = r
	if f.err != nil {
		return core.RenameAccepted{}, f.err
	}
	n, e := policy.CheckRename("home", r.Name)
	return core.RenameAccepted{Name: n, Restarting: true}, e
}

func TestRenameRoutes(t *testing.T) {
	f := &renameFake{}
	h := New(f, Options{})
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	if st, body := do("GET", "/api/lolad/name", ""); st != 200 || !strings.Contains(body, `"name":"home"`) || !strings.Contains(body, `"restart":"auto"`) {
		t.Fatal(st, body)
	}
	if st, body := do("GET", "/api/lolad/name/check?name=Milo+PC", ""); st != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"name":"milo-pc"`) {
		t.Fatal(st, body)
	}
	if st, body := do("GET", "/api/lolad/name/check?name=home", ""); st != 200 || !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "already called") {
		t.Fatal(st, body)
	}
	if st, body := do("POST", "/api/lolad/name", `{"name":"Milo PC","anyway":true}`); st != 202 || !strings.Contains(body, `"name":"milo-pc"`) || !strings.Contains(body, `"restarting":true`) || !f.got.Anyway {
		t.Fatal(st, body)
	}
	for _, in := range []string{`{"name":""}`, `{"name":"home"}`} {
		if st, body := do("POST", "/api/lolad/name", in); st != 422 || !strings.Contains(body, `"message":"`) || strings.Contains(body, "invalid request") {
			t.Fatal(in, st, body)
		}
	}
	f.err = outboxErr{}
	if st, body := do("POST", "/api/lolad/name", `{"name":"milo-pc"}`); st != 409 || !strings.Contains(body, `"outbox":2`) || !strings.Contains(body, "still on the way") {
		t.Fatal(st, body)
	}
	if st, _ := do("POST", "/api/lolad/name", `not json`); st != 422 {
		t.Fatal(st)
	}
}

type fixErr struct{ peopleError }

func (fixErr) Fix() string { return "rename_lolad" }

// A refusal with a fix carries it, so the dashboard can offer the button.
func TestFriendWriteFix(t *testing.T) {
	w := httptest.NewRecorder()
	friendWrite(w, nil, fixErr{})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"fix":"rename_lolad"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	friendWrite(w, nil, peopleError{})
	if strings.Contains(w.Body.String(), "fix") {
		t.Fatal(w.Body.String())
	}
}
