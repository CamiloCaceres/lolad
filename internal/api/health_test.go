package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestInstallKind(t *testing.T) {
	for _, c := range []struct{ exe, forced, want string }{
		{"/usr/local/bin/lolad", "", "cli"},
		{"/Applications/Lolad.app/Contents/Helpers/lolad", "", "app"},
		{`C:\Users\a\AppData\Local\Programs\Lolad\bin\lolad-daemon.exe`, "", "app"},
		{`C:\tools\lolad.exe`, "", "cli"},
		{"/usr/local/bin/lolad", "app", "app"},
		{"/usr/local/bin/lolad", "cli", "cli"},
		{"/home/x/notanapp/lolad", "", "cli"},
	} {
		if got := InstallKind(c.exe, c.forced); got != c.want {
			t.Errorf("InstallKind(%q,%q)=%q want %q", c.exe, c.forced, got, c.want)
		}
	}
}

func TestHealth(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h := New(nil, Options{Version: "9.9", StateDir: "/state", StartedAt: started})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got Health
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Product != "lolad" || got.Version != "9.9" || got.PID != os.Getpid() || got.StartedAt != "2026-01-02T03:04:05Z" || got.StateDir != "/state" || got.Exe == "" || (got.Install != "cli" && got.Install != "app") {
		t.Fatalf("%+v", got)
	}
}
