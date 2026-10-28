package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// One page or fragment reads the agent list and conversations several times
// over: the view, the conversation list, the nav badges, a thread header.
// reqCache keeps the first answer for the rest of a GET, so every live
// refresh costs one read of each. Writes get no cache: what they render
// afterwards must show their own effect.
type reqCache struct {
	agents    []core.AgentStatus
	agentsErr error
	hasAgents bool
	convs     []core.Conversation
	convsErr  error
	hasConvs  bool
	asks      map[string][]msgView
}

type reqCacheKey struct{}

func cached(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			r = r.WithContext(context.WithValue(r.Context(), reqCacheKey{}, &reqCache{}))
		}
		h.ServeHTTP(w, r)
	})
}

func cacheOf(ctx context.Context) *reqCache {
	c, _ := ctx.Value(reqCacheKey{}).(*reqCache)
	return c
}

// list is svc.List, once per request. Callers get their own copy to sort.
func (s *server) list(ctx context.Context) ([]core.AgentStatus, error) {
	c := cacheOf(ctx)
	if c == nil {
		return s.svc.List(ctx)
	}
	if !c.hasAgents {
		c.agents, c.agentsErr = s.svc.List(ctx)
		c.hasAgents = true
	}
	return slices.Clone(c.agents), c.agentsErr
}

// agentStatus is svc.Get, answered from the request's agent list when it has one.
func (s *server) agentStatus(ctx context.Context, name string) (core.AgentStatus, error) {
	if cacheOf(ctx) == nil {
		return s.svc.Get(ctx, name)
	}
	list, err := s.list(ctx)
	if err != nil {
		return core.AgentStatus{}, err
	}
	for _, a := range list {
		if a.Name == name {
			return a, nil
		}
	}
	return core.AgentStatus{}, fmt.Errorf("agent %q: %w", name, core.ErrNotFound)
}

// conversations is svc.Conversations, once per request.
func (s *server) conversations(ctx context.Context) ([]core.Conversation, error) {
	c := cacheOf(ctx)
	if c == nil {
		return s.svc.Conversations(ctx)
	}
	if !c.hasConvs {
		c.convs, c.convsErr = s.svc.Conversations(ctx)
		c.hasConvs = true
	}
	return slices.Clone(c.convs), c.convsErr
}
