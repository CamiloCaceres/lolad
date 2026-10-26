package web_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

type federationSpy struct {
	core.Service
	update                  core.ConnectionUpdate
	provider, remote, local string
	allow                   bool
	accept                  string
	removed                 string
}

func (s *federationSpy) AcceptInvite(ctx context.Context, code string) (core.Connection, error) {
	s.accept = code
	return core.Connection{}, fmt.Errorf("invalid: %s", code)
}
func (s *federationSpy) UpdateConnection(ctx context.Context, p string, u core.ConnectionUpdate) (core.Connection, error) {
	s.provider = p
	s.update = u
	return s.Service.UpdateConnection(ctx, p, u)
}
func (s *federationSpy) DecideGrant(ctx context.Context, p, r, l string, a bool) (core.Grant, error) {
	s.provider = p
	s.remote = r
	s.local = l
	s.allow = a
	return s.Service.DecideGrant(ctx, p, r, l, a)
}
func (s *federationSpy) RemoveConnection(ctx context.Context, p string) error {
	s.removed = p
	return s.Service.RemoveConnection(ctx, p)
}
func fedSetup(t *testing.T) (*federationSpy, func(string, string, url.Values) (int, string)) {
	t.Helper()
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	sp := &federationSpy{Service: f}
	server := httptest.NewServer(web.New(sp, web.Options{}))
	t.Cleanup(server.Close)
	return sp, func(method, path string, form url.Values) (int, string) {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := noRedirect.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}
func TestFederationSecretsStayInCreationResponse(t *testing.T) {
	sp, do := fedSetup(t)
	_, body := do("POST", "/connections/invite", nil)
	if !strings.Contains(body, "lolad-invite:demo-only-not-a-real-invite") {
		t.Fatal("invite absent from creation response")
	}
	for _, p := range []string{"/connections", "/", "/agents/ultron", "/org", "/traffic"} {
		status, b := do("GET", p, nil)
		if status != 200 {
			t.Fatalf("%s: %d", p, status)
		}
		if strings.Contains(b, "lolad-invite:") {
			t.Fatalf("invite leaked on %s", p)
		}
	}
	code := "SECRET-INVITE-MUST-NOT-ECHO"
	status, b := do("POST", "/connections/accept", url.Values{"code": {code}})
	if status != 422 || sp.accept != code {
		t.Fatal("accept not called")
	}
	if strings.Contains(b, code) {
		t.Fatal("code echoed in accept error")
	}
	if !strings.Contains(b, `type="password" autocomplete="off"`) {
		t.Fatal("accept input must hide code")
	}
}
func TestFederationActions(t *testing.T) {
	for _, decision := range []string{"allow", "block"} {
		t.Run(decision, func(t *testing.T) {
			sp, do := fedSetup(t)
			status, _ := do("POST", "/grants/decide", url.Values{"decision": {decision}, "peer": {"jugui.lolad.local"}, "remote": {"jugui@jugui.lolad.local"}, "local": {"ultron"}})
			if status != 303 || sp.provider != "jugui.lolad.local" || sp.remote != "jugui@jugui.lolad.local" || sp.local != "ultron" || sp.allow != (decision == "allow") {
				t.Fatal("incorrect grant arguments")
			}
		})
	}
	sp, do := fedSetup(t)
	status, _ := do("POST", "/connections/jugui.lolad.local", url.Values{"action": {"shares"}, "share_ultron": {"yes"}, "mode_ultron": {"specific"}, "remote_ultron": {"nova@jugui.lolad.local"}})
	if status != 303 || len(sp.update.Shares) != 1 || sp.update.Shares[0].Local != "ultron" || sp.update.Shares[0].Remote[0] != "nova@jugui.lolad.local" {
		t.Fatal("share selection lost")
	}
	for _, action := range []string{"pause", "resume"} {
		status, _ = do("POST", "/connections/jugui.lolad.local", url.Values{"action": {action}})
		if status != 303 || sp.update.Paused == nil || *sp.update.Paused != (action == "pause") {
			t.Fatal("pause/resume not applied")
		}
	}
	status, _ = do("POST", "/connections/jugui.lolad.local", url.Values{"action": {"shares"}, "share_ultron": {"yes"}, "mode_ultron": {"specific"}})
	if status != 422 {
		t.Fatal("empty pre-approval selection must not silently share with everyone")
	}
	status, _ = do("POST", "/connections/jugui.lolad.local", url.Values{"action": {"shares"}, "share_ultron": {"yes"}, "mode_ultron": {"specific"}, "remote_ultron": {"stranger@other.lolad.local"}})
	if status != 422 {
		t.Fatal("foreign contact must not be accepted")
	}
	status, _ = do("POST", "/connections/jugui.lolad.local", url.Values{"action": {"shares"}})
	if status != 303 || sp.update.Shares == nil || len(sp.update.Shares) != 0 {
		t.Fatal("unsharing every agent must use an empty non-nil share list")
	}
	status, _ = do("POST", "/connections/jugui.lolad.local/remove", nil)
	if status != 422 || sp.removed != "" {
		t.Fatal("removal needs confirmation")
	}
	status, _ = do("POST", "/connections/jugui.lolad.local/remove", url.Values{"confirm": {"remove"}})
	if status != 303 || sp.removed != "jugui.lolad.local" {
		t.Fatal("removal not applied")
	}
}
func TestFederationRemoteScreens(t *testing.T) {
	_, do := fedSetup(t)
	for _, tc := range []struct {
		path string
		want []string
	}{{"/", []string{"Jugui wants to message Ultron", "2 waiting", "Jugui&#39;s lolad"}}, {"/inbox/jugui@jugui.lolad.local", []string{"Awaiting approval", "Queued", "Failed", "Delivered", "recipient is no longer shared", "Message Jugui"}}, {"/inbox/nova@jugui.lolad.local", []string{"Message Nova", "No messages with Nova yet"}}, {"/connections", []string{"Studio", "3 messages queued", "Agents they can reach", "Nova"}}, {"/agents/ultron", []string{"Shared with", "Approved sender: Nova"}}, {"/org", []string{"Outside this lolad", "external-edge"}}, {"/traffic?connection=jugui.lolad.local", []string{"External · jugui.lolad.local", "Awaiting approval", "Received"}}} {
		status, b := do("GET", tc.path, nil)
		if status != 200 {
			t.Fatalf("%s: %d", tc.path, status)
		}
		if tc.path == "/traffic?connection=jugui.lolad.local" && strings.Contains(b, "Message blocked by policy") {
			t.Fatal("connection filter includes local policy events")
		}
		for _, want := range tc.want {
			if !strings.Contains(b, want) {
				t.Errorf("%s: missing %q", tc.path, want)
			}
		}
	}
}
