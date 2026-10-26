package web

import (
	"github.com/CamiloCaceres/lolad/internal/core"
	"testing"
	"time"
)

func TestNextAgentTrigger(t *testing.T) {
	now := time.Date(2026, 10, 26, 7, 0, 0, 0, time.FixedZone("test", -5*3600))
	rt := core.Runtime{Triggers: []core.Trigger{{Cron: "bad"}, {Mail: "ops@example.com", Every: "5m"}, {Cron: "0 9 * * *"}, {Cron: "30 7 * * *"}}}
	want := now.Add(30 * time.Minute)
	if got := nextAgentTrigger(rt, now); !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
	if got := nextAgentTrigger(core.Runtime{Triggers: []core.Trigger{{Webhook: "calendar"}, {Mail: "ops@example.com"}}}, now); !got.IsZero() {
		t.Fatalf("event-driven triggers cannot have invented next-run timestamps: %s", got)
	}
}

func TestRosterUnknownStateNeedsAttention(t *testing.T) {
	v := fleetView{Agents: []core.AgentStatus{
		{Agent: core.Agent{Name: "healthy"}, State: core.StateIdle},
		{Agent: core.Agent{Name: "silent"}, State: core.StateUnknown},
	}}
	v.filter("", "attention")
	if v.NeedsAttention != 1 || len(v.Groups) != 1 || len(v.Groups[0].Agents) != 1 || v.Groups[0].Agents[0].Name != "silent" {
		t.Fatalf("unknown state must surface for inspection: %+v", v)
	}
}
