package hub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Membership is this lolad's hub registration, stored in
// <config dir>/hub.yaml (0600). It holds no secret: the tunnel authenticates
// with the provider key.
type Membership struct {
	URL      string    `yaml:"url"`
	Handle   string    `yaml:"handle"`
	JoinedAt time.Time `yaml:"joined_at"`
}

// MembershipPath is hub.yaml next to config.yaml.
func MembershipPath(configDir string) string { return filepath.Join(configDir, "hub.yaml") }

// LoadMembership returns nil, nil when not joined.
func LoadMembership(path string) (*Membership, error) {
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var m Membership
	if e = yaml.Unmarshal(b, &m); e != nil {
		return nil, fmt.Errorf("hub.yaml: %w", e)
	}
	if m.URL == "" && m.Handle == "" {
		return nil, nil
	}
	if m.URL, e = NormalizeURL(m.URL); e != nil || !ValidHandle(m.Handle) {
		return nil, errors.New("hub.yaml: invalid url or handle")
	}
	return &m, nil
}

// SaveMembership writes hub.yaml atomically with mode 0600.
func SaveMembership(path string, m Membership) error {
	b, e := yaml.Marshal(m)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".hub-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0o600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if c := f.Close(); e == nil {
		e = c
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

// RemoveMembership deletes hub.yaml; missing is fine.
func RemoveMembership(path string) error {
	if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}
