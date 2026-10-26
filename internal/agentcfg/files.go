package agentcfg

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/CamiloCaceres/lolad/internal/core"
)

const maxFile = 1 << 20

func excluded(rel string) bool {
	if r := filepath.ToSlash(filepath.Clean(rel)); r == "brain/credentials.yaml" || r == "brain/companies-credentials.yaml" {
		return true
	}
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		switch p {
		case ".git", ".venv", "node_modules", "emails", "indexes", "outbox", "__pycache__":
			return true
		}
		if strings.HasPrefix(p, ".env") || strings.HasPrefix(p, ".lolad-backup-") {
			return true
		}
	}
	return false
}
func fileKind(rel string) string {
	e := strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
	switch e {
	case "md", "yaml", "toml", "json", "txt":
		return e
	case "yml":
		return "yaml"
	}
	return ""
}
func fileInfo(rel string, s fs.FileInfo) core.FileInfo {
	f := core.FileInfo{Path: filepath.ToSlash(rel), Size: s.Size(), ModTime: s.ModTime(), Kind: fileKind(rel), Group: "other"}
	switch f.Path {
	case "CLAUDE.md", "AGENTS.md":
		f.Group = "instructions"
		f.Note = "The lolad section between its markers is regenerated."
	case "lolabot.yaml":
		f.Group = "config"
		f.Note = "The runtime block is managed by lolad; edits are validated by reload."
	case ".claude/settings.json", ".codewhale-home/config.toml":
		f.Group = "config"
	}
	for _, g := range []string{"brain", "memory", "skills"} {
		if strings.HasPrefix(f.Path, g+"/") {
			f.Group = g
		}
	}
	if strings.HasPrefix(f.Path, ".claude/skills/") || strings.HasPrefix(f.Path, ".codewhale-home/skills/") {
		f.Group = "skills"
	}
	return f
}
func text(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c == 0 || c < 9 || (c > 13 && c < 32) {
			return false
		}
	}
	return true
}

// A runtime may name its env file tokens.txt instead of .env. Treat aliases as
// secrets too, including symlinks to an env file inside this directory.
func (d *Dir) isSecretFile(rel string) bool {
	p, e := d.safe(rel, true)
	if e != nil {
		return false
	}
	var cfg struct {
		Runtime core.Runtime `yaml:"runtime"`
		Paths   struct {
			Credentials string `yaml:"credentials"`
		} `yaml:"paths"`
	}
	_, n, e := d.readYAML("lolabot.yaml")
	if e == nil {
		decode(n, &cfg)
	}
	for _, name := range []string{".env", cfg.Runtime.EnvFile, "brain/credentials.yaml", "brain/companies-credentials.yaml", cfg.Paths.Credentials} {
		if name == "" {
			continue
		}
		env, e := d.envPath(name)
		if e != nil {
			continue
		}
		if q, e := filepath.EvalSymlinks(env); e == nil {
			env = q
		}
		if p == env {
			return true
		}
	}
	return false
}

func (d *Dir) readFile(rel string) (core.FileContent, error) {
	var out core.FileContent
	if excluded(rel) || fileKind(rel) == "" || d.isSecretFile(rel) {
		return out, core.ErrInvalid
	}
	p, e := d.safe(rel, false)
	if e != nil {
		return out, e
	}
	targetRel, _ := filepath.Rel(d.root, p)
	if excluded(targetRel) {
		return out, core.ErrInvalid
	}
	s, e := os.Stat(p)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return out, core.ErrNotFound
		}
		return out, core.ErrInvalid
	}
	if !s.Mode().IsRegular() || s.Size() > maxFile {
		return out, core.ErrInvalid
	}
	f, e := os.Open(p)
	if e != nil {
		return out, core.ErrInvalid
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
	if e != nil {
		return out, core.ErrInvalid
	}
	if len(b) > maxFile || !text(b) {
		return out, core.ErrInvalid
	}
	out = core.FileContent{FileInfo: fileInfo(filepath.Clean(rel), s), Content: string(b), Hash: hash(b)}
	return out, nil
}
func (d *Dir) ReadFile(rel string) (core.FileContent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readFile(rel)
}
func (d *Dir) WriteFile(rel, content, baseHash string) (core.FileContent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(content) > maxFile || !text([]byte(content)) {
		return core.FileContent{}, core.ErrInvalid
	}
	old, e := d.readFile(rel)
	if e != nil {
		return core.FileContent{}, e
	}
	if old.Hash != baseHash {
		return core.FileContent{}, core.ErrConflict
	}
	p, e := d.safe(rel, false)
	if e != nil {
		return core.FileContent{}, e
	}
	s, e := os.Stat(p)
	if e != nil {
		return core.FileContent{}, core.ErrNotFound
	}
	if e = atomicWrite(p, []byte(content), s.Mode()); e != nil {
		return core.FileContent{}, e
	}
	return d.readFile(rel)
}
func (d *Dir) Files() ([]core.FileInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []core.FileInfo{}
	e := filepath.WalkDir(d.root, func(p string, s fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(d.root, p)
		if rel == "." {
			return nil
		}
		if excluded(rel) {
			if s.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if s.IsDir() {
			return nil
		}
		if fileKind(rel) == "" {
			return nil
		}
		f, e := d.readFile(rel)
		if e == nil {
			out = append(out, f.FileInfo)
		}
		return nil
	})
	return out, e
}
