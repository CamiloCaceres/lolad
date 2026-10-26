package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type newView struct {
	Teams  []core.Team
	Depth  map[string]int
	Me     core.User
	Req    core.CreateRequest // echoed back after an error
	Adopt  core.AdoptRequest
	Error  string // create form
	AError string // adopt form
}

func (s *server) newData(r *http.Request) (newView, error) {
	v := newView{Me: s.svc.Me(), Req: core.CreateRequest{Harness: core.HarnessClaude, Permissions: core.PermBypass, Start: true},
		Adopt: core.AdoptRequest{Runtime: core.Runtime{Harness: core.HarnessClaude, Permissions: core.PermBypass}}}
	ov, err := s.orgData(r.Context())
	if err != nil {
		return v, err
	}
	v.Teams, v.Depth = ov.Teams, ov.Depth
	return v, nil
}

func (s *server) newPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.newData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if t := r.URL.Query().Get("team"); t != "" {
		v.Req.Team = t
	}
	s.page(w, r, http.StatusOK, "new", layout{Title: "New agent", Data: v})
}

func field(r *http.Request, k string) string { return strings.TrimSpace(r.FormValue(k)) }

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	req := core.CreateRequest{
		Name: field(r, "name"), Slug: field(r, "slug"), Role: field(r, "role"), User: field(r, "user"), Dir: field(r, "dir"),
		Harness: core.Harness(field(r, "harness")), Model: field(r, "model"), Permissions: core.Permissions(field(r, "permissions")),
		OnWake: field(r, "on_wake"), Mission: field(r, "mission"), Team: field(r, "team"),
		Head: r.FormValue("head") == "on", Start: r.FormValue("start") == "on",
	}
	st, err := s.svc.Create(r.Context(), req)
	if err != nil {
		s.newFailed(w, r, err, func(v *newView) { v.Req, v.Error = req, humanize(err, "Pick another name, or set a different slug.") })
		return
	}
	s.redirect(w, r, "/agents/"+st.Name)
}

func (s *server) adopt(w http.ResponseWriter, r *http.Request) {
	req := core.AdoptRequest{Dir: field(r, "dir"), Slug: field(r, "slug"), Runtime: core.Runtime{
		Harness: core.Harness(field(r, "harness")), Model: field(r, "model"), Permissions: core.Permissions(field(r, "permissions")),
	}}
	st, err := s.svc.Adopt(r.Context(), req)
	if err != nil {
		s.newFailed(w, r, err, func(v *newView) { v.Adopt, v.AError = req, humanize(err, "Set a different slug.") })
		return
	}
	s.redirect(w, r, "/agents/"+st.Name)
}

// newFailed re-renders the forms with what was typed and the error.
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

// humanize turns a service error into a sentence for a form, with a hint for
// a name clash.
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
