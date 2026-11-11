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
	"sync/atomic"
	"testing"
	"time"
)

func conv(peer, id string, pending bool, kind string, unread int) Conversation {
	var c Conversation
	c.Peer, c.Unread, c.Pending = peer, unread, pending
	c.Last.ID = id
	c.Last.Subject = "Deploy?"
	c.Last.Context.Userchat.Kind = kind
	return c
}

func TestComputeAndIcon(t *testing.T) {
	agents := []Agent{{Name: "a", State: "idle"}, {Name: "b", State: "busy"}, {Name: "c", State: "waiting"}, {Name: "d", State: "stopped"}}
	s := Compute(agents, []Conversation{conv("a", "m1", true, "ask", 2), conv("b", "m2", false, "say", 1), conv("c", "m3", true, "say", 0)}, nil)
	if s.Running != 3 || s.Waiting != 1 || s.Failed != 0 || s.Questions != 1 || s.Unread != 3 {
		t.Fatalf("%+v", s)
	}
	if s.Icon() != iconAttention {
		t.Fatal("want attention")
	}
	if s.InboxLabel() != "1 question waiting" || s.AgentsLabel() != "Agents: 3 running, 1 waiting" {
		t.Fatalf("%q %q", s.InboxLabel(), s.AgentsLabel())
	}

	failed := Compute([]Agent{{State: "failed"}, {State: "crashed"}, {State: "idle"}}, nil, []Connection{{Pending: 2}})
	if failed.Icon() != iconProblem || failed.Failed != 2 || failed.Pending != 2 {
		t.Fatalf("%+v", failed)
	}
	if got := failed.AgentsLabel(); got != "Agents: 1 running, 2 failed" {
		t.Fatal(got)
	}
	if (Snapshot{}).Icon() != iconProblem || !strings.Contains((Snapshot{}).Tooltip(), "not running") {
		t.Fatal("daemon down is a problem")
	}
	if Compute(nil, nil, nil).Icon() != iconNormal {
		t.Fatal("empty and up is normal")
	}
	if Compute([]Agent{{State: "idle"}}, []Conversation{conv("a", "m", false, "say", 4)}, nil).InboxLabel() != "4 unread messages" {
		t.Fatal("unread label")
	}
}

func TestDetector(t *testing.T) {
	d := newDetector()
	first := Data{
		Agents: []Agent{{Name: "old", State: "failed"}},
		Convs:  []Conversation{conv("iris", "m1", true, "ask", 1)},
		Conns:  []Connection{{Provider: "p", Pending: 1}},
	}
	if n := d.Observe(first); len(n) != 0 {
		t.Fatalf("first read must only seed: %v", n)
	}
	if n := d.Observe(first); len(n) != 0 {
		t.Fatalf("no change, no notes: %v", n)
	}
	next := Data{
		Agents: []Agent{{Name: "old", State: "failed"}, {Name: "new", Display: "New", State: "failed"}},
		Convs:  []Conversation{conv("iris", "m1", true, "ask", 1), conv("juno", "m2", true, "ask", 1), conv("x", "m3", true, "say", 1)},
		Conns:  []Connection{{Provider: "p", Display: "Jugui", Pending: 2}},
	}
	notes := d.Observe(next)
	kinds := map[string]Note{}
	for _, n := range notes {
		kinds[n.Kind] = n
	}
	if len(notes) != 3 || kinds[kindAsk].Link != "/inbox/juno" || kinds[kindFailed].Link != "/agents/new" || kinds[kindFederation].Link != "/connections" {
		t.Fatalf("%+v", notes)
	}
	// An agent that recovers and fails again is news again.
	d.Observe(Data{Agents: []Agent{{Name: "new", State: "idle"}}})
	if n := d.Observe(Data{Agents: []Agent{{Name: "new", State: "failed"}}}); len(n) != 1 {
		t.Fatalf("refail: %v", n)
	}
}

func TestBatcherCollapsesAndLimits(t *testing.T) {
	var mu sync.Mutex
	var got []Note
	b := newBatcher(30*time.Millisecond, 2, func(n Note) { mu.Lock(); got = append(got, n); mu.Unlock() })
	b.Add(Note{Kind: kindAsk, ID: "1", Title: "Iris asks", Link: "/inbox/iris"})
	time.Sleep(80 * time.Millisecond)
	for i := 0; i < 5; i++ {
		b.Add(Note{Kind: kindAsk, ID: fmt.Sprint("b", i), Title: "X asks"})
	}
	time.Sleep(80 * time.Millisecond)
	// Third flush within the hour exceeds maxPerHour = 2 and is dropped.
	b.Add(Note{Kind: kindFailed, ID: "f", Title: "A failed"})
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].ID != "1" || got[1].Title != "5 questions waiting" {
		t.Fatalf("%+v", got)
	}
}

func TestCollapseMixed(t *testing.T) {
	n := collapse([]Note{{Kind: kindAsk}, {Kind: kindFailed}})
	if n.Title != "2 things need you" || n.Link != "/" {
		t.Fatalf("%+v", n)
	}
}

func TestBackoff(t *testing.T) {
	d := time.Duration(0)
	var seq []time.Duration
	for i := 0; i < 8; i++ {
		d = nextBackoff(d)
		seq = append(seq, d)
	}
	if seq[0] != time.Second || seq[1] != 2*time.Second || seq[7] != backoffMax {
		t.Fatalf("%v", seq)
	}
}

func TestFeedRefreshAndStream(t *testing.T) {
	var streams atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"a","state":"idle"},{"name":"b","state":"failed"}]`)
	})
	mux.HandleFunc("/api/mail/conversations", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"peer":"a","unread":1,"pending":true,"last":{"id":"m","context":{"userchat":{"kind":"ask"}}}}]`)
	})
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		n := streams.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		if n == 1 {
			fmt.Fprint(w, ": keepalive\n\nevent: mail\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			return // drop: the client must reconnect
		}
		<-r.Context().Done()
	})
	// /api/connections answers 404: older daemon, must be tolerated.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var mu sync.Mutex
	var last Data
	var calls int
	f := newFeed(srv.URL, func(d Data) { mu.Lock(); last = d; calls++; mu.Unlock() })
	f.debounce = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := last.Snap.Up && streams.Load() >= 2 && f.Connected()
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !last.Snap.Up || last.Snap.Failed != 1 || last.Snap.Questions != 1 || last.Snap.Icon() != iconProblem {
		t.Fatalf("snapshot %+v calls=%d streams=%d", last.Snap, calls, streams.Load())
	}
	if streams.Load() < 2 {
		t.Fatalf("stream was not re-opened: %d", streams.Load())
	}
}

func TestFeedDaemonDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	got := make(chan Data, 1)
	f := newFeed(url, func(d Data) {
		select {
		case got <- d:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)
	select {
	case d := <-got:
		if d.Snap.Up || d.Snap.Icon() != iconProblem {
			t.Fatalf("%+v", d.Snap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot")
	}
}

func TestPrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "desktop.json")
	s := loadPrefs(path)
	if !s.NotifyOn(kindAsk) {
		t.Fatal("default on")
	}
	if err := s.Update(func(p *Prefs) { p.Notify = map[string]bool{kindAsk: false}; p.Frame = &Frame{10, 20, 1000, 700} }); err != nil {
		t.Fatal(err)
	}
	r := loadPrefs(path)
	if r.NotifyOn(kindAsk) || !r.NotifyOn(kindFailed) || r.Get().Frame.W != 1000 {
		t.Fatalf("%+v", r.Get())
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestUsableFrame(t *testing.T) {
	screens := [][4]int{{0, 0, 1920, 1080}}
	if usableFrame(&Frame{100, 100, 1280, 860}, screens) == nil {
		t.Fatal("fine frame rejected")
	}
	if usableFrame(&Frame{3000, 100, 1280, 860}, screens) != nil {
		t.Fatal("off-screen frame accepted")
	}
	if usableFrame(&Frame{0, 0, 300, 200}, screens) != nil || usableFrame(nil, screens) != nil {
		t.Fatal("tiny or nil accepted")
	}
}

func TestFirstRunTarget(t *testing.T) {
	cases := []struct {
		s       SetupState
		agents  int
		welcome bool
		want    string
	}{
		{SetupState{Known: true, Done: true}, 0, true, ""},
		{SetupState{Known: true, Done: false}, 0, true, "/welcome"},
		{SetupState{Known: true, Done: false}, 3, false, ""}, // agents exist: never onboard
		{SetupState{}, 0, true, "/welcome"}, // 404, nothing set up yet
		{SetupState{}, 0, false, "/"},
		{SetupState{}, 5, true, ""}, // existing install skips onboarding
	}
	for i, c := range cases {
		if got := firstRunTarget(c.s, c.agents, c.welcome); got != c.want {
			t.Errorf("case %d: %q want %q", i, got, c.want)
		}
	}
	if onboardingDone(SetupState{}, 0) || !onboardingDone(SetupState{}, 2) || !onboardingDone(SetupState{Known: true}, 9) {
		t.Fatal("onboardingDone")
	}
}

func TestHealthSetupFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"product":"lolad","version":"0.4.0","pid":7,"exe":"/usr/local/bin/lolad","install":"cli","state_dir":"/s"}`)
	})
	mux.HandleFunc("/api/setup", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"done":false}`) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h := fetchHealth(srv.URL)
	if h == nil || h.Install != "cli" || h.StateDir != "/s" {
		t.Fatalf("%+v", h)
	}
	if got := connectedBanner(h); got != "Connected to lolad 0.4.0 at /usr/local/bin/lolad" {
		t.Fatal(got)
	}
	if s := fetchSetup(srv.URL); !s.Known || s.Done {
		t.Fatalf("%+v", s)
	}
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	if fetchHealth(none.URL) != nil || fetchSetup(none.URL).Known {
		t.Fatal("404 must read as unknown")
	}
	if connectedBanner(nil) != "lolad is running" || connectedBanner(&Health{Version: "v1", Install: "app"}) != "lolad 1 is running" {
		t.Fatal("banner")
	}
}

func TestMigrationText(t *testing.T) {
	txt := migrationText(&Health{Exe: "/x/lolad", Install: "cli"}, []string{"com.example.lolad"})
	for _, want := range []string{"pm2 stop lolad", "launchctl bootout gui/$(id -u)/com.example.lolad", "/x/lolad", "Ctrl-C"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	got := parseLaunchdLabels("PID\tStatus\tLabel\n123\t0\tcom.example.lolad\n-\t0\tcom.apple.x\n55\t0\tdev.caceres.lolad.desktop\n-\t0\tapplication.dev.caceres.lolad.desktop.123\n")
	if len(got) != 1 || got[0] != "com.example.lolad" {
		t.Fatalf("%v", got)
	}
}

func TestParseServiceStatus(t *testing.T) {
	s, err := parseServiceStatus([]byte("note\n{\"installed\":true,\"running\":false,\"mechanism\":\"smappservice\",\"path\":\"/a/lolad\"}\n"))
	if err != nil || !s.Installed || s.Running || s.Mechanism != "smappservice" || s.Path != "/a/lolad" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := parseServiceStatus([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
}

func TestRunValueAndBundle(t *testing.T) {
	if got := runValue(`C:\Users\a b\Lolad.exe`); got != `"C:\Users\a b\Lolad.exe" --background` {
		t.Fatal(got)
	}
	if bundlePath("/Applications/Lolad.app/Contents/MacOS/Lolad") != "/Applications/Lolad.app" || bundlePath("/tmp/Lolad") != "" {
		t.Fatal("bundlePath")
	}
}

// Inside a bundle "Contents/MacOS/lolad" and "Lolad" are the same file on a
// case-insensitive disk; the CLI must come from Helpers.
func TestFindLoladSkipsSelf(t *testing.T) {
	dir := t.TempDir()
	macos := filepath.Join(dir, "Lolad.app", "Contents", "MacOS")
	helpers := filepath.Join(dir, "Lolad.app", "Contents", "Helpers")
	_ = os.MkdirAll(macos, 0o755)
	_ = os.MkdirAll(helpers, 0o755)
	exe := filepath.Join(macos, "Lolad")
	_ = os.WriteFile(exe, []byte("app"), 0o755)
	_ = os.WriteFile(filepath.Join(helpers, "lolad"), []byte("cli"), 0o755)
	t.Setenv("HOME", dir)
	got, err := findLoladFrom(exe, "darwin")
	if err != nil || filepath.Base(filepath.Dir(got)) != "Helpers" {
		t.Fatalf("%q %v", got, err)
	}
}
