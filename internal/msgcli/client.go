// Package msgcli is the agent-side lolad msg client. Mail travels exclusively
// through the provider HTTP API; local files are read only for credentials.
package msgcli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
)

const help = `lolad msg [--as name] <command>
  inbox [--all] [--json]
  read <id|last8>            (saves attached files to attachments/<id>/)
  download <id|last8> [--dest DIR] [--force]
  send <to> "<subject>" "<body>" [--type request] [--priority high] [--context '{json}'] [--attach FILE]...
  reply <id|last8> "<body>" [--attach FILE]...
  thread <id|last8>
  who
  tell-user say|update|deliver "<text>" [--attach FILE]...
  tell-user ask "<question>" --option "Yes" --option "No"
`

type client struct {
	ctx         context.Context
	base        string
	credentials amp.Credentials
	key         ed25519.PrivateKey
	http        *http.Client
	out         io.Writer
}
type serverError struct {
	status int
	reason string
}

func (e *serverError) Error() string { return e.reason }
func (c *client) request(method, path string, body any, result any) error {
	var b []byte
	var e error
	if body != nil {
		b, e = json.Marshal(body)
		if e != nil {
			return e
		}
	}
	r, e := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.credentials.APIKey)
	r.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var v struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v)
		if v.Message == "" {
			v.Message = v.Error
		}
		if v.Message == "" {
			v.Message = resp.Status
		}
		return &serverError{status: resp.StatusCode, reason: v.Message}
	}
	if result == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16<<20))
	dec.UseNumber()
	return dec.Decode(result)
}

// Run returns 2 for policy/signature denials (HTTP 403), 1 for other errors.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fail := func(e error) int {
		fmt.Fprintln(stderr, e)
		var se *serverError
		if errors.As(e, &se) && se.status == 403 {
			return 2
		}
		return 1
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, help)
		return 0
	}
	name := os.Getenv("LOLAD_AGENT")
	remaining := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--as" {
			i++
			if i >= len(args) {
				return fail(fmt.Errorf("--as requires a name"))
			}
			name = args[i]
		} else if strings.HasPrefix(args[i], "--as=") {
			name = strings.TrimPrefix(args[i], "--as=")
		} else {
			remaining = append(remaining, args[i])
		}
	}
	args = remaining
	if len(args) == 0 {
		return fail(fmt.Errorf("command required"))
	}
	if args[0] == "help" {
		fmt.Fprint(stdout, help)
		return 0
	}
	if name == "" {
		return fail(fmt.Errorf("set LOLAD_AGENT or use --as <name>"))
	}
	name = strings.ToLower(name)
	state := os.Getenv("LOLAD_STATE")
	if state == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return fail(e)
		}
		state = filepath.Join(home, ".local", "share", "lolad")
	}
	creds, key, e := amp.ReadCredentials(state, name)
	if e != nil {
		return fail(fmt.Errorf("identity %s: %w", name, e))
	}
	base := strings.TrimRight(os.Getenv("LOLAD_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:4100"
	}
	c := &client{ctx: ctx, base: base, credentials: creds, key: key, http: &http.Client{Timeout: 15 * time.Second}, out: stdout}
	if e = c.run(args); e != nil {
		return fail(e)
	}
	return 0
}

// parseOptions accepts flags before or after positional arguments. Repeated
// --option is retained for user choice buttons.
func parseOptions(args []string, allowed map[string]bool) ([]string, map[string][]string, error) {
	pos := []string{}
	opts := map[string][]string{}
	for i := 0; i < len(args); i++ {
		s := args[i]
		if s == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(s, "--") {
			k, v, has := strings.Cut(strings.TrimPrefix(s, "--"), "=")
			takes, ok := allowed[k]
			if !ok {
				return nil, nil, fmt.Errorf("unknown flag --%s", k)
			}
			if takes && !has {
				i++
				if i >= len(args) {
					return nil, nil, fmt.Errorf("--%s requires a value", k)
				}
				v = args[i]
			}
			if !takes {
				v = "true"
			}
			opts[k] = append(opts[k], v)
		} else {
			pos = append(pos, s)
		}
	}
	return pos, opts, nil
}
func option(opts map[string][]string, k, def string) string {
	v := opts[k]
	if len(v) == 0 {
		return def
	}
	return v[len(v)-1]
}
func (c *client) history(box string) ([]amp.Message, error) {
	out := []amp.Message{}
	before := ""
	for {
		path := "/v1/messages?limit=1000&box=" + url.QueryEscape(box)
		if before != "" {
			path += "&before=" + url.QueryEscape(before)
		}
		var v struct {
			Messages []amp.Message `json:"messages"`
		}
		if e := c.request("GET", path, nil, &v); e != nil {
			return nil, e
		}
		out = append(out, v.Messages...)
		if len(v.Messages) < 1000 {
			return out, nil
		}
		next := v.Messages[len(v.Messages)-1].Envelope.Timestamp.Format(time.RFC3339Nano)
		if next == before {
			return nil, fmt.Errorf("history pagination made no progress")
		}
		before = next
	}
}
func (c *client) find(id string) (amp.Message, error) {
	if strings.HasPrefix(id, "msg_") {
		var m amp.Message
		e := c.request("GET", "/v1/messages/"+url.PathEscape(id), nil, &m)
		return m, e
	}
	if len(id) != 8 {
		return amp.Message{}, fmt.Errorf("use a full message ID or its last 8 characters")
	}
	msgs, e := c.history("all")
	if e != nil {
		return amp.Message{}, e
	}
	var found amp.Message
	count := 0
	for _, m := range msgs {
		if strings.HasSuffix(m.ID, id) {
			found = m
			count++
		}
	}
	if count == 0 {
		return found, fmt.Errorf("message %s not found", id)
	}
	if count > 1 {
		return found, fmt.Errorf("ambiguous message suffix %s", id)
	}
	return found, nil
}
func payloadOf(m amp.Message) (amp.Payload, error) {
	var p amp.Payload
	dec := json.NewDecoder(bytes.NewReader(m.Payload))
	dec.UseNumber()
	return p, dec.Decode(&p)
}

// printMessage shows a message; files lists saved attachments (nil = list
// them with how to save).
func (c *client) printMessage(m amp.Message, files ...saved) error {
	p, e := payloadOf(m)
	if e != nil {
		return e
	}
	_, localDomain, _ := amp.ParseAddress(c.credentials.Address)
	_, remoteDomain, _ := amp.ParseAddress(m.Envelope.From)
	if remoteDomain != "" && remoteDomain != localDomain {
		display := remoteDomain
		if entries, err := c.directory(); err == nil {
			for _, v := range entries {
				if v.Peer == remoteDomain && v.PeerDisplay != "" {
					display = v.PeerDisplay
					break
				}
			}
		}
		fmt.Fprintf(c.out, "External — from %s via %s's lolad\n", m.Envelope.From, display)
	}
	fmt.Fprintf(c.out, "%s\nFrom: %s\nTo: %s\nSubject: %s\nType: %s  Priority: %s\nThread: %s\n\n%s\n", m.ID, m.Envelope.From, m.Envelope.To, m.Envelope.Subject, p.Type, m.Envelope.Priority, m.Envelope.ThreadID, p.Message)
	if len(p.Context) > 0 {
		b, _ := json.Marshal(p.Context)
		fmt.Fprintf(c.out, "Context: %s\n", b)
	}
	if files == nil && len(p.Attachments) > 0 {
		for _, a := range p.Attachments {
			files = append(files, saved{Attachment: a, Note: "save with lolad msg read " + m.ID})
		}
	}
	printAttachments(c.out, files)
	return nil
}

// receive saves the files of a message addressed to us.
func (c *client) receive(m amp.Message, force bool, dest string) ([]saved, error) {
	p, e := payloadOf(m)
	if e != nil || len(p.Attachments) == 0 {
		return nil, e
	}
	if dest == "" {
		home := agentHome()
		ignoreAttachments(home)
		dest = filepath.Join(home, "attachments")
	}
	return c.saveAttachments(m.ID, p.Attachments, dest, force), nil
}
func (c *client) send(to, subject, body, typ, priority, reply string, context map[string]any, attach []string) error {
	_, domain, e := amp.ParseAddress(c.credentials.Address)
	if e != nil {
		return e
	}
	if !strings.Contains(to, "@") {
		to = strings.ToLower(to) + "@" + domain
	} else {
		to = strings.ToLower(to)
	}
	files, e := c.uploadAll(attach)
	if e != nil {
		return e
	}
	payload, e := amp.CanonicalJSON(amp.Payload{Type: typ, Message: body, Context: context, Attachments: files})
	if e != nil {
		return e
	}
	env := amp.Envelope{From: c.credentials.Address, To: to, Subject: subject, Priority: priority, InReplyTo: reply}
	sig, e := amp.Sign(c.key, env, json.RawMessage(payload))
	if e != nil {
		return e
	}
	var res struct {
		ID string `json:"id"`
	}
	if e = c.request("POST", "/v1/route", amp.RouteRequest{To: to, Subject: subject, Priority: priority, InReplyTo: reply, Signature: sig, Payload: payload}, &res); e != nil {
		return e
	}
	fmt.Fprintln(c.out, res.ID)
	return nil
}

type entry struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Kind        string `json:"kind"`
	Display     string `json:"display"`
	Role        string `json:"role"`
	Team        string `json:"team"`
	MayMessage  bool   `json:"may_message"`
	Remote      bool   `json:"remote"`
	Peer        string `json:"peer"`
	PeerDisplay string `json:"peer_display"`
}

func (c *client) directory() ([]entry, error) {
	var v struct {
		Agents []entry `json:"agents"`
	}
	e := c.request("GET", "/v1/agents", nil, &v)
	return v.Agents, e
}
func (c *client) run(args []string) error {
	switch args[0] {
	case "inbox":
		pos, opts, e := parseOptions(args[1:], map[string]bool{"all": false, "json": false})
		if e != nil {
			return e
		}
		if len(pos) > 0 {
			return fmt.Errorf("usage: inbox [--all] [--json]")
		}
		var msgs []amp.Message
		if option(opts, "all", "") != "" {
			msgs, e = c.history("inbox")
		} else { // Paging all history and filtering avoids silently truncating unread mail.
			msgs, e = c.history("inbox")
			if e == nil {
				unread := []amp.Message{}
				for _, m := range msgs {
					if m.Status != "read" {
						unread = append(unread, m)
					}
				}
				msgs = unread
			}
		}
		if e != nil {
			return e
		}
		if option(opts, "json", "") != "" {
			return json.NewEncoder(c.out).Encode(msgs)
		}
		if len(msgs) == 0 {
			fmt.Fprintln(c.out, "No messages.")
		}
		for _, m := range msgs {
			fmt.Fprintf(c.out, "%s  %s  %s  %s\n", m.ID, m.Status, m.Envelope.From, m.Envelope.Subject)
		}
		return nil
	case "read":
		if len(args) != 2 {
			return fmt.Errorf("usage: read <id|last8>")
		}
		m, e := c.find(args[1])
		if e != nil {
			return e
		}
		if m.Envelope.To != c.credentials.Address {
			return c.printMessage(m)
		}
		if e = c.request("POST", "/v1/messages/"+url.PathEscape(m.ID)+"/read", nil, &m); e != nil {
			return e
		}
		files, e := c.receive(m, false, "")
		if e != nil {
			return e
		}
		return c.printMessage(m, files...)
	case "download":
		pos, opts, e := parseOptions(args[1:], map[string]bool{"dest": true, "force": false})
		if e != nil {
			return e
		}
		if len(pos) != 1 {
			return fmt.Errorf("usage: download <id|last8> [--dest DIR] [--force]")
		}
		m, e := c.find(pos[0])
		if e != nil {
			return e
		}
		files, e := c.receive(m, option(opts, "force", "") != "", option(opts, "dest", ""))
		if e != nil {
			return e
		}
		if len(files) == 0 {
			fmt.Fprintln(c.out, "No attachments.")
		}
		printAttachments(c.out, files)
		return nil
	case "send":
		pos, opts, e := parseOptions(args[1:], map[string]bool{"type": true, "priority": true, "context": true, "attach": true})
		if e != nil {
			return e
		}
		if len(pos) != 3 {
			return fmt.Errorf("usage: send <to> <subject> <body> [--type request] [--priority high] [--context JSON] [--attach FILE]...")
		}
		var context map[string]any
		if raw := option(opts, "context", ""); raw != "" {
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			if e = dec.Decode(&context); e != nil {
				return fmt.Errorf("context: %w", e)
			}
			if context == nil {
				return fmt.Errorf("context must be a JSON object")
			}
		}
		return c.send(pos[0], pos[1], pos[2], option(opts, "type", "notification"), option(opts, "priority", "normal"), "", context, opts["attach"])
	case "reply":
		pos, opts, e := parseOptions(args[1:], map[string]bool{"attach": true})
		if e != nil {
			return e
		}
		if len(pos) != 2 {
			return fmt.Errorf("usage: reply <id|last8> <body> [--attach FILE]...")
		}
		m, e := c.find(pos[0])
		if e != nil {
			return e
		}
		to := m.Envelope.From
		if to == c.credentials.Address {
			to = m.Envelope.To
		}
		subject := m.Envelope.Subject
		if !strings.HasPrefix(subject, "Re: ") {
			subject = "Re: " + subject
		}
		return c.send(to, subject, pos[1], "response", "normal", m.ID, nil, opts["attach"])
	case "thread":
		if len(args) != 2 {
			return fmt.Errorf("usage: thread <id|last8>")
		}
		m, e := c.find(args[1])
		if e != nil {
			return e
		}
		msgs, e := c.history("all")
		if e != nil {
			return e
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].Envelope.Timestamp.Before(msgs[j].Envelope.Timestamp) })
		for _, v := range msgs {
			if v.Envelope.ThreadID == m.Envelope.ThreadID {
				if e = c.printMessage(v); e != nil {
					return e
				}
				fmt.Fprintln(c.out)
			}
		}
		return nil
	case "who":
		if len(args) != 1 {
			return fmt.Errorf("usage: who")
		}
		entries, e := c.directory()
		if e != nil {
			return e
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].Peer != entries[j].Peer {
				return entries[i].Peer < entries[j].Peer
			}
			return entries[i].Address < entries[j].Address
		})
		previous := ""
		for _, v := range entries {
			if v.MayMessage {
				if v.Remote && v.Peer != previous {
					display := v.PeerDisplay
					if display == "" {
						display = v.Peer
					}
					fmt.Fprintf(c.out, "\n%s's lolad (%s):\n", display, v.Peer)
					previous = v.Peer
				}
				fmt.Fprintf(c.out, "%s  %s  %s  %s\n", v.Address, v.Kind, v.Role, v.Team)
			}
		}
		return nil
	case "tell-user":
		pos, opts, e := parseOptions(args[1:], map[string]bool{"option": true, "subject": true, "attach": true})
		if e != nil {
			return e
		}
		if len(pos) != 2 {
			return fmt.Errorf("usage: tell-user say|update|deliver|ask <text> [--option text]")
		}
		kind := pos[0]
		switch kind {
		case "say", "update", "deliver", "ask":
		default:
			return fmt.Errorf("invalid tell-user kind %s", kind)
		}
		if kind != "ask" && len(opts["option"]) > 0 {
			return fmt.Errorf("--option is only valid for ask")
		}
		entries, e := c.directory()
		if e != nil {
			return e
		}
		to := ""
		for _, v := range entries {
			if v.Kind == "human" && !v.Remote {
				to = v.Address
				break
			}
		}
		if to == "" {
			return fmt.Errorf("user identity not found")
		}
		typ := "notification"
		if kind == "ask" {
			typ = "request"
		}
		if kind == "update" {
			typ = "update"
		}
		return c.send(to, option(opts, "subject", subjectFromText(pos[1])), pos[1], typ, "normal", "", map[string]any{"userchat": core.UserChat{Kind: kind, Options: opts["option"]}}, opts["attach"])
	default:
		return fmt.Errorf("unknown msg command %q; use lolad msg help", args[0])
	}
}

// subjectFromText uses a readable first line without splitting Unicode characters.
func subjectFromText(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}
