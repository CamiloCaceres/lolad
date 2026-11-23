package policy

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LegacyTenant is the tenant lolad used for every install before
// v0.2.0-beta.1. An org.yaml without a tenant, or an older install with
// agents and no org.yaml, keeps it, so existing addresses never change.
const LegacyTenant = "home"

// genericLogins are OS account names many people share; a tenant made from
// one gets a random suffix so two fresh installs do not collide.
var genericLogins = map[string]bool{
	"home": true, "user": true, "users": true, "admin": true, "administrator": true, "root": true,
	"owner": true, "guest": true, "ubuntu": true, "pi": true, "lolad": true, "test": true,
	"default": true, "me": true, "mac": true, "macbook": true, "pc": true, "windows": true,
	"desktop": true, "laptop": true, "dev": true, "demo": true, "local": true,
}

// DefaultTenant names a new lolad after the OS login, for example "milo"
// (provider milo.lolad.local). A login that is generic, empty or not Latin
// gets a random suffix: "user-k3p7", "lolad-9xqa". Call it once and store the
// result: the suffix differs on every call.
func DefaultTenant() string {
	login := ""
	if u, err := user.Current(); err == nil {
		login = u.Username
	}
	return tenantFor(login, randomSuffix)
}

const maxTenant = 32

// tenantFor slugs an OS login (DOMAIN\name and paths are reduced to the
// name) into a tenant, adding suffix() for generic or empty results.
func tenantFor(login string, suffix func() string) string {
	login = login[strings.LastIndexAny(login, `\/`)+1:]
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(login) {
		s := fold(r)
		if s == "" {
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
			continue
		}
		b.WriteString(s)
		dash = false
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxTenant-5 {
		slug = strings.TrimRight(slug[:maxTenant-5], "-")
	}
	switch {
	case slug == "":
		return "lolad-" + suffix()
	case genericLogins[slug] || len(slug) < 2:
		return slug + "-" + suffix()
	}
	return slug
}

// fold maps a lowercase rune to tenant characters: a-z and 0-9 as they are,
// common accented Latin letters to their base letter, anything else to "".
func fold(r rune) string {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return string(r)
	}
	for base, set := range accents {
		if strings.ContainsRune(set, r) {
			return base
		}
	}
	return ""
}

var accents = map[string]string{
	"a": "àáâãäåāăą", "c": "çćčĉċ", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ", "h": "ĥħ",
	"i": "ìíîïĩīĭįı", "j": "ĵ", "k": "ķ", "l": "ĺļľŀł", "n": "ñńņňŉ", "o": "òóôõöøōŏő",
	"r": "ŕŗř", "s": "śŝşšș", "t": "ţťŧț", "u": "ùúûüũūŭůűų", "w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	"ss": "ß", "ae": "æ", "oe": "œ", "th": "þ",
}

func randomSuffix() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// OrgYAML is the org.yaml a new lolad starts with. setup writes the same
// file when onboarding runs before the daemon has.
func OrgYAML(tenant, name, display string) []byte {
	q := func(s string) string { b, _ := yaml.Marshal(s); return strings.TrimRight(string(b), "\n") }
	return fmt.Appendf(nil, "# This lolad's name: addresses look like <name>@%s.lolad.local.\n# Lolads you connect with must each have a different one.\ntenant: %s\nuser:\n  name: %s\n  display: %s\ndefaults:\n  user_to_agents: all\n  agents_to_user: all\n  vertical: true\n  independents: user-only\n",
		tenant, q(tenant), q(name), q(display))
}

// CreateOrg writes a new org.yaml at path (mode 0600), failing with
// os.ErrExist rather than replacing one that is already there.
func CreateOrg(path string, doc []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(doc)
	if err == nil {
		err = f.Sync()
	}
	if c := f.Close(); err == nil {
		err = c
	}
	if err != nil {
		os.Remove(path) // never leave a half-written org.yaml behind
	}
	return err
}

// firstRun writes org.yaml with a fresh tenant when there is none and lolad
// manages no agents yet, so the name is chosen once and kept. With agents
// already registered and no org.yaml, this is an older install running as
// LegacyTenant, and nothing is written.
func firstRun(path string, known []string) error {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || len(known) > 0 {
		return nil
	}
	name, display := localUser()
	err := CreateOrg(path, OrgYAML(DefaultTenant(), name, display))
	if errors.Is(err, os.ErrExist) {
		return nil // created meanwhile: keep that one
	}
	return err
}
