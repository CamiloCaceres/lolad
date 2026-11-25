package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A running daemon that macOS keeps from an agent folder says so in
// /api/health; elsewhere, or with nothing blocked, it is plainly ready.
func TestHealthReportsBlockedFolder(t *testing.T) {
	old := healthGOOS
	t.Cleanup(func() { healthGOOS = old })
	blocked := []string{"/Volumes/DevSSD/Developer/projects"}
	m := http.NewServeMux()
	healthRoute(m, Options{Version: "1", Blocked: func() []string { return blocked }})
	get := func() Health {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
		var h Health
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	healthGOOS = "darwin"
	if h := get(); h.State != StateMacOSAllow || h.Drive != "DevSSD" || h.WaitingFor != blocked[0] || h.Product != "lolad" {
		t.Fatalf("%+v", h)
	}
	blocked = nil
	if h := get(); h.State != StateReady || h.Drive != "" {
		t.Fatalf("allowed: %+v", h)
	}
	blocked = []string{"/mnt/x"}
	healthGOOS = "linux"
	if h := get(); h.State != StateReady {
		t.Fatalf("linux: %+v", h)
	}
}
