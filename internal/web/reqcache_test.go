package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// counting tallies the service reads a request makes.
type counting struct {
	core.Service
	list, get, convs atomic.Int32
}

func (c *counting) List(ctx context.Context) ([]core.AgentStatus, error) {
	c.list.Add(1)
	return c.Service.List(ctx)
}
func (c *counting) Get(ctx context.Context, name string) (core.AgentStatus, error) {
	c.get.Add(1)
	return c.Service.Get(ctx, name)
}
func (c *counting) Conversations(ctx context.Context) ([]core.Conversation, error) {
	c.convs.Add(1)
	return c.Service.Conversations(ctx)
}

// Live refreshes fire several requests per event; each must read the agent
// list and conversations once, not once per helper that wants them.
func TestOneReadPerRequest(t *testing.T) {
	f := fake.New(fake.Options{})
	t.Cleanup(f.Close)
	c := &counting{Service: f}
	srv := httptest.NewServer(web.New(c, web.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)
	for _, p := range []string{"/", "/inbox/lola", "/inbox/lola/messages", "/inbox/lola/head", "/fragments/conversations?active=lola", "/fragments/nav", "/agents", "/agents/lola", "/agents/lola/part/head"} {
		c.list.Store(0)
		c.get.Store(0)
		c.convs.Store(0)
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", p, resp.StatusCode)
		}
		if n := c.list.Load() + c.get.Load(); n > 1 {
			t.Errorf("GET %s read agents %d times (List %d, Get %d)", p, n, c.list.Load(), c.get.Load())
		}
		if n := c.convs.Load(); n > 1 {
			t.Errorf("GET %s read conversations %d times", p, n)
		}
	}
}
