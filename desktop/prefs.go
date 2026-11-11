package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Prefs is the app's own small settings file. It holds nothing the daemon
// needs and no secrets: notification toggles, the window frame and a few
// "already asked" flags. Per-kind notification toggles will move to the
// daemon's Settings later (docs/desktop.md §6.4); until then they live here.
type Prefs struct {
	Notify map[string]bool `json:"notify,omitempty"` // kind -> enabled; missing = on
	Frame  *Frame          `json:"frame,omitempty"`

	NotifyAsked     bool   `json:"notify_asked,omitempty"`     // macOS permission was requested
	DefaultsApplied bool   `json:"defaults_applied,omitempty"` // login items switched on after onboarding
	MigrationDecl   string `json:"migration_declined,omitempty"`
}

// Frame is the main window's remembered position and size.
type Frame struct {
	X, Y, W, H int
}

const (
	kindAsk        = "ask"
	kindFailed     = "failed"
	kindFederation = "federation"
)

var notifyKinds = []struct{ Kind, Label string }{
	{kindAsk, "Questions from agents"},
	{kindFailed, "Agent failed"},
	{kindFederation, "Sender awaiting approval"},
}

// prefsDir is the app's config dir (separate from the daemon's ~/.config/lolad).
func prefsDir() string {
	if p := os.Getenv("LOLAD_DESKTOP_CONFIG"); p != "" {
		return p
	}
	d, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "Lolad")
}

type prefStore struct {
	mu   sync.Mutex
	path string
	p    Prefs
}

func loadPrefs(path string) *prefStore {
	s := &prefStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.p)
	}
	return s
}

func (s *prefStore) Get() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.p
	c.Notify = map[string]bool{}
	for k, v := range s.p.Notify {
		c.Notify[k] = v
	}
	return c
}

// Update applies fn and writes the file atomically.
func (s *prefStore) Update(fn func(*Prefs)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.p)
	b, err := json.MarshalIndent(s.p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// NotifyOn reports whether a notification kind is enabled (default on).
func (s *prefStore) NotifyOn(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.p.Notify[kind]
	return !ok || v
}

// usableFrame returns f when it is big enough and its top-left corner lies on
// one of the screens, so a window saved on an unplugged monitor reopens on a
// visible one. screens are x,y,w,h rectangles.
func usableFrame(f *Frame, screens [][4]int) *Frame {
	if f == nil || f.W < 940 || f.H < 600 {
		return nil
	}
	for _, s := range screens {
		if f.X >= s[0] && f.X < s[0]+s[2]-80 && f.Y >= s[1] && f.Y < s[1]+s[3]-80 {
			return f
		}
	}
	return nil
}
