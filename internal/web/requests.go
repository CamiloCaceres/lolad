package web

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Requests: what an assistant asked the user for (a choice, a few details, a
// key, or an answer in words). They stay in the conversation, where they were
// asked, behind a small "Answer" under the question; a one-line bar above the
// composer counts them. Chatting doesn't answer them. Answers are staged here,
// then sent together as one "answers" mail, so the assistant wakes once. See
// core/ask.go and INBOX.md.

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
				m.Answered, m.Answer = true, r.Chat.Decision
			case r.Chat.Kind == "answers":
				if k := slices.IndexFunc(r.Chat.Answers, func(a core.AskAnswer) bool { return a.Ask == m.ID }); k >= 0 {
					a := r.Chat.Answers[k]
					m.Answered, m.Skipped, m.Answer = true, a.Skipped, answerSummary(a, m.Chat.Fields)
				}
			case r.InReplyTo == m.ID, legacy:
				m.Answered = true
			}
			if m.Answered {
				break
			}
		}
	}
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
	if do == "" && option != "" {
		do = "option"
	}
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
		s.drafts.set(peer, core.AskAnswer{Ask: id, Decision: option, Text: text}) // a task's "what happened" rides along
	default:
		a, edit := s.collect(ctx, peer, ask, r)
		if edit.Error != "" {
			s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, map[string]reqEdit{id: edit}, "")
			return
		}
		s.drafts.set(peer, a)
	}
	s.renderRequests(w, r, peer, http.StatusOK, nil, "")
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
			return fail("Write an answer, or skip this request.")
		}
		a.Text = text
		return a, e
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
		v := strings.TrimSpace(r.FormValue("f." + f.Name))
		if f.Type == core.FieldLogs { // verbatim, but without blank lines around it
			v = strings.Trim(strings.ReplaceAll(r.FormValue("f."+f.Name), "\r\n", "\n"), "\n")
			if strings.TrimSpace(v) == "" {
				v = ""
			}
		}
		e.Values[f.Name] = v
		label := fieldLabel(f)
		if v == "" {
			if f.Optional {
				continue
			}
			return fail("Fill in %s, or skip this request.", label)
		}
		switch f.Type {
		case core.FieldNumber:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return fail("%s needs a number.", label)
			}
		case core.FieldDate:
			if _, err := time.Parse("2006-01-02", v); err != nil {
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
	var asks []msgView
	var answers []core.AskAnswer
	for _, m := range open {
		if a, ok := s.drafts.get(peer, m.ID); ok {
			asks, answers = append(asks, m), append(answers, a)
		}
	}
	if len(answers) == 0 {
		s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, nil, "Answer or skip a request first.")
		return
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
		s.renderRequests(w, r, peer, http.StatusUnprocessableEntity, nil, fmt.Sprintf("These answers carry %d files; one send takes %d. Change one, send, then send the rest.", len(files), maxAnswerFiles))
		return
	}
	out := core.Outgoing{To: peer, Subject: subj, Body: answersBody(asks, answers), Type: "response", InReplyTo: asks[len(asks)-1].ID,
		Context: map[string]any{"userchat": jsonMap(core.UserChat{Kind: "answers", Answers: answers})}, Attachments: files}
	if _, err := s.svc.UserSend(ctx, out); err != nil {
		s.renderRequests(w, r, peer, statusFor(err), nil, "Not sent: "+humanize(err, ""))
		return
	}
	ids := make([]string, len(answers))
	for i, a := range answers {
		ids[i] = a.Ask
	}
	s.drafts.drop(peer, ids...)
	if isHX(r) {
		w.Header().Set("HX-Trigger", `{"lolad:mail":{}}`)
	}
	s.renderRequests(w, r, peer, http.StatusOK, nil, "")
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
