package harness

import (
	"encoding/json"
	"github.com/CamiloCaceres/lolad/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepare(t *testing.T) {
	for _, kind := range []core.Harness{core.HarnessClaude, core.HarnessCodewhale} {
		t.Run(string(kind), func(t *testing.T) {
			dir := t.TempDir()
			state := t.TempDir()
			os.WriteFile(filepath.Join(dir, ".env"), []byte("FROM_FILE=yes\nSHARED=file\n"), 0600)
			t.Setenv("LOLAD_SECRET", "forbidden")
			t.Setenv("AMP_API_KEY", "forbidden")
			a := core.Agent{Name: "test", Dir: dir, Runtime: core.Runtime{Harness: kind, Model: "model[1m]", Env: map[string]string{"SHARED": "runtime", "ROOT": "${LOLABOT_HOME}"}, OnWake: "wake"}}
			l := New(kind, "/bin/harness")
			s, e := l.Prepare(a, core.LaunchOpts{Fresh: true, LoladBin: "/path with ' quote/lolad", LoladURL: "http://localhost:4100", StateDir: state})
			if e != nil {
				t.Fatal(e)
			}
			joined := strings.Join(s.Argv, "|")
			if !strings.Contains(joined, "model[1m]") || !strings.HasSuffix(joined, "|--|wake") || strings.Contains(joined, "--continue") {
				t.Fatal(joined)
			}
			env := strings.Join(s.Env, "\n")
			for _, v := range []string{"SHARED=runtime", "FROM_FILE=yes", "ROOT=" + dir} {
				if !strings.Contains(env, v) {
					t.Fatal(env)
				}
			}
			if strings.Contains(env, "forbidden") {
				t.Fatal("daemon credentials inherited")
			}
			if kind == core.HarnessClaude {
				b, e := os.ReadFile(filepath.Join(state, "agents/test/claude-settings.json"))
				if e != nil {
					t.Fatal(e)
				}
				var overlay map[string]any
				if json.Unmarshal(b, &overlay) != nil || overlay["hooks"] == nil {
					t.Fatal(string(b))
				}
			}
			s, e = l.Prepare(a, core.LaunchOpts{StateDir: state})
			if e != nil || !strings.Contains(strings.Join(s.Argv, "|"), "--continue") {
				t.Fatalf("%v %v", s, e)
			}
		})
	}
}
func TestCapturedReadiness(t *testing.T) {
	for _, kind := range []core.Harness{core.HarnessClaude, core.HarnessCodewhale} {
		name := "claude"
		if kind == core.HarnessCodewhale {
			name = "cw"
		}
		b, e := os.ReadFile("testdata/" + name + "-idle.txt")
		if e != nil {
			t.Fatal(e)
		}
		l := New(kind, "")
		if !l.PromptReady(string(b)) {
			t.Fatalf("not ready: %s", b)
		}
		if l.PromptReady(string(b) + "\nEnter to confirm · Esc to cancel") {
			t.Fatal("permission prompt ready")
		}
	}
}

func TestCodewhaleAgentConfigUntouched(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, ".codewhale-home")
	os.MkdirAll(filepath.Join(home, "prompts"), 0700)
	os.WriteFile(filepath.Join(home, "prompts/constitution.md"), []byte("identity"), 0600)
	original := "provider = \"deepseek\"\n[skills]\nscan_codewhale_only = true\n"
	os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0600)
	s, e := New(core.HarnessCodewhale, "/bin/cw").Prepare(core.Agent{Name: "a", Dir: dir}, core.LaunchOpts{Fresh: true, StateDir: t.TempDir(), LoladBin: "/bin/lolad"})
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if string(after) != original {
		t.Fatal("agent config modified")
	}
	env := strings.Join(s.Env, "\n")
	if !strings.Contains(env, "CODEWHALE_HOME="+home) || !strings.Contains(env, "CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE=1") {
		t.Fatal(env)
	}
}

func TestCapturedDialogs(t *testing.T) {
	for _, tc := range []struct {
		kind core.Harness
		name string
	}{{core.HarnessClaude, "claude-initial.txt"}, {core.HarnessCodewhale, "cw-no-provider.txt"}} {
		b, e := os.ReadFile("testdata/" + tc.name)
		if e != nil {
			t.Fatal(e)
		}
		if New(tc.kind, "").PromptReady(string(b)) {
			t.Fatalf("dialog ready: %s", tc.name)
		}
	}
}
