package fake

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
)

func (s *Service) CreateOptions(context.Context) (core.CreateOptions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := []core.AdoptCandidate{
		{Dir: "/Users/you/agents/atlas", Name: "Atlas", Slug: "atlas", Harness: core.HarnessClaude, Model: "claude-sonnet-5-5[1m]"},
		{Dir: "/Users/you/agents/archive-helper", Name: "Archive helper", Slug: "archive-helper", Harness: core.HarnessCodewhale, Error: "CLAUDE.md is missing. Restore it before adopting."},
	}
	for i := range candidates {
		for _, agent := range s.agents {
			if candidates[i].Dir == agent.Dir {
				candidates[i].Registered = true
				candidates[i].Name = agent.Display
				candidates[i].Slug = agent.Name
			}
		}
	}
	for _, agent := range s.agents {
		found := false
		for _, candidate := range candidates {
			if candidate.Dir == agent.Dir {
				found = true
				break
			}
		}
		if found {
			continue
		}
		candidates = append(candidates, core.AdoptCandidate{Dir: agent.Dir, Name: agent.Display, Slug: agent.Name, Harness: agent.Runtime.Harness, Model: agent.Runtime.Model, Registered: true})
	}
	return core.CreateOptions{DefaultDir: "/Users/you/agents", Candidates: candidates}, nil
}
