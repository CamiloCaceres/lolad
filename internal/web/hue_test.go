package web

import (
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func TestHuesSpreadAcrossTheTeam(t *testing.T) {
	defer hueTable.Store(nil)
	var list []core.AgentStatus
	for _, n := range []string{"Aurelio", "Chiqui", "Heimdall", "Iris", "Jarvis", "Mechita", "Sebastian", "sgi-dev", "Tomas", "Ultron"} {
		list = append(list, core.AgentStatus{Agent: core.Agent{Name: n, Display: n}})
	}
	setRoster(list)
	count := map[string]int{}
	for _, a := range list {
		count[hue(a.Display)]++
	}
	for h, n := range count {
		if n > 2 {
			t.Errorf("%s used %d times, want at most 2: %v", h, n, count)
		}
	}
	if hue("Iris") != hue("iris") {
		t.Error("hue should ignore case")
	}
	if got := hue("Nobody"); got != "h"+itoa6("Nobody") {
		t.Errorf("unknown names fall back to their own hue, got %s", got)
	}
}

func itoa6(name string) string { return string(rune('0' + hueIndex(name))) }
