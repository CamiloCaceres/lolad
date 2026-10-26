package fake

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Per-agent settings, kept in memory. Values in env are what the agent would
// see; nothing here ever returns one.

type envEntry struct {
	key, value, comment string
	usedBy              []string // besides email accounts, which are derived
}

type fileEntry struct {
	info    core.FileInfo
	content string
}

type agentCfg struct {
	env    []envEntry
	files  map[string]*fileEntry
	skills []core.Skill // Source "agent"
	email  core.EmailSettings
	mod    bool // a mail mod is enabled in .claude/settings.json
	stock  bool // tools/email.sh is lolabot's stock aim-secret one
}

// library is the user's own skills and those of enabled plugins.
var library = []core.Skill{
	{ID: "user:herdr", Name: "herdr", Description: "Control herdr workspaces, tabs and panes from inside it.", Source: "user"},
	{ID: "user:tell-user", Name: "tell-user", Description: "Send the operator a message, a finished deliverable or an approval request.", Source: "user"},
	{ID: "user:memory-search", Name: "memory-search", Description: "Search long-term memory of past sessions: decisions, facts, corrections.", Source: "user"},
	{ID: "user:docs-search", Name: "docs-search", Description: "Look up a function or API in the codebase's generated docs.", Source: "user"},
	{ID: "user:mail-handler", Name: "mail-handler", Description: "Triage an inbox: answer, file, or escalate each new email.", Source: "user"},
	{ID: "plugin:impeccable:impeccable", Name: "impeccable", Description: "Design, critique and polish frontend interfaces.", Source: "plugin:impeccable"},
	{ID: "plugin:figma:figma-use", Name: "figma-use", Description: "Read and write Figma files through the Figma plugin API.", Source: "plugin:figma"},
	{ID: "plugin:productivity:task-management", Name: "task-management", Description: "Keep a TASKS.md current from conversations and trackers.", Source: "plugin:productivity"},
}

func init() {
	for i := range library {
		home := "/Users/milo/.claude/skills/"
		if p, ok := strings.CutPrefix(library[i].Source, "plugin:"); ok {
			home = "/Users/milo/.claude/plugins/cache/" + p + "/skills/"
		}
		library[i].Path = home + library[i].Name
	}
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func kindOf(p string) string {
	switch ext := strings.TrimPrefix(path.Ext(p), "."); ext {
	case "md", "yaml", "toml", "json", "txt":
		return ext
	case "yml":
		return "yaml"
	}
	return "txt"
}

func groupOf(p string) (group, note string) {
	switch {
	case p == "CLAUDE.md" || p == "AGENTS.md":
		return "instructions", "The lolad section between its markers is regenerated; edit outside them."
	case p == "lolabot.yaml":
		return "config", "The runtime block is managed by lolad; edits to it are validated on reload."
	case p == ".claude/settings.json" || p == ".codewhale-home/config.toml":
		return "config", ""
	case strings.HasPrefix(p, "brain/"):
		return "brain", ""
	case strings.HasPrefix(p, "memory/"):
		return "memory", ""
	case strings.HasPrefix(p, "skills/"), strings.HasPrefix(p, ".claude/skills/"), strings.HasPrefix(p, ".codewhale-home/skills/"):
		return "skills", ""
	}
	return "other", ""
}

// cfgOf returns an agent's settings, making a plain lolabot dir's worth the
// first time. Caller holds mu.
func (s *Service) cfgOf(a *core.AgentStatus) *agentCfg {
	if c, ok := s.cfg[a.Name]; ok {
		return c
	}
	c := &agentCfg{files: map[string]*fileEntry{}}
	c.put("CLAUDE.md", claudeMD(a), s.now().Add(-3*24*time.Hour))
	c.put("lolabot.yaml", lolabotYAML(a, nil), s.now().Add(-26*time.Hour))
	c.put("brain/profile.md", "# "+a.Display+"\n\n"+a.Role+" for Camilo Caceres.\n", s.now().Add(-9*24*time.Hour))
	c.put("memory/MEMORY.md", "# Memory\n\n- Prefers short updates over long reports.\n", s.now().Add(-5*time.Hour))
	s.cfg[a.Name] = c
	return c
}

func (c *agentCfg) put(p, content string, at time.Time) {
	g, note := groupOf(p)
	c.files[p] = &fileEntry{info: core.FileInfo{Path: p, Size: int64(len(content)), ModTime: at, Kind: kindOf(p), Group: g, Note: note}, content: content}
}

func (c *agentCfg) envIndex(key string) int {
	return slices.IndexFunc(c.env, func(e envEntry) bool { return e.key == key })
}

func (c *agentCfg) envValue(key string) string {
	if i := c.envIndex(key); i >= 0 {
		return c.env[i].value
	}
	return ""
}

func (c *agentCfg) setEnv(key, value string) {
	if i := c.envIndex(key); i >= 0 {
		c.env[i].value = value
		return
	}
	c.env = append(c.env, envEntry{key: key, value: value})
}

// pending records a change that takes effect at the next launch. Caller holds mu.
func (s *Service) pending(a *core.AgentStatus, why string) {
	if running(a.State) {
		a.PendingRestart = true
		reasons := strings.Split(a.PendingReason, ", ")
		if a.PendingReason == "" {
			reasons = nil
		}
		if !slices.Contains(reasons, why) {
			a.PendingReason = strings.Join(append(reasons, why), ", ")
		}
	}
	s.emit(a.Name, "config", why)
}

func running(st core.State) bool { return st != core.StateStopped && st != core.StateFailed }

// agentFor finds an agent and its settings. Caller holds mu.
func (s *Service) agentFor(name string) (*core.AgentStatus, *agentCfg, error) {
	a := s.find(name)
	if a == nil {
		return nil, nil, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	return a, s.cfgOf(a), nil
}

// ---------------------------------------------------------------- runtime

var rotateRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (s *Service) UpdateRuntime(ctx context.Context, name string, rt core.Runtime) (core.AgentStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(name)
	if a == nil {
		return core.AgentStatus{}, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
	}
	switch {
	case rt.Permissions != "" && rt.Permissions != core.PermBypass && rt.Permissions != core.PermDefault:
		return core.AgentStatus{}, fmt.Errorf("%w: permissions must be bypass or default", core.ErrInvalid)
	case rt.Rotate != "" && !rotateRE.MatchString(rt.Rotate):
		return core.AgentStatus{}, fmt.Errorf("%w: rotate must be a time like 04:00", core.ErrInvalid)
	case rt.Skills != "" && rt.Skills != core.SkillsInherit && rt.Skills != core.SkillsBarebones:
		return core.AgentStatus{}, fmt.Errorf("%w: skills must be inherit or barebones", core.ErrInvalid)
	case strings.ContainsAny(rt.Model, " \t\n"):
		return core.AgentStatus{}, fmt.Errorf("%w: a model id has no spaces", core.ErrInvalid)
	}
	old := a.Runtime
	rt.Slug, rt.Harness = old.Slug, old.Harness
	a.Runtime = rt
	var why []string
	if rt.Model != old.Model {
		why = append(why, "model changed")
	}
	if rt.SkillsMode() != old.SkillsMode() {
		why = append(why, "skills changed")
	}
	if len(why) == 0 {
		why = append(why, "runtime changed")
	}
	for _, w := range why {
		s.pending(a, w)
	}
	c := s.cfgOf(a)
	if f := c.files["lolabot.yaml"]; f != nil {
		c.put("lolabot.yaml", lolabotYAML(a, c), s.now())
	}
	return s.status(a), nil
}

func (s *Service) Models(h core.Harness) []core.ModelOption {
	if h == core.HarnessCodewhale {
		return []core.ModelOption{
			{ID: "deepseek-v4", Label: "DeepSeek V4", Note: "codewhale's default"},
			{ID: "kimi-k3", Label: "Kimi K3"},
			{ID: "qwen4-coder", Label: "Qwen4 Coder"},
		}
	}
	return []core.ModelOption{
		{ID: "claude-opus-5-5", Label: "Opus 5.5", Note: "most capable"},
		{ID: "claude-opus-5-5[1m]", Label: "Opus 5.5 (1M context)", Note: "long sessions"},
		{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5", Note: "fast, capable"},
		{ID: "claude-haiku-5-5", Label: "Haiku 5.5", Note: "quickest, cheapest"},
		{ID: "claude-fable-5-1", Label: "Fable 5.1"},
	}
}

// ---------------------------------------------------------------- skills

func (s *Service) skillsDir(a *core.AgentStatus) string {
	if a.Runtime.Harness == core.HarnessCodewhale {
		return ".codewhale-home/skills/"
	}
	return ".claude/skills/"
}

func (s *Service) Skills(ctx context.Context, name string) ([]core.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(c.skills)
	inherit := a.Runtime.SkillsMode() == core.SkillsInherit
	for _, sk := range library {
		sk.Active = inherit
		out = append(out, sk)
	}
	return out, nil
}

func (s *Service) AddSkill(ctx context.Context, name, from string) (core.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return core.Skill{}, err
	}
	i := slices.IndexFunc(library, func(sk core.Skill) bool { return sk.ID == from })
	if i < 0 {
		return core.Skill{}, fmt.Errorf("skill %q: %w", from, core.ErrNotFound)
	}
	src := library[i]
	if slices.ContainsFunc(c.skills, func(sk core.Skill) bool { return sk.Name == src.Name }) {
		return core.Skill{}, fmt.Errorf("%s already has a skill named %q: %w", a.Display, src.Name, core.ErrExists)
	}
	rel := s.skillsDir(a) + src.Name
	sk := core.Skill{ID: "agent:" + src.Name, Name: src.Name, Description: src.Description, Source: "agent",
		Path: a.Dir + "/" + rel, FilePath: rel + "/SKILL.md", Active: true}
	c.skills = append(c.skills, sk)
	c.put(sk.FilePath, skillMD(sk.Name, sk.Description), s.now())
	s.pending(a, "skills changed")
	return sk, nil
}

func (s *Service) RemoveSkill(ctx context.Context, name, skillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(c.skills, func(sk core.Skill) bool { return sk.ID == skillID })
	if i < 0 {
		if strings.HasPrefix(skillID, "agent:") {
			return fmt.Errorf("skill %q: %w", skillID, core.ErrNotFound)
		}
		return fmt.Errorf("%w: only the agent's own skills can be removed", core.ErrInvalid)
	}
	for p := range c.files {
		if strings.HasPrefix(p, strings.TrimSuffix(c.skills[i].FilePath, "SKILL.md")) {
			delete(c.files, p)
		}
	}
	c.skills = slices.Delete(c.skills, i, i+1)
	s.pending(a, "skills changed")
	return nil
}

// ---------------------------------------------------------------- env

func (s *Service) Env(ctx context.Context, name string) ([]core.EnvVar, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.agentFor(name)
	if err != nil {
		return nil, err
	}
	out := make([]core.EnvVar, 0, len(c.env))
	for _, e := range c.env {
		v := core.EnvVar{Key: e.key, Set: e.value != "", Length: utf8.RuneCountInString(e.value), Comment: e.comment, UsedBy: slices.Clone(e.usedBy)}
		for _, acc := range c.email.Accounts {
			if acc.PasswordKey == e.key {
				v.UsedBy = append(v.UsedBy, "email: "+acc.Address)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) SetEnv(ctx context.Context, name, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return err
	}
	if !envKeyRE.MatchString(key) {
		return fmt.Errorf("%w: %q is not a valid name (letters, digits and _, not starting with a digit)", core.ErrInvalid, key)
	}
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("%w: a value is one line", core.ErrInvalid)
	}
	c.setEnv(key, value)
	s.pending(a, ".env changed")
	return nil
}

func (s *Service) DeleteEnv(ctx context.Context, name, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return err
	}
	i := c.envIndex(key)
	if i < 0 {
		return fmt.Errorf("%s: %w", key, core.ErrNotFound)
	}
	c.env = slices.Delete(c.env, i, i+1)
	s.pending(a, ".env changed")
	return nil
}

// ---------------------------------------------------------------- files

var groupOrder = []string{"instructions", "config", "brain", "memory", "skills", "other"}

func (s *Service) Files(ctx context.Context, name string) ([]core.FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.agentFor(name)
	if err != nil {
		return nil, err
	}
	out := make([]core.FileInfo, 0, len(c.files))
	for _, f := range c.files {
		out = append(out, f.info)
	}
	slices.SortFunc(out, func(a, b core.FileInfo) int {
		return cmp.Or(cmp.Compare(slices.Index(groupOrder, a.Group), slices.Index(groupOrder, b.Group)), strings.Compare(a.Path, b.Path))
	})
	return out, nil
}

func (s *Service) ReadFile(ctx context.Context, name, p string) (core.FileContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.agentFor(name)
	if err != nil {
		return core.FileContent{}, err
	}
	f, ok := c.files[p]
	if !ok {
		return core.FileContent{}, fmt.Errorf("%s: %w", p, core.ErrNotFound)
	}
	return core.FileContent{FileInfo: f.info, Content: f.content, Hash: hashOf(f.content)}, nil
}

func (s *Service) WriteFile(ctx context.Context, name, p, content, baseHash string) (core.FileContent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.agentFor(name)
	if err != nil {
		return core.FileContent{}, err
	}
	f, ok := c.files[p]
	if !ok {
		return core.FileContent{}, fmt.Errorf("%s: %w", p, core.ErrNotFound)
	}
	if hashOf(f.content) != baseHash {
		return core.FileContent{}, fmt.Errorf("%s: %w", p, core.ErrConflict)
	}
	c.put(p, content, s.now())
	return core.FileContent{FileInfo: c.files[p].info, Content: content, Hash: hashOf(content)}, nil
}

// ---------------------------------------------------------------- email

func passwordKey(configKey string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(configKey) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "EMAIL_PASSWORD_" + b.String()
}

// emailView is what Email answers: passwords reduced to set/unset. Caller holds mu.
func (s *Service) emailView(a *core.AgentStatus, c *agentCfg) core.EmailSettings {
	e := c.email
	e.Accounts = slices.Clone(e.Accounts)
	e.OperatorEmails = slices.Clone(e.OperatorEmails)
	e.Notes = slices.Clone(e.Notes)
	for i := range e.Accounts {
		e.Accounts[i].Password = ""
		e.Accounts[i].PasswordSet = c.envValue(e.Accounts[i].PasswordKey) != ""
	}
	e.Watch, e.WatchAccount, e.Every = "none", "", ""
	if c.mod {
		e.Watch = "mod"
	}
	for _, t := range a.Runtime.Triggers {
		if t.Mail != "" {
			e.Watch, e.WatchAccount, e.Every = "lolad", t.Mail, cmp.Or(t.Every, "2m")
			if c.mod {
				e.Notes = append(e.Notes, "The agent's mail mod also watches this inbox, so each new email would reach it twice. Turn one of them off.")
			}
		}
	}
	return e
}

func (s *Service) Email(ctx context.Context, name string) (core.EmailSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return core.EmailSettings{}, err
	}
	return s.emailView(a, c), nil
}

func (s *Service) SaveEmail(ctx context.Context, name string, in core.EmailSettings) (core.EmailSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, c, err := s.agentFor(name)
	if err != nil {
		return core.EmailSettings{}, err
	}
	if !c.email.Supported {
		return core.EmailSettings{}, fmt.Errorf("%w: %s has no tools/email.sh", core.ErrInvalid, a.Display)
	}
	seen := map[string]bool{}
	accounts := make([]core.EmailAccount, 0, len(in.Accounts))
	for _, acc := range in.Accounts {
		acc.Address = strings.TrimSpace(acc.Address)
		local, domain, ok := strings.Cut(acc.Address, "@")
		if !ok || local == "" || !strings.Contains(domain, ".") {
			return core.EmailSettings{}, fmt.Errorf("%w: %q is not an email address", core.ErrInvalid, acc.Address)
		}
		if seen[strings.ToLower(acc.Address)] {
			return core.EmailSettings{}, fmt.Errorf("%w: %s is listed twice", core.ErrInvalid, acc.Address)
		}
		seen[strings.ToLower(acc.Address)] = true
		if acc.IMAPServer == "" || acc.SMTPServer == "" {
			return core.EmailSettings{}, fmt.Errorf("%w: %s needs an IMAP and an SMTP server", core.ErrInvalid, acc.Address)
		}
		acc.ConfigKey = cmp.Or(acc.ConfigKey, strings.ToLower(local))
		acc.IMAPPort = cmp.Or(acc.IMAPPort, 993)
		acc.SMTPPort = cmp.Or(acc.SMTPPort, 465)
		acc.Username = cmp.Or(acc.Username, acc.Address)
		acc.PasswordKey = cmp.Or(acc.PasswordKey, passwordKey(acc.ConfigKey))
		if acc.Password != "" {
			c.setEnv(acc.PasswordKey, acc.Password)
		}
		acc.Password, acc.PasswordSet = "", false
		accounts = append(accounts, acc)
	}
	var ops []string
	for _, o := range in.OperatorEmails {
		if o = strings.TrimSpace(o); o != "" {
			if !strings.Contains(o, "@") {
				return core.EmailSettings{}, fmt.Errorf("%w: %q is not an email address", core.ErrInvalid, o)
			}
			ops = append(ops, o)
		}
	}
	triggers := slices.DeleteFunc(slices.Clone(a.Runtime.Triggers), func(t core.Trigger) bool { return t.Mail != "" })
	switch in.Watch {
	case "lolad":
		acct := cmp.Or(in.WatchAccount, func() string {
			if len(accounts) > 0 {
				return accounts[0].Address
			}
			return ""
		}())
		if !seen[strings.ToLower(acct)] {
			return core.EmailSettings{}, fmt.Errorf("%w: pick an account for lolad to watch", core.ErrInvalid)
		}
		if in.Every != "" {
			if d, err := time.ParseDuration(in.Every); err != nil || d < 30*time.Second {
				return core.EmailSettings{}, fmt.Errorf("%w: interval must be a duration of at least 30s, like 2m", core.ErrInvalid)
			}
		}
		triggers = append(triggers, core.Trigger{Mail: acct, Every: in.Every, Prompt: "New email:"})
	case "", "none", "mod":
	default:
		return core.EmailSettings{}, fmt.Errorf("%w: watch must be none or lolad", core.ErrInvalid)
	}
	c.email.Accounts, c.email.OperatorEmails = accounts, ops
	a.Runtime.Triggers = triggers
	if c.stock {
		c.stock = false
		c.email.Notes = []string{"tools/email.sh was updated to read passwords from .env; the original is kept as tools/email.sh.lolad-orig."}
	}
	s.pending(a, "email changed")
	return s.emailView(a, c), nil
}

func (s *Service) TestEmail(ctx context.Context, name, address string) (core.EmailTest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, c, err := s.agentFor(name)
	if err != nil {
		return core.EmailTest{}, err
	}
	i := slices.IndexFunc(c.email.Accounts, func(a core.EmailAccount) bool { return strings.EqualFold(a.Address, address) })
	if i < 0 {
		return core.EmailTest{}, fmt.Errorf("account %q: %w", address, core.ErrNotFound)
	}
	acc := c.email.Accounts[i]
	pw := c.envValue(acc.PasswordKey)
	switch {
	case pw == "":
		return core.EmailTest{IMAP: "not tried: no password set", SMTP: "not tried: no password set",
			Detail: acc.PasswordKey + " is empty in .env. Set the account's password, then test again."}, nil
	case strings.HasPrefix(pw, "bad"):
		return core.EmailTest{IMAP: "ok", SMTP: "535 5.7.8 Authentication failed",
			Detail: "Logged in to " + acc.IMAPServer + " but " + acc.SMTPServer + " refused the login. Some providers need an app password for SMTP."}, nil
	}
	return core.EmailTest{OK: true, IMAP: "ok", SMTP: "ok", Detail: fmt.Sprintf("Logged in to %s:%d and %s:%d as %s.", acc.IMAPServer, acc.IMAPPort, acc.SMTPServer, acc.SMTPPort, acc.Username)}, nil
}

// ---------------------------------------------------------------- seed

func (s *Service) seedSettings() {
	now := s.now()
	h := time.Hour
	if a := s.find("lola"); a != nil {
		c := s.cfgOf(a)
		c.env = []envEntry{
			{key: "LINEAR_API_KEY", value: strings.Repeat("l", 48), comment: "Linear, read/write on the Lumio workspace"},
			{key: "GITHUB_TOKEN", value: strings.Repeat("g", 40), usedBy: []string{"tools/gh.sh"}},
			{key: "SLACK_WEBHOOK_URL", value: "", comment: "#ops alerts; not set up yet"},
		}
		c.put("AGENTS.md", "# Agents\n\nSee CLAUDE.md.\n", now.Add(-12*24*h))
		c.put("brain/company.md", companyMD, now.Add(-2*24*h))
		c.put("brain/people.md", "# People\n\n| Name | Role | Notes |\n|---|---|---|\n| Mario | Investor | Monthly call, first Tuesday |\n| Laura | Legal | Reviews pricing changes |\n", now.Add(-6*24*h))
		c.put("memory/2026-10-24.md", "# 24 Oct\n\n- Founders tier on hold until legal reviews.\n- Newsletter moves to every two weeks.\n", now.Add(-20*h))
		c.put("skills/morning-brief.md", "# Morning brief\n\n1. Read overnight mail.\n2. Summarise what changed per team.\n3. List what needs Milo today, **asks first**.\n", now.Add(-8*24*h))
		c.put(".claude/settings.json", "{\n  \"enabledPlugins\": {}\n}\n", now.Add(-30*24*h))
		c.skills = []core.Skill{{ID: "agent:brief-writer", Name: "brief-writer", Description: "Write the morning brief from overnight mail and events.", Source: "agent",
			Path: a.Dir + "/.claude/skills/brief-writer", FilePath: ".claude/skills/brief-writer/SKILL.md", Active: true}}
		c.put(".claude/skills/brief-writer/SKILL.md", skillMD("brief-writer", c.skills[0].Description), now.Add(-8*24*h))
		c.email = core.EmailSettings{Supported: true}
		a.PendingRestart, a.PendingReason = true, ".env changed"
	}
	if a := s.find("ultron"); a != nil {
		c := s.cfgOf(a)
		c.mod = true
		c.env = []envEntry{
			{key: "EMAIL_PASSWORD_ULTRON", value: strings.Repeat("u", 24)},
			{key: "GOOGLE_CALENDAR_TOKEN", value: strings.Repeat("c", 163), comment: "OAuth refresh token for milo's calendar", usedBy: []string{"tools/calendar.py"}},
			{key: "OPENWEATHER_KEY", value: strings.Repeat("w", 32)},
		}
		c.email = core.EmailSettings{Supported: true, OperatorEmails: []string{"you@example.com"},
			Accounts: []core.EmailAccount{{Address: "ultron@mail.example.com", ConfigKey: "ultron", IMAPServer: "imap.migadu.com", IMAPPort: 993,
				SMTPServer: "smtp.migadu.com", SMTPPort: 465, Username: "ultron@mail.example.com", PasswordKey: "EMAIL_PASSWORD_ULTRON"}}}
		c.put(".claude/settings.json", "{\n  \"enabledPlugins\": {\n    \"ultron-mail@local\": true\n  }\n}\n", now.Add(-10*24*h))
		c.put("brain/credentials.yaml", "email_accounts:\n  ultron:\n    username: ultron@mail.example.com\n    password_secret: EMAIL_PASSWORD_ULTRON\n", now.Add(-10*24*h))
		c.put("brain/routines.md", "# Routines\n\n- 07:30 plan the day from the calendar.\n- Gym on Tuesdays and Thursdays.\n", now.Add(-4*24*h))
		c.skills = []core.Skill{
			{ID: "agent:mail-handler", Name: "mail-handler", Description: "Triage an inbox: answer, file, or escalate each new email.", Source: "agent",
				Path: a.Dir + "/.claude/skills/mail-handler", FilePath: ".claude/skills/mail-handler/SKILL.md", Active: true},
			{ID: "agent:calendar", Name: "calendar", Description: "Read and change milo's Google Calendar.", Source: "agent",
				Path: a.Dir + "/.claude/skills/calendar", FilePath: ".claude/skills/calendar/SKILL.md", Active: true},
		}
		for _, sk := range c.skills {
			c.put(sk.FilePath, skillMD(sk.Name, sk.Description), now.Add(-10*24*h))
		}
		c.put("lolabot.yaml", lolabotYAML(a, c), now.Add(-26*h))
	}
	if a := s.find("iris"); a != nil {
		c := s.cfgOf(a)
		c.stock = true
		c.env = []envEntry{{key: "NOTION_TOKEN", value: strings.Repeat("n", 50)}}
		c.email = core.EmailSettings{Supported: true, Notes: []string{"tools/email.sh is lolabot's stock version, which reads passwords from aim-secret (AI Maestro's vault). Saving here updates it to read .env and keeps the original as tools/email.sh.lolad-orig."}}
		c.skills = []core.Skill{{ID: "agent:mail-handler", Name: "mail-handler", Description: "Triage an inbox: answer, file, or escalate each new email.", Source: "agent",
			Path: a.Dir + "/.claude/skills/mail-handler", FilePath: ".claude/skills/mail-handler/SKILL.md", Active: true}}
		c.put(c.skills[0].FilePath, skillMD("mail-handler", c.skills[0].Description), now.Add(-2*24*h))
	}
	if a := s.find("ledger"); a != nil {
		c := s.cfgOf(a)
		c.env = []envEntry{{key: "EMAIL_PASSWORD_OPS", value: "bad-" + strings.Repeat("o", 14)}, {key: "XERO_CLIENT_SECRET", value: strings.Repeat("x", 36)}}
		c.email = core.EmailSettings{Supported: true, OperatorEmails: []string{"you@example.com"},
			Accounts: []core.EmailAccount{{Address: "ops@example.com", ConfigKey: "ops", IMAPServer: "imap.gmail.com", IMAPPort: 993,
				SMTPServer: "smtp.gmail.com", SMTPPort: 465, Username: "ops@example.com", PasswordKey: "EMAIL_PASSWORD_OPS"}}}
	}
	for _, n := range []string{"forge", "relay", "quill"} {
		if a := s.find(n); a != nil {
			c := s.cfgOf(a)
			c.email = core.EmailSettings{Supported: true}
			c.env = []envEntry{{key: "GITHUB_TOKEN", value: strings.Repeat("g", 40)}}
		}
	}
	// patch, juno and scout are codewhale agents without lolabot's email tool.
	for _, n := range []string{"patch", "juno", "scout"} {
		if a := s.find(n); a != nil {
			c := s.cfgOf(a)
			delete(c.files, "CLAUDE.md")
			c.put("AGENTS.md", claudeMD(a), now.Add(-3*24*h))
			c.put(".codewhale-home/config.toml", "[model]\nname = \""+cmp.Or(a.Runtime.Model, "deepseek-v4")+"\"\n\n[skills]\nscan_codewhale_only = false\n", now.Add(-7*24*h))
		}
	}
}

func skillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\n" + desc + "\n\n## Steps\n\n1. Read the request.\n2. Do the work.\n3. Report back by mail.\n"
}

func claudeMD(a *core.AgentStatus) string {
	return "# " + a.Display + "\n\nYou are " + a.Display + ", " + a.Role + " for Camilo Caceres (milo).\n\n" +
		"## How you work\n\n- Read `brain/` at the start of every session.\n- Keep `memory/` current: one file per day.\n- Ask milo only when a decision is his.\n\n" +
		"<!-- lolad:begin -->\n## Mail (managed by lolad)\n\nYou are `" + a.Name + "` on lolad. Read new mail with `lolad msg inbox`, answer with `lolad msg reply <id>`.\n" +
		"Ask milo with `lolad msg ask-user`; it blocks until he answers.\n<!-- lolad:end -->\n"
}

func lolabotYAML(a *core.AgentStatus, c *agentCfg) string {
	rt := a.Runtime
	var b strings.Builder
	fmt.Fprintf(&b, "agent:\n  name: %s\n  role: %s\n\n", a.Display, a.Role)
	if c != nil && len(c.email.Accounts) > 0 {
		b.WriteString("email:\n  accounts:\n")
		for _, acc := range c.email.Accounts {
			fmt.Fprintf(&b, "    - address: %s\n      config_key: %s\n      imap_server: %s\n      imap_port: %d\n      smtp_server: %s\n      smtp_port: %d\n",
				acc.Address, acc.ConfigKey, acc.IMAPServer, acc.IMAPPort, acc.SMTPServer, acc.SMTPPort)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "runtime:\n  harness: %s\n", rt.Harness)
	if rt.Model != "" {
		fmt.Fprintf(&b, "  model: %s\n", rt.Model)
	}
	if rt.Permissions != "" {
		fmt.Fprintf(&b, "  permissions: %s\n", rt.Permissions)
	}
	if rt.Rotate != "" {
		fmt.Fprintf(&b, "  rotate: \"%s\"\n", rt.Rotate)
	}
	if rt.Skills != "" {
		fmt.Fprintf(&b, "  skills: %s\n", rt.Skills)
	}
	return b.String()
}

const companyMD = `# Lumio

Lumio sells invoicing software to small agencies.

## Teams

- **Engineering** (Forge): billing, the API, the web app.
- **Growth** (Juno): pricing page, newsletter, research.

## This quarter

1. Ship the new pricing page.
2. Invoice CSV export.
3. Move the newsletter to every two weeks.

> Keep milo's inbox short: one brief a day, asks only when it's his call.

` + "```sh\nlolad msg inbox\n```\n"
