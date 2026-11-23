package policy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

// tenantNameRE is what a person may rename their lolad to: 2 to 32 of a-z,
// 0-9 and "-", starting and ending with a letter or digit. It is a subset of
// what amp accepts, so every name here makes a valid provider.
var tenantNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// RenameError refuses a new name with words the dashboard can show as is.
type RenameError struct{ Msg string }

func (e RenameError) Error() string       { return "invalid request: " + e.Msg }
func (e RenameError) UserMessage() string { return e.Msg }
func (e RenameError) Unwrap() error       { return core.ErrInvalid }

// TenantSlug turns what a person typed ("Milo's PC") into a tenant
// ("milo-s-pc"): lowercase, accents folded, anything else a single dash.
// Unlike DefaultTenant it never adds a random suffix.
func TenantSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		f := fold(r)
		if f == "" {
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
			continue
		}
		b.WriteString(f)
		dash = false
	}
	return strings.Trim(b.String(), "-")
}

// CheckRename returns the tenant a rename from current to input would use,
// or a RenameError saying why it cannot.
func CheckRename(current, input string) (string, error) {
	name := TenantSlug(input)
	switch {
	case name == "":
		return "", RenameError{"Type a new name for this lolad."}
	case name == strings.ToLower(current):
		return "", RenameError{fmt.Sprintf("This lolad is already called %q.", name)}
	case name == LegacyTenant:
		return "", RenameError{fmt.Sprintf("%q is the old default that many lolads share. Pick a name that is yours, like your first name.", LegacyTenant)}
	case !tenantNameRE.MatchString(name):
		return "", RenameError{"Use 2 to 32 letters, digits or dashes, like milo-pc."}
	}
	return name, nil
}

// SetTenant changes the tenant in org.yaml, keeping the rest of the file and
// its comments. A missing org.yaml gets the defaults with this tenant. The
// result must still validate; the file is replaced atomically.
func SetTenant(path, tenant string) error {
	if !tenantNameRE.MatchString(tenant) {
		return fmt.Errorf("invalid tenant %q", tenant)
	}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		o := DefaultOrg()
		o.Tenant = tenant
		return Save(path, o)
	}
	if e != nil {
		return e
	}
	var doc yaml.Node
	if e = yaml.Unmarshal(b, &doc); e != nil {
		return yamlError(path, e)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a mapping", path)
	}
	m := doc.Content[0]
	old := LegacyTenant
	found := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "tenant" {
			old = m.Content[i+1].Value
			m.Content[i+1].Value, m.Content[i+1].Tag, m.Content[i+1].Style = tenant, "!!str", 0
			found = true
		}
	}
	if !found {
		m.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tenant"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: tenant}}, m.Content...)
	}
	// The comment OrgYAML writes names the tenant; keep it true.
	for _, n := range []*yaml.Node{&doc, m, m.Content[0]} {
		n.HeadComment = strings.ReplaceAll(n.HeadComment, "@"+old+".lolad.local", "@"+tenant+".lolad.local")
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if e = enc.Encode(&doc); e != nil {
		return e
	}
	if e = enc.Close(); e != nil {
		return e
	}
	return writeOrg(path, out.Bytes())
}

// writeOrg replaces path atomically with mode 0600 after checking that the
// bytes load as a valid org.
func writeOrg(path string, b []byte) error {
	o := DefaultOrg()
	if e := yaml.Unmarshal(b, &o); e != nil {
		return e
	}
	if _, e := Validate(o); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".org-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
