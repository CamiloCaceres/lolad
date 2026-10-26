// Package harness builds direct argv launches and per-agent hook overlays.
package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

type Adapter struct {
	Kind   core.Harness
	Binary string
	Path   string
}

var _ core.Launcher = (*Adapter)(nil)

func New(kind core.Harness, binary string) *Adapter { return &Adapter{Kind: kind, Binary: binary} }
func (a *Adapter) Harness() core.Harness            { return a.Kind }
func quote(s string) string                         { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func managed(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".lolad-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func (a *Adapter) Prepare(agent core.Agent, o core.LaunchOpts) (core.LaunchSpec, error) {
	spec := core.LaunchSpec{Dir: agent.Dir}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(agent.Name) {
		return spec, fmt.Errorf("%w: agent slug", core.ErrInvalid)
	}
	env := map[string]string{}
	for _, v := range os.Environ() {
		k, v, _ := strings.Cut(v, "=")
		if strings.HasPrefix(k, "LOLAD_") || strings.HasPrefix(k, "AMP_") || strings.HasPrefix(k, "ORCA_AGENT_HOOK_") {
			continue
		}
		env[k] = v
	}
	if a.Path != "" {
		env["PATH"] = a.Path
	}
	envFile := agent.Runtime.EnvFile
	if envFile == "" {
		envFile = ".env"
	}
	if !filepath.IsAbs(envFile) {
		envFile = filepath.Join(agent.Dir, envFile)
	}
	f, e := os.Open(envFile)
	if e == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				f.Close()
				return spec, fmt.Errorf("invalid env assignment")
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
				v = v[1 : len(v)-1]
			}
			env[k] = strings.ReplaceAll(v, "${LOLABOT_HOME}", agent.Dir)
		}
		e = sc.Err()
		f.Close()
		if e != nil {
			return spec, e
		}
	} else if !os.IsNotExist(e) || agent.Runtime.EnvFile != "" {
		return spec, e
	}
	for k, v := range agent.Runtime.Env {
		env[k] = strings.ReplaceAll(v, "${LOLABOT_HOME}", agent.Dir)
	}
	env["LOLABOT_HOME"] = agent.Dir
	env["LOLAD_AGENT"] = agent.Name
	env["LOLAD_URL"] = o.LoladURL
	env["LOLAD_BIN"] = o.LoladBin
	env["LOLAD_STATE"] = o.StateDir
	env["LOLAD_HARNESS"] = string(a.Kind)
	env["PATH"] = filepath.Dir(o.LoladBin) + string(os.PathListSeparator) + env["PATH"]
	bin := a.Binary
	if bin == "" {
		for _, dir := range filepath.SplitList(env["PATH"]) {
			candidate := filepath.Join(dir, string(a.Kind))
			if resolved, err := exec.LookPath(candidate); err == nil {
				bin = resolved
				break
			}
		}
		if bin == "" {
			return spec, fmt.Errorf("%s: %w", a.Kind, exec.ErrNotFound)
		}
	}
	args := []string{bin}
	if !o.Fresh {
		args = append(args, "--continue")
	}
	if agent.Runtime.Model != "" {
		args = append(args, "--model", agent.Runtime.Model)
	}
	dir := filepath.Join(o.StateDir, "agents", agent.Name)
	switch a.Kind {
	case core.HarnessClaude:
		if agent.Runtime.Permissions == core.PermBypass || agent.Runtime.Permissions == "" {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
		hooks := map[string]any{}
		for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification", "PermissionRequest", "SessionEnd", "PreToolUse", "PostToolUse"} {
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "LOLAD_AGENT=" + quote(agent.Name) + " " + quote(o.LoladBin) + " hook " + event, "timeout": 3}}}}
		}
		data, _ := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
		path := filepath.Join(dir, "claude-settings.json")
		if e := managed(path, data); e != nil {
			return spec, e
		}
		args = append(args, "--setting-sources", "project,local", "--settings", path)
	case core.HarnessCodewhale:
		if agent.Runtime.Permissions == core.PermBypass || agent.Runtime.Permissions == "" {
			args = append(args, "--approval-policy", "never")
		}
		home := filepath.Join(agent.Dir, ".codewhale-home")
		if st, e := os.Stat(home); e != nil || !st.IsDir() {
			home = filepath.Join(dir, "codewhale-home")
			if e := os.MkdirAll(home, 0700); e != nil {
				return spec, e
			}
		}
		env["CODEWHALE_HOME"] = home
		if _, e := os.Stat(filepath.Join(home, "prompts", "constitution.md")); e == nil {
			env["CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE"] = "1"
		} else {
			delete(env, "CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE")
		}
		data, e := os.ReadFile(filepath.Join(home, "config.toml"))
		if e != nil && !os.IsNotExist(e) {
			return spec, e
		}
		text := string(data) + "\n# lolad managed hook overlay\n"
		for _, event := range []string{"session_start", "session_end", "message_submit", "tool_call_before", "tool_call_after", "turn_end", "session_idle", "waiting_for_user", "session_busy"} {
			cmd := "LOLAD_AGENT=" + quote(agent.Name) + " " + quote(o.LoladBin) + " hook " + event
			text += "\n[[hooks.hooks]]\nevent = " + strconv.Quote(event) + "\ncommand = " + strconv.Quote(cmd) + "\ntimeout_secs = 3\ncontinue_on_error = true\n"
		}
		path := filepath.Join(dir, "codewhale-config.toml")
		if e := managed(path, []byte(text)); e != nil {
			return spec, e
		}
		args = append(args, "--config", path)
		if o.Fresh {
			args = append(args, "--fresh")
		}
	default:
		return spec, fmt.Errorf("%w: unknown harness %q", core.ErrInvalid, a.Kind)
	}
	args = append(args, agent.Runtime.Args...)
	prompt := o.FirstPrompt
	if prompt == "" && o.Fresh {
		prompt = agent.Runtime.OnWake
	}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	spec.Argv = args
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		spec.Env = append(spec.Env, k+"="+env[k])
	}
	return spec, nil
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func Plain(pane string) string { return ansi.ReplaceAllString(pane, "") }

// InputLine returns the last composer row, excluding transcript prompt echoes.
func InputLine(pane string) int {
	lines := strings.Split(Plain(pane), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		v := strings.TrimSpace(lines[i])
		if strings.HasPrefix(v, "│❯") || strings.HasPrefix(v, "❯") {
			return i
		}
	}
	return -1
}
func (a *Adapter) PromptReady(pane string) bool {
	p := Plain(pane)
	lower := strings.ToLower(p)
	for _, bad := range []string{"enter to confirm", "do you trust", "yes, i trust", "allow once", "allow this", "esc to cancel", "waiting for approval", "approve this", "esc to interrupt", "escape to interrupt"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	lines := strings.Split(p, "\n")
	i := InputLine(p)
	if i < 0 {
		return false
	}
	if a.Kind == core.HarnessCodewhale {
		return strings.HasPrefix(strings.TrimSpace(lines[i]), "│❯") && strings.Contains(strings.Join(lines[i:], "\n"), "╰")
	}
	return i > 0 && strings.Contains(lines[i-1], "──") && i+1 < len(lines) && strings.Contains(strings.Join(lines[i+1:], "\n"), "──")
}
