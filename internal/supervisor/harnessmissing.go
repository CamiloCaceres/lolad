package supervisor

import (
	"errors"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// harnessRetry is how often an agent whose harness is not installed looks
// for it again. Like a macOS permission, it spends no crash budget: once the
// user installs it, the agent starts within this long.
const harnessRetry = 30 * time.Second

// ProblemHarnessMissing is the kind of the problem below.
const ProblemHarnessMissing = "harness_missing"

func harnessName(h core.Harness) string {
	switch h {
	case core.HarnessCodewhale:
		return "Codewhale"
	default:
		return "Claude Code"
	}
}

// classifyHarnessMissing turns "the harness is not installed" into a problem
// that says so in plain words, instead of a raw lookup error and a crash
// loop. permissionGOOS picks the install instructions.
func classifyHarnessMissing(err error, h core.Harness, now time.Time) (core.AgentProblem, bool) {
	if !errors.Is(err, core.ErrHarnessMissing) {
		return core.AgentProblem{}, false
	}
	name := harnessName(h)
	hint := "Open Settings → Claude and set it up there"
	switch {
	case h == core.HarnessCodewhale:
		hint = "Install Codewhale, make sure `codewhale` runs in a terminal"
	case permissionGOOS == "windows":
		hint += ", or install it yourself in PowerShell: irm https://claude.ai/install.ps1 | iex"
	default:
		hint += " and press Install"
	}
	return core.AgentProblem{
		Kind:     ProblemHarnessMissing,
		Title:    name + " isn't installed",
		Hint:     hint + ". The assistant starts by itself as soon as lolad finds " + name + ", no restart needed.",
		Detail:   err.Error(),
		NeedsYou: true,
		Since:    now,
		Until:    now.Add(harnessRetry),
	}, true
}
