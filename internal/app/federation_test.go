package app_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFederationListenerBoundary(t *testing.T) {
	a := remoteApp(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The signed handler's own auth response must survive both device auth and
	// cross-origin browser protection unchanged, even without a device session.
	for _, remote := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/v1/federation/deliver", strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://unrelated.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		expected := httptest.NewRecorder()
		a.Engine.Handler().ServeHTTP(expected, r.Clone(t.Context()))
		got := httptest.NewRecorder()
		a.Handler(remote, "test", logger).ServeHTTP(got, r)
		if got.Code != expected.Code || got.Body.String() != expected.Body.String() {
			t.Fatalf("remote=%v federation intercepted: %d %s (want %d)", remote, got.Code, got.Body.String(), expected.Code)
		}
	}
	p, e := a.Auth.Pair(t.Context(), "")
	if e != nil {
		t.Fatal(e)
	}
	token, _, e := a.Auth.Redeem(t.Context(), p.Code, "test", "cli", "127.0.0.1", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/v1/route", "/v1/info", "/v1/agents", "/v1/messages", "/v1/auth/register"} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			a.Handler(true, "test", logger).ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatalf("remote serves %s %s: %d", method, path, w.Code)
			}
		}
	}
}
