package agentcfg

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

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
	parts := d.FingerprintParts(envFile, rt)
	b, _ := json.Marshal(parts)
	return hash(b), parts
}

// FingerprintParts computes only the named parts of Fingerprint (all of them
// when none are named). The skills and plugins parts walk every inherited
// skill and plugin folder; a caller comparing just env or plugin_set should
// not pay for that on each status read.
func (d *Dir) FingerprintParts(envFile string, rt core.Runtime, want ...string) map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	need := func(part string) bool { return len(want) == 0 || slices.Contains(want, part) }
	parts := map[string]string{}
	if need("runtime") {
		b, _ := json.Marshal(rt)
		parts["runtime"] = hash(b)
	}
	if need("env") {
		var b []byte
		if p, e := d.envPath(envFile); e == nil {
			b, _ = os.ReadFile(p)
		}
		parts["env"] = hash(b)
	}
	inherit := rt.SkillsMode() == core.SkillsInherit
	if need("skills") {
		var entries []string
		for _, rel := range []string{".claude/skills", ".codewhale-home/skills", ".codewhale/skills"} {
			p, e := d.safe(rel, true)
			if e != nil {
				continue
			}
			fingerprintSkills(d.root, p, &entries)
		}
		if inherit {
			for _, root := range d.userSkillRoots() {
				fingerprintSkills(d.root, root, &entries)
			}
		}
		sort.Strings(entries)
		parts["skills"] = hash([]byte(strings.Join(entries, "\n")))
	}
	// Plugins are their own parts: a running Claude Code activates updated
	// plugin versions with /reload-plugins and skill folders with
	// /reload-skills, but which plugins are enabled is read only at launch.
	if need("plugins") || need("plugin_set") {
		var plugins, enabled []string
		if inherit && d.harness == core.HarnessClaude {
			for name, root := range d.enabledPluginRoots() {
				enabled = append(enabled, name)
				if need("plugins") {
					plugins = append(plugins, "plugin:"+name+":"+root)
					fingerprintSkills(d.root, root, &plugins)
				}
			}
		}
		if need("plugins") {
			sort.Strings(plugins)
			parts["plugins"] = hash([]byte(strings.Join(plugins, "\n")))
		}
		if need("plugin_set") {
			sort.Strings(enabled)
			parts["plugin_set"] = hash([]byte(strings.Join(enabled, "\n")))
		}
	}
	if need("email") {
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
	}
	return parts
}

// Follow linked skill directories as the harness does, tracking cycles without
// including file content (the fingerprint contract is names plus file mtimes).
// Directory mtimes are left out: they move whenever an entry is added or
// removed, which the child names already capture, and Claude Code adds and
// removes per-process `.in_use/<pid>` markers inside every plugin it loads, so
// counting them made each launch report its own skills as changed. Synced
// skills likewise get `.last-complete-round` rewritten on every sync.
func fingerprintSkills(root, path string, entries *[]string) {
	if skipSkillEntry(filepath.Base(path)) {
		return
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return
	}
	st, e := os.Stat(real)
	if e != nil {
		return
	}
	rel, e := filepath.Rel(root, path)
	walkSkills(rel, e == nil, real, st.IsDir(), st.ModTime(), map[string]bool{}, entries)
}

func skipSkillEntry(name string) bool { return name == ".in_use" || name == ".last-complete-round" }

// walkSkills records one resolved entry and, for a directory, its children.
// Only symlinked children are resolved: any other child's real path is its
// parent's plus its name and its Lstat is its Stat, so a plain file costs one
// syscall rather than a full EvalSymlinks per path component. Names stay
// lexically relative to the fingerprint root (empty when that root has no
// relative path to it), as filepath.Rel would give them.
func walkSkills(rel string, named bool, real string, dir bool, mod time.Time, stack map[string]bool, entries *[]string) {
	name := ""
	if named {
		name = filepath.ToSlash(rel)
	}
	if !dir {
		*entries = append(*entries, name+":"+mod.UTC().Format("2006-01-02T15:04:05.000000000Z"))
		return
	}
	*entries = append(*entries, name+"/")
	stack[real] = true
	defer delete(stack, real)
	children, e := os.ReadDir(real)
	if e != nil {
		return
	}
	for _, child := range children {
		if skipSkillEntry(child.Name()) {
			continue
		}
		childRel := filepath.Join(rel, child.Name())
		childReal := filepath.Join(real, child.Name())
		if child.Type()&fs.ModeSymlink != 0 {
			if childReal, e = filepath.EvalSymlinks(childReal); e != nil || stack[childReal] {
				continue
			}
			st, e := os.Stat(childReal)
			if e != nil {
				continue
			}
			walkSkills(childRel, named, childReal, st.IsDir(), st.ModTime(), stack, entries)
			continue
		}
		if stack[childReal] {
			continue
		}
		if child.IsDir() {
			walkSkills(childRel, named, childReal, true, time.Time{}, stack, entries)
			continue
		}
		info, e := child.Info()
		if e != nil {
			continue
		}
		walkSkills(childRel, named, childReal, false, info.ModTime(), stack, entries)
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
