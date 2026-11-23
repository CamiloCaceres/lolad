package hub

import (
	"context"
	"net/netip"
	"strings"
	"time"
)

// Open sign-up (docs/friend-connect-design.md §1): a lolad can join with no
// code, proving possession of its provider key, behind limits on where
// sign-ups come from and how many happen.

// Sign-up modes. "closed" is reported by /v1/hub/info when the mode is open
// but a hub-wide cap is reached.
const (
	SignupOpen   = "open"
	SignupInvite = "invite"
	SignupClosed = "closed"
)

// Limits bound open sign-up and per-member traffic. A zero value turns that
// limit off. DefaultLimits are the launch defaults.
type Limits struct {
	SignupsPerIP  int           // successful open sign-ups per source IP per 24 h
	SignupsPerNet int           // per IPv4 /24 or IPv6 /56 per 24 h
	SignupsHourly int           // open sign-ups hub-wide per hour
	SignupsDaily  int           // open sign-ups hub-wide per 24 h
	MaxAccounts   int           // accounts in total; over it, open sign-up is closed
	ReapUnused    time.Duration // open sign-ups that never connect are removed after this
	BytesPerDay   int64         // bytes carried per handle per 24 h (0 = no cap)
}

// DefaultLimits are used when Options.Limits is nil.
func DefaultLimits() Limits {
	return Limits{SignupsPerIP: 3, SignupsPerNet: 10, SignupsHourly: 60, SignupsDaily: 300, MaxAccounts: 2000, ReapUnused: 72 * time.Hour}
}

const settingSignup = "signup_mode"

// SignupMode is the configured mode: the console's setting if the operator
// changed it, else Options.Signup.
func (s *Server) SignupMode(ctx context.Context) string {
	if v, e := s.store.Setting(ctx, settingSignup); e == nil && (v == SignupOpen || v == SignupInvite) {
		return v
	}
	return s.o.Signup
}

// SetSignupMode switches open sign-up on or off; it outlives restarts.
func (s *Server) SetSignupMode(ctx context.Context, mode string) error {
	if mode != SignupOpen && mode != SignupInvite {
		return ErrNotFound
	}
	return s.store.SetSetting(ctx, settingSignup, mode)
}

// signupState is the live mode for /v1/hub/info: closed when open but full.
func (s *Server) signupState(ctx context.Context) string {
	mode := s.SignupMode(ctx)
	if mode != SignupOpen {
		return mode
	}
	if reason, _ := s.capReached(ctx); reason != "" {
		return SignupClosed
	}
	return mode
}

// capReached reports a hub-wide cap that refuses open sign-ups now, with how
// long to wait.
func (s *Server) capReached(ctx context.Context) (string, time.Duration) {
	l := s.lim
	now := s.o.Clock()
	total, hour, day, err := s.store.signupCounts(ctx, now.Add(-time.Hour), now.Add(-24*time.Hour))
	switch {
	case err != nil:
		return "unavailable", time.Minute
	case l.MaxAccounts > 0 && total >= l.MaxAccounts:
		return "full", 24 * time.Hour
	case l.SignupsDaily > 0 && day >= l.SignupsDaily:
		return "daily", time.Hour
	case l.SignupsHourly > 0 && hour >= l.SignupsHourly:
		return "hourly", 10 * time.Minute
	}
	return "", 0
}

// netKey groups an IP with its neighbours: /24 for IPv4, /56 for IPv6.
func netKey(ip string) string {
	a, e := netip.ParseAddr(ip)
	if e != nil {
		return ip
	}
	a = a.Unmap()
	bits := 56
	if a.Is4() {
		bits = 24
	}
	p, _ := a.Prefix(bits)
	return p.String()
}

// signupAllowed checks the per-source limits without counting.
func (s *Server) signupAllowed(ip string) bool {
	l := s.lim
	return (l.SignupsPerIP <= 0 || s.limits.peek("su:ip:"+ip, l.SignupsPerIP, 24*time.Hour)) &&
		(l.SignupsPerNet <= 0 || s.limits.peek("su:net:"+netKey(ip), l.SignupsPerNet, 24*time.Hour))
}

func (s *Server) signupCounted(ip string) {
	s.limits.hit("su:ip:"+ip, 24*time.Hour)
	s.limits.hit("su:net:"+netKey(ip), 24*time.Hour)
}

// suggestHandle turns a refused handle into one that may be registered.
func (s *Server) suggestHandle(ctx context.Context, h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	base := strings.Trim(b.String(), "-")
	if len(base) > 28 {
		base = strings.TrimRight(base[:28], "-")
	}
	for len(base) < 3 && base != "" {
		base += "0"
	}
	if base == "" {
		return ""
	}
	if ValidHandle(base) {
		if _, e := s.store.Account(ctx, base); e == ErrNotFound {
			return base
		}
	}
	return s.store.suggest(ctx, base)
}
