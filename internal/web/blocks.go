package web

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Helpers for the blocks partial (templates/partials/blocks.html).

var sudoRE = regexp.MustCompile(`(^|[;&|(\s])sudo\s`)

// adminCommand reports whether a command asks for the computer's password:
// the agent said so, or it runs sudo.
func adminCommand(b core.Block) bool {
	return b.Type == core.BlockCommand && (b.Admin || sudoRE.MatchString(b.Code))
}

// linkHost is the site a link opens, for the user to check before clicking.
func linkHost(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}

// valuesText is a values block as lines to paste at once.
func valuesText(rows []core.ValueRow) string {
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = r.Label + ": " + r.Value
	}
	return strings.Join(lines, "\n")
}
