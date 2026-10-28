package supervisor

import (
	"context"
	"time"

	"github.com/CamiloCaceres/lolad/internal/agentcfg"
	"github.com/CamiloCaceres/lolad/internal/core"
)

// Commander types a harness-local slash command (one that starts no model
// turn) into an idle session. deliver.Router implements it.
type Commander interface {
	Command(ctx context.Context, a core.Agent, text string) (core.NotifyResult, error)
}

// launchFields keeps the runtime fields a session reads only at launch. Model
// and skills mode are compared separately so the banner can name them.
func launchFields(rt core.Runtime) core.Runtime {
	out := core.Runtime{Harness: rt.Harness, Permissions: rt.Permissions, EnvFile: rt.EnvFile}
	if len(rt.Env) > 0 {
		out.Env = rt.Env
	}
	if len(rt.Args) > 0 {
		out.Args = rt.Args
	}
	return out
}

// hotReload applies skill and plugin updates to an idle claude session in
// place instead of asking for a restart: updated plugin versions first, then
// skill folders. Which plugins are enabled is read only at launch (see
// pendingRestart), and Claude Code's /model also rewrites the user's global
// default, so a model change stays a restart.
func (s *Supervisor) hotReload(ctx context.Context, r *record, now time.Time) {
	if r.Status.Runtime.Harness != core.HarnessClaude || r.Status.State != core.StateIdle || r.Restarting || len(r.LaunchFingerprint) == 0 || now.Before(r.NextHotCheck) {
		return
	}
	c, ok := s.opts.Notifier.(Commander)
	if !ok || r.Status.Runtime.SkillsMode() != r.LaunchRuntime.SkillsMode() {
		return
	}
	r.NextHotCheck = now.Add(15 * time.Second)
	name := r.Status.Name
	_, parts := agentcfg.New(r.Status.Dir, r.Status.Runtime.Harness, agentcfg.Options{}).Fingerprint(r.Status.Runtime.EnvFile, r.Status.Runtime)
	for _, step := range []struct{ part, command string }{{"plugins", "/reload-plugins"}, {"skills", "/reload-skills"}} {
		if parts[step.part] == r.LaunchFingerprint[step.part] {
			continue
		}
		result, e := c.Command(ctx, r.Status.Agent, step.command)
		if result == core.NotifyBusy && e == nil {
			return // input not empty or agent no longer idle; the next check retries
		}
		if e != nil || result != core.NotifyConfirmed {
			detail := step.command + " was not accepted"
			if e != nil {
				detail = step.command + ": " + e.Error()
			}
			s.event(name, "error", "hot reload: "+detail)
			r.NextHotCheck = now.Add(2 * time.Minute)
			return
		}
		r.LaunchFingerprint[step.part] = parts[step.part]
		s.event(name, "reloaded", step.command)
		_ = s.persist(r)
	}
}
