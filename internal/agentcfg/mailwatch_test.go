package agentcfg

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type fakeMail struct {
	uid           int
	from, subject string
}

// fakeMailbox serves just enough IMAP for MailWatcher, with UIDs after the
// header literal to exercise that ordering.
type fakeMailbox struct {
	mu       sync.Mutex
	validity int
	msgs     []fakeMail
	commands []string
}

func (b *fakeMailbox) serve(c net.Conn) {
	fmt.Fprint(c, "* OK test server\r\n")
	r := bufio.NewReader(c)
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return
		}
		tag, cmd, _ := strings.Cut(strings.TrimSpace(line), " ")
		b.mu.Lock()
		b.commands = append(b.commands, cmd)
		msgs := append([]fakeMail(nil), b.msgs...)
		validity := b.validity
		b.mu.Unlock()
		switch {
		case strings.HasPrefix(cmd, "LOGIN"):
			if !strings.Contains(cmd, `"s3cret"`) {
				fmt.Fprintf(c, "%s NO bad login\r\n", tag)
				return
			}
			fmt.Fprintf(c, "%s OK logged in\r\n", tag)
		case cmd == "EXAMINE INBOX":
			next := 1
			if len(msgs) > 0 {
				next = msgs[len(msgs)-1].uid + 1
			}
			fmt.Fprintf(c, "* %d EXISTS\r\n* OK [UIDVALIDITY %d] ok\r\n* OK [UIDNEXT %d] ok\r\n%s OK [READ-ONLY] done\r\n", len(msgs), validity, next, tag)
		case strings.HasPrefix(cmd, "UID SEARCH UID "):
			var from int
			fmt.Sscanf(strings.TrimPrefix(cmd, "UID SEARCH UID "), "%d:*", &from)
			var hits []string
			for _, m := range msgs {
				if m.uid >= from || m.uid == msgs[len(msgs)-1].uid {
					hits = append(hits, fmt.Sprint(m.uid))
				}
			}
			fmt.Fprintf(c, "* SEARCH %s\r\n%s OK done\r\n", strings.Join(hits, " "), tag)
		case strings.HasPrefix(cmd, "UID FETCH "):
			var uid int
			fmt.Sscanf(strings.TrimPrefix(cmd, "UID FETCH "), "%d", &uid)
			for i, m := range msgs {
				if m.uid == uid {
					head := "From: " + m.from + "\r\nSubject: " + m.subject + "\r\nDate: Tue, 27 Oct 2026 15:00:00 -0500\r\n\r\n"
					fmt.Fprintf(c, "* %d FETCH (FLAGS (\\Seen))\r\n", i+1)
					fmt.Fprintf(c, "* %d FETCH (BODY[HEADER.FIELDS (FROM SUBJECT DATE)] {%d}\r\n%s UID %d)\r\n", i+1, len(head), head, uid)
				}
			}
			fmt.Fprintf(c, "%s OK done\r\n", tag)
		case cmd == "LOGOUT":
			fmt.Fprintf(c, "* BYE\r\n%s OK bye\r\n", tag)
			return
		default:
			fmt.Fprintf(c, "%s BAD unknown\r\n", tag)
		}
	}
}

func TestMailWatcher(t *testing.T) {
	serverTLS, clientTLS := mailTLS(t)
	box := &fakeMailbox{validity: 7, msgs: []fakeMail{{uid: 3, from: "old@example.com", subject: "old"}}}
	port := listenMail(t, serverTLS, false, box.serve)
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", fmt.Sprintf("agent: {name: Test}\nemail:\n  accounts:\n    - address: agent@example.org\n      config_key: agent\n      imap_server: 127.0.0.1\n      imap_port: %d\n      smtp_server: 127.0.0.1\n      smtp_port: 465\n  operator_emails:\n    - Boss@Example.com\nruntime:\n  harness: claude\n  env_file: secrets.env\n", port))
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  agent:\n    username: agent@example.org\n    password_secret: EMAIL_PASSWORD_AGENT\n")
	put(t, d.root, "secrets.env", "EMAIL_PASSWORD_AGENT=s3cret\n")
	w := MailWatcher{StateDir: t.TempDir(), trust: clientTLS}
	check := func() watchResult {
		t.Helper()
		out, e := w.Watch(context.Background(), d.root, "agent@example.org")
		if e != nil {
			t.Fatal(e)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var r watchResult
		if e = json.Unmarshal([]byte(lines[len(lines)-1]), &r); e != nil {
			t.Fatal(e, string(out))
		}
		return r
	}
	if r := check(); !r.Initialized || len(r.New) != 0 {
		t.Fatalf("first check must only record the inbox: %+v", r)
	}
	if r := check(); r.Initialized || len(r.New) != 0 {
		t.Fatalf("old mail announced: %+v", r)
	}
	box.mu.Lock()
	box.msgs = append(box.msgs, fakeMail{uid: 5, from: "Boss <boss@example.com>", subject: "=?UTF-8?Q?Caf=C3=A9_plan?="}, fakeMail{uid: 6, from: "stranger@example.net", subject: "hi"})
	box.mu.Unlock()
	r := check()
	if len(r.New) != 2 || r.New[0].ID != "2" || r.New[0].UID != 5 || r.New[0].Subject != "Café plan" || r.New[0].Trust != "operator" || r.New[1].Trust != "external" {
		t.Fatalf("%+v", r)
	}
	if r = check(); len(r.New) != 0 {
		t.Fatalf("mail announced twice: %+v", r)
	}
	box.mu.Lock()
	box.validity = 8
	for _, c := range box.commands {
		if strings.Contains(c, "SELECT") || strings.Contains(c, "STORE") || (strings.Contains(c, "FETCH") && !strings.Contains(c, "BODY.PEEK")) {
			t.Fatalf("watch could mark mail read: %s", c)
		}
	}
	box.mu.Unlock()
	if r = check(); !r.Initialized || len(r.New) != 0 {
		t.Fatalf("new UIDVALIDITY must re-baseline, not replay the inbox: %+v", r)
	}
}

type fallbackWatch struct{ called string }

func (f *fallbackWatch) Watch(_ context.Context, dir, account string) ([]byte, error) {
	f.called = account
	return []byte(`{"new":[]}`), nil
}

func TestMailWatcherFallsBackForUnknownAccounts(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "agent: {name: Test}\n")
	fb := &fallbackWatch{}
	w := MailWatcher{StateDir: t.TempDir(), Fallback: fb}
	if _, e := w.Watch(context.Background(), d.root, "custom@example.org"); e != nil || fb.called != "custom@example.org" {
		t.Fatalf("custom watch not used: %v %q", e, fb.called)
	}
	w.Fallback = nil
	if _, e := w.Watch(context.Background(), d.root, "custom@example.org"); e == nil {
		t.Fatal("unknown account without a fallback must fail")
	}
	_ = core.ErrNotFound
}
