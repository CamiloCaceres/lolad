package updater

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// PublicKeyB64 is the base64 (std) of the 32-byte Ed25519 public key that
// update manifests must be signed with. Release builds override it:
//
//	-ldflags "-X github.com/CamiloCaceres/lolad/desktop/updater.PublicKeyB64=<base64>"
//
// The default is a TEST key whose private half is public
// (testdata/test-key.pem). A build that still has it trusts anyone, so the
// Updater refuses to use it unless Config.AllowUnsigned (dev) is set.
var PublicKeyB64 = "zjaNjSmS8T5G3hFMTtqydv5wkevgp6veT0CzZBd4zt4="

// testKeyB64 is the compiled-in default, kept to detect an un-overridden build.
const testKeyB64 = "zjaNjSmS8T5G3hFMTtqydv5wkevgp6veT0CzZBd4zt4="

// DefaultManifestURL is where release workflows publish update.json.
const DefaultManifestURL = "https://github.com/CamiloCaceres/lolad/releases/latest/download/update.json"

// Artifact is one downloadable build for one platform.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is update.json.
type Manifest struct {
	Version            string              `json:"version"`
	NotesURL           string              `json:"notes_url"`
	MinVersion         string              `json:"min_version,omitempty"`
	MultiplexerVersion string              `json:"multiplexer_version"`
	Platforms          map[string]Artifact `json:"platforms"`
	Signature          string              `json:"signature,omitempty"`
}

var (
	ErrBadSignature = errors.New("update manifest signature is missing or does not match")
	ErrDowngrade    = errors.New("update manifest is older than the installed version")
)

// Canonical returns the bytes that are signed: the JSON document with the
// "signature" member removed, keys sorted at every level, no insignificant
// whitespace, no HTML escaping, numbers kept verbatim. It works on the raw
// document so fields this version does not know about are still covered.
func Canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("manifest is not a JSON object: %w", err)
	}
	if dec.More() {
		return nil, errors.New("manifest has trailing data")
	}
	delete(doc, "signature")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil { // map keys are sorted by encoding/json
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SignManifest returns raw with "signature" set (re-encoded, indented).
func SignManifest(raw []byte, key ed25519.PrivateKey) ([]byte, error) {
	canon, err := Canonical(raw)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	doc["signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, canon))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ParseAndVerify checks the signature over raw with pub, then decodes it.
// Nothing in the manifest is trusted before the signature checks out.
func ParseAndVerify(raw []byte, pub ed25519.PublicKey) (*Manifest, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("update public key is not configured")
	}
	var sigHolder struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &sigHolder); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigHolder.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrBadSignature
	}
	canon, err := Canonical(raw)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, canon, sig) {
		return nil, ErrBadSignature
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if _, _, ok := parseVersion(m.Version); !ok {
		return nil, fmt.Errorf("manifest version %q is not a version", m.Version)
	}
	return &m, nil
}

// ValidateURL accepts https, and http only for 127.0.0.1 (tests).
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad url %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if u.Hostname() == "127.0.0.1" {
			return nil
		}
	}
	return fmt.Errorf("url %q must be https", raw)
}

func publicKeyFromB64(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("compiled-in update public key is invalid")
	}
	return ed25519.PublicKey(b), nil
}

// MarshalPrivateKeyPEM / ParsePrivateKeyPEM / MarshalPublicKeyPEM use PKCS#8
// and PKIX PEM, readable by openssl.
func MarshalPrivateKeyPEM(k ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func ParsePrivateKeyPEM(b []byte) (ed25519.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("no PEM block in key file")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("key is not Ed25519")
	}
	return ek, nil
}

func MarshalPublicKeyPEM(k ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}
