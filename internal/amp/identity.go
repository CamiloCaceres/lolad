package amp

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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var tenantRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,126}$`)

func ProviderName(tenant string) string { return strings.ToLower(tenant) + ".lolad.local" }

// ParseAddress accepts a bare local name or a complete AMP address.
func ParseAddress(address string) (name, domain string, err error) {
	s := strings.ToLower(strings.TrimSpace(address))
	parts := strings.Split(s, "@")
	if len(parts) > 2 || !nameRE.MatchString(parts[0]) {
		return "", "", fmt.Errorf("%w: invalid address", core.ErrInvalid)
	}
	name = parts[0]
	if len(parts) == 2 {
		domain = parts[1]
		if domain == "" || !tenantRE.MatchString(domain) {
			return "", "", fmt.Errorf("%w: invalid domain", core.ErrInvalid)
		}
	}
	return
}
func ExpandAddress(address, tenant string) (string, error) {
	n, d, e := ParseAddress(address)
	if e != nil {
		return "", e
	}
	if d == "" {
		d = ProviderName(tenant)
	}
	return n + "@" + d, nil
}
func (p *Provider) local(address string) (string, error) {
	n, d, e := ParseAddress(address)
	if e != nil {
		return "", e
	}
	if d != "" && d != p.Name() {
		return "", fmt.Errorf("%w: foreign provider", core.ErrNotFound)
	}
	return n, nil
}

type Credentials struct {
	core.Identity
	APIKey   string `json:"api_key"`
	Provider string `json:"provider"`
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func keyHash(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func fingerprint(pub ed25519.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	h := sha256.Sum256(der)
	return "SHA256:" + base64.StdEncoding.EncodeToString(h[:])
}
func publicPEM(pub ed25519.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, _ := pem.Decode([]byte(s))
	if b == nil {
		return nil, fmt.Errorf("invalid PEM public key")
	}
	v, e := x509.ParsePKIXPublicKey(b.Bytes)
	if e != nil {
		return nil, e
	}
	pub, ok := v.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key must be Ed25519")
	}
	return pub, nil
}
func ReadCredentials(stateDir, name string) (Credentials, ed25519.PrivateKey, error) {
	var c Credentials
	if !nameRE.MatchString(name) {
		return c, nil, core.ErrInvalid
	}
	dir := filepath.Join(stateDir, "identities", name)
	b, e := os.ReadFile(filepath.Join(dir, "identity.json"))
	if e != nil {
		return c, nil, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, nil, e
	}
	b, e = os.ReadFile(filepath.Join(dir, "private.pem"))
	if e != nil {
		return c, nil, e
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return c, nil, fmt.Errorf("invalid private PEM")
	}
	v, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return c, nil, e
	}
	k, ok := v.(ed25519.PrivateKey)
	if !ok {
		return c, nil, fmt.Errorf("private key must be Ed25519")
	}
	return c, k, nil
}
func (p *Provider) identity(ctx context.Context, name string) (core.Identity, ed25519.PublicKey, error) {
	var i core.Identity
	var pub []byte
	var at string
	e := p.db.QueryRowContext(ctx, `SELECT name,address,kind,fingerprint,created_at,public_key FROM amp_identities WHERE name=?`, name).Scan(&i.Name, &i.Address, &i.Kind, &i.Fingerprint, &at, &pub)
	if errors.Is(e, sql.ErrNoRows) {
		e = core.ErrNotFound
	}
	if e == nil {
		i.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
	}
	return i, ed25519.PublicKey(pub), e
}
func (p *Provider) EnsureIdentity(ctx context.Context, name, kind string) (core.Identity, error) {
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	n, e := p.local(name)
	if e != nil {
		return core.Identity{}, e
	}
	i, _, e := p.identity(ctx, n)
	if e == nil {
		return i, nil
	}
	if !errors.Is(e, core.ErrNotFound) {
		return i, e
	}
	if kind != "agent" && kind != "human" && kind != "system" {
		return i, core.ErrInvalid
	}
	// Existing files recover the same identity after a database rebuild.
	c, key, e := ReadCredentials(p.stateDir, n)
	if e == nil {
		if c.Address != n+"@"+p.Name() || c.Kind != kind || c.APIKey == "" {
			return i, fmt.Errorf("identity files disagree with provider")
		}
		pub := key.Public().(ed25519.PublicKey)
		if c.Fingerprint != fingerprint(pub) {
			return i, fmt.Errorf("identity fingerprint mismatch")
		}
		return c.Identity, p.insertIdentity(ctx, c.Identity, pub, c.APIKey)
	}
	if !os.IsNotExist(e) {
		return i, e
	}
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return i, e
	}
	token, e := randomHex(32)
	if e != nil {
		return i, e
	}
	i = core.Identity{Name: n, Address: n + "@" + p.Name(), Kind: kind, Fingerprint: fingerprint(pub), CreatedAt: time.Now().UTC()}
	c = Credentials{Identity: i, APIKey: "amp_live_sk_" + token, Provider: p.Name()}
	dir := filepath.Join(p.stateDir, "identities", n)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return i, e
	}
	der, e := x509.MarshalPKCS8PrivateKey(priv)
	if e != nil {
		return i, e
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	for _, f := range []struct {
		name string
		data []byte
	}{{"private.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}, {"public.pem", []byte(publicPEM(pub))}, {"identity.json", data}} {
		if e = writeSecret(filepath.Join(dir, f.name), f.data); e != nil {
			return i, e
		}
	}
	return i, p.insertIdentity(ctx, i, pub, c.APIKey)
}
func writeSecret(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func (p *Provider) insertIdentity(ctx context.Context, i core.Identity, pub ed25519.PublicKey, key string) error {
	_, e := p.db.ExecContext(ctx, `INSERT INTO amp_identities(name,address,kind,fingerprint,created_at,public_key,api_key_hash) VALUES(?,?,?,?,?,?,?)`, i.Name, i.Address, i.Kind, i.Fingerprint, i.CreatedAt.Format(time.RFC3339Nano), []byte(pub), keyHash(key))
	return e
}
func (p *Provider) Identities(ctx context.Context) ([]core.Identity, error) {
	rows, e := p.db.QueryContext(ctx, `SELECT name,address,kind,fingerprint,created_at FROM amp_identities ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.Identity{}
	for rows.Next() {
		var i core.Identity
		var at string
		if e = rows.Scan(&i.Name, &i.Address, &i.Kind, &i.Fingerprint, &at); e != nil {
			return nil, e
		}
		i.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, i)
	}
	return out, rows.Err()
}
