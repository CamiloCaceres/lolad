package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
