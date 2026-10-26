package web_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestAgentsRosterFiltering(t *testing.T) {
	e := setup(t)
	for _, tc := range []struct {
		path         string
		want, absent []string
	}{
		{"/agents", []string{"Find an agent", "Engineering", "Pending restart", "Next trigger", "crash restarts / 24h"}, nil},
		{"/fragments/fleet?q=LEDGER", []string{"Ledger", `id="fleet"`}, []string{"Forge", "Lola", "No agents yet"}},
		{"/agents?q=engineering", []string{"Forge", "Relay", "Patch"}, []string{"Ledger", "Ultron"}},
		{"/agents?status=attention", []string{"Ledger", "Relay", "Quill", "Lola"}, []string{"Iris", "Forge", "Scout"}},
		{"/fragments/fleet?q=nobody-matches", []string{"No matching agents"}, []string{"No agents yet", "Create your first"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := e.do("GET", tc.path, nil, false)
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			for _, text := range tc.want {
				if !strings.Contains(body, text) {
					t.Errorf("missing %q", text)
				}
			}
			for _, text := range tc.absent {
				if strings.Contains(body, text) {
					t.Errorf("unexpected %q", text)
				}
			}
		})
	}
}

func TestAgentsFilteredControlResumesAndFreshWakes(t *testing.T) {
	e := setup(t)
	before, _ := e.svc.Get(context.Background(), "lola")
	resp, body := e.do("POST", "/agents/lola/restart", url.Values{"from": {"fleet"}, "q": {"lola"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, `id="fleet"`) || strings.Contains(body, "Forge") {
		t.Fatalf("restart lost filtered roster: %d %s", resp.StatusCode, body)
	}
	resumed, _ := e.svc.Get(context.Background(), "lola")
	if !before.SessionSince.Equal(resumed.SessionSince) || resumed.PendingRestart {
		t.Fatalf("restart must resume conversation and apply pending settings: %+v", resumed)
	}
	resp, _ = e.do("POST", "/agents/scout/fresh", url.Values{"from": {"detail"}}, true)
	fresh, _ := e.svc.Get(context.Background(), "scout")
	if resp.StatusCode != 200 || fresh.State != core.StateIdle || time.Since(fresh.SessionSince) > time.Minute {
		t.Fatalf("fresh wake did not start stopped agent: status %d state %s", resp.StatusCode, fresh.State)
	}
	resp, _ = e.do("POST", "/agents/scout/stop", url.Values{"from": {"fleet"}, "q": {"scout"}, "status": {"attention"}}, false)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/agents?q=scout&status=attention" {
		t.Errorf("plain control redirect = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestAgentDetailJobsAndConsequences(t *testing.T) {
	e := setup(t)
	_, body := e.do("GET", "/agents/lola", nil, false)
	for _, want := range []string{"Open conversation", "Recent activity", "Mail sent to milo", "Mail received from forge", "Trigger fired", "Configuration", "Skills mode", "/settings/skills", "/settings/email", "/settings/files", "Read-only", "Restart · resume", "Wake · fresh conversation", "New mail can wake it again", "Next scheduled trigger"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A mail lifecycle event and the corresponding traffic item appear once.
	if strings.Count(body, "Pricing copy review</p>") != 2 {
		t.Errorf("expected separate sent and received mail, without duplicate raw mail events")
	}
	_, body = e.do("GET", "/agents/scout", nil, false)
	if !strings.Contains(body, "Start when needed") || strings.Contains(body, "New mail can wake it again") {
		t.Error("stopped agent must describe its disabled wake-on-mail setting")
	}
	if !strings.Contains(body, "Wake · fresh conversation") {
		t.Error("fresh wake must be available to stopped agents")
	}
}
