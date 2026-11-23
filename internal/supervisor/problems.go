package supervisor

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// stuckAfter is how long a turn may go without a single hook (no tool started
// or finished) before the agent is flagged as possibly stuck.
const stuckAfter = 45 * time.Minute

// classifyFailure turns a failed turn into a problem a non-technical user can
// act on. kind is Claude Code's StopFailure error type ("" for harnesses that
// only send text, like Codewhale's session_error); msg is what the harness
// said. ok is false when the failure needs no attention (a reply cut short).
func classifyFailure(harness core.Harness, kind, msg string, now time.Time) (p core.AgentProblem, ok bool) {
	p = core.AgentProblem{Since: now, Detail: msg}
	vendor, signIn := "Claude", "Open Settings, then AI tools, and press Sign in. Then choose Restart in its Actions menu."
	if harness == core.HarnessCodewhale {
		vendor, signIn = "its AI provider", "Check the API key in its Settings (Files, .env), then choose Restart in its Actions menu."
	}
	lower := strings.ToLower(msg)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	if kind == "" || kind == "unknown" {
		switch {
		case has("401", "unauthorized", "invalid api key", "authentication", "please run /login", "not logged in", "token has expired", "oauth"):
			kind = "authentication_failed"
		case has("402", "insufficient balance", "credit balance", "billing", "payment"):
			kind = "billing_error"
		case has("usage limit", "hit your limit", "limit reached", "resets "):
			kind = "rate_limit"
		case has("429", "rate limit", "rate_limit", "too many requests"):
			kind = "rate_limit"
		case has("529", "503", "overloaded", "capacity", "unavailable"):
			kind = "overloaded"
		case has("connection", "network", "timed out", "timeout", "econn", "enotfound", "fetch failed", "socket", "dns"):
			kind = "network"
		}
	}
	retry := func(d time.Duration) { p.Until = now.Add(d) }
	switch kind {
	case "max_output_tokens":
		return p, false
	case "authentication_failed":
		p.Kind, p.NeedsYou = "login", true
		p.Title = "Signed out of " + vendor
		if harness == core.HarnessCodewhale {
			p.Title = "Its API key was refused"
		}
		p.Hint = signIn
	case "oauth_org_not_allowed":
		p.Kind, p.NeedsYou = "login", true
		p.Title = "This Claude account can't be used here"
		p.Hint = "Sign in with another Claude account: open Settings, then AI tools, and press Sign in. Then choose Restart in its Actions menu."
	case "billing_error":
		p.Kind, p.NeedsYou = "billing", true
		p.Title = "Out of credit with " + vendor
		p.Hint = "Check your plan or credit with " + vendor + ", then choose Restart in its Actions menu."
		if harness != core.HarnessCodewhale {
			p.Title = "Your Claude account has a billing problem"
			p.Hint = "Check your plan at claude.ai (or your credit at console.anthropic.com if you use an API key), then choose Restart in its Actions menu."
		}
	case "account_on_hold", "verification_required":
		p.Kind, p.NeedsYou = "account", true
		p.Title = "Your Claude account needs attention"
		p.Hint = "Sign in at claude.ai to see what Anthropic needs, then choose Restart in its Actions menu."
	case "model_not_found":
		p.Kind, p.NeedsYou = "model", true
		p.Title = "Its AI model isn't available"
		p.Hint = "Pick another model in its Settings, then restart it."
	case "invalid_request":
		p.Kind, p.NeedsYou = "request", true
		p.Title = vendorCap(vendor) + " turned down its last request"
		p.Hint = "Choose Restart in its Actions menu. If it keeps happening, use Start fresh: it keeps its memory and files."
	case "cloud_credential_error":
		p.Kind, p.NeedsYou = "login", true
		p.Title = "Its cloud credentials stopped working"
		p.Hint = "Check the cloud keys in its Settings (Files, .env), then choose Restart in its Actions menu."
	case "rate_limit":
		if at, found := resetTime(msg, now); found || has("usage limit", "hit your limit", "limit reached", "resets") {
			p.Kind = "usage_limit"
			p.Title = "Reached its " + vendor + " usage limit"
			p.Hint = "It continues by itself when the limit resets. Messages wait until then. For more usage, check your plan."
			if found {
				p.Until = at.Add(time.Minute)
			} else {
				retry(30 * time.Minute)
			}
		} else {
			p.Kind = "rate_limit"
			p.Title = vendorCap(vendor) + " asked it to slow down"
			p.Hint = "It tries again by itself in a few minutes. Messages wait until then."
			retry(5 * time.Minute)
		}
	case "overloaded", "server_error":
		p.Kind = "overloaded"
		p.Title = vendorCap(vendor) + " is overloaded or having trouble"
		p.Hint = "This is on " + vendor + "'s side. It tries again by itself in a few minutes."
		retry(5 * time.Minute)
	case "network":
		p.Kind = "network"
		p.Title = "Can't reach " + vendor
		p.Hint = "Check this computer's internet connection. It tries again by itself in a few minutes."
		retry(5 * time.Minute)
	default:
		p.Kind = "error"
		p.Title = "Its last step failed with an error"
		p.Hint = "It tries again by itself in a few minutes. If this keeps happening, open its screen or restart it."
		retry(10 * time.Minute)
	}
	return p, true
}

func vendorCap(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var (
	resetUnix = regexp.MustCompile(`\|(\d{10})\b`)
	resetAt   = regexp.MustCompile(`(?i)resets\s+(?:at\s+)?(?:(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2}),?\s+(?:at\s+)?)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s*\(([^)]+)\))?`)
)

// resetTime reads when a usage limit resets from Claude Code's message:
// "You've hit your limit · resets 3pm (America/Bogota)", "resets Nov 25, 9:30am",
// or the older "Claude AI usage limit reached|1764000000".
func resetTime(msg string, now time.Time) (time.Time, bool) {
	if m := resetUnix.FindStringSubmatch(msg); m != nil {
		if n, e := strconv.ParseInt(m[1], 10, 64); e == nil {
			if t := time.Unix(n, 0); t.After(now) && t.Sub(now) < 8*24*time.Hour {
				return t, true
			}
		}
	}
	m := resetAt.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	loc := now.Location()
	if m[6] != "" {
		if l, e := time.LoadLocation(strings.TrimSpace(m[6])); e == nil {
			loc = l
		}
	}
	hour, _ := strconv.Atoi(m[3])
	minute, _ := strconv.Atoi(m[4])
	if hour < 1 || hour > 12 || minute > 59 {
		return time.Time{}, false
	}
	hour %= 12
	if strings.EqualFold(m[5], "pm") {
		hour += 12
	}
	local := now.In(loc)
	y, mo, d := local.Date()
	dated := m[1] != ""
	if dated {
		months := "janfebmaraprmayjunjulaugsepoctnovdec"
		mo = time.Month(strings.Index(months, strings.ToLower(m[1]))/3 + 1)
		d, _ = strconv.Atoi(m[2])
	}
	t := time.Date(y, mo, d, hour, minute, 0, 0, loc)
	if !t.After(now) {
		if dated {
			t = t.AddDate(1, 0, 0)
		} else {
			t = t.AddDate(0, 0, 1)
		}
	}
	if t.Sub(now) > 8*24*time.Hour {
		return time.Time{}, false
	}
	return t, true
}

// setProblem records p and moves the agent to the matching state: waiting
// when only the user can fix it, paused when lolad retries by itself. Mail
// is not delivered in either state, so no delivery attempt is wasted.
func (s *Supervisor) setProblem(r *record, p core.AgentProblem) {
	old := r.Status.Problem
	if old != nil && old.Kind == p.Kind && old.Title == p.Title {
		p.Since = old.Since // the same problem again: keep when it began
	}
	r.Status.Problem = &p
	state := core.StatePaused
	if p.NeedsYou {
		state = core.StateWaiting
	}
	if p.Kind == "stuck" {
		state = r.Status.State
	}
	detail := p.Title
	if p.Kind == "dialog" || p.Screen {
		detail = r.Status.Detail // keep "startup blocked: …" so the startup checks keep running
	}
	s.setState(r, state, detail)
	r.Status.Problem = &p // setState may clear screen problems; this one is new
	if old == nil || old.Kind != p.Kind || old.Title != p.Title {
		ev := p.Title
		if p.Detail != "" {
			ev += ": " + p.Detail
		}
		s.event(r.Status.Name, "problem", ev)
	}
}

// clearProblem forgets the problem and re-arms mail notices that failed
// while it lasted. With retry, it also lets the agent take mail again: Ready
// if its prompt is showing, otherwise unsure.
func (s *Supervisor) clearProblem(r *record, reason string, retry bool) {
	if r.Status.Problem == nil {
		return
	}
	r.Status.Problem = nil
	s.event(r.Status.Name, "problem-cleared", reason)
	now := s.opts.Clock.Now()
	for _, p := range s.queues[r.Status.Name] {
		if p.Confirmations == 0 && p.Attempts > 0 {
			p.Attempts = 0
			p.Next = now
			p.GaveUpAt = time.Time{}
			s.savePending(r.Status.Name, p)
		}
	}
	if !retry {
		return
	}
	state := core.StateUnknown
	if pane, e := s.opts.Sessions.Capture(r.Status.Name, 40, false); e == nil {
		if l := s.opts.Launchers[r.Status.Runtime.Harness]; l != nil && l.PromptReady(pane) {
			state = core.StateIdle
		}
	}
	s.setState(r, state, "")
}

// checkProblem runs each reconcile tick for a running agent. It retries a
// paused agent once its wait is over and flags a turn that has made no
// progress for a long time. It reports whether a problem is active, so the
// caller skips the generic stale-hook check.
func (s *Supervisor) checkProblem(r *record, now time.Time) bool {
	if p := r.Status.Problem; p != nil {
		if p.Kind == "stuck" {
			return false
		}
		if !p.Until.IsZero() && !now.Before(p.Until) {
			s.clearProblem(r, "trying again", true)
			return false
		}
		return true
	}
	if r.Status.State == core.StateBusy && !r.Status.LastActivity.IsZero() && now.Sub(r.Status.LastActivity) >= stuckAfter {
		s.setProblem(r, core.AgentProblem{
			Kind:  "stuck",
			Title: "Still on the same step after " + strconv.Itoa(int(stuckAfter.Minutes())) + " minutes",
			Hint:  "Long jobs can take this long. If it looks stuck, open its screen to see what it's doing, or choose Stop what it's doing in its Actions menu.",
			Since: now,
		})
	}
	return false
}

// screenLine picks the most telling line of a screen: the first one that
// contains one of the markers, else the first line with text. Borders are skipped.
func screenLine(pane string, markers []string) string {
	first, hit := "", ""
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimSpace(strings.TrimFunc(line, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("─━│┃╭╮╰╯┌┐└┘", r)
		}))
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		l := strings.ToLower(line)
		for _, m := range markers {
			if hit == "" && strings.Contains(l, m) {
				hit = line
			}
		}
	}
	out := hit
	if out == "" {
		out = first
	}
	if r := []rune(out); len(r) > 200 {
		out = string(r[:200]) + "…"
	}
	return out
}

// crashReason is what a dead pane last printed, to explain an exit better
// than "exit 1": the last line with text, which is usually the error.
func crashReason(pane string) string {
	lines := strings.Split(strings.TrimRight(pane, "\n "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(strings.TrimFunc(lines[i], func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("─━│┃╭╮╰╯┌┐└┘", r)
		}))
		if l == "" || strings.HasPrefix(l, "Pane is dead") {
			continue
		}
		if r := []rune(l); len(r) > 200 {
			l = string(r[:200]) + "…"
		}
		return l
	}
	return ""
}
