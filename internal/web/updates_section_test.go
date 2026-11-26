package web_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Settings carries the Updates section hidden; static/updates.js fills and
// shows it only inside the desktop app (desktop/bridge.go).
func TestAppSettingsUpdatesSection(t *testing.T) {
	e := setup(t)
	_, body := e.do("GET", "/settings", nil, false)
	for _, want := range []string{`id="updates"`, `data-updates hidden`, `data-upd-actions`, `/static/updates.js`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	i := strings.Index(body, "/static/updates.js")
	j := strings.LastIndex(body[:i], `src="`) + len(`src="`)
	src := body[j : strings.IndexByte(body[j:], '"')+j]
	resp, err := http.Get(e.srv.URL + src)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	js, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(js), "window.__loladUpdate =") || strings.Contains(string(js), "innerHTML") {
		t.Fatalf("updates.js: %d, %d bytes", resp.StatusCode, len(js))
	}
}
