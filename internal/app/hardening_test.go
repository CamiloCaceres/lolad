package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryAndRedactedRequestLog(t *testing.T) {
	var logs bytes.Buffer
	h := harden(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("secret-panic") }), true, slog.New(slog.NewTextHandler(&logs, nil)))
	r := httptest.NewRequest("POST", "/example?token=secret-query", strings.NewReader("secret-body"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("panic status %d", w.Code)
	}
	for _, secret := range []string{"secret-panic", "secret-query", "secret-body"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret exposed")
		}
	}
	if !strings.Contains(logs.String(), "status=500") {
		t.Fatal("missing request status")
	}
}
