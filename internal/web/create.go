package web

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type newView struct {
	Teams           []core.Team
	Depth           map[string]int
	Me              core.User
	Req             core.CreateRequest
	Adopt           core.AdoptRequest
	Options         core.CreateOptions
	Agents          []core.AgentStatus
	ClaudeModels    []core.ModelOption
	CodewhaleModels []core.ModelOption
	Error, AError   string
	Fields          map[string]string
	Adopting        bool
	RecoveryDir     string
	Partial         string
}

func (s *server) newData(r *http.Request) (newView, error) {
	v := newView{Me: s.svc.Me(), Req: core.CreateRequest{Harness: core.HarnessClaude, Permissions: core.PermBypass, Skills: core.SkillsInherit, Start: true}, Adopt: core.AdoptRequest{Runtime: core.Runtime{Harness: core.HarnessClaude, Permissions: core.PermBypass}}, Fields: map[string]string{}}
	ov, err := s.orgData(r.Context())
	if err != nil {
		return v, err
	}
	v.Teams, v.Depth = ov.Teams, ov.Depth
	v.Options, err = s.svc.CreateOptions(r.Context())
	if err != nil {
		return v, err
	}
	v.Agents, err = s.svc.List(r.Context())
	v.ClaudeModels = s.svc.Models(core.HarnessClaude)
	v.CodewhaleModels = s.svc.Models(core.HarnessCodewhale)
	return v, err
}

func (s *server) newPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.newData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Req.Team = r.URL.Query().Get("team")
	v.Adopting = r.URL.Query().Get("mode") == "adopt"
	v.Adopt.Dir = r.URL.Query().Get("dir")
	s.page(w, r, http.StatusOK, "new", layout{Title: "New agent", Data: v})
}
func field(r *http.Request, k string) string { return strings.TrimSpace(r.FormValue(k)) }

var newSlugSeparators = regexp.MustCompile(`[^a-z0-9]+`)
var newSlugValid = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func newSlug(name string) string {
	return strings.Trim(newSlugSeparators.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	req := core.CreateRequest{Name: field(r, "name"), Slug: field(r, "slug"), Role: field(r, "role"), User: field(r, "user"), Dir: field(r, "dir"), Harness: core.Harness(field(r, "harness")), Model: field(r, "model"), Skills: core.SkillsMode(field(r, "skills")), Permissions: core.Permissions(field(r, "permissions")), OnWake: field(r, "on_wake"), Mission: field(r, "mission"), Team: field(r, "team"), Head: r.FormValue("head") == "on", Start: r.FormValue("start") == "on"}
	if req.Harness == "" {
		req.Harness = core.HarnessClaude
	}
	if req.Permissions == "" {
		req.Permissions = core.PermBypass
	}
	fields := map[string]string{}
	if req.Name == "" {
		fields["name"] = "Give your agent a name."
	}
	// Existing integrations can continue submitting a role without a mission;
	// the v3 form requires a purpose and uses it on every fresh conversation.
	if r.FormValue("flow") == "v3" && req.Mission == "" {
		fields["mission"] = "Describe the job you want this agent to own."
	}
	slug := req.Slug
	if slug == "" {
		slug = newSlug(req.Name)
	}
	if !newSlugValid.MatchString(slug) {
		fields["slug"] = "Use 1–63 lowercase letters, numbers or dashes; start with a letter or number."
	}
	if req.Skills != "" && req.Skills != core.SkillsInherit && req.Skills != core.SkillsBarebones {
		fields["skills"] = "Choose inherited skills or barebones."
	}
	for key, value := range map[string]string{"name": req.Name, "role": req.Role, "user": req.User, "dir": req.Dir} {
		if strings.ContainsAny(value, "\n\r\"\\|&") {
			fields[key] = "Setup cannot use quotes, backslashes, newlines, | or & here."
		}
	}
	agents, err := s.svc.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, a := range agents {
		if a.Name == slug {
			fields["slug"] = "This slug already exists. Choose another name or edit the slug below."
		}
		if req.Dir != "" && filepath.Clean(a.Dir) == filepath.Clean(req.Dir) {
			fields["dir"] = "This directory already belongs to " + a.Display + ". Open that agent instead."
		}
	}
	if req.Dir != "" && !filepath.IsAbs(req.Dir) {
		fields["dir"] = "Enter an absolute directory path, or leave it blank for the default."
	}
	options, err := s.svc.CreateOptions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := req.Dir
	if target == "" {
		target = filepath.Join(options.DefaultDir, slug)
	}
	for _, c := range options.Candidates {
		if filepath.Clean(c.Dir) == filepath.Clean(target) {
			fields["dir"] = "This directory already contains an agent. Choose another directory or use Adopt."
		}
	}
	if len(fields) > 0 {
		s.newFailed(w, r, core.ErrInvalid, func(v *newView) {
			v.Req = req
			v.Fields = fields
			v.Error = "Check the highlighted fields. Nothing has been created."
		})
		return
	}
	if req.OnWake == "" && req.Mission != "" {
		req.OnWake = "Read CLAUDE.md, check lolad msg inbox, and continue your mission. Reply only when needed.\nYour purpose: " + req.Mission
	}
	st, err := s.svc.Create(r.Context(), req)
	if err != nil {
		s.newFailed(w, r, err, func(v *newView) {
			v.Req = req
			v.Error = humanize(err, "Choose a different slug or adopt the existing directory.")
			v.Partial = st.Name
			v.RecoveryDir = req.Dir
			if v.RecoveryDir == "" {
				v.RecoveryDir = filepath.Join(v.Options.DefaultDir, slug)
			}
			if errors.Is(err, core.ErrExists) {
				v.Fields["dir"] = v.Error
			}
		})
		return
	}
	s.redirect(w, r, "/inbox/"+url.PathEscape(st.Name)+"?created=1")
}

func (s *server) adopt(w http.ResponseWriter, r *http.Request) {
	req := core.AdoptRequest{Dir: field(r, "dir"), Slug: field(r, "slug"), Runtime: core.Runtime{Harness: core.Harness(field(r, "harness")), Model: field(r, "model"), Permissions: core.Permissions(field(r, "permissions"))}}
	if req.Runtime.Harness == "" {
		req.Runtime.Harness = core.HarnessClaude
	}
	if !filepath.IsAbs(req.Dir) {
		s.newFailed(w, r, core.ErrInvalid, func(v *newView) {
			v.Adopt = req
			v.Adopting = true
			v.AError = "Enter an absolute directory path."
			v.Fields["adopt-dir"] = v.AError
		})
		return
	}
	agents, err := s.svc.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, a := range agents {
		if filepath.Clean(a.Dir) == filepath.Clean(req.Dir) || (req.Slug != "" && a.Name == req.Slug) {
			s.newFailed(w, r, core.ErrExists, func(v *newView) {
				v.Adopt = req
				v.Adopting = true
				v.Partial = a.Name
				v.AError = "This directory or slug is already registered. Open its settings, or choose a different directory and slug."
			})
			return
		}
	}
	st, err := s.svc.Adopt(r.Context(), req)
	if err != nil {
		s.newFailed(w, r, err, func(v *newView) {
			v.Adopt = req
			v.AError = humanize(err, "Set a different slug.")
			v.Adopting = true
			v.Partial = st.Name
		})
		return
	}
	s.redirect(w, r, "/inbox/"+url.PathEscape(st.Name)+"?adopted=1")
}

func (s *server) newFailed(w http.ResponseWriter, r *http.Request, err error, set func(*newView)) {
	code := statusFor(err)
	if code == http.StatusInternalServerError {
		s.log.Error("create", "err", err)
	}
	v, verr := s.newData(r)
	if verr != nil {
		s.fail(w, r, verr)
		return
	}
	set(&v)
	if isHX(r) {
		s.fragment(w, http.StatusUnprocessableEntity, "new-body", v)
		return
	}
	s.page(w, r, code, "new", layout{Title: "New agent", Data: v})
}

func humanize(err error, existsHint string) string {
	msg := err.Error()
	for _, e := range []error{core.ErrInvalid, core.ErrDenied} {
		msg = strings.TrimPrefix(msg, e.Error()+": ")
	}
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	if errors.Is(err, core.ErrExists) {
		msg += " " + existsHint
	}
	return msg
}

// Keep path escaping in the view rather than allowing a directory into a URL.
func (v newView) RecoveryURL() string { return "/new?mode=adopt&dir=" + url.QueryEscape(v.RecoveryDir) }
func (v newView) Existing() string {
	var names []string
	for _, a := range v.Agents {
		names = append(names, a.Name)
	}
	return strings.Join(names, ",")
}
func (v newView) DefaultPath() string {
	slug := v.Req.Slug
	if slug == "" {
		slug = newSlug(v.Req.Name)
	}
	if slug == "" {
		slug = "<slug>"
	}
	return filepath.Join(v.Options.DefaultDir, slug)
}
func (v newView) Field(key string) string { return v.Fields[key] }

func (v newView) AvailableCandidates() []core.AdoptCandidate {
	var out []core.AdoptCandidate
	for _, c := range v.Options.Candidates {
		if !c.Registered {
			out = append(out, c)
		}
	}
	return out
}
func (v newView) RegisteredCandidates() []core.AdoptCandidate {
	var out []core.AdoptCandidate
	for _, c := range v.Options.Candidates {
		if c.Registered {
			out = append(out, c)
		}
	}
	return out
}
