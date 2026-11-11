package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0}, {"1.0.1", "1.0.0", 1}, {"1.2", "1.10", -1}, {"v2.0.0", "1.9.9", 1},
		{"1.0.0-rc1", "1.0.0", -1}, {"1.0", "1.0.0", 0}, {"1.0.0+build5", "1.0.0", 0},
	}
	for _, c := range cases {
		got, ok := compareVersions(c.a, c.b)
		if !ok || got != c.want {
			t.Errorf("compare(%s,%s)=%d,%v want %d", c.a, c.b, got, ok, c.want)
		}
	}
	if _, ok := compareVersions("abc", "1.0"); ok {
		t.Error("garbage version accepted")
	}
}

func TestCanonicalIgnoresSignatureOrderAndWhitespace(t *testing.T) {
	a, _ := Canonical([]byte(`{"b":1,"a":{"y":2,"x":"<&>"},"signature":"zzz"}`))
	b, _ := Canonical([]byte("{ \"a\" : {\"x\":\"<&>\", \"y\":2}, \"b\":1 }"))
	if string(a) != string(b) || string(a) != `{"a":{"x":"<&>","y":2},"b":1}` {
		t.Fatalf("canonical mismatch: %s / %s", a, b)
	}
}

func TestManifestSignature(t *testing.T) {
	key := testKey(t)
	pub := key.Public().(ed25519.PublicKey)
	raw := []byte(`{"version":"1.2.3","notes_url":"https://x","multiplexer_version":"3","platforms":{},"future_field":[1,2]}`)
	signed, err := SignManifest(raw, key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseAndVerify(signed, pub)
	if err != nil || m.Version != "1.2.3" {
		t.Fatalf("verify: %v %+v", err, m)
	}
	// tamper with a known field and with one this version does not know about
	for field, val := range map[string]any{"version": "9.9.9", "future_field": []int{1, 3}} {
		var d map[string]any
		json.Unmarshal(signed, &d)
		d[field] = val
		bad, _ := json.Marshal(d)
		if _, err := ParseAndVerify(bad, pub); !errors.Is(err, ErrBadSignature) {
			t.Errorf("tampered %s accepted: %v", field, err)
		}
	}
	// unsigned
	if _, err := ParseAndVerify(raw, pub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("unsigned accepted: %v", err)
	}
	// other key
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := ParseAndVerify(signed, otherPub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong key accepted: %v", err)
	}
	// garbage signature
	var doc map[string]any
	json.Unmarshal(signed, &doc)
	doc["signature"] = "!!!"
	g, _ := json.Marshal(doc)
	if _, err := ParseAndVerify(g, pub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("garbage sig accepted: %v", err)
	}
}

func TestKeyPEMRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b, _ := MarshalPrivateKeyPEM(priv)
	got, err := ParsePrivateKeyPEM(b)
	if err != nil || !got.Equal(priv) {
		t.Fatal("private key round trip", err)
	}
	if p, _ := MarshalPublicKeyPEM(pub); !strings.Contains(string(p), "PUBLIC KEY") {
		t.Fatal("public pem")
	}
}

func TestCompiledDefaultKeyIsTheTestKey(t *testing.T) {
	pub, err := publicKeyFromB64(PublicKeyB64)
	if err != nil || !pub.Equal(testKey(t).Public()) {
		t.Fatal("default PublicKeyB64 must match testdata/test-key.pem")
	}
	if _, err := New(Config{CurrentVersion: "1.0.0"}); !errors.Is(err, ErrTestKey) {
		t.Fatalf("test key must be refused without AllowUnsigned: %v", err)
	}
	if _, err := New(Config{CurrentVersion: "1.0.0", AllowUnsigned: true}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://github.com/x": true, "http://127.0.0.1:1/x": true,
		"http://example.com/x": false, "http://localhost/x": false, "ftp://x/y": false, "https://": false,
	} {
		if (ValidateURL(u) == nil) != ok {
			t.Errorf("ValidateURL(%q) want ok=%v", u, ok)
		}
	}
	if _, err := New(Config{CurrentVersion: "1.0.0", ManifestURL: "http://evil.example/update.json", PublicKey: testKey(t).Public().(ed25519.PublicKey)}); err == nil {
		t.Error("http manifest url accepted")
	}
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.1.0")
	u := f.updater(t, nil)
	res, err := u.Check(ctx)
	if err != nil || !res.Available || res.Version != "1.1.0" || res.Artifact.Size == 0 {
		t.Fatalf("check: %v %+v", err, res)
	}
	// multiplexer file absent and manifest names one: agents restart
	if !res.NeedsAgentRestart || !strings.Contains(res.Message, "agents will restart") {
		t.Fatalf("expected restart notice: %+v", res)
	}
	// same multiplexer version installed: no restart
	os.WriteFile(filepath.Join(f.dir, "multiplexer.version"), []byte("mux-2\n"), 0o644)
	res, _ = u.Check(ctx)
	if res.NeedsAgentRestart || !strings.Contains(res.Message, "keep running") {
		t.Fatalf("unexpected restart notice: %+v", res)
	}
	// different one: restart
	os.WriteFile(filepath.Join(f.dir, "multiplexer.version"), []byte("mux-1"), 0o644)
	if res, _ = u.Check(ctx); !res.NeedsAgentRestart {
		t.Fatal("differing multiplexer should need restart")
	}
}

func TestCheckUpToDateDowngradeAndForgery(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.0.0")
	u := f.updater(t, nil)
	res, err := u.Check(ctx)
	if err != nil || res.Available {
		t.Fatalf("same version: %v %+v", err, res)
	}
	f.publishManifest(t, "0.9.0", "m")
	if _, err := u.Check(ctx); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade: %v", err)
	}
	// signed by a different key
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f.key = other
	f.publishManifest(t, "2.0.0", "m")
	if _, err := u.Check(ctx); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged manifest: %v", err)
	}
	// unsigned
	f.srv.manifest = []byte(`{"version":"2.0.0","platforms":{}}`)
	if _, err := u.Check(ctx); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("unsigned manifest: %v", err)
	}
}

func TestCheckMissingPlatformMinVersionAndHTTPError(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.1.0")
	if _, err := f.updater(t, func(c *Config) { c.Platform = "windows-amd64" }).Check(ctx); !errors.Is(err, ErrNoPlatform) {
		t.Fatalf("platform: %v", err)
	}
	f.srv.publish(t, f.key, Manifest{Version: "3.0.0", MinVersion: "2.0.0", MultiplexerVersion: "m",
		Platforms: map[string]Artifact{"darwin-arm64": {URL: f.srv.URL + "/art.zip", SHA256: sum(f.body), Size: 5}}})
	res, err := f.updater(t, nil).Check(ctx)
	if err != nil || !res.ManualInstall || !strings.Contains(res.Message, "manually") && !strings.Contains(res.Message, "installer") {
		t.Fatalf("min_version: %v %+v", err, res)
	}
	if _, err := f.updater(t, nil).Download(ctx, res, nil); !errors.Is(err, ErrManualInstall) {
		t.Fatalf("manual install should block download: %v", err)
	}
	f.srv.manifest = nil
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if _, err := f.updater(t, func(c *Config) { c.ManifestURL = bad.URL + "/update.json" }).Check(ctx); err == nil {
		t.Fatal("404 manifest accepted")
	}
}

func TestOnboardingBlocks(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.1.0")
	u := f.updater(t, nil)
	os.WriteFile(filepath.Join(f.dir, "flag"), nil, 0o644)
	res, err := u.Check(ctx)
	if err != nil || !res.Available || !res.OnboardingBlocked {
		t.Fatalf("check during onboarding: %v %+v", err, res)
	}
	if _, err := u.Download(ctx, res, nil); !errors.Is(err, ErrOnboarding) {
		t.Fatalf("download: %v", err)
	}
	if err := u.Apply("whatever"); !errors.Is(err, ErrOnboarding) {
		t.Fatalf("apply: %v", err)
	}
	os.Remove(filepath.Join(f.dir, "flag"))
	if _, err := u.Download(ctx, res, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDownload(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.1.0")
	u := f.updater(t, nil)
	res, _ := u.Check(ctx)
	var last, total int64
	calls := 0
	p, err := u.Download(ctx, res, func(d, tt int64) { last, total = d, tt; calls++ })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(f.body) || last != int64(len(f.body)) || total != last || calls == 0 {
		t.Fatalf("download content/progress: %d/%d calls=%d", last, total, calls)
	}
	if filepath.Base(p) != "art.zip" {
		t.Fatalf("name %s", p)
	}
}

func TestDownloadRejectsBadArtifacts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "1.1.0")
	u := f.updater(t, nil)
	res, _ := u.Check(ctx)
	leftover := func() int { e, _ := filepath.Glob(filepath.Join(f.dir, "lolad-update-*")); return len(e) }

	cases := map[string]func(r CheckResult) CheckResult{
		"hash":      func(r CheckResult) CheckResult { r.Artifact.SHA256 = strings.Repeat("0", 64); return r },
		"too long":  func(r CheckResult) CheckResult { r.Artifact.Size--; return r },
		"too short": func(r CheckResult) CheckResult { r.Artifact.Size++; return r },
	}
	for name, mut := range cases {
		r := mut(*res)
		if _, err := u.Download(ctx, &r, nil); !errors.Is(err, ErrChecksum) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if leftover() != 0 {
		t.Errorf("failed downloads left %d temp dirs", leftover())
	}
	r := *res
	r.Artifact.URL = f.srv.URL + "/missing.zip"
	if _, err := u.Download(ctx, &r, nil); err == nil {
		t.Error("404 accepted")
	}
	r.Artifact.URL = "http://example.com/a.zip"
	if _, err := u.Download(ctx, &r, nil); err == nil {
		t.Error("non-https accepted")
	}
	if _, err := u.Download(ctx, &CheckResult{}, nil); err == nil {
		t.Error("empty result accepted")
	}
}

func TestDownloadRefusesRedirectToPlainHTTP(t *testing.T) {
	f := newFixture(t, "1.1.0")
	mux := http.NewServeMux()
	mux.HandleFunc("/r", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/evil.zip", http.StatusFound)
	})
	rs := httptest.NewServer(mux)
	defer rs.Close()
	u := f.updater(t, nil)
	res := &CheckResult{Available: true, Artifact: Artifact{URL: rs.URL + "/r", SHA256: strings.Repeat("a", 64), Size: 3}}
	if _, err := u.Download(context.Background(), res, nil); err == nil {
		t.Fatal("redirect to http accepted")
	}
}
