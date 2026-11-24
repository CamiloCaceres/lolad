package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func onDarwin(t *testing.T) {
	t.Helper()
	old := permissionGOOS
	permissionGOOS = "darwin"
	t.Cleanup(func() { permissionGOOS = old })
}

func TestClassifyPermission(t *testing.T) {
	onDarwin(t)
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	eperm := func(p string) error { return &fs.PathError{Op: "open", Path: p, Err: syscall.EPERM} }
	for _, c := range []struct {
		name    string
		err     error
		text    string
		ok      bool
		title   string
		setting string
	}{
		{"env on a removable volume", eperm("/Volumes/DevSSD/Developer/projects/iris/.env"), "", true, "macOS is blocking access to “DevSSD”", "Removable Volumes"},
		{"wrapped with %w", fmt.Errorf("read env: %w", eperm("/Volumes/DevSSD/a/.env")), "", true, "macOS is blocking access to “DevSSD”", "Removable Volumes"},
		{"wrapped as text", fmt.Errorf("read env: %v", eperm("/Volumes/My Drive/a/.env")), "", true, "macOS is blocking access to “My Drive”", "Removable Volumes"},
		{"what a dead pane printed", nil, "exit 1: Error: EPERM: operation not permitted, open '/Volumes/DevSSD/x/settings.json'", true, "macOS is blocking access to “DevSSD”", "Removable Volumes"},
		{"chdir into the agent folder", nil, "exit 1: chdir /Volumes/DevSSD/x: operation not permitted", true, "macOS is blocking access to “DevSSD”", "Removable Volumes"},
		{"Documents", eperm("/Users/milo/Documents/agents/iris/.env"), "", true, "macOS is blocking access to your Documents folder", "Documents Folder"},
		{"Desktop", eperm("/Users/milo/Desktop/x"), "", true, "macOS is blocking access to your Desktop folder", "Desktop Folder"},
		{"no path", errors.New("operation not permitted"), "", true, "macOS is blocking access to its folder", "the folder or drive it lives on"},
		{"Unix permission denied", &fs.PathError{Op: "open", Path: "/Volumes/DevSSD/a", Err: syscall.EACCES}, "", false, "", ""},
		{"ordinary crash", nil, "exit 1: API overloaded (529)", false, "", ""},
		{"nothing", nil, "", false, "", ""},
	} {
		p, ok := classifyPermission(c.err, c.text, now)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, want %v (%+v)", c.name, ok, c.ok, p)
			continue
		}
		if !ok {
			continue
		}
		if p.Kind != ProblemMacOSPermission || !p.NeedsYou || p.Title != c.title || !strings.Contains(p.Hint, c.setting) ||
			!strings.Contains(p.Hint, "Privacy & Security") || !p.Until.Equal(now.Add(permissionRetry)) || p.Detail == "" {
			t.Errorf("%s: %+v", c.name, p)
		}
	}
}

func TestClassifyPermissionOnlyOnMacOS(t *testing.T) {
	old := permissionGOOS
	permissionGOOS = "linux"
	defer func() { permissionGOOS = old }()
	if p, ok := classifyPermission(&fs.PathError{Op: "open", Path: "/mnt/x/.env", Err: syscall.EPERM}, "", time.Now()); ok {
		t.Fatalf("linux: %+v", p)
	}
}

// blockedLauncher fails Prepare with EPERM while blocked is set, like the
// harness reading .env from a drive macOS has taken away.
type blockedLauncher struct {
	fakeLauncher
	blocked bool
	tries   int
}

func (b *blockedLauncher) Prepare(a core.Agent, o core.LaunchOpts) (core.LaunchSpec, error) {
	b.tries++
	if b.blocked {
		return core.LaunchSpec{}, &fs.PathError{Op: "open", Path: "/Volumes/DevSSD/Developer/projects/agent/.env", Err: syscall.EPERM}
	}
	return b.fakeLauncher.Prepare(a, o)
}

// A blocked agent waits for the user and retries every 15 s for as long as
// it takes, without crashing into "failed"; once access is back it starts
// on the next retry.
func TestPermissionBlockRetriesWithoutCrashBudget(t *testing.T) {
	onDarwin(t)
	b := &blockedLauncher{blocked: true}
	f := setup(t, func(o *Options) {
		o.Launchers = map[core.Harness]core.Launcher{core.HarnessClaude: b, core.HarnessCodewhale: b}
	})
	f.tick(t)
	for i := 0; i < 20; i++ { // far more than the six crashes that mean "failed"
		a := f.state(t)
		if a.State != core.StateWaiting || a.Problem == nil || a.Problem.Kind != ProblemMacOSPermission || !a.Problem.NeedsYou ||
			a.Restarts != 0 || a.NextRestart.Sub(f.c.Now()) != permissionRetry {
			t.Fatalf("attempt %d: %+v problem %+v", i, a, a.Problem)
		}
		tries := b.tries
		f.c.advance(permissionRetry - time.Second)
		f.tick(t)
		if b.tries != tries {
			t.Fatal("retried before 15 s")
		}
		f.c.advance(time.Second)
		f.tick(t)
		if b.tries != tries+1 {
			t.Fatalf("attempt %d: did not retry after 15 s", i)
		}
	}
	since := f.state(t).Problem.Since
	if !since.Equal(time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("problem since %v: the same problem keeps when it began", since)
	}
	b.blocked = false // the user clicked Allow
	f.c.advance(permissionRetry)
	f.tick(t)
	a := f.state(t)
	if a.State != core.StateStarting || a.Problem != nil || a.Restarts != 0 || f.sessions.starts != 1 {
		t.Fatalf("after Allow: %+v problem %+v starts %d", a, a.Problem, f.sessions.starts)
	}
}

// A harness that dies saying "operation not permitted" is blocked, not crashed.
func TestDeadPaneNotPermittedIsBlocked(t *testing.T) {
	onDarwin(t)
	f := setup(t, nil)
	f.tick(t)
	f.sessions.dead = true
	f.sessions.pane = "Error: EPERM: operation not permitted, open '/Volumes/DevSSD/Developer/projects/agent/.claude/settings.json'\n"
	f.tick(t)
	a := f.state(t)
	if a.State != core.StateWaiting || a.Problem == nil || a.Problem.Kind != ProblemMacOSPermission || a.Restarts != 0 {
		t.Fatalf("%+v problem %+v", a, a.Problem)
	}
	f.tick(t) // no session while blocked: not "session disappeared"
	if a := f.state(t); a.State != core.StateWaiting || a.Restarts != 0 {
		t.Fatalf("second tick: %+v", a)
	}
	f.sessions.pane = ""
	f.c.advance(permissionRetry)
	f.tick(t)
	if a := f.state(t); a.State != core.StateStarting || a.Problem != nil {
		t.Fatalf("retry: %+v", a)
	}
}
