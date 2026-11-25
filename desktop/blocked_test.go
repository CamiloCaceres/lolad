package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A daemon that is up but that macOS refuses an agent folder says so in
// /api/health; the feed carries it, the tray line and one note name the drive.
func TestFeedReportsBlockedFolderWhileUp(t *testing.T) {
	state := `"state":"waiting_for_macos_permission","waiting_for":"/Volumes/DevSSD/agents","drive":"DevSSD"`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"product":"lolad",`+state+`}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var d Data
	f := newFeed(srv.URL, func(got Data) { d = got })
	f.refresh(context.Background())
	if !d.Snap.Up || d.Blocked == nil || d.Blocked.Drive != "DevSSD" || d.Starting != nil {
		t.Fatalf("got %+v (blocked %+v)", d, d.Blocked)
	}
	if l := upLabel(d, &Health{Version: "1", Install: "app"}); l != "macOS is blocking “DevSSD”: some assistants are paused" {
		t.Fatalf("label %q", l)
	}
	n := permissionNote(d, 0, "darwin")
	if n == nil || n.ID != "macos-permission" || n.Link != "/agents" || n.Title != "macOS is blocking access to “DevSSD”" ||
		!strings.Contains(n.Body, "click Allow") || !strings.Contains(n.Body, "Full Disk Access") {
		t.Fatalf("note %+v", n)
	}

	// Access allowed: health is ready again, so no note and the usual line.
	state = `"state":"ready"`
	f.refresh(context.Background())
	if d.Blocked != nil || permissionNote(d, 0, "darwin") != nil {
		t.Fatalf("still blocked: %+v", d.Blocked)
	}
	if l := upLabel(d, &Health{Version: "1", Install: "app"}); l != "lolad 1 is running" {
		t.Fatalf("label %q", l)
	}
}

// Blocked agents the daemon still lists keep their own, more specific note.
func TestBlockedAgentsNoteWinsOverBlockedFolder(t *testing.T) {
	a := Agent{Name: "iris", State: "waiting"}
	a.Problem = &struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Hint  string `json:"hint"`
	}{"macos_permission", "macOS is blocking access to “DevSSD”", "click Allow."}
	d := Data{Snap: Snapshot{Up: true}, Agents: []Agent{a}, Blocked: &Health{Drive: "DevSSD"}}
	if n := permissionNote(d, 0, "darwin"); n == nil || !strings.HasPrefix(n.Body, "iris is waiting. ") {
		t.Fatalf("%+v", n)
	}
}
