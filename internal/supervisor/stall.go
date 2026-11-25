package supervisor

import (
	"strconv"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// stallAfter is how long a message may wait, unannounced, for an agent that
// is idle or unsure before lolad looks again; see checkStalled.
const stallAfter = 10 * time.Minute

// unresponsive is the problem raised when mail keeps waiting and the screen
// does not show the agent ready for it.
const unresponsive = "unresponsive"

// checkStalled keeps mail from waiting silently. Delivery only types a notice
// into an idle agent whose screen shows its prompt; when either is wrong
// (the state went unsure, or the screen reads as not ready), nothing else
// would ever try again. So once a message has waited stallAfter for an idle
// or unsure agent: if its prompt shows, it is marked idle and every waiting
// notice is retried now; if not, or if that retry still gets nothing through
// within stallAfter, the agent gets a problem the user can see and act on.
// The problem clears by itself when the screen changes to show the prompt,
// on the agent's next hook, or on a restart.
func (s *Supervisor) checkStalled(r *record, now time.Time) {
	if p := r.Status.Problem; p != nil {
		if p.Kind == unresponsive && now.Sub(r.StallCheckAt) >= 30*time.Second {
			r.StallCheckAt = now
			// Ready again means the screen changed to show the prompt. A
			// problem raised while the prompt already showed (notices typed
			// into it went nowhere) waits for a hook or the mail being read.
			if ready, pane := s.screenReady(r); ready && pane != r.StallPane {
				s.clearProblem(r, "ready for messages again", true)
				s.rearmWaiting(r, now)
			}
		}
		return
	}
	if r.Status.State != core.StateIdle && r.Status.State != core.StateUnknown {
		r.StallRetryAt = time.Time{}
		return
	}
	waited, waiting := s.longestWait(r, now)
	if !waiting {
		r.StallRetryAt = time.Time{} // all told or read: start over next time
		return
	}
	if waited < stallAfter {
		return
	}
	if !r.StallRetryAt.IsZero() && now.Sub(r.StallRetryAt) < stallAfter {
		return
	}
	ready, pane := s.screenReady(r)
	if ready && r.StallRetryAt.IsZero() {
		r.StallRetryAt = now
		if r.Status.State != core.StateIdle {
			s.setState(r, core.StateIdle, "prompt showing")
		}
		s.rearmWaiting(r, now)
		s.event(r.Status.Name, "delivery", "a message has waited "+minutes(waited)+"; its prompt is showing, so telling it again")
		return
	}
	r.StallRetryAt, r.StallPane = now, pane
	s.setProblem(r, core.AgentProblem{
		Kind:     unresponsive,
		Title:    "Isn't responding to messages",
		Hint:     "A message has waited " + minutes(waited) + " and its screen doesn't show it ready for one. Open its screen to see what it shows, or choose Restart in its Actions menu.",
		Detail:   screenLine(pane, []string{"esc to", "allow", "confirm", "trust", "approve", "error"}),
		NeedsYou: true,
		Screen:   true,
		Since:    now,
	})
}

// longestWait is how long the oldest stalled message has waited since it
// arrived or was last announced. A message is stalled when it was never
// announced and no notice is on its way: none was ever tried (the notifier
// keeps finding the agent not ready), or every scheduled one failed. While
// the retry schedule still runs, it is left to it.
// waiting reports whether any message is still unannounced at all.
func (s *Supervisor) longestWait(r *record, now time.Time) (most time.Duration, waiting bool) {
	for _, p := range s.queues[r.Status.Name] {
		if p.Confirmations > 0 {
			continue
		}
		waiting = true
		if p.Attempts > 0 && p.Attempts < len(retryDelays) {
			continue
		}
		since := p.Mail.CreatedAt
		if p.LastNotice.After(since) {
			since = p.LastNotice
		}
		if since.IsZero() {
			continue
		}
		if d := now.Sub(since); d > most {
			most = d
		}
	}
	return most, waiting
}

// rearmWaiting lets every unannounced message be tried again now.
func (s *Supervisor) rearmWaiting(r *record, now time.Time) {
	for _, p := range s.queues[r.Status.Name] {
		if p.Confirmations > 0 {
			continue
		}
		p.Attempts, p.Next, p.GaveUpAt, p.LastBlock = 0, now, time.Time{}, time.Time{}
		s.savePending(r.Status.Name, p)
	}
}

func (s *Supervisor) screenReady(r *record) (bool, string) {
	pane, e := s.opts.Sessions.Capture(r.Status.Name, 40, false)
	if e != nil {
		return false, ""
	}
	l := s.opts.Launchers[r.Status.Runtime.Harness]
	return l != nil && l.PromptReady(pane), pane
}

func minutes(d time.Duration) string {
	m := int(d.Minutes())
	if m == 1 {
		return "1 minute"
	}
	return strconv.Itoa(m) + " minutes"
}
