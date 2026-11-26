package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// A paused assistant says why in plain words, when it continues, and offers
// a way to act; the seed's Iris has hit a usage limit.
func TestAgentProblemShown(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	srv := httptest.NewServer(web.New(f, web.Options{}))
	t.Cleanup(srv.Close)
	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	b := get("/agents/iris")
	for _, want := range []string{"Paused", "Reached its Claude usage limit", "Tries again by itself", "Try now", "What it said"} {
		if !strings.Contains(b, want) {
			t.Errorf("agent page missing %q", want)
		}
	}
	if b = get("/agents"); !strings.Contains(b, "Reached its Claude usage limit") {
		t.Error("roster does not explain the pause")
	}
}

// blockedProblem makes Relay (waiting on a permission prompt in the seed)
// carry a Problem only the user can fix.
type blockedProblem struct{ core.Service }

func (b blockedProblem) List(ctx context.Context) ([]core.AgentStatus, error) {
	list, err := b.Service.List(ctx)
	for i := range list {
		if list[i].Name == "relay" {
			list[i].State = core.StateWaiting
			list[i].Problem = &core.AgentProblem{Kind: "login", Title: "Needs you to sign in again", Hint: "Open it and sign in.", NeedsYou: true}
		}
	}
	return list, err
}

// Home names a waiting assistant's problem with the assistant's own name
// (regression: the strip read $.Display off the page data and Home failed to render).
func TestHomeBlockedProblem(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	srv := httptest.NewServer(web.New(blockedProblem{f}, web.Options{}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "Relay: Needs you to sign in again") {
		t.Fatalf("home: %d\n%s", resp.StatusCode, b)
	}
}
