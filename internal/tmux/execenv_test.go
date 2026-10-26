package tmux

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The test binary doubles as `lolad exec-env` so Start can be tested end to end.
func TestMain(m *testing.M) {
	ServeExecEnv()
	os.Exit(m.Run())
}

func TestEnvFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	env := []string{"A=1", "MULTI=line one\nline two", "EMPTY=", "EQ=a=b"}
	file, err := writeEnvFile(dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(file); runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	got, err := readEnvFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, env) {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("env file not removed after reading")
	}
}

func TestChildEnvKeepsPaneVariables(t *testing.T) {
	get := func(k string) string {
		return map[string]string{"TERM": "tmux-256color", "TMUX_PANE": "%1", "SECRET": "x"}[k]
	}
	got := childEnv([]string{"PATH=/bin", "TERM=xterm"}, get)
	if !slices.Contains(got, "TERM=xterm") || !slices.Contains(got, "TMUX_PANE=%1") || slices.Contains(got, "TERM=tmux-256color") {
		t.Fatalf("got %q", got)
	}
	for _, v := range got {
		if strings.HasPrefix(v, "SECRET=") {
			t.Fatal("non-pane variable passed through")
		}
	}
}
