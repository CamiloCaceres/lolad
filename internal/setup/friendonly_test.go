package setup

import (
	"context"
	"testing"
)

// TestFriendOnlyIsSetUp: someone with no harness at all who connected with a
// friend is set up. Nothing sends them back to onboarding, and the state
// survives a restart.
func TestFriendOnlyIsSetUp(t *testing.T) {
	e := newEnv(t, nil) // no harness on this machine
	ctx := context.Background()
	st, err := e.m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.FirstReady(); ok || st.Done || !e.m.Needed() {
		t.Fatalf("fresh install without a harness: done=%v needed=%v", st.Done, e.m.Needed())
	}
	yes := true
	if st, err = e.m.Save(ctx, SaveRequest{FriendOnly: &yes}); err != nil {
		t.Fatal(err)
	}
	if !st.Done || st.Step != StepDone || !st.FriendOnly || e.m.Needed() {
		t.Fatalf("after connecting with a friend: %+v needed=%v", st, e.m.Needed())
	}
	again := New(e.m.opts)
	again.noSystemDirs = true
	if st, _ = again.Status(ctx); !st.Done || again.Needed() {
		t.Fatalf("after a restart: done=%v needed=%v", st.Done, again.Needed())
	}
}

// TestFriendOnlyResumesWhenAHarnessArrives: once they install Claude, the
// steps that make their own assistants are offered again, still set up.
func TestFriendOnlyResumesWhenAHarnessArrives(t *testing.T) {
	s := Status{FriendOnly: true, ServiceDone: true, Harnesses: []Harness{{ID: "claude", Ready: true, Supported: true}}}
	s.Settle()
	if !s.Done || s.Step != StepFolder {
		t.Fatalf("%+v", s)
	}
}
