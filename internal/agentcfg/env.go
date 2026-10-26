package agentcfg

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type envLine struct{ raw, key, value, comment string }

func escapedQuote(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func parseEnv(b []byte) []envLine {
	var out []envLine
	for _, raw := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		l := envLine{raw: raw}
		s := strings.TrimSpace(raw)
		s = strings.TrimPrefix(s, "export ")
		k, v, ok := strings.Cut(s, "=")
		if ok && envKey.MatchString(strings.TrimSpace(k)) {
			l.key = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if len(v) > 0 && (v[0] == '\'' || v[0] == '"') {
				q := v[0]
				end := 1
				for end < len(v) {
					if v[end] == q && (q == '\'' || !escapedQuote(v, end)) {
						break
					}
					end++
				}
				if end < len(v) {
					lit := v[:end+1]
					if q == '"' {
						if x, e := strconv.Unquote(lit); e == nil {
							l.value = x
						} else {
							l.value = lit[1 : len(lit)-1]
						}
					} else {
						l.value = lit[1 : len(lit)-1]
					}
					l.comment = strings.TrimSpace(v[end+1:])
				} else {
					l.value = v
				}
			} else {
				if i := strings.Index(v, "#"); i >= 0 {
					l.comment = strings.TrimSpace(v[i:])
					v = v[:i]
				}
				l.value = strings.TrimSpace(v)
			}
		}
		out = append(out, l)
	}
	if len(b) == 0 {
		return nil
	}
	return out
}
func (d *Dir) envPath(name string) (string, error) {
	if name == "" {
		name = ".env"
	}
	if filepath.IsAbs(name) { // The API explicitly supports external env files.
		return filepath.Clean(name), nil
	}
	return d.safe(name, true)
}
func (d *Dir) envValues(name string) ([]envLine, error) {
	p, e := d.envPath(name)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, errors.New("cannot read env file")
	}
	return parseEnv(b), nil
}
func (d *Dir) Env(name string) ([]core.EnvVar, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	lines, e := d.envValues(name)
	if e != nil {
		return nil, e
	}
	uses := d.emailUses()
	out := []core.EnvVar{}
	positions := map[string]int{}
	for i, l := range lines {
		if l.key == "" {
			continue
		}
		v := core.EnvVar{Key: l.key, Set: l.value != "", Length: utf8.RuneCountInString(l.value), UsedBy: uses[l.key]}
		if l.key == "DEEPSEEK_API_KEY" {
			v.UsedBy = append(v.UsedBy, "codewhale provider")
		}
		if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1].raw), "#") {
			v.Comment = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i-1].raw), "#"))
		}
		if n, ok := positions[l.key]; ok {
			out[n] = v
		} else {
			positions[l.key] = len(out)
			out = append(out, v)
		}
	}
	return out, nil
}
func (d *Dir) SetEnv(name, key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setEnv(name, key, &value)
}
func (d *Dir) DeleteEnv(name, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setEnv(name, key, nil)
}
func quoteEnv(v string) string {
	if strings.ContainsAny(v, " \t\r\n#\"'\\") {
		return strconv.Quote(v)
	}
	return v
}
func (d *Dir) setEnv(name, key string, value *string) error {
	if !envKey.MatchString(key) || (value != nil && strings.ContainsRune(*value, 0)) {
		return core.ErrInvalid
	}
	p, e := d.envPath(name)
	if e != nil {
		return e
	}
	lines, e := d.envValues(name)
	if e != nil {
		return e
	}
	var out []string
	found := false
	for _, l := range lines {
		if l.key != key {
			out = append(out, l.raw)
			continue
		}
		if value != nil && !found {
			prefix := ""
			if strings.HasPrefix(strings.TrimSpace(l.raw), "export ") {
				prefix = "export "
			}
			s := prefix + key + "=" + quoteEnv(*value)
			if l.comment != "" {
				s += " " + l.comment
			}
			out = append(out, s)
		}
		found = true
	}
	if !found && value != nil {
		out = append(out, key+"="+quoteEnv(*value))
	}
	b := strings.Join(out, "\n")
	if len(out) > 0 {
		b += "\n"
	}
	if e = d.ignoreEnv(p); e != nil {
		return e
	}
	return atomicWrite(p, []byte(b), 0600)
}
func (d *Dir) ignoreEnv(p string) error {
	if !inside(d.root, p) {
		return nil
	}
	_, ig := os.Stat(filepath.Join(d.root, ".gitignore"))
	_, git := os.Stat(filepath.Join(d.root, ".git"))
	if ig != nil && git != nil {
		return nil
	}
	q, e := d.safe(".gitignore", true)
	if e != nil {
		return e
	}
	b, e := os.ReadFile(q)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return errors.New("cannot read gitignore")
	}
	rel, _ := filepath.Rel(d.root, p)
	entry := "/" + filepath.ToSlash(rel)
	for _, l := range strings.Split(string(b), "\n") {
		if l == entry || l == strings.TrimPrefix(entry, "/") {
			return nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte(entry+"\n")...)
	return atomicWrite(q, b, 0644)
}
