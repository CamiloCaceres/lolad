package registry

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func eperm(op, p string) error { return &fs.PathError{Op: op, Path: p, Err: syscall.EPERM} }

func agentIn(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "lolabot.yaml"), []byte("agent: {name: "+name+"}\nruntime: {harness: claude}\n"), 0o644)
	return dir
}

// macOS refuses the drive with the agents ("operation not permitted"): the
// daemon still starts, the folder is reported, its agents are kept (not
// removed and stopped), and they are back once access is allowed.
func TestReloadSkipsFolderMacOSRefuses(t *testing.T) {
	home, drive := t.TempDir(), t.TempDir()
	agentIn(t, home, "local")
	agentIn(t, drive, "ultron")
	config := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(config, []byte("agents_dirs: ["+home+", "+drive+"]\n"), 0o644)

	denied := false
	t.Cleanup(func() { readDir = os.ReadDir })
	readDir = func(p string) ([]os.DirEntry, error) {
		if denied && p == drive {
			return nil, eperm("open", p)
		}
		return os.ReadDir(p)
	}
	names := func(r *Registry) (n []string) {
		for _, a := range r.List() {
			n = append(n, a.Name)
		}
		return n
	}

	// Denied from the start: the registry loads what it can.
	denied = true
	r, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !slices.Equal(names(r), []string{"local"}) || !slices.Equal(r.Blocked(), []string{drive}) || len(r.Diagnostics()) != 1 {
		t.Fatalf("agents %v blocked %v diag %v", names(r), r.Blocked(), r.Diagnostics())
	}
	// Allowed: ultron appears.
	denied = false
	d, err := r.Reload()
	if err != nil || len(d.Added) != 1 || len(r.Blocked()) != 0 {
		t.Fatalf("%v %+v %v", err, d, r.Blocked())
	}
	// Refused again later (after an update, say): ultron is kept as it was,
	// and the refusal is reported once, not on every retry.
	denied = true
	for i := 0; i < 3; i++ {
		d, err = r.Reload()
		if err != nil || len(d.Removed)+len(d.Added)+len(d.Changed) != 0 || !slices.Equal(names(r), []string{"local", "ultron"}) {
			t.Fatalf("retry %d: %v %+v %v", i, err, d, names(r))
		}
		if want := map[bool]int{true: 1, false: 0}[i == 0]; len(d.Rejected) != want {
			t.Fatalf("retry %d: events %v", i, d.Rejected)
		}
	}
}

// An agent listed on its own (agents:) whose folder is refused.
func TestReloadSkipsAgentMacOSRefuses(t *testing.T) {
	dir := agentIn(t, t.TempDir(), "ultron")
	config := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(config, []byte("agents_dirs: []\nagents: ["+dir+"]\n"), 0o644)
	t.Cleanup(func() { readAgent = ReadAgent })
	readAgent = func(p string) (core.Agent, error) {
		return core.Agent{}, eperm("open", filepath.Join(p, "lolabot.yaml"))
	}
	r, err := New(config)
	if err != nil || len(r.List()) != 0 || !slices.Equal(r.Blocked(), []string{dir}) {
		t.Fatalf("%v %v %v", err, r.List(), r.Blocked())
	}
}

func TestUnder(t *testing.T) {
	for _, c := range []struct {
		dir  string
		want bool
	}{{"/Volumes/D/a", true}, {"/Volumes/D", true}, {"/Volumes/DX/a", false}, {"/Volumes", false}} {
		if under(c.dir, []string{"/Volumes/D"}) != c.want {
			t.Errorf("%s", c.dir)
		}
	}
}
