package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type teamNode struct {
	core.Team
	Depth    int
	Head     *core.AgentStatus
	Members  []core.AgentStatus
	Children []*teamNode
}

type participant struct {
	Name, Display, Kind string // Kind: user | agent
	State               core.State
}

type cell struct {
	From, To    string
	Explanation string
	Self        bool
	core.Decision
}

type orgView struct {
	Connections []core.Connection

	Org          core.Org
	Roots        []*teamNode
	Teams        []core.Team // flat, tree order, for selects
	Depth        map[string]int
	TeamOf       map[string]string // agent -> team id
	Independents []core.AgentStatus
	Agents       []core.AgentStatus
	People       []participant
	Matrix       [][]cell
	Patterns     []string
	Error        string
	Notice       string
	Create       bool
	Sender       string
	Choices      []orgChoice
	RuleViews    []orgRuleView
	Test         *cell
}

func (s *server) orgData(ctx context.Context) (orgView, error) {
	var v orgView
	v.Connections, _ = s.svc.Connections(ctx)
	org, err := s.svc.Org(ctx)
	if err != nil {
		return v, err
	}
	list, err := s.list(ctx)
	if err != nil {
		return v, err
	}
	slices.SortFunc(list, func(a, b core.AgentStatus) int { return strings.Compare(a.Name, b.Name) })
	agents := agentMap(list)
	v.Org, v.Agents, v.Depth, v.TeamOf = org, list, map[string]int{}, map[string]string{}
	for _, t := range org.Teams {
		if t.Head != "" {
			v.TeamOf[t.Head] = t.ID
		}
		for _, m := range t.Members {
			v.TeamOf[m] = t.ID
		}
	}

	placed := map[string]bool{}
	var build func(parent string, depth int) []*teamNode
	build = func(parent string, depth int) []*teamNode {
		var out []*teamNode
		for _, t := range org.Teams {
			if t.Parent != parent || v.Depth[t.ID] > 0 || depth > len(org.Teams) {
				continue
			}
			n := &teamNode{Team: t, Depth: depth, Head: agents[t.Head]}
			if t.Head != "" && n.Head == nil {
				n.Head = &core.AgentStatus{Agent: core.Agent{Name: t.Head, Display: t.Head}, State: core.StateUnknown}
			}
			v.Depth[t.ID] = depth + 1
			v.Teams = append(v.Teams, t)
			placed[t.Head] = true
			for _, m := range t.Members {
				placed[m] = true
				if a := agents[m]; a != nil {
					n.Members = append(n.Members, *a)
				} else {
					n.Members = append(n.Members, core.AgentStatus{Agent: core.Agent{Name: m, Display: m}, State: core.StateUnknown})
				}
			}
			n.Children = build(t.ID, depth+1)
			out = append(out, n)
		}
		return out
	}
	v.Roots = build("", 0)
	for _, a := range list {
		if !placed[a.Name] {
			v.Independents = append(v.Independents, a)
		}
	}

	// Matrix rows and columns: the user, then agents in tree order.
	v.People = []participant{{Name: org.User.Name, Display: org.User.Display, Kind: "user"}}
	seen := map[string]bool{org.User.Name: true}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		p := participant{Name: name, Display: name, Kind: "agent", State: core.StateUnknown}
		if a := agents[name]; a != nil {
			p.Display, p.State = a.Display, a.State
		}
		v.People = append(v.People, p)
	}
	for _, t := range v.Teams {
		add(t.Head)
		for _, m := range t.Members {
			add(m)
		}
	}
	for _, a := range list {
		add(a.Name)
	}
	for _, from := range v.People {
		row := make([]cell, 0, len(v.People))
		for _, to := range v.People {
			c := cell{From: from.Name, To: to.Name, Self: from.Name == to.Name}
			if !c.Self {
				c.Decision = s.svc.Check(ctx, from.Name, to.Name)
				c.Explanation = orgExplanation(org, c.Decision)
			}
			row = append(row, c)
		}
		v.Matrix = append(v.Matrix, row)
	}

	v.Patterns = []string{"*", "user"}
	for _, t := range v.Teams {
		v.Patterns = append(v.Patterns, "team:"+t.ID)
	}
	for _, a := range list {
		v.Patterns = append(v.Patterns, a.Name)
	}
	v.Sender = org.User.Name
	v.Choices = []orgChoice{{"*", "Everyone"}, {"user", "You"}, {org.User.Name, "You (" + org.User.Name + ")"}}
	for _, t := range v.Teams {
		v.Choices = append(v.Choices, orgChoice{"team:" + t.ID, t.Name + " and its sub-teams"})
	}
	for _, a := range list {
		v.Choices = append(v.Choices, orgChoice{a.Name, a.Display})
	}
	for _, rule := range org.Rules {
		for _, value := range []string{rule.From, rule.To} {
			if !slices.ContainsFunc(v.Choices, func(c orgChoice) bool { return c.Value == value }) {
				v.Choices = append(v.Choices, orgChoice{value, value + " (not registered)"})
			}
		}
		v.RuleViews = append(v.RuleViews, orgRuleView{Rule: rule, FromLabel: orgLabel(v.Choices, rule.From), ToLabel: orgLabel(v.Choices, rule.To)})
	}
	return v, nil
}

func (s *server) orgPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.orgData(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Create = r.URL.Query().Get("create") == "1"
	for _, p := range v.People {
		if p.Name == r.URL.Query().Get("sender") {
			v.Sender = p.Name
		}
	}
	s.page(w, r, http.StatusOK, "org", layout{Title: "Org", Data: v})
}

// mutate loads the org, applies fn, saves it and re-renders the org body.
// A validation error re-renders with the message and the org unchanged.
func (s *server) mutate(w http.ResponseWriter, r *http.Request, notice string, fn func(*core.Org) error) {
	ctx := r.Context()
	org, err := s.svc.Org(ctx)
	if err == nil {
		if err = fn(&org); err == nil {
			err = s.svc.SaveOrg(ctx, org)
		}
	}
	if err != nil && statusFor(err) == http.StatusInternalServerError {
		s.fail(w, r, err)
		return
	}
	if !isHX(r) {
		if err != nil {
			http.Error(w, err.Error(), statusFor(err))
			return
		}
		http.Redirect(w, r, "/org", http.StatusSeeOther)
		return
	}
	v, verr := s.orgData(ctx)
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	code := http.StatusOK
	if err != nil {
		v.Error = strings.TrimPrefix(err.Error(), core.ErrInvalid.Error()+": ")
		code = http.StatusUnprocessableEntity
	} else {
		v.Notice = notice
	}
	s.fragment(w, code, "org-body", v)
}

func teamIndex(org *core.Org, id string) int {
	return slices.IndexFunc(org.Teams, func(t core.Team) bool { return t.ID == id })
}

// claim removes names from every team except keep, so that saving a team
// moves its new head and members instead of failing validation.
func claim(org *core.Org, keep string, names ...string) {
	for i := range org.Teams {
		if org.Teams[i].ID == keep {
			continue
		}
		t := &org.Teams[i]
		t.Members = slices.DeleteFunc(t.Members, func(m string) bool { return slices.Contains(names, m) })
		if slices.Contains(names, t.Head) {
			t.Head = ""
		}
	}
}

func teamFromForm(r *http.Request, t *core.Team) {
	t.Name = strings.TrimSpace(r.FormValue("name"))
	t.Parent = r.FormValue("parent")
	t.Head = r.FormValue("head")
	t.Open = r.FormValue("open") == "on"
	t.Instructions = strings.TrimSpace(r.FormValue("instructions"))
	t.Members = nil
	for _, m := range r.Form["members"] {
		if m != t.Head && !slices.Contains(t.Members, m) {
			t.Members = append(t.Members, m)
		}
	}
}

func (s *server) orgTeamAdd(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := slugify(r.FormValue("id"))
	if id == "" {
		id = slugify(r.FormValue("name"))
	}
	s.mutate(w, r, "Team "+id+" added.", func(org *core.Org) error {
		if id == "" {
			return fmt.Errorf("%w: a team needs a name", core.ErrInvalid)
		}
		if teamIndex(org, id) >= 0 {
			return fmt.Errorf("%w: team id %q is taken", core.ErrInvalid, id)
		}
		t := core.Team{ID: id}
		teamFromForm(r, &t)
		if t.Name == "" {
			t.Name = id
		}
		claim(org, id, append(slices.Clone(t.Members), t.Head)...)
		org.Teams = append(org.Teams, t)
		return nil
	})
}

func (s *server) orgTeamSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.PathValue("id")
	s.mutate(w, r, "Team "+id+" saved.", func(org *core.Org) error {
		i := teamIndex(org, id)
		if i < 0 {
			return fmt.Errorf("team %q: %w", id, core.ErrNotFound)
		}
		t := org.Teams[i]
		teamFromForm(r, &t)
		if t.Name == "" {
			t.Name = id
		}
		claim(org, id, append(slices.Clone(t.Members), t.Head)...)
		org.Teams[i] = t
		return nil
	})
}

func (s *server) orgTeamDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mutate(w, r, "Team "+id+" removed; its sub-teams moved up a level.", func(org *core.Org) error {
		i := teamIndex(org, id)
		if i < 0 {
			return fmt.Errorf("team %q: %w", id, core.ErrNotFound)
		}
		parent := org.Teams[i].Parent
		for j := range org.Teams {
			if org.Teams[j].Parent == id {
				org.Teams[j].Parent = parent
			}
		}
		org.Teams = slices.Delete(org.Teams, i, i+1)
		return nil
	})
}

func (s *server) orgRuleAdd(w http.ResponseWriter, r *http.Request) {
	rule := core.Rule{
		From: strings.TrimSpace(r.FormValue("from")), To: strings.TrimSpace(r.FormValue("to")),
		Allow: r.FormValue("allow") != "deny", Both: r.FormValue("both") == "on", Note: strings.TrimSpace(r.FormValue("note")),
	}
	s.mutate(w, r, "Rule added.", func(org *core.Org) error {
		if rule.From == "" || rule.To == "" {
			return fmt.Errorf("%w: a rule needs both from and to", core.ErrInvalid)
		}
		if r.FormValue("mode") == "protect" {
			if rule.To == "*" || rule.To == "user" || strings.HasPrefix(rule.To, "team:") || rule.To == org.User.Name {
				return fmt.Errorf("%w: choose one agent to protect", core.ErrInvalid)
			}
			org.Rules = append([]core.Rule{{From: "user", To: rule.To, Allow: true, Note: "Only you may message this agent"}, {From: "*", To: rule.To, Allow: false, Note: "Protected from other senders"}}, org.Rules...)
		} else if r.FormValue("priority") == "first" {
			org.Rules = append([]core.Rule{rule}, org.Rules...)
		} else {
			org.Rules = append(org.Rules, rule)
		}
		return nil
	})
}

func (s *server) orgRuleOp(w http.ResponseWriter, r *http.Request) {
	i, err := strconv.Atoi(r.PathValue("i"))
	op := r.PathValue("op")
	s.mutate(w, r, "Rules updated.", func(org *core.Org) error {
		if err != nil || i < 0 || i >= len(org.Rules) {
			return fmt.Errorf("%w: no rule %s", core.ErrInvalid, r.PathValue("i"))
		}
		switch op {
		case "save":
			from, to := strings.TrimSpace(r.FormValue("from")), strings.TrimSpace(r.FormValue("to"))
			if from == "" || to == "" {
				return fmt.Errorf("%w: choose a sender and recipient", core.ErrInvalid)
			}
			org.Rules[i] = core.Rule{From: from, To: to, Allow: r.FormValue("allow") != "deny", Both: r.FormValue("both") == "on", Note: strings.TrimSpace(r.FormValue("note"))}
		case "delete":
			org.Rules = slices.Delete(org.Rules, i, i+1)
		case "up":
			if i > 0 {
				org.Rules[i-1], org.Rules[i] = org.Rules[i], org.Rules[i-1]
			}
		case "down":
			if i < len(org.Rules)-1 {
				org.Rules[i+1], org.Rules[i] = org.Rules[i], org.Rules[i+1]
			}
		default:
			return fmt.Errorf("%w: unknown operation %q", core.ErrInvalid, op)
		}
		return nil
	})
}

func (s *server) orgDefaults(w http.ResponseWriter, r *http.Request) {
	s.mutate(w, r, "Defaults saved.", func(org *core.Org) error {
		d := core.Defaults{
			UserToAgents: r.FormValue("user_to_agents"), AgentsToUser: r.FormValue("agents_to_user"),
			Vertical: r.FormValue("vertical") == "on", Independents: r.FormValue("independents"),
		}
		for _, f := range []struct{ name, v string }{{"user_to_agents", d.UserToAgents}, {"agents_to_user", d.AgentsToUser}} {
			if !slices.Contains([]string{"all", "heads", "none"}, f.v) {
				return fmt.Errorf("%w: %s must be all, heads or none", core.ErrInvalid, f.name)
			}
		}
		if !slices.Contains([]string{"user-only", "all"}, d.Independents) {
			return fmt.Errorf("%w: independents must be user-only or all", core.ErrInvalid)
		}
		org.Defaults = d
		return nil
	})
}

func (s *server) orgCheck(w http.ResponseWriter, r *http.Request) {
	from, to := strings.TrimSpace(r.FormValue("from")), strings.TrimSpace(r.FormValue("to"))
	c := &cell{From: from, To: to, Self: from == to}
	if from == "" || to == "" {
		s.fail(w, r, fmt.Errorf("%w: pick both a sender and a recipient", core.ErrInvalid))
		return
	}
	if !c.Self {
		c.Decision = s.svc.Check(r.Context(), from, to)
		org, err := s.svc.Org(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		c.Explanation = orgExplanation(org, c.Decision)
	}
	if !isHX(r) {
		v, err := s.orgData(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Test = c
		s.page(w, r, http.StatusOK, "org", layout{Title: "Org", Data: v})
		return
	}
	s.fragment(w, http.StatusOK, "org-test-result", c)
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Sentence labels keep policy patterns out of the editor's working vocabulary.
type orgChoice struct{ Value, Label string }
type orgRuleView struct {
	core.Rule
	FromLabel, ToLabel string
}

func orgLabel(choices []orgChoice, value string) string {
	for _, c := range choices {
		if c.Value == value {
			return c.Label
		}
	}
	return value
}
func orgExplanation(org core.Org, d core.Decision) string {
	var n int
	if _, err := fmt.Sscanf(d.Reason, "rule %d", &n); err == nil && n > 0 && n <= len(org.Rules) {
		r := org.Rules[n-1]
		if r.Note != "" {
			return fmt.Sprintf("Exception %d: %s", n, r.Note)
		}
		return fmt.Sprintf("Exception %d is the first matching rule.", n)
	}
	switch {
	case strings.HasPrefix(d.Reason, "system"):
		return "The system may always send."
	case strings.Contains(d.Reason, "user_to_agents"):
		if d.Allow {
			return "Your access to agents allows this."
		}
		return "Your access to agents excludes this recipient."
	case strings.Contains(d.Reason, "agents_to_user"):
		if d.Allow {
			return "Agent access to you allows this."
		}
		return "Agent access to you excludes this sender."
	case strings.HasPrefix(d.Reason, "vertical"):
		return "Their head–member or direct reporting line allows this."
	case strings.Contains(d.Reason, "open"):
		return "Members of this open team may talk directly."
	case strings.Contains(d.Reason, "independents"):
		if d.Allow {
			return "Independent agents may talk with everyone."
		}
		return "An agent is independent; agent access needs an exception."
	default:
		return "No direct reporting line, open team or exception connects them."
	}
}
