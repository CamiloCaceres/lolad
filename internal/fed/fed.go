// Package fed implements pinned, allowlist AMP federation. Construct with New,
// mount Handler at /v1/federation/ on both listeners (outside device auth), and
// run Run(ctx) in a goroutine. New attaches AMP's remote hook automatically.
// Register OnApproval for pending first-message approvals and OnChange for
// outbound delivery updates; EventSink receives metadata-only federation events.
package fed

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

type Options struct {
	DB                  *sql.DB
	StateDir, ConfigDir string
	Provider            *amp.Provider
	Enabled             bool
	Display, URL        string
	Org                 func() core.Org
	Agents              func() []core.Agent
	EventSink           func(core.Event)
	HTTPClient          *http.Client
	Clock               func() time.Time
	Backoff             []time.Duration
}
type peer struct {
	core.Connection
	PublicKey string `json:"public_key"`
	Removed   bool   `json:"removed,omitempty"`
}
type document struct {
	Peers  []peer       `json:"peers"`
	Grants []core.Grant `json:"grants"`
}
type cacheEntry struct {
	At       time.Time
	Contacts []core.RemoteContact
}
type rateWindow struct {
	At    time.Time
	Count int
}
type Engine struct {
	o         Options
	key       ed25519.PrivateKey
	pub       string
	mu        sync.Mutex
	doc       document
	rates     map[string]rateWindow
	cache     map[string]cacheEntry
	approvals []func(core.Grant)
	changes   []func(core.Mail)
	wake      chan struct{}
}

var _ core.Federator = (*Engine)(nil)
var _ amp.Remote = (*Engine)(nil)

const schema = `
CREATE TABLE IF NOT EXISTS fed_invites(secret_hash TEXT PRIMARY KEY, expires INTEGER NOT NULL, used_at INTEGER, used_by TEXT);
CREATE TABLE IF NOT EXISTS fed_outbox(id TEXT PRIMARY KEY, peer TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL, created_at INTEGER NOT NULL, last_error TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'deliver', body BLOB);
CREATE TABLE IF NOT EXISTS fed_sender_keys(peer TEXT NOT NULL,address TEXT NOT NULL,public_key TEXT NOT NULL,first_seen INTEGER NOT NULL,PRIMARY KEY(peer,address));
CREATE TABLE IF NOT EXISTS fed_received(id TEXT PRIMARY KEY,peer TEXT NOT NULL,from_addr TEXT NOT NULL,to_addr TEXT NOT NULL,status TEXT NOT NULL);
`

func New(o Options) (*Engine, error) {
	if o.DB == nil || o.Provider == nil || o.StateDir == "" || o.ConfigDir == "" {
		return nil, fmt.Errorf("%w: federation database, provider and directories required", core.ErrInvalid)
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	// Redirects must never forward a signed request or invite to a different host.
	client := *o.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.HTTPClient = &client
	if o.Org == nil {
		o.Org = func() core.Org { return core.Org{} }
	}
	if o.Agents == nil {
		o.Agents = func() []core.Agent { return nil }
	}
	if o.Display == "" {
		words := strings.Fields(o.Org().User.Display)
		if len(words) > 0 {
			o.Display = words[0]
		} else {
			o.Display = o.Org().User.Name
		}
	}
	o.URL = strings.TrimRight(o.URL, "/")
	if o.URL != "" && !validURL(o.URL) {
		return nil, fmt.Errorf("%w: federation URL requires HTTPS (HTTP is loopback-only)", core.ErrInvalid)
	}
	if len(o.Backoff) == 0 {
		o.Backoff = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}
	}
	f := &Engine{o: o, rates: map[string]rateWindow{}, cache: map[string]cacheEntry{}, wake: make(chan struct{}, 1)}
	if e := f.loadKey(); e != nil {
		return nil, e
	}
	if _, e := o.DB.Exec(schema); e != nil {
		return nil, e
	}
	if e := f.load(); e != nil {
		return nil, e
	}
	o.Provider.SetRemote(f)
	return f, nil
}
func validURL(s string) bool {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	return u.Scheme == "http" && (strings.EqualFold(h, "localhost") || ip != nil && ip.IsLoopback())
}
func pubPEM(k ed25519.PublicKey) string {
	b, _ := x509.MarshalPKIXPublicKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: b}))
}
func fingerprint(k string) string {
	pub, e := amp.ParsePublicKey(k)
	if e != nil {
		return ""
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	h := sha256.Sum256(der)
	return "SHA256:" + base64.StdEncoding.EncodeToString(h[:])
}
func (f *Engine) loadKey() error {
	dir := filepath.Join(f.o.StateDir, "federation")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	path := filepath.Join(dir, "provider.key")
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		der, _ := x509.MarshalPKCS8PrivateKey(key)
		b = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(b)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if e != nil {
		return e
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return errors.New("invalid federation private key")
	}
	v, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return e
	}
	key, ok := v.(ed25519.PrivateKey)
	if !ok {
		return errors.New("federation key must be Ed25519")
	}
	f.key = key
	f.pub = pubPEM(key.Public().(ed25519.PublicKey))
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	return atomicWrite(filepath.Join(dir, "provider.pub"), []byte(f.pub))
}
func atomicWrite(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".fed-*")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	if e = file.Chmod(0600); e == nil {
		_, e = file.Write(b)
	}
	if e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
func (f *Engine) load() error {
	b, e := os.ReadFile(filepath.Join(f.o.ConfigDir, "connections.yaml"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var v map[string]any
	if e = yaml.Unmarshal(b, &v); e != nil {
		return e
	}
	j, e := json.Marshal(v)
	if e != nil {
		return e
	}
	var doc document
	if e = json.Unmarshal(j, &doc); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, p := range doc.Peers {
		if p.Provider == f.o.Provider.Name() || seen[p.Provider] || !validProvider(p.Provider) || (p.URL != "" && !validURL(p.URL)) || fingerprint(p.PublicKey) == "" {
			return fmt.Errorf("invalid federation connection configuration")
		}
		seen[p.Provider] = true
	}
	f.doc = doc
	return nil
}

// Reload preserves the last valid configuration when the file is malformed.
func (f *Engine) Reload() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.doc
	if e := f.load(); e != nil {
		f.doc = old
		return e
	}
	f.cache = map[string]cacheEntry{}
	f.signal()
	return nil
}
func (f *Engine) save() error {
	j, e := json.Marshal(f.doc)
	if e != nil {
		return e
	}
	var v map[string]any
	if e = json.Unmarshal(j, &v); e != nil {
		return e
	}
	b, e := yaml.Marshal(v)
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(f.o.ConfigDir, "connections.yaml"), b)
}
func (f *Engine) mutate(fn func() error) error {
	raw, _ := json.Marshal(f.doc)
	if e := fn(); e != nil {
		_ = json.Unmarshal(raw, &f.doc)
		return e
	}
	if e := f.save(); e != nil {
		_ = json.Unmarshal(raw, &f.doc)
		return e
	}
	return nil
}
func (f *Engine) Enabled() bool { return f.o.Enabled }
func (f *Engine) Info(context.Context) core.FederationInfo {
	v := core.FederationInfo{Enabled: f.o.Enabled, Provider: f.o.Provider.Name(), Display: f.o.Display, URL: f.o.URL, Fingerprint: fingerprint(f.pub)}
	if !v.Enabled {
		v.Problem = "Federation is disabled"
	} else if v.URL == "" {
		v.Problem = "Set a reachable federation URL to create invites"
	}
	return v
}
func (f *Engine) OnApproval(fn func(core.Grant)) {
	if fn != nil {
		f.mu.Lock()
		f.approvals = append(f.approvals, fn)
		f.mu.Unlock()
	}
}
func (f *Engine) OnChange(fn func(core.Mail)) {
	if fn != nil {
		f.mu.Lock()
		f.changes = append(f.changes, fn)
		f.mu.Unlock()
	}
}
func (f *Engine) event(detail string) {
	if f.o.EventSink != nil {
		f.o.EventSink(core.Event{At: f.o.Clock().UTC(), Kind: "federation", Detail: detail})
	}
}
func (f *Engine) signal() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}
func (f *Engine) find(provider string) *peer {
	for i := range f.doc.Peers {
		if f.doc.Peers[i].Provider == provider {
			return &f.doc.Peers[i]
		}
	}
	return nil
}
func (f *Engine) grant(peer, remote, local string) *core.Grant {
	for i := range f.doc.Grants {
		g := &f.doc.Grants[i]
		if g.Peer == peer && g.Remote == remote && g.Local == local {
			return g
		}
	}
	return nil
}
func (f *Engine) Connections(ctx context.Context) ([]core.Connection, error) {
	f.mu.Lock()
	var out []core.Connection
	for _, p := range f.doc.Peers {
		c := p.Connection
		c.Fingerprint = fingerprint(p.PublicKey)
		c = cloneConnection(c)
		if p.Removed {
			c.Paused = true
			c.LastError = "removed by them"
		}
		out = append(out, c)
	}
	f.mu.Unlock()
	for i := range out {
		if e := f.o.DB.QueryRowContext(ctx, `SELECT count(*) FROM fed_outbox WHERE peer=? AND kind='deliver'`, out[i].Provider).Scan(&out[i].Queued); e != nil {
			return nil, e
		}
		f.mu.Lock()
		for _, g := range f.doc.Grants {
			if g.Peer == out[i].Provider && g.State == core.GrantPending {
				out[i].Pending++
			}
		}
		f.mu.Unlock()
	}
	return out, nil
}
func (f *Engine) Grants(ctx context.Context) ([]core.Grant, error) {
	f.mu.Lock()
	out := append([]core.Grant{}, f.doc.Grants...)
	f.mu.Unlock()
	for i := range out {
		held, e := f.o.Provider.Held(ctx, out[i].Peer, out[i].Remote, out[i].Local)
		if e != nil {
			return nil, e
		}
		out[i].Held = len(held)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].State == core.GrantPending) != (out[j].State == core.GrantPending) {
			return out[i].State == core.GrantPending
		}
		if out[i].State != core.GrantPending {
			return out[i].DecidedAt.After(out[j].DecidedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
func (f *Engine) UpdateConnection(ctx context.Context, provider string, u core.ConnectionUpdate) (core.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.find(provider)
	if p == nil {
		return core.Connection{}, core.ErrNotFound
	}
	for _, s := range u.Shares {
		found := false
		for _, a := range f.o.Agents() {
			if a.Name == s.Local {
				found = true
			}
		}
		if !found {
			return core.Connection{}, fmt.Errorf("%w: unknown shared agent", core.ErrInvalid)
		}
		for _, r := range s.Remote {
			_, d, e := amp.ParseAddress(r)
			if e != nil || d != provider {
				return core.Connection{}, core.ErrInvalid
			}
		}
	}
	e := f.mutate(func() error {
		if u.Display != nil {
			p.Display = *u.Display
		}
		if u.Paused != nil {
			p.Paused = *u.Paused
		}
		if u.UserReachable != nil {
			p.UserReachable = *u.UserReachable
		}
		if u.Shares != nil {
			p.Shares = cloneShares(u.Shares)
			for _, s := range u.Shares {
				for _, r := range s.Remote {
					g := f.grant(provider, r, s.Local)
					if g == nil {
						f.doc.Grants = append(f.doc.Grants, core.Grant{Peer: provider, Remote: r, Local: s.Local, State: core.GrantAllowed, Origin: "shared", CreatedAt: f.o.Clock().UTC(), DecidedAt: f.o.Clock().UTC()})
					} else if g.State != core.GrantBlocked {
						g.State = core.GrantAllowed
						g.Origin = "shared"
					}
				}
			}
		}
		return nil
	})
	if e != nil {
		return core.Connection{}, e
	}
	for _, g := range f.doc.Grants {
		if g.Peer == provider && g.State != core.GrantPending {
			if e = f.resolveGrant(ctx, g); e != nil {
				return core.Connection{}, e
			}
		}
	}
	f.cache = map[string]cacheEntry{}
	f.signal()
	return cloneConnection(p.Connection), nil
}
func (f *Engine) RemoveConnection(ctx context.Context, provider string) error {
	f.mu.Lock()
	p := f.find(provider)
	if p == nil {
		f.mu.Unlock()
		return core.ErrNotFound
	}
	copy := *p
	f.mu.Unlock()
	// Best effort only; removal must work while the peer is offline.
	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, _, _ = f.request(notifyCtx, copy, "POST", "unpair", map[string]any{})
	cancel()
	f.mu.Lock()
	e := f.mutate(func() error {
		for i := range f.doc.Peers {
			if f.doc.Peers[i].Provider == provider {
				f.doc.Peers = append(f.doc.Peers[:i], f.doc.Peers[i+1:]...)
				break
			}
		}
		var grants []core.Grant
		for _, g := range f.doc.Grants {
			if g.Peer != provider {
				grants = append(grants, g)
			}
		}
		f.doc.Grants = grants
		return nil
	})
	delete(f.cache, provider)
	f.mu.Unlock()
	if e != nil {
		return e
	}
	f.event("Unpaired " + provider)
	return f.failPeer(ctx, provider, "connection removed")
}
func domain(address string) string { _, d, _ := amp.ParseAddress(address); return d }
func hashSecret(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func cloneShares(in []core.Share) []core.Share {
	if in == nil {
		return nil
	}
	out := append([]core.Share{}, in...)
	for i := range out {
		out[i].Remote = append([]string(nil), out[i].Remote...)
	}
	return out
}
func cloneConnection(c core.Connection) core.Connection { c.Shares = cloneShares(c.Shares); return c }
