package agentcfg

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestEmail(ctx context.Context, a core.EmailAccount, password string) core.EmailTest {
	return testEmail(ctx, a, password, nil)
}
func testEmail(ctx context.Context, a core.EmailAccount, password string, trust *tls.Config) core.EmailTest {
	a = defaultAccount(a)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := core.EmailTest{}
	type result struct {
		imap bool
		err  error
	}
	ch := make(chan result, 2)
	go func() { ch <- result{true, imapLogin(ctx, a, password, trust)} }()
	go func() { ch <- result{false, smtpLogin(ctx, a, password, trust)} }()
	for i := 0; i < 2; i++ {
		r := <-ch
		s := "ok"
		if r.err != nil {
			s = "connection or authentication failed"
			if errors.Is(r.err, context.DeadlineExceeded) || ctx.Err() != nil {
				s = "connection timed out or canceled"
			}
		}
		if r.imap {
			out.IMAP = s
		} else {
			out.SMTP = s
		}
	}
	out.OK = out.IMAP == "ok" && out.SMTP == "ok"
	return out
}
func tlsFor(host string, trust *tls.Config) *tls.Config {
	c := &tls.Config{MinVersion: tls.VersionTLS12}
	if trust != nil {
		c = trust.Clone()
	}
	c.ServerName = host
	return c
}
func mailConn(ctx context.Context, host string, port int, implicit bool, trust *tls.Config) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dial := net.Dialer{Timeout: 10 * time.Second}
	c, e := dial.DialContext(ctx, "tcp", addr)
	if e != nil {
		return nil, e
	}
	deadline := time.Now().Add(10 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = c.SetDeadline(deadline)
	if implicit {
		tc := tls.Client(c, tlsFor(host, trust))
		if e = tc.HandshakeContext(ctx); e != nil {
			c.Close()
			return nil, e
		}
		return tc, nil
	}
	return c, nil
}
func imapLogin(ctx context.Context, a core.EmailAccount, password string, trust *tls.Config) error {
	if strings.ContainsAny(a.Username+password, "\r\n\x00") {
		return core.ErrInvalid
	}
	c, e := mailConn(ctx, a.IMAPServer, a.IMAPPort, true, trust)
	if e != nil {
		return e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	r := bufio.NewReaderSize(c, 65536)
	line, e := r.ReadString('\n')
	if e != nil {
		return e
	}
	if !strings.HasPrefix(strings.ToUpper(line), "* OK") {
		return errors.New("IMAP greeting failed")
	}
	quote := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	if _, e = fmt.Fprintf(c, "a1 LOGIN %s %s\r\n", quote(a.Username), quote(password)); e != nil {
		return e
	}
	for i := 0; i < 100; i++ {
		line, e = r.ReadString('\n')
		if e != nil {
			return e
		}
		if strings.HasPrefix(line, "a1 ") {
			if !strings.HasPrefix(strings.ToUpper(line), "A1 OK") {
				return errors.New("IMAP authentication failed")
			}
			_, e = fmt.Fprint(c, "a2 LOGOUT\r\n")
			if e != nil {
				return e
			}
			for j := 0; j < 100; j++ {
				line, e = r.ReadString('\n')
				if e != nil {
					return e
				}
				if strings.HasPrefix(line, "a2 ") {
					return nil
				}
			}
			return errors.New("IMAP logout failed")
		}
	}
	return errors.New("IMAP response limit")
}

type loginAuth struct {
	user, pass string
	step       int
}

func (a *loginAuth) Start(s *smtp.ServerInfo) (string, []byte, error) {
	if !s.TLS {
		return "", nil, errors.New("TLS required")
	}
	return "LOGIN", nil, nil
}
func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	if a.step == 1 {
		return []byte(a.user), nil
	}
	if a.step == 2 {
		return []byte(a.pass), nil
	}
	return nil, errors.New("authentication failed")
}
func smtpLogin(ctx context.Context, a core.EmailAccount, password string, trust *tls.Config) error {
	return smtpLoginTransport(ctx, a, password, trust, a.SMTPPort == 587)
}

func smtpLoginTransport(ctx context.Context, a core.EmailAccount, password string, trust *tls.Config, startTLS bool) error {
	c, e := mailConn(ctx, a.SMTPServer, a.SMTPPort, !startTLS, trust)
	if e != nil {
		return e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	s, e := smtp.NewClient(c, a.SMTPServer)
	if e != nil {
		return e
	}
	defer s.Close()
	if startTLS {
		if e = s.StartTLS(tlsFor(a.SMTPServer, trust)); e != nil {
			return e
		}
	}
	ok, methods := s.Extension("AUTH")
	if !ok {
		return errors.New("SMTP authentication unavailable")
	}
	var auth smtp.Auth
	methods = " " + strings.ToUpper(methods) + " "
	if strings.Contains(methods, " PLAIN ") {
		auth = smtp.PlainAuth("", a.Username, password, a.SMTPServer)
	} else if strings.Contains(methods, " LOGIN ") {
		auth = &loginAuth{user: a.Username, pass: password}
	} else {
		return errors.New("SMTP authentication unavailable")
	}
	if e = s.Auth(auth); e != nil {
		return e
	}
	return s.Quit()
}
