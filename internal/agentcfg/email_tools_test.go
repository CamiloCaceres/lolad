package agentcfg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Stand-ins for lolabot's template files. The real ones are recognised by hash
// (TestEmailToolsKnownTemplate checks those against a lolabot checkout); these
// are registered as stock for the duration of a test.
const (
	fakeTemplateWrapper = "#!/bin/bash\n# template wrapper\npython3 \"$(dirname \"$0\")/email_client.py\" \"$@\"\n"
	fakeTemplateClient  = "# template client: reads config['password']\n"
	fakeTemplateConfig  = "# template config: credentials default brain/companies-credentials.yaml\n"
	fakeTemplateSend    = "#!/bin/bash\n# template send wrapper with a fixed sender\n"
)

func withStock(t *testing.T, rel, content string) {
	t.Helper()
	for i := range emailTools {
		if emailTools[i].rel == rel {
			old := emailTools[i].stock
			emailTools[i].stock = append(append([]string{}, old...), contentHash([]byte(content)))
			t.Cleanup(func() { emailTools[i].stock = old })
			return
		}
	}
	t.Fatal("unknown tool", rel)
}

func templateAgent(t *testing.T) *Dir {
	t.Helper()
	withStock(t, "tools/email.sh", fakeTemplateWrapper)
	withStock(t, "tools/email_client.py", fakeTemplateClient)
	withStock(t, "tools/config.py", fakeTemplateConfig)
	withStock(t, "tools/email-send.sh", fakeTemplateSend)
	d := newDir(t)
	put(t, d.root, "lolabot.yaml", "email:\n  accounts:\n    - address: agent@example.test\n      config_key: agent\n")
	put(t, d.root, "tools/email.sh", fakeTemplateWrapper)
	put(t, d.root, "tools/email_client.py", fakeTemplateClient)
	put(t, d.root, "tools/config.py", fakeTemplateConfig)
	put(t, d.root, "tools/email-send.sh", fakeTemplateSend)
	put(t, d.root, "tools/email_sanitizer.py", "# sanitizer\n")
	return d
}

// lolad-managed: the login names its password and the env file has it.
func managedEmail(t *testing.T, d *Dir) {
	t.Helper()
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  agent:\n    username: agent@example.test\n    password_secret: EMAIL_PASSWORD_AGENT\n")
	if e := d.SetEnv("", "EMAIL_PASSWORD_AGENT", "not-a-real-password"); e != nil {
		t.Fatal(e)
	}
}

func read(t *testing.T, d *Dir, rel string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(d.root, rel))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(filepath.Join(root, "tools"), func(p string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			out[strings.TrimPrefix(p, root+"/")] = info.Mode().String() + info.ModTime().String() + string(b)
		}
		return nil
	})
	return out
}

func hasNote(notes []string, part string) bool {
	for _, n := range notes {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

func TestEmailToolsTemplateUpgradedWithBackups(t *testing.T) {
	d := templateAgent(t)
	managedEmail(t, d)
	put(t, d.root, "tools/config.py.lolad-orig", "an earlier backup\n")
	s, e := d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	if !s.Supported || !hasNote(s.Notes, "Updated tools/config.py, tools/attachment_guard.py, tools/env_secret.py, tools/email_client.py, tools/email.sh, tools/email-send.sh") {
		t.Fatalf("notes: %q", s.Notes)
	}
	for _, tool := range emailTools {
		if read(t, d, tool.rel) != string(tool.asset) {
			t.Fatal("not installed", tool.rel)
		}
		st, _ := os.Stat(filepath.Join(d.root, tool.rel))
		if st.Mode().Perm() != tool.mode {
			t.Fatal("mode", tool.rel, st.Mode())
		}
	}
	for rel, want := range map[string]string{
		"tools/email.sh.lolad-orig":        fakeTemplateWrapper,
		"tools/email_client.py.lolad-orig": fakeTemplateClient,
		"tools/config.py.lolad-orig":       "an earlier backup\n", // never overwritten
		"tools/email-send.sh.lolad-orig":   fakeTemplateSend,
	} {
		if read(t, d, rel) != want {
			t.Fatal("backup", rel)
		}
	}
	for _, rel := range []string{"tools/env_secret.py.lolad-orig", "tools/attachment_guard.py.lolad-orig"} {
		if _, e := os.Stat(filepath.Join(d.root, rel)); !os.IsNotExist(e) {
			t.Fatal("backup of a file that did not exist", rel)
		}
	}
	if read(t, d, "tools/email_sanitizer.py") != "# sanitizer\n" {
		t.Fatal("sanitizer touched")
	}
	// Up to date now: a second read changes nothing and says nothing.
	before := snapshot(t, d.root)
	s, _ = d.Email("")
	if len(s.Notes) != 0 || !equalMaps(before, snapshot(t, d.root)) {
		t.Fatalf("second read: %q", s.Notes)
	}
}

func TestEmailToolsTemplateWaitsForSaveWithoutManagedSecret(t *testing.T) {
	d := templateAgent(t)
	put(t, d.root, "brain/credentials.yaml", "email_accounts:\n  agent:\n    username: agent@example.test\n    password: legacy\n")
	before := snapshot(t, d.root)
	s, e := d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	if !equalMaps(before, snapshot(t, d.root)) || !hasNote(s.Notes, "lolabot's template version") {
		t.Fatalf("read path wrote without a managed secret: %q", s.Notes)
	}
	s, e = d.SaveEmail("", s)
	if e != nil {
		t.Fatal(e)
	}
	if read(t, d, "tools/email.sh") != string(emailScript) || read(t, d, "tools/email.sh.lolad-orig") != fakeTemplateWrapper || !hasNote(s.Notes, "Updated tools/") {
		t.Fatalf("save did not upgrade: %q", s.Notes)
	}
}

func TestEmailToolsCustomisedLeftAlone(t *testing.T) {
	for name, setup := range map[string]func(*Dir){
		"custom wrapper": func(d *Dir) { put(t, d.root, "tools/email.sh", fakeTemplateWrapper+"# local change\n") },
		"custom client":  func(d *Dir) { put(t, d.root, "tools/email_client.py", fakeTemplateClient+"# local change\n") },
		"custom guard":   func(d *Dir) { put(t, d.root, "tools/attachment_guard.py", "# someone else's guard\n") },
	} {
		t.Run(name, func(t *testing.T) {
			d := templateAgent(t)
			managedEmail(t, d)
			setup(d)
			before := snapshot(t, d.root)
			s, e := d.Email("")
			if e != nil {
				t.Fatal(e)
			}
			if !hasNote(s.Notes, "has local changes") {
				t.Fatalf("notes: %q", s.Notes)
			}
			if _, e = d.SaveEmail("", s); e != nil {
				t.Fatal(e)
			}
			if !equalMaps(before, snapshot(t, d.root)) {
				t.Fatal("customised tool set changed")
			}
		})
	}
	// A customised optional wrapper does not block the rest, and is kept.
	d := templateAgent(t)
	managedEmail(t, d)
	put(t, d.root, "tools/email-send.sh", "#!/bin/bash\n# mine\n")
	s, _ := d.Email("")
	if read(t, d, "tools/email.sh") != string(emailScript) || read(t, d, "tools/email-send.sh") != "#!/bin/bash\n# mine\n" || !hasNote(s.Notes, "email-send.sh has local changes") {
		t.Fatalf("optional tool: %q", s.Notes)
	}
}

func TestEmailToolsPreviousLoladAssetRefreshed(t *testing.T) {
	old, e := os.ReadFile("testdata/email-lolad-v1.sh")
	if e != nil {
		t.Fatal(e)
	}
	if !contains(toolWrapper.shipped, contentHash(old)) {
		t.Fatal("testdata/email-lolad-v1.sh is not listed as a shipped version")
	}
	d := newDir(t)
	managedEmail(t, d)
	put(t, d.root, "tools/email.sh", string(old))
	put(t, d.root, "tools/email.sh.lolad-orig", string(stockEmailScript)) // from the first upgrade
	put(t, d.root, "tools/env_secret.py", string(envScript))
	client := "# an aim-secret era client that knows password_secret\n"
	put(t, d.root, "tools/email_client.py", client)
	s, e := d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	if read(t, d, "tools/email.sh") != string(emailScript) || !hasNote(s.Notes, "Updated tools/email.sh to read") {
		t.Fatalf("not refreshed: %q", s.Notes)
	}
	if read(t, d, "tools/email.sh.lolad-orig") != string(stockEmailScript) || read(t, d, "tools/email_client.py") != client {
		t.Fatal("refresh touched more than the old lolad file")
	}
	for _, rel := range []string{"tools/config.py", "tools/attachment_guard.py", "tools/email-send.sh"} {
		if _, e := os.Stat(filepath.Join(d.root, rel)); !os.IsNotExist(e) {
			t.Fatal("refresh added", rel)
		}
	}
}

func TestEmailToolsCurrentUntouched(t *testing.T) {
	d := newDir(t)
	managedEmail(t, d)
	for _, tool := range emailTools {
		put(t, d.root, tool.rel, string(tool.asset))
	}
	put(t, d.root, "tools/email_sanitizer.py", "# sanitizer\n")
	stamp := time.Now().Add(-time.Hour)
	for _, tool := range emailTools {
		if e := os.Chtimes(filepath.Join(d.root, tool.rel), stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	before := snapshot(t, d.root)
	s, e := d.Email("")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.SaveEmail("", s); e != nil {
		t.Fatal(e)
	}
	if !equalMaps(before, snapshot(t, d.root)) {
		t.Fatal("current tools rewritten")
	}
	if hasNote(s.Notes, "tools") {
		t.Fatalf("notes: %q", s.Notes)
	}
}

func TestEmailToolsHashLists(t *testing.T) {
	for _, tool := range emailTools {
		cur := contentHash(tool.asset)
		if contains(tool.stock, cur) || contains(tool.shipped, cur) {
			t.Fatal("current asset listed as an old version", tool.rel)
		}
	}
	if contentHash(stockEmailScript) == contentHash(emailScript) {
		t.Fatal("aim-secret wrapper equals the asset")
	}
}

// LOLABOT_DIR points at a lolabot checkout (read only): its tools must be
// recognised as the template set, and the upgrade runs on a temporary copy.
func TestEmailToolsKnownTemplate(t *testing.T) {
	src := os.Getenv("LOLABOT_DIR")
	if src == "" {
		t.Skip("set LOLABOT_DIR to a lolabot checkout")
	}
	d := newDir(t)
	managedEmail(t, d)
	for _, rel := range []string{"tools/email.sh", "tools/email_client.py", "tools/config.py", "tools/email-send.sh", "tools/email_sanitizer.py"} {
		b, e := os.ReadFile(filepath.Join(src, rel))
		if e != nil {
			t.Fatal(e)
		}
		put(t, d.root, rel, string(b))
	}
	p, e := d.planEmail()
	if e != nil || p.kind != planTemplate || p.blocked != "" || len(p.writes) != len(emailTools) {
		t.Fatalf("plan %+v %v", p, e)
	}
	if _, e = d.Email(""); e != nil {
		t.Fatal(e)
	}
	if read(t, d, "tools/email_client.py") != string(emailClientScript) {
		t.Fatal("not upgraded")
	}
}

// The installed scripts, run for real: secret names and the account list need no
// network. Skipped where python3 has no PyYAML (the bootstrap would install it).
func TestEmailToolsScriptsRun(t *testing.T) {
	if exec.Command("python3", "-c", "import yaml").Run() != nil {
		t.Skip("python3 without PyYAML")
	}
	d := newDir(t)
	managedEmail(t, d)
	put(t, d.root, "lolabot.yaml", "email:\n  accounts:\n    - address: agent@example.test\n      config_key: agent\n      imap_server: imap.example.test\n      smtp_server: smtp.example.test\n")
	put(t, d.root, "tools/email_sanitizer.py", "def sanitize_email(*a, **k): pass\ndef assess_attachment(*a, **k): pass\ndef format_security_summary(*a, **k): pass\n")
	for _, tool := range emailTools {
		put(t, d.root, tool.rel, string(tool.asset))
		_ = os.Chmod(filepath.Join(d.root, tool.rel), tool.mode)
	}
	run := func(args ...string) string {
		cmd := exec.Command(filepath.Join(d.root, "tools/email.sh"), args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(args, e)
		}
		if bytes.Contains(out, []byte("not-a-real-password")) {
			t.Fatal("secret printed")
		}
		return string(out)
	}
	if got := run("secrets-needed"); got != "EMAIL_PASSWORD_AGENT\n" {
		t.Fatalf("secrets-needed: %q", got)
	}
	if got := run("accounts"); !strings.Contains(got, "agent@example.test") {
		t.Fatalf("accounts: %q", got)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
