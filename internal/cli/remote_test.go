package cli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPairAndDevicesCLI(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/devices/pair":
			fmt.Fprint(w, `{"code":"ABCD2345","expires":"2026-10-26T12:10:00Z"}`)
		case "/api/devices":
			fmt.Fprint(w, `[{"id":"dev_test","name":"phone"}]`)
		case "/api/devices/dev_test":
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := registry.Config{Remote: registry.RemoteConfig{PublicURL: "https://example.ts.net:4100"}}
	var out bytes.Buffer
	for _, args := range [][]string{{"pair", "--name", "phone"}, {"devices"}, {"devices", "ls"}, {"devices", "rm", "dev_test"}} {
		if e := remoteCommand(t.Context(), server.URL, c, args, &out); e != nil {
			t.Fatal(e)
		}
	}
	if !strings.Contains(out.String(), "ABCD2345") || !strings.Contains(out.String(), "lolad://pair?") {
		t.Fatal("pair output missing")
	}
	if strings.Contains(out.String(), "lol_") {
		t.Fatal("existing credential printed")
	}
	if len(calls) != 4 || calls[3] != "DELETE /api/devices/dev_test" {
		t.Fatal(calls)
	}
}
func TestDoctorReadOnlyChecks(t *testing.T) {
	dir := t.TempDir()
	for _, bin := range []string{"tmux", "claude", "codewhale", "tailscale"} {
		body := "#!/bin/sh\nexit 0\n"
		if bin == "tailscale" {
			body = "#!/bin/sh\n[ \"$*\" = 'serve status --json' ] || exit 1\nprintf '%s' '{\"Web\":{\"handler\":{\"Proxy\":\"http://127.0.0.1:4191\"}}}'\n"
		}
		if e := os.WriteFile(filepath.Join(dir, bin), []byte(body), 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", dir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Error("unexpected request")
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	c := registry.Config{Listen: strings.TrimPrefix(server.URL, "http://"), StateDir: dir, AgentsDirs: []string{dir}, TmuxSocket: "lolad-test-doctor", Remote: registry.RemoteConfig{Listen: "127.0.0.1:4191", PublicURL: "https://example.ts.net:4100"}}
	var out bytes.Buffer
	if e := doctor(context.Background(), c, &out); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"OK   listener config", "Tailscale Serve mapping", "daily database backup"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("missing %s: %s", value, out.String())
		}
	}
}

func TestServeMapping(t *testing.T) {
	data := []byte(`{"Web":{"example.ts.net:4100":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:4191"}}}}}`)
	if !serveMapped(data, "https://example.ts.net:4100", "127.0.0.1:4191") {
		t.Fatal("mapping not found")
	}
	if serveMapped(data, "https://other.ts.net:4100", "127.0.0.1:4191") || serveMapped(data, "https://example.ts.net:4100", "127.0.0.1:4100") {
		t.Fatal("wrong backend/origin accepted")
	}
}
