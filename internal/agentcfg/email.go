package agentcfg

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

//go:embed assets/email.sh
var emailScript []byte

//go:embed assets/email-aim-secret.sh
var stockEmailScript []byte

func stockEmail(b []byte) bool {
	return bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(stockEmailScript))
}

//go:embed assets/env_secret.py
var envScript []byte

func (d *Dir) readYAML(rel string) ([]byte, *yaml.Node, error) {
	p, e := d.safe(rel, true)
	if e != nil {
		return nil, nil, e
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		b = nil
	} else if e != nil {
		return nil, nil, errors.New("cannot read configuration")
	}
	var n yaml.Node
	if len(b) > 0 {
		if yaml.Unmarshal(b, &n) != nil {
			return nil, nil, errors.New("invalid configuration YAML")
		}
	} else {
		n = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		return nil, nil, core.ErrInvalid
	}
	return b, &n, nil
}
func nodeGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		n = n.Content[0]
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func nodeSet(n *yaml.Node, key string, v *yaml.Node) {
	if n.Kind == yaml.DocumentNode {
		n = n.Content[0]
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if v == nil {
				n.Content = append(n.Content[:i], n.Content[i+2:]...)
			} else {
				n.Content[i+1] = v
			}
			return
		}
	}
	if v != nil {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	}
}
func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
func decode(n *yaml.Node, v any) {
	if n != nil {
		_ = n.Decode(v)
	}
}

// blockSpan uses YAML key positions; comments belonging to neighboring fields remain untouched.
func blockSpan(b []byte, n *yaml.Node, key string) (int, int) {
	root := n.Content[0]
	lines := strings.SplitAfter(string(b), "\n")
	offset := func(line int) int {
		x := 0
		for i := 0; i < line-1 && i < len(lines); i++ {
			x += len(lines[i])
		}
		return x
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			start := offset(root.Content[i].Line)
			end := len(b)
			if i+2 < len(root.Content) {
				end = offset(root.Content[i+2].Line)
			} // retain trailing blank/comment lines outside the block
			for end > start {
				prev := strings.LastIndex(string(b[:end-1]), "\n") + 1
				l := strings.TrimSpace(string(b[prev:end]))
				if l == "" || strings.HasPrefix(l, "#") {
					end = prev
				} else {
					break
				}
			}
			return start, end
		}
	}
	return len(b), len(b)
}
func (d *Dir) emailUses() map[string][]string {
	out := map[string][]string{}
	addresses := map[string]string{}
	_, config, e := d.readYAML("lolabot.yaml")
	if e == nil {
		var cfg emailYAML
		decode(nodeGet(config, "email"), &cfg)
		for _, a := range cfg.Accounts {
			key := a.ConfigKey
			if key == "" {
				key, _, _ = strings.Cut(a.Address, "@")
			}
			addresses[key] = a.Address
		}
	}
	_, n, e := d.readYAML("brain/credentials.yaml")
	if e != nil {
		return out
	}
	acc := nodeGet(n, "email_accounts")
	if acc == nil || acc.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(acc.Content); i += 2 {
		var key string
		decode(nodeGet(acc.Content[i+1], "password_secret"), &key)
		if key != "" {
			label := addresses[acc.Content[i].Value]
			if label == "" {
				label = acc.Content[i].Value
			}
			out[key] = append(out[key], "email: "+label)
		}
	}
	return out
}

type emailAccountYAML struct {
	Address    string `yaml:"address"`
	ConfigKey  string `yaml:"config_key"`
	IMAPServer string `yaml:"imap_server"`
	IMAPPort   int    `yaml:"imap_port"`
	SMTPServer string `yaml:"smtp_server"`
	SMTPPort   int    `yaml:"smtp_port"`
}
type emailYAML struct {
	Accounts       []emailAccountYAML `yaml:"accounts"`
	OperatorEmails []string           `yaml:"operator_emails"`
}

func defaultAccount(a core.EmailAccount) core.EmailAccount {
	if a.ConfigKey == "" {
		a.ConfigKey, _, _ = strings.Cut(a.Address, "@")
	}
	if a.Username == "" {
		a.Username = a.Address
	}
	if a.IMAPPort == 0 {
		a.IMAPPort = 993
	}
	if a.SMTPPort == 0 {
		a.SMTPPort = 465
	}
	if a.PasswordKey == "" {
		a.PasswordKey = "EMAIL_PASSWORD_" + regexp.MustCompile(`[^A-Z0-9_]`).ReplaceAllString(strings.ToUpper(a.ConfigKey), "_")
	}
	return a
}
func (d *Dir) Email(envFile string) (core.EmailSettings, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.email(envFile)
}
func (d *Dir) email(envFile string) (core.EmailSettings, error) {
	out := core.EmailSettings{Watch: "none", Accounts: []core.EmailAccount{}, OperatorEmails: []string{}}
	p, e := d.safe("tools/email.sh", false)
	if e == nil {
		b, _ := os.ReadFile(p)
		out.Supported = true
		if stockEmail(b) {
			out.Notes = append(out.Notes, "email.sh uses aim-secret; lolad will update it to read .env")
		}
	}
	_, n, e := d.readYAML("lolabot.yaml")
	if e != nil {
		return out, e
	}
	var cfg emailYAML
	decode(nodeGet(n, "email"), &cfg)
	if cfg.OperatorEmails != nil {
		out.OperatorEmails = cfg.OperatorEmails
	}
	_, creds, e := d.readYAML("brain/credentials.yaml")
	if e != nil && !errors.Is(e, core.ErrNotFound) {
		return out, e
	}
	values, e := d.envValues(envFile)
	if e != nil {
		return out, e
	}
	env := map[string]string{}
	for _, l := range values {
		env[l.key] = l.value
	}
	for _, a := range cfg.Accounts {
		v := defaultAccount(core.EmailAccount{Address: a.Address, ConfigKey: a.ConfigKey, IMAPServer: a.IMAPServer, IMAPPort: a.IMAPPort, SMTPServer: a.SMTPServer, SMTPPort: a.SMTPPort})
		if creds != nil {
			c := nodeGet(nodeGet(creds, "email_accounts"), v.ConfigKey)
			decode(nodeGet(c, "username"), &v.Username)
			decode(nodeGet(c, "password_secret"), &v.PasswordKey)
			var plain string
			decode(nodeGet(c, "password"), &plain)
			v.PasswordSet = plain != ""
		}
		v.PasswordSet = v.PasswordSet || env[v.PasswordKey] != ""
		out.Accounts = append(out.Accounts, v)
	}
	p, e = d.safe(".claude/settings.json", false)
	if e == nil {
		b, _ := os.ReadFile(p)
		var settings struct {
			Enabled map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(b, &settings) == nil {
			for k, v := range settings.Enabled {
				if v && strings.Contains(strings.ToLower(k), "mail") {
					out.Watch = "mod"
				}
			}
		}
	}
	return out, nil
}
func (d *Dir) SaveEmail(envFile string, s core.EmailSettings) (core.EmailSettings, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cfg := emailYAML{OperatorEmails: s.OperatorEmails}
	seen := map[string]bool{}
	for i, a := range s.Accounts {
		a = defaultAccount(a)
		if a.Address == "" || a.ConfigKey == "" || !envKey.MatchString(a.PasswordKey) || seen[a.ConfigKey] || a.IMAPPort < 1 || a.IMAPPort > 65535 || a.SMTPPort < 1 || a.SMTPPort > 65535 {
			return core.EmailSettings{}, core.ErrInvalid
		}
		seen[a.ConfigKey] = true
		s.Accounts[i] = a
		cfg.Accounts = append(cfg.Accounts, emailAccountYAML{a.Address, a.ConfigKey, a.IMAPServer, a.IMAPPort, a.SMTPServer, a.SMTPPort})
	}
	b, n, e := d.readYAML("lolabot.yaml")
	if e != nil {
		return core.EmailSettings{}, e
	}
	_, creds, e := d.readYAML("brain/credentials.yaml")
	if errors.Is(e, core.ErrNotFound) {
		creds = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else if e != nil {
		return core.EmailSettings{}, e
	}
	accounts := nodeGet(creds, "email_accounts")
	if accounts == nil {
		accounts = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		nodeSet(creds, "email_accounts", accounts)
	}
	if accounts.Kind != yaml.MappingNode {
		return core.EmailSettings{}, core.ErrInvalid
	}
	// This file is reserialized; unrelated keys survive, formatting/comments may change.
	keep := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, a := range s.Accounts {
		c := nodeGet(accounts, a.ConfigKey)
		if c == nil {
			c = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if c.Kind != yaml.MappingNode {
			return core.EmailSettings{}, core.ErrInvalid
		}
		nodeSet(c, "username", scalar(a.Username))
		nodeSet(c, "password_secret", scalar(a.PasswordKey))
		if a.Password != "" {
			if e = d.setEnv(envFile, a.PasswordKey, &a.Password); e != nil {
				return core.EmailSettings{}, e
			}
		} else if old := nodeGet(c, "password"); old != nil && old.Value != "" {
			v := old.Value
			existing, readErr := d.envValues(envFile)
			if readErr != nil {
				return core.EmailSettings{}, readErr
			}
			present := false
			for _, l := range existing {
				if l.key == a.PasswordKey {
					present = l.value != ""
				}
			}
			if !present {
				if e = d.setEnv(envFile, a.PasswordKey, &v); e != nil {
					return core.EmailSettings{}, e
				}
			}
		}
		nodeSet(c, "password", nil)
		nodeSet(keep, a.ConfigKey, c)
	}
	nodeSet(creds, "email_accounts", keep)
	cb, e := yaml.Marshal(creds)
	if e != nil {
		return core.EmailSettings{}, core.ErrInvalid
	}
	bp, e := d.safe("brain", true)
	if e != nil {
		return core.EmailSettings{}, e
	}
	if e = os.MkdirAll(bp, 0700); e != nil {
		return core.EmailSettings{}, errors.New("cannot create credentials directory")
	}
	cp, e := d.safe("brain/credentials.yaml", true)
	if e != nil {
		return core.EmailSettings{}, e
	}
	if e = atomicWrite(cp, cb, 0600); e != nil {
		return core.EmailSettings{}, e
	}
	block, e := yaml.Marshal(map[string]any{"email": cfg})
	if e != nil {
		return core.EmailSettings{}, core.ErrInvalid
	}
	start, end := blockSpan(b, n, "email")
	nb := append([]byte{}, b[:start]...)
	if len(nb) > 0 && nb[len(nb)-1] != '\n' {
		nb = append(nb, '\n')
	}
	nb = append(nb, block...)
	nb = append(nb, b[end:]...)
	p, e := d.safe("lolabot.yaml", true)
	if e != nil {
		return core.EmailSettings{}, e
	}
	mode := os.FileMode(0644)
	if st, e := os.Stat(p); e == nil {
		mode = st.Mode()
	}
	if e = atomicWrite(p, nb, mode); e != nil {
		return core.EmailSettings{}, e
	}
	upgraded, e := d.upgradeEmail()
	if e != nil {
		return core.EmailSettings{}, e
	}
	out, e := d.email(envFile)
	if upgraded {
		out.Notes = append(out.Notes, "Upgraded stock email.sh and env_secret.py to read .env, including escaped quoted values; originals kept as .lolad-orig")
	}
	out.Notes = append(out.Notes, "credentials.yaml is reserialized; other top-level keys are preserved, formatting and comments may change")
	return out, e
}
func (d *Dir) upgradeEmail() (bool, error) {
	p, e := d.safe("tools/email.sh", false)
	if errors.Is(e, core.ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return false, errors.New("cannot read email tool")
	}
	if !stockEmail(b) {
		return false, nil
	}
	for _, f := range []struct {
		rel string
		b   []byte
	}{{"tools/email.sh", emailScript}, {"tools/env_secret.py", envScript}} {
		p, e := d.safe(f.rel, true)
		if e != nil {
			return false, e
		}
		old, e := os.ReadFile(p)
		if e == nil {
			backup, e := d.safe(f.rel+".lolad-orig", true)
			if e != nil {
				return false, e
			}
			if _, e = os.Stat(backup); errors.Is(e, os.ErrNotExist) {
				st, _ := os.Stat(p)
				if e = atomicWrite(backup, old, st.Mode()); e != nil {
					return false, e
				}
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return false, errors.New("cannot read email tool")
		}
		if e = atomicWrite(p, f.b, 0755); e != nil {
			return false, e
		}
	}
	return true, nil
}
func (d *Dir) EmailPassword(envFile, address string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, e := d.email(envFile)
	if e != nil {
		return "", e
	}
	for _, a := range s.Accounts {
		if a.Address == address {
			lines, e := d.envValues(envFile)
			if e != nil {
				return "", e
			}
			v := ""
			for _, l := range lines {
				if l.key == a.PasswordKey {
					v = l.value
				}
			}
			if v != "" {
				return v, nil
			}
			_, n, e := d.readYAML("brain/credentials.yaml")
			if e != nil {
				return "", e
			}
			decode(nodeGet(nodeGet(nodeGet(n, "email_accounts"), a.ConfigKey), "password"), &v)
			return v, nil
		}
	}
	return "", core.ErrNotFound
}
