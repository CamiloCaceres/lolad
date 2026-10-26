package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFederationConfig(t *testing.T) {
	for _, tc := range []struct {
		body    string
		enabled bool
		url     string
	}{
		{"", true, ""},
		{"remote:\n  public_url: https://milo.example\n", true, "https://milo.example"},
		{"federation:\n  enabled: false\n  display: Milo\n  url: http://127.0.0.1:4196\n", false, "http://127.0.0.1:4196"},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if e := os.WriteFile(path, []byte(tc.body), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := LoadConfig(path)
		if e != nil || c.Federation.Enabled != tc.enabled || c.Federation.URL != tc.url {
			t.Fatalf("%+v %v", c.Federation, e)
		}
	}
	for _, url := range []string{"https://milo.example", "http://localhost:4196", "http://127.0.0.1:4196", "http://[::1]:4196"} {
		if e := ValidateFederationURL(url); e != nil {
			t.Fatal(e)
		}
	}
	for _, url := range []string{"http://192.168.1.2:4196", "http://milo.example", "ftp://localhost", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=x"} {
		if e := ValidateFederationURL(url); e == nil {
			t.Errorf("accepted invalid origin")
		}
	}
}
