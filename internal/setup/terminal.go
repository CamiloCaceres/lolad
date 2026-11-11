package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Actions are the terminal actions POST /api/setup/terminal accepts.
var Actions = []string{"install-claude", "login-claude", "install-codex", "login-codex", "login-codewhale"}

const sessionPrefix = "setup-"

type session struct {
	info    TerminalInfo
	started time.Time
	cancel  context.CancelFunc
}

func actionTitle(a string) string {
	switch a {
	case "install-claude":
		return "Installing Claude Code"
	case "login-claude":
		return "Signing in to Claude Code"
	case "install-codex":
		return "Installing Codex"
	case "login-codex":
		return "Signing in to Codex"
	case "login-codewhale":
		return "Signing in to Codewhale"
	}
	return a
}

// quoteSh single-quotes s for sh.
func quoteSh(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// quotePS single-quotes s for PowerShell.
func quotePS(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// command is the vendor's official installer or login, wrapped so the window
// says when it has finished.
func (m *Manager) command(action, binPath string) ([]string, error) {
	var body string
	switch action {
	case "install-claude":
		if m.goos == "windows" {
			body = "irm https://claude.ai/install.ps1 | iex"
		} else {
			body = "curl -fsSL https://claude.ai/install.sh | bash"
		}
	case "install-codex":
		body = "npm install -g @openai/codex"
	case "login-claude", "login-codex", "login-codewhale":
		if binPath == "" {
			return nil, invalid("%s is not installed yet; install it first", strings.TrimPrefix(action, "login-"))
		}
		sub := "login"
		if action == "login-claude" {
			sub = "auth login"
		}
		if m.goos == "windows" {
			body = "& " + quotePS(binPath) + " " + sub
		} else {
			body = quoteSh(binPath) + " " + sub
		}
	default:
		return nil, invalid("unknown action %q", action)
	}
	if m.goos == "windows" {
		return []string{"powershell.exe", "-NoLogo", "-NoProfile", "-Command",
			body + "; Write-Host ''; Write-Host 'Finished. You can close this panel and press Re-scan.'"}, nil
	}
	return []string{"/bin/sh", "-c", body + "; rc=$?; echo; echo \"Finished (exit $rc). You can close this panel and press Re-scan.\"; exit $rc"}, nil
}

// StartTerminal starts action in a lolad-managed session named
// setup-<action>. A session still running is reused; a finished one is
// replaced. It is killed when it ends (after a short grace so its output can
// be read) or after MaxAge, whichever is first.
func (m *Manager) StartTerminal(ctx context.Context, action string) (TerminalInfo, error) {
	if !slices.Contains(Actions, action) {
		return TerminalInfo{}, invalid("unknown action %q", action)
	}
	if m.opts.Sessions == nil {
		return TerminalInfo{}, fmt.Errorf("%w: no terminal sessions in this build", ErrUnavailable)
	}
	if _, err := m.Status(ctx); err != nil { // makes sure a scan has run
		return TerminalInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bin := ""
	id := strings.TrimPrefix(strings.TrimPrefix(action, "login-"), "install-")
	for _, h := range m.harness {
		if h.ID == id {
			bin = h.Path
		}
	}
	argv, err := m.command(action, bin)
	if err != nil {
		return TerminalInfo{}, err
	}
	name := sessionPrefix + action
	info := TerminalInfo{Session: name, TerminalURL: "/welcome?terminal=" + name, Action: action, Title: actionTitle(action)}
	if m.opts.Sessions.Exists(name) {
		if dead, _, err := m.opts.Sessions.PaneDead(name); err == nil && !dead {
			if _, ok := m.sessions[name]; ok {
				return info, nil
			}
		}
		_ = m.opts.Sessions.Kill(name)
	}
	if old, ok := m.sessions[name]; ok {
		old.cancel()
		delete(m.sessions, name)
	}
	path := strings.Join(uniq(append(filepath.SplitList(m.captured), m.searchDirs(m.captured, "")...)), string(filepath.ListSeparator))
	if err := m.opts.Sessions.Start(name, m.home, m.minimalEnv(path), argv, 100, 30); err != nil {
		return TerminalInfo{}, fmt.Errorf("start %s: %w", action, err)
	}
	wctx, cancel := context.WithCancel(context.Background())
	m.sessions[name] = &session{info: info, started: m.now(), cancel: cancel}
	go m.watch(wctx, name)
	return info, nil
}

// watch rescans when the session's command ends, then removes the session.
func (m *Manager) watch(ctx context.Context, name string) {
	poll, grace, maxAge := m.opts.Poll, m.opts.Grace, m.opts.MaxAge
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if grace <= 0 {
		grace = 30 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	deadline := time.NewTimer(maxAge)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	finish := func() {
		m.mu.Lock()
		if s, ok := m.sessions[name]; ok {
			s.cancel()
			delete(m.sessions, name)
		}
		m.mu.Unlock()
		_ = m.opts.Sessions.Kill(name)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			finish()
			return
		case <-tick.C:
			dead, _, err := m.opts.Sessions.PaneDead(name)
			if err != nil || !m.opts.Sessions.Exists(name) {
				finish()
				return
			}
			if !dead {
				continue
			}
			// The command ended: look again for what it installed or signed
			// in, and leave the output up for a while.
			m.mu.Lock()
			_ = m.rescanLocked(context.Background())
			m.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(grace):
				finish()
			case <-deadline.C:
				finish()
			}
			return
		}
	}
}

func (m *Manager) liveTerminalsLocked() []TerminalInfo {
	var out []TerminalInfo
	for _, s := range m.sessions {
		out = append(out, s.info)
	}
	slices.SortFunc(out, func(a, b TerminalInfo) int { return strings.Compare(a.Session, b.Session) })
	return out
}

// Attach opens a session StartTerminal made, and nothing else.
func (m *Manager) Attach(ctx context.Context, session string, control bool) (core.Terminal, error) {
	if m.opts.Sessions == nil {
		return nil, ErrUnavailable
	}
	action := strings.TrimPrefix(session, sessionPrefix)
	if !strings.HasPrefix(session, sessionPrefix) || !slices.Contains(Actions, action) {
		return nil, fmt.Errorf("%w: no such setup session", core.ErrNotFound)
	}
	if !m.opts.Sessions.Exists(session) {
		return nil, fmt.Errorf("%w: that setup session has ended", core.ErrNotRunning)
	}
	return m.opts.Sessions.Attach(ctx, session, !control)
}

// ActionTitle is a short sentence for a terminal action, for panel headings.
func ActionTitle(action string) string { return actionTitle(action) }

// ValidSession reports whether name is a session StartTerminal could have made.
func ValidSession(name string) bool {
	return strings.HasPrefix(name, sessionPrefix) && slices.Contains(Actions, strings.TrimPrefix(name, sessionPrefix))
}
