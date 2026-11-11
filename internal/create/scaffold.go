package create

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Scaffold is a native Go port of lolabot's setup.sh for a NEW instance. It
// needs no Bash, sed, cp, git or Python, so it works on a fresh Windows or Mac.
//
// It reproduces setup.sh as of the revision whose digest is listed in
// knownSetupSHA256: the same directory tree, the same placeholder
// substitutions (applied in the same order), the same no-clobber copies and
// the same starter files. Documented differences are listed in
// docs/creation-integration.md ("Native scaffold").

// ScaffoldParams are the inputs of setup.sh. Zero values take the same defaults
// setup.sh uses with --yes, except that system facts are detected natively.
type ScaffoldParams struct {
	Name, Role, User string
	// Dir is the instance directory (TARGET_DIR).
	Dir string

	Language, NativeLanguage     string // default English / Language
	SystemName, OS, RAM, Storage string // detected when empty
	Hardware, Timezone           string // detected when empty
	UserEmail, AgentEmail        string // default user@example.com / agent@example.com
	Now                          time.Time
}

// knownSetupSHA256 lists digests (line endings normalised to LF) of the
// upstream setup.sh revisions this port reproduces.
var knownSetupSHA256 = map[string]string{
	"eff643128f9bd090600d7dfef72b11121d174ad6bef442ac5bfcb58e423ec0f1": "lolabot 2025-10-06 (setup.sh with --yes, --name, --role, --user)",
}

// SetupRecognized reports whether the template's setup.sh is a revision the Go
// port reproduces. A template without setup.sh is recognized: the Go port needs
// only CLAUDE.TEMPLATE.md.
func SetupRecognized(templateDir string) (recognized bool, present bool, err error) {
	data, err := os.ReadFile(filepath.Join(templateDir, "setup.sh"))
	if os.IsNotExist(err) {
		return true, false, nil
	}
	if err != nil {
		return false, true, err
	}
	sum := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	_, ok := knownSetupSHA256[hex.EncodeToString(sum[:])]
	return ok, true, nil
}

func yamlQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// Scaffold creates the instance in p.Dir from templateDir. Like setup.sh it
// refuses a directory that already holds a CLAUDE.md, never overwrites files
// copied from the template (cp -n) and writes CLAUDE.md, lolabot.yaml and
// .gitignore unconditionally.
func Scaffold(templateDir string, p ScaffoldParams) error {
	templateFile := filepath.Join(templateDir, "CLAUDE.TEMPLATE.md")
	tmpl, err := os.ReadFile(templateFile)
	if err != nil {
		return fmt.Errorf("%w: CLAUDE.TEMPLATE.md not found in %s", core.ErrInvalid, templateDir)
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.User) == "" {
		return fmt.Errorf("%w: name and user are required", core.ErrInvalid)
	}
	for _, v := range []string{p.Name, p.Role, p.User, p.Language, p.NativeLanguage, p.SystemName, p.OS, p.RAM, p.Storage, p.Hardware, p.Timezone, p.UserEmail, p.AgentEmail, p.Dir} {
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("%w: setup fields must be single-line", core.ErrInvalid)
		}
	}
	for _, e := range []string{p.UserEmail, p.AgentEmail} {
		if strings.ContainsAny(e, `/\:`) || e == "." || e == ".." {
			return fmt.Errorf("%w: email %q is not usable as a directory name", core.ErrInvalid, e)
		}
	}
	target, err := resolvePath(p.Dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(target, "CLAUDE.md")); err == nil {
		return fmt.Errorf("%w: %s already contains a CLAUDE.md file", core.ErrExists, target)
	}

	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	role := def(p.Role, "Personal Assistant")
	lang := def(p.Language, "English")
	native := def(p.NativeLanguage, lang)
	sys := detectSystem()
	systemName := def(p.SystemName, sys.Name)
	osName := def(p.OS, sys.OS)
	ram := def(p.RAM, sys.RAM)
	storage := def(p.Storage, sys.Storage)
	hardware := def(p.Hardware, sys.Hardware)
	tz := def(p.Timezone, sys.Timezone)
	email1 := def(p.UserEmail, "user@example.com")
	email2 := def(p.AgentEmail, "agent@example.com")

	first := p.User
	if i := strings.IndexByte(first, ' '); i >= 0 {
		first = first[:i]
	}
	firstLower := strings.ToLower(first)
	profileFile := firstLower + "-profile.md"
	homeDir := filepath.Dir(target)
	instanceName := filepath.Base(target)
	agentID := "pa-" + strings.ReplaceAll(strings.ToLower(p.Name), " ", "-")
	year := now.Format("2006")

	// Directory structure.
	dirs := []string{"brain", "memory", "indexes"}
	for _, e := range []string{email1, email2} {
		for _, sub := range []string{"inbox", "sent", "drafts", "attachments"} {
			dirs = append(dirs, filepath.Join("emails", e, sub))
		}
	}
	dirs = append(dirs, filepath.Join("articles", "drafts"), filepath.Join("articles", "published"),
		"prompts", "tools", "scripts", "skills", "tests", "docs")
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(target, d), 0o755); err != nil {
			return err
		}
	}

	// cp -n tools/* : regular files directly inside tools/ (shell globs skip
	// dotfiles; cp without -r skips directories). Symlinks are followed.
	if err := copyFlat(filepath.Join(templateDir, "tools"), filepath.Join(target, "tools")); err != nil {
		return err
	}
	// cp -rn skills/* : every non-dot top-level entry, recursively.
	if err := copySkills(filepath.Join(templateDir, "skills"), filepath.Join(target, "skills")); err != nil {
		return err
	}
	// Brain templates and the memory README: only these named files, no clobber.
	for _, f := range []string{"eisenhower.md", "agents.md", "README.md"} {
		if err := copyFileNoClobber(filepath.Join(templateDir, "brain", f), filepath.Join(target, "brain", f), true); err != nil {
			return err
		}
	}
	if err := copyFileNoClobber(filepath.Join(templateDir, "memory", "README.md"), filepath.Join(target, "memory", "README.md"), true); err != nil {
		return err
	}

	yaml := "# lolabot.yaml — PA instance configuration\n" +
		"# Generated by lolabot setup.sh on " + now.Format(time.RFC3339) + "\n" +
		"\n" +
		"agent:\n" +
		"  name: \"" + yamlQuote(p.Name) + "\"\n" +
		"  role: \"" + yamlQuote(role) + "\"\n" +
		"  id: \"" + yamlQuote(agentID) + "\"\n" +
		"\n" +
		"user:\n" +
		"  name: \"" + yamlQuote(p.User) + "\"\n" +
		"  preferred_language: \"" + yamlQuote(lang) + "\"\n" +
		"  native_language: \"" + yamlQuote(native) + "\"\n" +
		"  profile_file: \"" + yamlQuote(profileFile) + "\"\n" +
		"\n" +
		"system:\n" +
		"  name: \"" + yamlQuote(systemName) + "\"\n" +
		"  hardware: \"" + yamlQuote(hardware) + "\"\n" +
		"  os: \"" + yamlQuote(osName) + "\"\n" +
		"  ram: \"" + yamlQuote(ram) + "\"\n" +
		"  storage: \"" + yamlQuote(storage) + "\"\n" +
		"  timezone: \"" + yamlQuote(tz) + "\"\n" +
		"\n" +
		"email:\n" +
		"  account_1: \"" + yamlQuote(email1) + "\"\n" +
		"  account_2: \"" + yamlQuote(email2) + "\"\n" +
		"\n" +
		"paths:\n" +
		"  home_dir: \"" + yamlQuote(homeDir) + "\"\n" +
		"  instance_dir: \"" + yamlQuote(target) + "\"\n" +
		"  instance_name: \"" + yamlQuote(instanceName) + "\"\n"
	if err := os.WriteFile(filepath.Join(target, "lolabot.yaml"), []byte(yaml), 0o644); err != nil {
		return err
	}

	// CLAUDE.md: sequential literal replacement, in setup.sh's sed order, so a
	// value that itself contains a later placeholder behaves identically.
	claude := string(tmpl)
	for _, kv := range [][2]string{
		{"AGENT_NAME", p.Name}, {"AGENT_ROLE", role}, {"AGENT_ID", agentID}, {"USER_NAME", p.User},
		{"USER_PREFERRED_LANGUAGE", lang}, {"USER_NATIVE_LANGUAGE", native},
		{"USER_FIRST_NAME_LOWER", firstLower}, {"USER_PROFILE_FILE", profileFile},
		{"SYSTEM_NAME", systemName}, {"SYSTEM_HARDWARE", hardware}, {"SYSTEM_OS", osName},
		{"SYSTEM_RAM", ram}, {"SYSTEM_STORAGE", storage}, {"SYSTEM_TIMEZONE", tz},
		{"HOME_DIR", homeDir}, {"INSTANCE_DIR", target}, {"INSTANCE_NAME", instanceName},
		{"EMAIL_ACCOUNT_1", email1}, {"EMAIL_ACCOUNT_2", email2}, {"YEAR", year},
	} {
		claude = strings.ReplaceAll(claude, "{{"+kv[0]+"}}", kv[1])
	}
	if err := os.WriteFile(filepath.Join(target, "CLAUDE.md"), []byte(claude), 0o644); err != nil {
		return err
	}

	// sed -i on the two brain files (in place, mode preserved).
	for _, f := range []string{"eisenhower.md", "agents.md"} {
		path := filepath.Join(target, "brain", f)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := strings.ReplaceAll(string(data), "{{AGENT_NAME}}", p.Name)
		s = strings.ReplaceAll(s, "{{USER_NAME}}", p.User)
		if err := os.WriteFile(path, []byte(s), info.Mode().Perm()); err != nil {
			return err
		}
	}

	// Starter memory files, only when absent.
	starters := []struct{ name, body string }{
		{profileFile, "# " + p.User + " - Profile\n\n## Basic Info\n- **Name:** " + p.User +
			"\n- **Preferred language:** " + lang + "\n- **Native language:** " + native +
			"\n\n## Family\n\n<!-- Add family members here -->\n\n## Background\n\n<!-- Add background information here -->\n\n## Health\n\n<!-- Add health information here -->\n\n## Preferences\n\n<!-- Add preferences here -->\n"},
		{"goals.md", "# Goals\n\n" + p.User + "'s current goals and priorities.\n\n**Last updated:** " + now.Format("2006-01-02") +
			"\n\n---\n\n## Active Goals\n\n<!-- Add goals here -->\n\n---\n\n## Completed Goals\n\n(None yet)\n"},
		{"journal-" + year + ".md", "# Journal " + year + "\n\nLife events and milestones for " + p.User + ".\n\n---\n\n## " +
			now.Month().String() + " " + year + "\n\n- **" + now.Format("2006-01-02") + ":** PA instance set up with lolabot framework\n"},
	}
	for _, s := range starters {
		path := filepath.Join(target, "memory", s.name)
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(s.body), 0o644); err != nil {
			return err
		}
	}

	if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		return err
	}
	return nil
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

const gitignore = `# Credentials & secrets
brain/credentials.yaml
brain/*-credentials.yaml
brain/*-credentials.yml
*.env
.env
.env.*

# Python
.venv/
__pycache__/
*.pyc
*.pyo
*.egg-info/
dist/
build/

# Indexes (large binary files - rebuild from tools)
indexes/*.mv2
indexes/*.db

# Email cache (can be re-synced)
emails/*/inbox/
emails/*/sent/
emails/*/drafts/
emails/*/attachments/

# OS
.DS_Store
Thumbs.db
*.swp
*.swo
*~

# IDE
.idea/
.vscode/
*.code-workspace

# Logs
*.log
/tmp/

# Node
node_modules/
`

// copyFlat is "cp -n src/* dst/" without -r.
func copyFlat(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := copyFileNoClobber(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), true); err != nil {
			return err
		}
	}
	return nil
}

// copySkills is "cp -rn src/* dst/".
func copySkills(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := copyTreeNoClobber(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyTreeNoClobber(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		if _, err := os.Lstat(dst); err == nil {
			return nil
		}
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(link, dst); err != nil {
			// Windows without symlink rights: fall back to a content copy.
			return copyFileNoClobber(src, dst, true)
		}
		return nil
	case info.IsDir():
		// Source permission bits with the umask applied by the OS, like cp -r.
		if err := os.MkdirAll(dst, info.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTreeNoClobber(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		return copyFileNoClobber(src, dst, false)
	}
	return nil // sockets, devices: cp -r would fail; setup.sh ignores failures
}

// copyFileNoClobber copies a regular file (following symlinks when follow is
// true) keeping its permission bits, and leaves an existing destination alone.
// A missing source is silently skipped, as in setup.sh (`cp -n … || true`).
func copyFileNoClobber(src, dst string, follow bool) error {
	var info fs.FileInfo
	var err error
	if follow {
		info, err = os.Stat(src)
	} else {
		info, err = os.Lstat(src)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil // directories are skipped by a non-recursive cp
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// resolvePath is `realpath -m`: absolute, with symlinks resolved for the part
// that exists (macOS /var -> /private/var, a linked projects folder).
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	cur := abs
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
