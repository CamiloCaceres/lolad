package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// harnessSpec describes one supported runtime.
type harnessSpec struct {
	id, name string
	bin      string // executable base name
	nodeCLI  bool   // an npm package whose launcher needs node
}

var harnessSpecs = []harnessSpec{
	{id: "claude", name: "Claude Code", bin: "claude"},
	{id: "codex", name: "Codex", bin: "codex", nodeCLI: true},
	{id: "codewhale", name: "Codewhale", bin: "codewhale"},
}

// configBinKey is the config.yaml key naming a harness's absolute path.
func configBinKey(id string) string { return id + "_bin" }

// canInstall and canLogin say which terminal actions exist for a harness.
func canInstall(id string) bool { return id == "claude" || id == "codex" }
func canLogin(id string) bool   { return true }

func installHint(goos, id string) string {
	switch id {
	case "claude":
		if goos == "windows" {
			return `Run in PowerShell: irm https://claude.ai/install.ps1 | iex`
		}
		return "Run: curl -fsSL https://claude.ai/install.sh | bash"
	case "codex":
		return "Run: npm install -g @openai/codex (needs Node.js; install it from nodejs.org first if `node` is not found)"
	case "codewhale":
		return "Install Codewhale from its project page, make sure `codewhale` runs in a terminal, then re-scan."
	}
	return ""
}

// searchDirs is every directory a harness binary may live in, most specific
// first: the captured login PATH, then the daemon's own PATH, then the usual
// places installers use.
func (m *Manager) searchDirs(captured string, npmPrefix string) []string {
	var dirs []string
	add := func(d string) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	for _, d := range filepath.SplitList(captured) {
		add(d)
	}
	for _, d := range filepath.SplitList(m.getenv("PATH")) {
		add(d)
	}
	h := m.home
	add(filepath.Join(h, ".local", "bin"))
	add(filepath.Join(h, ".claude", "local"))
	add(filepath.Join(h, ".npm-global", "bin"))
	add(filepath.Join(h, ".bun", "bin"))
	add(filepath.Join(h, ".volta", "bin"))
	add(filepath.Join(h, ".cargo", "bin"))
	switch m.goos {
	case "windows":
		local := m.getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(h, "AppData", "Local")
		}
		add(filepath.Join(local, "Programs"))
		for _, s := range harnessSpecs {
			add(filepath.Join(local, "Programs", s.name))
			add(filepath.Join(local, "Programs", s.id))
		}
		if a := m.getenv("APPDATA"); a != "" {
			add(filepath.Join(a, "npm"))
		}
		add(npmPrefix)
	default:
		if !m.noSystemDirs {
			add("/opt/homebrew/bin")
			add("/usr/local/bin")
			add("/usr/bin")
		}
		if npmPrefix != "" {
			add(filepath.Join(npmPrefix, "bin"))
		}
	}
	return uniq(dirs)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// find returns the first executable called bin in dirs.
func (m *Manager) find(dirs []string, bin string) string {
	names := []string{bin}
	if m.goos == "windows" {
		names = []string{bin + ".exe", bin + ".cmd", bin + ".bat", bin}
	}
	for _, d := range dirs {
		for _, n := range names {
			p := filepath.Join(d, n)
			if executable(p, m.goos) {
				return p
			}
		}
	}
	return ""
}

func executable(p, goos string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return goos == "windows" || fi.Mode().Perm()&0o111 != 0
}

var versionRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?`)

func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if v := versionRe.FindString(line); v != "" {
			return v
		}
	}
	return ""
}

// npmPrefix asks npm where global packages go, when npm can be found.
func (m *Manager) npmPrefix(ctx context.Context, dirs []string, captured string) string {
	if p := m.getenv("NPM_CONFIG_PREFIX"); p != "" {
		return p
	}
	npm := m.find(dirs, "npm")
	if npm == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := m.run(ctx, m.minimalEnv(strings.Join(dirs, string(os.PathListSeparator))), npm, "config", "get", "prefix")
	if err != nil {
		return ""
	}
	out = strings.TrimSpace(out)
	if i := strings.LastIndex(out, "\n"); i >= 0 {
		out = strings.TrimSpace(out[i+1:])
	}
	if !filepath.IsAbs(out) {
		return ""
	}
	return out
}

// detect scans for every harness. configured maps a harness ID to the path
// config.yaml already names (it is tried first when it still exists).
func (m *Manager) detect(ctx context.Context, captured string, configured map[string]string) []Harness {
	dirs := m.searchDirs(captured, "")
	// npm's prefix only matters when something is still missing.
	missing := false
	for _, s := range harnessSpecs {
		if configured[s.id] == "" || !executable(configured[s.id], m.goos) {
			if m.find(dirs, s.bin) == "" {
				missing = true
			}
		}
	}
	if missing {
		if p := m.npmPrefix(ctx, dirs, captured); p != "" {
			dirs = m.searchDirs(captured, p)
		}
	}
	out := make([]Harness, 0, len(harnessSpecs))
	for _, s := range harnessSpecs {
		h := Harness{ID: s.id, Name: s.name, InstallHint: installHint(m.goos, s.id), CanInstall: canInstall(s.id), CanLogin: canLogin(s.id)}
		path := ""
		if c := configured[s.id]; c != "" && executable(c, m.goos) {
			path = c
		}
		if path == "" {
			path = m.find(dirs, s.bin)
		}
		if path == "" {
			out = append(out, h)
			continue
		}
		h.Installed, h.Path = true, path
		run := strings.Join(uniq(append([]string{filepath.Dir(path)}, dirs...)), string(os.PathListSeparator))
		vctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		vout, err := m.run(vctx, m.minimalEnv(run), path, "--version")
		cancel()
		h.Version = parseVersion(vout)
		switch {
		case err != nil && h.Version == "":
			h.Problem = "Found, but running it with --version failed. Try it in a terminal."
		case s.nodeCLI && m.needsNode(path) && m.find(dirs, "node") == "":
			h.Problem = "Needs Node.js, which was not found. Install it from nodejs.org, then re-scan."
		}
		h.LoggedIn, h.LoginChecked = m.loggedIn(ctx, s.id, path, run)
		h.Ready = h.Problem == "" && (h.LoggedIn || !h.LoginChecked)
		out = append(out, h)
	}
	return out
}

// needsNode reports whether path is a script that runs under node: an npm
// shim, or a file whose first line says so.
func (m *Manager) needsNode(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	if strings.Contains(filepath.ToSlash(real), "/node_modules/") {
		return true
	}
	f, err := os.Open(real)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 128)
	n, _ := f.Read(buf)
	first, _, _ := strings.Cut(string(buf[:n]), "\n")
	return strings.HasPrefix(first, "#!") && strings.Contains(first, "node")
}

// loggedIn is best effort and never reads a secret into memory beyond what is
// needed to see that one exists. checked is false when lolad cannot tell.
func (m *Manager) loggedIn(ctx context.Context, id, bin, path string) (in, checked bool) {
	switch id {
	case "claude":
		return m.claudeLoggedIn(ctx, bin, path), true
	case "codex":
		return m.codexLoggedIn(), true
	default:
		return m.codewhaleLoggedIn()
	}
}

func (m *Manager) claudeLoggedIn(ctx context.Context, bin, path string) bool {
	// Newer versions answer `claude auth status`; an exit status of 0 with
	// loggedIn:true is authoritative. Older ones lack the subcommand, so fall
	// back to the files and keychain entry a login leaves.
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	out, err := m.run(sctx, m.minimalEnv(path), bin, "auth", "status")
	cancel()
	if err == nil {
		var v struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &v) == nil && v.LoggedIn != nil {
			return *v.LoggedIn
		}
	}
	cfg := m.getenv("CLAUDE_CONFIG_DIR")
	if cfg == "" {
		cfg = filepath.Join(m.home, ".claude")
	}
	if fi, err := os.Stat(filepath.Join(cfg, ".credentials.json")); err == nil && fi.Size() > 2 {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(m.home, ".claude.json")); err == nil && strings.Contains(string(b), `"oauthAccount"`) {
		return true
	}
	if m.goos == "darwin" {
		kctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		// Without -w/-g the secret is never printed; only the exit status matters.
		if _, err := m.run(kctx, m.minimalEnv(path), "/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials"); err == nil {
			return true
		}
	}
	return false
}

func (m *Manager) codexLoggedIn() bool {
	home := m.getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(m.home, ".codex")
	}
	b, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return false
	}
	var v struct {
		APIKey string         `json:"OPENAI_API_KEY"`
		Tokens map[string]any `json:"tokens"`
	}
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	return v.APIKey != "" || len(v.Tokens) > 0
}

// codewhaleLoggedIn looks for the credential files such CLIs usually keep. It
// cannot be sure, so a miss is reported as "unknown" and does not block.
func (m *Manager) codewhaleLoggedIn() (in, checked bool) {
	home := m.getenv("CODEWHALE_HOME")
	if home == "" {
		home = filepath.Join(m.home, ".codewhale")
	}
	for _, n := range []string{"auth.json", "credentials.json", ".credentials.json"} {
		if fi, err := os.Stat(filepath.Join(home, n)); err == nil && fi.Size() > 2 {
			return true, true
		}
	}
	return false, false
}
