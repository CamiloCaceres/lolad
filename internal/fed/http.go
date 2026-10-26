package fed

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
)

type pairBody struct {
	Version   int    `json:"version"`
	Provider  string `json:"provider"`
	Display   string `json:"display"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"`
	Secret    string `json:"secret,omitempty"`
}
type inviteCode struct {
	V         int    `json:"v"`
	Provider  string `json:"provider"`
	Display   string `json:"display"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"`
	Secret    string `json:"secret"`
}
type deliverBody struct {
	Envelope        amp.Envelope    `json:"envelope"`
	Payload         json.RawMessage `json:"payload"`
	SenderPublicKey string          `json:"sender_public_key"`
}
type receiptBody struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

func (f *Engine) CreateInvite(ctx context.Context) (core.Invite, error) {
	if !f.Enabled() || f.o.URL == "" {
		return core.Invite{}, fmt.Errorf("%w: %s", core.ErrInvalid, f.Info(ctx).Problem)
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return core.Invite{}, e
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	expires := f.o.Clock().UTC().Add(72 * time.Hour)
	if _, e := f.o.DB.ExecContext(ctx, `INSERT INTO fed_invites(secret_hash,expires) VALUES(?,?)`, hashSecret(secret), expires.Unix()); e != nil {
		return core.Invite{}, e
	}
	raw, _ := json.Marshal(inviteCode{V: 1, Provider: f.o.Provider.Name(), Display: f.o.Display, URL: f.o.URL, PublicKey: f.pub, Secret: secret})
	return core.Invite{Code: "lolad-invite:" + base64.RawURLEncoding.EncodeToString(raw), ExpiresAt: expires}, nil
}
func (f *Engine) AcceptInvite(ctx context.Context, code string) (core.Connection, error) {
	if !f.Enabled() {
		return core.Connection{}, fmt.Errorf("%w: federation disabled", core.ErrDenied)
	}
	var c inviteCode
	const prefix = "lolad-invite:"
	if !strings.HasPrefix(strings.TrimSpace(code), prefix) || len(code) > 16384 {
		return core.Connection{}, core.ErrInvalid
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(code), prefix))
	if e != nil || json.Unmarshal(raw, &c) != nil || c.V != 1 || c.Secret == "" || !validURL(c.URL) || fingerprint(c.PublicKey) == "" {
		return core.Connection{}, fmt.Errorf("%w: invalid invite", core.ErrInvalid)
	}
	if c.Provider == f.o.Provider.Name() {
		return core.Connection{}, fmt.Errorf("%w: rename the tenant on one side", core.ErrInvalid)
	}
	if !validProvider(c.Provider) {
		return core.Connection{}, core.ErrInvalid
	}
	f.mu.Lock()
	existing := f.find(c.Provider)
	if existing != nil && existing.PublicKey != c.PublicKey {
		f.mu.Unlock()
		return core.Connection{}, fmt.Errorf("%w: provider key mismatch", core.ErrDenied)
	}
	f.mu.Unlock()
	target := peer{Connection: core.Connection{Provider: c.Provider, Display: c.Display, URL: c.URL}, PublicKey: c.PublicKey}
	status, b, e := f.request(ctx, target, "POST", "pair", pairBody{Version: 1, Provider: f.o.Provider.Name(), Display: f.o.Display, URL: f.o.URL, PublicKey: f.pub, Secret: c.Secret})
	if e != nil {
		return core.Connection{}, fmt.Errorf("pairing peer unreachable")
	}
	if status != 200 {
		return core.Connection{}, fmt.Errorf("pairing rejected (HTTP %d)", status)
	}
	var result pairBody
	if json.Unmarshal(b, &result) != nil || result.Provider != c.Provider || result.PublicKey != c.PublicKey || !validURL(result.URL) {
		return core.Connection{}, fmt.Errorf("%w: pairing response did not match invite", core.ErrDenied)
	}
	target.URL = result.URL
	target.Display = result.Display
	target.CreatedAt = f.o.Clock().UTC()
	target.Fingerprint = fingerprint(target.PublicKey)
	f.mu.Lock()
	e = f.mutate(func() error {
		if p := f.find(c.Provider); p != nil {
			if p.PublicKey != target.PublicKey {
				return core.ErrDenied
			}
			target = *p
		} else {
			f.doc.Peers = append(f.doc.Peers, target)
		}
		return nil
	})
	f.mu.Unlock()
	if e != nil {
		return core.Connection{}, e
	}
	f.event("Paired " + target.Provider)
	return target.Connection, nil
}
func validProvider(s string) bool {
	_, d, e := amp.ParseAddress("x@" + s)
	return e == nil && d == s && strings.HasSuffix(s, ".lolad.local")
}
func wireError(status int, b []byte) error {
	var v struct{ Error, Message string }
	_ = json.Unmarshal(b, &v)
	if v.Message == "" {
		v.Message = "federation request rejected"
	} // Only known peer error text, never request bodies or credentials.
	if len(v.Message) > 256 {
		v.Message = "federation request rejected"
	}
	return fmt.Errorf("federation HTTP %d: %s", status, v.Message)
}
func (f *Engine) request(ctx context.Context, p peer, method, path string, body any) (int, []byte, error) {
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
	}
	if !validURL(p.URL) {
		return 0, nil, errors.New("peer has no reachable URL")
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.URL, "/")+"/v1/federation/"+path, bytes.NewReader(raw))
	if e != nil {
		return 0, nil, e
	}
	timestamp := strconv.FormatInt(f.o.Clock().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AMP-Provider", f.o.Provider.Name())
	req.Header.Set("X-AMP-Timestamp", timestamp)
	req.Header.Set("X-AMP-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, append([]byte(timestamp+"."), raw...))))
	res, e := f.o.HTTPClient.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil {
		return 0, nil, e
	}
	if len(b) > 1<<20 {
		return 0, nil, errors.New("peer response too large")
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		f.mu.Lock()
		if current := f.find(p.Provider); current != nil {
			current.LastSeen = f.o.Clock().UTC()
			current.LastError = ""
		}
		f.mu.Unlock()
	}
	return res.StatusCode, b, nil
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func reject(w http.ResponseWriter, status int, code, msg string) {
	if status == 429 {
		w.Header().Set("Retry-After", "60")
	}
	jsonResponse(w, status, map[string]string{"error": code, "message": msg})
}
func (f *Engine) verify(r *http.Request, body []byte, pub string) bool {
	ts := r.Header.Get("X-AMP-Timestamp")
	n, e := strconv.ParseInt(ts, 10, 64)
	if e != nil {
		return false
	}
	delta := f.o.Clock().Unix() - n
	if delta > 300 || delta < -300 {
		return false
	}
	key, e := amp.ParsePublicKey(pub)
	if e != nil {
		return false
	}
	sig, e := base64.StdEncoding.DecodeString(r.Header.Get("X-AMP-Signature"))
	return e == nil && ed25519.Verify(key, append([]byte(ts+"."), body...), sig)
}
func (f *Engine) Handler() http.Handler { return http.HandlerFunc(f.serve) }
func (f *Engine) serve(w http.ResponseWriter, r *http.Request) {
	if !f.Enabled() {
		reject(w, 503, "federation_disabled", "Federation is disabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/federation/")
	if path == r.URL.Path || (path == "directory" && r.Method != "GET") || (path != "directory" && r.Method != "POST") {
		reject(w, 404, "not_found", "Unknown endpoint")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		reject(w, 413, "request_too_large", "Request exceeds 1 MB")
		return
	}
	if path == "pair" {
		f.pair(w, r, raw)
		return
	}
	provider := r.Header.Get("X-AMP-Provider")
	f.mu.Lock()
	p := f.find(provider)
	if p == nil || p.Removed {
		f.mu.Unlock()
		reject(w, 403, "provider_not_trusted", "Provider is not connected")
		return
	}
	copy := *p
	f.mu.Unlock()
	if !f.verify(r, raw, copy.PublicKey) {
		reject(w, 403, "invalid_signature", "Invalid provider signature or timestamp")
		return
	}
	login := r.Header.Get("Tailscale-User-Login")
	if copy.Login != "" && login != "" && copy.Login != login {
		reject(w, 403, "provider_not_trusted", "Tailscale login does not match pinned peer")
		return
	}
	if copy.Paused && path != "unpair" {
		reject(w, 503, "provider_paused", "Connection is paused")
		return
	}
	f.mu.Lock()
	if p = f.find(provider); p != nil {
		p.LastSeen = f.o.Clock().UTC()
	}
	f.mu.Unlock()
	switch path {
	case "deliver":
		f.deliver(w, r, copy, raw)
	case "receipt":
		f.receipt(w, r, copy, raw)
	case "directory":
		jsonResponse(w, 200, f.directory(copy))
	case "unpair":
		f.mu.Lock()
		e = f.mutate(func() error {
			p = f.find(provider)
			if p != nil {
				p.Removed = true
				p.Paused = true
				p.LastError = "removed by them"
			}
			return nil
		})
		f.mu.Unlock()
		if e != nil {
			reject(w, 500, "internal_error", "Cannot save connection")
			return
		}
		_ = f.failPeer(r.Context(), provider, "removed by them")
		f.event("Removed by " + provider)
		jsonResponse(w, 200, map[string]bool{"ok": true})
	default:
		reject(w, 404, "not_found", "Unknown endpoint")
	}
}
func (f *Engine) rate(key string, limit int) bool {
	now := f.o.Clock()
	v := f.rates[key]
	if now.Sub(v.At) >= time.Minute || v.At.IsZero() {
		v = rateWindow{At: now}
	}
	if v.Count >= limit {
		return false
	}
	v.Count++
	f.rates[key] = v
	return true
}
func (f *Engine) pair(w http.ResponseWriter, r *http.Request, raw []byte) {
	var b pairBody
	if json.Unmarshal(raw, &b) != nil {
		reject(w, 400, "invalid_request", "Invalid pairing request")
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	f.mu.Lock()
	v := f.rates["pair:"+host]
	if f.o.Clock().Sub(v.At) >= time.Hour {
		v = rateWindow{At: f.o.Clock()}
	}
	limited := v.Count >= 10
	f.mu.Unlock()
	if limited {
		reject(w, 429, "rate_limited", "Too many failed pairing attempts")
		return
	}
	bad := func() {
		f.mu.Lock()
		v = f.rates["pair:"+host]
		if v.At.IsZero() || f.o.Clock().Sub(v.At) >= time.Hour {
			v = rateWindow{At: f.o.Clock()}
		}
		v.Count++
		f.rates["pair:"+host] = v
		f.mu.Unlock()
		reject(w, 403, "invalid_invite", "Invite is invalid, expired, or already used")
	}
	if b.Provider == f.o.Provider.Name() {
		reject(w, 400, "invalid_request", "Rename the tenant on one side")
		return
	}
	if b.Version != 1 || !validProvider(b.Provider) || (b.URL != "" && !validURL(b.URL)) || r.Header.Get("X-AMP-Provider") != b.Provider || !f.verify(r, raw, b.PublicKey) {
		bad()
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old := f.find(b.Provider)
	if old != nil {
		login := r.Header.Get("Tailscale-User-Login")
		if old.PublicKey != b.PublicKey || old.Removed || old.Login != "" && login != "" && old.Login != login {
			go f.event("Provider key or login mismatch for " + b.Provider)
			reject(w, 403, "provider_not_trusted", "Provider key or login mismatch")
			return
		}
		// Retries are idempotent only for the same consumed invite. The secret is
		// still required, so a copied public key cannot revive an unrelated pairing.
		var hash string
		var used sql.NullInt64
		var usedBy sql.NullString
		var expires int64
		e := f.o.DB.QueryRowContext(r.Context(), `SELECT secret_hash,expires,used_at,used_by FROM fed_invites WHERE secret_hash=?`, hashSecret(b.Secret)).Scan(&hash, &expires, &used, &usedBy)
		if e != nil || used.Valid && usedBy.String != b.Provider || !used.Valid && expires <= f.o.Clock().Unix() {
			v = f.rates["pair:"+host]
			if v.At.IsZero() || f.o.Clock().Sub(v.At) >= time.Hour {
				v = rateWindow{At: f.o.Clock()}
			}
			v.Count++
			f.rates["pair:"+host] = v
			reject(w, 403, "invalid_invite", "Invite is invalid, expired, or already used")
			return
		}

		if !used.Valid {
			if _, e = f.o.DB.ExecContext(r.Context(), `UPDATE fed_invites SET used_at=?,used_by=? WHERE secret_hash=? AND used_at IS NULL`, f.o.Clock().Unix(), b.Provider, hash); e != nil {
				reject(w, 500, "internal_error", "Cannot consume invite")
				return
			}
		}
		jsonResponse(w, 200, pairBody{Version: 1, Provider: f.o.Provider.Name(), Display: f.o.Display, URL: f.o.URL, PublicKey: f.pub})
		return
	}
	var hash string
	var expires int64
	var used sql.NullInt64
	e := f.o.DB.QueryRowContext(r.Context(), `SELECT secret_hash,expires,used_at FROM fed_invites WHERE secret_hash=?`, hashSecret(b.Secret)).Scan(&hash, &expires, &used)
	candidate := hashSecret(b.Secret)
	if e != nil || used.Valid || expires <= f.o.Clock().Unix() || subtle.ConstantTimeCompare([]byte(hash), []byte(candidate)) != 1 { // Cannot call bad while holding mu.
		v.Count++
		if v.At.IsZero() {
			v.At = f.o.Clock()
		}
		f.rates["pair:"+host] = v
		reject(w, 403, "invalid_invite", "Invite is invalid, expired, or already used")
		return
	}
	p := peer{Connection: core.Connection{Provider: b.Provider, Display: b.Display, URL: b.URL, Login: r.Header.Get("Tailscale-User-Login"), CreatedAt: f.o.Clock().UTC(), LastSeen: f.o.Clock().UTC(), Fingerprint: fingerprint(b.PublicKey)}, PublicKey: b.PublicKey}

	// Keep consumption uncommitted until YAML is durable. If the process stops
	// between save and commit, the existing-peer branch safely consumes on retry.
	tx, e := f.o.DB.BeginTx(r.Context(), nil)
	if e != nil {
		reject(w, 500, "internal_error", "Cannot consume invite")
		return
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(r.Context(), `UPDATE fed_invites SET used_at=?,used_by=? WHERE secret_hash=? AND used_at IS NULL`, f.o.Clock().Unix(), b.Provider, hash)
	if e != nil {
		reject(w, 500, "internal_error", "Cannot consume invite")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		reject(w, 403, "invalid_invite", "Invite already used")
		return
	}
	previous := f.doc
	e = f.mutate(func() error { f.doc.Peers = append(f.doc.Peers, p); return nil })
	if e != nil {
		reject(w, 500, "internal_error", "Cannot save connection")
		return
	}
	if e = tx.Commit(); e != nil {
		f.doc = previous
		_ = f.save()
		reject(w, 500, "internal_error", "Cannot finish pairing")
		return
	}
	go f.event("Paired " + p.Provider)
	jsonResponse(w, 200, pairBody{Version: 1, Provider: f.o.Provider.Name(), Display: f.o.Display, URL: f.o.URL, PublicKey: f.pub})
}
