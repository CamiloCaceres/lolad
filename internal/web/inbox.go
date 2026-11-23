package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type convView struct {
	core.Conversation
	Origin string
	Asks   []msgView         // questions from the peer the user hasn't answered, oldest first
	Agent  *core.AgentStatus // nil when the peer is not an agent (lolad)
	Active bool
	Chat   core.UserChat // of Last
	Mine   bool          // Last was sent by the user
}

type msgView struct {
	core.Mail
	Mine       bool
	Chat       core.UserChat
	Kind       string          // say | update | deliver | ask | decision | reply | system
	Day        string          // set on the first message of a day
	ShowSubj   bool            // show the subject line
	Cont       bool            // continues a run from the same sender
	New        bool            // first message that was unread when the thread was opened
	Answered   bool            // ask: the user has answered (by decision or text); no longer waiting
	Answer     string          // ask: what the user answered (the option, or a summary of their answers)
	Skipped    bool            // ask: the user chose not to answer
	Reply      *core.AskAnswer // ask: the answer, when it came as a decision or an answers mail
	AnsweredAt time.Time       // ask: when it was answered
	Folded     bool            // answers or decision mail shown as its cards' receipts instead
	Superseded bool            // update replaced by the next update from the same sender
	Live       bool            // update that is the newest message in the conversation
	Lines      []answerLine    // answers: each request and what the user said
	Req        *reqView        // open ask: how to answer it, and the staged answer
	Receipt    string          // last message from the user: Sent | Delivered | Read
	Files      []attView
	HideBody   bool // the body only lists the files (a message sent with no text)
	Bare       bool // nothing to show in a bubble: just the files
}

type threadView struct {
	Origin  string
	Arrival string // created or adopted landing message
	Peer    string
	Display string
	Agent   *core.AgentStatus
	Msgs    []msgView
	ReplyTo string // last message from the peer; a typed message answers it
	CanSend bool
	Policy  core.Decision // may the user message this peer
	Error   string
	// Files: Attach is false for peers on another lolad (the provider refuses
	// to send files there yet); AttachNote says why.
	Attach     bool
	AttachNote string
	MaxFiles   int
	MaxBytes   int64
	MaxLabel   string    // "500 MB"
	Tray       *trayView // open requests from the peer, above the composer
}

type inboxView struct {
	Approvals []approvalView
	OOB       bool   // render the list as an out-of-band swap
	Active    string // the open conversation's peer
	Recent    []convView
	Convs     []convView
	Thread    *threadView
	Asks      []askView          // home: questions waiting on the user, newest first
	Trays     []trayView         // home: the same asks grouped by assistant, as request panels
	Unread    []convView         // home: unread conversations without an ask
	Others    []core.AgentStatus // agents with no conversation yet
	Agents    []core.AgentStatus // everyone the user could write to
	Team      []core.AgentStatus // home: this lolad's own assistants, for the orb strip
	Broken    []core.AgentStatus // home: assistants that crashed or can't start
	Blocked   []core.AgentStatus // home: assistants stuck on a permission prompt
	Needs     int                // home: asks + approvals + blocked + broken
	Me        string             // home: the user's display name, for the greeting
}

// homeLine is the one sentence under the greeting: how much needs the user.
func (v inboxView) HomeLine() string {
	switch {
	case len(v.Team) == 0:
		return "Make your first assistant to get started."
	case v.Needs == 0:
		return "Nothing needs you. Everyone is fine."
	case v.Needs == 1 && len(v.Team) > 1:
		return "One thing needs you. Everyone else is fine."
	case v.Needs == 1:
		return "One thing needs you."
	}
	return strconv.Itoa(v.Needs) + " things need you."
}

type approvalView struct {
	core.Grant
	LocalDisplay string
}

type askView struct {
	Conv convView
	Ask  msgView
}

// openAsks finds the questions each peer asked that the user hasn't answered
// yet. Conversation.Pending only looks at the last message; an ask followed by
// a progress update is still waiting. convs must be the request's full list
// (s.conversations): within a GET the answer is kept for the nav badges.
func (s *server) openAsks(ctx context.Context, convs []core.Conversation) map[string][]msgView {
	c := cacheOf(ctx)
	if c != nil && c.asks != nil {
		return c.asks
	}
	me := s.svc.Me().Name
	out := map[string][]msgView{}
	if c != nil {
		defer func() { c.asks = out }()
	}
	for _, c := range convs {
		msgs, err := s.svc.Conversation(ctx, c.Peer, 200, time.Time{})
		if err != nil {
			continue
		}
		v := make([]msgView, len(msgs))
		for i, m := range msgs {
			v[i] = msgView{Mail: m}
		}
		shape(v, me)
		for _, m := range v {
			if !m.Mine && m.Kind == "ask" && !m.Answered {
				out[c.Peer] = append(out[c.Peer], m)
			}
		}
	}
	return out
}

// State is the peer's state, "" when it is not an agent.
func (c convView) State() core.State {
	if c.Agent == nil {
		return ""
	}
	return c.Agent.State
}

func stateOf(a *core.AgentStatus) core.State {
	if a == nil {
		return ""
	}
	return a.State
}

func agentMap(list []core.AgentStatus) map[string]*core.AgentStatus {
	m := make(map[string]*core.AgentStatus, len(list))
	for i := range list {
		m[list[i].Name] = &list[i]
	}
	return m
}

func (s *server) inboxData(ctx context.Context, active string) (inboxView, map[string]*core.AgentStatus, error) {
	v := inboxView{Active: active}
	convs, err := s.conversations(ctx)
	if err != nil {
		return v, nil, err
	}
	list, err := s.list(ctx)
	if err != nil {
		return v, nil, err
	}
	slices.SortFunc(list, func(a, b core.AgentStatus) int { return strings.Compare(a.Name, b.Name) })
	v.Agents = slices.Clone(list)
	v.Team = slices.Clone(list)
	v.Me = cmp.Or(s.svc.Me().Display, s.svc.Me().Name)
	for _, a := range list {
		switch a.State {
		case core.StateFailed, core.StateCrashed:
			v.Broken = append(v.Broken, a)
		case core.StateWaiting:
			v.Blocked = append(v.Blocked, a)
		}
	}
	contacts, err := s.svc.RemoteContacts(ctx, s.svc.Me().Name)
	if err != nil {
		return v, nil, err
	}
	for _, c := range contacts {
		v.Agents = append(v.Agents, core.AgentStatus{Agent: core.Agent{Name: c.Address, Display: cmp.Or(c.Display, c.Name), Role: c.PeerDisplay + "’s lolad · " + c.Kind}})
	}
	grants, err := s.svc.Grants(ctx)
	if err != nil {
		return v, nil, err
	}
	for _, g := range grants {
		if g.State == core.GrantPending {
			name := g.Local
			for _, a := range list {
				if a.Name == g.Local {
					name = cmp.Or(a.Display, a.Name)
				}
			}
			if g.Local == s.svc.Me().Name {
				name = "you"
			}
			v.Approvals = append(v.Approvals, approvalView{Grant: g, LocalDisplay: name})
		}
	}
	connections, err := s.svc.Connections(ctx)
	if err != nil {
		return v, nil, err
	}
	agents := agentMap(list)
	me := s.svc.Me().Name
	asks := s.openAsks(ctx, convs)
	for _, c := range convs {
		cv := convView{Conversation: c, Asks: asks[c.Peer], Agent: agents[c.Peer], Active: c.Peer == active, Chat: userChat(c.Last), Mine: c.Last.FromName == me}
		cv.Origin = remoteLabel(c.Peer, connections)
		for _, rc := range contacts {
			if rc.Address == c.Peer {
				cv.Display = cmp.Or(rc.Display, rc.Name)
			}
		}
		cv.Pending = len(cv.Asks) > 0
		if cv.Agent != nil && cv.Display == "" {
			cv.Display = cv.Agent.Display
		}
		if cv.Display == "" {
			cv.Display = c.Peer
		}
		v.Convs = append(v.Convs, cv)
		for _, a := range cv.Asks {
			v.Asks = append(v.Asks, askView{Conv: cv, Ask: a})
		}
		if !cv.Pending && c.Unread > 0 {
			v.Unread = append(v.Unread, cv)
		}
	}
	slices.SortFunc(v.Convs, func(a, b convView) int {
		rank := func(c convView) int {
			if c.Pending {
				return 0
			}
			if c.Unread > 0 {
				return 1
			}
			return 2
		}
		if n := cmp.Compare(rank(a), rank(b)); n != 0 {
			return n
		}
		return b.LastAt.Compare(a.LastAt)
	})
	for _, c := range v.Convs {
		if !c.Pending && c.Unread == 0 {
			v.Recent = append(v.Recent, c)
		}
	}
	slices.SortFunc(v.Unread, func(a, b convView) int { return b.LastAt.Compare(a.LastAt) })
	slices.SortFunc(v.Asks, func(a, b askView) int { return b.Ask.CreatedAt.Compare(a.Ask.CreatedAt) })
	if active == "" { // only Home shows the request panels
		v.Trays = s.homeTrays(ctx, v.Asks)
	}
	// An agent stuck on a permission prompt that also asked in mail is shown
	// once, by its ask.
	asked := map[string]bool{}
	for _, a := range v.Asks {
		asked[a.Conv.Peer] = true
	}
	v.Blocked = slices.DeleteFunc(v.Blocked, func(a core.AgentStatus) bool { return asked[a.Name] })
	v.Needs = len(v.Asks) + len(v.Approvals) + len(v.Blocked) + len(v.Broken)
	return v, agents, nil
}

func (s *server) inbox(w http.ResponseWriter, r *http.Request) {
	if s.welcomeRedirect(w, r) { // first run: onboarding, until the user has agents or leaves
		return
	}
	v, _, err := s.inboxData(r.Context(), "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "inbox", layout{Title: "Inbox", Class: "is-inbox", Data: v})
}

// loadThread reads the conversation, marks the peer's mail to the user read,
// and builds the view.
func (s *server) loadThread(ctx context.Context, peer string, agents map[string]*core.AgentStatus) (*threadView, error) {
	return s.loadThreadWith(ctx, peer, agents, nil)
}

// loadThreadWith is loadThread showing what was posted with refused answers.
func (s *server) loadThreadWith(ctx context.Context, peer string, agents map[string]*core.AgentStatus, edits map[string]reqEdit) (*threadView, error) {
	msgs, err := s.svc.Conversation(ctx, peer, 200, time.Time{})
	if err != nil {
		return nil, err
	}
	a := agents[peer]
	var contact *core.RemoteContact
	if core.IsRemoteName(peer) {
		cs, err := s.svc.RemoteContacts(ctx, s.svc.Me().Name)
		if err != nil {
			return nil, err
		}
		for i := range cs {
			if cs[i].Address == peer {
				contact = &cs[i]
				break
			}
		}
	}
	if a == nil && contact == nil && len(msgs) == 0 {
		return nil, fmt.Errorf("no agent or conversation %q: %w", peer, core.ErrNotFound)
	}
	me := s.svc.Me().Name
	t := &threadView{Peer: peer, Display: peer, Agent: a, CanSend: a != nil}
	if a != nil && a.Display != "" {
		t.Display = a.Display
	}
	if core.IsRemoteName(peer) {
		t.CanSend = true
		cs, _ := s.svc.Connections(ctx)
		t.Origin = remoteLabel(peer, cs)
		if contact != nil {
			t.Display = cmp.Or(contact.Display, contact.Name)
		}
	}
	if a != nil || core.IsRemoteName(peer) {
		t.Policy = s.svc.Check(ctx, me, peer)
	}
	marked := false
	for _, m := range msgs {
		if m.ToName == me && m.Status != core.MailRead {
			if err := s.svc.MarkRead(ctx, m.ID); err != nil {
				s.log.Warn("mark read", "id", m.ID, "err", err)
			}
			if !marked {
				marked = true
				t.Msgs = append(t.Msgs, msgView{Mail: m, New: true})
				continue
			}
		}
		t.Msgs = append(t.Msgs, msgView{Mail: m})
	}
	shape(t.Msgs, me)
	for i := range t.Msgs {
		m := &t.Msgs[i]
		m.Files = attViews(peer, m.Attachments)
		m.HideBody = len(m.Attachments) > 0 && (strings.TrimSpace(m.Body) == "" || m.Body == filesBody(attachmentNames(m.Attachments)))
		m.Bare = m.HideBody && !m.ShowSubj && (m.Kind == "say" || m.Kind == "reply")
	}
	t.MaxFiles, t.MaxBytes = maxFiles, cmp.Or(s.maxUpload, DefaultMaxUpload)
	t.MaxLabel = sizeLimit(t.MaxBytes)
	t.Attach = !core.IsRemoteName(peer)
	if !t.Attach {
		t.AttachNote = noRemoteFiles
	}
	// A typed message never answers a request: it replies to the peer's last
	// message that isn't an open ask.
	for i := len(t.Msgs) - 1; i >= 0; i-- {
		if m := t.Msgs[i]; !m.Mine && (m.Kind != "ask" || m.Answered) {
			t.ReplyTo = m.ID
			break
		}
	}
	tray := s.buildTray(ctx, peer, t.Display, stateOf(a), openOf(t.Msgs), false, edits)
	t.Tray = &tray
	for i := range tray.Reqs {
		if j := slices.IndexFunc(t.Msgs, func(m msgView) bool { return m.ID == tray.Reqs[i].Ask.ID }); j >= 0 {
			t.Msgs[j].Req = &tray.Reqs[i]
		}
	}
	return t, nil
}

func baseSubject(s string) string {
	for {
		t := strings.TrimSpace(s)
		if len(t) >= 3 && strings.EqualFold(t[:3], "re:") {
			s = t[3:]
			continue
		}
		return t
	}
}

// shape fills the presentation fields of a thread, oldest first.
func shape(msgs []msgView, me string) {
	lastDay, lastSubject := "", ""
	for i := range msgs {
		m := &msgs[i]
		m.Mine = m.FromName == me
		m.Chat = userChat(m.Mail)
		m.Kind = m.Chat.Kind
		if m.Kind == "" {
			m.Kind = "say"
			if m.FromName == "lolad" || m.Type == "system" {
				m.Kind = "system"
			}
		}
		day := m.CreatedAt.Local().Format("2006-01-02")
		if day != lastDay {
			m.Day = dayLabel(m.CreatedAt)
			lastDay = day
		}
		subj := subject(m.Mail)
		m.ShowSubj = !m.Mine && subj != "" && (subj != lastSubject || m.Kind == "ask" || m.Kind == "deliver") && !restates(subj, m.Body)
		lastSubject = subj
		if i > 0 && m.Day == "" && !m.New {
			p := msgs[i-1]
			m.Cont = p.FromName == m.FromName && m.CreatedAt.Sub(p.CreatedAt) < 10*time.Minute && !m.ShowSubj
		}
	}
	for i := range msgs {
		if msgs[i].Mine && msgs[i].Remote != "" {
			msgs[i].Receipt = deliveryLabel(msgs[i].Delivery)
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Mine {
			if msgs[i].Remote != "" {
				break
			}
			msgs[i].Receipt = map[core.MailStatus]string{core.MailUnread: "Sent", core.MailNotified: "Delivered", core.MailRead: "Read"}[msgs[i].Status]
			break
		}
	}
	closeAsks(msgs)
	answerLines(msgs)
	foldAnswers(msgs)
	foldUpdates(msgs)
}

// updMore reports whether an update has more than its one line (the start
// of its body): blocks, files, or a body longer than the line shows.
func updMore(m msgView) bool {
	if len(m.Chat.Blocks) > 0 || len(m.Files) > 0 {
		return true
	}
	return !m.HideBody && len([]rune(strings.Join(strings.Fields(m.Body), " "))) > 140
}

// foldUpdates keeps one line per run of updates: each update from a sender
// that is followed directly by another update from the same sender is
// superseded by it. The newest message, if it's an update, is Live.
func foldUpdates(msgs []msgView) {
	for i := range msgs {
		m := &msgs[i]
		if m.Mine || m.Kind != "update" {
			continue
		}
		if i+1 < len(msgs) {
			n := msgs[i+1]
			m.Superseded = !n.Mine && n.Kind == "update" && n.FromName == m.FromName && n.Day == ""
		} else {
			m.Live = true
		}
	}
}

func dayLabel(t time.Time) string {
	switch w := when(t); {
	case strings.Contains(w, ":"):
		return "Today"
	case w == "Yesterday":
		return w
	}
	return t.Local().Format("Monday, 2 January")
}

func (s *server) thread(w http.ResponseWriter, r *http.Request) {
	peer := r.PathValue("peer")
	list, err := s.list(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.loadThread(r.Context(), peer, agentMap(list))
	if errors.Is(err, core.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	// Read after loadThread marked the mail read, so the list agrees.
	v, _, err := s.inboxData(r.Context(), peer)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if r.URL.Query().Get("created") == "1" {
		t.Arrival = t.Display + " is ready"
	} else if r.URL.Query().Get("adopted") == "1" {
		t.Arrival = t.Display + " was added"
	}
	v.Thread = t
	s.page(w, r, http.StatusOK, "inbox", layout{Title: t.Display, Class: "is-inbox has-thread", Data: v})
}

// messages is the live refresh of an open thread. It answers with the
// message list plus out-of-band updates of the conversation list and nav.
func (s *server) messages(w http.ResponseWriter, r *http.Request) {
	s.renderThread(w, r, r.PathValue("peer"), "", http.StatusOK)
}

func (s *server) renderThread(w http.ResponseWriter, r *http.Request, peer, errMsg string, code int) {
	s.renderThreadFull(w, r, peer, code, errMsg, nil, "")
}

// renderThreadWith refreshes the thread after a request action.
func (s *server) renderThreadWith(w http.ResponseWriter, r *http.Request, peer string, code int, edits map[string]reqEdit, barErr string) {
	s.renderThreadFull(w, r, peer, code, "", edits, barErr)
}

func (s *server) renderThreadFull(w http.ResponseWriter, r *http.Request, peer string, code int, errMsg string, edits map[string]reqEdit, barErr string) {
	ctx := r.Context()
	list, err := s.list(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, err := s.loadThreadWith(ctx, peer, agentMap(list), edits)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t.Error = errMsg
	if t.Tray != nil {
		t.Tray.Error = barErr
	}
	v, _, err := s.inboxData(ctx, peer)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Thread = t
	v.OOB = true
	b := s.badges(r)
	b.OOB = true
	s.fragment(w, code, "thread-refresh", map[string]any{"V": v, "Badges": b})
}

func (s *server) threadHead(w http.ResponseWriter, r *http.Request) {
	peer := r.PathValue("peer")
	list, e := s.list(r.Context())
	if e != nil {
		s.fail(w, r, e)
		return
	}
	t, e := s.loadThread(r.Context(), peer, agentMap(list))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if a, err := s.agentStatus(r.Context(), peer); err == nil {
		t.Agent, t.Display, t.CanSend = &a, a.Display, true
	}
	s.fragment(w, http.StatusOK, "thread-head", t)
}

func (s *server) conversationsFragment(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.inboxData(r.Context(), r.URL.Query().Get("active"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.fragment(w, http.StatusOK, "conv-list", v)
}

func (s *server) navFragment(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("part") == "alert" {
		s.fragment(w, http.StatusOK, "nav-alert", s.badges(r))
		return
	}
	s.fragment(w, http.StatusOK, "nav-badges", s.badges(r))
}

// subjectFor makes a subject for a message from the user: "Re: " and the
// replied-to subject, or the replied-to text when its subject was a
// placeholder, or the first line of the body. Never empty (the provider
// requires 1-256 characters).
func subjectFor(body string, parent *core.Mail) string {
	firstLine := func(s string) string {
		line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
		return excerpt(line, 80)
	}
	if parent != nil {
		if s := cmp.Or(subject(*parent), firstLine(parent.Body)); s != "" {
			return excerpt("Re: "+s, 120)
		}
	}
	return cmp.Or(firstLine(body), "Message")
}

func findMail(msgs []core.Mail, id string) *core.Mail {
	i := slices.IndexFunc(msgs, func(m core.Mail) bool { return m.ID == id })
	if i < 0 {
		return nil
	}
	return &msgs[i]
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	peer := r.PathValue("peer")
	body := strings.TrimSpace(strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n"))
	ids, err := attachmentIDs(r)
	if err != nil {
		s.sendFailed(w, r, peer, err)
		return
	}
	if len(ids) > 0 && core.IsRemoteName(peer) {
		s.sendFailed(w, r, peer, fmt.Errorf("%w: %s", core.ErrInvalid, noRemoteFiles))
		return
	}
	if body == "" && len(ids) == 0 {
		s.sendFailed(w, r, peer, fmt.Errorf("%w: write a message first", core.ErrInvalid))
		return
	}
	if body == "" {
		names := make([]string, len(ids))
		for i, id := range ids {
			names[i] = "a file"
			if a, ok := s.uploads.get(peer, id); ok {
				names[i] = a.Filename
			}
		}
		body = filesBody(names)
	}
	msgs, err := s.svc.Conversation(r.Context(), peer, 200, time.Time{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := core.Outgoing{To: peer, Body: body, Type: "request", Attachments: ids,
		Context: map[string]any{"userchat": map[string]any{"kind": "say"}}}
	parent := findMail(msgs, r.FormValue("reply_to"))
	if parent != nil && userChat(*parent).Kind == "ask" {
		parent = nil // chatting doesn't answer a request; its panel does
	}
	if parent != nil && parent.FromName == peer {
		out.InReplyTo, out.Type = parent.ID, "response"
		out.Context = map[string]any{"userchat": map[string]any{"kind": "reply"}}
	} else {
		parent = nil
	}
	out.Subject = subjectFor(body, parent)
	if _, err := s.svc.UserSend(r.Context(), out); err != nil {
		s.sendFailed(w, r, peer, err)
		return
	}
	s.uploads.done(ids)
	s.afterSend(w, r, peer)
}

func (s *server) decide(w http.ResponseWriter, r *http.Request) {
	peer, id, option := r.PathValue("peer"), r.FormValue("id"), r.FormValue("option")
	msgs, err := s.svc.Conversation(r.Context(), peer, 200, time.Time{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ask := findMail(msgs, id)
	if ask == nil || ask.FromName != peer {
		s.sendFailed(w, r, peer, fmt.Errorf("%w: that question is no longer in this conversation", core.ErrInvalid))
		return
	}
	if uc := userChat(*ask); uc.Kind != "ask" || !slices.Contains(uc.Options, option) {
		s.sendFailed(w, r, peer, fmt.Errorf("%w: %q is not one of the options", core.ErrInvalid, option))
		return
	}
	out := core.Outgoing{
		To: peer, Subject: subjectFor("", ask), Body: option, Type: "response", InReplyTo: ask.ID,
		Context: map[string]any{"userchat": map[string]any{"kind": "decision", "decision": option}},
	}
	if _, err := s.svc.UserSend(r.Context(), out); err != nil {
		s.sendFailed(w, r, peer, err)
		return
	}
	s.afterSend(w, r, peer)
}

func (s *server) afterSend(w http.ResponseWriter, r *http.Request, peer string) {
	if !isHX(r) {
		http.Redirect(w, r, "/inbox/"+peer, http.StatusSeeOther)
		return
	}
	s.renderThread(w, r, peer, "", http.StatusOK)
}

// sendFailed shows why a message did not go out, above the composer. The
// typed text stays in the textarea (the composer is not swapped).
func (s *server) sendFailed(w http.ResponseWriter, r *http.Request, peer string, err error) {
	code := statusFor(err)
	msg := err.Error()
	if errors.Is(err, core.ErrDenied) {
		msg = "Not sent: the org policy doesn't let you message " + peer + ". " + strings.TrimPrefix(msg, core.ErrDenied.Error()+": ")
	} else {
		msg = humanize(err, "")
	}
	if !isHX(r) {
		http.Error(w, msg, code)
		return
	}
	w.Header().Set("HX-Retarget", "#composer-error")
	w.Header().Set("HX-Reswap", "innerHTML")
	s.fragment(w, http.StatusUnprocessableEntity, "composer-error", msg)
}
