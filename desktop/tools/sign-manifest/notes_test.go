package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

// --notes-file puts short notes into the signed manifest; an empty value
// leaves them out, and a long file is refused rather than cut silently.
func TestGenerateNotesFile(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key.pem")
	if _, err := do(t, "keygen", "--out", key); err != nil {
		t.Fatal(err)
	}
	kb, _ := os.ReadFile(key)
	priv, _ := updater.ParsePrivateKeyPEM(kb)
	pub := priv.Public().(ed25519.PublicKey)
	art := filepath.Join(dir, "Lolad.app.zip")
	os.WriteFile(art, []byte("zipdata"), 0o644)
	man := filepath.Join(dir, "update.json")
	gen := func(notes string) error {
		_, err := do(t, "generate", "--notes-file", notes, "--version", "1.2.3", "--notes-url", "https://n",
			"--multiplexer-version", "m1", "--url-base", "https://example.com/dl/", "--darwin-arm64", art, "--key", key, "--out", man)
		return err
	}
	nf := filepath.Join(dir, "notes.txt")
	os.WriteFile(nf, []byte("\nSettings has an Updates section.\nFaster start.\n\n"), 0o644)
	if err := gen(nf); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(man)
	m, err := updater.ParseAndVerify(raw, pub)
	if err != nil || m.Notes != "Settings has an Updates section.\nFaster start." {
		t.Fatalf("notes %q, %v", m.Notes, err)
	}
	if err := gen(""); err != nil {
		t.Fatal(err)
	}
	if raw, _ = os.ReadFile(man); strings.Contains(string(raw), `"notes"`) {
		t.Fatal("empty notes written")
	}
	os.WriteFile(nf, []byte(strings.Repeat("x", 601)), 0o644)
	if err := gen(nf); err == nil || !strings.Contains(err.Error(), "under 600") {
		t.Fatalf("long notes: %v", err)
	}
	if err := gen(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatal("missing notes file accepted")
	}
}
