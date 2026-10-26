package fake

import (
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

var tools = []string{
	"Read internal/billing/invoice.go", "Bash: go test ./internal/...", "Edit web/pricing.tsx",
	"Grep \"price_id\"", "WebFetch stripe.com/docs/billing", "Bash: git diff --stat", "apply_patch README.md",
	"lolad msg inbox", "Write drafts/changelog.md",
}

type chatter struct{ from, to, typ, subject, body string }

// Mail agents send each other while the simulation runs. Every pair is
// allowed by the seeded org.
var traffic = []chatter{
	{"forge", "patch", "task", "Flaky test in billing", "TestInvoiceRounding fails about 1 in 20 runs on CI. Can you pin the clock in that test?"},
	{"patch", "forge", "response", "Re: Flaky test in billing", "Pinned the clock with a fake; 200 runs green. Merged."},
	{"juno", "scout", "request", "Competitor pricing", "Can you pull current pricing for the three closest competitors? Plans, seats, annual discount."},
	{"scout", "juno", "response", "Re: Competitor pricing", "Table is in research/pricing-2026-10.md. Two of three dropped seat minimums this quarter."},
	{"lola", "forge", "request", "Status for Milo's evening wrap", "One line on what Engineering shipped today, please."},
	{"forge", "lola", "response", "Re: Status for Milo's evening wrap", "Invoice CSV export shipped; Ledger's trigger path fixed; relay is blocked on a permission prompt."},
	{"relay", "patch", "request", "API shape for invoice export", "Is the export endpoint paginated, or do I stream the whole CSV?"},
	{"patch", "relay", "response", "Re: API shape for invoice export", "Streamed; no pagination. Content-Disposition is set, so a plain link works."},
	{"lola", "juno", "notification", "Founders tier", "Holding the Founders tier until legal reviews. Everything else on the pricing page can go."},
}

var userNotes = []chatter{
	{"forge", "milo", "update", "Deploy", "Deployed api v2026.10.25-3 to production. Error rate flat, p95 unchanged."},
	{"lola", "milo", "update", "Afternoon check-in", "All quiet: Engineering is on billing fixes, Growth is drafting the newsletter. Nothing needs you."},
	{"juno", "milo", "notification", "Newsletter draft", "Next issue is drafted: three items, 412 words. I'll send it Thursday unless you want to see it first."},
	{"ultron", "milo", "notification", "Tomorrow", "Tomorrow: 9:30 call with Mario, 14:00 dentist reminder, flight check-in opens at 07:15."},
}

var userAsks = []chatter{
	{"juno", "milo", "request", "Blog post title", "Quill has two titles for Thursday's post: \"Pricing without seat math\" or \"Why we dropped seat minimums\". Which one?|Pricing without seat math|Why we dropped seat minimums"},
	{"forge", "milo", "request", "Database upgrade window", "Postgres 18 upgrade needs ~4 minutes of read-only mode. Tonight at 23:00, or Saturday morning?|Tonight 23:00|Saturday 08:00|Not this week"},
	{"ultron", "milo", "request", "Gym", "Your Thursday class was cancelled. Book the 18:30 one instead?|Book 18:30|Skip this week"},
}

func (s *Service) run() {
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()
	step := 0
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
		s.mu.Lock()
		s.tick(step)
		s.mu.Unlock()
		step++
		t.Reset(time.Duration(3500+mrand.IntN(4000)) * time.Millisecond)
	}
}

// tick does one random thing. Caller holds mu.
func (s *Service) tick(step int) {
	now := s.now()
	for _, a := range s.agents {
		if a.State == core.StateCrashed && !a.NextRestart.IsZero() && now.After(a.NextRestart) {
			s.emit(a.Name, "restarted", fmt.Sprintf("after crash, attempt %d", a.Restarts+1))
			a.Restarts++
			s.boot(a, false)
			return
		}
	}
	switch r := mrand.IntN(10); {
	case r < 5:
		var live []*core.AgentStatus
		for _, a := range s.agents {
			if a.State == core.StateIdle || a.State == core.StateBusy {
				live = append(live, a)
			}
		}
		if len(live) == 0 {
			return
		}
		a := live[mrand.IntN(len(live))]
		if a.State == core.StateIdle {
			s.setState(a, core.StateBusy, tools[mrand.IntN(len(tools))])
		} else {
			s.setState(a, core.StateIdle, "")
		}
	case r < 8:
		c := traffic[step%len(traffic)]
		s.send(c)
	case r < 9:
		s.send(userNotes[mrand.IntN(len(userNotes))])
	default:
		c := userAsks[mrand.IntN(len(userAsks))]
		for _, m := range s.mail {
			if m.FromName == c.from && m.Subject == c.subject {
				return // asked already
			}
		}
		s.send(c)
	}
}

// send delivers a scripted message; a body with "|" carries ask options.
func (s *Service) send(c chatter) {
	out := core.Outgoing{To: c.to, Subject: c.subject, Body: c.body, Type: c.typ}
	if c.to == s.org.User.Name {
		kind := "say"
		if c.typ == "update" {
			kind = "update"
		}
		uc := map[string]any{"kind": kind}
		if parts := strings.Split(c.body, "|"); len(parts) > 1 {
			out.Body = parts[0]
			uc["kind"], uc["options"] = "ask", parts[1:]
		}
		out.Context = map[string]any{"userchat": uc}
	}
	if a := s.find(c.from); a != nil && a.State != core.StateIdle && a.State != core.StateBusy {
		return // only running agents talk
	}
	_, _ = s.deliver(c.from, c.to, out)
}

// scheduleReply makes the agent read the user's message, work briefly and
// answer. Caller holds mu.
func (s *Service) scheduleReply(agent string, m core.Mail) {
	a := s.find(agent)
	if a == nil || a.State == core.StateStopped || a.State == core.StateFailed {
		return
	}
	body := "Got it. I'll take it from here and report back when it's done."
	if uc, ok := m.Context["userchat"].(map[string]any); ok && uc["kind"] == "decision" {
		body = fmt.Sprintf("Going with \"%s\". I'll let the team know.", uc["decision"])
	}
	id, subject := m.ID, m.Subject
	time.AfterFunc(1500*time.Millisecond, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := range s.mail {
			if s.mail[i].ID == id {
				s.mail[i].Status, s.mail[i].NotifiedAt = core.MailNotified, s.now()
				s.emit(agent, "notified", "Stop hook: "+subject)
			}
		}
		if a := s.find(agent); a != nil && (a.State == core.StateIdle || a.State == core.StateBusy) {
			s.setState(a, core.StateBusy, "lolad msg read "+id[len(id)-8:])
		}
	})
	time.AfterFunc(5*time.Second, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		a := s.find(agent)
		if a == nil || a.State != core.StateBusy {
			return
		}
		for i := range s.mail {
			if s.mail[i].ID == id {
				s.mail[i].Status, s.mail[i].ReadAt = core.MailRead, s.now()
			}
		}
		if !strings.HasPrefix(subject, "Re: ") {
			subject = "Re: " + subject
		}
		_, _ = s.deliver(agent, s.org.User.Name, core.Outgoing{To: s.org.User.Name, Subject: subject, Body: body,
			InReplyTo: id, Type: "response", Context: map[string]any{"userchat": map[string]any{"kind": "say"}}})
		s.setState(a, core.StateIdle, "")
	})
}
