package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

func do(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out, &out)
	return out.String(), err
}

func TestKeygenSignGenerateEndToEnd(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key.pem")
	out, err := do(t, "keygen", "--out", key)
	if err != nil || !strings.Contains(out, "PublicKeyB64=") {
		t.Fatalf("keygen: %v %s", err, out)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode())
	}
	if _, err := do(t, "keygen", "--out", key); err == nil {
		t.Fatal("keygen overwrote an existing key")
	}
	kb, _ := os.ReadFile(key)
	priv, _ := updater.ParsePrivateKeyPEM(kb)
	pub := priv.Public().(ed25519.PublicKey)
	if _, err := os.Stat(filepath.Join(dir, "key.pub.pem")); err != nil {
		t.Fatal("no public pem")
	}

	art := filepath.Join(dir, "Lolad.app.zip")
	os.WriteFile(art, []byte("zipdata"), 0o644)
	win := filepath.Join(dir, "Lolad-Setup.exe")
	os.WriteFile(win, []byte("exe"), 0o644)
	man := filepath.Join(dir, "update.json")
	if _, err := do(t, "generate", "--version", "1.2.3", "--notes-url", "https://n", "--multiplexer-version", "m1",
		"--url-base", "https://example.com/dl/", "--darwin-arm64", art, "--windows-amd64", win, "--out", man); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(man)
	if _, err := updater.ParseAndVerify(raw, pub); err == nil {
		t.Fatal("unsigned generate output verified")
	}
	if _, err := do(t, "sign", "--key", key, "--in", man); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(man)
	m, err := updater.ParseAndVerify(raw, pub)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("zipdata"))
	a := m.Platforms["darwin-arm64"]
	if a.SHA256 != hex.EncodeToString(h[:]) || a.Size != 7 || a.URL != "https://example.com/dl/Lolad.app.zip" || len(m.Platforms) != 2 {
		t.Fatalf("manifest: %+v", m)
	}

	// generate with --key signs directly
	if _, err := do(t, "generate", "--version", "1.2.4", "--notes-url", "https://n", "--multiplexer-version", "m1",
		"--url-base", "https://example.com/dl", "--darwin-arm64", art, "--key", key, "--out", man); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(man)
	if m, err := updater.ParseAndVerify(raw, pub); err != nil || m.Version != "1.2.4" {
		t.Fatalf("direct signing: %v", err)
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{}, {"bogus"}, {"sign"}, {"sign", "--in", "x"}, {"generate"},
		{"generate", "--version", "1", "--notes-url", "u", "--multiplexer-version", "m", "--url-base", "https://x"},
		{"generate", "--version", "1", "--notes-url", "u", "--multiplexer-version", "m", "--url-base", "http://example.com", "--darwin-arm64", filepath.Join(dir, "none")},
	} {
		if _, err := do(t, args...); err == nil {
			t.Errorf("%v: expected error", args)
		}
	}
}
