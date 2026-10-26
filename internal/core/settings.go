package core

import "time"

// Per-agent settings managed from the dashboard: model, skills, .env secrets,
// config files and email. All of it lives in the agent's own directory, so
// the agent stays self-describing and portable.

// ModelOption is one choice in the model picker. Free text is also accepted.
type ModelOption struct {
	ID    string `json:"id"`    // what goes into runtime.model, e.g. "claude-opus-5-5[1m]"
	Label string `json:"label"` // "Opus 5.5 (1M context)"
	Note  string `json:"note,omitempty"`
}

// Skill is one skill an agent can see, or could be given.
type Skill struct {
	ID          string `json:"id"`   // stable within the listing: "<source>:<name>", e.g. "user:herdr", "agent:mail-handler"
	Name        string `json:"name"` // from SKILL.md frontmatter, else the dir name
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`              // "agent" (in the agent dir) | "user" (~/.claude/skills or harness equivalent) | "plugin:<plugin>"
	Path        string `json:"path"`                // absolute path of the skill dir
	FilePath    string `json:"file_path,omitempty"` // relative to the agent dir when Source=="agent" (for the file editor)
	Active      bool   `json:"active"`              // the agent sees it under its current skills mode
}

// EnvVar is one entry of the agent's env file. Values never leave the daemon:
// the dashboard can set, replace or delete a secret but never read it back.
type EnvVar struct {
	Key     string   `json:"key"`
	Set     bool     `json:"set"`               // non-empty value present
	Length  int      `json:"length,omitempty"`  // characters, so "is this the right token?" has a hint
	Comment string   `json:"comment,omitempty"` // the comment line directly above it, if any
	UsedBy  []string `json:"used_by,omitempty"` // e.g. "email: ultron@mail.example.com"
}

// FileInfo is an editable text file inside the agent dir.
type FileInfo struct {
	Path    string    `json:"path"` // relative, slash-separated
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Kind    string    `json:"kind"`           // md | yaml | toml | json | txt
	Group   string    `json:"group"`          // "instructions" (CLAUDE.md, AGENTS.md) | "config" (lolabot.yaml, harness config) | "brain" | "memory" | "skills" | "other"
	Note    string    `json:"note,omitempty"` // e.g. "the lolad section is regenerated; edit outside its markers"
}

// FileContent is a file plus the hash a later write must present.
type FileContent struct {
	FileInfo
	Content string `json:"content"`
	Hash    string `json:"hash"` // sha256 hex of the content as read
}

// EmailAccount is one mailbox in lolabot's email setup: the address and
// servers in lolabot.yaml (email.accounts), the login in
// brain/credentials.yaml (email_accounts.<config_key>), and the password in
// the agent's env file under PasswordKey (credentials' password_secret).
type EmailAccount struct {
	Address     string `json:"address"`
	ConfigKey   string `json:"config_key"` // default: local part of the address
	IMAPServer  string `json:"imap_server"`
	IMAPPort    int    `json:"imap_port"` // default 993
	SMTPServer  string `json:"smtp_server"`
	SMTPPort    int    `json:"smtp_port"`          // default 465
	Username    string `json:"username"`           // default: the address
	PasswordKey string `json:"password_key"`       // env var name, default EMAIL_PASSWORD_<CONFIG_KEY>
	PasswordSet bool   `json:"password_set"`       // read-only in responses
	Password    string `json:"password,omitempty"` // write-only: set on save to replace; never returned
}

// EmailSettings is the agent's whole email setup.
type EmailSettings struct {
	Supported      bool           `json:"supported"` // the agent has lolabot's tools/email.sh
	Accounts       []EmailAccount `json:"accounts"`
	OperatorEmails []string       `json:"operator_emails"` // senders whose authenticated mail is trusted as the operator
	// Watch is how new mail reaches the agent: "none", "lolad" (a lolad mail
	// trigger: works for both harnesses), or "mod" (the agent's own Claude Code
	// mod, e.g. ultron-mail; read-only, detected). Only one should be active.
	Watch        string   `json:"watch"`
	WatchAccount string   `json:"watch_account,omitempty"`
	Every        string   `json:"every,omitempty"` // lolad watch interval, default 2m
	Notes        []string `json:"notes,omitempty"` // warnings, e.g. "email.sh uses aim-secret; lolad will update it to read .env"
}

// EmailTest is the result of logging in to an account's IMAP and SMTP servers.
type EmailTest struct {
	OK     bool   `json:"ok"`
	IMAP   string `json:"imap"` // "ok" or the error
	SMTP   string `json:"smtp"`
	Detail string `json:"detail,omitempty"`
}
