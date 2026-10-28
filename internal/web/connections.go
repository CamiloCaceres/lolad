package web

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type shareEditor struct {
	Name, Display string
	Enabled       bool
	Remote        []string
}
type connectionView struct {
	core.Connection
	Editors  []shareEditor
	Contacts []core.RemoteContact
	Grants   []core.Grant
}
type connectionsView struct {
	Info        core.FederationInfo
	Connections []connectionView
	Invite      *core.Invite
	Error       string
}

func (s *server) connectionData(r *http.Request) (connectionsView, error) {
	ctx := r.Context()
	v := connectionsView{Info: s.svc.Info(ctx)}
	cs, err := s.svc.Connections(ctx)
	if err != nil {
		return v, err
	}
	agents, err := s.list(ctx)
	if err != nil {
		return v, err
	}
	contacts, err := s.svc.RemoteContacts(ctx, s.svc.Me().Name)
	if err != nil {
		return v, err
	}
	grants, err := s.svc.Grants(ctx)
	if err != nil {
		return v, err
	}
	for _, c := range cs {
		cv := connectionView{Connection: c}
		for _, a := range agents {
			e := shareEditor{Name: a.Name, Display: cmp.Or(a.Display, a.Name)}
			for _, share := range c.Shares {
				if share.Local == a.Name {
					e.Enabled = true
					e.Remote = share.Remote
				}
			}
			cv.Editors = append(cv.Editors, e)
		}
		for _, rc := range contacts {
			if rc.Peer == c.Provider {
				cv.Contacts = append(cv.Contacts, rc)
			}
		}
		// Keep already approved addresses editable when the peer directory is offline.
		for _, share := range c.Shares {
			for _, address := range share.Remote {
				if !slices.ContainsFunc(cv.Contacts, func(rc core.RemoteContact) bool { return rc.Address == address }) {
					cv.Contacts = append(cv.Contacts, core.RemoteContact{Address: address, Display: address, Peer: c.Provider, Kind: "contact"})
				}
			}
		}
		for _, g := range grants {
			if g.Peer == c.Provider && g.State == core.GrantAllowed {
				cv.Grants = append(cv.Grants, g)
			}
		}
		v.Connections = append(v.Connections, cv)
	}
	return v, nil
}
func (s *server) connectionsPage(w http.ResponseWriter, r *http.Request) {
	v, e := s.connectionData(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	s.page(w, r, 200, "connections", layout{Title: "Connections", Data: v})
}
func (s *server) connectionInvite(w http.ResponseWriter, r *http.Request) {
	invite, e := s.svc.CreateInvite(r.Context())
	if e != nil {
		s.connectionError(w, r, "Could not create an invite. Check this lolad's address and try again.")
		return
	}
	v, e := s.connectionData(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	v.Invite = &invite
	// Never cache or redirect a one-time invite response into browser history.
	s.page(w, r, 200, "connections", layout{Title: "Your invitation", Nav: "connections", Data: v})
}
func (s *server) connectionError(w http.ResponseWriter, r *http.Request, msg string) {
	v, e := s.connectionData(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	v.Error = msg
	s.page(w, r, 422, "connections", layout{Title: "Connections", Data: v})
}
func (s *server) connectionAccept(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	code := r.FormValue("code")
	if strings.TrimSpace(code) == "" {
		s.connectionError(w, r, "Paste the invitation you received first.")
		return
	}
	if _, e := s.svc.AcceptInvite(r.Context(), code); e != nil {
		// Service errors may include parser input. Never echo any part of the code.
		s.connectionError(w, r, "Could not accept that invitation. Ask for a new invitation and try again.")
		return
	}
	s.redirect(w, r, "/connections")
}
func (s *server) connectionUpdate(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	var u core.ConnectionUpdate
	switch r.FormValue("action") {
	case "pause":
		u.Paused = new(true)
	case "resume":
		u.Paused = new(false)
	case "reachability":
		u.UserReachable = new(r.FormValue("reachable") == "yes")
	case "shares":
		agents, e := s.list(r.Context())
		if e != nil {
			s.fail(w, r, e)
			return
		}
		contacts, e := s.svc.RemoteContacts(r.Context(), s.svc.Me().Name)
		if e != nil {
			s.fail(w, r, e)
			return
		}
		allowed := map[string]bool{}
		for _, c := range contacts {
			if c.Peer == p {
				allowed[c.Address] = true
			}
		}
		connections, e := s.svc.Connections(r.Context())
		if e != nil {
			s.fail(w, r, e)
			return
		}
		for _, c := range connections {
			if c.Provider == p {
				for _, sh := range c.Shares {
					for _, addr := range sh.Remote {
						allowed[addr] = true
					}
				}
			}
		}
		_ = r.ParseForm()
		u.Shares = []core.Share{}
		for _, a := range agents {
			if r.FormValue("share_"+a.Name) != "yes" {
				continue
			}
			sh := core.Share{Local: a.Name}
			mode := r.FormValue("mode_" + a.Name)
			if mode != "anyone" && mode != "specific" {
				s.fail(w, r, fmt.Errorf("%w: choose who may reach each shared agent", core.ErrInvalid))
				return
			}
			if mode == "specific" {
				for _, addr := range r.Form["remote_"+a.Name] {
					if !allowed[addr] {
						s.fail(w, r, fmt.Errorf("%w: choose a contact on this connection", core.ErrInvalid))
						return
					}
					if !slices.Contains(sh.Remote, addr) {
						sh.Remote = append(sh.Remote, addr)
					}
				}
				if len(sh.Remote) == 0 {
					s.fail(w, r, fmt.Errorf("%w: choose at least one pre-approved person", core.ErrInvalid))
					return
				}
			}
			u.Shares = append(u.Shares, sh)
		}
	default:
		s.fail(w, r, core.ErrInvalid)
		return
	}
	if _, e := s.svc.UpdateConnection(r.Context(), p, u); e != nil {
		s.fail(w, r, e)
		return
	}
	s.redirect(w, r, "/connections")
}
func (s *server) connectionRemove(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("confirm") != "remove" {
		s.fail(w, r, fmt.Errorf("%w: confirm removal first", core.ErrInvalid))
		return
	}
	if e := s.svc.RemoveConnection(r.Context(), r.PathValue("provider")); e != nil {
		s.fail(w, r, e)
		return
	}
	s.redirect(w, r, "/connections")
}
func (s *server) grantDecide(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("decision")
	if action != "allow" && action != "block" {
		s.fail(w, r, core.ErrInvalid)
		return
	}
	if _, e := s.svc.DecideGrant(r.Context(), r.FormValue("peer"), r.FormValue("remote"), r.FormValue("local"), action == "allow"); e != nil {
		s.fail(w, r, e)
		return
	}
	s.redirect(w, r, "/")
}
func deliveryLabel(state string) string {
	switch state {
	case core.DeliveryQueued:
		return "Queued"
	case core.DeliveryHeld:
		return "Awaiting approval"
	case core.DeliveryDelivered:
		return "Delivered"
	case core.DeliveryBlocked:
		return "Blocked"
	case core.DeliveryFailed:
		return "Failed"
	}
	return state
}
func remoteLabel(name string, cs []core.Connection) string {
	_, p, ok := strings.Cut(name, "@")
	if !ok {
		return ""
	}
	for _, c := range cs {
		if c.Provider == p {
			return cmp.Or(c.Display, p) + "'s lolad"
		}
	}
	return p
}
