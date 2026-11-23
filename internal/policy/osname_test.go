package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccountDisplay(t *testing.T) {
	for _, c := range []struct{ username, name, want string }{
		// Windows local account: Go's Name falls back to the SAM name.
		{`CAMILO\Usuario`, `CAMILO\Usuario`, "Usuario"},
		{`CAMILO\Usuario`, "", "Usuario"},
		{`CAMILO\USUARIO`, `CAMILO\USUARIO`, "Usuario"},
		// A full name wins.
		{`CAMILO\Usuario`, "Camilo Caceres", "Camilo Caceres"},
		{`CORP\camilo.caceres`, `CORP\camilo.caceres`, "Camilo Caceres"},
		// macOS / Linux.
		{"milo", "Milo Smith", "Milo Smith"},
		{"milo", "milo", "Milo"},
		{"milo", "", "Milo"},
		{"ana_maria", "", "Ana Maria"},
		{"josé", "", "José"},
		{"", "", ""},
	} {
		if got := AccountDisplay(c.username, c.name); got != c.want {
			t.Errorf("AccountDisplay(%q, %q) = %q, want %q", c.username, c.name, got, c.want)
		}
	}
}

func TestFixStoredDisplay(t *testing.T) {
	for in, want := range map[string]string{
		`CAMILO\Usuario`: "Usuario",
		`CORP\j.smith`:   "J Smith",
		"Camilo":         "Camilo",
		"Camilo Caceres": "Camilo Caceres",
		`A\B and C`:      `A\B and C`, // typed by a person: left alone
		"":               "",
	} {
		if got := fixStoredDisplay(in); got != want {
			t.Errorf("fixStoredDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}

// An org.yaml written by beta.1/beta.2 on Windows loads with a clean name.
func TestLoadRepairsWindowsDisplay(t *testing.T) {
	p := filepath.Join(t.TempDir(), "org.yaml")
	if err := os.WriteFile(p, OrgYAML("usuario", "usuario", `CAMILO\Usuario`), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if o.User.Display != "Usuario" || o.User.Name != "usuario" || o.Tenant != "usuario" {
		t.Fatalf("loaded user %+v tenant %q, want display Usuario, name and tenant usuario", o.User, o.Tenant)
	}
}
