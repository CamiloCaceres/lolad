package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/api"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

func TestStartupState(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 22, 0, 0, 0, time.UTC)
	wait := 5 * time.Second
	stuck := map[string]time.Time{"/Volumes/DevSSD/Developer/projects": t0}
	for _, c := range []struct {
		name                 string
		now                  time.Time
		pending              map[string]time.Time
		goos                 string
		state, folder, drive string
	}{
		{"early", t0.Add(2 * time.Second), stuck, "darwin", api.StateStarting, "", ""},
		{"slow but nothing held", t0.Add(9 * time.Second), nil, "darwin", api.StateStarting, "", ""},
		{"held on macOS", t0.Add(6 * time.Second), stuck, "darwin", api.StateMacOSAllow, "/Volumes/DevSSD/Developer/projects", "DevSSD"},
		{"held elsewhere", t0.Add(6 * time.Second), stuck, "linux", api.StateStarting, "/Volumes/DevSSD/Developer/projects", ""},
		{"probe started late", t0.Add(6 * time.Second), map[string]time.Time{"/Volumes/X/a": t0.Add(4 * time.Second)}, "darwin", api.StateStarting, "", ""},
		{"oldest named", t0.Add(20 * time.Second), map[string]time.Time{"/Volumes/B/x": t0.Add(time.Second), "/Users/me/Documents/agents": t0}, "darwin", api.StateMacOSAllow, "/Users/me/Documents/agents", "agents"},
	} {
		s, f, d := startupState(c.now, t0, c.pending, c.goos, wait)
		if s != c.state || f != c.folder || d != c.drive {
			t.Errorf("%s: got (%q, %q, %q), want (%q, %q, %q)", c.name, s, f, d, c.state, c.folder, c.drive)
		}
	}
}

func TestDriveName(t *testing.T) {
	for in, want := range map[string]string{
		"/Volumes/DevSSD/Developer/projects": "DevSSD",
		"/Volumes/My Drive":                  "My Drive",
		"/Users/me/Documents":                "Documents",
	} {
		if got := driveName(in); got != want {
			t.Errorf("driveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartupFolders(t *testing.T) {
	got := startupFolders(registry.Config{AgentsDirs: []string{"/a", " ", "/b"}, Agents: []string{"/b", "/c/one"}})
	if strings.Join(got, ",") != "/a,/b,/c/one" {
		t.Fatalf("got %v", got)
	}
}

// A folder read that never returns (macOS waiting on a dialog) shows up in
// /api/health; the dashboard and the API say so instead of hanging; once the
// app is ready every request goes to it.
func TestStartupGate(t *testing.T) {
	now := time.Date(2026, 11, 23, 22, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	g := newStartup("1.2.3", "/state", now)
	g.goos, g.now = "darwin", clock
	release := make(chan struct{})
	readDone := make(chan struct{})
	g.probe([]string{"/Volumes/DevSSD/Developer/projects", "/Users/me/agents"}, func(d string) error {
		if strings.HasPrefix(d, "/Volumes/") {
			<-release
			defer close(readDone)
		}
		return nil
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	health := func() api.Health {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var h api.Health
		if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&h) != nil {
			t.Fatalf("health: %s", resp.Status)
		}
		return h
	}
	if h := health(); h.Product != "lolad" || h.Version != "1.2.3" || h.State != api.StateStarting {
		t.Fatalf("at start: %+v", h)
	}
	advance(6 * time.Second)
	h := health()
	if h.State != api.StateMacOSAllow || h.Drive != "DevSSD" || h.WaitingFor != "/Volumes/DevSSD/Developer/projects" {
		t.Fatalf("while held: %+v", h)
	}
	resp, err := http.Get(srv.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || body["state"] != api.StateMacOSAllow || !strings.Contains(body["error"], `"DevSSD"`) {
		t.Fatalf("/api/agents while held: %s %v", resp.Status, body)
	}
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	var page strings.Builder
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	page.Write(buf[:n])
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(page.String(), "Click Allow") || !strings.Contains(page.String(), `http-equiv="refresh"`) {
		t.Fatalf("dashboard while held: %s %s", resp.Status, page.String())
	}

	close(release) // the user clicked Allow
	<-readDone
	for i := 0; ; i++ {
		h := health()
		if h.State == api.StateStarting && h.WaitingFor == "" {
			break
		}
		if i == 100 {
			t.Fatalf("after Allow, before ready: %+v", h)
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dashboard " + r.URL.Path)) }))
	resp, err = http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	n, _ = resp.Body.Read(buf)
	resp.Body.Close()
	if got := string(buf[:n]); got != "dashboard /api/health" {
		t.Fatalf("after Ready: %q", got)
	}
}

func TestReadyHealthSaysReady(t *testing.T) {
	if h := api.NewHealth("1", "/s", time.Time{}); h.State != api.StateReady || h.Product != "lolad" {
		t.Fatalf("%+v", h)
	}
}
