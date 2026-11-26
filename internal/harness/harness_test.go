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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
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
			if kind == core.HarnessCodewhale && (!strings.Contains(joined, "--sandbox-mode|danger-full-access") || strings.Contains(joined, "--approval-policy")) {
				t.Fatal("bypass must permit the shell tool:", joined)
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
				// Bypass is the default; a disclaimer at launch would stall the agent.
				if overlay["skipDangerousModePermissionPrompt"] != true {
					t.Fatal("bypass consent missing:", string(b))
				}
				d := a
				d.Runtime.Permissions = core.PermDefault
				if _, e := l.Prepare(d, core.LaunchOpts{Fresh: true, StateDir: state}); e != nil {
					t.Fatal(e)
				}
				b, _ = os.ReadFile(filepath.Join(state, "agents/test/claude-settings.json"))
				if strings.Contains(string(b), "skipDangerousModePermissionPrompt") {
					t.Fatal("consent written without bypass:", string(b))
				}
			}
			s, e = l.Prepare(a, core.LaunchOpts{StateDir: state})
			if e != nil || !strings.Contains(strings.Join(s.Argv, "|"), "--continue") {
				t.Fatalf("%v %v", s, e)
			}
		})
	}
}

// Codewhale hooks report "sess_…" runtime ids that its --resume rejects with
// "No session found" (exit 1), which crash-looped sgi-dev. Such an id must
// resume the agent's latest session with --continue; a saved id still works.
func TestCodewhaleResumeIgnoresHookID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := core.Agent{Name: "cw", Dir: t.TempDir(), Runtime: core.Runtime{Harness: core.HarnessCodewhale}}
	l := New(core.HarnessCodewhale, "/bin/codewhale")
	// The TUI also exits 1 when a resume comes with a prompt, so none is passed.
	resume := core.LaunchOpts{StateDir: t.TempDir(), FirstPrompt: "check your inbox"}
	for id, want := range map[string]string{"sess_a693075f": "|--continue", "b2a34390-b214-429c-b2f8-45741963a9c8": "|--resume|b2a34390-b214-429c-b2f8-45741963a9c8"} {
		s, e := l.PrepareResume(a, resume, id)
		if e != nil {
			t.Fatal(e)
		}
		joined := strings.Join(s.Argv, "|")
		if !strings.Contains(joined, want) || (want == "|--continue" && strings.Contains(joined, "sess_")) || strings.Contains(joined, "check your inbox") {
			t.Fatalf("%s: %s", id, joined)
		}
	}
	fresh := resume
	fresh.Fresh = true
	if s, _ := l.PrepareResume(a, fresh, ""); !strings.HasSuffix(strings.Join(s.Argv, "|"), "|--|check your inbox") {
		t.Fatal("fresh Codewhale lost its first prompt:", s.Argv)
	}
	// Claude ids are resumable as they are, and Claude accepts the prompt.
	c := core.Agent{Name: "cl", Dir: t.TempDir(), Runtime: core.Runtime{Harness: core.HarnessClaude}}
	s, e := New(core.HarnessClaude, "/bin/claude").PrepareResume(c, resume, "sess_x")
	if j := strings.Join(s.Argv, "|"); e != nil || !strings.Contains(j, "--resume|sess_x") || !strings.HasSuffix(j, "|--|check your inbox") {
		t.Fatal(s.Argv, e)
	}
}

// A resumed Codewhale reopens in "ask" even under bypass; lolad switches it.
func TestCodewhaleStartupModeKeys(t *testing.T) {
	cw := New(core.HarnessCodewhale, "")
	bypass := core.Agent{Runtime: core.Runtime{Harness: core.HarnessCodewhale, Permissions: core.PermBypass}}
	bar := func(mode string) string {
		return "● ask about the schema\n╭────╮\n│❯ Type a message…\n╰────╯\n ● " + mode + "  Shift+Tab to change   work (Tab)\n DeepSeek · deepseek-flash"
	}
	for mode, want := range map[string]bool{"ask": true, "auto": true, "Full Access": false, "never": false} {
		if got := len(cw.StartupModeKeys(bypass, bar(mode))) > 0; got != want {
			t.Fatalf("%s: switch=%v", mode, got)
		}
	}
	if keys := cw.StartupModeKeys(bypass, bar("ask")); len(keys) != 1 || keys[0] != "BTab" {
		t.Fatal(keys)
	}
	// Transcript text above the composer never counts as the mode.
	if cw.StartupModeKeys(bypass, "● ask me later\n╭─╮\n│❯\n╰─╯\n ● Full Access   work (Tab)") != nil {
		t.Fatal("transcript read as mode")
	}
	def := bypass
	def.Runtime.Permissions = core.PermDefault
	if cw.StartupModeKeys(def, bar("ask")) != nil {
		t.Fatal("switched an agent that asked for approvals")
	}
	if New(core.HarnessClaude, "").StartupModeKeys(bypass, bar("ask")) != nil {
		t.Fatal("Claude has no such mode bar")
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
	original := "provider = \"deepseek\"\napproval_policy = \"never\"\n[skills]\nscan_codewhale_only = true\n"
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
	if !strings.Contains(env, "CODEWHALE_YOLO=true") || !strings.Contains(env, "CODEWHALE_ALLOW_SHELL=1") {
		t.Fatal("bypass environment missing")
	}
	for i, arg := range s.Argv {
		if arg == "--config" {
			overlay, err := os.ReadFile(s.Argv[i+1])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(overlay), "approval_policy =") {
				t.Fatal("root deny policy overrides bypass")
			}
		}
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

func TestGlobalEnvPrecedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, state := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(state, GlobalEnvFile), []byte("DEEPSEEK_API_KEY=shared\nBOTH=global\n"), 0600)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("BOTH=agent\n"), 0600)
	a := core.Agent{Name: "test", Dir: dir, Runtime: core.Runtime{Harness: core.HarnessCodewhale}}
	s, e := New(core.HarnessCodewhale, "/bin/harness").Prepare(a, core.LaunchOpts{Fresh: true, LoladBin: "/bin/lolad", StateDir: state})
	if e != nil {
		t.Fatal(e)
	}
	env := strings.Join(s.Env, "\n")
	if !strings.Contains(env, "DEEPSEEK_API_KEY=shared") || !strings.Contains(env, "BOTH=agent") || strings.Contains(env, "BOTH=global") {
		t.Fatal(env)
	}
	if strings.Contains(env, "LOLAD_ENV_KEYS=") && strings.Contains(strings.SplitN(strings.SplitN(env, "LOLAD_ENV_KEYS=", 2)[1], "\n", 2)[0], "DEEPSEEK") {
		t.Fatal("shared keys must not be live-unset")
	}
}

// Every agent gets lolad's user tools (message_user, ask_user) over MCP.
func TestUserToolsMCP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	opts := func(state string) core.LaunchOpts {
		return core.LaunchOpts{Fresh: true, LoladBin: "/opt/lolad", LoladURL: "http://127.0.0.1:4100", StateDir: state}
	}
	state := t.TempDir()
	a := core.Agent{Name: "test", Dir: t.TempDir(), Runtime: core.Runtime{Harness: core.HarnessClaude}}
	s, e := New(core.HarnessClaude, "/bin/claude").Prepare(a, opts(state))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(state, "agents/test/mcp.json")
	if !strings.Contains(strings.Join(s.Argv, "|"), "|--mcp-config="+path+"|") {
		t.Fatal("no --mcp-config=:", s.Argv)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
			Env     map[string]string
		} `json:"mcpServers"`
	}
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &cfg) != nil {
		t.Fatal(string(b))
	}
	srv := cfg.MCPServers["user"]
	if srv.Command != "/opt/lolad" || strings.Join(srv.Args, " ") != "msg --as test mcp" || srv.Env["LOLAD_AGENT"] != "test" || srv.Env["LOLAD_STATE"] != state || srv.Env["LOLAD_URL"] == "" {
		t.Fatal(string(b))
	}
	if b, _ = os.ReadFile(filepath.Join(state, "agents/test/claude-settings.json")); !strings.Contains(string(b), "mcp__user__ask_user") || !strings.Contains(string(b), "mcp__user__get_user_messages") {
		t.Fatal("tools not allowed:", string(b))
	}

	// Codewhale reads its home's mcp.json: lolad adds its server, leaves the rest.
	cw := core.Agent{Name: "cw", Dir: t.TempDir(), Runtime: core.Runtime{Harness: core.HarnessCodewhale}}
	home := filepath.Join(cw.Dir, ".codewhale-home")
	os.MkdirAll(home, 0700)
	os.WriteFile(filepath.Join(home, "mcp.json"), []byte(`{"servers":{"mine":{"command":"/bin/mine","read_timeout":30}},"extra":true}`), 0600)
	if _, e = New(core.HarnessCodewhale, "/bin/codewhale").Prepare(cw, opts(t.TempDir())); e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	b, _ = os.ReadFile(filepath.Join(home, "mcp.json"))
	if json.Unmarshal(b, &doc) != nil || doc["extra"] != true {
		t.Fatal(string(b))
	}
	servers := doc["servers"].(map[string]any)
	mine, lolad := servers["mine"].(map[string]any), servers["user"].(map[string]any)
	if mine["command"] != "/bin/mine" || mine["read_timeout"] != float64(30) || lolad["command"] != "/opt/lolad" || lolad["enabled"] != true {
		t.Fatal(string(b))
	}
}

// Dialog words in the transcript above the composer are text, not a dialog:
// an agent that wrote about "Allow once" was never told about its mail
// again until the words scrolled away. A spinner line there still means
// busy, and dialog words in or under the composer still mean a dialog.
func TestPromptReadyIgnoresTranscriptWords(t *testing.T) {
	b, e := os.ReadFile("testdata/claude-idle.txt")
	if e != nil {
		t.Fatal(e)
	}
	idle := string(b)
	at := strings.Index(idle, "────")
	l := New(core.HarnessClaude, "")
	with := func(above string) string { return idle[:at] + above + "\n" + idle[at:] }
	if !l.PromptReady(with("⏺ When the dialog shows, pick \"Allow once\", or press Esc to cancel.\n  Do you trust this folder? Enter to confirm. esc to interrupt")) {
		t.Fatal("transcript words blocked the prompt")
	}
	for _, spin := range []string{"✻ Brewing… (12s · esc to interrupt)", "· Thinking… (esc to interrupt)", "✶ Running… (3s · ↓ 1.2k tokens · Esc to interrupt)"} {
		if l.PromptReady(with(spin)) {
			t.Fatalf("spinner %q read as ready", spin)
		}
	}
	if l.PromptReady(idle + "\nEnter to confirm · Esc to cancel") {
		t.Fatal("dialog words under the composer read as ready")
	}
}
