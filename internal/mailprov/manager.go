package mailprov

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/core"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type recovery struct {
	Address, Key, Provider, Operation string
	Created                           bool
}
type Manager struct {
	mu       sync.Mutex
	backend  Backend
	dir      string
	password func(string, string) (string, error)
	Override Provider
	last     map[string]Row
}

func New(b Backend, dir string, password func(string, string) (string, error)) *Manager {
	return &Manager{backend: b, dir: dir, password: password, last: map[string]Row{}}
}
func defaults() Settings {
	return Settings{Provider: "manual", Pattern: "{slug}@{domain}", Watch: "lolad"}
}
func (m *Manager) read() (Settings, error) {
	s := defaults()
	b, e := os.ReadFile(filepath.Join(m.dir, "email.json"))
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil || json.Unmarshal(b, &s) != nil {
		return s, errors.New("cannot read mail settings")
	}
	_, e = os.Stat(filepath.Join(m.dir, "secrets", "purelymail-token"))
	s.TokenSet = e == nil
	return s, nil
}
func writePrivate(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return writeBytes(path, b)
}
func writeBytes(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return errors.New("cannot create private mail state")
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".email-*")
	if e != nil {
		return errors.New("cannot write private mail state")
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), path)
	}
	if e != nil {
		return errors.New("cannot save private mail state")
	}
	return nil
}
func validAddress(s string) bool {
	a, e := mail.ParseAddress(s)
	return e == nil && a.Address == s && !strings.ContainsAny(s, "\r\n ") && strings.Contains(s, "@")
}
func (m *Manager) EmailProvider(ctx context.Context, u *ProviderUpdate) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	if e != nil || u == nil {
		return s, e
	}
	n := u.Settings
	if n.Provider == "" {
		n = s
	}
	if n.Provider != "manual" && n.Provider != "purelymail" {
		return s, fmt.Errorf("%w: choose manual or purelymail", core.ErrInvalid)
	}
	if !validAddress(strings.NewReplacer("{slug}", "agent", "{domain}", n.Domain).Replace(n.Pattern)) || !strings.Contains(n.Pattern, "{slug}") {
		return s, fmt.Errorf("%w: invalid address pattern", core.ErrInvalid)
	}
	if n.Watch != "lolad" && n.Watch != "none" {
		return s, fmt.Errorf("%w: default watch must be lolad or none", core.ErrInvalid)
	}
	for _, a := range n.Operators {
		if !validAddress(a) {
			return s, core.ErrInvalid
		}
	}
	if u.Token != "" {
		if strings.ContainsAny(u.Token, "\r\n") {
			return s, core.ErrInvalid
		}
		if e = writeBytes(filepath.Join(m.dir, "secrets", "purelymail-token"), []byte(u.Token)); e != nil {
			return s, e
		}
	}
	n.TokenSet = false
	if e = writePrivate(filepath.Join(m.dir, "email.json"), n); e != nil {
		return s, e
	}
	return m.read()
}
func (m *Manager) provider(s Settings) (Provider, error) {
	if m.Override != nil {
		return m.Override, nil
	}
	if s.Provider == "manual" {
		return Manual{}, nil
	}
	b, e := os.ReadFile(filepath.Join(m.dir, "secrets", "purelymail-token"))
	if e != nil || len(b) == 0 {
		return nil, fmt.Errorf("%w: set the Purelymail API token first", core.ErrInvalid)
	}
	return &Purelymail{Token: string(b)}, nil
}
func (m *Manager) EmailFleet(ctx context.Context) (Fleet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	f := Fleet{Settings: s, Agents: []Row{}}
	if e != nil {
		return f, e
	}
	agents, e := m.backend.List(ctx)
	for _, a := range agents {
		row := m.last[a.Name]
		row.Name, row.Display, row.PendingRestart = a.Name, a.Display, a.PendingRestart
		row.Suggested = strings.NewReplacer("{slug}", a.Name, "{domain}", s.Domain).Replace(s.Pattern)
		em, err := m.backend.Email(ctx, a.Name)
		if err != nil {
			row.Error = "Email settings unavailable"
		}
		row.Supported = em.Supported
		row.Watch = em.Watch
		row.Address = ""
		if len(em.Accounts) > 0 {
			row.Address = em.Accounts[0].Address
			row.Provider = "manual"
			if strings.HasSuffix(em.Accounts[0].IMAPServer, "purelymail.com") {
				row.Provider = "purelymail"
			}
		}
		if row.LastTest != nil && !strings.EqualFold(row.TestAddress, row.Address) {
			row.LastTest = nil
			row.TestedAt = time.Time{}
		}
		f.Agents = append(f.Agents, row)
	}
	slices.SortStableFunc(f.Agents, func(a, b Row) int {
		rank := func(r Row) int {
			if r.Address != "" {
				return 0
			}
			if r.Supported {
				return 1
			}
			return 2
		}
		if rank(a) != rank(b) {
			return rank(a) - rank(b)
		}
		return strings.Compare(strings.ToLower(a.Display), strings.ToLower(b.Display))
	})
	return f, e
}
func (m *Manager) journal(name string) string {
	return filepath.Join(m.dir, "email-recovery", name+".json")
}
func (m *Manager) account(ctx context.Context, name string, r Request) (core.EmailSettings, string, error) {
	em, e := m.backend.Email(ctx, name)
	if e != nil {
		return em, "", e
	}
	address := r.Address
	if address == "" && len(em.Accounts) > 0 {
		address = em.Accounts[0].Address
	}
	return em, address, nil
}
func (m *Manager) ProvisionEmail(ctx context.Context, name string, r Request) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	if e != nil {
		return Result{}, e
	}
	if r.Watch != "" {
		if r.Watch != "none" && r.Watch != "lolad" {
			return Result{}, core.ErrInvalid
		}
		s.Watch = r.Watch
	}
	em, e := m.backend.Email(ctx, name)
	if e != nil {
		return Result{}, e
	}
	if !em.Supported {
		return Result{}, fmt.Errorf("%w: install tools/email.sh first", core.ErrInvalid)
	}
	address := strings.ToLower(strings.TrimSpace(r.Address))
	if address == "" {
		address = strings.ToLower(strings.TrimSpace(r.Account.Address))
	}
	if address == "" {
		address = strings.NewReplacer("{slug}", name, "{domain}", s.Domain).Replace(s.Pattern)
	}
	if !validAddress(address) {
		return Result{}, core.ErrInvalid
	}
	if _, er := os.Stat(m.journal(name)); errors.Is(er, os.ErrNotExist) {
		for _, a := range em.Accounts {
			if strings.EqualFold(a.Address, address) {
				return m.test(ctx, name, address, "Mailbox is already attached."), nil
			}
		}
	} else {
		em.Accounts = slices.DeleteFunc(em.Accounts, func(a core.EmailAccount) bool { return strings.EqualFold(a.Address, address) })
	}
	acc := r.Account
	acc.Address = address
	if acc.Password != "" {
		if acc.IMAPServer == "" || acc.SMTPServer == "" {
			return Result{}, core.ErrInvalid
		}
	} else {
		p, e := m.provider(s)
		if e != nil {
			return Result{}, e
		}
		if s.Provider == "manual" {
			return Result{}, ErrManual
		}
		rec := recovery{}
		b, er := os.ReadFile(m.journal(name))
		if er == nil {
			if json.Unmarshal(b, &rec) != nil || rec.Address != address || rec.Operation != "provision" || rec.Provider != s.Provider {
				return Result{}, fmt.Errorf("%w: resolve the pending mailbox operation first", core.ErrConflict)
			}
		} else if !errors.Is(er, os.ErrNotExist) {
			return Result{}, errors.New("cannot read mailbox recovery state")
		}
		if rec.Key == "" {
			users, e := p.List(ctx)
			if e != nil {
				return Result{}, e
			}
			if slices.ContainsFunc(users, func(a string) bool { return strings.EqualFold(a, address) }) {
				return Result{}, ErrExists
			}
			rec = recovery{Address: address, Key: "EMAIL_PASSWORD_LOLAD_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_" + rand.Text(), Provider: s.Provider, Operation: "provision"}
			pw := make([]byte, 32)
			if _, e = rand.Read(pw); e != nil {
				return Result{}, errors.New("cannot generate mailbox password")
			}
			if e = m.backend.SetEnv(ctx, name, rec.Key, base64.RawURLEncoding.EncodeToString(pw)); e != nil {
				return Result{}, errors.New("cannot stage mailbox password in agent environment")
			}
			if e = writePrivate(m.journal(name), rec); e != nil {
				return Result{}, e
			}
		}
		password, e := m.password(name, rec.Key)
		if e != nil || password == "" {
			return Result{}, errors.New("staged mailbox password unavailable; resolve recovery before retrying")
		}
		if !rec.Created {
			users, e := p.List(ctx)
			if e != nil {
				return Result{}, e
			}
			if slices.ContainsFunc(users, func(a string) bool { return strings.EqualFold(a, address) }) {
				return Result{}, errors.New("mailbox exists after an uncertain create; verify ownership and attach manually")
			}
			if e = p.Create(ctx, address, password); e != nil {
				return Result{}, e
			}
			rec.Created = true
			if e = writePrivate(m.journal(name), rec); e != nil {
				return Result{}, errors.New("mailbox created but recovery state could not be saved; do not create again")
			}
		}
		acc = core.EmailAccount{Address: address, ConfigKey: name + "_" + strings.ToLower(rec.Key[len(rec.Key)-8:]), Username: address, PasswordKey: rec.Key, Password: password, IMAPServer: "imap.purelymail.com", IMAPPort: 993, SMTPServer: "smtp.purelymail.com", SMTPPort: 465}
	}
	em.Accounts = append(em.Accounts, acc)
	em.OperatorEmails = s.Operators
	if em.Watch != "mod" {
		em.Watch = s.Watch
		em.WatchAccount = address
		em.Every = "2m"
	}
	if _, e = m.backend.SaveEmail(ctx, name, em); e != nil {
		return Result{}, errors.New("mailbox prepared but agent configuration could not be saved; retry this address to finish without creating again")
	}
	_ = os.Remove(m.journal(name))
	return m.test(ctx, name, address, "Email attached. Restart the agent to apply its new environment."), nil
}
func (m *Manager) test(ctx context.Context, name, address, message string) Result {
	t, e := m.backend.TestEmail(ctx, name, address)
	if e != nil {
		t = core.EmailTest{Detail: "Mailbox saved; connection test unavailable. Test again from settings."}
	}
	m.last[name] = Row{TestAddress: address, LastTest: &t, TestedAt: time.Now()}
	return Result{Address: address, Message: message, Test: &t}
}
func (m *Manager) RotateEmail(ctx context.Context, name string, r Request) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	if e != nil {
		return Result{}, e
	}
	em, address, e := m.account(ctx, name, r)
	if e != nil {
		return Result{}, e
	}
	i := slices.IndexFunc(em.Accounts, func(a core.EmailAccount) bool { return a.Address == address })
	if i < 0 {
		return Result{}, core.ErrNotFound
	}
	if em.Accounts[i].IMAPServer != "imap.purelymail.com" {
		return Result{}, ErrManual
	}
	s.Provider = "purelymail"
	p, e := m.provider(s)
	if e != nil {
		return Result{}, e
	}
	pw := make([]byte, 32)
	if _, e = rand.Read(pw); e != nil {
		return Result{}, errors.New("cannot generate password")
	}
	password := base64.RawURLEncoding.EncodeToString(pw)
	// Stage the replacement in the agent env before the irreversible provider change.
	key := "EMAIL_PASSWORD_LOLAD_ROTATE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_" + fmt.Sprintf("%x", pw[:6])
	if e = m.backend.SetEnv(ctx, name, key, password); e != nil {
		return Result{}, errors.New("cannot stage replacement password")
	}
	if e = p.Reset(ctx, address, password); e != nil {
		return Result{}, e
	}
	em.Accounts[i].PasswordKey = key
	em.Accounts[i].Password = password
	if _, e = m.backend.SaveEmail(ctx, name, em); e != nil {
		return Result{}, errors.New("provider password rotated; replacement remains in agent .env under EMAIL_PASSWORD_LOLAD_ROTATE key; retry rotation to repair configuration")
	}
	return m.test(ctx, name, address, "Password rotated. Restart the agent to apply it."), nil
}
func (m *Manager) DetachEmail(ctx context.Context, name string, r Request) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	em, address, e := m.account(ctx, name, r)
	if e != nil {
		return Result{}, e
	}
	i := slices.IndexFunc(em.Accounts, func(a core.EmailAccount) bool { return a.Address == address })
	if i < 0 {
		return Result{Address: address, Message: "Mailbox already detached."}, nil
	}
	if r.DeleteMailbox {
		if r.Confirm != address {
			return Result{}, fmt.Errorf("%w: type the full mailbox address to delete it", core.ErrInvalid)
		}
		if em.Accounts[i].IMAPServer != "imap.purelymail.com" {
			return Result{}, ErrManual
		}
		s, e := m.read()
		if e != nil {
			return Result{}, e
		}
		s.Provider = "purelymail"
		p, e := m.provider(s)
		if e != nil {
			return Result{}, e
		}
		users, e := p.List(ctx)
		if e != nil {
			return Result{}, e
		}
		if slices.ContainsFunc(users, func(a string) bool { return strings.EqualFold(a, address) }) {
			if e = p.Delete(ctx, address); e != nil {
				return Result{}, e
			}
		}
	}
	em.Accounts = slices.Delete(em.Accounts, i, i+1)
	if em.WatchAccount == address {
		em.Watch = "none"
		em.WatchAccount = ""
	}
	if _, e = m.backend.SaveEmail(ctx, name, em); e != nil {
		return Result{}, errors.New("could not save detached mailbox settings; retry detach")
	}
	delete(m.last, name)
	return Result{Address: address, Message: "Mailbox detached. Its environment secret is retained until removed in Secrets."}, nil
}

func (m *Manager) CheckEmail(ctx context.Context, name, address string) (core.EmailTest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, e := m.backend.TestEmail(ctx, name, address)
	if e == nil {
		m.last[name] = Row{TestAddress: address, LastTest: &t, TestedAt: time.Now()}
	}
	return t, e
}
