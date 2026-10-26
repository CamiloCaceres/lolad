package api

import (
	"context"
	"errors"
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/http/httptest"
	"strings"
	"testing"
)

type federationFake struct {
	core.Service
	code      string
	update    core.ConnectionUpdate
	acceptErr error
}

func (f *federationFake) Info(context.Context) core.FederationInfo {
	return core.FederationInfo{Enabled: true}
}
func (f *federationFake) Connections(context.Context) ([]core.Connection, error) {
	return []core.Connection{{Provider: "jugui.lolad.local"}}, nil
}
func (f *federationFake) CreateInvite(context.Context) (core.Invite, error) {
	return core.Invite{Code: "single-use-test-secret"}, nil
}
func (f *federationFake) AcceptInvite(_ context.Context, code string) (core.Connection, error) {
	f.code = code
	return core.Connection{Provider: "jugui.lolad.local"}, f.acceptErr
}
func (f *federationFake) UpdateConnection(_ context.Context, _ string, u core.ConnectionUpdate) (core.Connection, error) {
	f.update = u
	return core.Connection{}, nil
}
func (f *federationFake) RemoveConnection(context.Context, string) error { return nil }
func (f *federationFake) Grants(context.Context) ([]core.Grant, error)   { return []core.Grant{}, nil }
func (f *federationFake) DecideGrant(context.Context, string, string, string, bool) (core.Grant, error) {
	return core.Grant{}, nil
}
func (f *federationFake) RemoteContacts(context.Context, string) ([]core.RemoteContact, error) {
	return []core.RemoteContact{}, nil
}
func TestFederationRoutes(t *testing.T) {
	f := &federationFake{}
	h := New(f, Options{})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/federation", "", 200}, {"GET", "/api/connections", "", 200},
		{"POST", "/api/connections", `{"code":"stdin-invite"}`, 200}, {"POST", "/api/connections/invite", "", 200},
		{"PATCH", "/api/connections/jugui.lolad.local", `{"shares":[]}`, 200},
		{"DELETE", "/api/connections/jugui.lolad.local", "", 200}, {"GET", "/api/grants", "", 200},
		{"POST", "/api/grants/decide", `{"peer":"jugui.lolad.local","remote":"jugui@jugui.lolad.local","local":"ultron","allow":false}`, 200},
		{"POST", "/api/grants/decide", `{"local":"ultron"}`, 422},
		{"GET", "/api/remote-contacts?local=ultron", "", 200}, {"POST", "/api/connections", `{} {}`, 422},
		{"GET", "/api/connections/invite", "", 405},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		invite := tc.method == "POST" && tc.path == "/api/connections/invite"
		if strings.Contains(w.Body.String(), "single-use-test-secret") != invite {
			t.Fatal("invite available outside create response")
		}
		if invite && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invite response cacheable")
		}
	}
	if f.code != "stdin-invite" || f.update.Shares == nil || len(f.update.Shares) != 0 {
		t.Fatal("accept or last unshare lost")
	}
}

func TestInviteAcceptanceNeverReflectsPeerErrors(t *testing.T) {
	f := &federationFake{acceptErr: errors.New("remote accidentally echoed invitation-secret")}
	w := httptest.NewRecorder()
	New(f, Options{}).ServeHTTP(w, httptest.NewRequest("POST", "/api/connections", strings.NewReader(`{"code":"invitation-secret"}`)))
	if w.Code != 422 || strings.Contains(w.Body.String(), "invitation-secret") {
		t.Fatal("pairing failure exposed submitted secret")
	}
}
