package harness

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ReadEnvFile parses an agent env file exactly as a launch does: blank lines
// and # comments skipped, an optional `export `, values decoded by envValue and
// ${LOLABOT_HOME} expanded to home. keys lists each name once, in file order.
func ReadEnvFile(path, home string) (env map[string]string, keys []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	env = map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, nil, fmt.Errorf("invalid env assignment")
		}
		k = strings.TrimSpace(k)
		if _, seen := env[k]; !seen {
			keys = append(keys, k)
		}
		env[k] = strings.ReplaceAll(envValue(strings.TrimSpace(v)), "${LOLABOT_HOME}", home)
	}
	return env, keys, sc.Err()
}

// EnvExport renders the agent's env file as shell for `eval`, so a running
// Claude Code session picks up .env edits in its next Bash command without a
// restart. The SessionStart hook registers it through CLAUDE_ENV_FILE. Keys
// pinned by runtime.env, PATH and lolad's own variables keep their launch
// values; keys deleted from the file since launch are unset.
func EnvExport(getenv func(string) string) (string, error) {
	path := getenv("LOLAD_ENV_FILE")
	if path == "" {
		return "", nil
	}
	env, keys, err := ReadEnvFile(path, getenv("LOLABOT_HOME"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	pinned := map[string]bool{"PATH": true, "LOLABOT_HOME": true}
	for _, k := range strings.Split(getenv("LOLAD_ENV_PINNED"), ",") {
		pinned[k] = true
	}
	skip := func(k string) bool { return pinned[k] || strings.HasPrefix(k, "LOLAD_") || !envName.MatchString(k) }
	var b strings.Builder
	for _, k := range keys {
		if !skip(k) {
			fmt.Fprintf(&b, "export %s=%s\n", k, quote(env[k]))
		}
	}
	for _, k := range strings.Split(getenv("LOLAD_ENV_KEYS"), ",") {
		if _, ok := env[k]; !ok && k != "" && !skip(k) {
			fmt.Fprintf(&b, "unset %s\n", k)
		}
	}
	return b.String(), nil
}
