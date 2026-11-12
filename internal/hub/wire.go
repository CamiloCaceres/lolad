// Package hub is the lolad hub (SPEC.md "Hub (v5)"): a small relay that gives
// a lolad behind NAT a public federation URL, <hub>/p/<handle>. Each lolad
// keeps a WebSocket tunnel open to the hub; peers POST signed federation
// requests to the public URL and the hub passes them down the tunnel.
//
// The hub is a pipe, not a provider: it stores no mail and verifies no
// federation signature (the receiving lolad does). Server is the hub itself
// (cmd/lolad-hub); Client is the lolad side of the tunnel; Register and Leave
// manage membership.
package hub

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Protocol constants shared by both ends.
const (
	MaxBody        = 1 << 20 // same as federation
	ForwardTimeout = 12 * time.Second
	MaxInFlight    = 32
	PingEvery      = 30 * time.Second
	SilenceLimit   = 90 * time.Second
	skew           = 300 // seconds of allowed clock difference on signed requests

	// WebSocket close codes.
	CloseUnauthorized = 4401
	CloseDisabled     = 4403
	CloseReplaced     = 4409
)

// frame is one WebSocket text message. Body is []byte, so encoding/json
// carries it as base64.
type frame struct {
	Type     string            `json:"type"` // challenge auth ready req res watch online
	ID       string            `json:"id,omitempty"`
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     []byte            `json:"body,omitempty"`
	ClientIP string            `json:"client_ip,omitempty"`
	Status   int               `json:"status,omitempty"`
	Nonce    string            `json:"nonce,omitempty"`
	Handle   string            `json:"handle,omitempty"`
	Sig      []byte            `json:"sig,omitempty"`
	URL      string            `json:"url,omitempty"`
	Handles  []string          `json:"handles,omitempty"`
}

// Headers forwarded through the tunnel; everything else (notably
// Tailscale-* and X-Forwarded-*) is stripped in both directions.
var requestHeaders = []string{"Content-Type", "X-AMP-Provider", "X-AMP-Timestamp", "X-AMP-Signature"}
var responseHeaders = []string{"Content-Type", "Retry-After"}

func pick(h http.Header, names []string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		if v := h.Get(n); v != "" {
			out[n] = v
		}
	}
	return out
}

var handleRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$`)
var reserved = map[string]bool{"admin": true, "api": true, "hub": true, "www": true, "p": true, "v1": true}

// ValidHandle reports whether h may be registered: [a-z0-9-]{3,32}, no
// leading or trailing dash, not reserved.
func ValidHandle(h string) bool { return handleRE.MatchString(h) && !reserved[h] }

// tunnelMessage is what the lolad signs to open a tunnel. It binds the
// signature to this hub and this connection's nonce.
func tunnelMessage(publicURL, nonce string) []byte {
	return []byte("lolad-hub-tunnel." + publicURL + "." + nonce)
}

// parsePublicKey accepts a PEM PKIX Ed25519 key (fed's provider.pub format).
func parsePublicKey(s string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("public key is not PEM")
	}
	v, e := x509.ParsePKIXPublicKey(block.Bytes)
	if e != nil {
		return nil, e
	}
	k, ok := v.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not Ed25519")
	}
	return k, nil
}

// signRequest sets the federation-style signature headers on req.
func signRequest(req *http.Request, handle string, body []byte, now time.Time, sign func([]byte) []byte) {
	ts := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("X-AMP-Provider", handle)
	req.Header.Set("X-AMP-Timestamp", ts)
	req.Header.Set("X-AMP-Signature", base64.StdEncoding.EncodeToString(sign(append([]byte(ts+"."), body...))))
}

// verifyRequest checks the signature headers against key.
func verifyRequest(r *http.Request, body []byte, key ed25519.PublicKey, now time.Time) bool {
	ts := r.Header.Get("X-AMP-Timestamp")
	n, e := strconv.ParseInt(ts, 10, 64)
	if e != nil {
		return false
	}
	if d := now.Unix() - n; d > skew || d < -skew {
		return false
	}
	sig, e := base64.StdEncoding.DecodeString(r.Header.Get("X-AMP-Signature"))
	return e == nil && ed25519.Verify(key, append([]byte(ts+"."), body...), sig)
}
