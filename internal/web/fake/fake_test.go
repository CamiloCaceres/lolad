package fake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestCheck(t *testing.T) {
	s := New(Options{})
	for _, tc := range []struct {
		from, to string
		allow    bool
		reason   string
	}{
		{"lolad", "scout", true, "system"},
		{"milo", "patch", true, "user_to_agents all"},
		{"ultron", "lola", true, "rule 1"},
		{"lola", "ultron", true, "rule 1"}, // both ways
		{"scout", "forge", false, "rule 2"},
		{"patch", "quill", true, "rule 3"},
		{"lola", "forge", true, "vertical: head of hq → head of child eng"},
		{"forge", "lola", true, "vertical: head of eng → head of parent hq"},
		{"forge", "patch", true, "vertical: head of eng → member"},
		{"relay", "forge", true, "vertical: member → head of eng"},
		{"patch", "relay", true, "team eng is open"},
		{"ledger", "patch", false, "no path"},
		{"ultron", "juno", false, "independents user-only"},
	} {
		d := s.Check(context.Background(), tc.from, tc.to)
		if d.Allow != tc.allow || !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("Check(%s, %s) = %+v, want allow=%v reason ~ %q", tc.from, tc.to, d, tc.allow, tc.reason)
		}
	}
}

func TestSaveOrgValidates(t *testing.T) {
	s := New(Options{})
	ctx := context.Background()
	org, _ := s.Org(ctx)
	bad := func(mut func(*core.Org)) error {
		o, _ := s.Org(ctx)
		mut(&o)
		return s.SaveOrg(ctx, o)
	}
	for name, mut := range map[string]func(*core.Org){
		"duplicate id":   func(o *core.Org) { o.Teams = append(o.Teams, core.Team{ID: "eng"}) },
		"missing parent": func(o *core.Org) { o.Teams[1].Parent = "nowhere" },
		"cycle":          func(o *core.Org) { o.Teams[0].Parent = "eng" },
		"two teams":      func(o *core.Org) { o.Teams[2].Members = append(o.Teams[2].Members, "patch") },
		"empty rule":     func(o *core.Org) { o.Rules = append(o.Rules, core.Rule{From: "lola"}) },
	} {
		if err := bad(mut); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ := s.Org(ctx); len(got.Teams) != len(org.Teams) || got.Teams[0].Parent != "" {
		t.Error("a refused save changed the org")
	}
}

func TestUserSendRespectsPolicy(t *testing.T) {
	s := New(Options{})
	ctx := context.Background()
	org, _ := s.Org(ctx)
	org.Defaults.UserToAgents = "none"
	if err := s.SaveOrg(ctx, org); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserSend(ctx, core.Outgoing{To: "lola", Subject: "x", Body: "x"}); !errors.Is(err, core.ErrDenied) {
		t.Errorf("err = %v, want ErrDenied", err)
	}
	evs, _ := s.Events(ctx, "milo", 1)
	if len(evs) != 1 || evs[0].Kind != "policy" {
		t.Errorf("denial not recorded as a policy event: %+v", evs)
	}
}
