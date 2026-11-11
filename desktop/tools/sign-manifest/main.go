// Command sign-manifest creates the Ed25519 update key, builds update.json
// from release artifacts, and signs it. It is the release workflow's tool.
//
//	sign-manifest keygen [--out key.pem]
//	sign-manifest sign --key key.pem --in update.json [--out update.json]
//	sign-manifest generate --version 1.2.3 --multiplexer-version 3.5a-1 \
//	    --notes-url URL --url-base https://github.com/.../releases/download/v1.2.3/ \
//	    --darwin-arm64 Lolad.app.zip --darwin-amd64 ... --windows-amd64 Lolad-Setup.exe \
//	    [--min-version 1.0.0] [--key key.pem] [--out update.json]
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/desktop/updater"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sign-manifest:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: sign-manifest keygen|sign|generate [flags]")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:], stdout, stderr)
	case "sign":
		return sign(args[1:], stdout)
	case "generate":
		return generate(args[1:], stdout)
	}
	return fmt.Errorf("unknown command %q (want keygen, sign or generate)", args[0])
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func keygen(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("keygen")
	out := fs.String("out", "key.pem", "private key file to create (0600, never overwritten)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privPEM, err := updater.MarshalPrivateKeyPEM(priv)
	if err != nil {
		return err
	}
	pubPEM, err := updater.MarshalPublicKeyPEM(pub)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("refusing to overwrite or create %s: %w", *out, err)
	}
	if _, err := f.Write(privPEM); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	pubFile := strings.TrimSuffix(*out, filepath.Ext(*out)) + ".pub.pem"
	if err := os.WriteFile(pubFile, pubPEM, 0o644); err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString(pub)
	fmt.Fprintf(stdout, "private key: %s (back it up offline, never commit it)\npublic key:  %s\n", *out, pubFile)
	fmt.Fprintf(stdout, "ldflags:     -X github.com/CamiloCaceres/lolad/desktop/updater.PublicKeyB64=%s\n", b64)
	return nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("--key is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return updater.ParsePrivateKeyPEM(b)
}

func sign(args []string, stdout io.Writer) error {
	fs := newFlags("sign")
	keyPath := fs.String("key", "", "private key file")
	in := fs.String("in", "", "manifest to sign")
	out := fs.String("out", "", "output (default: overwrite --in)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("--in is required")
	}
	key, err := loadKey(*keyPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	signed, err := updater.SignManifest(raw, key)
	if err != nil {
		return err
	}
	// Prove it verifies before writing anything.
	if _, err := updater.ParseAndVerify(signed, key.Public().(ed25519.PublicKey)); err != nil {
		return fmt.Errorf("signed manifest does not verify: %w", err)
	}
	dst := *out
	if dst == "" {
		dst = *in
	}
	if err := os.WriteFile(dst, signed, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "signed %s\n", dst)
	return nil
}

func generate(args []string, stdout io.Writer) error {
	fs := newFlags("generate")
	version := fs.String("version", "", "release version")
	notes := fs.String("notes-url", "", "release notes URL")
	minV := fs.String("min-version", "", "oldest version that can update in place")
	mux := fs.String("multiplexer-version", "", "bundled tmux/psmux version")
	base := fs.String("url-base", "", "URL prefix the artifacts are published under")
	keyPath := fs.String("key", "", "sign with this key (otherwise run `sign` afterwards)")
	out := fs.String("out", "update.json", "output file")
	arts := map[string]*string{
		"darwin-arm64":  fs.String("darwin-arm64", "", "macOS arm64 Lolad.app.zip"),
		"darwin-amd64":  fs.String("darwin-amd64", "", "macOS amd64 Lolad.app.zip"),
		"windows-amd64": fs.String("windows-amd64", "", "Windows installer"),
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version == "" || *notes == "" || *mux == "" || *base == "" {
		return errors.New("--version, --notes-url, --multiplexer-version and --url-base are required")
	}
	m := updater.Manifest{Version: *version, NotesURL: *notes, MinVersion: *minV, MultiplexerVersion: *mux, Platforms: map[string]updater.Artifact{}}
	for plat, p := range arts {
		if *p == "" {
			continue
		}
		a, err := describe(*p, *base)
		if err != nil {
			return fmt.Errorf("%s: %w", plat, err)
		}
		m.Platforms[plat] = a
	}
	if len(m.Platforms) == 0 {
		return errors.New("give at least one of --darwin-arm64, --darwin-amd64, --windows-amd64")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if *keyPath != "" {
		key, err := loadKey(*keyPath)
		if err != nil {
			return err
		}
		if raw, err = updater.SignManifest(raw, key); err != nil {
			return err
		}
	} else if raw, err = indent(raw); err != nil {
		return err
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	signedNote := "unsigned"
	if *keyPath != "" {
		signedNote = "signed"
	}
	fmt.Fprintf(stdout, "wrote %s (%s, %d platforms)\n", *out, signedNote, len(m.Platforms))
	return nil
}

func indent(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, "", "  ")
}

func describe(path, base string) (updater.Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return updater.Artifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return updater.Artifact{}, err
	}
	u := strings.TrimRight(base, "/") + "/" + filepath.Base(path)
	if err := updater.ValidateURL(u); err != nil {
		return updater.Artifact{}, err
	}
	return updater.Artifact{URL: u, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
