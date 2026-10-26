package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/create"
	"gopkg.in/yaml.v3"
)

// CreateOptions scans only configured roots, one level deep. It never reads
// credentials, instructions, or any file other than lolabot.yaml.
func (a *App) CreateOptions(ctx context.Context) (core.CreateOptions, error) {
	out := core.CreateOptions{DefaultDir: a.Config.NewAgentsDir, Candidates: []core.AdoptCandidate{}}
	dirs := map[string]bool{}
	for _, dir := range a.Config.Agents {
		dirs[filepath.Clean(dir)] = true
	}
	roots := append(append([]string(nil), a.Config.AgentsDirs...), a.Config.NewAgentsDir)
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return out, fmt.Errorf("cannot scan agent directory %s", root)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				dirs[filepath.Join(root, entry.Name())] = true
			}
		}
	}
	registered := map[string]bool{}
	if a.Registry != nil {
		for _, agent := range a.Registry.List() {
			registered[filepath.Clean(agent.Dir)] = true
		}
	}
	for dir := range dirs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		data, err := os.ReadFile(filepath.Join(dir, "lolabot.yaml"))
		if os.IsNotExist(err) {
			continue
		}
		abs, errAbs := filepath.Abs(dir)
		if errAbs != nil {
			continue
		}
		c := core.AdoptCandidate{Dir: abs, Registered: registered[abs]}
		var doc struct {
			Agent struct {
				Name string `yaml:"name"`
			} `yaml:"agent"`
			Runtime core.Runtime `yaml:"runtime"`
		}
		if err != nil {
			c.Error = "Cannot read lolabot.yaml."
		} else if yaml.Unmarshal(data, &doc) != nil {
			// YAML parser errors may echo source lines containing credentials.
			c.Error = "lolabot.yaml is not valid YAML. Fix it before adopting."
		} else {
			c.Name, c.Slug, c.Harness, c.Model = doc.Agent.Name, doc.Runtime.Slug, doc.Runtime.Harness, doc.Runtime.Model
			if c.Slug == "" {
				c.Slug = create.Slug(c.Name)
			}
			if c.Slug == "" {
				c.Slug = create.Slug(filepath.Base(dir))
			}
			if c.Harness == "" {
				c.Harness = core.HarnessClaude
			}
			if c.Harness != core.HarnessClaude && c.Harness != core.HarnessCodewhale {
				c.Error = "Unknown harness in lolabot.yaml."
			}
			if _, e := os.Stat(filepath.Join(dir, "CLAUDE.md")); e != nil {
				c.Error = "CLAUDE.md is missing or unreadable. Restore it before adopting."
			}
		}
		out.Candidates = append(out.Candidates, c)
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].Dir < out.Candidates[j].Dir })
	return out, nil
}
