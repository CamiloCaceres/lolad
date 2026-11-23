package web

import "net/http"

// appSettingsView is the Settings hub: the app-wide settings that used to be
// separate nav items (email, devices, traffic) plus "Advanced".
type appSettingsView struct {
	Remote  bool // device pairing is available (daemon with auth)
	Version string
	Setup   bool // onboarding is available: link back to it for AI tools
}

func (s *server) appSettings(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, "appsettings", layout{
		Title: "Settings",
		Nav:   "settings",
		Data:  appSettingsView{Remote: s.auth != nil, Version: s.version, Setup: s.setup != nil},
	})
}
