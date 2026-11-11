package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errLocked = errors.New("lock held by another process")

// instanceLock is the single-instance lock of `lolad serve`.
type instanceLock struct{ f *os.File }

// acquireInstanceLock takes an exclusive, non-blocking lock on path. The OS
// drops it when the process dies, so a crash never leaves a stale lock.
// It returns errLocked when another process holds it.
func acquireInstanceLock(path string) (*instanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return &instanceLock{f: f}, nil
}

func (l *instanceLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	unlockFile(l.f)
	l.f.Close()
	l.f = nil
}
