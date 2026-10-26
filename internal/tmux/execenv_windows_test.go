package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsExitStatusPersistsAndResets(t *testing.T) {
	dir := t.TempDir()
	s := &Sessions{RunDir: dir}
	if code := s.exitStatus("agent", 0); code != -1 {
		t.Fatalf("missing status looks successful: %d", code)
	}
	path := filepath.Join(dir, "agent.exit")
	if err := os.WriteFile(path, []byte("7"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := s.exitStatus("agent", 0); code != 7 {
		t.Fatalf("status=%d", code)
	}
	args := exitStatusArgs(dir, "agent")
	if len(args) != 2 || args[1] != path {
		t.Fatalf("status path not passed privately: %v", args)
	}
	if code := s.exitStatus("agent", 0); code != -1 {
		t.Fatal("new launch inherited old exit status")
	}
}
