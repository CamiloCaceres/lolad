package fake

import (
	"fmt"
	"slices"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// check is a compact re-statement of SPEC.md's policy order, good enough for
// the dashboard to show a realistic matrix. The real thing is internal/policy.
func check(org core.Org, from, to string) core.Decision {
	allow := func(r string, a ...any) core.Decision {
		return core.Decision{Allow: true, Reason: fmt.Sprintf(r, a...)}
	}
	deny := func(r string, a ...any) core.Decision {
		return core.Decision{Allow: false, Reason: fmt.Sprintf(r, a...)}
	}
	user := org.User.Name

	if from == "lolad" {
		return allow("system identity may always send")
	}
	for i, r := range org.Rules {
		hit := match(org, r.From, from) && match(org, r.To, to)
		if !hit && r.Both {
			hit = match(org, r.From, to) && match(org, r.To, from)
		}
		if hit {
			arrow := "→"
			if r.Both {
				arrow = "↔"
			}
			if r.Allow {
				return allow("rule %d (%s%s%s)", i+1, r.From, arrow, r.To)
			}
			return deny("rule %d denies (%s%s%s)", i+1, r.From, arrow, r.To)
		}
	}
	if from == user || to == user {
		key, mode, other := "user_to_agents", org.Defaults.UserToAgents, to
		if to == user {
			key, mode, other = "agents_to_user", org.Defaults.AgentsToUser, from
		}
		switch mode {
		case "none":
			return deny("default: %s none", key)
		case "heads":
			if headTeam(org, other) != nil {
				return allow("default: %s heads (%s is a head)", key, other)
			}
			return deny("default: %s heads (%s is not a head)", key, other)
		default:
			return allow("default: %s all", key)
		}
	}
	if org.Defaults.Vertical {
		if t := headTeam(org, from); t != nil {
			if slices.Contains(t.Members, to) {
				return allow("vertical: head of %s → member", t.ID)
			}
			if p := teamByID(org, t.Parent); p != nil && p.Head == to {
				return allow("vertical: head of %s → head of parent %s", t.ID, p.ID)
			}
			for _, c := range org.Teams {
				if c.Parent == t.ID && c.Head == to {
					return allow("vertical: head of %s → head of child %s", t.ID, c.ID)
				}
			}
		}
		if t := memberTeam(org, from); t != nil && t.Head == to {
			return allow("vertical: member → head of %s", t.ID)
		}
	}
	ft, tt := teamOf(org, from), teamOf(org, to)
	if ft != nil && tt != nil && ft.ID == tt.ID && ft.Open {
		return allow("team %s is open", ft.ID)
	}
	if ft == nil || tt == nil {
		if org.Defaults.Independents == "all" {
			return allow("default: independents all")
		}
		return deny("default: independents user-only")
	}
	return deny("default: no path")
}

func match(org core.Org, pattern, name string) bool {
	switch {
	case pattern == "*":
		return true
	case pattern == "user":
		return name == org.User.Name
	case len(pattern) > 5 && pattern[:5] == "team:":
		return inTeamTree(org, pattern[5:], name, 0)
	}
	return pattern == name
}

func inTeamTree(org core.Org, id, name string, depth int) bool {
	t := teamByID(org, id)
	if t == nil || depth > len(org.Teams) {
		return false
	}
	if t.Head == name || slices.Contains(t.Members, name) {
		return true
	}
	for _, c := range org.Teams {
		if c.Parent == id && inTeamTree(org, c.ID, name, depth+1) {
			return true
		}
	}
	return false
}

func teamByID(org core.Org, id string) *core.Team {
	for i := range org.Teams {
		if org.Teams[i].ID == id {
			return &org.Teams[i]
		}
	}
	return nil
}

func headTeam(org core.Org, name string) *core.Team {
	for i := range org.Teams {
		if org.Teams[i].Head == name {
			return &org.Teams[i]
		}
	}
	return nil
}

func memberTeam(org core.Org, name string) *core.Team {
	for i := range org.Teams {
		if slices.Contains(org.Teams[i].Members, name) {
			return &org.Teams[i]
		}
	}
	return nil
}

func teamOf(org core.Org, name string) *core.Team {
	if t := headTeam(org, name); t != nil {
		return t
	}
	return memberTeam(org, name)
}

// validate mirrors the checks SPEC.md lists for SaveOrg.
func validate(org core.Org) error {
	seen := map[string]bool{}
	for _, t := range org.Teams {
		if t.ID == "" {
			return fmt.Errorf("%w: team with empty id", core.ErrInvalid)
		}
		if seen[t.ID] {
			return fmt.Errorf("%w: duplicate team id %q", core.ErrInvalid, t.ID)
		}
		seen[t.ID] = true
	}
	owner := map[string]string{}
	for _, t := range org.Teams {
		if t.Parent != "" && !seen[t.Parent] {
			return fmt.Errorf("%w: team %q: parent %q does not exist", core.ErrInvalid, t.ID, t.Parent)
		}
		for p, n := t.Parent, 0; p != ""; n++ {
			if p == t.ID || n > len(org.Teams) {
				return fmt.Errorf("%w: team %q is its own ancestor", core.ErrInvalid, t.ID)
			}
			pt := teamByID(org, p)
			if pt == nil {
				return fmt.Errorf("%w: team %q: ancestor %q does not exist", core.ErrInvalid, t.ID, p)
			}
			p = pt.Parent
		}
		names := append([]string{}, t.Members...)
		if t.Head != "" {
			names = append(names, t.Head)
		}
		for _, n := range names {
			if prev, ok := owner[n]; ok && prev != t.ID {
				return fmt.Errorf("%w: %s belongs to both %q and %q", core.ErrInvalid, n, prev, t.ID)
			}
			owner[n] = t.ID
		}
	}
	for i, r := range org.Rules {
		if r.From == "" || r.To == "" {
			return fmt.Errorf("%w: rule %d needs both from and to", core.ErrInvalid, i+1)
		}
	}
	return nil
}
