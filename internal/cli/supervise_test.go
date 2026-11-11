package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testSupervise(codes []int, runFor time.Duration) (int, []time.Duration, string, int) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	var sleeps []time.Duration
	var log bytes.Buffer
	i := 0
	o := defaultSuperviseOptions(func(context.Context) (int, error) {
		clk.t = clk.t.Add(runFor)
		c := codes[i]
		i++
		return c, nil
	}, &log)
	o.Now = clk.now
	o.Sleep = func(_ context.Context, d time.Duration) bool {
		sleeps = append(sleeps, d)
		clk.t = clk.t.Add(d)
		return true
	}
	rc := supervise(context.Background(), o)
	return rc, sleeps, log.String(), i
}

func TestSuperviseBackoffAndGiveUp(t *testing.T) {
	codes := make([]int, 20)
	for i := range codes {
		codes[i] = 1
	}
	rc, sleeps, log, runs := testSupervise(codes, time.Second)
	if rc != 1 || runs != 10 {
		t.Fatalf("rc=%d runs=%d", rc, runs)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if sleeps[i] != w*time.Second {
			t.Fatalf("sleep %d = %v want %v (%v)", i, sleeps[i], w*time.Second, sleeps)
		}
	}
	if !strings.Contains(log, "giving up") {
		t.Fatal(log)
	}
}

func TestSuperviseCleanExitNotRestarted(t *testing.T) {
	rc, sleeps, _, runs := testSupervise([]int{1, 0}, time.Second)
	if rc != 0 || runs != 2 || len(sleeps) != 1 {
		t.Fatalf("rc=%d runs=%d sleeps=%v", rc, runs, sleeps)
	}
}

func TestSuperviseWindowForgetsOldCrashes(t *testing.T) {
	// Each run lasts 5 minutes: only two crashes fit in the 10 minute window,
	// so it never gives up; it ends when the fake child finally exits 0.
	codes := make([]int, 30)
	for i := range codes {
		codes[i] = 1
	}
	codes[29] = 0
	rc, sleeps, _, runs := testSupervise(codes, 5*time.Minute)
	if rc != 0 || runs != 30 {
		t.Fatalf("rc=%d runs=%d", rc, runs)
	}
	// Stable runs reset the backoff to 1s every time.
	for _, s := range sleeps {
		if s != time.Second {
			t.Fatalf("backoff not reset: %v", sleeps)
		}
	}
}

func TestSuperviseCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var log bytes.Buffer
	o := defaultSuperviseOptions(func(ctx context.Context) (int, error) { cancel(); return 1, nil }, &log)
	if rc := supervise(ctx, o); rc != 0 {
		t.Fatal(rc)
	}
}
