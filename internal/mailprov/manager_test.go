package mailprov

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/CamiloCaceres/lolad/internal/agentcfg"
	"github.com/CamiloCaceres/lolad/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type diskBackend struct {
	d     *agentcfg.Dir
	fail  bool
	saved core.EmailSettings
	mod   bool
}

func (b *diskBackend) List(context.Context) ([]core.AgentStatus, error) {
	return []core.AgentStatus{{Agent: core.Agent{Name: "iris", Display: "Iris"}}}, nil
}
func (b *diskBackend) Email(context.Context, string) (core.EmailSettings, error) {
	v, e := b.d.Email("")
	if b.mod {
		v.Watch = "mod"
	}
	return v, e
}
func (b *diskBackend) SaveEmail(_ context.Context, _ string, v core.EmailSettings) (core.EmailSettings, error) {
	if b.fail {
		return core.EmailSettings{}, errors.New("save failed")
	}
	b.saved = v
	return b.d.SaveEmail("", v)
}
func (b *diskBackend) TestEmail(context.Context, string, string) (core.EmailTest, error) {
	return core.EmailTest{OK: true, IMAP: "ok", SMTP: "ok"}, nil
}
func (b *diskBackend) SetEnv(_ context.Context, _ string, key, value string) error {
	return b.d.SetEnv("", key, value)
}

type memoryProvider struct {
	users                    []string
	creates, resets, deletes int
	fail                     bool
}

func (p *memoryProvider) List(context.Context) ([]string, error) { return p.users, nil }
func (p *memoryProvider) Domains(context.Context) ([]string, error) {
	return []string{"example.test"}, nil
}
func (p *memoryProvider) Create(_ context.Context, a, pw string) error {
	p.creates++
	if p.fail {
		return errors.New("unavailable")
	}
	p.users = append(p.users, a)
	return nil
}
func (p *memoryProvider) Reset(context.Context, string, string) error {
	p.resets++
	if p.fail {
		return errors.New("unavailable")
	}
	return nil
}
func (p *memoryProvider) Delete(_ context.Context, address string) error {
	p.deletes++
	for i, a := range p.users {
		if a == address {
			p.users = append(p.users[:i], p.users[i+1:]...)
			break
		}
	}
	return nil
}
func fixture(t *testing.T) (*Manager, *diskBackend, *memoryProvider, string) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tools"), 0700)
	os.WriteFile(filepath.Join(root, "tools/email.sh"), []byte("#!/bin/sh\n"), 0700)
	os.WriteFile(filepath.Join(root, "lolabot.yaml"), []byte("agent:\n  name: Iris\n"), 0600)
	d := agentcfg.New(root, core.HarnessClaude, agentcfg.Options{UserHome: t.TempDir()})
	b := &diskBackend{d: d}
	state := t.TempDir()
	os.WriteFile(filepath.Join(state, "email.json"), []byte(`{"provider":"purelymail","domain":"mail.example.com","pattern":"{slug}@{domain}","operators":["operator@example.com"],"watch":"lolad"}`), 0600)
	m := New(b, state, func(_ string, key string) (string, error) { return d.ProvisionPassword("", key) })
	p := &memoryProvider{}
	m.Override = p
	return m, b, p, root
}
func TestProvisionPartialRetryAndSecretBoundary(t *testing.T) {
	m, b, p, root := fixture(t)
	ctx := context.Background()
	b.fail = true
	_, e := m.ProvisionEmail(ctx, "iris", Request{})
	if e == nil || !strings.Contains(e.Error(), "retry") {
		t.Fatal(e)
	}
	if p.creates != 1 {
		t.Fatal("create count")
	}
	b.fail = false
	// Recover after a daemon restart, with the same non-secret journal.
	m = New(b, m.dir, m.password)
	m.Override = p
	result, e := m.ProvisionEmail(ctx, "iris", Request{})
	if e != nil || result.Test == nil || !result.Test.OK {
		t.Fatal(result, e)
	}
	if p.creates != 1 {
		t.Fatal("retry created duplicate")
	}
	password, e := b.d.ProvisionPassword("", b.saved.Accounts[0].PasswordKey)
	if e != nil || len(password) < 40 {
		t.Fatal("missing strong password")
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), password) {
		t.Fatal("password in result")
	}
	for _, dir := range []string{root, m.dir} {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			v, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if strings.Contains(string(v), password) && filepath.Base(path) != ".env" {
				t.Errorf("password outside env: %s", path)
			}
			return nil
		})
	}
	st, _ := os.Stat(filepath.Join(root, ".env"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("env not private")
	}
	if b.saved.Watch != "lolad" || b.saved.OperatorEmails[0] != "operator@example.com" {
		t.Fatal("defaults missing")
	}
	if _, e = m.ProvisionEmail(ctx, "iris", Request{}); e != nil || p.creates != 1 {
		t.Fatal("not idempotent", e)
	}
}
func TestExistsAndMod(t *testing.T) {
	m, b, p, _ := fixture(t)
	p.users = []string{"iris@mail.example.com"}
	if _, e := m.ProvisionEmail(context.Background(), "iris", Request{}); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
	p.users = nil
	b.mod = true
	if _, e := m.ProvisionEmail(context.Background(), "iris", Request{}); e != nil {
		t.Fatal(e)
	}
	if b.saved.Watch != "mod" {
		t.Fatal("mod ownership changed")
	}
}
func TestTokenWriteOnly(t *testing.T) {
	m, _, _, _ := fixture(t)
	ctx := context.Background()
	u := ProviderUpdate{Token: "hidden-api-token"}
	s, e := m.EmailProvider(ctx, &u)
	if e != nil || !s.TokenSet {
		t.Fatal(s, e)
	}
	f, e := m.EmailFleet(ctx)
	if e != nil {
		t.Fatal(e)
	}
	v, _ := json.Marshal(f)
	if strings.Contains(string(v), u.Token) {
		t.Fatal("token returned")
	}
	config, _ := os.ReadFile(filepath.Join(m.dir, "email.json"))
	if strings.Contains(string(config), u.Token) {
		t.Fatal("token in config")
	}
	st, e := os.Stat(filepath.Join(m.dir, "secrets", "purelymail-token"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("token permissions", e)
	}
}
func TestRotateAndTypedDetach(t *testing.T) {
	m, b, p, _ := fixture(t)
	ctx := context.Background()
	m.ProvisionEmail(ctx, "iris", Request{})
	old, e := b.d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	p.fail = true
	if _, e = m.RotateEmail(ctx, "iris", Request{}); e == nil {
		t.Fatal("expected failed reset")
	}
	after, _ := b.d.Email("")
	if old.Accounts[0].PasswordKey != after.Accounts[0].PasswordKey {
		t.Fatal("failed reset changed active credential")
	}
	p.fail = false
	if _, e = m.RotateEmail(ctx, "iris", Request{}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.DetachEmail(ctx, "iris", Request{DeleteMailbox: true, Confirm: "wrong"}); e == nil || p.deletes != 0 {
		t.Fatal("delete without confirmation")
	}
	if _, e = m.DetachEmail(ctx, "iris", Request{DeleteMailbox: true, Confirm: "iris@mail.example.com"}); e != nil || p.deletes != 1 {
		t.Fatal(e)
	}
	after, _ = b.d.Email("")
	if len(after.Accounts) != 0 {
		t.Fatal("still attached")
	}
}

func TestManualAttachAndDefaultWatchChoice(t *testing.T) {
	m, b, p, _ := fixture(t)
	ctx := context.Background()
	settings := defaults()
	settings.Provider = "manual"
	settings.Domain = "example.test"
	if _, e := m.EmailProvider(ctx, &ProviderUpdate{Settings: settings}); e != nil {
		t.Fatal(e)
	}
	result, e := m.ProvisionEmail(ctx, "iris", Request{Address: "iris@example.test", Watch: "none", Account: core.EmailAccount{IMAPServer: "imap.example.test", SMTPServer: "smtp.example.test", Password: "manual-private-password"}})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "manual-private-password") || p.creates != 0 || b.saved.Watch != "none" {
		t.Fatal("manual attach violated secret/provider/watch boundary")
	}
}
func TestProvisionSecondMailboxAndDetachPartialRetry(t *testing.T) {
	m, b, p, _ := fixture(t)
	ctx := context.Background()
	if _, e := m.ProvisionEmail(ctx, "iris", Request{}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ProvisionEmail(ctx, "iris", Request{Address: "iris-second@mail.example.com"}); e != nil {
		t.Fatal(e)
	}
	em, e := b.d.Email("")
	if e != nil || len(em.Accounts) != 2 || em.Accounts[0].PasswordKey == em.Accounts[1].PasswordKey {
		t.Fatal("second mailbox replaced active credential", e)
	}
	b.fail = true
	q := Request{Address: "iris-second@mail.example.com", DeleteMailbox: true, Confirm: "iris-second@mail.example.com"}
	if _, e = m.DetachEmail(ctx, "iris", q); e == nil {
		t.Fatal("expected partial failure")
	}
	b.fail = false
	if _, e = m.DetachEmail(ctx, "iris", q); e != nil {
		t.Fatal(e)
	}
	if p.deletes != 1 {
		t.Fatal("repeated remote deletion")
	}
}
