package msgcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// A file that can't be fetched (expired on the server, here deleted) never
// holds back the user's mail: the message comes back with the file listed as
// not received, the next message comes back too, and nothing repeats.
func TestGetUserMessagesSkipsFileItCannotFetch(t *testing.T) {
	p, c, state := mailbox(t)
	home := t.TempDir()
	t.Setenv("LOLABOT_HOME", home)
	t.Setenv("LOLAD_HARNESS", "claude")
	ctx := t.Context()
	gone, err := p.UploadAttachment(ctx, "milo", "report.txt", "text/plain", strings.NewReader("old"))
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := p.UploadAttachment(ctx, "milo", "nul.log", "text/plain", strings.NewReader("log lines"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Files", Body: "Here are the files.", Attachments: []string{gone.ID, reserved.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(state, "attachments", gone.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Later", Body: "And one more thing."}); err != nil {
		t.Fatal(err)
	}

	res, err := c.getUserMessages()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From the user (2 messages; 0 requests still open):",
		"   Here are the files.\n   File: .user-channel/files/" + reserved.ID + "/_nul.log",
		"   File not received: report.txt (",
		"   And one more thing.",
	} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text lacks %q:\n%s", want, res.Text)
		}
	}
	msgs := canon(t, res.Structured).(map[string]any)["messages"].([]any)
	first := msgs[0].(map[string]any)
	if u := first["unavailable"].([]any); len(u) != 1 || u[0].(map[string]any)["name"] != "report.txt" {
		t.Fatalf("unavailable: %v", first)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".user-channel", "files", reserved.ID, "_nul.log")); err != nil || string(b) != "log lines" {
		t.Fatalf("saved file: %q %v", b, err)
	}
	if res, err = c.getUserMessages(); err != nil || res.Text != "Nothing new from the user (0 requests still open)." {
		t.Fatalf("second call: %q %v", res.Text, err)
	}
}

func TestSafeNameWindowsReserved(t *testing.T) {
	for in, want := range map[string]string{
		"CON.txt": "_CON.txt", "nul.log": "_nul.log", "aux": "_aux", "Com1.tar.gz": "_Com1.tar.gz", "lpt9": "_lpt9",
		"console.txt": "console.txt", "com10": "com10", "lpt0.txt": "lpt0.txt", "nul_.log": "nul_.log",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
