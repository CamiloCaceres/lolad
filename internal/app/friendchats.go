package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
)

// Friend chats: the first message someone on a connected lolad sends one of
// the user's agents. The user never sees that conversation in their own
// inbox, so lolad records it once per (friend, agent) pair, tells the user
// (an event and the ntfy push) and shows it on Home until dismissed.

var _ web.FriendChats = (*App)(nil)

const friendChatsFile = "friend-chats.json"

// friendChatLimit bounds the file: the oldest dismissed pairs go first.
const friendChatLimit = 500

type friendChats struct {
	mu   sync.Mutex
	path string
}

func (a *App) friendChatStore() *friendChats {
	a.friendOnce.Do(func() { a.friends = &friendChats{path: filepath.Join(a.Config.StateDir, friendChatsFile)} })
	return a.friends
}

func (f *friendChats) load() ([]web.FriendChat, error) {
	b, e := os.ReadFile(f.path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v []web.FriendChat
	return v, json.Unmarshal(b, &v)
}

func (f *friendChats) save(v []web.FriendChat) error {
	if n := len(v) - friendChatLimit; n > 0 {
		kept := v[:0]
		for _, c := range v {
			if n > 0 && c.Seen {
				n--
				continue
			}
			kept = append(kept, c)
		}
		v = kept
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return writeFileAtomic(f.path, b)
}

// friendChat runs for every mail that reaches a mailbox. Mail from another
// lolad to one of our agents starts a friend chat the first time that pair
// talks, unless the user already approved the sender by hand (they've seen
// it) or the pair has talked before (existing installs).
func (a *App) friendChat(m core.Mail) {
	if m.Remote == "" || !core.IsRemoteName(m.FromName) || m.ToName == a.Me().Name || core.IsRemoteName(m.ToName) {
		return
	}
	agent, e := a.Registry.Get(m.ToName)
	if e != nil {
		return
	}
	store := a.friendChatStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, e := store.load()
	if e != nil {
		a.RecordEvent(core.Event{Kind: "error", Detail: "friend chats: " + e.Error()})
		return
	}
	if slices.ContainsFunc(list, func(c web.FriendChat) bool {
		return c.Peer == m.Remote && c.Remote == m.FromName && c.Local == m.ToName
	}) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := web.FriendChat{Peer: m.Remote, Remote: m.FromName, Local: m.ToName, Subject: oneLine(m.Subject), At: m.CreatedAt}
	if s, ok := a.Engine.Sender(ctx, m.FromName); ok {
		c.RemoteDisplay, c.PeerDisplay, c.Kind = s.Display, s.PeerDisplay, s.Kind
	}
	if grants, e := a.Engine.Grants(ctx); e == nil && slices.ContainsFunc(grants, func(g core.Grant) bool {
		return g.Peer == m.Remote && g.Remote == m.FromName && g.Local == m.ToName && g.Origin == "approved"
	}) {
		c.Seen = true
	}
	if earlier, e := a.Provider.List(ctx, m.ToName, core.MailFilter{Box: "all", Peer: m.FromName, Limit: 2}); e == nil && slices.ContainsFunc(earlier, func(o core.Mail) bool { return o.ID != m.ID }) {
		c.Seen = true
	}
	if e = store.save(append(list, c)); e != nil {
		a.RecordEvent(core.Event{Kind: "error", Detail: "friend chats: " + e.Error()})
		return
	}
	if c.Seen {
		return
	}
	who := cmp.Or(c.RemoteDisplay, c.Remote)
	title := who + " from " + cmp.Or(c.PeerDisplay, c.Peer) + "’s lolad started talking to " + cmp.Or(agent.Display, agent.Name) + ": " + c.Subject
	a.RecordEvent(core.Event{Agent: m.ToName, Kind: "federation", Detail: title})
	a.push(title, "friend chat notification")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// push sends title to the user's ntfy topic, if any. what names the
// notification in error events (never the remote-supplied text).
func (a *App) push(title, what string) {
	u := a.Me()
	if u.Ntfy == "" {
		return
	}
	title = oneLine(title)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Ntfy, strings.NewReader(title))
	if err != nil {
		return
	}
	req.Header.Set("Title", title)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.RecordEvent(core.Event{Kind: "error", Detail: what + " failed"})
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		a.RecordEvent(core.Event{Kind: "error", Detail: what + " rejected"})
	}
}

// FriendChats lists friend chats the user hasn't dismissed, newest first.
func (a *App) FriendChats(context.Context) ([]web.FriendChat, error) {
	store := a.friendChatStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, e := store.load()
	if e != nil {
		return nil, e
	}
	out := slices.DeleteFunc(list, func(c web.FriendChat) bool {
		if c.Seen {
			return true
		}
		_, e := a.Registry.Get(c.Local) // the agent was removed since
		return e != nil
	})
	slices.SortFunc(out, func(x, y web.FriendChat) int { return y.At.Compare(x.At) })
	return out, nil
}

// DismissFriendChat hides one friend chat from Home. Unknown pairs are fine.
func (a *App) DismissFriendChat(_ context.Context, peer, remote, local string) error {
	store := a.friendChatStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, e := store.load()
	if e != nil {
		return e
	}
	changed := false
	for i := range list {
		if list[i].Peer == peer && list[i].Remote == remote && list[i].Local == local && !list[i].Seen {
			list[i].Seen, changed = true, true
		}
	}
	if !changed {
		return nil
	}
	if e = store.save(list); e != nil {
		return e
	}
	a.RecordEvent(core.Event{Agent: local, Kind: "federation", Detail: "friend chat dismissed"})
	return nil
}
