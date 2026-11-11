package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// state is <state>/setup.json: how far the user got, so quitting midway
// resumes. Nothing secret lives here.
type state struct {
	Version      int       `json:"version"`
	ServiceDone  bool      `json:"service_done"`
	FolderSet    bool      `json:"folder_set"`
	NameSet      bool      `json:"name_set"`
	UserName     string    `json:"user_name,omitempty"`
	AgentsDir    string    `json:"agents_dir,omitempty"`
	SkippedAgent bool      `json:"skipped_agent"`
	Dismissed    bool      `json:"dismissed"`
	Completed    bool      `json:"completed"`
	CapturePath  *bool     `json:"capture_path,omitempty"` // nil: on
	UpdatedAt    time.Time `json:"updated_at"`
}

type stateFile struct {
	mu   sync.Mutex
	path string
	st   state
	ok   bool // loaded
}

func (f *stateFile) load() state {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked()
	return f.st
}

func (f *stateFile) loadLocked() {
	if f.ok {
		return
	}
	f.ok = true
	b, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	var s state
	if json.Unmarshal(b, &s) == nil {
		f.st = s
	}
}

// update applies fn and writes the result when something changed.
func (f *stateFile) update(now time.Time, fn func(*state)) (state, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked()
	next := f.st
	if f.st.CapturePath != nil {
		v := *f.st.CapturePath
		next.CapturePath = &v
	}
	fn(&next)
	next.Version = 1
	next.UpdatedAt = f.st.UpdatedAt
	if bytes.Equal(mustJSON(next), mustJSON(f.st)) {
		return f.st, nil
	}
	next.UpdatedAt = now
	if err := writeAtomic(f.path, mustJSON(next), 0o600); err != nil {
		return f.st, err
	}
	f.st = next
	return f.st, nil
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return append(b, '\n')
}

// writeAtomic replaces path with data, keeping an existing file's mode.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".setup-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
