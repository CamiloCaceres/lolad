package fake

import (
	"fmt"
	"slices"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

func ptr[T any](v T) *T { return &v }

type agentSeed struct {
	name, display, role string
	rt                  core.Runtime
	state               core.State
	detail              string
	restarts            int
	lastErr             string
	up                  time.Duration // how long the current process has been running
}

// seed builds a small company: milo, Lola as chief of staff over an
// engineering and a growth team, a bookkeeper in her office, and an
// independent personal assistant, Ultron.
func (s *Service) seed() {
	now := s.now()
	s.org = core.Org{
		Tenant: "lumio",
		User:   core.User{Name: "milo", Display: "Camilo Caceres"},
		Teams: []core.Team{
			{ID: "hq", Name: "Lumio HQ", Head: "lola", Members: []string{"ledger"},
				Instructions: "The chief of staff's office. Lola routes work to the teams and keeps Milo's inbox short."},
			{ID: "eng", Name: "Engineering", Parent: "hq", Head: "forge", Members: []string{"patch", "relay"}, Open: true,
				Instructions: "Ship small, test everything, report merges to Lola."},
			{ID: "growth", Name: "Growth", Parent: "hq", Head: "juno", Members: []string{"quill", "scout"}, Open: true},
		},
		Rules: []core.Rule{
			{From: "ultron", To: "lola", Allow: true, Both: true, Note: "personal errands that need the company"},
			{From: "scout", To: "team:eng", Allow: false, Note: "research goes through Juno, not straight to engineering"},
			{From: "patch", To: "quill", Allow: true, Note: "release notes"},
		},
		Defaults: core.Defaults{UserToAgents: "all", AgentsToUser: "all", Vertical: true, Independents: "user-only"},
	}

	seeds := []agentSeed{
		{"lola", "Lola", "Chief of Staff", core.Runtime{Harness: core.HarnessClaude, Model: "claude-opus-5-5", Permissions: core.PermBypass,
			Rotate: "04:00", OnWake: "Read brain/ and memory/, then work through your inbox.", EnvFile: ".env",
			Env:      map[string]string{"LUMIO_ENV": "prod", "NOTES_DIR": "${LOLABOT_HOME}/notes"},
			Triggers: []core.Trigger{{Cron: "0 8 * * 1-5", Prompt: "Morning brief: what changed overnight and what needs Milo today."}}},
			core.StateBusy, "Bash: go test ./...", 0, "", 26 * time.Hour},
		{"forge", "Forge", "Head of Engineering", core.Runtime{Harness: core.HarnessClaude, Model: "claude-sonnet-5-5", Permissions: core.PermBypass, Rotate: "04:30"},
			core.StateIdle, "", 0, "", 26 * time.Hour},
		{"patch", "Patch", "Backend Engineer", core.Runtime{Harness: core.HarnessCodewhale, Permissions: core.PermBypass, Args: []string{"--profile", "backend"}},
			core.StateBusy, "apply_patch internal/billing/invoice.go", 1, "", 3 * time.Hour},
		{"relay", "Relay", "Frontend Engineer", core.Runtime{Harness: core.HarnessClaude, Model: "claude-sonnet-5-5", Permissions: core.PermDefault},
			core.StateWaiting, "Permission: Bash(rm -rf node_modules && npm ci --prefer-offline --no-audit --loglevel=error)", 0, "", 5 * time.Hour},
		{"juno", "Juno", "Head of Growth", core.Runtime{Harness: core.HarnessCodewhale, Model: "deepseek-v4", Permissions: core.PermBypass, Rotate: "05:00"},
			core.StateIdle, "", 0, "", 20 * time.Hour},
		{"quill", "Quill", "Writer", core.Runtime{Harness: core.HarnessClaude, Model: "claude-haiku-5-5", Permissions: core.PermBypass},
			core.StateCrashed, "", 2, "exit status 1: API overloaded (529)", 0},
		{"scout", "Scout", "Researcher", core.Runtime{Harness: core.HarnessCodewhale, Permissions: core.PermBypass, Autostart: ptr(false), WakeOnMessage: ptr(false)},
			core.StateStopped, "", 0, "", 0},
		{"ledger", "Ledger", "Bookkeeper", core.Runtime{Harness: core.HarnessCodewhale, Permissions: core.PermBypass,
			Triggers: []core.Trigger{{Mail: "ops@example.com", Every: "5m", Prompt: "New ops mail:"}}},
			core.StateFailed, "", 6, "exit status 127: tools/email.sh: No such file or directory", 0},
		{"ultron", "Ultron", "Personal Assistant", core.Runtime{Harness: core.HarnessClaude, Model: "claude-opus-5-5", Permissions: core.PermBypass,
			Triggers: []core.Trigger{{Webhook: "calendar", Prompt: "Calendar change:"}, {Cron: "30 7 * * *", Prompt: "Plan Milo's day."}}},
			core.StateIdle, "", 0, "", 50 * time.Hour},
		{"iris", "Iris", "Inbox Assistant", core.Runtime{Harness: core.HarnessClaude, Model: "claude-sonnet-5-5", Permissions: core.PermBypass, Skills: core.SkillsBarebones},
			core.StateIdle, "", 0, "", 9 * time.Hour},
	}
	for i, sd := range seeds {
		a := &core.AgentStatus{
			Agent:   core.Agent{Name: sd.name, Display: sd.display, Role: sd.role, Dir: "/Users/you/agents/" + sd.name, Runtime: sd.rt},
			Address: s.addr(sd.name), State: sd.state, Detail: sd.detail, Desired: "up",
			Restarts: sd.restarts, LastError: sd.lastErr, LastActivity: now.Add(-time.Duration(i*7+2) * time.Minute),
		}
		if sd.up > 0 {
			a.PID = 41200 + i*37
			a.StartedAt = now.Add(-sd.up)
			a.SessionSince = now.Add(-min(sd.up, 14*time.Hour))
		}
		switch sd.state {
		case core.StateStopped:
			a.Desired = "down"
			a.LastActivity = now.Add(-3 * 24 * time.Hour)
		case core.StateCrashed:
			a.NextRestart = now.Add(40 * time.Second)
			a.LastActivity = now.Add(-4 * time.Minute)
		case core.StateFailed:
			a.LastActivity = now.Add(-5*time.Hour - 50*time.Minute)
		}
		s.agents = append(s.agents, a)
	}
	s.seedSettings()

	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	h, m := time.Hour, time.Minute
	chat := func(kind string, options ...string) map[string]any {
		uc := map[string]any{"kind": kind}
		if len(options) > 0 {
			uc["options"] = options
		}
		return map[string]any{"userchat": uc}
	}
	decision := func(d string) map[string]any {
		return map[string]any{"userchat": map[string]any{"kind": "decision", "decision": d}}
	}
	put := func(at time.Time, from, to, typ, subject, body string, ctx map[string]any, st core.MailStatus) string {
		id := fmt.Sprintf("msg_%d_%08x", at.Unix(), len(s.mail)*2654435761%0xffffffff)
		ml := core.Mail{ID: id, ThreadID: id, From: s.addr(from), To: s.addr(to), FromName: from, ToName: to,
			Subject: subject, Priority: "normal", Type: typ, Body: body, Context: ctx,
			Signed: true, Verified: true, CreatedAt: at, Status: st}
		if st != core.MailUnread {
			ml.NotifiedAt = at.Add(20 * time.Second)
		}
		if st == core.MailRead {
			ml.ReadAt = at.Add(2 * time.Minute)
		}
		s.mail = append(s.mail, ml)
		return id
	}
	reply := func(parent string) func(string) string {
		return func(id string) string {
			for i := range s.mail {
				if s.mail[i].ID == id {
					s.mail[i].InReplyTo = parent
					for _, p := range s.mail {
						if p.ID == parent {
							s.mail[i].ThreadID = p.ThreadID
						}
					}
				}
			}
			return id
		}
	}
	R, N, U := core.MailRead, core.MailNotified, core.MailUnread

	put(ago(30*h), "juno", "milo", "request", "Newsletter cadence",
		"Open rates dropped to 31% on the weekly issue. Quill thinks we're sending too often. Move to every two weeks?",
		chat("ask", "Weekly", "Every two weeks"), R)
	ask0 := s.mail[len(s.mail)-1].ID
	reply(ask0)(put(ago(29*h), "milo", "juno", "response", "Re: Newsletter cadence", "Every two weeks", decision("Every two weeks"), R))
	put(ago(28*h+40*m), "juno", "milo", "notification", "Newsletter cadence",
		"Switched the newsletter to every two weeks. Next issue goes out on the 30th.", chat("say"), R)

	put(ago(26*h), "ultron", "milo", "notification", "Flight moved",
		"Your flight to Bogotá moved to 07:15 on Friday. I rebooked the airport car for 05:00 and moved the 9:00 call to Monday.", chat("say"), R)
	put(ago(25*h+50*m), "milo", "ultron", "notification", "Re: Flight moved", "Perfect, thanks.", chat("reply"), R)

	put(ago(8*h), "lolad", "lola", "system", "Morning brief",
		"Morning brief: what changed overnight and what needs Milo today.", nil, R)
	put(ago(5*h+50*m), "lola", "milo", "update", "Ledger is down",
		"Ledger crashed six times since 16:10 and the supervisor gave up on it. Its mail trigger can't find tools/email.sh, so nobody is reading ops@ right now. Looking into it.",
		chat("update"), R)
	t1 := put(ago(3*h), "milo", "lola", "notification", "Ledger + pricing",
		"Can you get Ledger back up? And I want the Q4 pricing page live this week.", chat("say"), R)
	reply(t1)(put(ago(2*h+50*m), "lola", "milo", "notification", "Re: Ledger + pricing",
		"On it. Ledger's `email.sh` moved in last week's lolabot update; Forge is patching the path and I'll restart it once that lands.\n\nPricing: Juno owns copy, Forge checks the Stripe side. Draft by this afternoon.",
		chat("say"), R))
	put(ago(2*h+45*m), "lola", "forge", "task", "Ledger is down",
		"Ledger crashes on start: `tools/email.sh: No such file or directory`. Find the new path and fix its mail trigger, please. Low risk, but ops@ is unread until it's back.", nil, R)
	put(ago(2*h+40*m), "forge", "patch", "task", "Fix Ledger mail trigger path",
		"tools/email.sh moved to tools/mail/email.sh in lolabot 0.9. Update ledger's lolabot.yaml trigger and anything else that shells out to it.", nil, R)
	put(ago(2*h), "forge", "milo", "update", "Ledger fix",
		"Found it: `tools/email.sh` moved to `tools/mail/email.sh` in lolabot 0.9. Patch is updating Ledger's trigger. ETA 20 minutes.", chat("update"), R)
	put(ago(1*h+58*m), "patch", "forge", "response", "Re: Fix Ledger mail trigger path",
		"Done in ledger/lolabot.yaml and tools/mail/README. Ledger needs a restart to pick it up; I can't restart other agents.", nil, R)
	put(ago(1*h+30*m), "lola", "juno", "request", "Pricing copy review",
		"Pricing page draft is on staging. Can Growth review the copy today? Team plan goes $24 → $29, annual discount 15% → 20%.", nil, R)
	put(ago(1*h+20*m), "juno", "quill", "request", "Tighten pricing page copy",
		"Tighten the hero and the plan blurbs. Keep \"no seat minimums\" — it tested best.", nil, R)
	put(ago(1*h+5*m), "lolad", "ultron", "system", "Calendar change",
		"Calendar change: Dr. Ruiz's office proposed new times for the cleaning.", nil, R)
	put(ago(50*m), "quill", "juno", "response", "Re: Tighten pricing page copy",
		"Copy v2 is in drafts/pricing-v2.md. Hero is down to 9 words; each plan blurb is one sentence.", nil, R)
	ask1 := put(ago(40*m), "ultron", "milo", "request", "Dentist",
		"Dr. Ruiz can do Tuesday 9:00 or Wednesday 16:30. Which one?", chat("ask", "Tue 9:00", "Wed 16:30", "Neither, next week"), R)
	put(ago(40*m), "juno", "lola", "response", "Re: Pricing copy review",
		"Copy reviewed and tightened (Quill's v2). Good to go from Growth.", nil, R)
	reply(ask1)(put(ago(35*m), "milo", "ultron", "response", "Re: Dentist", "Wed 16:30", decision("Wed 16:30"), R))
	put(ago(34*m), "ultron", "milo", "notification", "Dentist", "Booked Wednesday 16:30 with Dr. Ruiz. It's on your calendar with a reminder the evening before.", chat("say"), U)
	put(ago(30*m), "forge", "lola", "response", "Stripe price IDs",
		"Stripe price IDs on staging match the new plans. Webhooks tested with the CLI.", nil, N)
	put(ago(25*m), "lola", "milo", "notification", "Pricing page draft",
		"Draft is up at https://staging.example.com/pricing. Juno and Quill reviewed the copy; Forge checked the Stripe price IDs.\n\nChanges vs. current:\n- Team plan $24 → $29\n- Annual discount 15% → 20%\n- New \"Founders\" tier, hidden behind a link",
		chat("deliver"), R)
	put(ago(15*m), "forge", "milo", "update", "Invoice export", "Invoice CSV export is merged and deployed. 214 tests pass.", chat("update"), U)
	put(ago(12*m), "relay", "milo", "notification", "Stuck on a permission prompt",
		"My session is waiting on a permission prompt for `rm -rf node_modules` (the lockfile is corrupted). Can you approve it in my terminal, or should I work around it?",
		chat("say"), N)
	put(ago(9*m), "patch", "quill", "notification", "Release notes: invoice export",
		"For the changelog: invoices can now be exported as CSV from Billing → Invoices → Export. Filters apply to the export.", nil, U)
	put(ago(6*m), "lola", "milo", "request", "Ship pricing today?",
		"Legal hasn't reviewed the terms for the new Founders tier. Ship today without Founders, or hold everything until legal signs off (likely Thursday)?",
		chat("ask", "Ship without Founders", "Hold until Thursday", "Ship everything now"), U)

	ev := func(at time.Time, agent, kind, detail string) {
		s.seq++
		s.events = append(s.events, core.Event{ID: s.seq, Agent: agent, At: at, Kind: kind, Detail: detail})
	}
	for _, a := range s.agents {
		if !a.StartedAt.IsZero() {
			ev(a.StartedAt, a.Name, "started", fmt.Sprintf("pid %d, resumed last conversation", a.PID))
			ev(a.StartedAt.Add(4*time.Second), a.Name, "state", "idle")
		}
	}
	ev(ago(3*24*h), "scout", "exited", "stopped by user (graceful)")
	ev(ago(3*24*h), "scout", "state", "stopped")
	ev(ago(3*h+10*m), "ultron", "policy", "denied ultron → juno: default: independents user-only")
	ev(ago(1*h+12*m), "scout", "policy", "denied scout → forge: rule 2 denies (scout→team:eng)")
	for i := 0; i < 6; i++ {
		at := ago(6*h - time.Duration(i*i)*time.Minute)
		ev(at, "ledger", "crashed", "exit status 127: tools/email.sh: No such file or directory")
		if i < 5 {
			ev(at.Add(5*time.Second<<i), "ledger", "restarted", fmt.Sprintf("backoff %s", 5*time.Second<<i))
		}
	}
	ev(ago(5*h+50*m), "ledger", "state", "failed: 6 crashes in 30 min")
	ev(ago(8*h), "lola", "trigger", "cron 0 8 * * 1-5")
	ev(ago(8*h), "lola", "notified", "Stop hook: Morning brief")
	ev(ago(4*h), "lola", "rotated", "skipped: rotation at 04:00 ran on the previous day")
	ev(ago(1*h+5*m), "ultron", "trigger", "webhook calendar")
	ev(ago(2*h+39*m), "patch", "notified", "typed into session: confirmed")
	ev(ago(4*m+20*time.Second), "quill", "crashed", "exit status 1: API overloaded (529)")
	ev(ago(4*m), "quill", "state", "crashed: restart in 45s (attempt 3)")
	ev(ago(12*m), "relay", "state", "waiting: Permission: Bash(rm -rf node_modules)")
	ev(ago(3*m), "patch", "state", "busy: apply_patch internal/billing/invoice.go")
	ev(ago(2*m), "lola", "state", "busy: Bash: go test ./...")
	for _, ml := range s.mail {
		agent := ml.ToName
		if s.find(agent) == nil {
			agent = ml.FromName
		}
		ev(ml.CreatedAt, agent, "mail", fmt.Sprintf("%s → %s: %s", ml.FromName, ml.ToName, ml.Subject))
	}
	s.seedTrafficAudit()
	slices.SortStableFunc(s.events, func(a, b core.Event) int { return a.At.Compare(b.At) })
	for i := range s.events {
		s.events[i].ID = int64(i + 1)
	}
	s.seq = int64(len(s.events))
}
