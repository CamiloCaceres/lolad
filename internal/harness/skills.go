package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// claudeSkills keeps user settings/hooks excluded. Recent Claude versions gate
// user skills on the user setting source too, so inherit exposes just that tree
// through an additional directory, without changing HOME or the auth config.
func claudeSkills(agent core.Agent, dir string, env map[string]string, overlay map[string]any) ([]string, error) {
	if agent.Runtime.SkillsMode() == core.SkillsBarebones {
		// Additional directories can survive in resumed sessions. Empty our prior
		// projection as well as omitting --add-dir, so it cannot retain user skills.
		target := filepath.Join(dir, "user-skills", ".claude", "skills")
		if _, err := os.Readlink(target); err == nil {
			if err = os.Remove(target); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("managed user skills projection is not a symlink")
		}
		return nil, nil
	}
	home := env["CLAUDE_CONFIG_DIR"]
	if home == "" {
		home = filepath.Join(env["HOME"], ".claude")
	}
	enabled := map[string]bool{}
	for _, path := range []string{filepath.Join(home, "settings.json"), filepath.Join(agent.Dir, ".claude", "settings.json"), filepath.Join(agent.Dir, ".claude", "settings.local.json")} {
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if e = json.Unmarshal(b, &settings); e != nil {
			return nil, fmt.Errorf("read plugin enablement: %w", e)
		}
		for k, v := range settings.EnabledPlugins {
			enabled[k] = v
		}
	}
	if len(enabled) > 0 {
		overlay["enabledPlugins"] = enabled
	}
	source := filepath.Join(home, "skills")
	if st, e := os.Stat(source); os.IsNotExist(e) {
		return nil, nil
	} else if e != nil {
		return nil, e
	} else if !st.IsDir() {
		return nil, fmt.Errorf("user skills path is not a directory")
	}
	projection := filepath.Join(dir, "user-skills")
	target := filepath.Join(projection, ".claude", "skills")
	if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return nil, e
	}
	if old, e := os.Readlink(target); e == nil {
		if old == source {
			return []string{"--add-dir", projection}, nil
		}
		if e = os.Remove(target); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, fmt.Errorf("managed user skills projection is not a symlink")
	}
	if e := os.Symlink(source, target); e != nil {
		return nil, e
	}
	return []string{"--add-dir", projection}, nil
}

// codewhaleSkills changes only the boolean in the private per-run overlay.
func codewhaleSkills(text string, mode core.SkillsMode) string {
	lines := strings.Split(text, "\n")
	kept := []string{}
	table := ""
	found := false
	setting := fmt.Sprintf("scan_codewhale_only = %t", mode == core.SkillsBarebones)
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			table = strings.TrimSpace(strings.SplitN(trim, "#", 2)[0])
			if table == "[skills]" {
				kept = append(kept, line, setting)
				found = true
				continue
			}
		}
		k, _, ok := strings.Cut(trim, "=")
		k = strings.Trim(strings.TrimSpace(k), "\"'")
		if ok && ((table == "[skills]" && k == "scan_codewhale_only") || (table == "" && k == "skills.scan_codewhale_only")) {
			continue
		}
		kept = append(kept, line)
	}
	if !found {
		kept = append(kept, "[skills]", setting)
	}
	return strings.Join(kept, "\n") + "\n"
}
