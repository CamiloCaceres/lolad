package cli

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestInstanceLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "lolad.lock")
	a, err := acquireInstanceLock(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstanceLock(p); !errors.Is(err, errLocked) {
		t.Fatalf("second lock: %v", err)
	}
	a.Release()
	b, err := acquireInstanceLock(p)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	b.Release()
}
