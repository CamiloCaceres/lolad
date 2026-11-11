package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingLog(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLog(filepath.Join(dir, "logs"), "daemon.log", 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: 2 per file
	for i := 0; i < 20; i++ {
		if _, err := l.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		info, _ := e.Info()
		if info.Size() > 100 {
			t.Errorf("%s is %d bytes", e.Name(), info.Size())
		}
	}
	if len(names) != 3 {
		t.Fatalf("want daemon.log, .1, .2 only; got %v", names)
	}
}
