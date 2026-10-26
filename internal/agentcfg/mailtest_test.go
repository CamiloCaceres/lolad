package agentcfg

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func mailTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
func listenMail(t *testing.T, cfg *tls.Config, plain bool, handler func(net.Conn)) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	if !plain {
		l = tls.NewListener(l, cfg)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _ = c.SetDeadline(time.Now().Add(3 * time.Second)); handler(c) }()
		}
	}()
	t.Cleanup(func() { l.Close(); <-done })
	return l.Addr().(*net.TCPAddr).Port
}
func fakeIMAP(t *testing.T, cfg *tls.Config, good string) int {
	return listenMail(t, cfg, false, func(c net.Conn) {
		fmt.Fprint(c, "* OK local test server\r\n")
		r := bufio.NewReader(c)
		line, _ := r.ReadString('\n')
		if !strings.Contains(line, `"`+good+`"`) {
			fmt.Fprint(c, "a1 NO authentication failed\r\n")
			return
		}
		fmt.Fprint(c, "a1 OK logged in\r\n")
		line, _ = r.ReadString('\n')
		if line != "a2 LOGOUT\r\n" {
			return
		}
		fmt.Fprint(c, "* BYE logging out\r\na2 OK logout\r\n")
	})
}
func fakeSMTP(t *testing.T, cfg *tls.Config, good string, startTLS, login bool) int {
	return listenMail(t, cfg, startTLS, func(c net.Conn) {
		fmt.Fprint(c, "220 localhost test\r\n")
		r := bufio.NewReader(c)
		secure := !startTLS
		method := "PLAIN"
		if login {
			method = "LOGIN"
		}
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				if secure {
					fmt.Fprintf(c, "250-localhost\r\n250 AUTH %s\r\n", method)
				} else {
					fmt.Fprint(c, "250-localhost\r\n250 STARTTLS\r\n")
				}
			case line == "STARTTLS":
				fmt.Fprint(c, "220 start TLS\r\n")
				tc := tls.Server(c, cfg)
				if tc.Handshake() != nil {
					return
				}
				c = tc
				r = bufio.NewReader(c)
				secure = true
			case strings.HasPrefix(line, "AUTH PLAIN "):
				b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				if !secure || string(b) != "\x00test@example.test\x00"+good {
					fmt.Fprint(c, "535 bad password\r\n")
				} else {
					fmt.Fprint(c, "235 authenticated\r\n")
				}
			case line == "AUTH LOGIN":
				if !secure {
					return
				}
				fmt.Fprint(c, "334 VXNlcm5hbWU6\r\n")
				u, _ := r.ReadString('\n')
				fmt.Fprint(c, "334 UGFzc3dvcmQ6\r\n")
				p, _ := r.ReadString('\n')
				user, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
				pass, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
				if string(user) == "test@example.test" && string(pass) == good {
					fmt.Fprint(c, "235 authenticated\r\n")
				} else {
					fmt.Fprint(c, "535 bad password\r\n")
				}
			case line == "QUIT":
				fmt.Fprint(c, "221 bye\r\n")
				return
			default:
				fmt.Fprint(c, "500 unsupported command\r\n")
				return
			}
		}
	})
}
func TestEmailTLS(t *testing.T) {
	server, trust := mailTLS(t)
	imap := fakeIMAP(t, server, "local-test-password")
	smtp := fakeSMTP(t, server, "local-test-password", false, false)
	a := core.EmailAccount{Address: "test@example.test", IMAPServer: "127.0.0.1", IMAPPort: imap, SMTPServer: "127.0.0.1", SMTPPort: smtp}
	got := testEmail(context.Background(), a, "local-test-password", trust)
	if !got.OK || got.IMAP != "ok" || got.SMTP != "ok" {
		t.Fatal(got)
	}
	got = testEmail(context.Background(), a, "wrong", trust)
	if got.OK || got.IMAP == "ok" || got.SMTP == "ok" {
		t.Fatal("bad password accepted")
	}
	got = TestEmail(context.Background(), a, "local-test-password")
	if got.OK {
		t.Fatal("untrusted cert accepted")
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	a.IMAPPort, _ = strconv.Atoi(port)
	a.SMTPPort = a.IMAPPort
	got = testEmail(context.Background(), a, "local-test-password", trust)
	if got.OK || got.IMAP == "ok" || got.SMTP == "ok" {
		t.Fatal("unreachable accepted")
	}
	if strings.Contains(fmt.Sprint(got), "local-test-password") {
		t.Fatal("secret in result")
	}
}
func TestSMTPStartTLSAndLogin(t *testing.T) {
	server, trust := mailTLS(t)
	for _, startTLS := range []bool{false, true} {
		for _, login := range []bool{false, true} {
			port := fakeSMTP(t, server, "test-password", startTLS, login)
			a := core.EmailAccount{Username: "test@example.test", SMTPServer: "127.0.0.1", SMTPPort: port}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			e := smtpLoginTransport(ctx, a, "test-password", trust, startTLS)
			cancel()
			if e != nil {
				t.Fatalf("startTLS=%v login=%v: %v", startTLS, login, e)
			}
		}
	}
}
