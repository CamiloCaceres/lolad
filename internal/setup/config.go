package setup

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// A config value is a string or a list of strings.
type yamlValue struct {
	scalar string
	list   []string
	isList bool
}

func str(s string) yamlValue     { return yamlValue{scalar: s} }
func strs(l ...string) yamlValue { return yamlValue{list: l, isList: true} }
func quote(s string) string      { b, _ := yaml.Marshal(s); return strings.TrimRight(string(b), "\n") }
func (v yamlValue) render(key string) string {
	if !v.isList {
		return key + ": " + quote(v.scalar)
	}
	var b strings.Builder
	b.WriteString(key + ":")
	for _, e := range v.list {
		b.WriteString("\n  - " + quote(e))
	}
	return b.String()
}

var topKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:`)

// mergeConfig sets top-level keys in a YAML document by editing the text, so
// every other key, comment and blank line survives byte for byte (the same
// promise the registry makes for lolabot.yaml). A key that exists is replaced
// where it stands, with its own leading comments kept; a key that does not is
// appended. The result is checked to still parse.
func mergeConfig(src string, set map[string]yamlValue, order []string) (string, error) {
	lines := strings.Split(src, "\n")
	// Top-level key positions.
	type span struct{ start, end int }
	spans := map[string]span{}
	var keys []string
	for i, l := range lines {
		if m := topKey.FindStringSubmatch(l); m != nil {
			keys = append(keys, m[1])
			spans[m[1]] = span{start: i, end: len(lines)}
		}
	}
	// A key's block ends at the next top-level key, minus trailing blank and
	// comment lines, which belong to what follows.
	starts := map[int]string{}
	for k, s := range spans {
		starts[s.start] = k
	}
	for k, s := range spans {
		end := len(lines)
		for j := s.start + 1; j < len(lines); j++ {
			if _, ok := starts[j]; ok {
				end = j
				break
			}
			// a document marker also ends it
			if strings.HasPrefix(lines[j], "---") || strings.HasPrefix(lines[j], "...") {
				end = j
				break
			}
		}
		for end > s.start+1 {
			t := strings.TrimSpace(lines[end-1])
			if t == "" || strings.HasPrefix(t, "#") {
				end--
				continue
			}
			break
		}
		spans[k] = span{s.start, end}
	}
	// Apply from the bottom up so earlier offsets stay valid.
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	var appendix []string
	for _, k := range order {
		v, ok := set[k]
		if !ok {
			continue
		}
		text := v.render(k)
		if s, ok := spans[k]; ok {
			edits = append(edits, edit{s.start, s.end, text})
		} else {
			appendix = append(appendix, text)
		}
	}
	for i := 0; i < len(edits); i++ { // sort descending by start
		for j := i + 1; j < len(edits); j++ {
			if edits[j].start > edits[i].start {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
	for _, e := range edits {
		repl := strings.Split(e.text, "\n")
		lines = append(lines[:e.start], append(repl, lines[e.end:]...)...)
	}
	out := strings.Join(lines, "\n")
	if len(appendix) > 0 {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += strings.Join(appendix, "\n") + "\n"
	}
	var probe map[string]any
	if err := yaml.Unmarshal([]byte(out), &probe); err != nil {
		return "", fmt.Errorf("config would no longer parse: %w", err)
	}
	return out, nil
}

// readConfig returns the file's text and its top-level keys; a missing file
// is empty, not an error.
func readConfig(path string) (string, map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", map[string]any{}, nil
	}
	if err != nil {
		return "", nil, err
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return "", nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return string(b), m, nil
}

func stringList(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case string:
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// writeConfigKeys merges set into the config file at path and reports whether
// it changed.
func writeConfigKeys(path string, set map[string]yamlValue, order []string) (bool, error) {
	src, _, err := readConfig(path)
	if err != nil {
		return false, err
	}
	out, err := mergeConfig(src, set, order)
	if err != nil {
		return false, err
	}
	if out == src {
		return false, nil
	}
	return true, writeAtomic(path, []byte(out), 0o600)
}

// orgDisplay sets user.display (and user.name when the file has none) in an
// org.yaml through the yaml node tree, which keeps comments. A missing file
// is created with the same defaults the policy store would use.
func orgDisplay(path, name, display string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		doc := fmt.Sprintf("tenant: home\nuser:\n  name: %s\n  display: %s\ndefaults:\n  user_to_agents: all\n  agents_to_user: all\n  vertical: true\n  independents: user-only\n", quote(name), quote(display))
		return writeAtomic(path, []byte(doc), 0o600)
	}
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a YAML mapping", path)
	}
	m := root.Content[0]
	var user *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "user" {
			user = m.Content[i+1]
		}
	}
	if user == nil {
		user = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "user"}, user)
	}
	if user.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: user is not a mapping", path)
	}
	set := func(k, v string, onlyIfMissing bool) {
		for i := 0; i+1 < len(user.Content); i += 2 {
			if user.Content[i].Value == k {
				if !onlyIfMissing {
					user.Content[i+1].Value, user.Content[i+1].Tag, user.Content[i+1].Kind = v, "!!str", yaml.ScalarNode
				}
				return
			}
		}
		user.Content = append(user.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	set("name", name, true)
	set("display", display, false)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return writeAtomic(path, out, 0o600)
}
