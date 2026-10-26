package web

import (
	"github.com/CamiloCaceres/lolad/internal/core"
	"strings"
	"testing"
)

func TestOrganizeFilesPrioritizesAndHidesNoise(t *testing.T) {
	files := []core.FileInfo{
		{Path: ".codewhale-home/builtin-plugins/snapshots/computer-use-3898/skills/use/SKILL.md"},
		{Path: "tools/node_modules/dependency/index.js"},
		{Path: "tools/email.sh"}, {Path: "memory/daily/2026-10-25.md"},
		{Path: ".claude/settings.local.json", Group: "other"}, {Path: "CLAUDE.md"},
		{Path: "AGENTS.md"}, {Path: "lolabot.yaml"}, {Path: ".codewhale-home/skills/ops/SKILL.md"},
		{Path: "brain/profile.md"},
	}
	groups, tree, hidden, count := organizeFiles(files, "", false, "")
	if hidden != 2 || count != 8 {
		t.Fatalf("hidden=%d count=%d", hidden, count)
	}
	want := []string{"instructions", "config", "brain", "memory", "skills"}
	if len(groups) != len(want) {
		t.Fatalf("groups=%+v", groups)
	}
	for i, key := range want {
		if groups[i].Key != key {
			t.Errorf("group %d=%s", i, groups[i].Key)
		}
	}
	if len(tree) != 1 || tree[0].Name != "tools" || tree[0].Open {
		t.Fatalf("tree=%+v", tree)
	}
	if groups[1].Files[0].Path != ".claude/settings.local.json" {
		t.Error("local settings should be prioritized regardless of backend group")
	}
	_, tree, hidden, count = organizeFiles(files, "COMPUTER-use", true, "")
	if count != 1 || hidden != 0 || len(tree) != 1 || !tree[0].Open {
		t.Fatalf("search count=%d hidden=%d tree=%+v", count, hidden, tree)
	}
	node := tree[0]
	for len(node.Children) > 0 {
		if !node.Open {
			t.Error("search ancestors must expand")
		}
		node = node.Children[0]
	}
	if node.File == nil || node.Name != "SKILL.md" {
		t.Fatalf("leaf=%+v", node)
	}
	_, tree, _, _ = organizeFiles(files, "", false, "tools/email.sh")
	if !tree[0].Open {
		t.Error("selected file's ancestors must expand")
	}
}

func TestOrganizeFilesDoesNotMutateServiceOrder(t *testing.T) {
	files := []core.FileInfo{{Path: "z.md"}, {Path: "a.md"}}
	organizeFiles(files, "", false, "")
	if files[0].Path != "z.md" {
		t.Error("presentation reordered service slice")
	}
	groups, tree, _, count := organizeFiles(files, "missing", false, "")
	if len(groups) != 0 || len(tree) != 0 || count != 0 {
		t.Error("unmatched search should be empty")
	}
}

func TestSettingsPathPreservesBothEnds(t *testing.T) {
	got := settingsPath(".codewhale-home/skills/long-skill-name")
	if !strings.HasPrefix(got, ".codewha") || !strings.HasSuffix(got, "long-skill-name") || !strings.Contains(got, "…") {
		t.Errorf("short path=%q", got)
	}
	if settingsPath("brain/people") != "brain/people" {
		t.Error("short paths should stay whole")
	}
}
