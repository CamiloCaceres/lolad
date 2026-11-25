package supervisor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// missingLauncher fails Prepare like harness.Adapter when claude is nowhere.
type missingLauncher struct {
	fakeLauncher
	missing bool
	tries   int
}

func (m *missingLauncher) Prepare(a core.Agent, o core.LaunchOpts) (core.LaunchSpec, error) {
	m.tries++
	if m.missing {
		return core.LaunchSpec{}, fmt.Errorf("claude is not installed: %w", core.ErrHarnessMissing)
	}
	return m.fakeLauncher.Prepare(a, o)
}

// Fresh Windows install: no "Something went wrong" loop into "Can't start",
// but one plain problem, and the agent starts once Claude Code is installed.
func TestHarnessMissingWaitsWithoutCrashing(t *testing.T) {
	old := permissionGOOS
	permissionGOOS = "windows"
	t.Cleanup(func() { permissionGOOS = old })
	m := &missingLauncher{missing: true}
	f := setup(t, func(o *Options) {
		o.Launchers = map[core.Harness]core.Launcher{core.HarnessClaude: m, core.HarnessCodewhale: m}
	})
	f.tick(t)
	for i := 0; i < 10; i++ {
		a := f.state(t)
		if a.State != core.StateWaiting || a.Problem == nil || a.Problem.Kind != ProblemHarnessMissing || !a.Problem.NeedsYou ||
			a.Problem.Title != "Claude Code isn't installed" || !strings.Contains(a.Problem.Hint, "Settings → Claude") ||
			!strings.Contains(a.Problem.Hint, "irm https://claude.ai/install.ps1 | iex") ||
			a.Restarts != 0 || a.NextRestart.Sub(f.c.Now()) != harnessRetry {
			t.Fatalf("attempt %d: %+v problem %+v", i, a, a.Problem)
		}
		tries := m.tries
		f.c.advance(harnessRetry - time.Second)
		f.tick(t)
		if m.tries != tries {
			t.Fatal("looked again too early")
		}
		f.c.advance(time.Second)
		f.tick(t)
		if m.tries != tries+1 {
			t.Fatalf("attempt %d: did not look again", i)
		}
	}
	m.missing = false // installed
	f.c.advance(harnessRetry)
	f.tick(t)
	if a := f.state(t); a.State != core.StateStarting || a.Problem != nil || a.Restarts != 0 || f.sessions.starts != 1 {
		t.Fatalf("after install: %+v problem %+v", a, a.Problem)
	}
}

func TestClassifyHarnessMissing(t *testing.T) {
	now := time.Now()
	if _, ok := classifyHarnessMissing(fmt.Errorf("boom"), core.HarnessClaude, now); ok {
		t.Fatal("ordinary error")
	}
	p, ok := classifyHarnessMissing(fmt.Errorf("x: %w", core.ErrHarnessMissing), core.HarnessCodewhale, now)
	if !ok || p.Title != "Codewhale isn't installed" || !strings.Contains(p.Hint, "codewhale") {
		t.Fatalf("%+v", p)
	}
}
