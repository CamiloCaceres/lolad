package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/robfig/cron/v3"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type fleetView struct {
	Agents                []core.AgentStatus
	Counts                []stateCount
	Teams                 map[string]string // id -> name
	Groups                []agentGroup
	Total, NeedsAttention int
	Query, Filter         string
}

// Fleet rows keep status and the next clock-based trigger together.
type fleetAgent struct {
	core.AgentStatus
	NextTrigger time.Time
}
type agentGroup struct {
	Name   string
	Agents []fleetAgent
}

func agentNeedsAttention(a core.AgentStatus) bool {
	return attention(a.State) || a.State == core.StateUnknown || a.PendingRestart
}

// nextAgentTrigger is a schedule preview, not a promise of delivery. Mail polls
// have no last-poll timestamp in Service, so only cron has a knowable next run.
func nextAgentTrigger(rt core.Runtime, now time.Time) time.Time {
	var next time.Time
	for _, trigger := range rt.Triggers {
		if trigger.Cron == "" {
			continue
		}
		schedule, err := cron.ParseStandard(trigger.Cron)
		if err != nil {
			continue
		}
		at := schedule.Next(now)
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return next
}

func (v *fleetView) filter(q, filter string) {
	v.Query, v.Filter, v.Total = strings.TrimSpace(q), filter, len(v.Agents)
	groups := map[string][]fleetAgent{}
	for _, a := range v.Agents {
		if agentNeedsAttention(a) {
			v.NeedsAttention++
		}
		if filter == "attention" && !agentNeedsAttention(a) {
			continue
		}
		text := strings.ToLower(strings.Join([]string{a.Name, a.Display, a.Role, a.Runtime.Model, v.Teams[a.Team]}, " "))
		if !strings.Contains(text, strings.ToLower(v.Query)) {
			continue
		}
		group := cmp.Or(v.Teams[a.Team], a.Team, "Independent")
		if len(v.Teams) == 0 {
			group = "All agents"
		}
		groups[group] = append(groups[group], fleetAgent{a, nextAgentTrigger(a.Runtime, time.Now())})
	}
	for _, name := range slices.Sorted(maps.Keys(groups)) {
		agents := groups[name]
		slices.SortStableFunc(agents, func(a, b fleetAgent) int {
			aa, ba := agentNeedsAttention(a.AgentStatus), agentNeedsAttention(b.AgentStatus)
			if aa != ba {
				if aa {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		v.Groups = append(v.Groups, agentGroup{name, agents})
	}
}

type stateCount struct {
	State core.State
	N     int
}

// stateOrder is how states are listed in summaries: what needs the operator first.
var stateOrder = []core.State{core.StateFailed, core.StateCrashed, core.StateWaiting, core.StateBusy, core.StateStarting, core.StateIdle, core.StateUnknown, core.StateStopped}

func (s *server) fleetData(ctx context.Context) (fleetView, error) {
	var v fleetView
	list, err := s.svc.List(ctx)
	if err != nil {
		return v, err
	}
	slices.SortStableFunc(list, func(a, b core.AgentStatus) int { return strings.Compare(a.Name, b.Name) })
	v.Agents = list
	n := map[core.State]int{}
	for _, a := range list {
		n[a.State]++
	}
	for _, st := range stateOrder {
		if n[st] > 0 {
			v.Counts = append(v.Counts, stateCount{st, n[st]})
		}
	}
	v.Teams = map[string]string{}
	if org, err := s.svc.Org(ctx); err == nil {
		for _, t := range org.Teams {
			v.Teams[t.ID] = t.Name
		}
	}
	return v, nil
}

func (s *server) fleet(w http.ResponseWriter, r *http.Request) {
	v, err := s.fleetData(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.filter(r.URL.Query().Get("q"), r.URL.Query().Get("status"))
	s.page(w, r, http.StatusOK, "agents", layout{Title: "Agents", Data: v})
}

func (s *server) fleetFragment(w http.ResponseWriter, r *http.Request) {
	v, err := s.fleetData(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.filter(r.FormValue("q"), r.FormValue("status"))
	s.fragment(w, http.StatusOK, "fleet-body", v)
}

type agentView struct {
	Shared []connectionView

	A                core.AgentStatus
	TeamName         string
	Events           []core.Event
	Mail             []core.Mail
	Me               string
	EnvKeys          []string
	Timeline         []activityItem
	NextTrigger      time.Time
	Emails           []string
	EmailUnavailable bool
}

type activityItem struct {
	At                  time.Time
	Kind, Title, Detail string
}

func (s *server) agentData(ctx context.Context, name string) (agentView, error) {
	a, err := s.svc.Get(ctx, name)
	if err != nil {
		return agentView{}, err
	}
	v := agentView{A: a, Me: s.svc.Me().Name, EnvKeys: slices.Sorted(maps.Keys(a.Runtime.Env)), NextTrigger: nextAgentTrigger(a.Runtime, time.Now())}
	connections, _ := s.svc.Connections(ctx)
	grants, _ := s.svc.Grants(ctx)
	for _, c := range connections {
		cv := connectionView{Connection: c}
		cv.Shares = nil
		for _, sh := range c.Shares {
			if sh.Local == name {
				cv.Shares = append(cv.Shares, sh)
			}
		}
		for _, g := range grants {
			if g.Peer == c.Provider && g.Local == name && g.State == core.GrantAllowed {
				cv.Grants = append(cv.Grants, g)
			}
		}
		if len(cv.Shares) > 0 || len(cv.Grants) > 0 {
			v.Shared = append(v.Shared, cv)
		}
	}
	if email, err := s.svc.Email(ctx, name); err == nil {
		for _, account := range email.Accounts {
			v.Emails = append(v.Emails, account.Address)
		}
	} else {
		v.EmailUnavailable = true
	}
	if org, err := s.svc.Org(ctx); err == nil {
		for _, t := range org.Teams {
			if t.ID == a.Team {
				v.TeamName = t.Name
			}
		}
	}
	if v.Events, err = s.svc.Events(ctx, name, 80); err != nil {
		return v, err
	}
	slices.SortStableFunc(v.Events, func(a, b core.Event) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	traffic, err := s.svc.Traffic(ctx, 400, time.Time{})
	if err != nil {
		return v, err
	}
	// The daemon's mail events carry only the message id; say what it was.
	byID := make(map[string]core.Mail, len(traffic))
	for _, m := range traffic {
		byID[m.ID] = m
	}
	for i, e := range v.Events {
		if m, ok := byID[e.Detail]; ok && e.Kind == "mail" {
			v.Events[i].Detail = fmt.Sprintf("%s → %s: %s", m.FromName, m.ToName, cmp.Or(subject(m), excerpt(m.Body, 80)))
		}
	}
	for _, m := range traffic {
		if m.FromName == name || m.ToName == name {
			v.Mail = append(v.Mail, m)
			if len(v.Mail) == 40 {
				break
			}
		}
	}
	// Mail becomes a first-class timeline entry in either direction. Suppress its
	// corresponding lifecycle event to avoid showing the same delivery twice.
	mailDetails := map[string]bool{}
	for _, m := range v.Mail {
		detail := fmt.Sprintf("%s → %s: %s", m.FromName, m.ToName, cmp.Or(subject(m), excerpt(m.Body, 80)))
		mailDetails[detail] = true
		mailDetails[fmt.Sprintf("%s → %s: %s", m.FromName, m.ToName, m.Subject)] = true
		title := "Mail received from " + m.FromName
		if m.Status == core.MailHeld {
			title = "Message awaiting approval from " + m.FromName
		}
		if m.FromName == name {
			title = "Mail sent to " + m.ToName
		}
		v.Timeline = append(v.Timeline, activityItem{m.CreatedAt, "mail", title, cmp.Or(subject(m), excerpt(m.Body, 120))})
	}
	for _, e := range v.Events {
		if e.Kind == "mail" && mailDetails[e.Detail] {
			continue
		}
		title := map[string]string{"state": "Activity changed", "started": "Agent started", "crashed": "Agent crashed", "exited": "Process exited", "rotated": "Fresh conversation", "trigger": "Trigger fired", "notified": "Mail surfaced", "restarted": "Agent restarted", "error": "Error", "policy": "Messaging policy"}[e.Kind]
		if title == "" {
			title = e.Kind
		}
		v.Timeline = append(v.Timeline, activityItem{e.At, e.Kind, title, e.Detail})
	}
	slices.SortStableFunc(v.Timeline, func(a, b activityItem) int { return b.At.Compare(a.At) })
	return v, nil
}

func (s *server) agent(w http.ResponseWriter, r *http.Request) {
	v, err := s.agentData(r.Context(), r.PathValue("name"))
	if errors.Is(err, core.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "agent", layout{Title: v.A.Display, Nav: "agents", Class: "is-agent", Data: v})
}

// agentPart refreshes one live section of the agent page.
func (s *server) agentPart(w http.ResponseWriter, r *http.Request) {
	part := r.PathValue("part")
	if !slices.Contains([]string{"head", "events", "mail", "facts", "pending"}, part) {
		s.notFound(w, r)
		return
	}
	v, err := s.agentData(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if part == "pending" {
		s.fragment(w, http.StatusOK, "pending", map[string]any{"A": v.A, "From": "settings"})
		return
	}
	s.fragment(w, http.StatusOK, "agent-"+part, v)
}

// control runs a lifecycle action. From the fleet table it answers with the
// row, from the agent page with its header.
func (s *server) control(w http.ResponseWriter, r *http.Request) {
	ctx, name, action := r.Context(), r.PathValue("name"), r.PathValue("action")
	var err error
	verb := ""
	switch action {
	case "start":
		err, verb = s.svc.Start(ctx, name), "Starting"
	case "stop":
		err, verb = s.svc.Stop(ctx, name), "Stopping"
	case "restart":
		err, verb = s.svc.Restart(ctx, name, false), "Restarting"
	case "fresh":
		err, verb = s.svc.Restart(ctx, name, true), "Fresh session for"
	case "interrupt":
		err, verb = s.svc.Interrupt(ctx, name), "Interrupted"
	default:
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !isHX(r) {
		back := cmp.Or(r.FormValue("back"), "/agents/"+name)
		if r.FormValue("from") == "fleet" {
			back = "/agents?" + url.Values{"q": {r.FormValue("q")}, "status": {r.FormValue("status")}}.Encode()
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	toast(w, "ok", verb+" "+name)
	if r.FormValue("from") == "fleet" {
		v, err := s.fleetData(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.filter(r.FormValue("q"), r.FormValue("status"))
		s.fragment(w, http.StatusOK, "fleet-body", v)
		return
	}
	if r.FormValue("from") == "settings" {
		a, err := s.svc.Get(ctx, name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.fragment(w, http.StatusOK, "pending", map[string]any{"A": a, "From": "settings"})
		return
	}
	v, err := s.agentData(ctx, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, http.StatusOK, "agent-head", v)
}

// termMsg is what the browser sends over the terminal websocket. Output goes
// the other way as binary frames.
type termMsg struct {
	Type string `json:"type"` // input | resize
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// terminal attaches to the agent's session: read-only unless ?mode=control.
func (s *server) terminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	readOnly := r.URL.Query().Get("mode") != "control"
	// Remote auth permits only the configured public HTTPS origin.
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	c, err := websocket.Accept(w, r, s.websocketOptions())
	if err != nil {
		return
	}
	defer c.CloseNow()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	term, err := s.svc.Attach(ctx, name, readOnly)
	if err != nil {
		code := websocket.StatusCode(4500)
		switch {
		case errors.Is(err, core.ErrNotRunning):
			code = 4409
		case errors.Is(err, core.ErrNotFound):
			code = 4404
		}
		_ = c.Close(code, excerpt(err.Error(), 100))
		return
	}
	defer term.Close()
	s.log.Info("terminal attached", "agent", name, "read_only", readOnly)

	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = c.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
		}
	}()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m termMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "resize":
			if m.Cols > 0 && m.Rows > 0 {
				_ = term.Resize(m.Cols, m.Rows)
			}
		case "input":
			if !readOnly {
				if _, err := term.Write([]byte(m.Data)); err != nil {
					return
				}
			}
		}
	}
}

// harnessLabel is for the config summary.
func harnessLabel(rt core.Runtime) string {
	if rt.Model == "" {
		return fmt.Sprintf("%s · default model", rt.Harness)
	}
	return fmt.Sprintf("%s · %s", rt.Harness, rt.Model)
}
