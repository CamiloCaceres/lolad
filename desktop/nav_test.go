package main

import (
	"reflect"
	"sync"
	"testing"
)

type navLog struct {
	mu  sync.Mutex
	got []string
}

func (l *navLog) gate() *navGate {
	return newNavGate(
		func(u string) { l.mu.Lock(); l.got = append(l.got, u); l.mu.Unlock() },
		func() { l.mu.Lock(); l.got = append(l.got, "reload"); l.mu.Unlock() },
	)
}

func (l *navLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	g := l.got
	l.got = nil
	return g
}

func TestNavGateHoldsUntilReady(t *testing.T) {
	var l navLog
	g := l.gate()
	g.Navigate("http://127.0.0.1:4100/")
	g.Reload()
	g.Navigate("/")
	g.Navigate("http://127.0.0.1:4100/welcome")
	if got := l.take(); len(got) != 0 {
		t.Fatalf("navigated before the webview was ready: %v", got)
	}
	if g.IsReady() {
		t.Fatal("IsReady before Ready")
	}
	g.Ready()
	if !g.IsReady() {
		t.Fatal("not IsReady after Ready")
	}
	if got, want := l.take(), []string{"http://127.0.0.1:4100/welcome"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("on ready got %v, want only the latest URL %v", got, want)
	}
	g.Ready() // a later navigation finishing must not replay anything
	if got := l.take(); len(got) != 0 {
		t.Fatalf("second Ready replayed %v", got)
	}
}

func TestNavGateReadyWithNothingPending(t *testing.T) {
	var l navLog
	g := l.gate()
	g.Ready()
	if got := l.take(); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

func TestNavGatePassesThroughOnceReady(t *testing.T) {
	var l navLog
	g := l.gate()
	g.Ready()
	g.Navigate("/a")
	g.Reload()
	g.Navigate("/b")
	if got, want := l.take(), []string{"/a", "reload", "/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Ready racing Navigate must end on the newest URL, never on the stale pending one.
func TestNavGateRaceKeepsNewest(t *testing.T) {
	for i := 0; i < 200; i++ {
		var l navLog
		g := l.gate()
		g.Navigate("/old")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); g.Ready() }()
		go func() { defer wg.Done(); g.Navigate("/new") }()
		wg.Wait()
		got := l.take()
		if len(got) == 0 || got[len(got)-1] != "/new" {
			t.Fatalf("run %d: got %v, want it to end on /new", i, got)
		}
	}
}
