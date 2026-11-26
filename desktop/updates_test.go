package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

// fakeUpdateServer publishes a signed update.json and one artifact, the way
// the GitHub release does.
type fakeUpdateServer struct {
	*httptest.Server
	key ed25519.PrivateKey

	mu       sync.Mutex
	manifest []byte
	artifact []byte
	status   int           // non-zero: every request answers this
	corrupt  bool          // serve different bytes than the manifest pins
	chunk    chan struct{} // non-nil: one artifact chunk per receive
}

func newFakeUpdateServer(t testing.TB) *fakeUpdateServer {
	t.Helper()
	b, err := os.ReadFile("updater/testdata/test-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	key, err := updater.ParsePrivateKeyPEM(b)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeUpdateServer{key: key, artifact: []byte(strings.Repeat("lolad-update-", 4096))}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *fakeUpdateServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status, manifest, art, corrupt, chunk := s.status, s.manifest, s.artifact, s.corrupt, s.chunk
	s.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	switch r.URL.Path {
	case "/update.json":
		w.Write(manifest)
	case "/Lolad.app.zip":
		if corrupt {
			art = append([]byte{}, art...)
			art[10] ^= 0xff
		}
		if chunk == nil {
			w.Write(art)
			return
		}
		fl := w.(http.Flusher)
		step := len(art) / 4
		for i := 0; i < len(art); i += step {
			select {
			case <-chunk:
			case <-r.Context().Done():
				return
			}
			end := min(i+step, len(art))
			w.Write(art[i:end])
			fl.Flush()
		}
	default:
		http.NotFound(w, r)
	}
}

// publish signs a manifest for version (darwin-arm64 and windows-amd64 both
// point at the one artifact).
func (s *fakeUpdateServer) publish(t testing.TB, version, notes string) {
	t.Helper()
	h := sha256.Sum256(s.artifact)
	art := updater.Artifact{URL: s.URL + "/Lolad.app.zip", SHA256: hex.EncodeToString(h[:]), Size: int64(len(s.artifact))}
	m := updater.Manifest{Version: version, NotesURL: "https://example.com/notes", Notes: notes, MultiplexerVersion: "mux-1",
		Platforms: map[string]updater.Artifact{"darwin-arm64": art, "windows-amd64": art}}
	raw, _ := json.Marshal(m)
	signed, err := updater.SignManifest(raw, s.key)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.manifest = signed
	s.mu.Unlock()
}

func (s *fakeUpdateServer) set(f func(*fakeUpdateServer)) { s.mu.Lock(); f(s); s.mu.Unlock() }

// fakeBackend is the real updater (manifest signature, download, hash) with
// the platform steps replaced: Verify and Apply would run codesign or the
// Windows installer and restart the real daemon.
type fakeBackend struct {
	*updater.Updater
	verifyErr, applyErr error
	applied             string
	onboarding          bool
}

func (b *fakeBackend) Verify(string) error { return b.verifyErr }
func (b *fakeBackend) Apply(p string) error {
	if b.applyErr == nil {
		b.applied = p
	}
	return b.applyErr
}
func (b *fakeBackend) OnboardingInProgress() bool { return b.onboarding }

type flowFixture struct {
	srv   *fakeUpdateServer
	b     *fakeBackend
	f     *updateFlow
	dir   string
	mu    sync.Mutex
	seen  []string // phases, in order of change
	notes []string // notifications
}

func newFlowFixture(t *testing.T, current string) *flowFixture {
	t.Helper()
	x := &flowFixture{srv: newFakeUpdateServer(t), dir: t.TempDir()}
	os.WriteFile(filepath.Join(x.dir, "multiplexer.version"), []byte("mux-1\n"), 0o644)
	x.srv.publish(t, "1.1.0", "Settings has an Updates section.")
	u, err := updater.New(updater.Config{
		CurrentVersion: current, ManifestURL: x.srv.URL + "/update.json",
		PublicKey: x.srv.key.Public().(ed25519.PublicKey), Platform: "darwin-arm64",
		TempDir: x.dir, OnboardingFlag: filepath.Join(x.dir, "no-flag"),
		MultiplexerVersionFile: filepath.Join(x.dir, "multiplexer.version"),
	})
	if err != nil {
		t.Fatal(err)
	}
	x.b = &fakeBackend{Updater: u}
	x.f = newUpdateFlow(x.b, current, filepath.Join(x.dir, "state"))
	x.f.now = func() time.Time { return time.Date(2026, 11, 26, 15, 4, 5, 0, time.UTC) }
	x.f.onChange = func(s UpdateState) {
		x.mu.Lock()
		if n := len(x.seen); n == 0 || x.seen[n-1] != s.Phase {
			x.seen = append(x.seen, s.Phase)
		}
		x.mu.Unlock()
	}
	x.f.notify = func(title, body string) { x.mu.Lock(); x.notes = append(x.notes, title); x.mu.Unlock() }
	return x
}

func (x *flowFixture) phases() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return strings.Join(x.seen, ">")
}

var bg = context.Background()

func TestUpdateFlowHappyPath(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	if st := x.f.State(); st.Phase != phIdle || st.Current != "1.0.0" || st.LastChecked != "" {
		t.Fatalf("start: %+v", st)
	}
	x.f.Check(bg, false)
	st := x.f.State()
	if st.Phase != phAvailable || st.Version != "1.1.0" || st.Notes != "Settings has an Updates section." || st.LastChecked != "2026-11-26T15:04:05Z" || st.AgentsRestart {
		t.Fatalf("after check: %+v", st)
	}
	x.f.Download(bg)
	if st = x.f.State(); st.Phase != phReady || st.Percent != 100 {
		t.Fatalf("after download: %+v", st)
	}
	x.f.Restart()
	if st = x.f.State(); st.Phase != phRestarting || x.b.applied == "" {
		t.Fatalf("after restart: %+v applied=%q", st, x.b.applied)
	}
	if b, err := os.ReadFile(filepath.Join(x.dir, "state", reopenMarker)); err != nil || strings.TrimSpace(string(b)) != "1.1.0" {
		t.Fatalf("reopen marker: %q %v", b, err)
	}
	if got := x.phases(); got != "checking>available>downloading>ready>restarting" {
		t.Fatalf("phases %s", got)
	}
	if len(x.notes) != 0 {
		t.Fatalf("a manual update must not notify: %v", x.notes)
	}
}

// The next launch reads the marker once: it opens Settings → Updates and
// says the update finished. A stale marker is dropped.
func TestUpdateFlowReopenAfterRestart(t *testing.T) {
	x := newFlowFixture(t, "1.1.0")
	marker := filepath.Join(x.dir, "state", reopenMarker)
	os.MkdirAll(filepath.Dir(marker), 0o700)
	os.WriteFile(marker, []byte("1.1.0\n"), 0o600)
	x.f.now = time.Now
	if !x.f.TakeReopen() || x.f.State().Phase != phUpdated {
		t.Fatalf("fresh marker: %+v", x.f.State())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker not removed")
	}
	if x.f.TakeReopen() {
		t.Fatal("marker read twice")
	}
	os.WriteFile(marker, []byte("1.1.0\n"), 0o600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(marker, old, old)
	if x.f.TakeReopen() {
		t.Fatal("stale marker reopened")
	}
}

// A release with another tmux/psmux says the assistants restart.
func TestUpdateFlowAgentsRestart(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	os.WriteFile(filepath.Join(x.dir, "multiplexer.version"), []byte("mux-0\n"), 0o644)
	x.f.Check(bg, false)
	if st := x.f.State(); !st.AgentsRestart {
		t.Fatalf("%+v", st)
	}
}

func TestUpdateFlowUpToDateAndNewerThanPublished(t *testing.T) {
	for _, cur := range []string{"1.1.0", "1.2.0-beta.1"} {
		x := newFlowFixture(t, cur)
		x.f.Check(bg, false)
		if st := x.f.State(); st.Phase != phUpToDate || st.Version != "" || st.LastChecked == "" {
			t.Fatalf("%s: %+v", cur, st)
		}
	}
}

// The last check time survives a restart of the app.
func TestUpdateFlowRemembersLastCheck(t *testing.T) {
	x := newFlowFixture(t, "1.1.0")
	x.f.Check(bg, false)
	again := newUpdateFlow(x.b, "1.1.0", filepath.Join(x.dir, "state"))
	if got := again.State().LastChecked; got != "2026-11-26T15:04:05Z" {
		t.Fatalf("last checked after relaunch = %q", got)
	}
}

func TestUpdateFlowFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(x *flowFixture)
		step  string // where it fails
		want  string // in the reason
	}{
		{"offline", func(x *flowFixture) { x.srv.Close() }, "check", "Couldn't reach the update server"},
		{"server error", func(x *flowFixture) { x.srv.set(func(s *fakeUpdateServer) { s.status = 503 }) }, "check", "HTTP 503"},
		{"forged manifest", func(x *flowFixture) {
			x.srv.set(func(s *fakeUpdateServer) {
				s.manifest = []byte(strings.Replace(string(s.manifest), "1.1.0", "1.1.1", 1))
			})
		}, "check", "signature check"},
		{"damaged download", func(x *flowFixture) { x.srv.set(func(s *fakeUpdateServer) { s.corrupt = true }) }, "download", "damaged"},
		{"wrong publisher", func(x *flowFixture) { x.b.verifyErr = updater.ErrSignatureInvalid }, "download", "same publisher"},
		{"install fails", func(x *flowFixture) { x.b.applyErr = errors.New("move the old app aside: permission denied") }, "restart", "isn't allowed to replace"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := newFlowFixture(t, "1.0.0")
			c.setup(x)
			x.f.Check(bg, false)
			x.f.Download(bg)
			x.f.Restart()
			st := x.f.State()
			if st.Phase != phFailed || st.Retry != c.step || !strings.Contains(st.Error, c.want) {
				t.Fatalf("%+v", st)
			}
			if c.step == "check" && st.LastChecked != "" {
				t.Error("a failed check must not count as checked")
			}
			if c.step == "restart" {
				if _, err := os.Stat(filepath.Join(x.dir, "state", reopenMarker)); err == nil {
					t.Error("reopen marker left after a failed install")
				}
			}
		})
	}
}

// Retry repeats the failed step, and only that step.
func TestUpdateFlowRetry(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	x.srv.set(func(s *fakeUpdateServer) { s.corrupt = true })
	x.f.Check(bg, false)
	x.f.Download(bg)
	if st := x.f.State(); st.Retry != "download" {
		t.Fatalf("%+v", st)
	}
	x.f.Restart() // not ready: ignored
	if x.b.applied != "" {
		t.Fatal("restart ran without a verified download")
	}
	x.srv.set(func(s *fakeUpdateServer) { s.corrupt = false })
	x.f.Retry(bg)
	if st := x.f.State(); st.Phase != phReady {
		t.Fatalf("after retry: %+v", st)
	}
}

// Progress moves while the artifact streams, and steps don't overlap.
func TestUpdateFlowProgressAndBusy(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	gate := make(chan struct{})
	x.srv.set(func(s *fakeUpdateServer) { s.chunk = gate })
	x.f.Check(bg, false)
	done := make(chan struct{})
	go func() { x.f.Download(bg); close(done) }()
	var pcts []int
	for i := 0; i < 4; i++ {
		gate <- struct{}{}
		deadline := time.Now().Add(2 * time.Second)
		for x.f.State().Percent < (i+1)*25-1 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		pcts = append(pcts, x.f.State().Percent)
		if i == 1 {
			x.f.Check(bg, false) // ignored: a download is running
			if x.f.State().Phase != phDownloading {
				t.Fatal("check interrupted the download")
			}
		}
	}
	<-done
	if pcts[0] != 25 || pcts[1] != 50 || pcts[2] != 75 || pcts[3] != 99 && pcts[3] != 100 {
		t.Fatalf("progress %v", pcts)
	}
	if x.f.State().Phase != phReady {
		t.Fatalf("%+v", x.f.State())
	}
}

// The daily check downloads on its own and sends one notice; a manual check
// stops at "available". A background check leaves a ready update alone.
func TestUpdateFlowBackground(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	x.f.Check(bg, true)
	if st := x.f.State(); st.Phase != phReady || len(x.notes) != 1 || x.notes[0] != "Lolad 1.1.0 is ready" {
		t.Fatalf("%+v notes=%v", st, x.notes)
	}
	x.f.Check(bg, true)
	x.f.Check(bg, false)
	if x.f.State().Phase != phReady || len(x.notes) != 1 {
		t.Fatal("a ready update was dropped or announced twice")
	}
}

func TestUpdateFlowOnboarding(t *testing.T) {
	x := newFlowFixture(t, "1.0.0")
	x.f.Check(bg, false)
	x.b.onboarding = true
	x.f.Download(bg)
	if st := x.f.State(); st.Phase != phOnboarding {
		t.Fatalf("%+v", st)
	}
	x.b.onboarding = false
	x.f.Download(bg)
	x.b.onboarding = true
	x.f.Restart()
	if st := x.f.State(); st.Phase != phFailed || st.Retry != "restart" || x.b.applied != "" {
		t.Fatalf("%+v", st)
	}
	x.b.onboarding = false
	x.f.Retry(bg)
	if x.b.applied == "" {
		t.Fatal("retry after setup did not install")
	}
}

func TestUpdateFlowUnavailable(t *testing.T) {
	f := newUpdateFlow(nil, "0.0.0-dev", t.TempDir())
	f.Check(bg, false)
	f.Download(bg)
	f.Restart()
	if st := f.State(); st.Phase != phUnavailable {
		t.Fatalf("%+v", st)
	}
	if l, on := trayUpdateLabel(f.State()); l != "Updates unavailable" || on {
		t.Fatal(l)
	}
	if got := unavailableReason(updater.ErrTestKey); !strings.Contains(got, "development build") {
		t.Fatal(got)
	}
}

func TestTrayUpdateLabel(t *testing.T) {
	for st, want := range map[UpdateState]string{
		{Phase: phIdle}:                         "Check for updates…",
		{Phase: phUpToDate}:                     "Check for updates…",
		{Phase: phChecking}:                     "Checking for updates…",
		{Phase: phAvailable, Version: "1.1.0"}:  "Update to 1.1.0…",
		{Phase: phDownloading, Percent: 42}:     "Downloading update… 42%",
		{Phase: phReady, Version: "1.1.0"}:      "Restart to update to 1.1.0…",
		{Phase: phFailed, Error: "x"}:           "Update failed: try again…",
		{Phase: phUpdated, Current: "1.1.0"}:    "Check for updates…",
		{Phase: phRestarting, Version: "1.1.0"}: "Updating…",
	} {
		if got, _ := trayUpdateLabel(st); got != want {
			t.Errorf("%s: %q, want %q", st.Phase, got, want)
		}
	}
}

func TestStopDaemonFrom(t *testing.T) {
	bundle := "/Applications/Lolad.app"
	up := true
	health := func(string) *Health {
		if !up {
			return nil
		}
		return &Health{Product: "lolad", PID: 4242, Exe: bundle + "/Contents/Helpers/lolad", Install: "app"}
	}
	var killed []int
	kill := func(pid int) error { killed = append(killed, pid); up = false; return nil }
	if err := stopDaemonFrom("http://127.0.0.1:4100", bundle, health, kill, time.Second); err != nil || len(killed) != 1 || killed[0] != 4242 {
		t.Fatalf("app daemon: err=%v killed=%v", err, killed)
	}
	// A CLI daemon (Homebrew) and a look-alike path are left alone.
	for _, exe := range []string{"/opt/homebrew/bin/lolad", "/Applications/Lolad.app.old/Contents/Helpers/lolad"} {
		up, killed = true, nil
		h := func(string) *Health { return &Health{Product: "lolad", PID: 7, Exe: exe} }
		stopDaemonFrom("http://127.0.0.1:4100", bundle, h, kill, time.Second)
		if len(killed) != 0 {
			t.Errorf("%s was stopped", exe)
		}
	}
}

func TestUpdateReason(t *testing.T) {
	for err, want := range map[error]string{
		context.DeadlineExceeded:                                                            "Couldn't reach",
		errors.New("update manifest: HTTP 404"):                                             "(HTTP 404)",
		errors.New("write: no space left on device"):                                        "free disk space",
		&os.PathError{Op: "rename", Path: "/Applications/Lolad.app", Err: os.ErrPermission}: "isn't allowed to replace",
		errors.New("odd"): "Something went wrong: odd",
	} {
		if got := updateReason(err); !strings.Contains(got, want) {
			t.Errorf("%v: %q", err, got)
		}
	}
}
