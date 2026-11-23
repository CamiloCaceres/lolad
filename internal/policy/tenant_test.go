package policy

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTenantFor(t *testing.T) {
	fixed := func() string { return "k3p7" }
	cases := map[string]string{
		"milo":                  "milo",
		"Milo":                  "milo",
		`CORP\Ana.Maria`:        "ana-maria",
		"/home/bob":             "bob",
		"José Núñez":            "jose-nunez",
		"Zoë_Ørsted":            "zoe-orsted",
		"straße":                "strasse",
		"  --x__y--  ":          "x-y",
		"山田":                    "lolad-k3p7", // nothing Latin left
		"":                      "lolad-k3p7",
		"User":                  "user-k3p7", // generic
		"Administrator":         "administrator-k3p7",
		"root":                  "root-k3p7",
		"home":                  "home-k3p7", // never the legacy name itself
		"lolad":                 "lolad-k3p7",
		"a":                     "a-k3p7", // too short to be distinctive
		"Ana山田":                 "ana",
		strings.Repeat("x", 80): strings.Repeat("x", maxTenant-5),
	}
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	for in, want := range cases {
		got := tenantFor(in, fixed)
		if got != want {
			t.Errorf("tenantFor(%q) = %q, want %q", in, got, want)
		}
		if !valid.MatchString(got) || strings.HasSuffix(got, "-") {
			t.Errorf("tenantFor(%q) = %q is not a valid tenant", in, got)
		}
	}
	// The suffix is random, so two generic installs differ.
	a, b := tenantFor("user", randomSuffix), tenantFor("user", randomSuffix)
	if a == b || !strings.HasPrefix(a, "user-") || len(a) != len("user-")+4 {
		t.Errorf("suffixes %q %q", a, b)
	}
	if DefaultTenant() == LegacyTenant {
		t.Error("DefaultTenant returned the legacy name")
	}
}

func TestFirstRunWritesTenantOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lolad", "org.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tenant := s.Org().Tenant
	if tenant == "" || tenant == LegacyTenant {
		t.Fatalf("fresh install tenant %q", tenant)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("org.yaml not written 0600:", err)
	}
	first, _ := os.ReadFile(path)
	// Later starts read the same name back and leave the file alone.
	for range 3 {
		s, err = Open(path, "some-agent")
		if err != nil || s.Org().Tenant != tenant {
			t.Fatal(err, s.Org().Tenant)
		}
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(first, again) {
		t.Fatal("org.yaml rewritten")
	}
}

func TestExistingOrgUnchanged(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range map[string]string{
		"home":      "# Camilo's\ntenant: home\nuser:\n  name: milo\n  display: Camilo\n",
		"no tenant": "user:\n  name: milo\n  display: Camilo\n",
		"custom":    "tenant: acme\nuser: {name: boss, display: Boss}\n",
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".yaml")
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path)
		if err != nil {
			t.Fatal(name, err)
		}
		want := map[string]string{"home": "home", "no tenant": LegacyTenant, "custom": "acme"}[name]
		if got := s.Org().Tenant; got != want {
			t.Errorf("%s: tenant %q, want %q", name, got, want)
		}
		b, _ := os.ReadFile(path)
		info, _ := os.Stat(path)
		if string(b) != doc || info.Mode().Perm() != 0o644 {
			t.Errorf("%s: org.yaml was modified", name)
		}
	}
}

// An older install with agents and no org.yaml has always run as "home";
// upgrading must not rename it.
func TestLegacyInstallWithoutOrgKeepsHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.yaml")
	s, err := Open(path, "ultron")
	if err != nil {
		t.Fatal(err)
	}
	if s.Org().Tenant != LegacyTenant {
		t.Fatal(s.Org().Tenant)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("org.yaml written for a legacy install")
	}
}

func TestCreateOrgNeverReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.yaml")
	os.WriteFile(path, []byte("tenant: mine\n"), 0o600)
	if err := CreateOrg(path, OrgYAML("other", "x", "X")); !os.IsExist(err) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "tenant: mine\n" {
		t.Fatal("replaced")
	}
	// The generated file loads and validates.
	p2 := filepath.Join(t.TempDir(), "org.yaml")
	if err := CreateOrg(p2, OrgYAML("ana-k3p7", "ana", "Ana \"A\" María")); err != nil {
		t.Fatal(err)
	}
	o, err := Load(p2)
	if err != nil || o.Tenant != "ana-k3p7" || o.User.Display != `Ana "A" María` {
		t.Fatal(o, err)
	}
}
