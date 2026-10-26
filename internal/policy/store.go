package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

func clone(o core.Org) core.Org {
	b, _ := json.Marshal(o)
	var c core.Org
	_ = json.Unmarshal(b, &c)
	return c
}
func Load(path string) (core.Org, error) {
	o := DefaultOrg()
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return o, nil
	}
	if e != nil {
		return o, e
	}
	if e = yaml.Unmarshal(b, &o); e != nil {
		return o, e
	}
	_, e = Validate(o)
	return o, e
}

// Save replaces the file atomically. YAML comments are not retained.
func Save(path string, org core.Org) error {
	if _, err := Validate(org); err != nil {
		return err
	}
	b, err := yaml.Marshal(org)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".org-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}

// Store publishes independent snapshots; slow subscribers receive the latest change.
var _ core.Policy = (*Store)(nil)

type Store struct {
	mu    sync.RWMutex
	path  string
	org   core.Org
	known []string
	subs  map[chan core.Org]struct{}
}

func Open(path string, known ...string) (*Store, error) {
	o, e := Load(path)
	if e != nil {
		return nil, e
	}
	return &Store{path: path, org: o, known: append([]string{}, known...), subs: map[chan core.Org]struct{}{}}, nil
}
func (s *Store) Org() core.Org { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.org) }
func (s *Store) Check(f, t string) core.Decision {
	s.mu.RLock()
	p := New(s.org, s.known...)
	s.mu.RUnlock()
	return p.Check(f, t)
}
func (s *Store) Contacts(f string) []string {
	s.mu.RLock()
	p := New(s.org, s.known...)
	s.mu.RUnlock()
	return p.Contacts(f)
}
func (s *Store) publish(o core.Org) {
	if reflect.DeepEqual(s.org, o) {
		return
	}
	s.org = clone(o)
	for ch := range s.subs {
		v := clone(o)
		select {
		case ch <- v:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- v
		}
	}
}
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, e := Load(s.path)
	if e == nil {
		s.publish(o)
	}
	return e
}
func (s *Store) Save(o core.Org) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := Save(s.path, o); e != nil {
		return e
	}
	s.publish(o)
	return nil
}
func (s *Store) SetKnown(names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known = append([]string{}, names...)
}
func (s *Store) Subscribe(ctx context.Context) <-chan core.Org {
	ch := make(chan core.Org, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	go func() { <-ctx.Done(); s.mu.Lock(); delete(s.subs, ch); close(ch); s.mu.Unlock() }()
	return ch
}
func (s *Store) mutate(fn func(*core.Org) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := clone(s.org)
	if e := fn(&o); e != nil {
		return e
	}
	if e := Save(s.path, o); e != nil {
		return e
	}
	s.publish(o)
	return nil
}
func teamIndex(o *core.Org, id string) (int, error) {
	for i, t := range o.Teams {
		if t.ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("%w: team %s", core.ErrNotFound, id)
}
func (s *Store) AddTeam(t core.Team) error {
	return s.mutate(func(o *core.Org) error { o.Teams = append(o.Teams, t); return nil })
}
func (s *Store) UpdateTeam(t core.Team) error {
	return s.mutate(func(o *core.Org) error {
		i, e := teamIndex(o, t.ID)
		if e != nil {
			return e
		}
		o.Teams[i] = t
		return nil
	})
}
func (s *Store) RemoveTeam(id string) error {
	return s.mutate(func(o *core.Org) error {
		i, e := teamIndex(o, id)
		if e != nil {
			return e
		}
		o.Teams = append(o.Teams[:i], o.Teams[i+1:]...)
		return nil
	})
}
func removeAgent(o *core.Org, name string) {
	for i := range o.Teams {
		t := &o.Teams[i]
		if local(t.Head) == local(name) {
			t.Head = ""
		}
		m := []string{}
		for _, n := range t.Members {
			if local(n) != local(name) {
				m = append(m, n)
			}
		}
		t.Members = m
	}
}
func (s *Store) SetHead(id, name string) error {
	return s.mutate(func(o *core.Org) error {
		i, e := teamIndex(o, id)
		if e != nil {
			return e
		}
		if name != "" {
			removeAgent(o, name)
		}
		o.Teams[i].Head = local(name)
		return nil
	})
}
func (s *Store) AddMember(id, name string) error {
	return s.mutate(func(o *core.Org) error {
		i, e := teamIndex(o, id)
		if e != nil {
			return e
		}
		if name == "" {
			return core.ErrInvalid
		}
		removeAgent(o, name)
		o.Teams[i].Members = append(o.Teams[i].Members, local(name))
		return nil
	})
}
func (s *Store) RemoveMember(id, name string) error {
	return s.mutate(func(o *core.Org) error {
		i, e := teamIndex(o, id)
		if e != nil {
			return e
		}
		m := []string{}
		for _, n := range o.Teams[i].Members {
			if local(n) != local(name) {
				m = append(m, n)
			}
		}
		o.Teams[i].Members = m
		return nil
	})
}
func (s *Store) AddRule(r core.Rule) error {
	return s.mutate(func(o *core.Org) error { o.Rules = append(o.Rules, r); return nil })
}
func (s *Store) RemoveRule(i int) error {
	return s.mutate(func(o *core.Org) error {
		if i < 0 || i >= len(o.Rules) {
			return core.ErrNotFound
		}
		o.Rules = append(o.Rules[:i], o.Rules[i+1:]...)
		return nil
	})
}
func (s *Store) MoveRule(from, to int) error {
	return s.mutate(func(o *core.Org) error {
		if from < 0 || to < 0 || from >= len(o.Rules) || to >= len(o.Rules) {
			return core.ErrInvalid
		}
		r := o.Rules[from]
		o.Rules = append(o.Rules[:from], o.Rules[from+1:]...)
		o.Rules = append(o.Rules, core.Rule{})
		copy(o.Rules[to+1:], o.Rules[to:])
		o.Rules[to] = r
		return nil
	})
}
