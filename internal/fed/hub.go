package fed

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Hooks for the hub tunnel (SPEC.md "Hub (v5)"). The hub client signs with the
// provider key, serves Handler through the tunnel, and wakes the outbox when a
// peer behind the same hub comes online.

func (f *Engine) selfURL() string {
	f.urlMu.RLock()
	defer f.urlMu.RUnlock()
	return f.o.URL
}

// SetURL changes the URL this lolad advertises to peers (invites and pairing
// responses). Already-paired peers keep the URL they have until they pair again.
func (f *Engine) SetURL(u string) error {
	u = strings.TrimRight(u, "/")
	if u != "" && !validURL(u) {
		return fmt.Errorf("%w: federation URL requires HTTPS (HTTP is loopback-only)", core.ErrInvalid)
	}
	f.urlMu.Lock()
	f.o.URL = u
	f.urlMu.Unlock()
	return nil
}

// ProviderSign signs msg with the provider key. The key itself never leaves fed.
func (f *Engine) ProviderSign(msg []byte) []byte { return ed25519.Sign(f.key, msg) }

// ProviderPublicKey is the provider public key as PEM.
func (f *Engine) ProviderPublicKey() string { return f.pub }

// PeerURLs lists the URLs of connected (not removed) peers.
func (f *Engine) PeerURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.doc.Peers {
		if !p.Removed && p.URL != "" {
			out = append(out, p.URL)
		}
	}
	return out
}

// RetryURL makes queued outbox entries for peers at url due now. Used when the
// hub reports that such a peer reconnected, so mail does not wait for backoff.
func (f *Engine) RetryURL(ctx context.Context, url string) error {
	url = strings.TrimRight(url, "/")
	f.mu.Lock()
	var providers []any
	for _, p := range f.doc.Peers {
		if !p.Removed && strings.TrimRight(p.URL, "/") == url {
			providers = append(providers, p.Provider)
		}
	}
	f.mu.Unlock()
	if len(providers) == 0 {
		return nil
	}
	q := `UPDATE fed_outbox SET next_at=? WHERE peer IN (?` + strings.Repeat(",?", len(providers)-1) + `)`
	if _, e := f.o.DB.ExecContext(ctx, q, append([]any{f.o.Clock().UnixMilli()}, providers...)...); e != nil {
		return e
	}
	f.signal()
	return nil
}
