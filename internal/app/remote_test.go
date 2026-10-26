package app_test

import (
	"context"
	"encoding/json"
	"github.com/CamiloCaceres/lolad/internal/app"
	"github.com/CamiloCaceres/lolad/internal/store"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func remoteApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOLAD_STATE", filepath.Join(dir, "state"))
	config := filepath.Join(dir, "config.yaml")
	agent := filepath.Join(dir, "agent")
	os.MkdirAll(agent, 0700)
	os.WriteFile(filepath.Join(agent, "lolabot.yaml"), []byte("agent:\n  name: Test\nruntime:\n  harness: claude\n  autostart: false\n  env:\n    PRIVATE: test-secret-do-not-return\n"), 0600)
	os.WriteFile(config, []byte("listen: 127.0.0.1:4190\nagents: ["+agent+"]\ntmux_socket: lolad-test-remote-"+filepath.Base(dir)+"\nremote:\n  listen: 127.0.0.1:4191\n  public_url: https://example.ts.net:4100\n  tailscale_logins: [owner@example.com]\n"), 0600)
	a, e := app.New(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	return a
}
func TestRemoteComposition(t *testing.T) {
	a := remoteApp(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	local, remote := a.Handler(false, "test", logger), a.Handler(true, "test", logger)
	p, _ := a.Auth.Pair(t.Context(), "")
	token, d, e := a.Auth.Redeem(t.Context(), p.Code, "test", "cli", "127.0.0.1", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path   string
		remote bool
		auth   bool
		want   int
	}{
		{"/api/agents", false, false, 200}, {"/api/agents", true, false, 401}, {"/api/agents", true, true, 200},
		{"/v1/health", true, true, 404}, {"/api/hook", true, true, 404}, {"/v1/health", false, false, 200},
		{"/healthz", true, false, 200}, {"/api/version", true, false, 200}, {"/devices", true, true, 200}, {"/login", true, false, 200},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		if tc.auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h := local
		if tc.remote {
			h = remote
		}
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s remote=%v: %d %s", tc.path, tc.remote, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "test-secret-do-not-return") || strings.Contains(w.Body.String(), token) {
			t.Fatal("credential in response")
		}
	}
	// Locally, a forged Tailscale header has no effect and creates no sessions.
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	w := httptest.NewRecorder()
	local.ServeHTTP(w, r)
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("local header minted cookie")
	}
	// Allowed public HTTPS origin can write through Serve; unrelated origins cannot.
	for _, origin := range []string{"https://example.ts.net:4100", "https://evil.example"} {
		r = httptest.NewRequest("POST", "http://127.0.0.1:4191/api/devices/pair", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		w = httptest.NewRecorder()
		remote.ServeHTTP(w, r)
		want := 200
		if strings.Contains(origin, "evil") {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("origin %s: %d %s", origin, w.Code, w.Body)
		}
	}
	if e := a.Auth.Revoke(t.Context(), d.ID); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/api/agents", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	remote.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("revoked token still works")
	}
}
func TestRemotePairAndDownload(t *testing.T) {
	a := remoteApp(t)
	h := a.Handler(true, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, _ := a.Auth.Pair(t.Context(), "")
	r := httptest.NewRequest("POST", "/api/pair", strings.NewReader(`{"code":"`+p.Code+`","device":"Pixel","kind":"android"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("pair: %d %s", w.Code, w.Body)
	}
	var reply struct {
		Token    string `json:"token"`
		DeviceID string `json:"device_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &reply)
	if reply.Token == "" || reply.DeviceID == "" {
		t.Fatal("pair shape")
	}
	dir := filepath.Join(a.Config.StateDir, "downloads")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "app.apk"), []byte("test apk"), 0600)
	outside := filepath.Join(a.Config.StateDir, "private")
	os.WriteFile(outside, []byte("private secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	for _, tc := range []struct {
		path string
		want int
	}{{"/download/app.apk", 200}, {"/download/", 404}, {"/download/escape", 404}, {"/download/%2e%2e%2fprivate", 404}} {
		r = httptest.NewRequest("GET", tc.path, nil)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if tc.want == 200 && !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("not attachment")
		}
	}
}
func TestSSERevocation(t *testing.T) {
	a := remoteApp(t)
	p, _ := a.Auth.Pair(t.Context(), "")
	token, d, _ := a.Auth.Redeem(t.Context(), p.Code, "test", "cli", "ip", "")
	server := httptest.NewServer(a.Handler(true, "test", slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/stream", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("stream not accepted")
	}
	if e = a.Auth.Revoke(t.Context(), d.ID); e != nil {
		t.Fatal(e)
	}
	if b, e := io.ReadAll(resp.Body); e != nil || len(b) > 0 {
		t.Fatalf("stream failed to close: %q %v", b, e)
	}
}
func TestBackupRotation(t *testing.T) {
	state := t.TempDir()
	db, e := store.Open(filepath.Join(state, "source.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.Exec(`CREATE TABLE sample(v TEXT);INSERT INTO sample VALUES('kept')`)
	for i := range 10 {
		if e := app.Backup(t.Context(), db, state, time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)); e != nil {
			t.Fatal(e)
		}
	}
	paths, _ := filepath.Glob(filepath.Join(state, "backups", "*.db"))
	if len(paths) != 7 {
		t.Fatalf("backups %d", len(paths))
	}
	if !strings.Contains(paths[0], "20260104") {
		t.Fatalf("oldest not rotated: %s", paths[0])
	}
	copy, e := store.Open(paths[0])
	if e != nil {
		t.Fatal(e)
	}
	defer copy.Close()
	var value string
	if e = copy.QueryRow(`SELECT v FROM sample`).Scan(&value); e != nil || value != "kept" {
		t.Fatal("inconsistent backup")
	}
}
func TestListenerValidation(t *testing.T) {
	for _, tc := range []struct {
		local, remote, public string
		valid                 bool
	}{{"127.0.0.1:4190", "127.0.0.1:4191", "https://example.ts.net:4100", true}, {"0.0.0.0:4190", "", "", false}, {"127.0.0.1:4190", "100.64.0.1:4191", "", false}, {"127.0.0.1:4190", "127.0.0.1:4190", "", false}, {"127.0.0.1:4190", "127.0.0.1:4191", "http://example.com", false}} {
		if e := app.ValidateListeners(tc.local, tc.remote, tc.public); (e == nil) != tc.valid {
			t.Fatalf("config %v err %v", tc, e)
		}
	}
}
