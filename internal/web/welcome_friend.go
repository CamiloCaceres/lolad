package web

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/setup"
)

// "Got an invite from a friend?", offered in two places when the service
// makes friend links, with the same preview → connect flow as the Sharing
// page (friends.go):
//
//   - on the done screen, as the optional last step of setup;
//   - on the first screen, as "I just want to chat with a friend's
//     assistant" (step "invite"): for someone with no Claude at all. They
//     paste the link and their name, connect, and land in the chat with
//     the friend's assistant. That counts as set up (setup.FriendOnly), so
//     nothing sends them back to /welcome or asks for Claude; they can add
//     Claude and assistants of their own later.

type welcomeFriend struct {
	On      bool   // the service makes friend links
	From    string // "invite": the friend path from the first screen; "": the done screen
	Preview *InvitePreview
	Token   string
	Agents  []agentPick
	Result  *AcceptResult
	Human   *core.RemoteContact  // whom to message once connected
	Theirs  []core.RemoteContact // their assistants we can now talk to
	Error   string
}

// fromInvite is the friend path's marker in forms and the screen it starts on.
const fromInvite = "invite"

// back is the screen an error or "Not now" returns to.
func (f welcomeFriend) back() string {
	if f.From == fromInvite {
		return fromInvite
	}
	return "done"
}

func (s *server) welcomeFriendRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /welcome/friend", s.welcomeFriendPreview)
	m.HandleFunc("POST /welcome/friend/accept", s.welcomeFriendAccept)
}

func (s *server) welcomeFriendRender(w http.ResponseWriter, r *http.Request, status int, step string, set func(*welcomeFriend)) {
	v, err := s.welcomeView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Step = step
	v.Friend.On = true
	if r.FormValue("from") == fromInvite {
		v.Friend.From = fromInvite
	}
	if agents, err := s.list(r.Context()); err == nil {
		for _, a := range agents {
			v.Friend.Agents = append(v.Friend.Agents, agentPick{Name: a.Name, Display: cmp.Or(a.Display, a.Name)})
		}
	}
	set(&v.Friend)
	s.welcomeRender(w, r, status, v)
}

func (s *server) welcomeFriendPreview(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if s.setup == nil || !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	back := welcomeFriend{From: r.FormValue("from")}.back()
	link := readLink(r)
	if link == "" {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, back, func(f *welcomeFriend) {
			f.Error = "Paste the link your friend sent you first."
		})
		return
	}
	pv, err := f.PreviewFriendInvite(r.Context(), link)
	if err != nil {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, back, func(f *welcomeFriend) {
			f.Error = userMessage(err, "That link can’t be used. Ask your friend to send it again.")
		})
		return
	}
	if back == fromInvite && pv.AlreadyConnected {
		s.friendOnly(r) // came back to the friend path: they're set up already
	}
	if back == fromInvite {
		// Their friend sees this name on everything they send.
		if name := field(r, "name"); name != "" {
			if err := s.saveUserName(r, name); err != nil {
				s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, back, func(f *welcomeFriend) { f.Error = humanize(err, "") })
				return
			}
		}
	}
	tok := s.friendLinks.put(link)
	s.welcomeFriendRender(w, r, http.StatusOK, "friend", func(f *welcomeFriend) { f.Preview, f.Token = &pv, tok })
}

func (s *server) welcomeFriendAccept(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if s.setup == nil || !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	tok := r.FormValue("token")
	back := welcomeFriend{From: r.FormValue("from")}.back()
	link, ok := s.friendLinks.get(tok)
	if !ok {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, back, func(f *welcomeFriend) {
			f.Error = "That took a while, so the link was cleared for safety. Paste it again."
		})
		return
	}
	agents, err := s.pickedAgents(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := f.AcceptFriendInvite(r.Context(), link, AcceptRequest{Agents: agents})
	if err != nil {
		pv, perr := f.PreviewFriendInvite(r.Context(), link)
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, "friend", func(wf *welcomeFriend) {
			wf.Error = userMessage(err, "Could not connect. Ask your friend for a new link and try again.")
			if perr == nil {
				wf.Preview, wf.Token = &pv, tok
			}
		})
		return
	}
	s.friendLinks.drop(tok)
	// Connected with a friend (or about to be, once they're online) is set
	// up for someone who hasn't finished setting up assistants of their own.
	s.friendOnly(r)
	var human *core.RemoteContact
	var theirs []core.RemoteContact
	if res.State == FriendConnected {
		if contacts, err := s.svc.RemoteContacts(r.Context(), s.svc.Me().Name); err == nil {
			for i := range contacts {
				switch c := contacts[i]; {
				case c.Peer != res.Connection.Provider:
				case c.Kind == "human" && human == nil:
					human = &contacts[i]
				case c.Kind == "agent":
					theirs = append(theirs, c)
				}
			}
		}
	}
	// The friend path ends in the chat with their assistant.
	if back == fromInvite && len(theirs) == 1 {
		http.Redirect(w, r, "/inbox/"+url.PathEscape(theirs[0].Address), http.StatusSeeOther)
		return
	}
	s.welcomeFriendRender(w, r, http.StatusOK, "friend-done", func(f *welcomeFriend) { f.Result, f.Human, f.Theirs = &res, human, theirs })
}

// friendOnly marks setup done for someone connected with a friend who hasn't
// set up assistants of their own.
func (s *server) friendOnly(r *http.Request) {
	if st, err := s.welcomeStatus(r.Context()); err == nil && !st.Done {
		yes := true
		if _, err := s.setup.Save(r.Context(), setup.SaveRequest{FriendOnly: &yes}); err != nil {
			s.log.Warn("welcome friend", "err", err)
		}
	}
}

// saveUserName stores what the user wants to be called. The daemon owns
// org.yaml while it runs, so it goes through the service.
func (s *server) saveUserName(r *http.Request, name string) error {
	if len(name) > 100 || strings.ContainsAny(name, "\n\r\x00") {
		return fmt.Errorf("%w: that name is too long or has a line break in it", core.ErrInvalid)
	}
	org, err := s.svc.Org(r.Context())
	if err != nil {
		return err
	}
	if org.User.Display == name {
		return nil
	}
	org.User.Display = name
	if err := s.svc.SaveOrg(r.Context(), org); err != nil {
		return err
	}
	yes := true
	_, err = s.setup.Save(r.Context(), setup.SaveRequest{UserName: &name, NameSaved: &yes})
	return err
}
