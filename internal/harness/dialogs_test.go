package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestWorkspaceTrustMerge(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	canonical, _ := filepath.EvalSymlinks(dir)
	path := filepath.Join(home, ".claude.json")
	original := `{"unrelated":9007199254740993,"projects":{"/other":{"hasTrustDialogAccepted":false},` + string(mustJSON(t, canonical)) + `:{"mcpServers":{"keep":true}}}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := trustClaude(dir, env); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["unrelated"]) != "9007199254740993" || !strings.Contains(string(b), `"keep": true`) || !strings.Contains(string(b), `"hasTrustDialogAccepted": false`) {
		t.Fatal(string(b))
	}
	var projects map[string]map[string]json.RawMessage
	json.Unmarshal(got["projects"], &projects)
	if string(projects[canonical]["hasTrustDialogAccepted"]) != "true" {
		t.Fatal(string(b))
	}
	if err = trustClaude(dir, env); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(b) {
		t.Fatal("idempotent trust rewrote config")
	}
	// The installed binary places its config inside an explicit config directory.
	env["CLAUDE_CONFIG_DIR"] = t.TempDir()
	if err = trustClaude(dir, env); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(env["CLAUDE_CONFIG_DIR"], ".claude.json")); err != nil {
		t.Fatal(err)
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestAgreementPreservesExistingState(t *testing.T) {
	for _, choice := range []string{"", "guided_custom", "expert_override"} {
		home := t.TempDir()
		path := filepath.Join(home, "setup_state.json")
		if choice != "" {
			os.WriteFile(path, []byte(`{"schema_version":1,"constitution_choice":"`+choice+`","constitution_checkpoint_completed_for":"future","unrelated":9007199254740993}`), 0600)
		}
		if err := AcceptCodewhaleAgreement(home); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		var got map[string]json.RawMessage
		json.Unmarshal(b, &got)
		want := choice
		if want == "" {
			want = "bundled"
		}
		if string(got["constitution_choice"]) != `"`+want+`"` {
			t.Fatal(string(b))
		}
		if choice != "" && (string(got["unrelated"]) != "9007199254740993" || string(got["constitution_checkpoint_completed_for"]) != `"future"`) {
			t.Fatal(string(b))
		}
	}
}
func TestJSONMergeRefusesMalformedAndSymlink(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "setup_state.json")
	for _, bad := range []string{"", "{broken", "null", "[]"} {
		os.WriteFile(path, []byte(bad), 0600)
		if AcceptCodewhaleAgreement(home) == nil {
			t.Fatal("accepted invalid state")
		}
		b, _ := os.ReadFile(path)
		if string(b) != bad {
			t.Fatal("overwrote invalid state")
		}
	}
	os.Remove(path)
	target := filepath.Join(t.TempDir(), "private")
	os.WriteFile(target, []byte("{}"), 0600)
	os.Symlink(target, path)
	if AcceptCodewhaleAgreement(home) == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "{}" {
		t.Fatal("changed symlink target")
	}
}
func TestStartupDialogCaptures(t *testing.T) {
	for _, tc := range []struct {
		kind core.Harness
		file string
		want string
		keys string
	}{
		{core.HarnessClaude, "claude-trust.txt", "claude workspace trust", "Down,Enter"},
		{core.HarnessCodewhale, "cw-working-agreement.txt", "codewhale working agreement", "Enter"},
		{core.HarnessCodewhale, "cw-no-provider.txt", "", ""},
		{core.HarnessCodewhale, "cw-provider-chooser.txt", "", ""},
		{core.HarnessClaude, "claude-idle.txt", "", ""},
	} {
		b, err := os.ReadFile("testdata/" + tc.file)
		if err != nil {
			t.Fatal(err)
		}
		l := New(tc.kind, "")
		name, keys := l.StartupDialog(string(b))
		if name != tc.want || strings.Join(keys, ",") != tc.keys {
			t.Fatalf("%s: %q %v", tc.file, name, keys)
		}
		if tc.want != "" && l.PromptReady(string(b)) {
			t.Fatal("dialog marked ready")
		}
	}
	l := New(core.HarnessClaude, "")
	selected := "Accessing workspace:\n No, exit\n ❯ Yes, I trust this folder\nEnter to confirm"
	_, keys := l.StartupDialog(selected)
	if strings.Join(keys, ",") != "Enter" {
		t.Fatal(keys)
	}
	for _, pane := range []string{"Allow this tool? Enter to confirm", "Yes, I trust this folder", "Working agreement\nEnter to confirm"} {
		name, _ := l.StartupDialog(pane)
		if name != "" {
			t.Fatal("accepted ambiguous dialog", pane)
		}
	}
}
