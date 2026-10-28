package agentcfg

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Fallback is the process boundary used for accounts lolad did not provision:
// supervisor.ExecMailWatch, which runs `tools/email.sh watch <account>`.
type mailFallback interface {
	Watch(context.Context, string, string) ([]byte, error)
}

// MailWatcher checks a provisioned mailbox itself, so a mail trigger works
// without the agent's own email tools. It prints the same last JSON line as
// `tools/email.sh watch`: {"account", "initialized", "new":[{id, uid, from,
// subject, date, trust}]}. The first check records the inbox and reports
// nothing; later checks report mail whose UID is above the last one seen. It
// opens INBOX read-only and fetches headers with BODY.PEEK, so nothing is
// marked read. `id` is the message sequence number, which `email.sh read`
// takes. trust is "operator" when the sender is in email.operator_emails.
type MailWatcher struct {
	StateDir string
	Fallback mailFallback
	trust    *tls.Config // tests
}

type watchedMail struct {
	ID      string `json:"id"`
	UID     uint32 `json:"uid"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Trust   string `json:"trust"`
}
type watchResult struct {
	Account     string        `json:"account"`
	Initialized bool          `json:"initialized"`
	New         []watchedMail `json:"new"`
}
type watchState struct {
	LastUID     uint32 `json:"last_uid"`
	UIDValidity uint32 `json:"uidvalidity"`
}

// maxReported bounds one check; anything older is still marked seen.
const maxReported = 25

func (w MailWatcher) Watch(ctx context.Context, dir, account string) ([]byte, error) {
	d := New(dir, core.HarnessClaude, Options{})
	envFile := d.runtimeEnvFile()
	settings, e := d.Email(envFile)
	if e != nil {
		return nil, e
	}
	var acct *core.EmailAccount
	for i := range settings.Accounts {
		if strings.EqualFold(settings.Accounts[i].Address, account) {
			a := defaultAccount(settings.Accounts[i])
			acct = &a
		}
	}
	if acct == nil || acct.IMAPServer == "" {
		if w.Fallback != nil {
			return w.Fallback.Watch(ctx, dir, account)
		}
		return nil, fmt.Errorf("%w: %s is not in lolabot.yaml email.accounts", core.ErrNotFound, account)
	}
	password, e := d.EmailPassword(envFile, acct.Address)
	if e != nil {
		return nil, e
	}
	if password == "" {
		return nil, fmt.Errorf("no password stored for %s", acct.Address)
	}
	statePath := w.statePath(dir, acct.Address)
	var st *watchState
	if b, e := os.ReadFile(statePath); e == nil {
		st = &watchState{}
		if json.Unmarshal(b, st) != nil {
			st = nil
		}
	}
	operators := map[string]bool{}
	for _, o := range settings.OperatorEmails {
		operators[strings.ToLower(strings.TrimSpace(o))] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, next, e := imapWatch(ctx, *acct, password, st, operators, w.trust)
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(next)
	if e = os.MkdirAll(filepath.Dir(statePath), 0700); e != nil {
		return nil, e
	}
	if e = atomicWrite(statePath, b, 0600); e != nil {
		return nil, e
	}
	out, e := json.Marshal(result)
	return append(out, '\n'), e
}
func (w MailWatcher) statePath(dir, account string) string {
	sum := sha256.Sum256([]byte(dir + "\x00" + strings.ToLower(account)))
	return filepath.Join(w.StateDir, "mail-watch", hex.EncodeToString(sum[:12])+".json")
}

// runtimeEnvFile reads runtime.env_file from lolabot.yaml ("" = .env).
func (d *Dir) runtimeEnvFile() string {
	_, n, e := d.readYAML("lolabot.yaml")
	if e != nil {
		return ""
	}
	var v string
	decode(nodeGet(nodeGet(n, "runtime"), "env_file"), &v)
	return v
}

var (
	respCode = regexp.MustCompile(`\[(UIDVALIDITY|UIDNEXT) (\d+)\]`)
	literal  = regexp.MustCompile(`\{(\d+)\}\r?\n$`)
	fetchSeq = regexp.MustCompile(`^\* (\d+) FETCH `)
	fetchUID = regexp.MustCompile(`\bUID (\d+)`)
)

type imapSession struct {
	w   io.Writer
	r   *bufio.Reader
	tag int
}

// command sends one tagged command and returns its untagged responses, each
// with any literals inlined, until the tagged reply. A non-OK reply is an error.
func (s *imapSession) command(format string, args ...any) ([]string, error) {
	s.tag++
	tag := "w" + strconv.Itoa(s.tag)
	if _, e := fmt.Fprintf(s.w, tag+" "+format+"\r\n", args...); e != nil {
		return nil, e
	}
	var out []string
	for len(out) < 10000 {
		line, e := s.r.ReadString('\n')
		if e != nil {
			return nil, e
		}
		for m := literal.FindStringSubmatch(line); m != nil; m = literal.FindStringSubmatch(line) {
			n, _ := strconv.Atoi(m[1])
			if n > 1<<20 {
				return nil, errors.New("IMAP literal too large")
			}
			buf := make([]byte, n)
			if _, e = io.ReadFull(s.r, buf); e != nil {
				return nil, e
			}
			rest, e := s.r.ReadString('\n')
			if e != nil {
				return nil, e
			}
			line = line[:len(line)-len(m[0])] + "\n" + string(buf) + rest
		}
		if strings.HasPrefix(line, tag+" ") {
			if !strings.HasPrefix(strings.ToUpper(line[len(tag)+1:]), "OK") {
				return out, fmt.Errorf("IMAP %s failed", strings.Fields(format)[0])
			}
			return out, nil
		}
		out = append(out, line)
	}
	return nil, errors.New("IMAP response limit")
}

func imapWatch(ctx context.Context, a core.EmailAccount, password string, st *watchState, operators map[string]bool, trust *tls.Config) (watchResult, watchState, error) {
	res := watchResult{Account: a.Address, New: []watchedMail{}}
	if strings.ContainsAny(a.Username+password, "\r\n\x00") {
		return res, watchState{}, core.ErrInvalid
	}
	c, e := mailConn(ctx, a.IMAPServer, a.IMAPPort, true, trust)
	if e != nil {
		return res, watchState{}, e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	if t, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(t)
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	s := &imapSession{w: c, r: bufio.NewReaderSize(c, 65536)}
	if line, e := s.r.ReadString('\n'); e != nil || !strings.HasPrefix(strings.ToUpper(line), "* OK") {
		return res, watchState{}, errors.New("IMAP greeting failed")
	}
	q := func(v string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"` }
	if _, e = s.command("LOGIN %s %s", q(a.Username), q(password)); e != nil {
		return res, watchState{}, errors.New("IMAP authentication failed")
	}
	lines, e := s.command("EXAMINE INBOX")
	if e != nil {
		return res, watchState{}, e
	}
	var validity, uidNext uint64
	for _, l := range lines {
		if m := respCode.FindStringSubmatch(l); m != nil {
			v, _ := strconv.ParseUint(m[2], 10, 32)
			if m[1] == "UIDVALIDITY" {
				validity = v
			} else {
				uidNext = v
			}
		}
	}
	next := watchState{UIDValidity: uint32(validity)}
	if st == nil || st.UIDValidity != next.UIDValidity {
		res.Initialized = true
		if uidNext > 0 {
			next.LastUID = uint32(uidNext - 1)
		}
		_, _ = s.command("LOGOUT")
		return res, next, nil
	}
	next.LastUID = st.LastUID
	lines, e = s.command("UID SEARCH UID %d:*", uint64(st.LastUID)+1)
	if e != nil {
		return res, watchState{}, e
	}
	var uids []uint32
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 2 && strings.EqualFold(f[1], "SEARCH") {
			for _, v := range f[2:] {
				// "n:*" always matches the newest message, even when it is old.
				if u, e := strconv.ParseUint(v, 10, 32); e == nil && uint32(u) > st.LastUID {
					uids = append(uids, uint32(u))
				}
			}
		}
	}
	for i, uid := range uids {
		if uid > next.LastUID {
			next.LastUID = uid
		}
		if i >= maxReported {
			continue
		}
		lines, e := s.command("UID FETCH %d (UID BODY.PEEK[HEADER.FIELDS (FROM SUBJECT DATE)])", uid)
		if e != nil {
			return res, watchState{}, e
		}
		for _, l := range lines {
			m := fetchSeq.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			// Only the reply carrying our header literal; servers may also send
			// unsolicited FETCH flag updates. UID may come before or after it.
			head, body, ok := strings.Cut(l, "\n")
			if !ok {
				continue
			}
			tail := strings.TrimRight(body, "\r\n")
			tail = tail[strings.LastIndex(tail, "\n")+1:]
			want := strconv.FormatUint(uint64(uid), 10)
			if u := fetchUID.FindStringSubmatch(head); u == nil || u[1] != want {
				if u = fetchUID.FindStringSubmatch(tail); u == nil || u[1] != want {
					continue
				}
			}
			msg, e := mail.ReadMessage(strings.NewReader(body + "\r\n"))
			if e != nil {
				continue
			}
			dec := new(mime.WordDecoder)
			from, e := dec.DecodeHeader(msg.Header.Get("From"))
			if e != nil {
				from = msg.Header.Get("From")
			}
			subject, e := dec.DecodeHeader(msg.Header.Get("Subject"))
			if e != nil {
				subject = msg.Header.Get("Subject")
			}
			if subject == "" {
				subject = "(no subject)"
			}
			trustLevel := "external"
			if addr, e := mail.ParseAddress(from); e == nil && operators[strings.ToLower(addr.Address)] {
				trustLevel = "operator"
			}
			res.New = append(res.New, watchedMail{ID: m[1], UID: uid, From: from, Subject: subject, Date: msg.Header.Get("Date"), Trust: trustLevel})
		}
	}
	_, _ = s.command("LOGOUT")
	return res, next, nil
}
