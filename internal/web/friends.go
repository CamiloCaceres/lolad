package web

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Friend connect on the Sharing page and at the end of onboarding
// (docs/friend-connect-design.md). One link: "Invite a friend" makes it,
// "Got an invite?" previews it, then "Connect with Ana" accepts it.
//
// The link is a secret. It is only ever in POST bodies. Between preview and
// connect it waits server-side under a random token (friendLinks), so it is
// never written back into a page, a URL or a log.

type agentPick struct {
	Name, Display string
	Checked       bool
}

// friendLinks holds pasted links between "Continue" and "Connect".
type friendLinks struct {
	mu sync.Mutex
	m  map[string]stashedLink
}

type stashedLink struct {
	link    string
	expires time.Time
}

const friendLinkTTL = 30 * time.Minute

func (f *friendLinks) put(link string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]stashedLink{}
	}
	for k, v := range f.m {
		if now.After(v.expires) {
			delete(f.m, k)
		}
	}
	if len(f.m) >= 64 { // a handful of open previews at most; drop the oldest
		var oldest string
		for k, v := range f.m {
			if oldest == "" || v.expires.Before(f.m[oldest].expires) {
				oldest = k
			}
		}
		delete(f.m, oldest)
	}
	f.m[tok] = stashedLink{link: link, expires: now.Add(friendLinkTTL)}
	return tok
}

func (f *friendLinks) get(tok string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[tok]
	if !ok || time.Now().After(v.expires) {
		delete(f.m, tok)
		return "", false
	}
	return v.link, true
}

func (f *friendLinks) drop(tok string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, tok)
}

// friendState is what the Sharing page shows for the friend flow.
type friendState struct {
	Agents []agentPick        // this lolad's assistants, for the checkboxes
	Links  []FriendInviteInfo // links made here, newest first
	Labels map[string]string  // agent slug -> display

	// Invite a friend
	For         string        // kept after an error
	Created     *FriendInvite // shown once, in the creation response only
	CreatedFor  string
	InviteError string

	// Got an invite?
	Preview      *InvitePreview
	PreviewToken string
	AcceptError  string
	AcceptFix    string // a refusal's fix: "rename_lolad" offers Rename this lolad
	Waiting      *AcceptResult
}

func (s *server) friendInviter() (FriendInviter, bool) {
	f, ok := s.svc.(FriendInviter)
	return f, ok
}

// userMessage is a refusal written for people, or fallback.
func userMessage(err error, fallback string) string {
	var um interface{ UserMessage() string }
	if errors.As(err, &um) {
		return um.UserMessage()
	}
	return fallback
}

func (s *server) friendData(r *http.Request, v *connectionsView, checked []string) error {
	f, ok := s.friendInviter()
	if !ok {
		return nil
	}
	ctx := r.Context()
	v.Friends = true
	v.Friend.Labels = map[string]string{}
	agents, err := s.list(ctx)
	if err != nil {
		return err
	}
	for _, a := range agents {
		d := cmp.Or(a.Display, a.Name)
		v.Friend.Labels[a.Name] = d
		v.Friend.Agents = append(v.Friend.Agents, agentPick{Name: a.Name, Display: d, Checked: slices.Contains(checked, a.Name)})
	}
	links, err := f.FriendInvites(ctx)
	if err != nil {
		return err
	}
	v.Friend.Links = links
	// Newest friend first: someone who just connected is at the top.
	slices.SortStableFunc(v.Connections, func(a, b connectionView) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return nil
}

// friendRender renders the Sharing page with the friend flow in some state.
func (s *server) friendRender(w http.ResponseWriter, r *http.Request, status int, checked []string, set func(*connectionsView)) {
	v, err := s.connectionData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.friendData(r, &v, checked); err != nil {
		s.fail(w, r, err)
		return
	}
	if set != nil {
		set(&v)
	}
	s.page(w, r, status, "connections", layout{Title: "Sharing", Nav: "connections", Data: v})
}

// pickedAgents returns the ticked assistants that exist here, in list order.
func (s *server) pickedAgents(r *http.Request) ([]string, error) {
	_ = r.ParseForm()
	want := r.Form["agent"]
	agents, err := s.list(r.Context())
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, a := range agents {
		if slices.Contains(want, a.Name) {
			out = append(out, a.Name)
		}
	}
	return out, nil
}

func (s *server) friendInvite(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if !ok {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	who := strings.TrimSpace(r.FormValue("for"))
	if len([]rune(who)) > 60 {
		who = string([]rune(who)[:60])
	}
	agents, err := s.pickedAgents(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	inv, err := f.CreateFriendInvite(r.Context(), FriendInviteRequest{Agents: agents, For: who})
	if err != nil {
		s.friendRender(w, r, http.StatusUnprocessableEntity, agents, func(v *connectionsView) {
			v.Friend.For = who
			v.Friend.InviteError = userMessage(err, "Could not make a link right now. Try again in a minute.")
		})
		return
	}
	// The link lives in this response only: never cached, never in history.
	s.friendRender(w, r, http.StatusOK, nil, func(v *connectionsView) {
		v.Friend.Created, v.Friend.CreatedFor = &inv, who
	})
}

func (s *server) friendCancel(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if !ok {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	if err := f.CancelFriendInvite(r.Context(), r.PathValue("id")); err != nil {
		s.friendRender(w, r, statusFor(err), nil, func(v *connectionsView) {
			v.Friend.InviteError = userMessage(err, "Could not cancel that link. It may have been used already.")
		})
		return
	}
	s.redirect(w, r, "/connections")
}

// readLink takes the pasted link from the form. It is never echoed.
func readLink(r *http.Request) string {
	return strings.TrimSpace(r.FormValue("link"))
}

func (s *server) friendPreview(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if !ok {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	link := readLink(r)
	if link == "" {
		s.friendRender(w, r, http.StatusUnprocessableEntity, nil, func(v *connectionsView) {
			v.Friend.AcceptError = "Paste the link your friend sent you first."
		})
		return
	}
	pv, err := f.PreviewFriendInvite(r.Context(), link)
	if err != nil {
		s.friendRender(w, r, http.StatusUnprocessableEntity, nil, func(v *connectionsView) {
			v.Friend.AcceptError = userMessage(err, "That link can’t be used. Ask your friend to send it again.")
			v.Friend.AcceptFix = fixOf(err)
		})
		return
	}
	tok := s.friendLinks.put(link)
	s.friendRender(w, r, http.StatusOK, nil, func(v *connectionsView) {
		v.Friend.Preview, v.Friend.PreviewToken = &pv, tok
	})
}

func (s *server) friendAccept(w http.ResponseWriter, r *http.Request) {
	f, ok := s.friendInviter()
	if !ok {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	tok := r.FormValue("token")
	link, ok := s.friendLinks.get(tok)
	if !ok {
		s.friendRender(w, r, http.StatusUnprocessableEntity, nil, func(v *connectionsView) {
			v.Friend.AcceptError = "That took a while, so the link was cleared for safety. Paste it again."
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
		// Keep the preview on screen so the person sees whose invite failed.
		pv, perr := f.PreviewFriendInvite(r.Context(), link)
		s.friendRender(w, r, http.StatusUnprocessableEntity, agents, func(v *connectionsView) {
			v.Friend.AcceptError = userMessage(err, "Could not connect. Ask your friend for a new link and try again.")
			v.Friend.AcceptFix = fixOf(err)
			if perr == nil {
				v.Friend.Preview, v.Friend.PreviewToken = &pv, tok
			}
		})
		return
	}
	s.friendLinks.drop(tok)
	if res.State == FriendWaiting {
		s.friendRender(w, r, http.StatusOK, nil, func(v *connectionsView) { v.Friend.Waiting = &res })
		return
	}
	s.redirect(w, r, "/connections?connected="+res.Connection.Provider)
}

// friendCard is one connection in plain words.
type friendCard struct {
	Waiting     bool                 // accepted a link while their lolad was offline
	Human       *core.RemoteContact  // their person, when we may write to them
	TheirAgents []core.RemoteContact // their assistants we may write to
	OurAgents   []string             // display names of ours they can reach
}

func cardFor(c core.Connection, contacts []core.RemoteContact, labels map[string]string) friendCard {
	var fc friendCard
	fc.Waiting = c.Waiting
	for i := range contacts {
		rc := contacts[i]
		if rc.Peer != c.Provider {
			continue
		}
		if rc.Kind == "human" {
			if fc.Human == nil {
				fc.Human = &rc
			}
			continue
		}
		fc.TheirAgents = append(fc.TheirAgents, rc)
	}
	for _, sh := range c.Shares {
		fc.OurAgents = append(fc.OurAgents, cmp.Or(labels[sh.Local], sh.Local))
	}
	return fc
}
