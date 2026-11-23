package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestCheckRename(t *testing.T) {
	ok := map[string]string{"Milo PC": "milo-pc", "  camilo ": "camilo", "José's Mac": "jose-s-mac", "ab": "ab", "milo--pc": "milo-pc"}
	for in, want := range ok {
		if got, e := CheckRename("home", in); e != nil || got != want {
			t.Errorf("%q: %q %v", in, got, e)
		}
	}
	for _, in := range []string{"", "   ", "!!", "home", "HOME", "x", "milo", strings.Repeat("a", 33), "日本"} {
		_, e := CheckRename("milo", in)
		var re RenameError
		if !errors.As(e, &re) || re.UserMessage() == "" || !errors.Is(e, core.ErrInvalid) {
			t.Errorf("%q accepted: %v", in, e)
		}
	}
}

// SetTenant changes only the tenant, keeps the rest and the comments, and
// updates the comment that names the tenant.
func TestSetTenant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "org.yaml")
	src := string(OrgYAML("home", "ana", "Ana")) + "# my note\nteams:\n  - id: ops\n    name: Ops\n"
	os.WriteFile(path, []byte(src), 0o600)
	if e := SetTenant(path, "milo-pc"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, "tenant: milo-pc") || !strings.Contains(s, "<name>@milo-pc.lolad.local") || !strings.Contains(s, "# my note") || strings.Contains(s, "home") {
		t.Fatal(s)
	}
	o, e := Load(path)
	if e != nil || o.Tenant != "milo-pc" || o.User.Name != "ana" || len(o.Teams) != 1 {
		t.Fatal(o, e)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	// An old org.yaml with no tenant key gets one.
	os.WriteFile(path, []byte("user:\n  name: ana\n"), 0o600)
	if e := SetTenant(path, "ana-mac"); e != nil {
		t.Fatal(e)
	}
	if o, e = Load(path); e != nil || o.Tenant != "ana-mac" {
		t.Fatal(o, e)
	}
	// No org.yaml: the defaults with this tenant.
	os.Remove(path)
	if e := SetTenant(path, "ana-pc"); e != nil {
		t.Fatal(e)
	}
	if o, e = Load(path); e != nil || o.Tenant != "ana-pc" {
		t.Fatal(o, e)
	}
	if SetTenant(path, "Bad Name") == nil || SetTenant(path, "") == nil {
		t.Fatal("invalid tenant written")
	}
}
