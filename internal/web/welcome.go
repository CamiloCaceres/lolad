package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
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
	setup.StepHarnesses: "Brain",
	setup.StepFolder:    "Folder",
	setup.StepName:      "Your name",
	setup.StepAgent:     "First assistant",
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
	CreatedState  core.State
	CreatedRole   string
	Hello         string // the new assistant's first message, once it has written
	NextHref      string

	// The first-run boards (Ob-Job, Ob-Shape): web screens inside the
	// setup's single "agent" step.
	Jobs []welcomeJob
	Job  welcomeJob // the chosen one
	Work string     // ask | own

	Friend welcomeFriend // optional "Got an invite from a friend?" (welcome_friend.go)
}

// agentHarnesses are the harnesses lolad can create agents on.
func creatableHarness(id string) bool {
	return id == string(core.HarnessClaude) || id == string(core.HarnessCodewhale)
}

// welcomeJob is a first job on the Ob-Job board: it gives the assistant its
// role, its mission and a few name ideas, and Ready a few first things to say.
type welcomeJob struct {
	Key, Label, Desc string
	Role, Mission    string
	Names            []string
	Chips            []string
}

var welcomeJobs = []welcomeJob{
	{Key: "assistant", Label: "Personal assistant", Desc: "Calendar, errands, reminders, bookings.", Role: "Personal assistant",
		Mission: "Be my personal assistant: keep my calendar, chase errands, book things and remind me before I forget.",
		Names:   []string{"Atlas", "Nova", "Sage", "Juniper", "Otto"}, Chips: []string{"Plan my week", "Remind me about bills", "Find a dentist near me"}},
	{Key: "inbox", Label: "Inbox helper", Desc: "Sorts your email and drafts replies for you to check.", Role: "Inbox helper",
		Mission: "Help me with my email: sort what comes in, tell me what matters, and draft replies for me to check before anything is sent.",
		Names:   []string{"Iris", "Wren", "Pip", "Milo", "Echo"}, Chips: []string{"What came in today?", "Draft a reply for me", "Help me clear my inbox"}},
	{Key: "research", Label: "Researcher", Desc: "Finds things out and sends you a short summary.", Role: "Researcher",
		Mission: "Find things out for me and send me a short, clear summary, with where you found it.",
		Names:   []string{"Scout", "Vera", "Sage", "Orion", "Lumen"}, Chips: []string{"Compare three options for me", "Summarise an article", "What's new in my field?"}},
	{Key: "writer", Label: "Writer", Desc: "Newsletters, posts and drafts in your voice.", Role: "Writer",
		Mission: "Write with me: newsletters, posts and drafts in my voice. Show me every draft before anything goes out.",
		Names:   []string{"Quill", "Poe", "Ink", "Juno", "Verse"}, Chips: []string{"Draft this week's newsletter", "Tighten a paragraph", "Ideas for a post"}},
	{Key: "books", Label: "Bookkeeper", Desc: "Receipts, budgets and a monthly money check-in.", Role: "Bookkeeper",
		Mission: "Keep my money tidy: track receipts and budgets, and give me a short money check-in every month.",
		Names:   []string{"Penny", "Ledger", "Tally", "Abe", "Coin"}, Chips: []string{"Set up a monthly budget", "Where did my money go?", "Remind me about bills"}},
	{Key: "other", Label: "Something else", Desc: "Describe it in your own words.", Role: "Assistant",
		Names: []string{"Atlas", "Nova", "Sage", "Juniper", "Otto"}, Chips: []string{"Here's what I need first", "What can you do?", "Plan my week"}},
}

func jobByKey(k string) (welcomeJob, bool) {
	for _, j := range welcomeJobs {
		if j.Key == k {
			return j, true
		}
	}
	return welcomeJobs[0], false
}

// askFirst is added to an assistant's wake-up text when the user chose "Ask
// me first" on Ob-Shape: it checks in through ask_user, not through
// terminal permission prompts the user would never see.
const askFirst = "Check with me through ask_user, and wait for my answer, before you do anything new, anything that costs money, or anything outward-facing (sending, posting, booking or buying)."

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
	m.HandleFunc("GET /welcome/waking", s.welcomeWaking)
	m.HandleFunc("POST /welcome/later", s.welcomeLater)
	m.HandleFunc("POST /welcome/leave", s.welcomeLeave)
	s.welcomeFriendRoutes(m)
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
	q := r.URL.Query()
	switch want := q.Get("step"); {
	case slices.Contains(setup.StepOrder, want) && stepIndex(want) <= cur:
		shown = want
	case (want == "job" || want == "shape") && cur >= stepIndex(setup.StepAgent):
		shown = want
	case (want == "waking" || want == "ready") && q.Get("agent") != "":
		shown = want
	}
	switch shown {
	case setup.StepLogin:
		shown = "hello" // Ob-Welcome; "Keep running" is offered on Ready
	case setup.StepAgent:
		shown = "job"
	case setup.StepDone:
		if q.Get("agent") != "" {
			shown = "ready"
		}
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
	v.Jobs = welcomeJobs
	v.Job, _ = jobByKey(q.Get("job"))
	v.Work = "ask"
	if q.Get("work") == "own" {
		v.Work = "own"
	}
	v.Agent.Name, v.Agent.Mission = v.Job.Names[0], cmp.Or(q.Get("mission"), v.Job.Mission, starterMission)
	if n := strings.TrimSpace(q.Get("name")); n != "" {
		v.Agent.Name = n
	}
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
	_, v.Friend.On = s.friendInviter()
	v.Poll = len(st.Terminals) > 0
	_, v.AnyReady = st.FirstReady()
	if slug := q.Get("agent"); slug != "" {
		if a, err := s.svc.Get(r.Context(), slug); err == nil {
			v.Created, v.CreatedName, v.CreatedState, v.CreatedRole = a.Name, cmp.Or(a.Display, a.Name), a.State, a.Role
			v.Hello = s.firstWords(r.Context(), a.Name)
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
	if r.FormValue("back") == "ready" {
		s.welcomeGo(w, r, "/welcome?step=ready&agent="+url.QueryEscape(r.FormValue("agent"))+"&job="+url.QueryEscape(r.FormValue("job")))
		return
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
	job, known := jobByKey(field(r, "job"))
	if known && job.Key != "other" {
		mission = job.Mission
	}
	work := cmp.Or(field(r, "work"), "own") // older forms: works on its own
	dir := field(r, "dir")
	keep := func(v *welcomeView) {
		v.Agent.Name, v.Agent.Mission, v.Agent.Harness = name, mission, harness
		v.Job, v.Work = job, work
		if dir != "" {
			v.Folder = dir
		}
	}
	fields := map[string]string{}
	slug := newSlug(name)
	if name == "" {
		fields["name"] = "Give your assistant a name."
	} else if !newSlugValid.MatchString(slug) {
		fields["name"] = "Use letters or numbers in the name."
	}
	if strings.ContainsAny(name, "\n\r\"\\|&") {
		fields["name"] = "Setup cannot use quotes, backslashes, newlines, | or & here."
	}
	if mission == "" {
		fields["mission"] = "Describe the job you want this assistant to own."
	}
	if dir != "" && fields["name"] == "" && fields["mission"] == "" {
		// Ob-Shape's Advanced: a different home for the assistants.
		if st, err := s.welcomeStatus(r.Context()); err == nil && dir != st.AgentsDir {
			if _, err := s.setup.Save(r.Context(), setup.SaveRequest{AgentsDir: &dir}); err != nil {
				fields["dir"] = humanize(err, "Choose another folder.")
			}
		}
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
				fields["name"] = "An assistant called " + slug + " already exists. Choose another name."
			}
		}
	}
	if len(fields) > 0 {
		s.welcomeFail(w, r, "shape", core.ErrInvalid, func(v *welcomeView) {
			keep(v)
			v.Fields = fields
			v.Error = "Check the highlighted fields. Nothing has been created."
		})
		return
	}
	onWake := "Read CLAUDE.md, check lolad msg inbox, and continue your mission. Reply only when needed.\nYour purpose: " + mission
	if work == "ask" {
		mission += "\n\n" + askFirst
		onWake += "\n" + askFirst
	}
	req := core.CreateRequest{
		Name: name, Slug: slug, Dir: filepath.Join(st.AgentsDir, slug), Role: job.Role,
		Harness: core.Harness(harness), Skills: core.SkillsInherit, Permissions: core.PermBypass,
		Mission: mission, Start: true, OnWake: onWake,
	}
	if !known {
		req.Role = ""
	}
	a, err := s.svc.Create(r.Context(), req)
	if err != nil {
		s.welcomeFail(w, r, "shape", err, func(v *welcomeView) {
			keep(v)
			v.Error = humanize(err, "Choose a different name.")
		})
		return
	}
	s.welcomeGo(w, r, "/welcome?step=waking&agent="+url.QueryEscape(a.Name)+"&job="+url.QueryEscape(job.Key))
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

// ---- Ob-Waking and Ob-Ready

// wakingGrace is how long Waking waits for the new assistant before moving
// on to Ready anyway, which then says it is still waking up.
const wakingGrace = 60 * time.Second

// welcomeWaking is Waking's live part: the checklist, polled until the new
// assistant is up (or can't start), then on to Ready.
func (s *server) welcomeWaking(w http.ResponseWriter, r *http.Request) {
	slug, job := r.URL.Query().Get("agent"), r.URL.Query().Get("job")
	a, err := s.svc.Get(r.Context(), slug)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ready := "/welcome?step=ready&agent=" + url.QueryEscape(a.Name) + "&job=" + url.QueryEscape(job)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	up := a.State == core.StateIdle || a.State == core.StateBusy || a.State == core.StateWaiting
	if up || (a.State != core.StateFailed && since > 0 && time.Since(time.Unix(since, 0)) > wakingGrace) {
		w.Header().Set("HX-Redirect", ready)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.fragment(w, http.StatusOK, "welcome-waking-list", map[string]any{"A": a, "Job": job, "Since": since})
}

// firstWords is the start of the assistant's first message to the user, for
// Ready; "" until it has written.
func (s *server) firstWords(ctx context.Context, slug string) string {
	msgs, err := s.svc.Conversation(ctx, slug, 20, time.Time{})
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		if m.FromName == slug {
			return excerpt(strings.Join(strings.Fields(m.Body), " "), 400)
		}
	}
	return ""
}

// welcomeDot is the screen's place among the five dots (0: no dots).
func welcomeDot(step string) int {
	switch step {
	case setup.StepHarnesses, setup.StepFolder:
		return 1
	case setup.StepName:
		return 2
	case "job":
		return 3
	case "shape":
		return 4
	case "waking":
		return 5
	}
	return 0
}

// welcomeBack is where the header's Back goes, "" for none.
func welcomeBack(v welcomeView) string {
	switch v.Step {
	case setup.StepFolder:
		return "/welcome?step=" + setup.StepHarnesses
	case setup.StepName:
		return "/welcome?step=" + setup.StepHarnesses
	case "job":
		return "/welcome?step=" + setup.StepName
	case "shape":
		return "/welcome?step=job&job=" + url.QueryEscape(v.Job.Key)
	}
	return ""
}

// welcomeHarness names the brain that is ready, for "Claude connected".
func welcomeHarness(v welcomeView) string {
	for _, h := range v.S.Harnesses {
		if h.Ready && creatableHarness(h.ID) {
			if h.ID == string(core.HarnessClaude) {
				return "Claude"
			}
			return h.Name
		}
	}
	return ""
}

func jobIcon(key string) string {
	return map[string]string{"assistant": "home", "inbox": "mail", "research": "search", "writer": "edit", "books": "list", "other": "plus"}[key]
}

// article is "A" or "An" for a label.
func article(s string) string {
	if s != "" && strings.ContainsRune("AEIOUaeiou", rune(s[0])) {
		return "An"
	}
	return "A"
}
