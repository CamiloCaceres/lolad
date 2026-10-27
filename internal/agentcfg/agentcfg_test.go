package agentcfg

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}
func newDir(t *testing.T) *Dir {
	t.Helper()
	return New(t.TempDir(), core.HarnessClaude, Options{UserHome: t.TempDir()})
}
func TestEnvRoundtrip(t *testing.T) {
	d := newDir(t)
	put(t, d.root, ".gitignore", "# ignore\ncache/\n")
	put(t, d.root, ".env", "# token hint\nexport FIRST='old value' # keep\n\n# second\nSECOND=two\n")
	v := "space # ' \" \\ line\nnext"
	if e := d.SetEnv("", "FIRST", v); e != nil {
		t.Fatal(e)
	}
	if e := d.SetEnv("", "NEW", "new token"); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(d.root, ".env"))
	if !strings.HasPrefix(string(raw), "# token hint\nexport FIRST=") || !strings.Contains(string(raw), " # keep\n\n# second\nSECOND=two\nNEW=") {
		t.Fatal("order/comments changed")
	}
	lines, e := d.envValues("")
	if e != nil {
		t.Fatal(e)
	}
	if lines[1].value != v {
		t.Fatal("roundtrip mismatch")
	}
	list, e := d.Env("")
	if e != nil || len(list) != 3 || list[0].Key != "FIRST" || list[0].Comment != "token hint" {
		t.Fatal("bad metadata", e)
	}
	if list[0].Length != len([]rune(v)) {
		t.Fatal("bad length")
	}
	st, _ := os.Stat(filepath.Join(d.root, ".env"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("mode")
	}
	b, _ := os.ReadFile(filepath.Join(d.root, ".gitignore"))
	if !strings.Contains(string(b), "/.env\n") {
		t.Fatal("not ignored")
	}
	if e = d.DeleteEnv("", "FIRST"); e != nil {
		t.Fatal(e)
	}
	if e = d.SetEnv("", "BAD-NAME", "dont print"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("invalid key accepted")
	}
	if e = d.SetEnv("", "DEEPSEEK_API_KEY", "token"); e != nil {
		t.Fatal(e)
	}
	list, _ = d.Env("")
	if len(list[len(list)-1].UsedBy) != 1 {
		t.Fatal("provider use missing")
	}
}
func TestFiles(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "CLAUDE.md", "instructions\n")
	_ = os.Chmod(filepath.Join(d.root, "CLAUDE.md"), 0600)
	put(t, d.root, ".env.txt", "secret")
	put(t, d.root, "node_modules/x.md", "skip")
	put(t, d.root, "binary.txt", "bad\x00")
	put(t, d.root, "large.md", strings.Repeat("a", maxFile+1))
	outside := t.TempDir()
	put(t, outside, "escape.md", "escape")
	if e := os.Symlink(filepath.Join(outside, "escape.md"), filepath.Join(d.root, "escape.md")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(d.root, "escape")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"../escape.md", "escape.md", "escape/new.md", ".env.txt", "node_modules/x.md", "binary.txt", "large.md", "/tmp/no.md"} {
		if _, e := d.ReadFile(p); !errors.Is(e, core.ErrInvalid) {
			t.Fatalf("accepted %s: %v", p, e)
		}
	}
	if _, e := d.ReadFile("missing.md"); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
	f, e := d.ReadFile("CLAUDE.md")
	if e != nil || f.Group != "instructions" || f.Note == "" {
		t.Fatal("metadata", e)
	}
	if _, e = d.WriteFile("CLAUDE.md", "new", "stale"); !errors.Is(e, core.ErrConflict) {
		t.Fatal("no conflict")
	}
	g, e := d.WriteFile("CLAUDE.md", "new", f.Hash)
	if e != nil || g.Hash == f.Hash {
		t.Fatal("write", e)
	}
	st, _ := os.Stat(filepath.Join(d.root, "CLAUDE.md"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("mode lost")
	}
	list, e := d.Files()
	if e != nil || len(list) != 1 {
		t.Fatal("listing", len(list), e)
	}
}
func TestEmailSave(t *testing.T) {
	d := newDir(t)
	prefix := "# header\nagent: {name: Test}\n\n"
	suffix := "# preserved next section\npaths:\n  brain: brain\n# tail\n"
	put(t, d.root, "lolabot.yaml", prefix+"email:\n  accounts: []\n\n"+suffix)
	put(t, d.root, "brain/credentials.yaml", "other:\n  token: preserved\nemail_accounts:\n  test:\n    username: old\n    password: old-secret\n")
	put(t, d.root, "tools/email.sh", string(stockEmailScript))
	put(t, d.root, "tools/env_secret.py", "old helper")
	put(t, d.root, ".claude/settings.json", `{"enabledPlugins":{"ultron-mail@local":true}}`)
	s := core.EmailSettings{Accounts: []core.EmailAccount{{Address: "test@example.test", IMAPServer: "imap.example.test", SMTPServer: "smtp.example.test", Password: "new secret"}}, OperatorEmails: []string{"operator@example.test"}}
	got, e := d.SaveEmail("", s)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Supported || got.Watch != "mod" || len(got.Accounts) != 1 || !got.Accounts[0].PasswordSet || got.Accounts[0].Password != "" {
		t.Fatal("email metadata")
	}
	b, _ := os.ReadFile(filepath.Join(d.root, "lolabot.yaml"))
	if !bytes.HasPrefix(b, []byte(prefix)) || !bytes.HasSuffix(b, []byte("\n"+suffix)) {
		t.Fatalf("outside email block changed: %q", b)
	}
	b, _ = os.ReadFile(filepath.Join(d.root, "brain/credentials.yaml"))
	if strings.Contains(string(b), "password:") || !strings.Contains(string(b), "token: preserved") || !strings.Contains(string(b), "password_secret: EMAIL_PASSWORD_TEST") {
		t.Fatal("credentials not migrated")
	}
	p, e := d.EmailPassword("", "test@example.test")
	if e != nil || p != "new secret" {
		t.Fatal("password lookup", e)
	}
	for _, p := range []string{"tools/email.sh.lolad-orig", "tools/env_secret.py.lolad-orig"} {
		if _, e := os.Stat(filepath.Join(d.root, p)); e != nil {
			t.Fatal(e)
		}
	}
	b, _ = os.ReadFile(filepath.Join(d.root, "tools/email.sh"))
	if !bytes.Equal(b, emailScript) {
		t.Fatal("upgrade mismatch")
	}
	vars, _ := d.Env("")
	if len(vars) != 1 || len(vars[0].UsedBy) != 1 {
		t.Fatal("email use missing")
	}
	_, e = d.SaveEmail("", s)
	if e != nil {
		t.Fatal("second save", e)
	}
}
func TestEmailJuguiCopy(t *testing.T) {
	source := "../registry/testdata/jugui.yaml"
	b, e := os.ReadFile(source)
	if e != nil {
		t.Skip("reference unavailable")
	}
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", string(b))
	put(t, d.root, "brain/credentials.yaml", "keep: yes\n")
	s, e := d.Email("")
	if e != nil || len(s.Accounts) == 0 {
		t.Fatal("read reference", e)
	}
	_, n, e := d.readYAML("lolabot.yaml")
	if e != nil {
		t.Fatal(e)
	}
	start, end := blockSpan(b, n, "email")
	s.Accounts[0].Username = "test-user"
	_, e = d.SaveEmail("", s)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(d.root, "lolabot.yaml"))
	if !bytes.HasPrefix(after, b[:start]) || !bytes.HasSuffix(after, b[end:]) {
		t.Fatal("reference bytes changed")
	}
}
func TestSkillsAndFingerprint(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "agent: {name: Test}\n")
	put(t, d.home, ".claude/skills/one/SKILL.md", "---\nname: First Skill\ndescription: Some description\n---\nbody\n")
	put(t, d.home, ".claude/skills/one/data.txt", "data")
	target := t.TempDir()
	put(t, target, "linked.txt", "linked")
	if e := os.Symlink(filepath.Join(target, "linked.txt"), filepath.Join(d.home, ".claude/skills/one/link.txt")); e != nil {
		t.Fatal(e)
	}
	plugin := filepath.Join(d.home, ".claude/plugins/cache/demo/v1")
	put(t, plugin, "skills/p/SKILL.md", "# plugin\n")
	put(t, d.home, ".claude/plugins/installed_plugins.json", `{"plugins":{"demo@local":[{"scope":"user","installPath":"`+plugin+`"}]}}`)
	put(t, d.home, ".claude/settings.json", `{"enabledPlugins":{"demo@local":true}}`)
	list, e := d.Skills(core.SkillsBarebones)
	if e != nil || len(list) != 2 || list[0].Active || list[1].Active {
		t.Fatal("discovery", e)
	}
	rt := core.Runtime{Harness: core.HarnessClaude}
	sum, parts := d.Fingerprint("", rt)
	if again, _ := d.Fingerprint("", rt); sum != again {
		t.Fatal("unstable fingerprint")
	}
	skill, e := d.AddSkill("user:one")
	if e != nil || skill.Source != "agent" || skill.Name != "First Skill" || skill.Description == "" {
		t.Fatal("add", e)
	}
	st, _ := os.Lstat(filepath.Join(skill.Path, "link.txt"))
	if st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("not dereferenced")
	}
	if _, e = d.AddSkill("user:one"); !errors.Is(e, core.ErrExists) {
		t.Fatal("duplicate")
	}
	_, newparts := d.Fingerprint("", rt)
	if newparts["skills"] == parts["skills"] {
		t.Fatal("skill drift")
	}
	if e = d.RemoveSkill("user:one"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("removed user skill")
	}
	if e = d.RemoveSkill(skill.ID); e != nil {
		t.Fatal(e)
	}
	rt.Model = "new"
	_, p := d.Fingerprint("", rt)
	if p["runtime"] == parts["runtime"] {
		t.Fatal("runtime drift")
	}
	if e = d.SetEnv("", "KEY", "value"); e != nil {
		t.Fatal(e)
	}
	_, p = d.Fingerprint("", rt)
	if p["env"] == parts["env"] {
		t.Fatal("env drift")
	}
	put(t, d.root, "lolabot.yaml", "email:\n  accounts: []\n")
	_, p = d.Fingerprint("", rt)
	if p["email"] == parts["email"] {
		t.Fatal("email drift")
	}
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  test: {username: test}\n")
	_, q := d.Fingerprint("", rt)
	if q["email"] == p["email"] {
		t.Fatal("credential drift")
	}
	cw := New(t.TempDir(), core.HarnessCodewhale, Options{UserHome: d.home})
	put(t, d.home, ".codewhale/skills/cw/SKILL.md", "# cw")
	put(t, cw.root, ".codewhale-home/skills/own/SKILL.md", "# own")
	list, e = cw.Skills(core.SkillsInherit)
	if e != nil || len(list) != 3 {
		t.Fatal("codewhale discovery", e)
	}
}

func TestEnvHelperAndAliases(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "runtime:\n  env_file: tokens.txt\n")
	put(t, d.root, "tokens.txt", "TOKEN=hidden\n")
	if e := os.Symlink(filepath.Join(d.root, "tokens.txt"), filepath.Join(d.root, "alias.txt")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"tokens.txt", "alias.txt"} {
		if _, e := d.ReadFile(p); !errors.Is(e, core.ErrInvalid) {
			t.Fatal("custom env exposed", p)
		}
	}
	put(t, d.root, ".git/secret.txt", "hidden")
	if e := os.Symlink(filepath.Join(d.root, ".git/secret.txt"), filepath.Join(d.root, "git-alias.txt")); e != nil {
		t.Fatal(e)
	}
	if _, e := d.ReadFile("git-alias.txt"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("git alias exposed")
	}
	if e := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(d.root, "dangling")); e != nil {
		t.Fatal(e)
	}
	if e := d.SetEnv("dangling/new.env", "KEY", "value"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("dangling symlink allowed", e)
	}
	helper := filepath.Join(t.TempDir(), "env_secret.py")
	if e := os.WriteFile(helper, envScript, 0600); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"plain", "space # ' \" \\ tab\t line\nend", "trailing\\", "secret\"quoted", "Español 日本語"} {
		if e := d.SetEnv("", "TEST", value); e != nil {
			t.Fatal(e)
		}
		result, e := exec.Command("python3", helper, filepath.Join(d.root, ".env"), "TEST").Output()
		if e != nil {
			t.Fatal("helper failed", e)
		}
		if string(result) != value {
			t.Fatal("helper roundtrip mismatch")
		}
	}
}

func TestEmailMigrationKeepsExistingSecret(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "email:\n  accounts:\n    - address: test@example.test\n      config_key: test\n")
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  test:\n    password: obsolete\n    password_secret: EMAIL_PASSWORD_TEST\n")
	if e := d.SetEnv("", "EMAIL_PASSWORD_TEST", "current"); e != nil {
		t.Fatal(e)
	}
	s, e := d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.SaveEmail("", s); e != nil {
		t.Fatal(e)
	}
	got, e := d.EmailPassword("", "test@example.test")
	if e != nil || got != "current" {
		t.Fatal("replaced existing env password")
	}
	put(t, d.root, "tools/email.sh", "#!/bin/bash\n# custom aim-secret wrapper\n")
	original, _ := os.ReadFile(filepath.Join(d.root, "tools/email.sh"))
	if _, e = d.SaveEmail("", s); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(d.root, "tools/email.sh"))
	if !bytes.Equal(original, after) {
		t.Fatal("custom script overwritten")
	}
}

func TestCredentialsAreNotEditableAndDefaultPasswordKey(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  old: {password: hidden}\n")
	put(t, d.root, "lolabot.yaml", "agent: {name: Test}\n")
	if _, e := d.ReadFile("brain/credentials.yaml"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("credential file exposed")
	}
	if e := os.Symlink(filepath.Join(d.root, "brain/credentials.yaml"), filepath.Join(d.root, "credentials-alias.yaml")); e != nil {
		t.Fatal(e)
	}
	if _, e := d.ReadFile("credentials-alias.yaml"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("credential alias exposed")
	}
	s, e := d.SaveEmail("", core.EmailSettings{Accounts: []core.EmailAccount{{Address: "first.last@example.test", Password: "test-secret"}}})
	if e != nil {
		t.Fatal(e)
	}
	if s.Accounts[0].PasswordKey != "EMAIL_PASSWORD_FIRST_LAST" {
		t.Fatal("invalid default password env key")
	}
}

func TestEmailWrapperUsesInheritedSecrets(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "tools/email.sh", string(emailScript))
	put(t, d.root, "tools/env_secret.py", string(envScript))
	put(t, d.root, "tools/email_client.py", `import os, sys
if sys.argv[1] == "secrets-needed":
    print("EMAIL_PASSWORD_TEST")
else:
    sys.exit(0 if os.environ.get("EMAIL_PASSWORD_TEST") == "local-only-password" else 5)
`)
	cmd := exec.Command("bash", filepath.Join(d.root, "tools/email.sh"), "accounts")
	cmd.Env = append(os.Environ(), "LOLABOT_HOME="+d.root, "EMAIL_PASSWORD_TEST=local-only-password")
	if _, e := cmd.CombinedOutput(); e != nil {
		t.Fatal("wrapper did not use exported secret", e)
	}
}

func TestLinkedAgentSkillFingerprintAndRemove(t *testing.T) {
	d := newDir(t)
	outside := t.TempDir()
	put(t, outside, "SKILL.md", "# linked\n")
	put(t, d.root, ".claude/skills/placeholder/SKILL.md", "# placeholder\n")
	if e := os.Symlink(outside, filepath.Join(d.root, ".claude/skills/linked")); e != nil {
		t.Fatal(e)
	}
	_, before := d.Fingerprint("", core.Runtime{})
	stamp := time.Now().Add(2 * time.Second)
	if e := os.Chtimes(filepath.Join(outside, "SKILL.md"), stamp, stamp); e != nil {
		t.Fatal(e)
	}
	_, after := d.Fingerprint("", core.Runtime{})
	if before["skills"] == after["skills"] {
		t.Fatal("linked skill changes missed")
	}
	if e := d.RemoveSkill("agent:linked"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(outside, "SKILL.md")); e != nil {
		t.Fatal("linked target removed")
	}
}

func TestAlternateCredentialsExcluded(t *testing.T) {
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "paths:\n  credentials: private/custom.yaml\n")
	for _, p := range []string{"brain/companies-credentials.yaml", "private/custom.yaml"} {
		put(t, d.root, p, "password: hidden\n")
		if _, e := d.ReadFile(p); !errors.Is(e, core.ErrInvalid) {
			t.Fatal("alternate credentials exposed", p)
		}
	}
	if e := os.Symlink(filepath.Join(d.root, "private/custom.yaml"), filepath.Join(d.root, "custom-alias.yaml")); e != nil {
		t.Fatal(e)
	}
	if _, e := d.ReadFile("custom-alias.yaml"); !errors.Is(e, core.ErrInvalid) {
		t.Fatal("custom credential alias exposed")
	}
	files, e := d.Files()
	if e != nil || len(files) != 1 || files[0].Path != "lolabot.yaml" {
		t.Fatal("credential files listed", e)
	}
}

func TestInheritedFingerprint(t *testing.T) {
	d := newDir(t)
	rt := core.Runtime{Harness: core.HarnessClaude}
	bare := rt
	bare.Skills = core.SkillsBarebones
	_, base := d.Fingerprint("", rt)
	_, bareBase := d.Fingerprint("", bare)
	put(t, d.home, ".claude/skills/new/SKILL.md", "# new user skill\n")
	_, user := d.Fingerprint("", rt)
	_, bareUser := d.Fingerprint("", bare)
	if base["skills"] == user["skills"] {
		t.Fatal("new user skill did not change fingerprint")
	}
	if bareBase["skills"] != bareUser["skills"] {
		t.Fatal("barebones depends on user skills")
	}
	plugin := filepath.Join(d.home, ".claude/plugins/cache/plugin/v1")
	put(t, plugin, "skills/p/SKILL.md", "# plugin\n")
	put(t, d.home, ".claude/plugins/installed_plugins.json", `{"plugins":{"plugin@local":[{"scope":"user","installPath":"`+plugin+`"}]}}`)
	put(t, d.home, ".claude/settings.json", `{"enabledPlugins":{"plugin@local":true}}`)
	_, enabled := d.Fingerprint("", rt)
	if user["skills"] == enabled["skills"] {
		t.Fatal("enabled plugin missed")
	}
	stamp := time.Now().Add(time.Second)
	if e := os.Chtimes(filepath.Join(plugin, "skills/p/SKILL.md"), stamp, stamp); e != nil {
		t.Fatal(e)
	}
	_, updated := d.Fingerprint("", rt)
	if enabled["skills"] == updated["skills"] {
		t.Fatal("plugin modification missed")
	}
	put(t, d.home, ".claude/settings.json", `{"enabledPlugins":{"plugin@local":false}}`)
	_, disabled := d.Fingerprint("", rt)
	if disabled["skills"] != user["skills"] {
		t.Fatal("disabled plugin still counted")
	}
}
