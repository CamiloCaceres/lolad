package cli

import (
	"bytes"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFederationDoctor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			fmt.Fprint(w, `{"ok":true}`)
		case "/api/connections":
			fmt.Fprint(w, `[{"provider":"jugui.lolad.local","last_error":"peer unreachable"}]`)
		default:
			t.Errorf("unexpected doctor request")
		}
	}))
	defer server.Close()
	state := t.TempDir()
	if e := os.Mkdir(filepath.Join(state, "federation"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(state, "federation", "provider.key"), []byte("write-only-placeholder"), 0600); e != nil {
		t.Fatal(e)
	}
	c := registry.Config{Listen: strings.TrimPrefix(server.URL, "http://"), StateDir: state, Federation: registry.FederationConfig{Enabled: true, URL: server.URL}}
	var out bytes.Buffer
	federationDoctor(t.Context(), c, &out)
	for _, expected := range []string{"OK   federation provider key", "OK   federation URL configured", "OK   federation URL reachable from this machine", "FIX  federation peer jugui.lolad.local: peer unreachable"} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if strings.Contains(out.String(), "write-only-placeholder") {
		t.Fatal("provider key read into doctor output")
	}
	out.Reset()
	c.Federation.Enabled = false
	federationDoctor(t.Context(), c, &out)
	if out.String() != "INFO federation disabled\n" {
		t.Fatal(out.String())
	}
}
