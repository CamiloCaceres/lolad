package web

import (
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/mailprov"
	"net/http"
	"strings"
)

type emailView struct {
	Fleet          mailprov.Fleet
	Message, Error string
}

func (s *server) emailPage(w http.ResponseWriter, r *http.Request) { s.renderEmail(w, r, "", nil) }
func (s *server) renderEmail(w http.ResponseWriter, r *http.Request, message string, err error) {
	v := emailView{Message: message}
	if err != nil {
		v.Error = humanize(err, "")
	}
	if svc, ok := s.svc.(mailprov.Service); ok {
		f, e := svc.EmailFleet(r.Context())
		v.Fleet = f
		if e != nil {
			v.Error = "Email settings are unavailable. Try again."
		}
	} else {
		v.Error = "Mailbox provisioning is unavailable in this service."
	}
	s.page(w, r, http.StatusOK, "email", layout{Title: "Email", Data: v})
}
func (s *server) emailProviderSave(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc.(mailprov.Service)
	if !ok {
		s.fail(w, r, fmt.Errorf("mailbox provisioning unavailable"))
		return
	}
	_ = r.ParseForm()
	u := mailprov.ProviderUpdate{Settings: mailprov.Settings{Provider: r.FormValue("provider"), Domain: field(r, "domain"), Pattern: field(r, "pattern"), Watch: r.FormValue("watch"), Operators: strings.FieldsFunc(r.FormValue("operators"), func(c rune) bool { return c == ',' || c == '\n' || c == ' ' || c == '\r' })}, Token: r.FormValue("token")}
	_, e := svc.EmailProvider(r.Context(), &u)
	s.renderEmail(w, r, "Provider settings saved.", e)
}
func (s *server) emailAction(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.svc.(mailprov.Service)
	if !ok {
		s.fail(w, r, fmt.Errorf("mailbox provisioning unavailable"))
		return
	}
	_ = r.ParseForm()
	q := mailprov.Request{Watch: r.FormValue("watch"), Address: field(r, "address"), DeleteMailbox: r.FormValue("delete_mailbox") == "yes", Confirm: field(r, "confirm")}
	var result mailprov.Result
	var e error
	switch r.PathValue("op") {
	case "provision":
		result, e = svc.ProvisionEmail(r.Context(), r.PathValue("name"), q)
	case "rotate":
		result, e = svc.RotateEmail(r.Context(), r.PathValue("name"), q)
	case "detach":
		result, e = svc.DetachEmail(r.Context(), r.PathValue("name"), q)
	case "test":
		t, err := svc.CheckEmail(r.Context(), r.PathValue("name"), q.Address)
		e = err
		result.Test = &t
		result.Message = "Connection test completed."
	default:
		e = fmt.Errorf("unknown email action")
	}
	if result.Test != nil {
		if result.Test.OK {
			result.Message += " IMAP and SMTP logins passed."
		} else {
			result.Message += " Connection needs attention: " + result.Test.Detail
		}
	}
	if r.FormValue("from") == "settings" {
		s.settled(w, r, "email", result.Message, e, nil)
		return
	}
	s.renderEmail(w, r, result.Message, e)
}
