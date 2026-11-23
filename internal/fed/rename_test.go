package fed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A rename tells each peer we are leaving (their side marks us removed) and
// changes nothing on ours; RenameConnections then empties our file.
func TestNotifyRenameAndConnections(t *testing.T) {
	a, b := setup(t, "home"), setup(t, "jugui")
	pair(t, a, b)
	a.f.NotifyRename(t.Context())
	if cs, _ := a.f.Connections(t.Context()); len(cs) != 1 {
		t.Fatal("our side changed before the restart", cs)
	}
	cs, _ := b.f.Connections(t.Context())
	if len(cs) != 1 || cs[0].LastError != "removed by them" {
		t.Fatalf("peer not told: %+v", cs)
	}
	if n, e := a.f.OutboxPending(t.Context()); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e := RenameConnections(a.config); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(a.config, "connections.yaml"))
	if strings.Contains(string(got), "jugui") || !strings.Contains(string(got), "peers: []") {
		t.Fatal(string(got))
	}
	// No file stays no file.
	empty := t.TempDir()
	if e := RenameConnections(empty); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(empty, "connections.yaml")); !os.IsNotExist(e) {
		t.Fatal("file created")
	}
}
