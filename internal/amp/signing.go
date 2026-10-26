// Package amp implements lolad's local Agent Messaging Protocol provider.
package amp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type Envelope struct {
	Version   string    `json:"version"`
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Priority  string    `json:"priority"`
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
	InReplyTo string    `json:"in_reply_to,omitempty"`
	ThreadID  string    `json:"thread_id"`
}
type Payload struct {
	Type    string         `json:"type"`
	Message string         `json:"message"`
	Context map[string]any `json:"context,omitempty"`
}
type Message struct {
	ID       string          `json:"id"`
	Envelope Envelope        `json:"envelope"`
	Payload  json.RawMessage `json:"payload"`
	QueuedAt time.Time       `json:"queued_at"`
	Status   string          `json:"status"`
}
type RouteRequest struct {
	To             string          `json:"to"`
	Subject        string          `json:"subject"`
	Priority       string          `json:"priority,omitempty"`
	InReplyTo      string          `json:"in_reply_to,omitempty"`
	Signature      string          `json:"signature,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

// CanonicalJSON sorts keys recursively, preserves JSON number literals, and
// emits compact UTF-8 without HTML escaping or a terminal newline.
func CanonicalJSON(payload any) ([]byte, error) {
	var raw []byte
	var err error
	switch v := payload.(type) {
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		raw, err = json.Marshal(v)
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err = dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("payload must contain one JSON value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err = enc.Encode(v); err != nil {
		return nil, err
	}
	// encoding/json escapes these two Unicode separators even with EscapeHTML off.
	// jq emits them literally; replacing only actual escapes preserves backslashes.
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	var out bytes.Buffer
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			if i+5 < len(b) && (string(b[i:i+6]) == `\u2028` || string(b[i:i+6]) == `\u2029`) {
				if b[i+5] == '8' {
					out.WriteString("\u2028")
				} else {
					out.WriteString("\u2029")
				}
				i += 5
				continue
			}
			out.WriteByte(b[i])
			i++
			out.WriteByte(b[i])
			continue
		}
		out.WriteByte(b[i])
	}
	return out.Bytes(), nil
}
func PayloadHash(payload any) (string, error) {
	b, e := CanonicalJSON(payload)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(h[:]), nil
}
func CanonicalString(from, to, subject, priority, inReplyTo, payloadHash string) string {
	return strings.Join([]string{from, to, subject, priority, inReplyTo, payloadHash}, "|")
}
func SigningBytes(e Envelope, payload any) ([]byte, error) {
	h, err := PayloadHash(payload)
	if err != nil {
		return nil, err
	}
	return []byte(CanonicalString(e.From, e.To, e.Subject, e.Priority, e.InReplyTo, h)), nil
}
func Sign(key ed25519.PrivateKey, e Envelope, payload any) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid Ed25519 private key")
	}
	b, err := SigningBytes(e, payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, b)), nil
}
func Verify(key ed25519.PublicKey, e Envelope, payload any) bool {
	b, err := SigningBytes(e, payload)
	sig, se := base64.StdEncoding.DecodeString(e.Signature)
	return err == nil && se == nil && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, b, sig)
}
