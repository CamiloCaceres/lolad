package msgcli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Attach paths follow one rule everywhere: relative to the working
// directory, absolute anywhere, regular files only.
func TestResolveAttach(t *testing.T) {
	cwd, elsewhere := t.TempDir(), t.TempDir()
	write := func(p string) {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cwd, "here.txt"))
	write(filepath.Join(elsewhere, "scratch.png"))
	got, err := resolveAttach(cwd, []string{"here.txt", filepath.Join(elsewhere, "scratch.png")})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != filepath.Join(cwd, "here.txt") || got[1] != filepath.Join(elsewhere, "scratch.png") {
		t.Fatalf("got %v", got)
	}
	for in, want := range map[string]string{
		"missing.txt": "doesn't exist (relative paths start at " + cwd + ")",
		elsewhere:     "is a folder; zip it first",
		"  ":          "path 1 is empty",
	} {
		if _, err := resolveAttach(cwd, []string{in}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", in, err, want)
		}
	}
}

// A file outside the agent's folder (a scratchpad, /tmp) can be sent to the
// user through the MCP tool and through tell-user alike.
func TestAttachFromOutsideAgentFolder(t *testing.T) {
	p, c, _ := mailbox(t)
	home, scratch := t.TempDir(), t.TempDir()
	t.Setenv("LOLABOT_HOME", home)
	file := filepath.Join(scratch, "chart.png")
	if err := os.WriteFile(file, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := decodeToolInput("message_user", []byte(`{"kind":"deliver","text":"Chart attached","attach":[`+strconv.Quote(file)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	m, err := buildUserMessage(false, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.Attach, err = resolveAttach(attachBase(), m.Attach); err != nil {
		t.Fatal(err)
	}
	id, err := c.sendToUser(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Filename != "chart.png" {
		t.Fatalf("attachments: %+v", got.Attachments)
	}
}
