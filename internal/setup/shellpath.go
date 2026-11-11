package setup

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// runFunc runs a command with exactly env as its environment and returns its
// standard output (and, on failure, whatever it printed).
type runFunc func(ctx context.Context, env []string, name string, args ...string) (string, error)

const maxOutput = 64 << 10

func defaultRun(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = nil // /dev/null
	cmd.WaitDelay = time.Second
	hideWindow(cmd)
	var out limited
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

type limited struct{ b []byte }

func (l *limited) Write(p []byte) (int, error) {
	if room := maxOutput - len(l.b); room > 0 {
		l.b = append(l.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
func (l *limited) String() string { return string(l.b) }

const (
	pathOpen  = "<<lolad-path:"
	pathClose = ":lolad-path>>"
)

// loginShellPATH asks the user's login shell for its PATH, once. Only PATH
// is ever read: the script prints one variable between markers, so noise from
// rc files (banners, "stty: not a tty", prompts) around it is ignored, and
// nothing else in the shell's environment is captured, stored or passed on.
func (m *Manager) loginShellPATH(ctx context.Context) (string, error) {
	if m.goos == "windows" {
		return m.windowsPATH(ctx)
	}
	shell := m.getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		shell = "/bin/sh"
		if m.goos == "darwin" {
			shell = "/bin/zsh"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	script := `printf '%s%s%s' '` + pathOpen + `' "$PATH" '` + pathClose + `'`
	out, err := m.run(ctx, m.minimalEnv(m.getenv("PATH")), shell, "-lic", script)
	if p := extractPATH(out); p != "" {
		return p, nil
	}
	if err == nil {
		err = errNoPATH
	}
	return "", err
}

type pathErr string

func (e pathErr) Error() string { return string(e) }

const errNoPATH = pathErr("the login shell printed no PATH")

func extractPATH(out string) string {
	i := strings.LastIndex(out, pathOpen)
	if i < 0 {
		return ""
	}
	rest := out[i+len(pathOpen):]
	j := strings.Index(rest, pathClose)
	if j < 0 {
		return ""
	}
	return cleanPATH(rest[:j], ':')
}

// cleanPATH keeps absolute, de-duplicated, single-line entries.
func cleanPATH(p string, sep rune) string {
	seen := map[string]bool{}
	var keep []string
	for _, e := range strings.FieldsFunc(p, func(r rune) bool { return r == sep }) {
		e = strings.TrimSpace(e)
		if e == "" || strings.ContainsAny(e, "\r\n\x00") || seen[e] {
			continue
		}
		if !filepath.IsAbs(e) && !(sep == ';' && len(e) > 2 && e[1] == ':') {
			continue
		}
		seen[e] = true
		keep = append(keep, e)
	}
	return strings.Join(keep, string(sep))
}

var regValue = regexp.MustCompile(`(?mi)^\s*Path\s+REG(?:_EXPAND)?_SZ\s+(.*?)\s*$`)

// windowsPATH reads the machine and user Path from the registry (what a new
// process started by Explorer would get), system first, as Windows composes it.
func (m *Manager) windowsPATH(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var parts []string
	for _, key := range []string{`HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, `HKCU\Environment`} {
		out, err := m.run(ctx, m.minimalEnv(m.getenv("PATH")), "reg", "query", key, "/v", "Path")
		if err != nil {
			continue
		}
		if p := parseRegPath(out, m.getenv); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "", errNoPATH
	}
	return cleanPATH(strings.Join(parts, ";"), ';'), nil
}

var percentVar = regexp.MustCompile(`%([^%]+)%`)

func parseRegPath(out string, getenv func(string) string) string {
	m := regValue.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return percentVar.ReplaceAllStringFunc(m[1], func(s string) string {
		if v := getenv(strings.Trim(s, "%")); v != "" {
			return v
		}
		return s
	})
}

// minimalEnv is the environment every process lolad runs for setup gets:
// enough to run, never the daemon's own variables, which may carry session
// tokens. PATH is what the caller decides.
func (m *Manager) minimalEnv(path string) []string {
	var env []string
	keys := []string{"HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "CODEX_HOME", "CODEWHALE_HOME", "CLAUDE_CONFIG_DIR"}
	if m.goos == "windows" {
		keys = append(keys, "USERPROFILE", "SystemRoot", "SYSTEMROOT", "COMSPEC", "TEMP", "TMP", "PATHEXT", "APPDATA", "LOCALAPPDATA", "USERNAME", "ProgramFiles", "ProgramData", "windir")
	}
	for _, k := range keys {
		if v := m.getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env, "PATH="+path, "TERM=xterm-256color")
	return env
}
