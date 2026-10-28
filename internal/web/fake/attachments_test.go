package fake

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestAttachmentsLikeTheProvider(t *testing.T) {
	ctx := context.Background()
	s := New(Options{})
	a, err := s.UploadAttachment(ctx, "../notes.txt", "", strings.NewReader("hello"))
	if err != nil || a.Filename != "notes.txt" || a.ContentType != "text/plain" || a.ScanStatus != "basic_clean" || !strings.HasPrefix(a.Digest, "sha256:") {
		t.Fatalf("upload = %+v, %v", a, err)
	}
	if _, rc, err := s.OpenAttachment(ctx, a.ID); err != nil {
		t.Fatal(err)
	} else if b, _ := io.ReadAll(rc); string(b) != "hello" {
		t.Errorf("read back %q", b)
	}
	if _, err := s.UserSend(ctx, core.Outgoing{To: "jugui@jugui.lolad.local", Subject: "x", Body: "x", Attachments: []string{a.ID}}); err == nil || !strings.Contains(err.Error(), "another lolad") {
		t.Errorf("remote send with file: %v", err)
	}
	m, err := s.UserSend(ctx, core.Outgoing{To: "forge", Subject: "x", Body: "x", Attachments: []string{a.ID}})
	if err != nil || len(m.Attachments) != 1 || m.Attachments[0].ID != a.ID {
		t.Fatalf("send = %+v, %v", m, err)
	}
	if _, err := s.UserSend(ctx, core.Outgoing{To: "forge", Subject: "x", Body: "x", Attachments: []string{a.ID}}); err == nil {
		t.Error("an upload goes in one message only")
	}
	b, _ := s.UploadAttachment(ctx, "fake.png", "image/png", strings.NewReader("not a png"))
	if b.ScanStatus != "suspicious" {
		t.Errorf("mismatched type scan = %q", b.ScanStatus)
	}
	if _, err := s.UploadAttachment(ctx, "empty", "", strings.NewReader("")); err == nil {
		t.Error("empty upload accepted")
	}
}
