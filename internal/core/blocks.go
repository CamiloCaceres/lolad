package core

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Blocks are the pieces an agent lays a message out with, so the user gets
// real UI instead of a wall of text: a command with a copy button, values to
// copy one by one, a link to open, a checklist. They travel in
// UserChat.Blocks; the mail body always carries the same content as plain
// text (BlocksText) for email, other agents and older dashboards.
//
// Agents reach them through lolad's MCP tools (message_user, ask_user) or
// lolad msg tell-user --json. The list is small on purpose: each block has one
// job, and the tool descriptions say when to use it.
const (
	BlockText    = "text"    // Markdown
	BlockCommand = "command" // something the user runs: Code, Where, Admin
	BlockCode    = "code"    // text the user pastes somewhere (a file, a settings box): Code
	BlockValues  = "values"  // label/value rows, each copied on its own: Rows
	BlockLink    = "link"    // a page to open: Label, URL
	BlockSteps   = "steps"   // a checklist the user works through: Items
)

// BlockTypes lists the valid Block types.
var BlockTypes = []string{BlockText, BlockCommand, BlockCode, BlockValues, BlockLink, BlockSteps}

// Limits on what one message may lay out.
const (
	MaxBlocks    = 24
	MaxBlockRows = 30
	MaxBlockText = 20000 // bytes of text or code in one block
)

// Block is one piece of a message laid out for the user.
type Block struct {
	Type  string     `json:"type"`
	Title string     `json:"title,omitempty"` // command, code, values, steps: a short heading
	Text  string     `json:"text,omitempty"`  // text: Markdown
	Code  string     `json:"code,omitempty"`  // command, code
	Where string     `json:"where,omitempty"` // command: where to run it, as a phrase ("in Terminal on your Mac")
	Admin bool       `json:"admin,omitempty"` // command: asks for an administrator password (sudo)
	Rows  []ValueRow `json:"rows,omitempty"`  // values
	Label string     `json:"label,omitempty"` // link
	URL   string     `json:"url,omitempty"`   // link: http or https
	Items []string   `json:"items,omitempty"` // steps: Markdown, one per step
}

// ValueRow is one value the user copies, with what it is.
type ValueRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SafeLinkURL reports whether u is a link the dashboard opens: http or https
// with a host.
func SafeLinkURL(u string) bool {
	p, err := url.Parse(strings.TrimSpace(u))
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

// ValidateBlocks checks blocks the way the dashboard draws them, with
// messages an agent can act on.
func ValidateBlocks(blocks []Block) error {
	if len(blocks) > MaxBlocks {
		return fmt.Errorf("%w: at most %d blocks", ErrInvalid, MaxBlocks)
	}
	for i, b := range blocks {
		where := fmt.Sprintf("block %d (%s)", i+1, b.Type)
		long := func(s string) bool { return len(s) > MaxBlockText }
		switch b.Type {
		case BlockText:
			if strings.TrimSpace(b.Text) == "" {
				return fmt.Errorf("%w: %s needs text", ErrInvalid, where)
			}
			if long(b.Text) {
				return fmt.Errorf("%w: %s is too long", ErrInvalid, where)
			}
		case BlockCommand, BlockCode:
			if strings.TrimSpace(b.Code) == "" {
				return fmt.Errorf("%w: %s needs code", ErrInvalid, where)
			}
			if long(b.Code) {
				return fmt.Errorf("%w: %s is too long; attach a file instead", ErrInvalid, where)
			}
		case BlockValues:
			if len(b.Rows) == 0 {
				return fmt.Errorf("%w: %s needs rows", ErrInvalid, where)
			}
			if len(b.Rows) > MaxBlockRows {
				return fmt.Errorf("%w: %s has more than %d rows", ErrInvalid, where, MaxBlockRows)
			}
			for _, r := range b.Rows {
				if strings.TrimSpace(r.Label) == "" || r.Value == "" {
					return fmt.Errorf("%w: %s: every row needs a label and a value", ErrInvalid, where)
				}
				if long(r.Value) {
					return fmt.Errorf("%w: %s: a value is too long", ErrInvalid, where)
				}
			}
		case BlockLink:
			if !SafeLinkURL(b.URL) {
				return fmt.Errorf("%w: %s needs an http(s) url", ErrInvalid, where)
			}
		case BlockSteps:
			if len(b.Items) == 0 {
				return fmt.Errorf("%w: %s needs items", ErrInvalid, where)
			}
			if len(b.Items) > MaxBlockRows {
				return fmt.Errorf("%w: %s has more than %d items", ErrInvalid, where, MaxBlockRows)
			}
			if slices.ContainsFunc(b.Items, func(s string) bool { return strings.TrimSpace(s) == "" || long(s) }) {
				return fmt.Errorf("%w: %s: every item needs text", ErrInvalid, where)
			}
		default:
			return fmt.Errorf("%w: %s: unknown type (use %v)", ErrInvalid, where, BlockTypes)
		}
	}
	return nil
}

// BlocksText is blocks as plain text (Markdown), for the mail body.
func BlocksText(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		var s strings.Builder
		if b.Title != "" && b.Type != BlockLink {
			s.WriteString(b.Title + ":\n")
		}
		switch b.Type {
		case BlockText:
			s.WriteString(strings.TrimSpace(b.Text))
		case BlockCommand, BlockCode:
			if b.Type == BlockCommand {
				note := "Run"
				if b.Where != "" {
					note += " " + b.Where
				}
				if b.Admin {
					note += " (asks for an administrator password)"
				}
				s.WriteString(note + ":\n")
			}
			fence := "```"
			for strings.Contains(b.Code, fence) {
				fence += "`"
			}
			s.WriteString(fence + "\n" + strings.TrimRight(b.Code, "\n") + "\n" + fence)
		case BlockValues:
			for i, r := range b.Rows {
				if i > 0 {
					s.WriteString("\n")
				}
				s.WriteString(r.Label + ": " + r.Value)
			}
		case BlockLink:
			s.WriteString(cmpOr(b.Label, b.Title, "Link") + ": " + b.URL)
		case BlockSteps:
			for i, it := range b.Items {
				if i > 0 {
					s.WriteString("\n")
				}
				fmt.Fprintf(&s, "%d. %s", i+1, strings.TrimSpace(it))
			}
		}
		parts = append(parts, s.String())
	}
	return strings.Join(parts, "\n\n")
}

func cmpOr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
