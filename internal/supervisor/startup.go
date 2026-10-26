package supervisor

import (
	"strings"
	"time"
	"unicode"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Optional capability: existing Launcher implementations need no core changes.
type startupDialogs interface {
	StartupDialog(pane string) (name string, keys []string)
}

func (s *Supervisor) checkStartup(r *record, now time.Time) error {
	if r.StartupAt.IsZero() {
		r.StartupAt = now
	}
	pane, err := s.opts.Sessions.Capture(r.Status.Name, 50, false)
	if err != nil {
		return err
	}
	launcher := s.opts.Launchers[r.Status.Runtime.Harness]
	if launcher == nil {
		return nil
	}
	if launcher.PromptReady(pane) {
		s.setState(r, core.StateIdle, "")
		return nil
	}
	// Stop automatically sending keys after two minutes, even for a recognized
	// dialog. One acceptance per dialog per process avoids repeated blind Enter.
	if now.Sub(r.StartupAt) < 2*time.Minute {
		if dialogs, ok := launcher.(startupDialogs); ok {
			name, keys := dialogs.StartupDialog(pane)
			if name != "" && len(keys) > 0 && !r.StartupDialogs[name] {
				if err = s.opts.Sessions.SendKeys(r.Status.Name, keys...); err != nil {
					return err
				}
				if r.StartupDialogs == nil {
					r.StartupDialogs = map[string]bool{}
				}
				r.StartupDialogs[name] = true
				s.event(r.Status.Name, "dialog", "accepted "+name)
			}
		}
		return nil
	}
	first := "(empty pane)"
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimSpace(line)
		// Skip TUI borders so provider/authentication headings are useful details.
		if line == "" || strings.TrimFunc(line, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("─━│┃╭╮╰╯┌┐└┘", r)
		}) == "" {
			continue
		}
		first = line
		break
	}
	runes := []rune(first)
	if len(runes) > 240 {
		first = string(runes[:240]) + "…"
	}
	detail := "startup blocked: " + first
	s.setState(r, core.StateWaiting, detail)
	s.event(r.Status.Name, "dialog", detail)
	return nil
}
