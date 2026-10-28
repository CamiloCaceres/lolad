package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestEnvExport(t *testing.T) {
	bash, e := exec.LookPath("bash")
	if e != nil {
		t.Skip(e)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := "# comment\nA=one\nexport B=\"two words\"\nQUOTE=it's $HOME `x`\nHOMEV=${LOLABOT_HOME}/x\nPATH=/bad\nLOLAD_URL=http://evil\nPINNED=from-file\nbad-name=1\n"
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	vars := map[string]string{"LOLAD_ENV_FILE": path, "LOLABOT_HOME": "/home/agent", "LOLAD_ENV_PINNED": "PINNED", "LOLAD_ENV_KEYS": "A,GONE,PINNED"}
	out, e := EnvExport(func(k string) string { return vars[k] })
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"PATH", "LOLAD_URL", "PINNED", "bad-name"} {
		if strings.Contains(out, bad+"=") {
			t.Fatalf("%s exported:\n%s", bad, out)
		}
	}
	script := out + `printf '%s|%s|%s|%s|%s|%s|%s' "$A" "$B" "$QUOTE" "$HOMEV" "${GONE-unset}" "$PINNED" "$PATH"`
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = []string{"GONE=launched", "PINNED=launch", "PATH=/usr/bin:/bin", "HOME=/nowhere"}
	got, e := cmd.Output()
	if e != nil {
		t.Fatal(e, out)
	}
	want := "one|two words|it's $HOME `x`|/home/agent/x|unset|launch|/usr/bin:/bin"
	if string(got) != want {
		t.Fatalf("got  %q\nwant %q\nscript:\n%s", got, want, out)
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if out, e = EnvExport(func(k string) string { return vars[k] }); e != nil || !strings.Contains(out, "unset A\n") {
		t.Fatalf("deleted env file should unset launched keys: %q %v", out, e)
	}
	if out, e = EnvExport(func(string) string { return "" }); e != nil || out != "" {
		t.Fatalf("outside lolad: %q %v", out, e)
	}
}

func TestPrepareRecordsLiveEnvInputs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=x\nOTHER=y\nTOKEN=z\n"), 0600); e != nil {
		t.Fatal(e)
	}
	a := New(core.HarnessClaude, "/bin/echo")
	agent := core.Agent{Name: "live", Dir: dir, Runtime: core.Runtime{Harness: core.HarnessClaude, Env: map[string]string{"PIN": "1"}}}
	spec, e := a.Prepare(agent, core.LaunchOpts{StateDir: t.TempDir(), LoladBin: "/usr/local/bin/lolad", Fresh: true})
	if e != nil {
		t.Fatal(e)
	}
	env := map[string]string{}
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if env["LOLAD_ENV_FILE"] != filepath.Join(dir, ".env") || env["LOLAD_ENV_KEYS"] != "TOKEN,OTHER" || env["LOLAD_ENV_PINNED"] != "PIN" || env["TOKEN"] != "z" {
		t.Fatalf("%v", env)
	}
}
