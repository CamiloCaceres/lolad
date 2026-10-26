package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/registry"
	"gopkg.in/yaml.v3"
)

// Persist explicit directories that discovery would not otherwise see. Keep the
// user's YAML nodes/comments and replace the configuration atomically.
func (a *App) registerDir(dir string) error {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return e
	}
	cfg := a.Registry.Config()
	for _, p := range cfg.Agents {
		v, _ := filepath.Abs(p)
		if v == abs {
			return nil
		}
	}
	for _, p := range cfg.AgentsDirs {
		v, _ := filepath.Abs(p)
		if filepath.Dir(abs) == v {
			return nil
		}
	}
	path := a.configPath
	data, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	var doc yaml.Node
	if len(data) > 0 {
		if e = yaml.Unmarshal(data, &doc); e != nil {
			return e
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: config must be a mapping", core.ErrInvalid)
	}
	var agents *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "agents" {
			agents = root.Content[i+1]
		}
	}
	if agents == nil {
		agents = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "agents"}, agents)
	}
	if agents.Tag == "!!null" {
		*agents = yaml.Node{Kind: yaml.SequenceNode}
	}
	if agents.Kind != yaml.SequenceNode {
		return fmt.Errorf("%w: config agents must be a sequence", core.ErrInvalid)
	}
	agents.Content = append(agents.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: abs})
	data, e = yaml.Marshal(&doc)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	e = f.Chmod(0600)
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}

type runtimeWriter struct{ a *App }

func (w runtimeWriter) WriteRuntime(dir string, r core.Runtime) error {
	if e := registry.WriteRuntime(dir, r); e != nil {
		return e
	}
	return w.a.registerDir(dir)
}
