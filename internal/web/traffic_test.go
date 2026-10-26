package web_test

import (
	"context"
	"fmt"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTrafficAuditViews(t *testing.T) {
	e := setup(t)
	for _, tc := range []struct {
		path         string
		want, absent []string
	}{
		{"/traffic?status=denied", []string{"Why it was denied", "rule 2 denies", "/org#org-rule-2", "Open sender", "Open recipient"}, []string{"Re: Verification received", "audit_seed_"}},
		{"/traffic?status=queued", []string{"Waiting for recipient notification", "3 attempts", "Reconcile ops invoices"}, []string{"Why it was denied", "Signature</dt><dd>Unsigned"}},
		{"/traffic?peer=quill&direction=in", []string{"Tighten pricing page copy", "Launch copy review"}, []string{"Copy v2 is in drafts/pricing-v2.md", "Why it was denied"}},
		{"/traffic?peer=quill&direction=out", []string{"Copy v2 is in drafts/pricing-v2.md"}, []string{"Tighten the hero and the plan blurbs"}},
		{"/traffic?period=1h", []string{"Re: Verification received"}, []string{"Flight moved", "Fix Ledger mail trigger path"}},
		{"/traffic?view=pairs", []string{"Pairs ranked by volume", "Focus on this pair", "forge ↔ patch"}, nil},
		{"/traffic?peer=forge&other=patch", []string{"Conversation:", "Re: Verification received"}, []string{"Ship pricing today?", "Launch copy review"}},
		{"/traffic?peer=quill&status=denied", []string{"No traffic matches these filters", "Clear filters"}, []string{"Why it was denied"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			code, body := e.get(tc.path)
			if code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			for _, s := range tc.want {
				if !strings.Contains(body, s) {
					t.Errorf("missing %q", s)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(body, s) {
					t.Errorf("unexpected %q", s)
				}
			}
		})
	}
}

func TestTrafficPagingAndLivePreserveFilters(t *testing.T) {
	e := setup(t)
	_, body := e.get("/traffic?period=7d&view=pairs")
	start := strings.Index(body, `href="/traffic?before=`)
	if start < 0 {
		t.Fatal("older cursor missing")
	}
	tail := body[start+6:]
	older := html.UnescapeString(strings.SplitN(tail, `"`, 2)[0])
	u, err := url.Parse(older)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("period") != "7d" || u.Query().Get("view") != "pairs" || u.Query().Get("before") == "" {
		t.Fatalf("lost filters: %s", older)
	}
	code, next := e.get(older)
	if code != 200 || !strings.Contains(next, ">Newest</a>") {
		t.Fatal("older page failed")
	}
	resp, fragment := e.do("GET", "/traffic?peer=forge&direction=out&period=7d&view=pairs&part=rows", nil, true)
	if resp.StatusCode != 200 || strings.Contains(fragment, "<!DOCTYPE") || !strings.Contains(fragment, "direction=out") || !strings.Contains(fragment, "period=7d") || !strings.Contains(fragment, "lolad:agent") {
		t.Fatal("live fragment lost filters or event trigger")
	}
}

// A registry-independent fixture exercises fleet size and outside participants
// without changing the shared fake's agents or human conversations.
type trafficFixture struct {
	core.Service
	agents []core.AgentStatus
	mail   []core.Mail
}

func (f trafficFixture) List(context.Context) ([]core.AgentStatus, error) { return f.agents, nil }
func (f trafficFixture) Traffic(context.Context, int, time.Time) ([]core.Mail, error) {
	return f.mail, nil
}
func (f trafficFixture) Events(context.Context, string, int) ([]core.Event, error) { return nil, nil }
func TestTrafficSmallAndLargeFleetWithOutsideParticipant(t *testing.T) {
	for _, size := range []int{2, 25} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			e := setup(t)
			fixture := trafficFixture{Service: e.svc.Service}
			for i := 0; i < size; i++ {
				name := fmt.Sprintf("agent-%02d", i)
				fixture.agents = append(fixture.agents, core.AgentStatus{Agent: core.Agent{Name: name, Display: name}})
			}
			fixture.mail = []core.Mail{{ID: "external-audit", FromName: "agent-00", ToName: "partner@outside.example", From: "agent-00@local", To: "partner@outside.example", Subject: "Deployment handoff", Body: "The verification report is ready.", CreatedAt: time.Now(), Status: core.MailNotified}}
			e.svc.Service = fixture
			code, body := e.get("/traffic?peer=partner%40outside.example")
			for _, want := range []string{"Deployment handoff", `value="partner@outside.example" selected`, fmt.Sprintf(`value="agent-%02d"`, size-1), `href="/agents/agent-00"`} {
				if code != 200 || !strings.Contains(body, want) {
					t.Errorf("fleet %d missing %s", size, want)
				}
			}
		})
	}
}
