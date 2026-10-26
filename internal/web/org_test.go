package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

func TestOrgPairExceptionTakesPriority(t *testing.T) {
	e := setup(t)
	// Scout is already denied access to Engineering by a broad rule.
	resp, body := e.do("POST", "/org/check", url.Values{"from": {"scout"}, "to": {"forge"}}, true)
	if resp.StatusCode != 200 || !strings.Contains(body, "Save exception") || !strings.Contains(body, "Both ways") || !strings.Contains(body, "research goes through Juno") {
		t.Fatalf("connection editor: %d %s", resp.StatusCode, body)
	}
	resp, _ = e.do("POST", "/org/rules", url.Values{"from": {"scout"}, "to": {"forge"}, "allow": {"allow"}, "both": {"on"}, "priority": {"first"}}, true)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	org, _ := e.svc.Org(context.Background())
	if org.Rules[0].From != "scout" || !org.Rules[0].Both {
		t.Fatalf("override not first: %+v", org.Rules)
	}
	// Verify real policy semantics independently of the fake evaluator.
	p := policy.New(org)
	if !p.Check("scout", "forge").Allow || !p.Check("forge", "scout").Allow || p.Check("scout", "patch").Allow {
		t.Fatal("override should permit only the selected pair, in both directions")
	}
	resp, _ = e.do("POST", "/org/rules/0/save", url.Values{"from": {"scout"}, "to": {"forge"}, "allow": {"deny"}, "note": {"Hold coordination"}}, true)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	org, _ = e.svc.Org(context.Background())
	if org.Rules[0].Allow || org.Rules[0].Both || org.Rules[0].Note != "Hold coordination" {
		t.Fatalf("edit: %+v", org.Rules[0])
	}
}

func TestOrgProtectAgentOverridesEarlierRules(t *testing.T) {
	e := setup(t)
	resp, _ := e.do("POST", "/org/rules", url.Values{"mode": {"protect"}, "from": {"*"}, "to": {"ultron"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode)
	}
	org, _ := e.svc.Org(context.Background())
	if len(org.Rules) < 2 || org.Rules[0] != (core.Rule{From: "user", To: "ultron", Allow: true, Note: "Only you may message this agent"}) || org.Rules[1].Allow || org.Rules[1].From != "*" {
		t.Fatalf("protection order: %+v", org.Rules)
	}
	p := policy.New(org)
	for _, from := range []string{"lola", "iris", "forge"} {
		if p.Check(from, "ultron").Allow {
			t.Errorf("%s can reach protected agent", from)
		}
	}
	if !p.Check("milo", "ultron").Allow || !p.Check("lolad", "ultron").Allow || !p.Check("ultron", "lola").Allow {
		t.Fatal("protection changed operator, system or outgoing access")
	}
	before := len(org.Rules)
	resp, _ = e.do("POST", "/org/rules", url.Values{"mode": {"protect"}, "from": {"*"}, "to": {"*"}}, true)
	after, _ := e.svc.Org(context.Background())
	if resp.StatusCode != http.StatusUnprocessableEntity || len(after.Rules) != before {
		t.Fatal("invalid protection changed rules")
	}
}

func TestOrgSenderViewAndCreateTeam(t *testing.T) {
	e := setup(t)
	code, page := e.get("/org?sender=iris&create=1")
	for _, want := range []string{`value="iris" selected`, `id="org-add-team" class="org-disclosure" open`, "Compare all senders", "Only I may message an agent", "Engineering and its sub-teams", `id="org-pair"`, `id="org-rule-1"`, `id="org-defaults"`} {
		if code != 200 || !strings.Contains(page, want) {
			t.Errorf("page missing %s (status %d)", want, code)
		}
	}
	if strings.Contains(page, `placeholder="lola, user, team:eng, *"`) || strings.Contains(page, "Test a message") {
		t.Error("raw pattern form or separate message checker remains")
	}
}
