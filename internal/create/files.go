package create

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/CamiloCaceres/lolad/docs"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func setupCompatibility(env []string) ([]string, func(), error) {
	if runtime.GOOS != "darwin" {
		return env, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "lolad-setup-tools-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	scripts := map[string]string{
		"realpath": "#!/bin/sh\nif [ \"$1\" = -m ]; then shift; fi\nexec /bin/realpath \"$@\"\n",
		"sed":      "#!/bin/sh\nif [ \"$1\" = -i ]; then shift; exec /usr/bin/sed -i '' \"$@\"; fi\nexec /usr/bin/sed \"$@\"\n",
	}
	for name, script := range scripts {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	result := append([]string(nil), env...)
	found := false
	for i, v := range result {
		if strings.HasPrefix(v, "PATH=") {
			result[i] = "PATH=" + dir + ":" + strings.TrimPrefix(v, "PATH=")
			found = true
		}
	}
	if !found {
		result = append(result, "PATH="+dir+":/usr/bin:/bin")
	}
	return result, cleanup, nil
}

func writeHarness(a core.Agent) error {
	if a.Runtime.Harness == core.HarnessClaude {
		dir := filepath.Join(a.Dir, ".claude")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		path := filepath.Join(dir, "settings.json")
		settings := map[string]any{}
		data, err := os.ReadFile(path)
		if err == nil {
			if err = json.Unmarshal(data, &settings); err != nil {
				return err
			}
			if settings == nil {
				return fmt.Errorf("settings.json must be an object")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if a.Runtime.Model != "" {
			settings["model"] = a.Runtime.Model
		}
		data, err = json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return err
		}
		if err = atomicWrite(path, append(data, '\n'), 0600); err != nil {
			return err
		}
		link := filepath.Join(a.Dir, "AGENTS.md")
		if _, err = os.Lstat(link); os.IsNotExist(err) {
			return os.Symlink("CLAUDE.md", link)
		} else {
			return err
		}
	}
	home := filepath.Join(a.Dir, ".codewhale-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	config := filepath.Join(home, "config.toml")
	// Existing configs can contain provider settings and secrets owned by the
	// agent. Keep them byte-for-byte; runtime passes the requested model via argv.
	if _, err := os.Stat(config); os.IsNotExist(err) {
		text := "# Managed initial lolad config; credentials belong in .env.\nprovider = \"deepseek\"\nreasoning_effort = \"high\"\ntelemetry = false\nverbosity = \"normal\"\nproject_instruction_imports = [\"claude\"]\napproval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\nsandbox_network_access = true\n\n[skills]\nscan_codewhale_only = true\n"
		if a.Runtime.Model != "" {
			text += "\n[providers.deepseek]\nmodel = " + strconv.Quote(a.Runtime.Model) + "\n"
		}
		if err = atomicWrite(config, []byte(text), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	launcher := filepath.Join(a.Dir, a.Name)
	if _, err := os.Lstat(launcher); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	script := "#!/bin/sh\nset -e\nLOLABOT_HOME=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\nexport LOLABOT_HOME\nexport CODEWHALE_HOME=\"$LOLABOT_HOME/.codewhale-home\"\nif [ -f \"$CODEWHALE_HOME/prompts/constitution.md\" ]; then\n  export CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE=1\nfi\nif [ -f \"$LOLABOT_HOME/.env\" ]; then\n  set -a; . \"$LOLABOT_HOME/.env\"; set +a\nfi\nexport LOLAD_AGENT=" + a.Name + "\ncd \"$LOLABOT_HOME\"\nexec codewhale \"$@\"\n"
	return atomicWrite(launcher, []byte(script), 0755)
}

const begin = "<!-- lolad:begin -->"
const end = "<!-- lolad:end -->"

// RegenerateSection refreshes one agent from the current org and policy. The
// service must invoke this for EVERY registered agent after each org change.
func (c *Creator) RegenerateSection(a core.Agent) error {
	if c.deps.Org == nil || c.deps.Contacts == nil {
		return fmt.Errorf("%w: org and contacts dependencies required", core.ErrInvalid)
	}
	org, err := c.deps.Org.Org(context.Background())
	if err != nil {
		return err
	}
	contacts := append([]string(nil), c.deps.Contacts.Contacts(a.Name)...)
	sort.Strings(contacts)
	contactText := "nobody under the current policy"
	if len(contacts) > 0 {
		contactText = strings.Join(contacts, ", ")
	}
	reporting := "You are independent (no team). You report to the user, " + org.User.Name + "."
	for _, team := range org.Teams {
		member := team.Head == a.Name
		for _, m := range team.Members {
			if m == a.Name {
				member = true
			}
		}
		if !member {
			continue
		}
		reporting = fmt.Sprintf("Your team is %s (%s). Its head is %s. You report to %s.", team.ID, team.Name, team.Head, team.Head)
		if team.Head == a.Name {
			boss := org.User.Name
			for _, parent := range org.Teams {
				if parent.ID == team.Parent && parent.Head != "" {
					boss = parent.Head
				}
			}
			reporting = fmt.Sprintf("Your team is %s (%s). You are its head. You report to %s.", team.ID, team.Name, boss)
		} else if team.Head == "" {
			reporting = fmt.Sprintf("Your team is %s (%s), currently without a head. Escalate to the user, %s.", team.ID, team.Name, org.User.Name)
		}
	}
	data := struct{ Address, Name, Reporting, Contacts string }{a.Name + "@" + org.Tenant + ".lolad.local", a.Name, reporting, contactText}
	tmpl, err := template.New("messaging").Parse(docs.AgentMessaging)
	if err != nil {
		return err
	}
	var section bytes.Buffer
	section.WriteString(begin + "\n")
	if err = tmpl.Execute(&section, data); err != nil {
		return err
	}
	section.WriteString(end)
	path := filepath.Join(a.Dir, "CLAUDE.md")
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(original)
	first, last := strings.Index(text, begin), strings.Index(text, end)
	if first >= 0 || last >= 0 {
		if first < 0 || last < first || strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
			return fmt.Errorf("%w: malformed lolad section; preserve and repair the fences", core.ErrInvalid)
		}
		text = text[:first] + section.String() + text[last+len(end):]
	} else {
		text = strings.TrimRight(text, "\n") + "\n\n" + section.String() + "\n"
	}
	// Preserve historical/user content, but redirect the standard upstream
	// guidance header so it cannot be mistaken for the active integration.
	old := "## Working with other agents (optional)\n"
	replacement := "## Working with other agents (optional)\n\n> Superseded: use the [lolad messaging](#lolad-messaging) section below. The following historical AI Maestro guidance does not apply.\n"
	if strings.Contains(text, old) && !strings.Contains(text, replacement) {
		text = strings.Replace(text, old, replacement, 1)
	}
	if text == string(original) {
		return nil
	}
	return atomicWrite(path, []byte(text), 0644)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	// Refuse to replace symlinked managed files: preserve the user's ownership
	// boundary rather than following a link outside the instance.
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing managed symlink %s", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".lolad-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
