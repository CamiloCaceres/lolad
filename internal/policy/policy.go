// Package policy loads the organization and evaluates its ordered messaging rules.
package policy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func DefaultOrg() core.Org {
	return core.Org{Tenant: "home", User: core.User{Name: "milo", Display: "Camilo Caceres"}, Defaults: core.Defaults{UserToAgents: "all", AgentsToUser: "all", Vertical: true, Independents: "user-only"}}
}

// Policy is an immutable snapshot. Construct a new snapshot when the org changes.
type Policy struct {
	org        core.Org
	names      []string
	teams      map[string]core.Team
	membership map[string]string
}

var _ core.Policy = (*Policy)(nil)

func local(s string) string { return strings.ToLower(strings.SplitN(strings.TrimSpace(s), "@", 2)[0]) }
func New(org core.Org, known ...string) *Policy {
	p := &Policy{org: clone(org), teams: map[string]core.Team{}, membership: map[string]string{}}
	names := map[string]bool{local(org.User.Name): true}
	for _, n := range known {
		names[local(n)] = true
	}
	for _, t := range org.Teams {
		p.teams[t.ID] = t
		for _, n := range append(append([]string{}, t.Members...), t.Head) {
			if n != "" {
				p.membership[local(n)] = t.ID
				names[local(n)] = true
			}
		}
	}
	for n := range names {
		if n != "" {
			p.names = append(p.names, n)
		}
	}
	sort.Strings(p.names)
	return p
}
func (p *Policy) match(pattern, name string) bool {
	pattern = strings.ToLower(pattern)
	switch {
	case pattern == "*":
		return true
	case pattern == "user":
		return name == local(p.org.User.Name)
	case strings.HasPrefix(pattern, "team:"):
		want := strings.TrimPrefix(pattern, "team:")
		id := p.membership[name]
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			if id == want {
				return true
			}
			seen[id] = true
			id = p.teams[id].Parent
		}
		return false
	default:
		return pattern == name
	}
}
func (p *Policy) Check(from, to string) core.Decision {
	from, to = local(from), local(to)
	answer := func(a bool, r string) core.Decision { return core.Decision{Allow: a, Reason: r} }
	if from == "lolad" {
		return answer(true, "system: lolad may always send")
	}
	for i, r := range p.org.Rules {
		if (p.match(r.From, from) && p.match(r.To, to)) || (r.Both && p.match(r.To, from) && p.match(r.From, to)) {
			return answer(r.Allow, fmt.Sprintf("rule %d (%s → %s): %s", i+1, r.From, r.To, r.Note))
		}
	}
	ft, fok := p.teams[p.membership[from]]
	tt, tok := p.teams[p.membership[to]]
	head := func(n string, t core.Team, ok bool) bool { return ok && local(t.Head) == n }
	mode := func(m string, h bool) bool { return m == "all" || (m == "heads" && h) }
	if from == local(p.org.User.Name) {
		return answer(mode(p.org.Defaults.UserToAgents, head(to, tt, tok)), "default: user_to_agents "+p.org.Defaults.UserToAgents)
	}
	if to == local(p.org.User.Name) {
		return answer(mode(p.org.Defaults.AgentsToUser, head(from, ft, fok)), "default: agents_to_user "+p.org.Defaults.AgentsToUser)
	}
	if p.org.Defaults.Vertical && fok && tok {
		if ft.ID == tt.ID && from != to && (head(from, ft, true) || head(to, tt, true)) {
			return answer(true, "vertical: head ↔ member of "+ft.ID)
		}
		if head(from, ft, true) && head(to, tt, true) && (ft.Parent == tt.ID || tt.Parent == ft.ID) {
			return answer(true, "vertical: parent ↔ child heads")
		}
	}
	if fok && tok && ft.ID == tt.ID && ft.Open {
		return answer(true, "open team: "+ft.ID)
	}
	if !fok || !tok {
		return answer(p.org.Defaults.Independents == "all", "default: independents "+p.org.Defaults.Independents)
	}
	return answer(false, "default: no path")
}
func (p *Policy) Contacts(from string) []string {
	out := []string{}
	for _, n := range p.names {
		if p.Check(from, n).Allow {
			out = append(out, n)
		}
	}
	return out
}

// Validate returns unknown-agent warnings separately from structural errors.
func Validate(org core.Org, known ...string) ([]string, error) {
	if org.Tenant == "" || org.User.Name == "" {
		return nil, fmt.Errorf("tenant and user.name are required")
	}
	validMode := func(s string) bool { return s == "all" || s == "heads" || s == "none" }
	if !validMode(org.Defaults.UserToAgents) || !validMode(org.Defaults.AgentsToUser) || (org.Defaults.Independents != "all" && org.Defaults.Independents != "user-only") {
		return nil, fmt.Errorf("invalid defaults")
	}
	teams := map[string]core.Team{}
	members := map[string]string{}
	names := map[string]bool{}
	for _, n := range known {
		names[local(n)] = true
	}
	warnings := []string{}
	for _, t := range org.Teams {
		if t.ID == "" {
			return nil, fmt.Errorf("team id is required")
		}
		if _, ok := teams[t.ID]; ok {
			return nil, fmt.Errorf("duplicate team %s", t.ID)
		}
		teams[t.ID] = t
		for _, n := range append(append([]string{}, t.Members...), t.Head) {
			if n == "" {
				continue
			}
			n = local(n)
			if prev, ok := members[n]; ok {
				return nil, fmt.Errorf("%s belongs to both %s and %s", n, prev, t.ID)
			}
			members[n] = t.ID
			if !names[n] {
				warnings = append(warnings, "unknown agent: "+n)
			}
		}
	}
	for _, t := range org.Teams {
		seen := map[string]bool{}
		id := t.ID
		for id != "" {
			if seen[id] {
				return nil, fmt.Errorf("team cycle at %s", id)
			}
			seen[id] = true
			v, ok := teams[id]
			if !ok {
				return nil, fmt.Errorf("unknown parent %s", id)
			}
			id = v.Parent
		}
	}
	for _, r := range org.Rules {
		for _, pat := range []string{r.From, r.To} {
			if pat == "" {
				return nil, fmt.Errorf("empty rule pattern")
			}
			if strings.HasPrefix(pat, "team:") {
				if _, ok := teams[strings.TrimPrefix(pat, "team:")]; !ok {
					return nil, fmt.Errorf("unknown rule team %s", pat)
				}
			}
		}
	}
	sort.Strings(warnings)
	return warnings, nil
}
