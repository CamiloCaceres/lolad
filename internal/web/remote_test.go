package web_test

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/auth"
	"github.com/CamiloCaceres/lolad/internal/store"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
	"github.com/coder/websocket"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteLoginAndDevicePages(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := auth.New(db)
	if e != nil {
		t.Fatal(e)
	}
	svc := fake.New(fake.Options{})
	defer svc.Close()
	h := a.Remote(web.New(svc, web.Options{Auth: a, PublicURL: "https://mac.ts.net:4100", TrustedOrigin: "https://mac.ts.net:4100", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}), nil)
	p, _ := a.Pair(t.Context(), "")
	form := url.Values{"credential": {p.Code}, "next": {"//evil.example"}}
	r := httptest.NewRequest("POST", "http://127.0.0.1:4191/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://mac.ts.net:4100")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("missing secure session")
	}
	r = httptest.NewRequest("GET", "/devices", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Browser") {
		t.Fatalf("devices: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/devices/pair", strings.NewReader("name=Phone"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<svg xmlns=") {
		t.Fatalf("pair QR: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("POST", "/logout", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal("logout failed")
	}
	r = httptest.NewRequest("GET", "/devices", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal("logged-out session accepted")
	}
}
func TestRemoteTerminalPublicOriginAndRevoke(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, _ := auth.New(db)
	p, _ := a.Pair(t.Context(), "")
	token, d, _ := a.Redeem(t.Context(), p.Code, "test", "cli", "ip", "")
	svc := fake.New(fake.Options{})
	defer svc.Close()
	server := httptest.NewServer(a.Remote(web.New(svc, web.Options{Auth: a, TrustedOrigin: "https://mac.ts.net:4100", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}), nil))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	h := http.Header{"Authorization": {"Bearer " + token}, "Origin": {"https://mac.ts.net:4100"}}
	conn, resp, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/agents/iris/terminal", &websocket.DialOptions{HTTPHeader: h})
	if e != nil {
		t.Fatalf("public Origin websocket: %v response %v", e, resp)
	}
	defer conn.CloseNow()
	if e = a.Revoke(t.Context(), d.ID); e != nil {
		t.Fatal(e)
	}
	// Drain any already-written terminal frame, then observe session cancellation.
	for {
		_, _, e = conn.Read(ctx)
		if e != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatal("revocation did not close terminal")
	}
}
