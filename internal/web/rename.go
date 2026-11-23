package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Rename this lolad (Settings › Your Lolad's name). The service does the work
// (core/rename.go, SPEC "Rename"): it checks the name, says what a rename
// ends, and renames at its next start. lolad restarts, so the page waits for
// it to come back, then says how it went.

// fixRename is the refusal fix that offers this screen ("Rename yours?").
const fixRename = "rename_lolad"

// fixOf is the action a refusal offers, "" for none.
func fixOf(err error) string {
	var f interface{ Fix() string }
	if errors.As(err, &f) {
		return f.Fix()
	}
	return ""
}

type renameView struct {
	core.LoladName
	Me      string // the user's address name, for "ana@milo.lolad.local"
	Typed   string // what was typed in the field
	Check   *core.NameCheck
	Error   string
	Outbox  int  // messages on the way: the rename needs "Rename anyway"
	Waiting bool // renaming: lolad is restarting
	Manual  bool // renaming, but the user has to restart lolad
	To      string
	Done    bool // back from a rename: show how it went
}

func (s *server) renamer() (core.LoladRenamer, bool) {
	r, ok := s.svc.(core.LoladRenamer)
	return r, ok
}

func (s *server) renameRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /settings/name", s.renamePage)
	m.HandleFunc("GET /settings/name/check", s.renameCheck)
	m.HandleFunc("POST /settings/name", s.renamePost)
	m.HandleFunc("GET /settings/name/wait", s.renameWait)
}

func (s *server) renameView(r *http.Request) (renameView, bool, error) {
	rn, ok := s.renamer()
	if !ok {
		return renameView{}, false, nil
	}
	n, err := rn.LoladName(r.Context())
	if err != nil {
		return renameView{}, true, err
	}
	return renameView{LoladName: n, Me: s.svc.Me().Name}, true, nil
}

func (s *server) renameRender(w http.ResponseWriter, r *http.Request, status int, v renameView) {
	s.page(w, r, status, "rename", layout{Title: "Your Lolad's name", Nav: "settings", Data: v})
}

func (s *server) renamePage(w http.ResponseWriter, r *http.Request) {
	v, ok, err := s.renameView(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Done = r.URL.Query().Get("done") == "1"
	if v.Pending != nil { // a rename is waiting for lolad's restart
		v.Waiting, v.To = true, v.Pending.To
		v.Manual = v.Restart == "manual"
	}
	s.renameRender(w, r, http.StatusOK, v)
}

// renameCheck is the field's live answer.
func (s *server) renameCheck(w http.ResponseWriter, r *http.Request) {
	rn, ok := s.renamer()
	if !ok {
		s.notFound(w, r)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		s.fragment(w, http.StatusOK, "rename-check", nil)
		return
	}
	c, err := rn.CheckLoladName(r.Context(), name)
	if err != nil {
		s.fragment(w, http.StatusOK, "rename-check", &core.NameCheck{Message: userMessage(err, "lolad couldn’t check that name. Try again.")})
		return
	}
	s.fragment(w, http.StatusOK, "rename-check", &c)
}

func (s *server) renamePost(w http.ResponseWriter, r *http.Request) {
	rn, ok := s.renamer()
	if !ok {
		s.notFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	name := strings.TrimSpace(r.FormValue("name"))
	v, _, err := s.renameView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Typed = name
	if name == "" {
		v.Error = "Type the new name first."
		s.renameRender(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if c, err := rn.CheckLoladName(r.Context(), name); err == nil && !c.OK {
		v.Check, v.Error = &c, c.Message
		s.renameRender(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	acc, err := rn.RenameLolad(r.Context(), core.RenameRequest{Name: name, Anyway: r.FormValue("anyway") == "1"})
	if err != nil {
		code := statusFor(err)
		v.Error = userMessage(err, humanize(err, "Rename didn’t start. Nothing changed."))
		var ob interface{ Outbox() int }
		if errors.Is(err, core.ErrConflict) {
			// Mail still on the way: the list below says so, and the
			// button becomes "Rename anyway". No separate error.
			v.Outbox, v.Error = max(v.Outbox, 1), ""
			if errors.As(err, &ob) {
				v.Outbox = ob.Outbox()
			}
		}
		s.renameRender(w, r, code, v)
		return
	}
	v.Waiting, v.To, v.Manual = true, acc.Name, !acc.Restarting
	s.renameRender(w, r, http.StatusAccepted, v)
}

// renameWait is polled while lolad restarts. While it's down the poll fails
// and htmx tries again; once a rename is no longer pending, the page reloads
// to show how it went.
func (s *server) renameWait(w http.ResponseWriter, r *http.Request) {
	v, ok, err := s.renameView(r)
	if !ok || err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if v.Pending != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/name?done=1")
	w.WriteHeader(http.StatusNoContent)
}
