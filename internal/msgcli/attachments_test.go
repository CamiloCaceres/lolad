package msgcli

import (
	"bytes"
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/policy"
)

func TestAttachmentsEndToEnd(t *testing.T) {
	state := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(state, "mail.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	o := policy.DefaultOrg()
	o.Defaults.Independents = "all"
	p, e := amp.New(db, state, "home", policy.New(o, "a", "b"), amp.WithOrg(func() core.Org { return o }))
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"a", "b", "milo"} {
		kind := "agent"
		if name == "milo" {
			kind = "human"
		}
		if _, e = p.EnsureIdentity(t.Context(), name, kind); e != nil {
			t.Fatal(e)
		}
	}
	server := httptest.NewServer(p)
	defer server.Close()
	t.Setenv("LOLAD_STATE", state)
	t.Setenv("LOLAD_URL", server.URL)
	run := func(name, home string, args ...string) string {
		t.Helper()
		t.Setenv("LOLAD_AGENT", name)
		t.Setenv("LOLABOT_HOME", home)
		var out, err bytes.Buffer
		if code := Run(context.Background(), args, &out, &err); code != 0 {
			t.Fatalf("%s %v: exit %d: %s", name, args, code, err.String())
		}
		return out.String()
	}
	aHome, bHome := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bHome, ".gitignore"), []byte(".env"), 0644)
	logo := filepath.Join(aHome, "logo.svg")
	os.WriteFile(logo, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0644)
	brief := filepath.Join(aHome, "brief.md")
	os.WriteFile(brief, []byte("# Brief\n"), 0644)
	id := strings.TrimSpace(run("a", aHome, "send", "b", "Assets", "two files", "--attach", logo, "--attach", brief))

	read := run("b", bHome, "read", id)
	saved := filepath.Join(bHome, "attachments", id, "logo.svg")
	if !strings.Contains(read, "Attachments (2):") || !strings.Contains(read, saved) {
		t.Fatalf("read did not save the files:\n%s", read)
	}
	if b, _ := os.ReadFile(saved); !bytes.Contains(b, []byte("<svg")) {
		t.Fatal("saved bytes differ")
	}
	if g, _ := os.ReadFile(filepath.Join(bHome, ".gitignore")); !strings.Contains(string(g), "\nattachments/\n") {
		t.Fatalf("attachments not git-ignored: %q", g)
	}
	// Reading again is idempotent and the sender only sees the listing.
	if again := run("b", bHome, "read", id); !strings.Contains(again, saved) {
		t.Fatal(again)
	}
	if entries, _ := os.ReadDir(filepath.Join(bHome, "attachments", id)); len(entries) != 2 {
		t.Fatalf("duplicate downloads: %v", entries)
	}
	if mine := run("a", aHome, "read", id); !strings.Contains(mine, "save with lolad msg read") {
		t.Fatal(mine)
	}
	dest := t.TempDir()
	if out := run("b", bHome, "download", id, "--dest", dest); !strings.Contains(out, filepath.Join(dest, id, "brief.md")) {
		t.Fatal(out)
	}
	// Replies carry files too, and a tampered store is refused on download.
	reply := strings.TrimSpace(run("b", bHome, "reply", id, "thanks, edited", "--attach", saved))
	m, _ := p.Get(t.Context(), reply)
	if len(m.Attachments) != 1 {
		t.Fatalf("reply attachment: %+v", m.Attachments)
	}
	os.WriteFile(filepath.Join(state, "attachments", m.Attachments[0].ID), []byte("tampered"), 0600)
	if out := run("a", aHome, "read", reply); !strings.Contains(out, "digest mismatch") {
		t.Fatalf("tampered file not caught:\n%s", out)
	}
}
