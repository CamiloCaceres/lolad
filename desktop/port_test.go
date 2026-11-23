package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The configured port is held by another program; lolad then moves to a new
// port and saves it in the config. The feed reports the conflict, then
// follows lolad to the new address.
func TestFeedFollowsLoladToNewPort(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not here", http.StatusNotFound)
	}))
	defer other.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"product":"lolad"}`) })
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[{"name":"a","state":"idle"}]`) })
	lolad := httptest.NewServer(mux)
	defer lolad.Close()

	cfg := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("LOLAD_URL", "")
	t.Setenv("LOLAD_CONFIG", cfg)
	hostport := func(u string) string { return strings.TrimPrefix(u, "http://") }
	os.WriteFile(cfg, []byte("listen: "+hostport(other.URL)+"\n"), 0o600)

	var mu sync.Mutex
	var last Data
	var moved []string
	f := newFeed(dashboardURL(listenAddr()), func(d Data) { mu.Lock(); last = d; mu.Unlock() })
	f.poll = 50 * time.Millisecond
	f.debounce = time.Millisecond
	f.locate = func() string { return dashboardURL(listenAddr()) }
	f.onMove = func(b string) { mu.Lock(); moved = append(moved, b); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)

	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			done := ok()
			mu.Unlock()
			if done {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("%s: last=%+v moved=%v base=%s", what, last, moved, f.Base())
	}
	wait("conflict", func() bool { return !last.Snap.Up && last.PortConflict == portOf(other.URL) })
	if got := downLabel(last); got != "Another program is using port "+portOf(other.URL) {
		t.Fatal(got)
	}

	os.WriteFile(cfg, []byte("listen: "+hostport(lolad.URL)+"\n"), 0o600)
	wait("follow", func() bool { return last.Snap.Up && len(moved) == 1 })
	if moved[0] != lolad.URL || f.Base() != lolad.URL || last.PortConflict != "" {
		t.Fatalf("moved=%v base=%s", moved, f.Base())
	}
}

func TestDownLabel(t *testing.T) {
	if downLabel(Data{}) != "lolad is not running" {
		t.Fatal(downLabel(Data{}))
	}
	if downLabel(Data{PortConflict: "4100"}) != "Another program is using port 4100" {
		t.Fatal("conflict label")
	}
}
