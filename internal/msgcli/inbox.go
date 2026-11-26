package msgcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
)

// get_user_messages (user-channel spec 4.5): what the user sent this agent
// that it hasn't received yet — messages the user wrote and answers to its
// requests — claimed (marked read) only after every file is saved under
// .user-channel/, one mail at a time so two servers of one agent never both
// return it.

type umAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	Path        string `json:"path"`
}

// umUnavailable is a file the user sent that the agent did not get, and why.
type umUnavailable struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type umAnswer struct {
	Ask         string              `json:"ask"`
	Question    string              `json:"question"`
	Status      string              `json:"status"`
	Choice      string              `json:"choice,omitempty"`
	Outcome     string              `json:"outcome,omitempty"`
	Text        string              `json:"text,omitempty"`
	Values      map[string]string   `json:"values,omitempty"`
	Secrets     []string            `json:"secrets,omitempty"`
	Files       map[string][]string `json:"files,omitempty"`
	Attachments []umAttachment      `json:"attachments,omitempty"`
	Unavailable []umUnavailable     `json:"unavailable,omitempty"`
	AnsweredAt  string              `json:"answered_at"`
}

type umMessage struct {
	Spec        string          `json:"spec"`
	ID          string          `json:"id"`
	From        string          `json:"from,omitempty"`
	Text        string          `json:"text"`
	Attachments []umAttachment  `json:"attachments,omitempty"`
	Unavailable []umUnavailable `json:"unavailable,omitempty"`
	CreatedAt   string          `json:"created_at"`
}

// umRequest is what the text form needs of a request.
type umRequest struct {
	Title, Text string
	Task        bool
	Fields      []core.AskField
}

// umItem is one answer or user message, in the order lolad stored it.
type umItem struct {
	answer  *umAnswer
	message *umMessage
}

type umResult struct {
	Text       string
	Structured map[string]any
}

// Secret delivery (spec 3.7): in the environment of later commands (Claude
// Code: lolad env-export before each Bash command), or the secrets file.
const (
	secretsEnvironment = "environment"
	secretsFile        = "file"
	secretsFilePath    = ".user-channel/secrets.env"
)

// secretsGOOS picks the shell form of the secrets-file hint (tests set it).
var secretsGOOS = runtime.GOOS

// secretsLoadHint says how to load the secrets file into the shell: the
// spec's POSIX form, or PowerShell on Windows (the file holds one
// export NAME='value' line each, with ' written as '\”).
func secretsLoadHint(goos string) string {
	if goos == "windows" {
		return `load in PowerShell with: Get-Content .user-channel\secrets.env | ForEach-Object { if ($_ -match "^export (\w+)='(.*)'$") { Set-Item "env:$($Matches[1])" ($Matches[2] -replace "'\\''", "'") } }`
	}
	return "load with: set -a; . ./" + secretsFilePath + "; set +a"
}

func secretsMode() string {
	if os.Getenv("LOLAD_HARNESS") == string(core.HarnessCodewhale) {
		return secretsFile
	}
	return secretsEnvironment
}

func itemTime(it umItem) time.Time {
	s := ""
	if it.answer != nil {
		s = it.answer.AnsweredAt
	} else {
		s = it.message.CreatedAt
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// assemble orders items as the spec says and builds both forms.
func assemble(reqs map[string]umRequest, items []umItem, open int, mode string) umResult {
	type keyed struct {
		it    umItem
		t     time.Time
		kind  int // 0 answer, 1 message
		order int
	}
	var ks []keyed
	answers, messages := []umAnswer{}, []umMessage{}
	for i, it := range items {
		k := keyed{it: it, t: itemTime(it), order: i}
		if it.message != nil {
			k.kind = 1
		}
		ks = append(ks, k)
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if !ks[i].t.Equal(ks[j].t) {
			return ks[i].t.Before(ks[j].t)
		}
		if ks[i].kind != ks[j].kind {
			return ks[i].kind < ks[j].kind
		}
		return ks[i].order < ks[j].order
	})
	for _, k := range ks {
		if k.it.answer != nil {
			answers = append(answers, *k.it.answer)
		} else {
			messages = append(messages, *k.it.message)
		}
	}
	res := umResult{Structured: map[string]any{"answers": answers, "messages": messages, "open": open}}
	if len(ks) == 0 {
		res.Text = fmt.Sprintf("Nothing new from the user (%s still open).", count(open, "request", "requests"))
		return res
	}
	var counts []string
	if len(messages) > 0 {
		counts = append(counts, count(len(messages), "message", "messages"))
	}
	if len(answers) > 0 {
		counts = append(counts, count(len(answers), "answer", "answers"))
	}
	parts := []string{fmt.Sprintf("From the user (%s; %s still open):", strings.Join(counts, ", "), count(open, "request", "requests"))}
	for _, k := range ks {
		var lines []string
		if m := k.it.message; m != nil {
			lines = append(lines, "Message, "+k.t.UTC().Format("2006-01-02 15:04")+" UTC:")
			if !core.Blank(m.Text) {
				lines = append(lines, indented(m.Text))
			}
			for _, a := range m.Attachments {
				l := "   File: " + a.Path
				if a.ContentType != "" {
					l += " (" + a.ContentType + ")"
				}
				lines = append(lines, l)
			}
			lines = append(lines, unavailableLines(m.Unavailable)...)
		} else {
			lines = answerLines(*k.it.answer, reqs[k.it.answer.Ask], mode)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	res.Text = strings.Join(parts, "\n\n")
	return res
}

func answerLines(a umAnswer, r umRequest, mode string) []string {
	lines := []string{"Answer to \"" + a.Question + "\" (request " + a.Ask + "):"}
	if a.Status == "skipped" {
		return append(lines, "   Skipped: the user chose not to answer. Carry on without it; ask again only if something changes.")
	}
	if a.Choice != "" {
		label := "Choice"
		if r.Task {
			label = "Result"
		}
		lines = append(lines, "   "+label+": "+a.Choice)
	}
	paths := func(ids []string) string {
		var ps []string
		for _, id := range ids {
			for _, at := range a.Attachments {
				if at.ID == id {
					ps = append(ps, at.Path)
				}
			}
		}
		return strings.Join(ps, ", ")
	}
	for _, f := range r.Fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		switch f.Type {
		case core.FieldSecret:
			for _, s := range a.Secrets {
				if s == f.Name {
					if mode == secretsFile {
						lines = append(lines, "   "+f.Name+" saved ("+secretsLoadHint(secretsGOOS)+")")
					} else {
						lines = append(lines, "   "+f.Name+" saved (in your environment)")
					}
				}
			}
			continue
		case core.FieldFile:
			if ids := a.Files[f.Name]; len(ids) > 0 {
				lines = append(lines, "   "+label+": "+paths(ids))
			}
			continue
		}
		v, ok := a.Values[f.Name]
		if !ok {
			continue
		}
		switch {
		case f.Type == core.FieldLogs:
			lines = append(lines, "   "+label+":", indented(fenced(normalize(v))))
		case strings.Contains(noTrail(v), "\n"):
			lines = append(lines, "   "+label+":", indented(v))
		default:
			lines = append(lines, "   "+label+": "+noTrail(v))
		}
	}
	if a.Text != "" {
		lines = append(lines, indented(a.Text))
	}
	if r.Task {
		if ids := a.Files["note"]; len(ids) > 0 {
			lines = append(lines, "   Files: "+paths(ids))
		}
	}
	return append(lines, unavailableLines(a.Unavailable)...)
}

// unavailableLines tells the agent about files it did not get, so it can ask
// the user to send them again.
func unavailableLines(us []umUnavailable) []string {
	var lines []string
	for _, u := range us {
		lines = append(lines, "   File not received: "+u.Name+" ("+u.Reason+")")
	}
	return lines
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
func noTrail(s string) string { return strings.TrimRight(normalize(s), "\n") }

// indented: normalized, trailing line breaks removed, three spaces before
// each line that is not blank, blank lines empty.
func indented(s string) string {
	lines := strings.Split(noTrail(s), "\n")
	for i, l := range lines {
		if core.Blank(l) {
			lines[i] = ""
		} else {
			lines[i] = "   " + l
		}
	}
	return strings.Join(lines, "\n")
}

func fenced(code string) string {
	fence := "\x60\x60\x60"
	for strings.Contains(code, fence) {
		fence += "\x60"
	}
	return fence + "\n" + strings.TrimRight(code, "\r\n") + "\n" + fence
}

// question names a request: its title, else the first line of its text,
// cut to 80 characters.
func question(r umRequest) string {
	q := r.Title
	if q == "" {
		first := r.Text
		if i := strings.IndexAny(first, "\r\n"); i >= 0 {
			first = first[:i]
		}
		q = strings.TrimSpace(first)
	}
	if utf8.RuneCountInString(q) > 80 {
		q = string([]rune(q)[:79]) + "…"
	}
	return q
}

// safeName is a file name as one safe path segment (spec 4.5).
func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("/\\<>:\"|?*", r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	s := strings.TrimLeftFunc(b.String(), func(r rune) bool { return r == '.' || isSpace(r) })
	if r := []rune(s); len(r) > 128 {
		s = string(r[:128])
	}
	s = strings.TrimRightFunc(s, func(r rune) bool { return r == '.' || isSpace(r) })
	if s == "" {
		return "file"
	}
	if windowsReserved(s) {
		s = "_" + s
	}
	return s
}

// windowsReserved reports a name Windows refuses as a file whatever its
// extension: CON, PRN, AUX, NUL, COM1-9 and LPT1-9 ("nul.log", "CON.txt").
// Every system gets the same safe name, so paths match across machines.
func windowsReserved(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRightFunc(base, isSpace))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		switch base[3] {
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return true
		}
	}
	return false
}

func isSpace(r rune) bool { return core.Blank(string(r)) }

func userChatOf(p amp.Payload) (core.UserChat, bool) {
	raw, ok := p.Context["userchat"]
	if !ok {
		return core.UserChat{}, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return core.UserChat{}, false
	}
	var uc core.UserChat
	return uc, json.Unmarshal(b, &uc) == nil
}

// oldAskCutoff: asks sent before it were closed by anything the user wrote
// after them (the dashboard's rule before answers mail existed).
var oldAskCutoff = time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)

// taskFilesKey is where the dashboard puts a failed task's files.
const taskFilesKey = "details"

// askInfo is one of this agent's requests to the user.
type askInfo struct {
	req  umRequest
	chat core.UserChat
	at   time.Time
}

func (c *client) getUserMessages() (umResult, error) {
	user, err := c.userAddress()
	if err != nil {
		return umResult{}, err
	}
	all, err := c.history("all")
	if err != nil {
		return umResult{}, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i].Envelope.Timestamp, all[j].Envelope.Timestamp
		if !a.Equal(b) {
			return a.Before(b)
		}
		return all[i].ID < all[j].ID
	})
	asks := map[string]askInfo{}
	var askOrder []string
	closed := map[string]bool{}
	var fromUser []amp.Message
	for _, m := range all {
		p, err := payloadOf(m)
		if err != nil {
			continue
		}
		uc, ok := userChatOf(p)
		switch {
		case m.Envelope.To == user && ok && uc.Kind == "ask":
			asks[m.ID] = askInfo{req: umRequest{Title: uc.Title, Text: p.Message, Task: uc.Task, Fields: uc.Fields}, chat: uc, at: m.Envelope.Timestamp}
			askOrder = append(askOrder, m.ID)
		case m.Envelope.From == user && m.Envelope.To != user:
			fromUser = append(fromUser, m)
			if ok && uc.Kind == "answers" {
				for _, a := range uc.Answers {
					closed[a.Ask] = true
				}
			}
			if ok && uc.Kind == "decision" && m.Envelope.InReplyTo != "" {
				closed[m.Envelope.InReplyTo] = true
			}
		}
	}
	for id, a := range asks {
		if !a.at.Before(oldAskCutoff) {
			continue
		}
		for _, m := range fromUser {
			if m.Envelope.Timestamp.After(a.at) {
				closed[id] = true
				break
			}
		}
	}
	open := 0
	for _, id := range askOrder {
		if !closed[id] {
			open++
		}
	}
	home := agentHome()
	// Prepare first (download files), claim last: a slow download or a
	// failure while preparing never leaves mail claimed but unreturned. The
	// time budget keeps one call short of a client's timeout; whatever is
	// left stays unread for the next call.
	type prepared struct {
		id    string
		items []umItem
	}
	var ready []prepared
	more := false
	start := time.Now()
	for _, m := range fromUser {
		if m.Status == string(core.MailRead) {
			continue
		}
		p, err := payloadOf(m)
		if err != nil {
			continue
		}
		if len(ready) > 0 && time.Since(start) >= prepareBudget {
			more = true
			break
		}
		ready = append(ready, prepared{m.ID, c.prepare(m, p, asks, home)})
	}
	var items []umItem
	for _, r := range ready {
		claimed, err := c.claim(r.id)
		if err != nil {
			if len(items) > 0 {
				more = true
				break // what is left waits for the next call
			}
			return umResult{}, err
		}
		if claimed {
			items = append(items, r.items...)
		}
	}
	var notes []string
	mode := secretsMode()
	if mode == secretsFile {
		var names []string
		for _, it := range items {
			if it.answer != nil {
				names = append(names, it.answer.Secrets...)
			}
		}
		// The mail is claimed by now: a failed copy is reported, never
		// returned as an error that would lose the answers.
		if err := writeSecretsFile(home, names); err != nil {
			notes = append(notes, "The secrets could not be copied to "+secretsFilePath+" ("+err.Error()+"); they are saved in your env file.")
		}
	}
	if more {
		notes = append(notes, "More from the user is waiting: call get_user_messages again.")
	}
	reqs := map[string]umRequest{}
	for id, a := range asks {
		reqs[id] = a.req
	}
	res := assemble(reqs, items, open, mode)
	if len(notes) > 0 {
		res.Text += "\n\n" + strings.Join(notes, "\n")
		res.Structured["notes"] = notes
	}
	if more {
		res.Structured["more"] = true
	}
	return res, nil
}

// prepareBudget is how long one get_user_messages call spends fetching files
// before it returns what it has; the rest waits for the next call.
var prepareBudget = 20 * time.Second

// claim marks one mail read; false when another reader got it first.
func (c *client) claim(id string) (bool, error) {
	err := c.request("POST", "/v1/messages/"+url.PathEscape(id)+"/claim", nil, nil)
	var se *serverError
	if errors.As(err, &se) && se.status == 409 {
		return false, nil
	}
	return err == nil, err
}

// prepare turns one mail from the user into answers or a user message, with
// its files saved under .user-channel/files/.
// A file that can't be saved is listed as unavailable with the reason.
func (c *client) prepare(m amp.Message, p amp.Payload, asks map[string]askInfo, home string) []umItem {
	at := m.Envelope.Timestamp.UTC().Format(time.RFC3339Nano)
	byID := map[string]core.Attachment{}
	for _, a := range p.Attachments {
		byID[a.ID] = a
	}
	save := func(ids []string) ([]umAttachment, []string, []umUnavailable) {
		var out []umAttachment
		var kept []string
		var missing []umUnavailable
		for _, id := range ids {
			a, ok := byID[id]
			if !ok || a.ScanStatus == "rejected" {
				continue
			}
			if a.ScanStatus == "suspicious" {
				// As msg read: not saved without the user's say-so.
				missing = append(missing, umUnavailable{Name: a.Filename, Reason: "held back: the scan marked it suspicious (its contents don't match its type). Ask the user before opening it; if they agree, fetch it with lolad msg download " + m.ID + " --force"})
				continue
			}
			s, err := c.saveUserFile(a, home)
			if err != nil {
				missing = append(missing, umUnavailable{Name: a.Filename, Reason: fetchReason(err)})
				continue
			}
			out, kept = append(out, s), append(kept, id)
		}
		return out, kept, missing
	}
	uc, ok := userChatOf(p)
	switch {
	case ok && uc.Kind == "answers":
		var items []umItem
		for _, a := range uc.Answers {
			info := asks[a.Ask]
			ans := answerRecord(a, info.chat, at)
			ans.Question = question(info.req)
			if ans.Question == "" {
				ans.Question = "Request " + a.Ask
			}
			if len(ans.Files) > 0 {
				var order []string
				for _, f := range info.chat.Fields {
					if _, ok := ans.Files[f.Name]; ok {
						order = append(order, f.Name)
					}
				}
				if _, ok := ans.Files["note"]; ok {
					order = append(order, "note")
				}
				for _, k := range order {
					atts, kept, missing := save(ans.Files[k])
					ans.Attachments = append(ans.Attachments, atts...)
					ans.Unavailable = append(ans.Unavailable, missing...)
					if len(kept) == 0 {
						delete(ans.Files, k)
					} else {
						ans.Files[k] = kept
					}
				}
				for k := range ans.Files {
					if k != "note" && !fieldNamed(info.chat.Fields, k) {
						delete(ans.Files, k)
					}
				}
				if len(ans.Files) == 0 {
					ans.Files = nil
				}
			}
			items = append(items, umItem{answer: &ans})
		}
		return items
	case ok && uc.Kind == "decision" && m.Envelope.InReplyTo != "":
		info := asks[m.Envelope.InReplyTo]
		ans := answerRecord(core.AskAnswer{Ask: m.Envelope.InReplyTo, Decision: uc.Decision}, info.chat, at)
		ans.Question = question(info.req)
		if ans.Question == "" {
			ans.Question = "Request " + m.Envelope.InReplyTo
		}
		return []umItem{{answer: &ans}}
	}
	ids := make([]string, 0, len(p.Attachments))
	for _, a := range p.Attachments {
		ids = append(ids, a.ID)
	}
	atts, _, missing := save(ids)
	text := p.Message
	if len(p.Attachments) > 0 && derivedFilesBody(text, p.Attachments) {
		text = "" // the dashboard's "Sent 2 files: …" body for a message that is only files
	}
	return []umItem{{message: &umMessage{Spec: "0.1", ID: m.ID, Text: text, Attachments: atts, Unavailable: missing, CreatedAt: at}}}
}

// fetchReason is why a file could not be saved, in words for the agent.
func fetchReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"):
		return "it is no longer on the server; files are kept for 30 days. Ask the user to send it again"
	case strings.Contains(msg, "digest mismatch"):
		return "it arrived damaged and was discarded. Ask the user to send it again"
	}
	return "could not be saved: " + msg
}

func fieldNamed(fields []core.AskField, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// derivedFilesBody reports whether body is the text the dashboard writes for
// a message with files and no words ("Sent 2 files: a.png, b.pdf").
func derivedFilesBody(body string, atts []core.Attachment) bool {
	for _, a := range atts {
		if !strings.Contains(body, a.Filename) {
			return false
		}
	}
	return strings.HasPrefix(body, "Sent ") && !strings.Contains(body, "\n")
}

// answerRecord is an answer as the spec's record (3.6).
func answerRecord(a core.AskAnswer, ask core.UserChat, at string) umAnswer {
	r := umAnswer{Ask: a.Ask, Status: "answered", AnsweredAt: at}
	if a.Skipped {
		r.Status = "skipped"
		return r
	}
	r.Choice, r.Text = a.Decision, a.Text
	if ask.Task && a.Decision != "" && len(ask.Options) == 2 {
		switch a.Decision {
		case ask.Options[0]:
			r.Outcome = "done"
		case ask.Options[1]:
			r.Outcome = "failed"
		}
	}
	for _, f := range ask.Fields {
		switch f.Type {
		case core.FieldSecret, core.FieldFile:
			continue
		}
		if v, ok := a.Values[f.Name]; ok {
			if r.Values == nil {
				r.Values = map[string]string{}
			}
			r.Values[f.Name] = v
		}
	}
	if len(a.Secrets) > 0 {
		r.Secrets = append([]string(nil), a.Secrets...)
	}
	for k, ids := range a.Files {
		if len(ids) == 0 {
			continue
		}
		if k == taskFilesKey && ask.Task {
			k = "note"
		}
		if r.Files == nil {
			r.Files = map[string][]string{}
		}
		r.Files[k] = append([]string(nil), ids...)
	}
	return r
}

// saveUserFile downloads one file the user sent to
// .user-channel/files/<id>/<safe name> and returns it as an attachment.
func (c *client) saveUserFile(a core.Attachment, home string) (umAttachment, error) {
	if a.Storage != "" && a.Storage != "provider" {
		return umAttachment{}, fmt.Errorf("%s: %s attachments can't be saved here", a.Filename, a.Storage)
	}
	root := filepath.Join(home, ".user-channel")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return umAttachment{}, err
	}
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte("# lolad: files and secrets the user sent; keep them out of git\n*\n"), 0o600)
	id := safeName(a.ID)
	name := safeName(a.Filename)
	dir := filepath.Join(root, "files", id)
	path := filepath.Join(dir, name)
	if have, err := fileDigest(path); err != nil || have != a.Digest {
		if err := c.download(a, dir, path); err != nil {
			return umAttachment{}, err
		}
	}
	return umAttachment{ID: a.ID, Name: a.Filename, ContentType: a.ContentType, Size: a.Size, Path: ".user-channel/files/" + id + "/" + name}, nil
}

// writeSecretsFile copies the named secrets from the agent's env file into
// .user-channel/secrets.env (spec 3.7, the file way), one export line each.
func writeSecretsFile(home string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	envFile := os.Getenv("LOLAD_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if !filepath.IsAbs(envFile) {
		envFile = filepath.Join(home, envFile)
	}
	values := readEnvFile(envFile)
	path := filepath.Join(home, secretsFilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(path), ".gitignore"), []byte("# lolad: files and secrets the user sent; keep them out of git\n*\n"), 0o600)
	existing := map[string]string{}
	var order []string
	if b, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if k, _, ok := strings.Cut(strings.TrimPrefix(l, "export "), "="); ok && core.ValidEnvKey(k) {
				if _, seen := existing[k]; !seen {
					order = append(order, k)
				}
				existing[k] = l
			}
		}
	}
	for _, n := range names {
		v, ok := values[n]
		if !ok {
			continue
		}
		if _, seen := existing[n]; !seen {
			order = append(order, n)
		}
		existing[n] = "export " + n + "='" + strings.ReplaceAll(v, "'", "'\\''") + "'"
	}
	var b strings.Builder
	for _, k := range order {
		b.WriteString(existing[k] + "\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readEnvFile reads KEY=VALUE lines as lolad writes them (values quoted with
// Go quoting when they hold spaces or quotes).
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
		k, v, ok := strings.Cut(l, "=")
		if !ok || !core.ValidEnvKey(strings.TrimSpace(k)) {
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(v, "\""):
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
		case len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'"):
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
