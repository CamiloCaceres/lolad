package web_test

import (
	"context"
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

// hubSpy adds core.Hubber to the fake service.
type hubSpy struct {
	core.Service
	status            *core.HubStatus
	url, handle, code string
	left              bool
	joinErr           error
}

func (s *hubSpy) Hub(context.Context) (*core.HubStatus, error) { return s.status, nil }
func (s *hubSpy) JoinHub(_ context.Context, u, h, c string) (*core.HubStatus, error) {
	s.url, s.handle, s.code = u, h, c
	if s.joinErr != nil {
		return nil, s.joinErr
	}
	s.status = &core.HubStatus{URL: u, Handle: h, State: core.HubConnected}
	return s.status, nil
}
func (s *hubSpy) LeaveHub(context.Context) error { s.left = true; s.status = nil; return nil }

func TestHubCard(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	sp := &hubSpy{Service: f}
	server := httptest.NewServer(web.New(sp, web.Options{}))
	t.Cleanup(server.Close)
	do := func(method, path string, form url.Values) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := noRedirect.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	_, b := do("GET", "/connections", nil)
	if !strings.Contains(b, `action="/connections/hub/join"`) || !strings.Contains(b, `name="code" type="password"`) {
		t.Fatal("join form missing")
	}
	code := "hubinv_SECRET-MUST-NOT-ECHO"
	sp.joinErr = &hubErr{"invalid request: hub: handle is taken (try \"milo-2\")"}
	status, b := do("POST", "/connections/hub/join", url.Values{"url": {"https://hub.example.com"}, "handle": {"Milo"}, "code": {code}})
	if status != 422 || sp.code != code || sp.handle != "milo" {
		t.Fatal("join not called", status, sp.handle)
	}
	if strings.Contains(b, code) || !strings.Contains(b, "handle is taken") || !strings.Contains(b, `value="https://hub.example.com"`) {
		t.Fatal("join error page", b)
	}
	sp.joinErr = nil
	if status, _ = do("POST", "/connections/hub/join", url.Values{"url": {"https://hub.example.com"}, "handle": {"milo"}, "code": {code}}); status != 303 {
		t.Fatal("join", status)
	}
	_, b = do("GET", "/connections", nil)
	if !strings.Contains(b, "https://hub.example.com") || !strings.Contains(b, "Connected") || !strings.Contains(b, "Leave hub") || strings.Contains(b, code) {
		t.Fatal("status card")
	}
	if status, _ = do("POST", "/connections/hub/leave", nil); status == 303 || sp.left {
		t.Fatal("leave without confirmation")
	}
	if status, _ = do("POST", "/connections/hub/leave", url.Values{"confirm": {"leave"}}); status != 303 || !sp.left {
		t.Fatal("leave", status)
	}
}

type hubErr struct{ s string }

func (e *hubErr) Error() string { return e.s }
