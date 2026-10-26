package app

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/agentcfg"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/harness"
	"github.com/CamiloCaceres/lolad/internal/registry"
)

func (a *App) settings(name string) (core.Agent, *agentcfg.Dir, error) {
	for _, v := range a.Registry.List() {
		if v.Name == name {
			return v, agentcfg.New(v.Dir, v.Runtime.Harness, agentcfg.Options{}), nil
		}
	}
	return core.Agent{}, nil, core.ErrNotFound
}

func (a *App) UpdateRuntime(ctx context.Context, name string, rt core.Runtime) (core.AgentStatus, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, _, e := a.settings(name)
	if e != nil {
		return core.AgentStatus{}, e
	}
	if rt.Slug != v.Runtime.Slug || rt.Harness != v.Runtime.Harness {
		return core.AgentStatus{}, fmt.Errorf("%w: slug and harness cannot be changed here", core.ErrInvalid)
	}
	if e = registry.Validate(rt); e != nil {
		return core.AgentStatus{}, e
	}
	if e = registry.WriteRuntime(v.Dir, rt); e != nil {
		return core.AgentStatus{}, e
	}
	if e = a.Supervisor.Reload(ctx); e != nil {
		return core.AgentStatus{}, e
	}
	return a.Get(ctx, name)
}
func (a *App) Models(h core.Harness) []core.ModelOption { return harness.Models(h) }
func (a *App) Skills(ctx context.Context, name string) ([]core.Skill, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return nil, e
	}
	return d.Skills(v.Runtime.SkillsMode())
}
func (a *App) AddSkill(ctx context.Context, name, from string) (core.Skill, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	_, d, e := a.settings(name)
	if e != nil {
		return core.Skill{}, e
	}
	return d.AddSkill(from)
}
func (a *App) RemoveSkill(ctx context.Context, name, id string) error {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	_, d, e := a.settings(name)
	if e != nil {
		return e
	}
	return d.RemoveSkill(id)
}
func (a *App) Env(ctx context.Context, name string) ([]core.EnvVar, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return nil, e
	}
	return d.Env(v.Runtime.EnvFile)
}
func (a *App) SetEnv(ctx context.Context, name, key, value string) error {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, d, e := a.settings(name)
	if e != nil {
		return e
	}
	return d.SetEnv(v.Runtime.EnvFile, key, value)
}
func (a *App) DeleteEnv(ctx context.Context, name, key string) error {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, d, e := a.settings(name)
	if e != nil {
		return e
	}
	return d.DeleteEnv(v.Runtime.EnvFile, key)
}
func (a *App) Files(ctx context.Context, name string) ([]core.FileInfo, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return nil, e
	}
	files, e := d.Files()
	out := make([]core.FileInfo, 0, len(files))
	for _, f := range files {
		if !envFilePath(v, f.Path) {
			out = append(out, f)
		}
	}
	return out, e
}
func (a *App) ReadFile(ctx context.Context, name, path string) (core.FileContent, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return core.FileContent{}, e
	}
	if envFilePath(v, path) {
		return core.FileContent{}, core.ErrDenied
	}
	return d.ReadFile(path)
}
func (a *App) WriteFile(ctx context.Context, name, path, content, hash string) (core.FileContent, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	agent, d, e := a.settings(name)
	if e != nil {
		return core.FileContent{}, e
	}
	if envFilePath(agent, path) {
		return core.FileContent{}, core.ErrDenied
	}
	if filepath.Clean(path) == "lolabot.yaml" {
		var doc struct {
			Runtime *core.Runtime `yaml:"runtime"`
			Agent   struct {
				Name string `yaml:"name"`
			} `yaml:"agent"`
		}
		if yaml.Unmarshal([]byte(content), &doc) != nil || doc.Runtime == nil {
			return core.FileContent{}, fmt.Errorf("%w: invalid agent runtime", core.ErrInvalid)
		}
		rt := *doc.Runtime
		if rt.Harness != agent.Runtime.Harness || rt.Slug != agent.Runtime.Slug {
			return core.FileContent{}, fmt.Errorf("%w: slug and harness cannot be changed here", core.ErrInvalid)
		}
		slug, err := registry.ResolveSlug(agent.Dir, doc.Agent.Name, rt.Slug)
		if err != nil || slug != agent.Name {
			return core.FileContent{}, fmt.Errorf("%w: agent slug cannot change", core.ErrInvalid)
		}
		if err = registry.Validate(rt); err != nil {
			return core.FileContent{}, err
		}
	}
	v, e := d.WriteFile(path, content, hash)
	if e == nil && filepath.Clean(path) == "lolabot.yaml" {
		e = a.Supervisor.Reload(ctx)
	}
	return v, e
}
func emailWatch(s core.EmailSettings, rt core.Runtime) core.EmailSettings {
	for i := range s.Accounts {
		s.Accounts[i].Password = ""
	}
	for _, t := range rt.Triggers {
		if t.Mail != "" {
			s.Watch = "lolad"
			s.WatchAccount = t.Mail
			s.Every = t.Every
			break
		}
	}
	return s
}
func (a *App) Email(ctx context.Context, name string) (core.EmailSettings, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return core.EmailSettings{}, e
	}
	s, e := d.Email(v.Runtime.EnvFile)
	return emailWatch(s, v.Runtime), e
}
func (a *App) SaveEmail(ctx context.Context, name string, s core.EmailSettings) (core.EmailSettings, error) {
	a.mutate.Lock()
	defer a.mutate.Unlock()
	v, d, e := a.settings(name)
	if e != nil {
		return core.EmailSettings{}, e
	}
	old, e := d.Email(v.Runtime.EnvFile)
	if e != nil {
		return core.EmailSettings{}, e
	}
	switch s.Watch {
	case "lolad":
		if old.Watch == "mod" {
			return core.EmailSettings{}, fmt.Errorf("%w: disable the agent's mail mod before enabling lolad email watching", core.ErrInvalid)
		}
		found := false
		for _, ac := range s.Accounts {
			if ac.Address == s.WatchAccount || ac.ConfigKey == s.WatchAccount {
				found = true
			}
		}
		if !found || s.WatchAccount == "" {
			return core.EmailSettings{}, fmt.Errorf("%w: watch_account must identify an email account", core.ErrInvalid)
		}
		if s.Every == "" {
			s.Every = "2m"
		}
		interval, err := time.ParseDuration(s.Every)
		if err != nil || interval <= 0 {
			return core.EmailSettings{}, fmt.Errorf("%w: invalid email watch interval", core.ErrInvalid)
		}
	case "none", "mod":
	default:
		return core.EmailSettings{}, fmt.Errorf("%w: email watch must be none, mod or lolad", core.ErrInvalid)
	}
	rt := v.Runtime
	rt.Triggers = nil
	for _, t := range v.Runtime.Triggers {
		if t.Mail == "" {
			rt.Triggers = append(rt.Triggers, t)
		}
	}
	if s.Watch == "lolad" {
		rt.Triggers = append(rt.Triggers, core.Trigger{Mail: s.WatchAccount, Every: s.Every})
	}
	if e = registry.Validate(rt); e != nil {
		return core.EmailSettings{}, e
	}
	saved, e := d.SaveEmail(v.Runtime.EnvFile, s)
	if e != nil {
		return core.EmailSettings{}, e
	}
	if !reflect.DeepEqual(rt, v.Runtime) {
		if e = registry.WriteRuntime(v.Dir, rt); e != nil {
			return core.EmailSettings{}, e
		}
	}
	if e = a.Supervisor.Reload(ctx); e != nil {
		return core.EmailSettings{}, e
	}
	return emailWatch(saved, rt), nil
}
func (a *App) TestEmail(ctx context.Context, name, address string) (core.EmailTest, error) {
	v, d, e := a.settings(name)
	if e != nil {
		return core.EmailTest{}, e
	}
	s, e := d.Email(v.Runtime.EnvFile)
	if e != nil {
		return core.EmailTest{}, e
	}
	for _, ac := range s.Accounts {
		if strings.EqualFold(ac.Address, address) {
			password, e := d.EmailPassword(v.Runtime.EnvFile, ac.Address)
			if e != nil {
				return core.EmailTest{}, e
			}
			return agentcfg.TestEmail(ctx, ac, password), nil
		}
	}
	return core.EmailTest{}, core.ErrNotFound
}

// The configured env file can have any extension; exclude aliases as well.
func envFilePath(a core.Agent, rel string) bool {
	env := a.Runtime.EnvFile
	if env == "" {
		env = ".env"
	}
	if !filepath.IsAbs(env) {
		env = filepath.Join(a.Dir, env)
	}
	path := filepath.Join(a.Dir, rel)
	if filepath.Clean(env) == filepath.Clean(path) {
		return true
	}
	es, e := os.Stat(env)
	ps, p := os.Stat(path)
	return e == nil && p == nil && os.SameFile(es, ps)
}
