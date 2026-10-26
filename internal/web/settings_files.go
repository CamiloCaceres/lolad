package web

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// fileNode is presentation only: core.Service remains the authority on which
// files can be read and written. Generated harness files are opt-in noise.
type fileNode struct {
	Name, Path string
	File       *core.FileInfo
	Children   []*fileNode
	Open       bool
}

func noisyFile(p string) bool {
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		switch part {
		case "node_modules", "builtin-plugins", "snapshots", "cache", "caches", ".cache", "state", "backups", "backup", ".git":
			return true
		}
	}
	return false
}

func importantFile(f core.FileInfo) string {
	switch {
	case f.Path == "CLAUDE.md" || f.Path == "AGENTS.md":
		return "instructions"
	case f.Path == "lolabot.yaml" || (strings.HasPrefix(f.Path, ".claude/settings") && strings.HasSuffix(f.Path, ".json")) || f.Path == ".codewhale-home/config.toml":
		return "config"
	case strings.HasPrefix(f.Path, "brain/"):
		return "brain"
	case strings.HasPrefix(f.Path, "memory/"):
		return "memory"
	case strings.HasPrefix(f.Path, "skills/") || strings.HasPrefix(f.Path, ".claude/skills/") || strings.HasPrefix(f.Path, ".codewhale-home/skills/"):
		return "skills"
	}
	return ""
}

func organizeFiles(files []core.FileInfo, query string, all bool, selected string) ([]fileGroup, []*fileNode, int, int) {
	var groups []fileGroup
	var tree []*fileNode
	hidden, count := 0, 0
	ordered := slices.Clone(files)
	slices.SortFunc(ordered, func(a, b core.FileInfo) int { return cmp.Compare(a.Path, b.Path) })
	for _, f := range ordered {
		if noisyFile(f.Path) && !all {
			hidden++
			continue
		}
		if !strings.Contains(strings.ToLower(f.Path), strings.ToLower(query)) {
			continue
		}
		count++
		if group := importantFile(f); group != "" && !noisyFile(f.Path) {
			i := slices.IndexFunc(groups, func(g fileGroup) bool { return g.Key == group })
			if i < 0 {
				groups = append(groups, fileGroup{Key: group, Label: fileGroupLabels[group]})
				i = len(groups) - 1
			}
			groups[i].Files = append(groups[i].Files, f)
			continue
		}
		level := &tree
		parts := strings.Split(f.Path, "/")
		for i, name := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			idx := slices.IndexFunc(*level, func(n *fileNode) bool { return n.Path == prefix })
			if idx < 0 {
				*level = append(*level, &fileNode{Name: name, Path: prefix})
				idx = len(*level) - 1
			}
			node := (*level)[idx]
			node.Open = query != "" || strings.HasPrefix(selected, prefix+"/")
			if i == len(parts)-1 {
				copy := f
				node.File = &copy
				node.Name = path.Base(f.Path)
			}
			level = &node.Children
		}
	}
	order := []string{"instructions", "config", "brain", "memory", "skills"}
	slices.SortFunc(groups, func(a, b fileGroup) int { return slices.Index(order, a.Key) - slices.Index(order, b.Key) })
	return groups, tree, hidden, count
}

// settingsPath keeps the beginning and most useful trailing directory names;
// the filename is rendered separately in full and title carries the full path.
func settingsPath(p string) string {
	runes := []rune(p)
	if len(runes) <= 30 {
		return p
	}
	return string(runes[:8]) + "…" + string(runes[len(runes)-20:])
}
