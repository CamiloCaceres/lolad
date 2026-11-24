package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A lolad held by a macOS permission dialog answers /api/health with its
// state and 503 elsewhere; the feed reports it as starting, not as down.
func TestFeedReportsStartingDaemon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"product":"lolad","state":"waiting_for_macos_permission","waiting_for":"/Volumes/DevSSD/x","drive":"DevSSD"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "starting", http.StatusServiceUnavailable) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := make(chan Data, 1)
	f := newFeed(srv.URL, func(d Data) {
		select {
		case got <- d:
		default:
		}
	})
	f.refresh(context.Background())
	d := <-got
	if d.Snap.Up || d.Starting == nil || d.Starting.Drive != "DevSSD" || d.Silent || d.PortConflict != "" {
		t.Fatalf("got %+v (starting %+v)", d, d.Starting)
	}
	if l := downLabel(d); l != "macOS is asking to use “DevSSD”: click Allow" {
		t.Fatalf("label %q", l)
	}
}

// An older lolad stuck before it could answer: the port takes the connection
// and says nothing.
func TestFeedReportsSilentPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()

	got := make(chan Data, 1)
	f := newFeed("http://"+l.Addr().String(), func(d Data) {
		select {
		case got <- d:
		default:
		}
	})
	f.api.Timeout = 200 * time.Millisecond
	f.refresh(context.Background())
	d := <-got
	if d.Snap.Up || !d.Silent || d.Starting != nil || d.PortConflict != "" {
		t.Fatalf("got %+v", d)
	}
	if l := downLabel(d); l != "lolad is not answering" {
		t.Fatalf("label %q", l)
	}
}

func TestPermissionNote(t *testing.T) {
	waiting := Data{Starting: &Health{Product: "lolad", State: "waiting_for_macos_permission", Drive: "DevSSD"}}
	if n := permissionNote(waiting, 0, "darwin"); n == nil || !strings.Contains(n.Body, "“DevSSD”") || !strings.Contains(n.Body, "Click Allow") {
		t.Fatalf("waiting: %+v", n)
	}
	if n := permissionNote(Data{Starting: &Health{State: "starting"}}, 0, "darwin"); n != nil {
		t.Fatalf("plain starting: %+v", n)
	}
	if n := permissionNote(Data{Silent: true}, 10*time.Second, "darwin"); n != nil {
		t.Fatalf("silent briefly: %+v", n)
	}
	if n := permissionNote(Data{Silent: true}, time.Minute, "darwin"); n == nil || !strings.Contains(n.Body, "click Allow") {
		t.Fatalf("silent a minute: %+v", n)
	}
	if n := permissionNote(Data{Silent: true}, time.Minute, "windows"); n != nil {
		t.Fatalf("silent on Windows: %+v", n)
	}
	if downLabel(Data{Starting: &Health{State: "starting"}}) != "lolad is starting…" {
		t.Fatal(downLabel(Data{Starting: &Health{State: "starting"}}))
	}
}

func TestHealthStarting(t *testing.T) {
	for _, c := range []struct {
		h    *Health
		want bool
	}{{nil, false}, {&Health{}, false}, {&Health{State: "ready"}, false}, {&Health{State: "starting"}, true}, {&Health{State: "waiting_for_macos_permission"}, true}} {
		if got := c.h.Starting(); got != c.want {
			t.Errorf("%+v: %v", c.h, got)
		}
	}
}

// Agents blocked by macOS after a reboot: one note for all of them.
func TestPermissionNoteForBlockedAgents(t *testing.T) {
	blocked := func(name string) Agent {
		a := Agent{Name: name, State: "waiting"}
		a.Problem = &struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Hint  string `json:"hint"`
		}{"macos_permission", "macOS is blocking access to “DevSSD”", "If macOS is asking whether Lolad may use “DevSSD”, click Allow."}
		return a
	}
	d := Data{Snap: Snapshot{Up: true}, Agents: []Agent{{Name: "ok", State: "idle"}, blocked("iris"), blocked("quill"), blocked("ledger")}}
	n := permissionNote(d, 0, "darwin")
	if n == nil || n.ID != "macos-permission" || n.Title != "macOS is blocking access to “DevSSD”" ||
		!strings.HasPrefix(n.Body, "3 assistants are waiting. ") || !strings.Contains(n.Body, "click Allow") {
		t.Fatalf("%+v", n)
	}
	one := Data{Snap: Snapshot{Up: true}, Agents: []Agent{blocked("iris")}}
	if n := permissionNote(one, 0, "darwin"); n == nil || !strings.HasPrefix(n.Body, "iris is waiting. ") {
		t.Fatalf("one: %+v", n)
	}
	if n := permissionNote(Data{Snap: Snapshot{Up: true}, Agents: []Agent{{Name: "ok", State: "idle"}}}, 0, "darwin"); n != nil {
		t.Fatalf("none blocked: %+v", n)
	}
}

// The daemon's JSON carries the problem through to the tray.
func TestAgentProblemDecodes(t *testing.T) {
	as, err := decodeList[Agent]([]byte(`[{"name":"iris","state":"waiting","problem":{"kind":"macos_permission","title":"T","hint":"H","needs_you":true}}]`))
	if err != nil || len(as) != 1 || as[0].Problem == nil || as[0].Problem.Kind != "macos_permission" || as[0].Problem.Hint != "H" {
		t.Fatalf("%v %+v", err, as)
	}
}
