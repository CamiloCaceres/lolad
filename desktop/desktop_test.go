package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardURL(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:4100": "http://127.0.0.1:4100/",
		":4100":          "http://127.0.0.1:4100/",
		"0.0.0.0:4100":   "http://127.0.0.1:4100/",
		"[::1]:4100":     "http://[::1]:4100/",
	} {
		if got := dashboardURL(in); got != want {
			t.Errorf("dashboardURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListenAddr(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	t.Setenv("LOLAD_URL", "")
	t.Setenv("LOLAD_CONFIG", cfg)
	if got := listenAddr(); got != defaultListen {
		t.Fatalf("no file: %q", got)
	}
	_ = os.WriteFile(cfg, []byte("agents: []\nlisten: \"127.0.0.1:4555\" # mine\n"), 0o600)
	if got := listenAddr(); got != "127.0.0.1:4555" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("LOLAD_URL", "http://127.0.0.1:9/")
	if got := listenAddr(); got != "127.0.0.1:9" {
		t.Fatalf("env override: %q", got)
	}
}

func TestAlive(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	if !alive(ok.URL) {
		t.Fatal("running server reported down")
	}
	ok.Close()
	if alive(ok.URL) {
		t.Fatal("closed server reported up")
	}
}

func TestOriginOf(t *testing.T) {
	if got := originOf("http://127.0.0.1:4100/inbox?x=1"); got != "http://127.0.0.1:4100" {
		t.Fatal(got)
	}
	if originOf("not a url") != "" || originOf("file:///etc/passwd") != "" {
		t.Fatal("non-http origins must be empty")
	}
}

func TestDownloadNamesStayInDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }))
	defer srv.Close()

	for _, name := range []string{"../../evil.txt", `..\..\evil.txt`, "/etc/passwd", "", ".."} {
		p, err := download(srv.URL, name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if filepath.Dir(p) != filepath.Join(home, "Downloads") {
			t.Fatalf("%q escaped Downloads: %s", name, p)
		}
	}
	// Existing files are never overwritten.
	a, _ := download(srv.URL, "r.pdf")
	b, _ := download(srv.URL, "r.pdf")
	if a == b {
		t.Fatal("overwrote an existing download")
	}
	if filepath.Base(b) != "r (2).pdf" {
		t.Fatalf("got %s", b)
	}
}

func TestDownloadRejectsErrorStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := download(srv.URL, "x"); err == nil {
		t.Fatal("a 404 must not be saved as a file")
	}
}

func TestCleanEnvDropsSecrets(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "secret")
	t.Setenv("GITHUB_TOKEN", "secret")
	for _, kv := range cleanEnv() {
		if kv == "ANTHROPIC_API_KEY=secret" || kv == "GITHUB_TOKEN=secret" {
			t.Fatalf("leaked %s", kv)
		}
	}
}
