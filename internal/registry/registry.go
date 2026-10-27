// Package registry discovers configured lolabot instances without changing their identity files.
package registry

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type RemoteConfig struct {
	Listen          string   `yaml:"listen"`
	PublicURL       string   `yaml:"public_url"`
	TailscaleLogins []string `yaml:"tailscale_logins"`
}

type FederationConfig struct {
	Enabled bool   `yaml:"enabled"`
	Display string `yaml:"display"`
	URL     string `yaml:"url"`
}

type Config struct {
	Federation      FederationConfig `yaml:"federation"`
	Remote          RemoteConfig     `yaml:"remote"`
	Listen          string           `yaml:"listen"`
	TmuxBin         string           `yaml:"tmux_bin"`
	TmuxSocket      string           `yaml:"tmux_socket"`
	AgentsDirs      []string         `yaml:"agents_dirs"`
	Agents          []string         `yaml:"agents"`
	LolabotTemplate string           `yaml:"lolabot_template"`
	NewAgentsDir    string           `yaml:"new_agents_dir"`
	ClaudeBin       string           `yaml:"claude_bin"`
	StateDir        string           `yaml:"-"`
}

func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	state := os.Getenv("LOLAD_STATE")
	if state == "" {
		state = filepath.Join(home, ".local/share/lolad")
	}
	return Config{Federation: FederationConfig{Enabled: true}, TmuxSocket: "lolad", Listen: "127.0.0.1:4100", AgentsDirs: []string{filepath.Join(home, "agents")}, LolabotTemplate: filepath.Join(home, "lolabot"), NewAgentsDir: filepath.Join(home, "agents"), StateDir: state}
}
func ConfigPath() string {
	if p := os.Getenv("LOLAD_CONFIG"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config/lolad/config.yaml")
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	if path == "" {
		path = ConfigPath()
	}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	// An explicit config is an opt-in discovery boundary. Only a missing
	// config inherits the built-in search directory.
	c.AgentsDirs = nil
	e = yaml.Unmarshal(b, &c)
	if e != nil {
		return c, e
	}
	if c.Federation.URL == "" {
		c.Federation.URL = c.Remote.PublicURL
	}
	return c, ValidateFederationURL(c.Federation.URL)
}

var invalidSlug = regexp.MustCompile(`[^a-z0-9]+`)

func Slug(dir string) string { return SanitizeSlug(filepath.Base(filepath.Clean(dir))) }

// SanitizeSlug converts a display name to a bounded local agent name.
func SanitizeSlug(value string) string {
	s := strings.Trim(invalidSlug.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func ResolveSlug(dir, display, override string) (string, error) {
	if override != "" {
		slug := strings.ToLower(override)
		if !validSlug.MatchString(slug) {
			return "", fmt.Errorf("%w: invalid runtime.slug %q", core.ErrInvalid, override)
		}
		return slug, nil
	}
	if slug := SanitizeSlug(display); slug != "" {
		return slug, nil
	}
	if slug := Slug(dir); slug != "" {
		return slug, nil
	}
	return "", fmt.Errorf("%w: empty slug", core.ErrInvalid)
}
func Validate(r core.Runtime) error {
	if r.Skills != "" && r.Skills != core.SkillsInherit && r.Skills != core.SkillsBarebones {
		return fmt.Errorf("%w: skills must be inherit or barebones", core.ErrInvalid)
	}
	if r.Permissions != "" && r.Permissions != core.PermBypass && r.Permissions != core.PermDefault {
		return fmt.Errorf("%w: permissions must be bypass or default", core.ErrInvalid)
	}
	if r.Slug != "" && !validSlug.MatchString(strings.ToLower(r.Slug)) {
		return fmt.Errorf("%w: invalid runtime.slug %q", core.ErrInvalid, r.Slug)
	}
	if r.Harness != core.HarnessClaude && r.Harness != core.HarnessCodewhale {
		return fmt.Errorf("%w: unknown harness %q", core.ErrInvalid, r.Harness)
	}
	if r.Rotate != "" {
		t, e := time.Parse("15:04", r.Rotate)
		if e != nil || t.Format("15:04") != r.Rotate {
			return fmt.Errorf("%w: rotate must be HH:MM", core.ErrInvalid)
		}
	}
	for _, t := range r.Triggers {
		n := 0
		for _, v := range []string{t.Cron, t.Mail, t.Webhook} {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("%w: trigger needs exactly one kind", core.ErrInvalid)
		}
		if t.Cron != "" {
			if _, e := cron.ParseStandard(t.Cron); e != nil {
				return fmt.Errorf("cron: %w", e)
			}
		}
		if t.Every != "" {
			d, e := time.ParseDuration(t.Every)
			if e != nil || d <= 0 {
				return fmt.Errorf("%w: invalid trigger every", core.ErrInvalid)
			}
		}
	}
	return nil
}
func ReadAgent(dir string) (core.Agent, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return core.Agent{}, e
	}
	b, e := os.ReadFile(filepath.Join(abs, "lolabot.yaml"))
	if e != nil {
		return core.Agent{}, e
	}
	var f struct {
		Agent struct {
			Name string `yaml:"name"`
			Role string `yaml:"role"`
		} `yaml:"agent"`
		Runtime *core.Runtime `yaml:"runtime"`
	}
	if e = yaml.Unmarshal(b, &f); e != nil {
		return core.Agent{}, e
	}
	if f.Runtime == nil {
		return core.Agent{}, core.ErrNotFound
	}
	if e = Validate(*f.Runtime); e != nil {
		return core.Agent{}, e
	}
	s, e := ResolveSlug(abs, f.Agent.Name, f.Runtime.Slug)
	if e != nil {
		return core.Agent{}, e
	}
	if f.Runtime.Permissions == "" {
		f.Runtime.Permissions = core.PermBypass
	}
	return core.Agent{Name: s, Display: f.Agent.Name, Role: f.Agent.Role, Dir: abs, Runtime: *f.Runtime}, nil
}

type Diff struct {
	Added, Changed, Removed []core.Agent
	Rejected                []core.Event
}
type Option func(*Registry)

// WithEvents surfaces rejected duplicate configuration to the integrator.
func WithEvents(fn func(core.Event)) Option { return func(r *Registry) { r.onEvent = fn } }

type Registry struct {
	mu          sync.RWMutex
	path        string
	config      Config
	agents      []core.Agent
	diagnostics []core.Event
	onEvent     func(core.Event)
}

func New(path string, options ...Option) (*Registry, error) {
	r := &Registry{path: path}
	for _, option := range options {
		option(r)
	}
	_, e := r.Reload()
	return r, e
}
func (r *Registry) Diagnostics() []core.Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]core.Event(nil), r.diagnostics...)
}
func (r *Registry) Config() Config { r.mu.RLock(); defer r.mu.RUnlock(); return r.config }
func (r *Registry) List() []core.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]core.Agent(nil), r.agents...)
}
func (r *Registry) Get(name string) (core.Agent, error) {
	for _, a := range r.List() {
		if a.Name == name {
			return a, nil
		}
	}
	return core.Agent{}, core.ErrNotFound
}
func (r *Registry) Reload() (Diff, error) {
	c, e := LoadConfig(r.path)
	if e != nil {
		return Diff{}, e
	}
	dirs := append([]string(nil), c.Agents...)
	for _, root := range c.AgentsDirs {
		entries, e := os.ReadDir(root)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return Diff{}, e
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				dirs = append(dirs, filepath.Join(root, entry.Name()))
			}
		}
	}
	var rejected []core.Event
	found := map[string]core.Agent{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		abs, e := filepath.Abs(dir)
		if e != nil {
			return Diff{}, e
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		a, e := ReadAgent(abs)
		if os.IsNotExist(e) || e == core.ErrNotFound {
			continue
		}
		if e != nil {
			return Diff{}, fmt.Errorf("%s: %w", dir, e)
		}
		if old, ok := found[a.Name]; ok {
			ev := core.Event{Agent: a.Name, At: time.Now(), Kind: "error", Detail: fmt.Sprintf("duplicate slug %s: retaining %s; rejected %s", a.Name, old.Dir, a.Dir)}
			rejected = append(rejected, ev)
			if r.onEvent != nil {
				r.onEvent(ev)
			}
			continue
		}
		found[a.Name] = a
	}
	agents := make([]core.Agent, 0, len(found))
	for _, a := range found {
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	r.mu.Lock()
	defer r.mu.Unlock()
	old := map[string]core.Agent{}
	for _, a := range r.agents {
		old[a.Name] = a
	}
	d := Diff{Rejected: rejected}
	for _, a := range agents {
		b, ok := old[a.Name]
		if !ok {
			d.Added = append(d.Added, a)
		} else if !reflect.DeepEqual(a, b) {
			d.Changed = append(d.Changed, a)
		}
		delete(old, a.Name)
	}
	for _, a := range old {
		d.Removed = append(d.Removed, a)
	}
	sort.Slice(d.Removed, func(i, j int) bool { return d.Removed[i].Name < d.Removed[j].Name })
	r.config = c
	r.agents = agents
	r.diagnostics = rejected
	return d, nil
}

// WriteRuntime replaces only the byte span occupied by the top-level runtime mapping.
// YAML node positions identify keys; slicing the original text preserves all unrelated bytes.
func WriteRuntime(dir string, r core.Runtime) error {
	if e := Validate(r); e != nil {
		return e
	}
	path := filepath.Join(dir, "lolabot.yaml")
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var doc yaml.Node
	if e = yaml.Unmarshal(b, &doc); e != nil {
		return e
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%w: expected YAML mapping", core.ErrInvalid)
	}
	encoded, e := yaml.Marshal(struct {
		Runtime core.Runtime `yaml:"runtime"`
	}{r})
	if e != nil {
		return e
	}
	newline := []byte("\n")
	if bytes.Contains(b, []byte("\r\n")) {
		newline = []byte("\r\n")
		encoded = bytes.ReplaceAll(encoded, []byte("\n"), newline)
	}
	offsets := []int{0}
	for i, c := range b {
		if c == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	root := doc.Content[0]
	start, end := -1, len(b)
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "runtime" {
			start = offsets[root.Content[i].Line-1]
			if i+2 < len(root.Content) {
				end = offsets[root.Content[i+2].Line-1]
			}
			break
		}
	}
	// Preserve trailing blank/comment lines before the following key (they are not runtime content).
	if start >= 0 {
		lines := bytes.SplitAfter(b[start:end], newline)
		for len(lines) > 0 {
			last := lines[len(lines)-1]
			trim := bytes.TrimSpace(last)
			if len(trim) == 0 || bytes.HasPrefix(last, []byte("#")) {
				end -= len(last)
				lines = lines[:len(lines)-1]
			} else {
				break
			}
		}
		b = append(append(append([]byte(nil), b[:start]...), encoded...), b[end:]...)
	} else {
		if len(b) > 0 && !bytes.HasSuffix(b, newline) {
			b = append(b, newline...)
		}
		b = append(b, encoded...)
	}
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".lolabot-*.yaml")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(info.Mode().Perm()); e == nil {
		_, e = f.Write(b)
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, path)
}

// ValidateFederationURL allows cleartext only on the local machine.
func ValidateFederationURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("%w: federation.url must be an HTTP(S) origin without credentials, path or query", core.ErrInvalid)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())) {
		return fmt.Errorf("%w: federation.url requires HTTPS except on loopback", core.ErrInvalid)
	}
	return nil
}
