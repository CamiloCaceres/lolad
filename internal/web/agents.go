package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type fleetView struct {
	Agents []core.AgentStatus
	Counts []stateCount
	Teams  map[string]string // id -> name
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
	s.page(w, r, http.StatusOK, "agents", layout{Title: "Agents", Data: v})
}

func (s *server) fleetFragment(w http.ResponseWriter, r *http.Request) {
	v, err := s.fleetData(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, http.StatusOK, "fleet-body", v)
}

type agentView struct {
	A        core.AgentStatus
	TeamName string
	Events   []core.Event
	Mail     []core.Mail
	Me       string
	EnvKeys  []string
}

func (s *server) agentData(ctx context.Context, name string) (agentView, error) {
	a, err := s.svc.Get(ctx, name)
	if err != nil {
		return agentView{}, err
	}
	v := agentView{A: a, Me: s.svc.Me().Name, EnvKeys: slices.Sorted(maps.Keys(a.Runtime.Env))}
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
	slices.SortStableFunc(v.Events, func(a, b core.Event) int { return b.At.Compare(a.At) })
	traffic, err := s.svc.Traffic(ctx, 400, time.Time{})
	if err != nil {
		return v, err
	}
	for _, m := range traffic {
		if m.FromName == name || m.ToName == name {
			v.Mail = append(v.Mail, m)
			if len(v.Mail) == 40 {
				break
			}
		}
	}
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
	if !slices.Contains([]string{"head", "events", "mail", "facts"}, part) {
		s.notFound(w, r)
		return
	}
	v, err := s.agentData(r.Context(), r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
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
		http.Redirect(w, r, cmp.Or(r.FormValue("back"), "/agents/"+name), http.StatusSeeOther)
		return
	}
	toast(w, "ok", verb+" "+name)
	if r.FormValue("from") == "fleet" {
		v, err := s.fleetData(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.fragment(w, http.StatusOK, "fleet-body", v)
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
	c, err := websocket.Accept(w, r, nil) // same-origin only (default OriginPatterns)
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
