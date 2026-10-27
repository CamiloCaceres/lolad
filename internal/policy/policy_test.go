package policy

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func company() core.Org {
	o := DefaultOrg()
	o.User = core.User{Name: "milo", Display: "Milo"} // the default user follows the OS account
	o.Teams = []core.Team{{ID: "company", Head: "chief", Members: []string{"assistant"}}, {ID: "engineering", Parent: "company", Head: "cto", Members: []string{"dev", "qa"}, Open: true}, {ID: "sales", Parent: "company", Head: "sales-head", Members: []string{"rep"}}}
	o.Rules = []core.Rule{{From: "chief", To: "cto", Allow: false, Note: "explicit deny beats vertical"}, {From: "dev", To: "rep", Allow: true, Both: true}, {From: "team:company", To: "indie", Allow: true}}
	return o
}
func TestPolicyCompanyMatrix(t *testing.T) {
	o := company()
	names := []string{"milo", "chief", "assistant", "cto", "dev", "qa", "sales-head", "rep", "indie"}
	p := New(o, names...)
	// Every pair is specified: users reach all, hierarchy is one step, only the
	// engineering team is open, and explicit rules precede every default.
	allowed := map[string]bool{}
	edge := func(a, b string) { allowed[a+"/"+b] = true }
	for _, n := range names {
		edge("milo", n)
		edge(n, "milo")
	}
	for _, pair := range [][2]string{{"chief", "assistant"}, {"assistant", "chief"}, {"cto", "chief"}, {"chief", "sales-head"}, {"sales-head", "chief"}, {"sales-head", "rep"}, {"rep", "sales-head"}, {"dev", "rep"}, {"rep", "dev"}} {
		edge(pair[0], pair[1])
	}
	for _, a := range []string{"cto", "dev", "qa"} {
		for _, b := range []string{"cto", "dev", "qa"} {
			edge(a, b)
		}
	}
	for _, a := range []string{"chief", "assistant", "cto", "dev", "qa", "sales-head", "rep"} {
		edge(a, "indie")
	}
	for _, a := range names {
		for _, b := range names {
			d := p.Check(a, b)
			if d.Allow != allowed[a+"/"+b] {
				t.Errorf("%s → %s = %+v", a, b, d)
			}
			if d.Reason == "" {
				t.Errorf("missing reason %s/%s", a, b)
			}
		}
	}
	if !p.Check("lolad", "nobody").Allow {
		t.Fatal("system denied")
	}
	if !strings.Contains(p.Check("chief", "cto").Reason, "rule 1") {
		t.Fatal("rule ordering lost")
	}
	if !p.Check("DEV@HOME.LOLAD.LOCAL", "INDIE").Allow {
		t.Fatal("case/address normalization or subtree rule")
	}
	want := []string{"cto", "dev", "indie", "milo", "qa", "rep"}
	if got := p.Contacts("dev"); !reflect.DeepEqual(got, want) {
		t.Fatalf("contacts %v", got)
	}
	o.Rules = nil
	o.Defaults.UserToAgents = "heads"
	o.Defaults.AgentsToUser = "none"
	p = New(o, names...)
	if p.Check("milo", "dev").Allow || !p.Check("milo", "cto").Allow || p.Check("cto", "milo").Allow {
		t.Fatal("user modes")
	}
	o.Defaults.Vertical = false
	o.Defaults.Independents = "all"
	p = New(o, names...)
	if p.Check("chief", "cto").Allow || !p.Check("indie", "dev").Allow {
		t.Fatal("vertical/independents defaults")
	}
}
func TestValidation(t *testing.T) {
	o := company()
	w, e := Validate(o, "chief")
	if e != nil || len(w) == 0 {
		t.Fatalf("warnings %v %v", w, e)
	}
	for _, mutate := range []func(*core.Org){func(o *core.Org) { o.Teams = append(o.Teams, o.Teams[0]) }, func(o *core.Org) { o.Teams[0].Parent = "sales" }, func(o *core.Org) { o.Teams[0].Parent = "missing" }, func(o *core.Org) { o.Teams[1].Members = append(o.Teams[1].Members, "chief") }, func(o *core.Org) { o.Defaults.Independents = "unknown" }, func(o *core.Org) { o.Rules[0].To = "team:missing" }} {
		bad := clone(o)
		mutate(&bad)
		if _, e = Validate(bad); e == nil {
			t.Fatal("invalid org accepted", bad)
		}
	}
}
func TestStoreAtomicMutationSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "org.yaml")
	s, e := Open(path, "a", "b", "c")
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(s.Org(), DefaultOrg()) {
		t.Fatal("missing-file defaults")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := s.Subscribe(ctx)
	for _, e := range []error{s.AddTeam(core.Team{ID: "root", Head: "a"}), s.AddTeam(core.Team{ID: "child", Parent: "root"}), s.AddMember("root", "b"), s.AddMember("child", "b"), s.SetHead("child", "c"), s.AddRule(core.Rule{From: "a", To: "b", Allow: false}), s.AddRule(core.Rule{From: "*", To: "*", Allow: true}), s.MoveRule(1, 0)} {
		if e != nil {
			t.Fatal(e)
		}
	}
	if !s.Check("a", "b").Allow {
		t.Fatal("moved rule not first")
	}
	if s.Org().Teams[0].Members != nil && len(s.Org().Teams[0].Members) != 0 {
		t.Fatal("member not moved")
	}
	snapshot := s.Org()
	snapshot.Teams[0].Head = "bad"
	if s.Org().Teams[0].Head != "a" {
		t.Fatal("snapshot aliases store")
	}
	if e = s.RemoveRule(0); e != nil {
		t.Fatal(e)
	}
	if s.Check("a", "b").Allow {
		t.Fatal("live policy stale")
	}
	if e = s.RemoveMember("child", "b"); e != nil {
		t.Fatal(e)
	}
	if e = s.SetHead("child", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveTeam("root"); e == nil {
		t.Fatal("removed referenced parent")
	}
	if e = s.RemoveTeam("child"); e != nil {
		t.Fatal(e)
	}
	if e = s.Reload(); e != nil {
		t.Fatal(e)
	}
	select {
	case o := <-ch:
		if len(o.Teams) != 1 {
			t.Fatal("latest update missing")
		}
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				s.Check("a", "b")
				_ = s.Org()
				_ = s.Save(s.Org())
			}
		})
	}
	wg.Wait()
	loaded, e := Load(path)
	if e != nil || !reflect.DeepEqual(loaded, s.Org()) {
		t.Fatalf("round trip %v", e)
	}
}
