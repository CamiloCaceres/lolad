package fake

import (
	"context"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestFederationDemoTransitions(t *testing.T) {
	s := New(Options{})
	defer s.Close()
	ctx := context.Background()
	cs, _ := s.Connections(ctx)
	cs[0].Shares[0].Local = "tampered"
	again, _ := s.Connections(ctx)
	if again[0].Shares[0].Local != "ultron" {
		t.Fatal("connections are not isolated copies")
	}
	g, e := s.DecideGrant(ctx, "jugui.lolad.local", "jugui@jugui.lolad.local", "ultron", true)
	if e != nil || g.State != core.GrantAllowed || g.Held != 0 {
		t.Fatal("approval did not release waiting grant")
	}
	if _, e = s.UpdateConnection(ctx, "jugui.lolad.local", core.ConnectionUpdate{Paused: new(true)}); e != nil {
		t.Fatal(e)
	}
	if s.Check(ctx, "milo", "jugui@jugui.lolad.local").Allow {
		t.Fatal("paused connection allows messages")
	}
}
