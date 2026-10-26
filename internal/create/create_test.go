package create

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"gopkg.in/yaml.v3"
)

type services struct {
	org   core.Org
	calls []string
	sent  core.Outgoing
	from  string
	fail  string
}

func (s *services) WriteRuntime(dir string, r core.Runtime) error {
	s.calls = append(s.calls, "runtime")
	if s.fail == "runtime" {
		return errors.New("runtime failed")
	}
	path := filepath.Join(dir, "lolabot.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Stand-in for registry's byte-preserving runtime writer.
	if i := strings.Index(string(data), "\nruntime:\n"); i >= 0 {
		data = data[:i+1]
	}
	block, err := yaml.Marshal(struct {
		Runtime core.Runtime `yaml:"runtime"`
	}{r})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(append(data, '\n'), block...), 0644)
}
func (s *services) EnsureIdentity(_ context.Context, name, kind string) (core.Identity, error) {
	s.calls = append(s.calls, "identity")
	if s.fail == "identity" {
		return core.Identity{}, errors.New("identity failed")
	}
	return core.Identity{Name: name, Kind: kind, Address: name + "@test.lolad.local"}, nil
}
func (s *services) Org(context.Context) (core.Org, error) { return s.org, nil }
func (s *services) SaveOrg(_ context.Context, o core.Org) error {
	s.calls = append(s.calls, "org")
	s.org = o
	return nil
}
func (s *services) Contacts(string) []string { return []string{"zeta", "milo", "alpha"} }
func (s *services) Send(_ context.Context, from string, out core.Outgoing) (core.Mail, error) {
	s.calls = append(s.calls, "mail")
	s.sent = out
	s.from = from
	return core.Mail{}, nil
}
func (s *services) Start(_ context.Context, name string) error {
	s.calls = append(s.calls, "start")
	return nil
}
func (s *services) Reload(context.Context) error { s.calls = append(s.calls, "reload"); return nil }
func newTestCreator(t *testing.T, templateDir string) (*Creator, *services) {
	t.Helper()
	s := &services{org: core.Org{Tenant: "test", User: core.User{Name: "milo", Display: "Camilo Caceres"}, Teams: []core.Team{{ID: "company", Head: "lola"}, {ID: "engineering", Parent: "company", Head: "chief"}}}}
	// Offline setup: upstream treats an unsuccessful optional venv as a warning.
	// A private uv stub prevents package installs; git init only touches temp dirs.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "uv"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + t.TempDir(), "PATH=" + bin + ":/usr/bin:/bin", "USER=lolad-test"}
	return New(Deps{TemplateDir: templateDir, AgentsDir: t.TempDir(), Runtime: s, Identity: s, Org: s, Contacts: s, Mail: s, Starter: s, Registrar: s, SetupEnv: env}), s
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".venv" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRealSetupBothHarnessesAndAdopt(t *testing.T) {
	source := os.Getenv("LOLAD_TEST_TEMPLATE")
	if source == "" {
		source = "/Users/you/lolabot"
	}
	if _, err := os.Stat(filepath.Join(source, "setup.sh")); os.IsNotExist(err) {
		t.Skip("real lolabot template unavailable; set LOLAD_TEST_TEMPLATE")
	}
	templateDir := filepath.Join(t.TempDir(), "template")
	copyTree(t, source, templateDir)
	for _, h := range []core.Harness{core.HarnessClaude, core.HarnessCodewhale} {
		t.Run(string(h), func(t *testing.T) {
			c, s := newTestCreator(t, templateDir)
			// Identity and directory are independent; always pin creation's slug.
			dir := filepath.Join(c.deps.AgentsDir, "different-directory")
			a, err := c.Create(context.Background(), core.CreateRequest{Name: "Display Name", Slug: "test-agent", Dir: dir, Harness: h, Model: "model[1m]", Team: "engineering", Mission: "Own the build", Start: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"CLAUDE.md", "lolabot.yaml", "brain/agents.md", "memory/goals.md", "tools", "skills", "indexes"} {
				if _, err := os.Stat(filepath.Join(a.Dir, path)); err != nil {
					t.Error(path, err)
				}
			}
			var config struct {
				Runtime core.Runtime `yaml:"runtime"`
			}
			if err := yaml.Unmarshal(mustRead(t, filepath.Join(a.Dir, "lolabot.yaml")), &config); err != nil {
				t.Fatal(err)
			}
			if config.Runtime.Slug != "test-agent" || config.Runtime.Harness != h || config.Runtime.Model != "model[1m]" || config.Runtime.Permissions != core.PermBypass {
				t.Fatalf("runtime %+v", config.Runtime)
			}
			section := string(mustRead(t, filepath.Join(a.Dir, "CLAUDE.md")))
			for _, want := range []string{begin, end, "test-agent@test.lolad.local", "engineering", "chief", "alpha, milo, zeta", "tell-user ask", "no acknowledgement ping-pong", "Superseded:"} {
				if !strings.Contains(section, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Count(section, begin) != 1 || strings.Count(section, end) != 1 {
				t.Fatal("duplicate fences")
			}
			for range 2 {
				if err := c.RegenerateSection(a.Agent); err != nil {
					t.Fatal(err)
				}
				if got := string(mustRead(t, filepath.Join(a.Dir, "CLAUDE.md"))); got != section {
					t.Fatal("regeneration was not idempotent")
				}
			}
			if s.from != "milo" || s.sent.To != a.Name || s.sent.Body != "Own the build" || a.State != core.StateStarting {
				t.Fatal("mission/start not wired")
			}
			if got := strings.Join(s.calls, ","); got != "runtime,reload,identity,org,mail,start" {
				t.Fatal(got)
			}
			if h == core.HarnessClaude {
				var settings map[string]any
				if err := json.Unmarshal(mustRead(t, filepath.Join(a.Dir, ".claude/settings.json")), &settings); err != nil {
					t.Fatal(err)
				}
				if settings["model"] != "model[1m]" {
					t.Fatal(settings)
				}
				target, err := os.Readlink(filepath.Join(a.Dir, "AGENTS.md"))
				if err != nil || target != "CLAUDE.md" {
					t.Fatal(target, err)
				}
			} else {
				config := string(mustRead(t, filepath.Join(a.Dir, ".codewhale-home/config.toml")))
				for _, want := range []string{`project_instruction_imports = ["claude"]`, `scan_codewhale_only = true`, `model = "model[1m]"`} {
					if !strings.Contains(config, want) {
						t.Error(want)
					}
				}
				if strings.Contains(config, "API_KEY") {
					t.Fatal("credential config")
				}
				launcher := filepath.Join(a.Dir, a.Name)
				if err := exec.Command("/bin/sh", "-n", launcher).Run(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(mustRead(t, launcher)), "export LOLAD_AGENT=test-agent") {
					t.Fatal("missing identity")
				}
				// Execute the real launcher with a fake binary, never start a live agent.
				bin := t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "codewhale"), []byte("#!/bin/sh\nprintf '%s\\n' \"$LOLAD_AGENT\" \"$CODEWHALE_HOME\" \"$@\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(launcher, "--model", "example")
				cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
				output, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "test-agent\n"+a.Dir+"/.codewhale-home\n--model\nexample") {
					t.Fatal(string(output), err)
				}
			}
			// Adopt a copied generated instance. Never read or modify a live agent tree.
			fixture := filepath.Join(t.TempDir(), "adopted")
			copyTree(t, a.Dir, fixture)
			before := mustRead(t, filepath.Join(fixture, "lolabot.yaml"))
			prefix := strings.Split(string(before), "\nruntime:\n")[0]
			adopted, err := c.Adopt(context.Background(), core.AdoptRequest{Dir: fixture, Runtime: core.Runtime{Harness: h}})
			if err != nil {
				t.Fatal(err)
			}
			if adopted.Name != "test-agent" || adopted.Display != "Display Name" {
				t.Fatalf("%+v", adopted)
			}
			if !strings.HasPrefix(string(mustRead(t, filepath.Join(fixture, "lolabot.yaml"))), prefix) {
				t.Fatal("lost template config")
			}
			if !strings.Contains(string(mustRead(t, filepath.Join(fixture, "CLAUDE.md"))), "test-agent@test.lolad.local") {
				t.Fatal("adopted identity missing")
			}
		})
	}
}
func TestValidationAndRefuseOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  core.CreateRequest
		want error
	}{
		{"missing name", core.CreateRequest{Harness: core.HarnessClaude}, core.ErrInvalid},
		{"invalid slug", core.CreateRequest{Name: "Test", Slug: "../evil", Harness: core.HarnessClaude}, core.ErrInvalid},
		{"unknown harness", core.CreateRequest{Name: "Test", Harness: "other"}, core.ErrInvalid},
		{"unknown team", core.CreateRequest{Name: "Test", Harness: core.HarnessClaude, Team: "missing"}, core.ErrNotFound},
		{"head without team", core.CreateRequest{Name: "Test", Harness: core.HarnessClaude, Head: true}, core.ErrInvalid},
		{"occupied head", core.CreateRequest{Name: "Test", Harness: core.HarnessClaude, Team: "company", Head: true}, core.ErrExists},
		{"sed metacharacter", core.CreateRequest{Name: "Test & Sons", Harness: core.HarnessClaude}, core.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCreator(t, "")
			_, err := c.Create(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	c, _ := newTestCreator(t, "")
	dir := filepath.Join(c.deps.AgentsDir, "test")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keep")
	if err := os.WriteFile(path, []byte("precious"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := c.Create(context.Background(), core.CreateRequest{Name: "Test", Harness: core.HarnessClaude})
	if !errors.Is(err, core.ErrExists) {
		t.Fatal(err)
	}
	if string(mustRead(t, path)) != "precious" {
		t.Fatal("overwrote existing content")
	}
}
func TestPreserveHarnessAndUserContent(t *testing.T) {
	c, _ := newTestCreator(t, "")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	original := `{"enabledPlugins":{"ultron-mail@local":true},"model":"old","hooks":{}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude/settings.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("user guidance"), 0644); err != nil {
		t.Fatal(err)
	}
	a := core.Agent{Name: "test", Dir: dir, Runtime: core.Runtime{Harness: core.HarnessClaude}}
	if err := writeHarness(a); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, filepath.Join(dir, ".claude/settings.json")))
	if !strings.Contains(got, "ultron-mail@local") || !strings.Contains(got, `"model": "old"`) {
		t.Fatal(got)
	}
	if string(mustRead(t, filepath.Join(dir, "AGENTS.md"))) != "user guidance" {
		t.Fatal("lost user AGENTS")
	}
	for _, bad := range []string{begin + "\nbroken", end + "\n" + begin, begin + end + begin + end} {
		path := filepath.Join(dir, "CLAUDE.md")
		if err := os.WriteFile(path, []byte(bad), 0644); err != nil {
			t.Fatal(err)
		}
		if err := c.RegenerateSection(a); !errors.Is(err, core.ErrInvalid) {
			t.Fatal(err)
		}
		if string(mustRead(t, path)) != bad {
			t.Fatal("modified malformed fences")
		}
	}
}
func TestSlug(t *testing.T) {
	for name, want := range map[string]string{"Ultron": "ultron", " Chief of Staff ": "chief-of-staff", "Foo_123": "foo-123", "!!!": ""} {
		if got := Slug(name); got != want {
			t.Fatal(fmt.Sprintf("%q => %q", name, got))
		}
	}
}

func TestOrgChangesAndHeadReporting(t *testing.T) {
	c, s := newTestCreator(t, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	original := "# Private charter\n\nKeep this exact charter.\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	a := core.Agent{Name: "chief", Dir: dir}
	if err := c.RegenerateSection(a); err != nil {
		t.Fatal(err)
	}
	text := string(mustRead(t, path))
	if !strings.HasPrefix(text, original) || !strings.Contains(text, "You are its head. You report to lola.") {
		t.Fatal(text)
	}
	s.org.Teams[0].Head = "new-lola"
	if err := c.RegenerateSection(a); err != nil {
		t.Fatal(err)
	}
	text = string(mustRead(t, path))
	if !strings.Contains(text, "You report to new-lola.") || strings.Contains(text, "You report to lola.") {
		t.Fatal(text)
	}
	a.Name = "new-lola"
	if err := c.RegenerateSection(a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, path)), "You are its head. You report to milo.") {
		t.Fatal("root head reporting")
	}
}

func TestExistingCodewhaleAndPartialFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "adopted")
	if err := os.MkdirAll(filepath.Join(dir, ".codewhale-home"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"lolabot.yaml":                "# keep config\nagent:\n  name: Existing\n  role: Assistant\n",
		"CLAUDE.md":                   "# Existing charter\n",
		".codewhale-home/config.toml": "provider = \"custom\"\nproject_instruction_imports = [\"claude\"]\n[skills]\nscan_codewhale_only = true\n",
		"adopted":                     "#!/bin/sh\n# Existing launcher\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	c, s := newTestCreator(t, "")
	s.fail = "identity"
	status, err := c.Adopt(context.Background(), core.AdoptRequest{Dir: dir, Runtime: core.Runtime{Harness: core.HarnessCodewhale, Model: "requested"}})
	if err == nil || !strings.Contains(err.Error(), "retry with adopt") || status.Dir != dir {
		t.Fatal(status, err)
	}
	for _, name := range []string{".codewhale-home/config.toml", "adopted", "CLAUDE.md"} {
		if string(mustRead(t, filepath.Join(dir, name))) != files[name] {
			t.Fatalf("changed existing %s", name)
		}
	}
	s.fail = ""
	if _, err := c.Adopt(context.Background(), core.AdoptRequest{Dir: dir, Runtime: core.Runtime{Harness: core.HarnessCodewhale}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, "CLAUDE.md"))), "existing@test.lolad.local") {
		t.Fatal("retry failed")
	}
}

func TestAdoptSlugDerivationAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, dir, display, stored, requested, runtimeSlug, want string
		persist                                                  bool
	}{
		{name: "ultron in jugui", dir: "jugui", display: "Ultron", want: "ultron", persist: true},
		{name: "lola in dsh-lola", dir: "dsh-lola", display: "Lola", want: "lola", persist: true},
		{name: "matching name", dir: "ultron", display: "Ultron", want: "ultron"},
		{name: "directory fallback", dir: "fallback", want: "fallback"},
		{name: "stored slug survives renamed display", dir: "jugui", display: "Renamed Agent", stored: "ultron", want: "ultron", persist: true},
		{name: "request override", dir: "jugui", display: "Ultron", stored: "ultron", requested: "assistant", want: "assistant", persist: true},
		{name: "runtime request override", dir: "jugui", display: "Ultron", runtimeSlug: "assistant", want: "assistant", persist: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCreator(t, "")
			dir := filepath.Join(t.TempDir(), tc.dir)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			content := fmt.Sprintf("agent:\n  name: %q\n  role: Assistant\n", tc.display)
			if tc.stored != "" {
				content += "runtime:\n  slug: " + tc.stored + "\n  harness: claude\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "lolabot.yaml"), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Charter\n"), 0644); err != nil {
				t.Fatal(err)
			}
			a, err := c.Adopt(context.Background(), core.AdoptRequest{Dir: dir, Slug: tc.requested, Runtime: core.Runtime{Slug: tc.runtimeSlug, Harness: core.HarnessClaude}})
			if err != nil {
				t.Fatal(err)
			}
			if a.Name != tc.want {
				t.Fatalf("name %q, want %q", a.Name, tc.want)
			}
			var doc struct {
				Runtime core.Runtime `yaml:"runtime"`
			}
			if err := yaml.Unmarshal(mustRead(t, filepath.Join(dir, "lolabot.yaml")), &doc); err != nil {
				t.Fatal(err)
			}
			expected := ""
			if tc.persist {
				expected = tc.want
			}
			if doc.Runtime.Slug != expected || a.Runtime.Slug != expected {
				t.Fatalf("written slug %q/status %q, want %q", doc.Runtime.Slug, a.Runtime.Slug, expected)
			}
			if !strings.Contains(string(mustRead(t, filepath.Join(dir, "CLAUDE.md"))), tc.want+"@test.lolad.local") {
				t.Fatal("wrong instruction identity")
			}
		})
	}
}

func TestCreateAndAdoptAutostart(t *testing.T) {
	template := t.TempDir()
	script := `#!/bin/sh
for dir; do :; done
printf 'agent:\n  name: Worker\n' > "$dir/lolabot.yaml"
printf '# Worker\n' > "$dir/CLAUDE.md"
`
	if err := os.WriteFile(filepath.Join(template, "setup.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, start := range []bool{false, true} {
		t.Run(fmt.Sprint(start), func(t *testing.T) {
			c, svc := newTestCreator(t, template)
			got, err := c.Create(t.Context(), core.CreateRequest{Name: "Worker", Harness: core.HarnessClaude, Start: start})
			if err != nil {
				t.Fatal(err)
			}
			if got.Runtime.AutostartEnabled() != start {
				t.Fatal(got.Runtime)
			}
			var doc struct {
				Runtime core.Runtime `yaml:"runtime"`
			}
			if err := yaml.Unmarshal(mustRead(t, filepath.Join(got.Dir, "lolabot.yaml")), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Runtime.AutostartEnabled() != start {
				t.Fatal("runtime not persisted")
			}
			if strings.Contains(strings.Join(svc.calls, ","), "start") != start {
				t.Fatal(svc.calls)
			}
			got, err = c.Adopt(t.Context(), core.AdoptRequest{Dir: got.Dir, Runtime: core.Runtime{Harness: core.HarnessClaude}})
			if err != nil {
				t.Fatal(err)
			}
			if got.Runtime.Autostart == nil || *got.Runtime.Autostart {
				t.Fatal("adopt must default to stopped")
			}
		})
	}
}
