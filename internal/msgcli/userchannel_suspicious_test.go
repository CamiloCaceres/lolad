package msgcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// A file the scan marked suspicious is not saved (as msg read): the agent is
// told it was held back and how to fetch it once the user agrees.
func TestGetUserMessagesHoldsBackSuspiciousFiles(t *testing.T) {
	p, c, _ := mailbox(t)
	home := t.TempDir()
	t.Setenv("LOLABOT_HOME", home)
	t.Setenv("LOLAD_HARNESS", "claude")
	ctx := t.Context()
	odd, err := p.UploadAttachment(ctx, "milo", "photo.png", "image/png", strings.NewReader("#!/bin/sh\necho not a picture\n"))
	if err != nil {
		t.Fatal(err)
	}
	if odd.ScanStatus != "suspicious" {
		t.Fatalf("scan status %q; the test needs a suspicious file", odd.ScanStatus)
	}
	m, err := p.Send(ctx, "milo", core.Outgoing{To: "a", Subject: "Photo", Body: "The photo.", Attachments: []string{odd.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.getUserMessages()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "   File not received: photo.png (held back: the scan marked it suspicious") ||
		!strings.Contains(res.Text, "lolad msg download "+m.ID+" --force") || strings.Contains(res.Text, "   File: ") {
		t.Fatalf("text:\n%s", res.Text)
	}
	if _, err := os.Stat(filepath.Join(home, ".user-channel", "files", odd.ID)); !os.IsNotExist(err) {
		t.Fatalf("suspicious file was saved: %v", err)
	}
}
