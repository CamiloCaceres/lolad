package fake

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/mailprov"
	"os"
	"path/filepath"
	"sync"
)

var emailManagers sync.Map

type demoMailState struct {
	manager *mailprov.Manager
	dir     string
}

func (s *Service) closeEmail() {
	if v, ok := emailManagers.LoadAndDelete(s); ok {
		os.RemoveAll(v.(demoMailState).dir)
	}
}

type demoProvider struct{ users []string }

func (p *demoProvider) Create(_ context.Context, a, pw string) error {
	p.users = append(p.users, a)
	return nil
}
func (p *demoProvider) List(context.Context) ([]string, error) { return p.users, nil }
func (p *demoProvider) Delete(_ context.Context, a string) error {
	for i, v := range p.users {
		if v == a {
			p.users = append(p.users[:i], p.users[i+1:]...)
			break
		}
	}
	return nil
}
func (p *demoProvider) Reset(context.Context, string, string) error { return nil }
func (p *demoProvider) Domains(context.Context) ([]string, error) {
	return []string{"mail.example.com"}, nil
}
func (s *Service) mailManager() *mailprov.Manager {
	if v, ok := emailManagers.Load(s); ok {
		return v.(demoMailState).manager
	}
	dir, e := os.MkdirTemp("", "lolad-fake-email-")
	if e != nil {
		panic(e)
	}
	// The demo shows a configured provider; real installs start with manual.
	if e = os.WriteFile(filepath.Join(dir, "email.json"), []byte(`{"provider":"purelymail","domain":"mail.example.com","pattern":"{slug}@{domain}","operators":["operator@example.com"],"watch":"lolad"}`), 0600); e != nil {
		panic(e)
	}
	m := mailprov.New(s, dir, func(name, key string) (string, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, c, e := s.agentFor(name)
		if e != nil {
			return "", e
		}
		return c.envValue(key), nil
	})
	m.Override = &demoProvider{users: []string{"ultron@mail.example.com"}}
	v, loaded := emailManagers.LoadOrStore(s, demoMailState{m, dir})
	if loaded {
		os.RemoveAll(dir)
	}
	return v.(demoMailState).manager
}
func (s *Service) EmailFleet(c context.Context) (mailprov.Fleet, error) {
	return s.mailManager().EmailFleet(c)
}
func (s *Service) EmailProvider(c context.Context, u *mailprov.ProviderUpdate) (mailprov.Settings, error) {
	return s.mailManager().EmailProvider(c, u)
}
func (s *Service) ProvisionEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return s.mailManager().ProvisionEmail(c, n, r)
}
func (s *Service) RotateEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return s.mailManager().RotateEmail(c, n, r)
}
func (s *Service) DetachEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return s.mailManager().DetachEmail(c, n, r)
}

func (s *Service) CheckEmail(c context.Context, n, address string) (core.EmailTest, error) {
	return s.mailManager().CheckEmail(c, n, address)
}
