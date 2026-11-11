package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/setup"
)

// First-run onboarding (/welcome). See WELCOME.md. Every step is a plain form
// post that redirects, so it works without scripting; htmx only makes the
// moves smooth and keeps the harness list live while an installer runs.

type welcomeStep struct {
	Key, Label string
	N          int
	State      string // done | current | todo
	Href       string // a finished step can be revisited
}

var welcomeLabels = map[string]string{
	setup.StepLogin:     "Start at login",
	setup.StepHarnesses: "Agent tools",
	setup.StepFolder:    "Agents folder",
	setup.StepName:      "Your name",
	setup.StepAgent:     "First agent",
}

type welcomeView struct {
	S        setup.Status
	Step     string // the screen shown
	Steps    []welcomeStep
	Terminal *setup.TerminalInfo
	Poll     bool // keep the harness list refreshing
	AnyReady bool // a harness is ready

	Error  string
	Fields map[string]string

	// form values
	Folder        string
	FolderWarning string
	Name          string
	Agent         struct{ Name, Mission, Harness string }
	Creatable     []setup.Harness // ready harnesses an agent can run on
	Created       string          // slug of the agent just made
	CreatedName   string
	NextHref      string
}

// agentHarnesses are the harnesses lolad can create agents on.
func creatableHarness(id string) bool {
	return id == string(core.HarnessClaude) || id == string(core.HarnessCodewhale)
}

const starterMission = "Be my right hand. Keep track of what I ask you to do, tell me when something is blocked, and help me decide what to work on next."

func (s *server) welcomeRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /welcome", s.welcomePage)
	m.HandleFunc("GET /welcome/harnesses", s.welcomeHarnesses)
	m.HandleFunc("POST /welcome/harnesses/rescan", s.welcomeRescan)
	m.HandleFunc("POST /welcome/terminal", s.welcomeTerminalStart)
	m.HandleFunc("GET /welcome/terminal/{session}", s.welcomeTerminal)
	m.HandleFunc("POST /welcome/service", s.welcomeService)
	m.HandleFunc("GET /welcome/folder/check", s.welcomeFolderCheck)
	m.HandleFunc("POST /welcome/folder", s.welcomeFolder)
	m.HandleFunc("POST /welcome/name", s.welcomeName)
	m.HandleFunc("POST /welcome/agent", s.welcomeAgent)
	m.HandleFunc("POST /welcome/later", s.welcomeLater)
	m.HandleFunc("POST /welcome/leave", s.welcomeLeave)
}

// welcomeRedirect sends a first-run visitor from "/" to /welcome. It never
// traps anyone who already has agents, or who left setup.
func (s *server) welcomeRedirect(w http.ResponseWriter, r *http.Request) bool {
	if s.setup == nil || !s.setup.Needed() {
		return false
	}
	if agents, err := s.list(r.Context()); err != nil || len(agents) > 0 {
		return false
	}
	// A full navigation, so the page's own scripts load.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/welcome")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	http.Redirect(w, r, "/welcome", http.StatusSeeOther)
	return true
}

// welcomeStatus is the setup status with the daemon's own view of agents.
func (s *server) welcomeStatus(ctx context.Context) (setup.Status, error) {
	st, err := s.setup.Status(ctx)
	if err != nil {
		return st, err
	}
	if agents, err := s.list(ctx); err == nil && len(agents) > 0 {
		st.HasAgents = true
	}
	st.Settle()
	return st, nil
}

func stepIndex(step string) int {
	if i := slices.Index(setup.StepOrder, step); i >= 0 {
		return i
	}
	return len(setup.StepOrder) // done
}

func (s *server) welcomeView(r *http.Request) (welcomeView, error) {
	v := welcomeView{Fields: map[string]string{}}
	st, err := s.welcomeStatus(r.Context())
	if err != nil {
		return v, err
	}
	v.S = st
	cur := stepIndex(st.Step)
	shown := st.Step
	if want := r.URL.Query().Get("step"); slices.Contains(setup.StepOrder, want) && stepIndex(want) <= cur {
		shown = want
	}
	// Done is only a screen right after finishing; later visits start over at the end.
	v.Step = shown
	for i, k := range setup.StepOrder {
		step := welcomeStep{Key: k, Label: welcomeLabels[k], N: i + 1, State: "todo"}
		switch {
		case k == shown:
			step.State = "current"
		case i < cur:
			step.State = "done"
			step.Href = "/welcome?step=" + k
		}
		v.Steps = append(v.Steps, step)
	}
	v.Folder = st.AgentsDir
	v.FolderWarning = st.AgentsDirWarning
	v.Name = st.UserName
	v.Agent.Name, v.Agent.Mission = "Maple", starterMission
	for _, h := range st.Harnesses {
		if h.Ready && creatableHarness(h.ID) {
			v.Creatable = append(v.Creatable, h)
		}
	}
	if len(v.Creatable) > 0 {
		v.Agent.Harness = v.Creatable[0].ID
	}
	if t := r.URL.Query().Get("terminal"); setup.ValidSession(t) {
		info := setup.TerminalInfo{Session: t, Action: strings.TrimPrefix(t, "setup-"), TerminalURL: "/welcome?terminal=" + t}
		info.Title = setup.ActionTitle(info.Action)
		v.Terminal = &info
	}
	v.Poll = len(st.Terminals) > 0
	_, v.AnyReady = st.FirstReady()
	if slug := r.URL.Query().Get("agent"); slug != "" {
		if a, err := s.svc.Get(r.Context(), slug); err == nil {
			v.Created, v.CreatedName = a.Name, a.Display
		}
	}
	return v, nil
}

func (s *server) welcomeRender(w http.ResponseWriter, r *http.Request, status int, v welcomeView) {
	_, pages := s.templates()
	t, ok := pages["welcome"]
	if !ok {
		http.Error(w, "no page welcome", http.StatusInternalServerError)
		return
	}
	s.write(w, status, t, "layout", v)
}

func (s *server) welcomePage(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	v, err := s.welcomeView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.welcomeRender(w, r, http.StatusOK, v)
}

// welcomeFail re-renders the current screen with an error, keeping what was typed.
func (s *server) welcomeFail(w http.ResponseWriter, r *http.Request, step string, err error, set func(*welcomeView)) {
	code := statusFor(err)
	if code == http.StatusInternalServerError {
		s.log.Error("welcome", "step", step, "err", err)
	}
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	q.Set("step", step)
	r2.URL.RawQuery = q.Encode()
	v, verr := s.welcomeView(r2)
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	v.Step = step
	v.Error = humanize(err, "")
	if set != nil {
		set(&v)
	}
	s.welcomeRender(w, r, code, v)
}

func (s *server) welcomeGo(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- step 1: start at login

func (s *server) welcomeService(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	if r.FormValue("op") == "install" {
		if _, err := s.setup.InstallService(r.Context()); err != nil {
			s.welcomeFail(w, r, setup.StepLogin, err, nil)
			return
		}
	} else {
		yes := true
		if _, err := s.setup.Save(r.Context(), setup.SaveRequest{ServiceDone: &yes}); err != nil {
			s.welcomeFail(w, r, setup.StepLogin, err, nil)
			return
		}
	}
	s.welcomeGo(w, r, "/welcome?step="+setup.StepHarnesses)
}

// ---- step 2: harnesses

// welcomeHarnesses is the live fragment: the list plus the Continue button
// (out of band), refreshed while an installer or login runs.
func (s *server) welcomeHarnesses(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	v, err := s.welcomeView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, http.StatusOK, "welcome-harnesses-live", v)
}

func (s *server) welcomeRescan(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	if r.FormValue("capture_path_present") != "" {
		on := r.FormValue("capture_path") == "on"
		if _, err := s.setup.Save(r.Context(), setup.SaveRequest{CapturePath: &on}); err != nil {
			s.welcomeFail(w, r, setup.StepHarnesses, err, nil)
			return
		}
	}
	if _, err := s.setup.Rescan(r.Context()); err != nil {
		s.welcomeFail(w, r, setup.StepHarnesses, err, nil)
		return
	}
	to := "/welcome?step=" + setup.StepHarnesses
	if t := r.FormValue("terminal"); setup.ValidSession(t) {
		to += "&terminal=" + url.QueryEscape(t)
	}
	s.welcomeGo(w, r, to)
}

func (s *server) welcomeTerminalStart(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	info, err := s.setup.StartTerminal(r.Context(), r.FormValue("action"))
	if err != nil {
		s.welcomeFail(w, r, setup.StepHarnesses, err, nil)
		return
	}
	s.welcomeGo(w, r, "/welcome?step="+setup.StepHarnesses+"&terminal="+url.QueryEscape(info.Session))
}

// welcomeTerminal streams a setup session to xterm.js, like an agent's terminal.
func (s *server) welcomeTerminal(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("session")
	control := r.URL.Query().Get("mode") == "control"
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	c, err := websocket.Accept(w, r, s.websocketOptions())
	if err != nil {
		return
	}
	defer c.CloseNow()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	term, err := s.setup.Attach(ctx, name, control)
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
	pumpTerminal(ctx, cancel, c, term, !control)
}

// ---- step 3: folder

func (s *server) welcomeFolderCheck(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	dir, warn := s.setup.CheckFolder(r.URL.Query().Get("dir"))
	s.fragment(w, http.StatusOK, "welcome-folder-check", map[string]string{"Path": dir, "Warning": warn})
}

func (s *server) welcomeFolder(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	dir := field(r, "dir")
	if _, err := s.setup.Save(r.Context(), setup.SaveRequest{AgentsDir: &dir}); err != nil {
		s.welcomeFail(w, r, setup.StepFolder, err, func(v *welcomeView) {
			v.Folder = dir
			v.Fields["dir"] = v.Error
		})
		return
	}
	s.welcomeGo(w, r, "/welcome?step="+setup.StepName)
}

// ---- step 4: name

func (s *server) welcomeName(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	name := field(r, "name")
	fail := func(err error) {
		s.welcomeFail(w, r, setup.StepName, err, func(v *welcomeView) {
			v.Name = name
			v.Fields["name"] = v.Error
		})
	}
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "\n\r\x00") {
		fail(fmt.Errorf("%w: tell lolad what to call you", core.ErrInvalid))
		return
	}
	// The daemon owns org.yaml while it runs, so go through it.
	org, err := s.svc.Org(r.Context())
	if err != nil {
		fail(err)
		return
	}
	org.User.Display = name
	if err := s.svc.SaveOrg(r.Context(), org); err != nil {
		fail(err)
		return
	}
	yes := true
	if _, err := s.setup.Save(r.Context(), setup.SaveRequest{UserName: &name, NameSaved: &yes}); err != nil {
		fail(err)
		return
	}
	s.welcomeGo(w, r, "/welcome?step="+setup.StepAgent)
}

// ---- step 5: first agent

func (s *server) welcomeAgent(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	name, mission, harness := field(r, "name"), field(r, "mission"), field(r, "harness")
	keep := func(v *welcomeView) { v.Agent.Name, v.Agent.Mission, v.Agent.Harness = name, mission, harness }
	fields := map[string]string{}
	slug := newSlug(name)
	if name == "" {
		fields["name"] = "Give your agent a name."
	} else if !newSlugValid.MatchString(slug) {
		fields["name"] = "Use letters or numbers in the name."
	}
	if strings.ContainsAny(name, "\n\r\"\\|&") {
		fields["name"] = "Setup cannot use quotes, backslashes, newlines, | or & here."
	}
	if mission == "" {
		fields["mission"] = "Describe the job you want this agent to own."
	}
	st, err := s.welcomeStatus(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if h, ok := st.Harness(harness); !ok || !h.Ready || !creatableHarness(harness) {
		fields["harness"] = "Choose a tool that is installed and signed in."
	}
	if len(fields) == 0 {
		agents, err := s.list(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, a := range agents {
			if a.Name == slug {
				fields["name"] = "An agent called " + slug + " already exists. Choose another name."
			}
		}
	}
	if len(fields) > 0 {
		s.welcomeFail(w, r, setup.StepAgent, core.ErrInvalid, func(v *welcomeView) {
			keep(v)
			v.Fields = fields
			v.Error = "Check the highlighted fields. Nothing has been created."
		})
		return
	}
	req := core.CreateRequest{
		Name: name, Slug: slug, Dir: filepath.Join(st.AgentsDir, slug),
		Harness: core.Harness(harness), Skills: core.SkillsInherit, Permissions: core.PermBypass,
		Mission: mission, Start: true,
		OnWake: "Read CLAUDE.md, check lolad msg inbox, and continue your mission. Reply only when needed.\nYour purpose: " + mission,
	}
	a, err := s.svc.Create(r.Context(), req)
	if err != nil {
		s.welcomeFail(w, r, setup.StepAgent, err, func(v *welcomeView) {
			keep(v)
			v.Error = humanize(err, "Choose a different name.")
		})
		return
	}
	s.welcomeGo(w, r, "/welcome?step=done&agent="+url.QueryEscape(a.Name))
}

// welcomeLater skips the first agent: the user will add one from New agent.
func (s *server) welcomeLater(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	yes := true
	if _, err := s.setup.Save(r.Context(), setup.SaveRequest{SkipAgent: &yes}); err != nil {
		s.welcomeFail(w, r, setup.StepAgent, err, nil)
		return
	}
	s.welcomeGo(w, r, "/welcome?step=done")
}

// welcomeLeave leaves setup for now; "/" stops sending the visitor back.
func (s *server) welcomeLeave(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	yes := true
	if _, err := s.setup.Save(r.Context(), setup.SaveRequest{Dismiss: &yes}); err != nil {
		s.fail(w, r, err)
		return
	}
	// A full navigation to the inbox.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// pumpTerminal copies a terminal to the websocket and keystrokes and resizes
// back, until either side ends. Output goes out as binary frames; input and
// resize arrive as termMsg JSON.
func pumpTerminal(ctx context.Context, cancel context.CancelFunc, c *websocket.Conn, term core.Terminal, readOnly bool) {
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
