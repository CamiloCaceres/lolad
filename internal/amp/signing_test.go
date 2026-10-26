package amp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolVectors(t *testing.T) {
	path := "/tmp/amp-spec/protocol/tests/test-vectors.json"
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		t.Skip("protocol clone not present")
	}
	if e != nil {
		t.Fatal(e)
	}
	var vectors struct {
		Payload struct {
			Cases []struct {
				ID    string          `json:"id"`
				Input json.RawMessage `json:"input_payload"`
				JSON  string          `json:"compact_sorted_json"`
				Hash  string          `json:"expected_sha256_base64"`
			} `json:"test_cases"`
		} `json:"payload_hash"`
		Canonical struct {
			Cases []struct {
				ID       string            `json:"id"`
				Input    map[string]string `json:"input"`
				Expected string            `json:"expected_canonical"`
			} `json:"test_cases"`
		} `json:"canonical_string"`
	}
	if e = json.Unmarshal(b, &vectors); e != nil {
		t.Fatal(e)
	}
	for _, v := range vectors.Payload.Cases {
		t.Run(v.ID, func(t *testing.T) {
			b, e := CanonicalJSON(v.Input)
			if e != nil || string(b) != v.JSON {
				t.Fatalf("canonical %s %v", b, e)
			}
			h, e := PayloadHash(v.Input)
			if e != nil || h != v.Hash {
				t.Fatalf("hash %s %v", h, e)
			}
		})
	}
	for _, v := range vectors.Canonical.Cases {
		t.Run(v.ID, func(t *testing.T) {
			i := v.Input
			if got := CanonicalString(i["from"], i["to"], i["subject"], i["priority"], i["in_reply_to"], i["payload_hash"]); got != v.Expected {
				t.Fatalf("canonical %s", got)
			}
		})
	}
}
func TestSignRoundTrip(t *testing.T) {
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	payload := json.RawMessage(`{"type":"request","message":"<hello> & goodbye","context":{"z":[{"b":2,"a":9007199254740993}],"a":true}}`)
	env := Envelope{From: "a@home.lolad.local", To: "b@home.lolad.local", Subject: "Hello", Priority: "normal"}
	env.Signature, e = Sign(priv, env, payload)
	if e != nil || !Verify(pub, env, payload) {
		t.Fatal("signature round trip", e)
	}
	if Verify(pub, env, json.RawMessage(`{"message":"tampered","type":"request"}`)) {
		t.Fatal("tampered payload verified")
	}
	env.Subject = "Changed"
	if Verify(pub, env, payload) {
		t.Fatal("tampered subject verified")
	}
	env.Signature = "bad"
	if Verify(pub, env, payload) {
		t.Fatal("bad base64 verified")
	}
	if _, e = PayloadHash(json.RawMessage(`{} {}`)); e == nil {
		t.Fatal("multiple values accepted")
	}
}
func TestJQOpenSSLCompatibility(t *testing.T) {
	if _, e := exec.LookPath("jq"); e != nil {
		t.Skip("jq unavailable")
	}
	openssl := "openssl" // macOS system LibreSSL may not support Ed25519; use installed OpenSSL 3.
	for _, candidate := range []string{"/opt/homebrew/opt/openssl@3/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"} {
		if _, e := os.Stat(candidate); e == nil {
			openssl = candidate
			break
		}
	}
	if _, e := exec.LookPath(openssl); e != nil {
		t.Skip("openssl unavailable")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	dir := t.TempDir()
	privPath := filepath.Join(dir, "private.pem")
	if e := os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	payload := json.RawMessage(`{"type":"request","message":"Hola <>& café \u2028 \\u2028","context":{"z":[{"y":2,"a":1}],"n":9007199254740993,"b":false}}`)
	cmd := exec.Command("jq", "-cS", ".payload")
	cmd.Stdin = strings.NewReader(`{"payload":` + string(payload) + `}`)
	jq, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	jq = bytes.TrimSuffix(jq, []byte("\n"))
	canonical, e := CanonicalJSON(payload)
	if e != nil || !bytes.Equal(jq, canonical) {
		t.Fatalf("jq mismatch\njq: %s\ngo: %s\n%v", jq, canonical, e)
	}
	hash := sha256.Sum256(jq)
	env := Envelope{From: "a@home.lolad.local", To: "b@home.lolad.local", Subject: "Hello", Priority: "high", InReplyTo: "msg_1_ab"}
	input := []byte(CanonicalString(env.From, env.To, env.Subject, env.Priority, env.InReplyTo, base64.StdEncoding.EncodeToString(hash[:])))
	inputPath := filepath.Join(dir, "canonical")
	if e = os.WriteFile(inputPath, input, 0600); e != nil {
		t.Fatal(e)
	}
	cmd = exec.Command(openssl, "pkeyutl", "-sign", "-rawin", "-inkey", privPath, "-in", inputPath)
	signature, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("installed openssl cannot sign: %s %v", signature, e)
	}
	env.Signature = base64.StdEncoding.EncodeToString(signature)
	if !Verify(pub, env, payload) {
		t.Fatal("openssl signature rejected")
	}
	goSignature, e := Sign(priv, env, payload)
	if e != nil || goSignature != env.Signature {
		t.Fatal("Go and OpenSSL signatures differ", e)
	}
	t.Logf("verified jq canonicalization and %s Ed25519 signature", openssl)
}
