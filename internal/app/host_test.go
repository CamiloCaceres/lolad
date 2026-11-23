package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopbackHost(t *testing.T) {
	for h, want := range map[string]bool{
		"":                            true,
		"127.0.0.1:4100":              true,
		"127.0.0.1":                   true,
		"localhost:4100":              true,
		"LOCALHOST":                   true,
		"localhost.:4100":             true,
		"[::1]:4100":                  true,
		"127.1.2.3:80":                true,
		"evil.example:4100":           false,
		"evil.example":                false,
		"10.0.0.5:4100":               false,
		"0.0.0.0:4100":                false,
		"localhost.evil.example:4100": false,
	} {
		if got := loopbackHost(h); got != want {
			t.Errorf("loopbackHost(%q) = %v, want %v", h, got, want)
		}
	}
}

// A page that rebinds its own domain to 127.0.0.1 sends its own Host; the
// local listener must refuse it before any handler runs.
func TestHardenRefusesForeignHostLocally(t *testing.T) {
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++ })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	local := harden(next, false, log)
	remote := harden(next, true, log)

	req := httptest.NewRequest("POST", "/api/agents/x/start", nil)
	req.Host = "evil.example:4100"
	rec := httptest.NewRecorder()
	local.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("foreign host: code %d, handler called %d times", rec.Code, called)
	}

	req.Host = "127.0.0.1:4100"
	rec = httptest.NewRecorder()
	local.ServeHTTP(rec, req)
	if rec.Code != 200 || called != 1 {
		t.Fatalf("loopback host: code %d, called %d", rec.Code, called)
	}

	// The remote listener sits behind Tailscale Serve, which sends the public name.
	req.Host = "mac.tailnet.ts.net"
	rec = httptest.NewRecorder()
	remote.ServeHTTP(rec, req)
	if called != 2 {
		t.Fatalf("remote listener refused its public host: code %d", rec.Code)
	}
}
