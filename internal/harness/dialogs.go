package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// mergeJSON changes only the requested fields, retaining unknown values (including
// large numbers) verbatim. The lock serializes lolad writers across processes;
// rename ensures readers never observe partial JSON. Never replace symlinks.
func mergeJSON(path string, update func(map[string]json.RawMessage) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lolad-lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = lockFile(lock); err != nil {
		return err
	}
	defer unlockFile(lock)
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing managed symlink %s", path)
	}
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	object := map[string]json.RawMessage{}
	if err == nil {
		if err = json.Unmarshal(original, &object); err != nil {
			return err
		}
		if object == nil {
			return fmt.Errorf("%s must contain an object", path)
		}
	}
	before, _ := json.Marshal(object)
	if err = update(object); err != nil {
		return err
	}
	after, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return err
	}
	compact, _ := json.Marshal(object)
	if bytes.Equal(before, compact) && len(original) > 0 {
		return nil
	}
	return managed(path, append(after, '\n'))
}

func trustClaude(dir string, env map[string]string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return err
	}
	path := filepath.Join(env["HOME"], ".claude.json")
	if env["CLAUDE_CONFIG_DIR"] != "" {
		path = filepath.Join(env["CLAUDE_CONFIG_DIR"], ".claude.json")
	}
	if env["HOME"] == "" && env["CLAUDE_CONFIG_DIR"] == "" {
		return fmt.Errorf("Claude workspace trust requires HOME")
	}
	return mergeJSON(path, func(root map[string]json.RawMessage) error {
		projects := map[string]json.RawMessage{}
		if raw, ok := root["projects"]; ok {
			if err := json.Unmarshal(raw, &projects); err != nil {
				return err
			}
			if projects == nil {
				return fmt.Errorf("Claude projects must be an object")
			}
		}
		project := map[string]json.RawMessage{}
		if raw, ok := projects[absolute]; ok {
			if err := json.Unmarshal(raw, &project); err != nil {
				return err
			}
			if project == nil {
				return fmt.Errorf("Claude project must be an object")
			}
		}
		project["hasTrustDialogAccepted"] = json.RawMessage("true")
		projects[absolute], _ = json.Marshal(project)
		root["projects"], _ = json.Marshal(projects)
		return nil
	})
}

// AcceptCodewhaleAgreement records the bundled agreement only when the private
// agent home has no explicit choice. Existing custom constitutions are retained.
// 0.9.4 is the checkpoint lane shipped by Codewhale 0.10.1, not its binary version.
func AcceptCodewhaleAgreement(home string) error {
	if st, err := os.Lstat(home); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing shared Codewhale home symlink %s", home)
	}
	return mergeJSON(filepath.Join(home, "setup_state.json"), func(state map[string]json.RawMessage) error {
		if _, ok := state["schema_version"]; !ok {
			state["schema_version"] = json.RawMessage("1")
		}
		choice := strings.Trim(string(state["constitution_choice"]), "\"")
		if choice == "" || choice == "unset" {
			state["constitution_choice"] = json.RawMessage(`"bundled"`)
		}
		if _, ok := state["constitution_checkpoint_completed_for"]; !ok {
			state["constitution_checkpoint_completed_for"] = json.RawMessage(`"0.9.4"`)
		}
		return nil
	})
}

// StartupDialog is an optional Launcher capability consumed by the supervisor.
// Only narrowly identified first-run agreements are accepted, never provider,
// login, tool permission, or arbitrary confirmation dialogs.
func (a *Adapter) StartupDialog(pane string) (string, []string) {
	p := strings.ToLower(Plain(pane))
	if a.Kind == core.HarnessClaude && strings.Contains(p, "accessing workspace:") && strings.Contains(p, "yes, i trust this folder") && strings.Contains(p, "enter to confirm") {
		for _, line := range strings.Split(p, "\n") {
			if strings.Contains(line, "❯") && strings.Contains(line, "no, exit") {
				return "claude workspace trust", []string{"Down", "Enter"}
			}
			if strings.Contains(line, "❯") && strings.Contains(line, "yes, i trust this folder") {
				return "claude workspace trust", []string{"Enter"}
			}
		}
	}
	if a.Kind == core.HarnessCodewhale && strings.Contains(p, "working agreement") && strings.Contains(p, "this agreement guides behavior, not runtime permissions") && strings.Contains(p, "enter") {
		return "codewhale working agreement", []string{"Enter"}
	}
	return "", nil
}
