package registry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestRuntimePreservesOriginalBytes(t *testing.T) {
	original, e := os.ReadFile("testdata/jugui.yaml")
	if e != nil {
		t.Fatal(e)
	}
	for _, crlf := range []bool{false, true} {
		t.Run(map[bool]string{false: "LF", true: "CRLF"}[crlf], func(t *testing.T) {
			b := original
			if crlf {
				b = bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "lolabot.yaml")
			if e := os.WriteFile(path, b, 0640); e != nil {
				t.Fatal(e)
			}
			r := core.Runtime{Harness: core.HarnessClaude, OnWake: "hello\nworld"}
			if e := WriteRuntime(dir, r); e != nil {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(path)
			if !bytes.HasPrefix(after, b) {
				t.Fatal("append changed original bytes")
			}
			suffix := []byte("\n# untouched comment\ncustom: 'keep  spacing' # yes\n")
			if crlf {
				suffix = bytes.ReplaceAll(suffix, []byte("\n"), []byte("\r\n"))
			}
			after = append(after, suffix...)
			os.WriteFile(path, after, 0640)
			r.Harness = core.HarnessCodewhale
			if e := WriteRuntime(dir, r); e != nil {
				t.Fatal(e)
			}
			after, _ = os.ReadFile(path)
			if !bytes.HasPrefix(after, b) || !bytes.HasSuffix(after, suffix) {
				t.Fatalf("unrelated bytes changed: %s", after)
			}
			if strings.Count(string(after), "runtime:") != 1 {
				t.Fatal("duplicate runtime")
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0640 {
				t.Fatal("file mode changed")
			}
			a, e := ReadAgent(dir)
			if e != nil || a.Runtime.Harness != r.Harness {
				t.Fatalf("read: %+v %v", a, e)
			}
		})
	}
}
func TestRegistryScanDiffValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "My_AGENT")
	os.Mkdir(dir, 0755)
	os.WriteFile(filepath.Join(dir, "lolabot.yaml"), []byte("agent:\n  name: Ultron\n  role: Assistant\nruntime:\n  harness: claude\n"), 0644)
	nested := filepath.Join(dir, "nested")
	os.Mkdir(nested, 0755)
	os.WriteFile(filepath.Join(nested, "lolabot.yaml"), []byte("runtime: {harness: claude}"), 0644)
	config := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(config, []byte("agents_dirs: ["+root+"]\nagents: ["+dir+"]\n"), 0644)
	r, e := New(config)
	if e != nil {
		t.Fatal(e)
	}
	agents := r.List()
	if len(agents) != 1 || agents[0].Name != "ultron" || agents[0].Display != "Ultron" || agents[0].Runtime.Permissions != core.PermBypass {
		t.Fatalf("agents: %+v", agents)
	}
	if d, e := r.Reload(); e != nil || len(d.Added)+len(d.Changed)+len(d.Removed) != 0 {
		t.Fatalf("diff: %+v %v", d, e)
	}
	WriteRuntime(dir, core.Runtime{Harness: core.HarnessCodewhale})
	d, e := r.Reload()
	if e != nil || len(d.Changed) != 1 {
		t.Fatalf("changed: %+v %v", d, e)
	}
	os.Remove(filepath.Join(dir, "lolabot.yaml"))
	d, e = r.Reload()
	if e != nil || len(d.Removed) != 1 {
		t.Fatalf("removed: %+v %v", d, e)
	}
}
func TestValidate(t *testing.T) {
	for _, r := range []core.Runtime{{Harness: "other"}, {Harness: core.HarnessClaude, Rotate: "1:00"}, {Harness: core.HarnessClaude, Triggers: []core.Trigger{{Cron: "bad"}}}, {Harness: core.HarnessClaude, Triggers: []core.Trigger{{Cron: "* * * * *", Mail: "account"}}}, {Harness: core.HarnessClaude, Triggers: []core.Trigger{{}}}, {Harness: core.HarnessClaude, Triggers: []core.Trigger{{Mail: "x", Every: "0s"}}}} {
		if Validate(r) == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if e := Validate(core.Runtime{Harness: core.HarnessClaude, Rotate: "23:59", Triggers: []core.Trigger{{Cron: "0 9 * * *"}}}); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"___A__B", strings.Repeat("a", 100), "9.foo"} {
		slug := Slug(s)
		if len(slug) > 63 || !strings.ContainsAny(slug[:1], "abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Fatal(slug)
		}
	}
}
func TestConfigDefaults(t *testing.T) {
	t.Setenv("LOLAD_CONFIG", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("LOLAD_STATE", "/tmp/lolad-custom")
	c, e := LoadConfig("")
	if e != nil || c.Listen != "127.0.0.1:4100" || c.StateDir != "/tmp/lolad-custom" {
		t.Fatalf("%+v %v", c, e)
	}
}

func TestRuntimeReplacementRemovesIndentedLiteralComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lolabot.yaml")
	os.WriteFile(path, []byte("agent: {name: Agent}\nruntime:\n  harness: claude\n  on_wake: |\n    # This is part of the prompt\n# Preserve top-level comment\nother: unchanged\n"), 0644)
	if e := WriteRuntime(dir, core.Runtime{Harness: core.HarnessCodewhale}); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "part of the prompt") || !strings.HasSuffix(string(b), "# Preserve top-level comment\nother: unchanged\n") {
		t.Fatalf("%s", b)
	}
	if _, e := ReadAgent(dir); e != nil {
		t.Fatal(e)
	}
}

func TestSlugPrecedenceAndDuplicateDiagnostics(t *testing.T) {
	for _, tt := range []struct{ display, override, want string }{{"Ultron", "", "ultron"}, {"Lola", "", "lola"}, {"Name / With Spaces", "", "name-with-spaces"}, {"", "", "fallback"}, {"Ultron", "CUSTOM-slug", "custom-slug"}} {
		got, e := ResolveSlug("/tmp/Fallback", tt.display, tt.override)
		if e != nil || got != tt.want {
			t.Fatalf("%+v: %s %v", tt, got, e)
		}
	}
	if _, e := ResolveSlug("/tmp/fallback", "Name", "bad slug"); e == nil {
		t.Fatal("invalid override accepted")
	}
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		os.Mkdir(dir, 0755)
		os.WriteFile(filepath.Join(dir, "lolabot.yaml"), []byte("agent: {name: Ultron}\nruntime: {harness: claude}\n"), 0644)
	}
	config := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(config, []byte("agents_dirs: ["+root+"]\n"), 0644)
	var events []core.Event
	r, e := New(config, WithEvents(func(ev core.Event) { events = append(events, ev) }))
	if e != nil {
		t.Fatal(e)
	}
	if len(r.List()) != 1 || r.List()[0].Dir != filepath.Join(root, "a") || len(events) != 1 || events[0].Kind != "error" || len(r.Diagnostics()) != 1 {
		t.Fatalf("agents=%+v events=%+v", r.List(), events)
	}
	d, e := r.Reload()
	if e != nil || len(d.Rejected) != 1 || len(d.Added)+len(d.Removed) != 0 {
		t.Fatalf("%+v %v", d, e)
	}
}

func TestExplicitConfigDoesNotInheritDiscovery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	missing, e := LoadConfig(p)
	if e != nil || len(missing.AgentsDirs) == 0 {
		t.Fatal("missing config lost defaults", e)
	}
	for _, content := range []string{"listen: 127.0.0.1:4196\n", "agents: []\n", "agents_dirs: []\n"} {
		if e = os.WriteFile(p, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := LoadConfig(p)
		if e != nil || len(c.AgentsDirs) != 0 {
			t.Fatalf("explicit config inherited scan dirs: %v %v", c.AgentsDirs, e)
		}
	}
	if e = os.WriteFile(p, []byte("agents_dirs: [/tmp/explicit]\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig(p)
	if e != nil || len(c.AgentsDirs) != 1 || c.AgentsDirs[0] != "/tmp/explicit" {
		t.Fatal(c.AgentsDirs, e)
	}
}
