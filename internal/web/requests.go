package web

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Requests: what an assistant asked the user for (a choice, a few details, a
// key, a task, or an answer in words). Each one is an open card in the
// conversation, where it was asked; a waiting bar above the composer points
// at it while it is out of view. Chatting doesn't answer them. A card's Send
// answer (or Skip) sends that one answer at once, as an "answers" mail with
// one entry; once answered the card folds into a one-line receipt. The older
// stage-then-send-together flow is still accepted (a post without send=1).
// See core/ask.go and INBOX.md.

// legacyAsksBefore: asks older than this were answered by any later message
// from the user (the rule before requests), so they don't come back as open.
var legacyAsksBefore = time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)

const (
	maxAnswerRunes = 8000
	maxLogRunes    = 60000 // a logs field: pasted output can be long
	maxAnswerFiles = 10    // files in one answers mail, like any message
)

// requestDrafts holds staged answers, in memory: peer → ask ID → answer.
// Secret values are never kept here; they are saved when staged.
type requestDrafts struct {
	mu sync.Mutex
	m  map[string]map[string]core.AskAnswer
}

func cloneAnswer(a core.AskAnswer) core.AskAnswer {
	a.Values, a.Secrets = maps.Clone(a.Values), slices.Clone(a.Secrets)
	return a
}

func (d *requestDrafts) get(peer, id string) (core.AskAnswer, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.m[peer][id]
	return cloneAnswer(a), ok
}

func (d *requestDrafts) set(peer string, a core.AskAnswer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]map[string]core.AskAnswer{}
	}
	if d.m[peer] == nil {
		d.m[peer] = map[string]core.AskAnswer{}
	}
	d.m[peer][a.Ask] = cloneAnswer(a)
}

func (d *requestDrafts) drop(peer string, ids ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range ids {
		delete(d.m[peer], id)
	}
}

// prune forgets drafts for asks that are no longer open (answered elsewhere).
func (d *requestDrafts) prune(peer string, open map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range d.m[peer] {
		if !open[id] {
			delete(d.m[peer], id)
		}
	}
}

// closeAsks marks each ask from the peer that the user has answered, oldest
// first: by a decision or an answers mail naming it, by a reply to it (typed
// before requests existed), or, for legacy asks, by anything the user wrote
// after it.
func closeAsks(msgs []msgView) {
	for i := range msgs {
		m := &msgs[i]
		if m.Mine || m.Kind != "ask" {
			continue
		}
		legacy := m.CreatedAt.Before(legacyAsksBefore)
		for _, r := range msgs[i+1:] {
			if !r.Mine {
				continue
			}
			switch {
			case r.Chat.Kind == "decision" && r.InReplyTo == m.ID:
				m.Answered, m.Answer, m.AnsweredAt = true, r.Chat.Decision, r.CreatedAt
				m.Reply = &core.AskAnswer{Ask: m.ID, Decision: r.Chat.Decision}
			case r.Chat.Kind == "answers":
				if k := slices.IndexFunc(r.Chat.Answers, func(a core.AskAnswer) bool { return a.Ask == m.ID }); k >= 0 {
					a := r.Chat.Answers[k]
					m.Answered, m.Skipped, m.Answer, m.AnsweredAt = true, a.Skipped, answerSummary(a, m.Chat.Fields), r.CreatedAt
					m.Reply = &a
				}
			case r.InReplyTo == m.ID, legacy:
				m.Answered, m.AnsweredAt = true, r.CreatedAt
			}
			if m.Answered {
				break
			}
		}
	}
}

// optSep splits an option into its label and a one-line consequence
// ("Book the 9:40 — €212 · lands 12:10"). The whole string is still the
// option's value, so the agent gets back exactly what it offered.
const optSep = " — "

func optLabel(o string) string {
	if l, _, ok := strings.Cut(o, optSep); ok && strings.TrimSpace(l) != "" {
		return strings.TrimSpace(l)
	}
	return o
}

func optNote(o string) string {
	if l, n, ok := strings.Cut(o, optSep); ok && strings.TrimSpace(l) != "" {
		return strings.TrimSpace(n)
	}
	return ""
}

// numberRE and dateRE are the user-channel spec's forms (3.6): ASCII digits,
// one optional decimal point or comma; YYYY-MM-DD.
var (
	numberRE = regexp.MustCompile(`^-?[0-9]+([.,][0-9]+)?$`)
	dateRE   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// taskFiles is the key, in a task answer's Files and Values, of the
// screenshots or logs sent with "I couldn't do it".
const taskFiles = "details"

// receiptLine is the one line an answered card folds into.
func receiptLine(a core.AskAnswer, uc core.UserChat) string {
	if a.Skipped {
		return "Skipped"
	}
	var parts []string
	switch {
	case a.Decision == "":
	case uc.Task && len(uc.Options) > 0 && a.Decision == uc.Options[0]:
		parts = append(parts, optLabel(a.Decision))
	case uc.Task:
		parts = append(parts, optLabel(a.Decision))
	default:
		parts = append(parts, "You chose: "+optLabel(a.Decision))
	}
	for _, f := range uc.Fields {
		if f.Type == core.FieldSecret {
			continue
		}
		if v, ok := a.Values[f.Name]; ok {
			if f.Type == core.FieldLogs {
				v = plural(strings.Count(v, "\n")+1, "line", "lines")
			}
			parts = append(parts, fieldLabel(f)+": "+v)
		}
	}
	for _, k := range a.Secrets {
		label := k
		for _, f := range uc.Fields {
			if f.Name == k {
				label = fieldLabel(f)
			}
		}
		parts = append(parts, label+": Saved")
	}
	if names := a.Values[taskFiles]; names != "" && len(a.Files[taskFiles]) > 0 {
		parts = append(parts, "Sent "+names)
	}
	if a.Text != "" {
		if len(parts) == 0 {
			parts = append(parts, "You said: "+a.Text)
		} else {
			parts = append(parts, a.Text)
		}
	}
	return excerpt(strings.Join(parts, " · "), 160)
}

// answerLine is one request in the user's answers message, as the user sees it.
type answerLine struct {
	Q, A    string
	Skipped bool
}

// answerLines fills the readable form of the user's answers messages.
func answerLines(msgs []msgView) {
	asks := map[string]*msgView{}
	for i := range msgs {
		m := &msgs[i]
		if m.Kind == "ask" && !m.Mine {
			asks[m.ID] = m
			continue
		}
		if !m.Mine || m.Kind != "answers" {
			continue
		}
		for _, a := range m.Chat.Answers {
			l := answerLine{Q: "An earlier request", Skipped: a.Skipped}
			var fields []core.AskField
			if q := asks[a.Ask]; q != nil {
				l.Q = cmp.Or(subject(q.Mail), excerpt(strings.Join(strings.Fields(q.Body), " "), 90))
				fields = q.Chat.Fields
			}
			l.A = answerSummary(a, fields)
			m.Lines = append(m.Lines, l)
		}
	}
}

// foldAnswers hides the user's answers and decision mails whose every
// request is shown in this view: each card's receipt already says what was
// answered. Mails answering requests further back stay, as a list.
func foldAnswers(msgs []msgView) {
	asks := map[string]bool{}
	for _, m := range msgs {
		if m.Kind == "ask" && !m.Mine {
			asks[m.ID] = true
		}
	}
	for i := range msgs {
		m := &msgs[i]
		if !m.Mine {
			continue
		}
		switch m.Kind {
		case "decision":
			m.Folded = m.InReplyTo != "" && asks[m.InReplyTo]
		case "answers":
			m.Folded = len(m.Chat.Answers) > 0 && !slices.ContainsFunc(m.Chat.Answers, func(a core.AskAnswer) bool { return !asks[a.Ask] })
		}
	}
}

func fieldLabel(f core.AskField) string { return cmp.Or(f.Label, f.Name) }

// answerSummary is one line for an answer: what the user chose or wrote.
func answerSummary(a core.AskAnswer, fields []core.AskField) string {
	if a.Skipped {
		return "Skipped"
	}
	var parts []string
	if a.Decision != "" {
		parts = append(parts, a.Decision)
	}
	for _, f := range fields {
		if v, ok := a.Values[f.Name]; ok {
			if f.Type == core.FieldLogs {
				v = plural(strings.Count(v, "\n")+1, "line", "lines")
			}
			parts = append(parts, fieldLabel(f)+": "+v)
		}
	}
	for _, k := range a.Secrets {
		parts = append(parts, k+" saved")
	}
	if a.Text != "" {
		parts = append(parts, a.Text)
	}
	return excerpt(strings.Join(parts, " · "), 160)
}

// restates reports whether a subject is only the body's first words (what
// lolad msg derives when an agent gives no subject), so it isn't shown twice.
func restates(subj, body string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.Join(strings.Fields(s), " "))
		return strings.TrimRight(strings.TrimLeft(s, "#*_> "), ".…:?! ")
	}
	s, b := norm(subj), norm(body)
	return s != "" && strings.HasPrefix(b, s)
}

// ---------------------------------------------------------------- views

type reqField struct {
	core.AskField
	Value string // staged or posted value; never a secret
	Saved int    // secret: length of the value already in the env file, 0 when unset
}

type reqView struct {
	Ask       msgView
	Title     string // the subject, when it says more than the body's first words
	Line      string // one line for Home: the title, else the start of the body
	Kind      string // options | form | words
	Fields    []reqField
	Secret    bool   // asks for at least one key
	SecretOff string // why keys can't be saved for this peer
	File      bool   // asks for files: the form posts multipart
	FileOff   string // why files can't be sent to this peer
	Draft     *core.AskAnswer
	Summary   string // of Draft
	Text      string // own-words answer being edited
	Error     string
}

type trayView struct {
	Peer    string
	Display string
	State   core.State
	Home    bool // a row on Home rather than the bar in the thread
	Reqs    []reqView
	Ready   int    // staged, ready to send
	Error   string // why Send failed
}

// Waiting is how many requests have no answer staged yet.
func (t trayView) Waiting() int { return len(t.Reqs) - t.Ready }

// FirstWaiting is the oldest request without a staged answer.
func (t trayView) FirstWaiting() string {
	for _, r := range t.Reqs {
		if r.Draft == nil {
			return r.Ask.ID
		}
	}
	return ""
}

// reqEdit is what the user posted with a card that was refused, shown again.
type reqEdit struct {
	Error  string
	Text   string
	Values map[string]string
}

func reqKind(uc core.UserChat) string {
	switch {
	case uc.Task && len(uc.Options) > 0:
		return "task"
	case len(uc.Fields) > 0:
		return "form"
	case len(uc.Options) > 0:
		return "options"
	}
	return "words"
}

// secretOff says why keys can't be saved for peer, "" when they can: only
// this lolad's own assistants have an env file.
func (s *server) secretOff(ctx context.Context, peer string) string {
	if core.IsRemoteName(peer) {
		return "Keys can only be saved for your own assistants. Skip this, or send it another way."
	}
	if _, err := s.agentStatus(ctx, peer); err != nil {
		return "Only your own assistants can receive keys."
	}
	return ""
}

// envLengths is the length of each key already set in the peer's env file.
func (s *server) envLengths(ctx context.Context, peer string) map[string]int {
	out := map[string]int{}
	vars, err := s.svc.Env(ctx, peer)
	if err != nil {
		return out
	}
	for _, v := range vars {
		if v.Set {
			out[v.Key] = max(v.Length, 1)
		}
	}
	return out
}

func hasSecret(uc core.UserChat) bool { return hasField(uc, core.FieldSecret) }

func hasField(uc core.UserChat, typ string) bool {
	return slices.ContainsFunc(uc.Fields, func(f core.AskField) bool { return f.Type == typ })
}

// fileOff says why files can't go to peer, "" when they can.
func fileOff(peer string) string {
	if core.IsRemoteName(peer) {
		return noRemoteFiles
	}
	return ""
}

// buildTray lays out the open asks of one peer, oldest first, with their
// staged answers. edits holds what was posted with refused cards.
func (s *server) buildTray(ctx context.Context, peer, display string, state core.State, open []msgView, home bool, edits map[string]reqEdit) trayView {
	t := trayView{Peer: peer, Display: cmp.Or(display, peer), State: state, Home: home}
	ids := map[string]bool{}
	for _, m := range open {
		ids[m.ID] = true
	}
	s.drafts.prune(peer, ids)
	var env map[string]int
	secretOff := ""
	for _, m := range open {
		uc := m.Chat
		r := reqView{Ask: m, Kind: reqKind(uc), Secret: hasSecret(uc), File: hasField(uc, core.FieldFile)}
		if r.File {
			r.FileOff = fileOff(peer)
		}
		if subj := subject(m.Mail); !restates(subj, m.Body) {
			r.Title = subj
		}
		r.Line = cmp.Or(r.Title, excerpt(strings.Join(strings.Fields(m.Body), " "), 140))
		if r.Secret && env == nil {
			env, secretOff = s.envLengths(ctx, peer), s.secretOff(ctx, peer)
		}
		r.SecretOff = secretOff
		d, staged := s.drafts.get(peer, m.ID)
		if staged {
			r.Draft, r.Summary = &d, answerSummary(d, uc.Fields)
			t.Ready++
		}
		e := edits[m.ID]
		r.Error, r.Text = e.Error, cmp.Or(e.Text, d.Text)
		for _, f := range uc.Fields {
			rf := reqField{AskField: f, Value: cmp.Or(e.Values[f.Name], d.Values[f.Name])}
			if f.Type == core.FieldSecret {
				rf.Value, rf.Saved = "", env[f.Name]
			}
			r.Fields = append(r.Fields, rf)
		}
		t.Reqs = append(t.Reqs, r)
	}
	return t
}

// openRequests reads the conversation and returns the peer's open asks,
// oldest first.
func (s *server) openRequests(ctx context.Context, peer string) ([]msgView, error) {
	msgs, err := s.svc.Conversation(ctx, peer, 200, time.Time{})
	if err != nil {
		return nil, err
	}
	v := make([]msgView, len(msgs))
	for i, m := range msgs {
		v[i] = msgView{Mail: m}
	}
	shape(v, s.svc.Me().Name)
	return openOf(v), nil
}

func openOf(msgs []msgView) []msgView {
	var out []msgView
	for _, m := range msgs {
		if !m.Mine && m.Kind == "ask" && !m.Answered {
			out = append(out, m)
		}
	}
	return out
}

// peerLook is the name and state shown for a peer.
func (s *server) peerLook(ctx context.Context, peer string) (string, core.State) {
	if a, err := s.agentStatus(ctx, peer); err == nil {
		return cmp.Or(a.Display, a.Name), a.State
	}
	if core.IsRemoteName(peer) {
		if cs, err := s.svc.RemoteContacts(ctx, s.svc.Me().Name); err == nil {
			for _, c := range cs {
				if c.Address == peer {
					return cmp.Or(c.Display, c.Name), ""
				}
			}
		}
	}
	return peer, ""
}

// homeTrays groups Home's open asks by peer, most recent request first.
func (s *server) homeTrays(ctx context.Context, asks []askView) []trayView {
	var order []string
	by := map[string][]msgView{}
	convs := map[string]convView{}
	for _, a := range asks { // newest first
		p := a.Conv.Peer
		if _, ok := by[p]; !ok {
			order = append(order, p)
			convs[p] = a.Conv
		}
		by[p] = append(by[p], a.Ask)
	}
	out := make([]trayView, 0, len(order))
	for _, p := range order {
		open := by[p]
		slices.Reverse(open)
		out = append(out, s.buildTray(ctx, p, convs[p].Display, convs[p].State(), open, true, nil))
	}
	return out
}

// ---------------------------------------------------------------- handlers

// renderRequests answers a request action: the thread again (the question
// shows its staged answer or its error in place, the bar its count), or a
// redirect back to it for plain form posts.
func (s *server) renderRequests(w http.ResponseWriter, r *http.Request, peer string, status int, edits map[string]reqEdit, barErr string) {
	if !isHX(r) {
		if barErr != "" || len(edits) > 0 {
			msg := barErr
			for _, e := range edits {
				msg = cmp.Or(msg, e.Error)
			}
			http.Error(w, msg, cmp.Or(status, http.StatusUnprocessableEntity))
			return
		}
		http.Redirect(w, r, "/inbox/"+peer, http.StatusSeeOther)
		return
	}
	s.renderThreadWith(w, r, peer, status, edits, barErr)
}

// stageRequest stages, changes or skips the answer to one ask. Nothing is
// sent: the panel's Send does that for every staged answer at once.
func (s *server) stageRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peer, id := r.PathValue("peer"), r.PathValue("id")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		// Files for a file field: the same limits as the composer's uploads.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Minute))
		r.Body = http.MaxBytesReader(w, r.Body, maxAnswerFiles*(cmp.Or(s.maxUpload, DefaultMaxUpload)+uploadSlack))
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			s.renderRequests(w, r, peer, http.StatusRequestEntityTooLarge, map[string]reqEdit{id: {Error: "Those files are too large to send."}}, "")
			return
		}
		defer r.MultipartForm.RemoveAll()
	}
	open, err := s.openRequests(ctx, peer)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	i := slices.IndexFunc(open, func(m msgView) bool { return m.ID == id })
	if i < 0 {
		s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, nil, "That request was already answered.")
		return
	}
	ask := open[i]
	do, option := r.FormValue("do"), r.FormValue("option")
	if (do == "" || do == "send") && option != "" {
		do = "option"
	}
	now := r.FormValue("send") == "1" && do != "change" // the card's own Send answer or Skip
	switch do {
	case "change":
		s.drafts.drop(peer, id)
	case "skip":
		s.drafts.set(peer, core.AskAnswer{Ask: id, Skipped: true})
	case "option":
		if !slices.Contains(ask.Chat.Options, option) {
			s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: {Error: fmt.Sprintf("%q is not one of the choices.", option)}}, "")
			return
		}
		text := strings.TrimSpace(strings.ReplaceAll(r.FormValue("text"), "\r\n", "\n"))
		if utf8.RuneCountInString(text) > maxAnswerRunes {
			s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: {Error: fmt.Sprintf("That note is too long (at most %d characters).", maxAnswerRunes), Text: text}}, "")
			return
		}
		if !ask.Chat.Task {
			// user-channel 3.6: a choice or your own words, not both. The
			// "Something else" box is always posted; with a choice picked,
			// the choice is the answer, and the card says beforehand that
			// the typed words won't be sent (.ask-dropped).
			text = ""
		}
		a := core.AskAnswer{Ask: id, Decision: option, Text: text} // a task's "what happened" rides along
		if ask.Chat.Task {
			names, ids, msg := s.uploadAll(ctx, peer, r, "files")
			if msg != "" {
				s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: {Error: msg, Text: text}}, "")
				return
			}
			if len(ids) > 0 && len(ask.Chat.Options) > 0 && option == ask.Chat.Options[0] {
				s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: {Error: "Files go with \"" + optLabel(ask.Chat.Options[len(ask.Chat.Options)-1]) + "\"; remove them, or say what went wrong.", Text: text}}, "")
				return
			}
			if len(ids) > 0 {
				a.Files = map[string][]string{taskFiles: ids}
				a.Values = map[string]string{taskFiles: names}
			}
		}
		s.drafts.set(peer, a)
	default:
		a, edit := s.collect(ctx, peer, ask, r)
		if edit.Error != "" {
			s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: edit}, "")
			return
		}
		s.drafts.set(peer, a)
	}
	if now {
		if status, msg := s.sendAnswers(ctx, peer, open, []string{id}); msg != "" {
			s.renderRequests(w, r, peer, status, map[string]reqEdit{id: {Error: msg}}, "")
			return
		}
		if isHX(r) {
			w.Header().Set("HX-Trigger", `{"lolad:mail":{}}`)
		}
		if !isHX(r) && r.FormValue("back") == "home" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	s.renderRequests(w, r, peer, http.StatusOK, nil, "")
}

// uploadAll uploads the files posted under name, for a task's "I couldn't
// do it". It returns their names, their attachment IDs, or why it couldn't.
func (s *server) uploadAll(ctx context.Context, peer string, r *http.Request, name string) (string, []string, string) {
	if r.MultipartForm == nil {
		return "", nil, ""
	}
	var parts []*multipart.FileHeader
	for _, p := range r.MultipartForm.File[name] {
		if p.Filename != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "", nil, ""
	}
	if off := fileOff(peer); off != "" {
		return "", nil, off
	}
	if len(parts) > maxAnswerFiles {
		return "", nil, fmt.Sprintf("Choose at most %d files.", maxAnswerFiles)
	}
	var names, ids []string
	for _, p := range parts {
		f, err := p.Open()
		if err != nil {
			return "", nil, fmt.Sprintf("Couldn't read %s.", p.Filename)
		}
		att, err := s.svc.UploadAttachment(ctx, p.Filename, p.Header.Get("Content-Type"), f)
		f.Close()
		if err != nil {
			return "", nil, fmt.Sprintf("Couldn't attach %s: %s", p.Filename, humanize(err, ""))
		}
		names, ids = append(names, p.Filename), append(ids, att.ID)
	}
	return strings.Join(names, ", "), ids, ""
}

// collect reads and checks a card's inputs. Keys are saved to the agent's env
// file here, once everything else is valid; the answer only names them.
func (s *server) collect(ctx context.Context, peer string, ask msgView, r *http.Request) (core.AskAnswer, reqEdit) {
	a := core.AskAnswer{Ask: ask.ID}
	text := strings.TrimSpace(strings.ReplaceAll(r.FormValue("text"), "\r\n", "\n"))
	e := reqEdit{Text: text, Values: map[string]string{}}
	fail := func(format string, args ...any) (core.AskAnswer, reqEdit) {
		e.Error = fmt.Sprintf(format, args...)
		return a, e
	}
	if utf8.RuneCountInString(text) > maxAnswerRunes {
		return fail("That answer is too long (at most %d characters).", maxAnswerRunes)
	}
	fields := ask.Chat.Fields
	if len(fields) == 0 {
		if text == "" {
			if len(ask.Chat.Options) > 0 {
				return fail("Choose one of the answers, or write your own.")
			}
			return fail("Write an answer, or skip this request.")
		}
		a.Text = text
		return a, e
	}
	known := map[string]bool{}
	for _, f := range fields {
		known["f."+f.Name] = true
	}
	posted := []string{}
	for k := range r.Form {
		posted = append(posted, k)
	}
	if r.MultipartForm != nil {
		for k := range r.MultipartForm.File {
			posted = append(posted, k)
		}
	}
	for _, k := range posted {
		if strings.HasPrefix(k, "f.") && !known[k] {
			return fail("This request has no field %q.", strings.TrimPrefix(k, "f."))
		}
	}
	var secrets, files []core.AskField
	for _, f := range fields {
		switch f.Type {
		case core.FieldSecret:
			secrets = append(secrets, f)
			continue
		case core.FieldFile:
			files = append(files, f)
			continue
		}
		raw := r.FormValue("f." + f.Name)
		v := strings.TrimSpace(raw)
		label := fieldLabel(f)
		switch f.Type {
		case core.FieldText: // user-channel 3.6: one line as typed
			if strings.ContainsAny(raw, "\r\n") {
				e.Values[f.Name] = v
				return fail("%s takes one line.", label)
			}
		case core.FieldLogs, core.FieldLongText: // as typed (browsers send CRLF; kept as LF)
			v = strings.ReplaceAll(raw, "\r\n", "\n")
			if strings.TrimSpace(v) == "" {
				v = ""
			}
		}
		e.Values[f.Name] = v
		if v == "" {
			if f.Optional {
				continue
			}
			return fail("Fill in %s, or skip this request.", label)
		}
		switch f.Type {
		case core.FieldNumber:
			if !numberRE.MatchString(v) {
				return fail("%s needs a number, like 40 or 2.5.", label)
			}
		case core.FieldDate:
			if d, err := time.Parse("2006-01-02", v); err != nil || !dateRE.MatchString(v) || d.Year() < 1 {
				return fail("%s needs a date.", label)
			}
		case core.FieldChoice:
			if !slices.Contains(f.Options, v) {
				return fail("Pick one of the choices for %s.", label)
			}
		}
		if limit := map[bool]int{true: maxLogRunes, false: maxAnswerRunes}[f.Type == core.FieldLogs]; utf8.RuneCountInString(v) > limit {
			return fail("%s is too long (at most %d characters). Attach it as a file instead.", label, limit)
		}
		if a.Values == nil {
			a.Values = map[string]string{}
		}
		a.Values[f.Name] = v
	}
	type upload struct {
		field core.AskField
		parts []*multipart.FileHeader
	}
	var uploads []upload
	count := 0
	for _, f := range files {
		var parts []*multipart.FileHeader
		if r.MultipartForm != nil {
			for _, p := range r.MultipartForm.File["f."+f.Name] {
				if p.Filename != "" {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) == 0 {
			if f.Optional {
				continue
			}
			return fail("Choose a file for %s, or skip this request.", fieldLabel(f))
		}
		if off := fileOff(peer); off != "" {
			return fail("%s", off)
		}
		names := make([]string, len(parts))
		for i, p := range parts {
			names[i] = p.Filename
		}
		e.Values[f.Name] = strings.Join(names, ", ")
		count += len(parts)
		uploads = append(uploads, upload{f, parts})
	}
	if count > maxAnswerFiles {
		return fail("Choose at most %d files.", maxAnswerFiles)
	}
	if len(secrets) > 0 {
		if off := s.secretOff(ctx, peer); off != "" {
			return fail("%s", off)
		}
		env := s.envLengths(ctx, peer)
		type pending struct{ key, value string }
		var save []pending
		for _, f := range secrets {
			v := strings.TrimRight(r.FormValue("f."+f.Name), "\r\n") // as typed otherwise: a key may have meaningful spaces
			switch {
			case v != "":
				save = append(save, pending{f.Name, v})
			case env[f.Name] > 0:
				a.Secrets = append(a.Secrets, f.Name) // keep the one already saved
			case !f.Optional:
				return fail("Paste %s, or skip this request.", fieldLabel(f))
			}
		}
		for _, p := range save {
			if err := s.svc.SetEnv(ctx, peer, p.key, p.value); err != nil {
				return fail("Couldn't save %s: %s", p.key, humanize(err, ""))
			}
			a.Secrets = append(a.Secrets, p.key)
		}
	}
	for _, u := range uploads {
		var ids, names []string
		for _, p := range u.parts {
			f, err := p.Open()
			if err != nil {
				return fail("Couldn't read %s.", p.Filename)
			}
			att, err := s.svc.UploadAttachment(ctx, p.Filename, p.Header.Get("Content-Type"), f)
			f.Close()
			if err != nil {
				return fail("Couldn't attach %s: %s", p.Filename, humanize(err, ""))
			}
			ids, names = append(ids, att.ID), append(names, p.Filename)
		}
		if a.Files == nil {
			a.Files = map[string][]string{}
		}
		if a.Values == nil {
			a.Values = map[string]string{}
		}
		a.Files[u.field.Name], a.Values[u.field.Name] = ids, strings.Join(names, ", ")
	}
	return a, e
}

// sendRequests sends every staged answer to the peer in one mail.
func (s *server) sendRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peer := r.PathValue("peer")
	open, err := s.openRequests(ctx, peer)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if status, msg := s.sendAnswers(ctx, peer, open, nil); msg != "" {
		s.renderRequests(w, r, peer, status, nil, msg)
		return
	}
	if isHX(r) {
		w.Header().Set("HX-Trigger", `{"lolad:mail":{}}`)
	}
	s.renderRequests(w, r, peer, http.StatusOK, nil, "")
}

// sendAnswers sends the staged answers to the open asks named by only (every
// staged answer when only is nil) in one mail, and forgets them. It returns
// an HTTP status and a message for the user when nothing was sent.
func (s *server) sendAnswers(ctx context.Context, peer string, open []msgView, only []string) (int, string) {
	var asks []msgView
	var answers []core.AskAnswer
	for _, m := range open {
		if only != nil && !slices.Contains(only, m.ID) {
			continue
		}
		if a, ok := s.drafts.get(peer, m.ID); ok {
			asks, answers = append(asks, m), append(answers, a)
		}
	}
	if len(answers) == 0 {
		return http.StatusUnprocessableEntity, "Answer or skip a request first."
	}
	subj := "Answers to " + plural(len(answers), "request", "requests")
	if len(answers) == 1 {
		subj = subjectFor("", &asks[0].Mail)
	}
	var files []string
	for _, a := range answers {
		for _, ids := range a.Files {
			files = append(files, ids...)
		}
	}
	if len(files) > maxAnswerFiles {
		return http.StatusUnprocessableEntity, fmt.Sprintf("These answers carry %d files; one send takes %d. Change one, send, then send the rest.", len(files), maxAnswerFiles)
	}
	out := core.Outgoing{To: peer, Subject: subj, Body: answersBody(asks, answers), Type: "response", InReplyTo: asks[len(asks)-1].ID,
		Context: map[string]any{"userchat": jsonMap(core.UserChat{Kind: "answers", Answers: answers})}, Attachments: files}
	if _, err := s.svc.UserSend(ctx, out); err != nil {
		return statusFor(err), "Not sent: " + humanize(err, "")
	}
	ids := make([]string, len(answers))
	for i, a := range answers {
		ids[i] = a.Ask
	}
	s.drafts.drop(peer, ids...)
	return http.StatusOK, ""
}

// jsonMap is v as the provider stores it: plain JSON values.
func jsonMap(v any) map[string]any {
	var m map[string]any
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &m)
	return m
}

// answersBody is the answers mail as the agent reads it.
func answersBody(asks []msgView, answers []core.AskAnswer) string {
	var b strings.Builder
	answered := 0
	for _, a := range answers {
		if !a.Skipped {
			answered++
		}
	}
	fmt.Fprintf(&b, "Answers to your requests (%d answered, %d skipped):\n", answered, len(answers)-answered)
	for i, a := range answers {
		m := asks[i]
		q := cmp.Or(subject(m.Mail), excerpt(strings.Join(strings.Fields(m.Body), " "), 90))
		fmt.Fprintf(&b, "\n%d. %q (request %s)\n", i+1, q, shortID(m.ID))
		if a.Skipped {
			b.WriteString("   Skipped: the user chose not to answer. Carry on without it; ask again only if something changes.\n")
			continue
		}
		if a.Decision != "" {
			label := "Choice"
			if m.Chat.Task {
				label = "Result"
			}
			fmt.Fprintf(&b, "   %s: %s\n", label, a.Decision)
		}
		for _, f := range m.Chat.Fields {
			v, ok := a.Values[f.Name]
			switch {
			case !ok:
			case f.Type == core.FieldFile:
				fmt.Fprintf(&b, "   %s: attached to this message (%s)\n", fieldLabel(f), v)
			case f.Type == core.FieldLogs:
				fence := "```"
				for strings.Contains(v, fence) {
					fence += "`"
				}
				fmt.Fprintf(&b, "   %s:\n   %s\n   %s\n   %s\n", fieldLabel(f), fence, indentLines(v), fence)
			default:
				fmt.Fprintf(&b, "   %s: %s\n", fieldLabel(f), indentLines(v))
			}
		}
		if names := a.Values[taskFiles]; names != "" && len(a.Files[taskFiles]) > 0 {
			fmt.Fprintf(&b, "   Screenshots or logs: attached to this message (%s)\n", names)
		}
		for _, k := range a.Secrets {
			fmt.Fprintf(&b, "   %s: saved in your .env file (the value is not in this message). Read it from there or from your environment.\n", k)
		}
		if a.Text != "" {
			fmt.Fprintf(&b, "   Answer: %s\n", indentLines(a.Text))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func indentLines(s string) string { return strings.ReplaceAll(s, "\n", "\n   ") }
