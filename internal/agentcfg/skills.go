package agentcfg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

func (d *Dir) agentSkills() string {
	if d.harness == core.HarnessCodewhale {
		return ".codewhale-home/skills"
	}
	return ".claude/skills"
}
func skillsAt(dir, source, root string, active bool) []core.Skill {
	out := []core.Skill{}
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		p := filepath.Join(dir, ent.Name())
		b, e := os.ReadFile(filepath.Join(p, "SKILL.md"))
		if e != nil {
			continue
		}
		var front struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		if strings.HasPrefix(s, "---\n") {
			if end := strings.Index(s[4:], "\n---"); end >= 0 {
				_ = yaml.Unmarshal([]byte(s[4:4+end]), &front)
			}
		}
		if front.Name == "" {
			front.Name = ent.Name()
		}
		v := core.Skill{ID: source + ":" + ent.Name(), Name: front.Name, Description: front.Description, Source: source, Path: p, Active: active}
		if source == "agent" {
			rel, _ := filepath.Rel(root, filepath.Join(p, "SKILL.md"))
			v.FilePath = filepath.ToSlash(rel)
		}
		out = append(out, v)
	}
	return out
}
func (d *Dir) Skills(mode core.SkillsMode) ([]core.Skill, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.skills(mode)
}
func (d *Dir) skills(mode core.SkillsMode) ([]core.Skill, error) {
	active := mode != core.SkillsBarebones
	out := []core.Skill{}
	p, e := d.safe(d.agentSkills(), true)
	if e != nil {
		return nil, e
	}
	out = append(out, skillsAt(p, "agent", d.root, true)...)
	dirs := d.userSkillRoots()
	seen := map[string]bool{}
	for _, p := range dirs {
		for _, s := range skillsAt(p, "user", d.root, active) {
			if !seen[s.ID] {
				out = append(out, s)
				seen[s.ID] = true
			}
		}
	}
	if d.harness == core.HarnessClaude {
		for name, root := range d.enabledPluginRoots() {
			out = append(out, skillsAt(filepath.Join(root, "skills"), "plugin:"+name, d.root, active)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (d *Dir) AddSkill(id string) (core.Skill, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	list, e := d.skills(core.SkillsInherit)
	if e != nil {
		return core.Skill{}, e
	}
	for _, s := range list {
		if s.ID != id {
			continue
		}
		if s.Source == "agent" {
			return core.Skill{}, core.ErrInvalid
		}
		name := filepath.Base(s.Path)
		dest, e := d.safe(filepath.Join(d.agentSkills(), name), true)
		if e != nil {
			return core.Skill{}, e
		}
		if _, e = os.Lstat(dest); e == nil {
			return core.Skill{}, core.ErrExists
		}
		parent, e := d.safe(d.agentSkills(), true)
		if e != nil {
			return core.Skill{}, e
		}
		if e = os.MkdirAll(parent, 0755); e != nil {
			return core.Skill{}, errors.New("cannot create skills directory")
		}
		tmp, e := os.MkdirTemp(parent, ".lolad-backup-skill-")
		if e != nil {
			return core.Skill{}, e
		}
		defer os.RemoveAll(tmp)
		if e = copySkill(s.Path, tmp, map[string]bool{}); e != nil {
			return core.Skill{}, e
		}
		if e = os.Rename(tmp, dest); e != nil {
			return core.Skill{}, errors.New("cannot install skill")
		}
		got := skillsAt(parent, "agent", d.root, true)
		for _, v := range got {
			if v.Path == dest {
				return v, nil
			}
		}
		return core.Skill{}, core.ErrInvalid
	}
	return core.Skill{}, core.ErrNotFound
}
func copySkill(src, dst string, stack map[string]bool) error {
	real, e := filepath.EvalSymlinks(src)
	if e != nil {
		return errors.New("cannot resolve skill")
	}
	if stack[real] {
		return core.ErrInvalid
	}
	st, e := os.Stat(real)
	if e != nil {
		return core.ErrInvalid
	}
	if st.IsDir() {
		stack[real] = true
		defer delete(stack, real)
		entries, e := os.ReadDir(real)
		if e != nil {
			return core.ErrInvalid
		}
		if e = os.MkdirAll(dst, 0755); e != nil {
			return e
		}
		for _, ent := range entries {
			if e = copySkill(filepath.Join(real, ent.Name()), filepath.Join(dst, ent.Name()), stack); e != nil {
				return e
			}
		}
		return nil
	}
	if !st.Mode().IsRegular() {
		return core.ErrInvalid
	}
	b, e := os.ReadFile(real)
	if e != nil {
		return core.ErrInvalid
	}
	return atomicWrite(dst, b, st.Mode())
}
func (d *Dir) RemoveSkill(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	name, ok := strings.CutPrefix(id, "agent:")
	if !ok || name == "" || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return core.ErrInvalid
	}
	rel := filepath.Join(d.agentSkills(), name)
	parent, e := d.safe(d.agentSkills(), false)
	if e != nil {
		return e
	}
	raw := filepath.Join(parent, name)
	if st, e := os.Lstat(raw); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(raw)
	}
	p, e := d.safe(rel, false)
	if e != nil {
		return e
	}
	if _, e = os.Stat(filepath.Join(p, "SKILL.md")); e != nil {
		return core.ErrNotFound
	}
	return os.RemoveAll(p)
}
func (d *Dir) Fingerprint(envFile string, rt core.Runtime) (string, map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	parts := map[string]string{}
	b, _ := json.Marshal(rt)
	parts["runtime"] = hash(b)
	p, e := d.envPath(envFile)
	if e == nil {
		b, _ = os.ReadFile(p)
	} else {
		b = nil
	}
	parts["env"] = hash(b)
	var entries []string
	for _, rel := range []string{".claude/skills", ".codewhale-home/skills", ".codewhale/skills"} {
		p, e := d.safe(rel, true)
		if e != nil {
			continue
		}
		fingerprintSkills(d.root, p, map[string]bool{}, &entries)
	}

	if rt.SkillsMode() == core.SkillsInherit {
		for _, root := range d.userSkillRoots() {
			fingerprintSkills(d.root, root, map[string]bool{}, &entries)
		}
		if d.harness == core.HarnessClaude {
			for name, root := range d.enabledPluginRoots() {
				entries = append(entries, "plugin:"+name+":"+root)
				fingerprintSkills(d.root, root, map[string]bool{}, &entries)
			}
		}
	}
	sort.Strings(entries)
	parts["skills"] = hash([]byte(strings.Join(entries, "\n")))
	var email []byte
	b, n, e := d.readYAML("lolabot.yaml")
	if e == nil {
		a, z := blockSpan(b, n, "email")
		email = append(email, b[a:z]...)
	}
	_, n, e = d.readYAML("brain/credentials.yaml")
	if e == nil {
		b, _ = yaml.Marshal(nodeGet(n, "email_accounts"))
		email = append(email, b...)
	}
	parts["email"] = hash(email)
	b, _ = json.Marshal(parts)
	return hash(b), parts
}

// Follow linked skill directories as the harness does, tracking cycles without
// including file content (the fingerprint contract is names plus mtimes).
func fingerprintSkills(root, path string, stack map[string]bool, entries *[]string) {
	real, e := filepath.EvalSymlinks(path)
	if e != nil || stack[real] {
		return
	}
	st, e := os.Stat(real)
	if e != nil {
		return
	}
	rel, _ := filepath.Rel(root, path)
	*entries = append(*entries, filepath.ToSlash(rel)+":"+st.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z"))
	if !st.IsDir() {
		return
	}
	stack[real] = true
	defer delete(stack, real)
	children, e := os.ReadDir(real)
	if e != nil {
		return
	}
	for _, child := range children {
		fingerprintSkills(root, filepath.Join(path, child.Name()), stack, entries)
	}
}

func (d *Dir) enabledPluginRoots() map[string]string {
	roots := map[string]string{}
	var settings struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	b, _ := os.ReadFile(filepath.Join(d.home, ".claude/settings.json"))
	_ = json.Unmarshal(b, &settings)
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	b, _ = os.ReadFile(filepath.Join(d.home, ".claude/plugins/installed_plugins.json"))
	_ = json.Unmarshal(b, &installed)
	for name, list := range installed.Plugins {
		if !settings.Enabled[name] {
			continue
		}
		for _, v := range list {
			if v.Scope == "user" {
				roots[name] = v.InstallPath
				break
			}
		}
	}
	return roots
}
func (d *Dir) userSkillRoots() []string {
	if d.harness == core.HarnessCodewhale {
		return []string{filepath.Join(d.home, ".codewhale/skills"), filepath.Join(d.home, ".agents/skills"), filepath.Join(d.home, ".claude/skills")}
	}
	return []string{filepath.Join(d.home, ".claude/skills")}
}
