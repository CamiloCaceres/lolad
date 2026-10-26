// Package agentcfg manages settings stored in a single lolabot directory.
package agentcfg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type Options struct{ UserHome string }

var directoryLocks sync.Map

type Dir struct {
	root    string
	harness core.Harness
	home    string
	mu      *sync.Mutex
}

func New(agentDir string, h core.Harness, o Options) *Dir {
	root, _ := filepath.Abs(agentDir)
	if p, e := filepath.EvalSymlinks(root); e == nil {
		root = p
	}
	if o.UserHome == "" {
		o.UserHome, _ = os.UserHomeDir()
	}
	lock, _ := directoryLocks.LoadOrStore(root, &sync.Mutex{})
	return &Dir{root: root, harness: h, home: o.UserHome, mu: lock.(*sync.Mutex)}
}
func hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func inside(root, p string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// safe resolves existing ancestors too, so a missing leaf cannot bypass symlink checks.
func (d *Dir) safe(rel string, missing bool) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", core.ErrInvalid
	}
	for _, s := range strings.Split(filepath.ToSlash(rel), "/") {
		if s == ".." {
			return "", core.ErrInvalid
		}
	}
	p := filepath.Join(d.root, filepath.Clean(rel))
	q := p
	for {
		r, e := filepath.EvalSymlinks(q)
		if e == nil {
			if !inside(d.root, r) {
				return "", core.ErrInvalid
			}
			if q == p {
				return r, nil
			}
			if missing {
				return p, nil
			}
			return "", core.ErrNotFound
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", core.ErrInvalid
		}
		if st, err := os.Lstat(q); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", core.ErrInvalid
		}
		if q == d.root || q == filepath.Dir(q) {
			return "", core.ErrNotFound
		}
		q = filepath.Dir(q)
	}
}
func atomicWrite(p string, b []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".lolad-backup-write-*")
	if e != nil {
		return errors.New("cannot create settings file")
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode.Perm()); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), p)
	}
	if e != nil {
		return errors.New("cannot write settings file")
	}
	return nil
}
