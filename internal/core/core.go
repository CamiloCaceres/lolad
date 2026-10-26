// Package core holds the types every other package shares, and the interfaces
// the packages are wired together with. It imports nothing from this module, so
// every package can depend on it and be built and tested on its own.
package core

import (
	"context"
	"errors"
	"io"
	"time"
)

// ---------------------------------------------------------------- agents

// Harness is the coding agent that runs a lolabot instance.
type Harness string

const (
	HarnessClaude    Harness = "claude"
	HarnessCodewhale Harness = "codewhale"
)

// Permissions is how much the harness asks before acting. Unattended agents run
// on bypass: an approval prompt nobody answers stalls the agent forever.
type Permissions string

const (
	PermBypass  Permissions = "bypass"
	PermDefault Permissions = "default"
)

// Runtime is the `runtime:` block of an agent's lolabot.yaml. It is the only
// thing lolad adds to lolabot; the agent stays self-describing.
type Runtime struct {
	Slug          string            `yaml:"slug,omitempty" json:"slug,omitempty"` // optional; default = agent.name sanitised, else dir basename
	Harness       Harness           `yaml:"harness" json:"harness"`
	Model         string            `yaml:"model,omitempty" json:"model,omitempty"`
	Permissions   Permissions       `yaml:"permissions,omitempty" json:"permissions,omitempty"`         // default bypass
	Autostart     *bool             `yaml:"autostart,omitempty" json:"autostart,omitempty"`             // nil = true
	WakeOnMessage *bool             `yaml:"wake_on_message,omitempty" json:"wake_on_message,omitempty"` // nil = true: mail to a stopped agent starts it
	OnWake        string            `yaml:"on_wake,omitempty" json:"on_wake,omitempty"`                 // first prompt of a fresh session
	Rotate        string            `yaml:"rotate,omitempty" json:"rotate,omitempty"`                   // "HH:MM" local; fresh session daily. "" = never
	EnvFile       string            `yaml:"env_file,omitempty" json:"env_file,omitempty"`               // relative to the agent dir; default ".env" if present
	Env           map[string]string `yaml:"env,omitempty" json:"env,omitempty"`                         // ${LOLABOT_HOME} is expanded
	Args          []string          `yaml:"args,omitempty" json:"args,omitempty"`                       // extra harness argv
	Skills        SkillsMode        `yaml:"skills,omitempty" json:"skills,omitempty"`                   // "" = inherit
	Triggers      []Trigger         `yaml:"triggers,omitempty" json:"triggers,omitempty"`
}

func (r Runtime) AutostartEnabled() bool     { return r.Autostart == nil || *r.Autostart }
func (r Runtime) WakeOnMessageEnabled() bool { return r.WakeOnMessage == nil || *r.WakeOnMessage }

// SkillsMode decides which skills (and user-level plugins) an agent sees.
type SkillsMode string

const (
	// SkillsInherit: the agent sees everything in the user's own Claude Code /
	// Codewhale space (~/.claude/skills, user plugins) plus its own.
	SkillsInherit SkillsMode = "inherit"
	// SkillsBarebones: only the agent's own skills (inside its dir); whoever
	// manages the agent adds skills to it explicitly.
	SkillsBarebones SkillsMode = "barebones"
)

func (r Runtime) SkillsMode() SkillsMode {
	if r.Skills == SkillsBarebones {
		return SkillsBarebones
	}
	return SkillsInherit
}

// Trigger turns something outside the agent into mail from the system
// identity. Exactly one of Cron, Mail, Webhook is set.
type Trigger struct {
	Cron    string `yaml:"cron,omitempty" json:"cron,omitempty"`       // 5-field cron, local time
	Mail    string `yaml:"mail,omitempty" json:"mail,omitempty"`       // account for `tools/email.sh watch <account>`
	Webhook string `yaml:"webhook,omitempty" json:"webhook,omitempty"` // name; POST /hook/<agent>/<name> with the agent's webhook token
	Every   string `yaml:"every,omitempty" json:"every,omitempty"`     // mail poll interval, Go duration; default 2m
	Prompt  string `yaml:"prompt,omitempty" json:"prompt,omitempty"`   // cron: the message text; webhook/mail: a prefix
}

func (t Trigger) Kind() string {
	switch {
	case t.Cron != "":
		return "cron"
	case t.Mail != "":
		return "mail"
	case t.Webhook != "":
		return "webhook"
	}
	return ""
}

// Agent is one registered lolabot instance.
type Agent struct {
	Name    string  `json:"name"`    // slug: tmux session, AMP local name, URL segment, CLI argument. ^[a-z0-9][a-z0-9-]{0,62}$
	Display string  `json:"display"` // agent.name from lolabot.yaml, e.g. "Ultron"
	Role    string  `json:"role"`    // agent.role
	Dir     string  `json:"dir"`     // absolute instance dir
	Runtime Runtime `json:"runtime"`
}

// State is what an agent is doing, as far as lolad can tell.
type State string

const (
	StateStopped  State = "stopped"  // not running, and not supposed to be
	StateStarting State = "starting" // launched, harness has not reported in yet
	StateIdle     State = "idle"     // waiting for input
	StateBusy     State = "busy"     // working on a turn
	StateWaiting  State = "waiting"  // blocked on a permission prompt or question in the TUI
	StateCrashed  State = "crashed"  // exited while desired up; restart pending (backoff)
	StateFailed   State = "failed"   // crashed too often; supervisor gave up until someone acts
	StateUnknown  State = "unknown"  // running, no signal either way
)

// AgentStatus is an Agent plus everything the dashboard shows about it.
type AgentStatus struct {
	Agent
	Address      string    `json:"address"` // AMP address
	State        State     `json:"state"`
	Desired      string    `json:"desired"` // "up" | "down"
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at,omitzero"`    // current process
	SessionSince time.Time `json:"session_since,omitzero"` // current conversation (reset by fresh/rotate)
	LastActivity time.Time `json:"last_activity,omitzero"`
	Restarts     int       `json:"restarts"` // crash restarts in the last 24h
	NextRestart  time.Time `json:"next_restart,omitzero"`
	Unread       int       `json:"unread"` // mail addressed to it, not yet read by it
	LastError    string    `json:"last_error,omitempty"`
	Detail       string    `json:"detail,omitempty"` // from the last hook, e.g. the tool waiting for approval
	Team         string    `json:"team,omitempty"`   // team id it belongs to, if any
	IsHead       bool      `json:"is_head,omitempty"`
	// PendingRestart: the agent's configuration (runtime block, .env, skills,
	// email) changed since its process started; a restart applies it.
	PendingRestart bool   `json:"pending_restart,omitempty"`
	PendingReason  string `json:"pending_reason,omitempty"` // e.g. "model changed, .env changed"
}

// Event is one line in an agent's history: lifecycle, state change, trigger, delivery.
type Event struct {
	ID     int64     `json:"id"`
	Agent  string    `json:"agent"` // "" for system-wide events
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // started exited crashed restarted state rotated mail notified trigger created policy error
	Detail string    `json:"detail"`
}

// ---------------------------------------------------------------- mail (AMP)

// MailStatus is where a message is from the recipient's point of view.
type MailStatus string

const (
	MailUnread   MailStatus = "unread"   // in the mailbox, recipient has not been told
	MailNotified MailStatus = "notified" // surfaced into the recipient's session (or pushed to the user)
	MailRead     MailStatus = "read"     // recipient fetched/opened it (AMP ack)
)

// Mail is one AMP message as stored by lolad's provider. From/To are full AMP
// addresses; FromName/ToName are local names (agent slugs, the user's name, or
// "lolad" for the system identity).
type Mail struct {
	ID         string         `json:"id"` // msg_<unix>_<hex>
	ThreadID   string         `json:"thread_id"`
	InReplyTo  string         `json:"in_reply_to,omitempty"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	FromName   string         `json:"from_name"`
	ToName     string         `json:"to_name"`
	Subject    string         `json:"subject"`
	Priority   string         `json:"priority"` // urgent|high|normal|low
	Type       string         `json:"type"`     // payload.type: request|response|notification|alert|task|status|handoff|ack|update|system
	Body       string         `json:"body"`     // payload.message
	Context    map[string]any `json:"context,omitempty"`
	Signed     bool           `json:"signed"`
	Verified   bool           `json:"verified"`
	CreatedAt  time.Time      `json:"created_at"`
	Status     MailStatus     `json:"status"`
	NotifiedAt time.Time      `json:"notified_at,omitzero"`
	ReadAt     time.Time      `json:"read_at,omitzero"`
	Attempts   int            `json:"attempts"` // notification attempts
}

// UserChat is the structured part of mail between the user and an agent,
// carried at payload.context.userchat (Maestro Lite's convention).
//
//	agent → user kinds: say | update | deliver | ask (Options = buttons)
//	user → agent kinds: say | reply | decision (Decision = chosen option)
type UserChat struct {
	Kind     string   `json:"kind"`
	Options  []string `json:"options,omitempty"`
	Decision string   `json:"decision,omitempty"`
}

// Outgoing is what a sender supplies; the provider fills id, from, timestamps.
type Outgoing struct {
	To        string         `json:"to"` // local name or full address
	Subject   string         `json:"subject"`
	Body      string         `json:"body"`
	Priority  string         `json:"priority,omitempty"`
	Type      string         `json:"type,omitempty"`
	InReplyTo string         `json:"in_reply_to,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// MailFilter selects messages from one participant's mailbox.
type MailFilter struct {
	Box    string     // "inbox" (default) | "sent" | "all"
	Peer   string     // only mail exchanged with this local name
	Status MailStatus // "" = any
	Limit  int        // 0 = 100
	Before time.Time  // paging
}

// Conversation is the user's thread with one participant.
type Conversation struct {
	Peer    string    `json:"peer"` // local name
	Display string    `json:"display"`
	Last    Mail      `json:"last"`
	Unread  int       `json:"unread"`  // from peer to user, unread
	Pending bool      `json:"pending"` // last message is an unanswered ask
	LastAt  time.Time `json:"last_at"`
}

// Identity is an AMP participant registered with lolad.
type Identity struct {
	Name        string    `json:"name"`    // local name
	Address     string    `json:"address"` // name@tenant.lolad.local
	Kind        string    `json:"kind"`    // "agent" | "human" | "system"
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

// Mailer is lolad's AMP provider seen from inside the process. Every message —
// from the dashboard, from an agent through /v1/route, from a trigger — goes
// through Send, which is where the policy is enforced.
type Mailer interface {
	EnsureIdentity(ctx context.Context, name, kind string) (Identity, error)
	Identities(ctx context.Context) ([]Identity, error)
	Send(ctx context.Context, fromName string, out Outgoing) (Mail, error) // ErrDenied when policy forbids
	Get(ctx context.Context, id string) (Mail, error)
	List(ctx context.Context, owner string, f MailFilter) ([]Mail, error)
	Traffic(ctx context.Context, limit int, before time.Time) ([]Mail, error) // every message, newest first
	MarkNotified(ctx context.Context, id string) error
	MarkRead(ctx context.Context, owner, id string) error
	Unread(ctx context.Context, owner string) ([]Mail, error)
	OnMail(fn func(Mail)) // called after each successful Send, in a goroutine
}

// ---------------------------------------------------------------- org and policy

// Team is one box on the org chart. Teams nest through Parent, which is how a
// company gets departments. The head speaks for the team upward.
type Team struct {
	ID           string   `yaml:"id" json:"id"` // slug
	Name         string   `yaml:"name" json:"name"`
	Parent       string   `yaml:"parent,omitempty" json:"parent,omitempty"`
	Head         string   `yaml:"head,omitempty" json:"head,omitempty"` // agent slug
	Members      []string `yaml:"members,omitempty" json:"members,omitempty"`
	Open         bool     `yaml:"open" json:"open"` // members may message each other freely
	Instructions string   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// Rule is an explicit allow/deny edge. From/To accept a local name, "user",
// "team:<id>" (any member or head of that team, including sub-teams), or "*".
// Rules are checked in order before the defaults; first match wins.
type Rule struct {
	From  string `yaml:"from" json:"from"`
	To    string `yaml:"to" json:"to"`
	Allow bool   `yaml:"allow" json:"allow"`
	Both  bool   `yaml:"both,omitempty" json:"both,omitempty"` // also the reverse direction
	Note  string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Defaults decide everything no rule matched.
type Defaults struct {
	UserToAgents string `yaml:"user_to_agents" json:"user_to_agents"` // "all" (default) | "heads" | "none"
	AgentsToUser string `yaml:"agents_to_user" json:"agents_to_user"` // "all" (default) | "heads" | "none"
	Vertical     bool   `yaml:"vertical" json:"vertical"`             // default true: a head ↔ its members; a head ↔ the head of the parent team and of child teams
	Independents string `yaml:"independents" json:"independents"`     // agents in no team: "user-only" (default) | "all"
}

// Org is ~/.config/lolad/org.yaml.
type Org struct {
	Tenant   string   `yaml:"tenant" json:"tenant"` // AMP tenant; addresses are <name>@<tenant>.lolad.local
	User     User     `yaml:"user" json:"user"`
	Teams    []Team   `yaml:"teams" json:"teams"`
	Rules    []Rule   `yaml:"rules" json:"rules"`
	Defaults Defaults `yaml:"defaults" json:"defaults"`
}

// User is the human operator's identity.
type User struct {
	Name    string `yaml:"name" json:"name"`                     // local name, e.g. "milo"
	Display string `yaml:"display" json:"display"`               // "Camilo Caceres"
	Ntfy    string `yaml:"ntfy,omitempty" json:"ntfy,omitempty"` // optional ntfy topic URL for phone pushes
}

// Decision is the policy's answer with the reason, shown in the UI and in the
// 403 an agent gets.
type Decision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"` // e.g. `rule 2 (lola→jugui)`, `vertical: head of lumio → member`, `default: independents user-only`
}

// Policy answers who may message whom. System identity "lolad" may always send.
type Policy interface {
	Check(from, to string) Decision
	Contacts(from string) []string // every local name `from` may message, sorted
}

// ---------------------------------------------------------------- the service

// CreateRequest makes a new lolabot instance and registers it.
type CreateRequest struct {
	Name        string      `json:"name"`        // display name, e.g. "Ultron"; slug derived unless Slug set
	Slug        string      `json:"slug"`        // optional
	Role        string      `json:"role"`        // default "Personal Assistant"
	User        string      `json:"user"`        // operator's full name; default org.user.display
	Dir         string      `json:"dir"`         // optional; default <config agents_dir>/<slug>
	Harness     Harness     `json:"harness"`     // required
	Model       string      `json:"model"`       // optional
	Permissions Permissions `json:"permissions"` // default bypass
	OnWake      string      `json:"on_wake"`     // optional; sensible default
	Mission     string      `json:"mission"`     // optional: what this agent is for; sent as its first mail from the user
	Team        string      `json:"team"`        // optional: team id to join
	Head        bool        `json:"head"`        // make it the team's head
	Start       bool        `json:"start"`       // start it right away
}

// AdoptRequest registers an existing lolabot dir (adds the runtime block).
type AdoptRequest struct {
	Dir     string  `json:"dir"`
	Slug    string  `json:"slug"` // optional; default agent.name from lolabot.yaml sanitised, else dir basename
	Runtime Runtime `json:"runtime"`
}

// Terminal is a live attachment to an agent's tmux session. Read yields raw
// terminal output (ANSI included); Write sends keystrokes.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}

// Service is what lolad does, independent of how it is reached. The JSON API,
// the dashboard and the CLI (through the API) all go through it.
type Service interface {
	// fleet
	List(ctx context.Context) ([]AgentStatus, error)
	Get(ctx context.Context, name string) (AgentStatus, error)
	Start(ctx context.Context, name string) error                        // desired=up
	Stop(ctx context.Context, name string) error                         // desired=down; graceful, then kill
	Restart(ctx context.Context, name string, fresh bool) error          // fresh=true: new conversation + on_wake
	Interrupt(ctx context.Context, name string) error                    // Escape into the session
	Events(ctx context.Context, name string, limit int) ([]Event, error) // name "" = all
	Subscribe(ctx context.Context) <-chan Event                          // live; closed when ctx ends
	Create(ctx context.Context, req CreateRequest) (AgentStatus, error)
	Adopt(ctx context.Context, req AdoptRequest) (AgentStatus, error)
	Reload(ctx context.Context) error
	Attach(ctx context.Context, name string, readOnly bool) (Terminal, error)
	Capture(ctx context.Context, name string, lines int) (string, error)

	// the user's mail
	Me() User
	Conversations(ctx context.Context) ([]Conversation, error)
	Conversation(ctx context.Context, peer string, limit int, before time.Time) ([]Mail, error) // oldest first
	UserSend(ctx context.Context, out Outgoing) (Mail, error)
	MarkRead(ctx context.Context, id string) error // as the user
	Traffic(ctx context.Context, limit int, before time.Time) ([]Mail, error)

	// org
	Org(ctx context.Context) (Org, error)
	SaveOrg(ctx context.Context, org Org) error // validated, written to org.yaml, policy reloaded
	Check(ctx context.Context, from, to string) Decision

	// per-agent settings (see settings.go)
	UpdateRuntime(ctx context.Context, name string, rt Runtime) (AgentStatus, error) // replaces the runtime block; slug and harness are immutable here
	Models(harness Harness) []ModelOption
	Skills(ctx context.Context, name string) ([]Skill, error)
	AddSkill(ctx context.Context, name, from string) (Skill, error) // from = a library skill's ID (Skill.ID with Source "user"/"plugin")
	RemoveSkill(ctx context.Context, name, skillID string) error    // only Source "agent"
	Env(ctx context.Context, name string) ([]EnvVar, error)
	SetEnv(ctx context.Context, name, key, value string) error
	DeleteEnv(ctx context.Context, name, key string) error
	Files(ctx context.Context, name string) ([]FileInfo, error)
	ReadFile(ctx context.Context, name, path string) (FileContent, error)
	WriteFile(ctx context.Context, name, path, content, baseHash string) (FileContent, error) // ErrConflict when baseHash is stale
	Email(ctx context.Context, name string) (EmailSettings, error)
	SaveEmail(ctx context.Context, name string, s EmailSettings) (EmailSettings, error)
	TestEmail(ctx context.Context, name, address string) (EmailTest, error)
}

var (
	ErrNotFound   = errors.New("not found")
	ErrExists     = errors.New("already exists")
	ErrNotRunning = errors.New("agent is not running")
	ErrInvalid    = errors.New("invalid request")
	ErrDenied     = errors.New("denied by policy")
	ErrConflict   = errors.New("changed since it was loaded")
)
