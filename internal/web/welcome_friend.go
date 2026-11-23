package web

import (
	"cmp"
	"net/http"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// The optional last onboarding step: "Got an invite from a friend?". It is
// offered on the done screen when the service makes friend links, and uses
// the same preview → connect flow as the Sharing page (friends.go).

type welcomeFriend struct {
	On      bool // the service makes friend links
	Preview *InvitePreview
	Token   string
	Agents  []agentPick
	Result  *AcceptResult
	Human   *core.RemoteContact // whom to message once connected
	Error   string
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
	link := readLink(r)
	if link == "" {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, "done", func(f *welcomeFriend) {
			f.Error = "Paste the link your friend sent you first."
		})
		return
	}
	pv, err := f.PreviewFriendInvite(r.Context(), link)
	if err != nil {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, "done", func(f *welcomeFriend) {
			f.Error = userMessage(err, "That link can’t be used. Ask your friend to send it again.")
		})
		return
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
	link, ok := s.friendLinks.get(tok)
	if !ok {
		s.welcomeFriendRender(w, r, http.StatusUnprocessableEntity, "done", func(f *welcomeFriend) {
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
	var human *core.RemoteContact
	if res.State == FriendConnected {
		if contacts, err := s.svc.RemoteContacts(r.Context(), s.svc.Me().Name); err == nil {
			for i := range contacts {
				if contacts[i].Peer == res.Connection.Provider && contacts[i].Kind == "human" {
					human = &contacts[i]
					break
				}
			}
		}
	}
	s.welcomeFriendRender(w, r, http.StatusOK, "friend-done", func(f *welcomeFriend) { f.Result, f.Human = &res, human })
}
