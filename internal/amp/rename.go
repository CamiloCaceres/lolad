package amp

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Renaming a lolad (tenant) changes the domain of every local address. These
// helpers rewrite what stores our own full addresses; everything keyed by
// bare names (from_name, to_name, owners) is untouched. The wire column is
// the signed record and is never changed. Both are idempotent: run again,
// they find nothing left under the old domain.

// RenameAddresses rewrites amp_identities.address and the from/to of stored
// mail from @<from>.lolad.local to @<to>.lolad.local, inside tx.
func RenameAddresses(ctx context.Context, tx *sql.Tx, from, to string) error {
	oldD, newD := "@"+ProviderName(from), "@"+ProviderName(to)
	if !tenantRE.MatchString(from) || !tenantRE.MatchString(to) || from == to {
		return fmt.Errorf("rename %q to %q: invalid tenant", from, to)
	}
	if _, e := tx.ExecContext(ctx, `UPDATE amp_identities SET address=name||? WHERE address=name||?`, newD, oldD); e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,json_extract(mail,'$.from'),json_extract(mail,'$.to') FROM amp_messages WHERE json_extract(mail,'$.from') LIKE ? OR json_extract(mail,'$.to') LIKE ?`, "%"+oldD, "%"+oldD)
	if e != nil {
		return e
	}
	type change struct{ id, from, to string }
	var list []change
	for rows.Next() {
		var c change
		var f, t sql.NullString
		if e = rows.Scan(&c.id, &f, &t); e != nil {
			rows.Close()
			return e
		}
		c.from, c.to = swapDomain(f.String, oldD, newD), swapDomain(t.String, oldD, newD)
		list = append(list, c)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, c := range list {
		if _, e = tx.ExecContext(ctx, `UPDATE amp_messages SET mail=json_set(mail,'$.from',?,'$.to',?) WHERE id=?`, c.from, c.to, c.id); e != nil {
			return e
		}
	}
	return nil
}

// swapDomain replaces the domain of addr when it is exactly oldD.
func swapDomain(addr, oldD, newD string) string {
	if n, ok := strings.CutSuffix(addr, oldD); ok && n != "" && !strings.Contains(n, "@") {
		return n + newD
	}
	return addr
}

// RenameIdentityFiles rewrites Address and Provider in each
// <stateDir>/identities/<name>/identity.json still under the old tenant. Keys
// and API keys stay as they are.
func RenameIdentityFiles(stateDir, from, to string) error {
	oldP, newP := ProviderName(from), ProviderName(to)
	dirs, e := os.ReadDir(filepath.Join(stateDir, "identities"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, d := range dirs {
		if !d.IsDir() || !nameRE.MatchString(d.Name()) {
			continue
		}
		path := filepath.Join(stateDir, "identities", d.Name(), "identity.json")
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		var v map[string]json.RawMessage
		if e = json.Unmarshal(b, &v); e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		var c Credentials
		if e = json.Unmarshal(b, &c); e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		if c.Provider != oldP && c.Address != d.Name()+"@"+oldP {
			continue
		}
		v["address"], _ = json.Marshal(d.Name() + "@" + newP)
		v["provider"], _ = json.Marshal(newP)
		out, _ := json.MarshalIndent(v, "", "  ")
		if e = writeSecret(path, out); e != nil {
			return e
		}
	}
	return nil
}

// WithFormerNames gives the names (tenants) this lolad had before its
// renames. See formerSignature.
func WithFormerNames(names []string) Option {
	return func(p *Provider) { p.former = append([]string(nil), names...) }
}

// formerSignature covers clients that outlive a rename. A process started
// before it (a long-running `lolad msg mcp` in every agent session) still
// signs From with its old address, while the provider checks the signature
// against the current one. When the signature verifies under one of the
// former names with the sender's own key, the caller (already authenticated
// as this identity) holds the key and meant exactly this mail: the provider
// re-signs the envelope under the current address, so the stored and
// forwarded wire verifies like any other. It returns the new signature, or
// false.
func (p *Provider) formerSignature(from string, env Envelope, pub ed25519.PublicKey, payload json.RawMessage) (string, bool) {
	name, _, e := ParseAddress(env.From)
	if e != nil || len(p.former) == 0 {
		return "", false
	}
	for _, t := range p.former {
		if t == p.tenant || !tenantRE.MatchString(t) {
			continue
		}
		old := env
		old.From = name + "@" + ProviderName(t)
		if !Verify(pub, old, payload) {
			continue
		}
		_, key, e := ReadCredentials(p.stateDir, from)
		if e != nil || !key.Public().(ed25519.PublicKey).Equal(pub) {
			return "", false
		}
		sig, e := Sign(key, env, payload)
		return sig, e == nil
	}
	return "", false
}
