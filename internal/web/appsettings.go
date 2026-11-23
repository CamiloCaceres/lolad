package web

import (
	"net/http"

	"github.com/CamiloCaceres/lolad/internal/setup"
)

// appSettingsView is the Settings hub: the app-wide settings that used to be
// separate nav items (email, devices, traffic) plus "Advanced".
type appSettingsView struct {
	Remote  bool // device pairing is available (daemon with auth)
	Version string
	Setup   bool // onboarding is available: link back to it for AI tools

	// From setup, when it is available (App-Settings board).
	Claude    string // Connected | Not signed in | Not installed | "" unknown
	Service   setup.ServiceStatus
	AgentsDir string
	Error     string

	Name *struct {
		Name, Provider, Me string
	} // this Lolad's name, when the service can rename it
}

func (s *server) appSettingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /settings", s.appSettings)
	m.HandleFunc("POST /settings/service", s.appSettingsService)
}

func (s *server) appSettingsData(r *http.Request) appSettingsView {
	v := appSettingsView{Remote: s.auth != nil, Version: s.version, Setup: s.setup != nil}
	if rn, ok := s.renamer(); ok {
		if n, err := rn.LoladName(r.Context()); err == nil {
			v.Name = &struct{ Name, Provider, Me string }{n.Name, n.Provider, s.svc.Me().Name}
		}
	}
	if s.setup == nil {
		return v
	}
	st, err := s.setup.Status(r.Context())
	if err != nil {
		return v
	}
	v.Service, v.AgentsDir = st.Service, st.AgentsDir
	if h, ok := st.Harness("claude"); ok {
		switch {
		case h.Ready:
			v.Claude = "Connected"
		case h.Installed:
			v.Claude = "Not signed in"
		default:
			v.Claude = "Not installed"
		}
	}
	return v
}

func (s *server) appSettings(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, "appsettings", layout{Title: "Settings", Nav: "settings", Data: s.appSettingsData(r)})
}

// appSettingsService turns on "Keep lolad running" (start at login).
func (s *server) appSettingsService(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.setup.InstallService(r.Context()); err != nil {
		v := s.appSettingsData(r)
		v.Error = humanize(err, "Run lolad service install once in a terminal instead.")
		s.page(w, r, statusFor(err), "appsettings", layout{Title: "Settings", Nav: "settings", Data: v})
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
