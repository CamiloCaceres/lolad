// Package core holds the types every other package shares, and the Service
// interface the dashboard and the HTTP API are written against. It imports
// nothing from this module, so every package can depend on it.
package core

import (
	"context"
	"errors"
	"io"
	"time"
)

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
	Harness     Harness           `yaml:"harness" json:"harness"`
	Model       string            `yaml:"model,omitempty" json:"model,omitempty"`
	Permissions Permissions       `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Autostart   *bool             `yaml:"autostart,omitempty" json:"autostart,omitempty"` // nil = true
	OnWake      string            `yaml:"on_wake,omitempty" json:"on_wake,omitempty"`     // first prompt of a fresh session
	Rotate      string            `yaml:"rotate,omitempty" json:"rotate,omitempty"`       // "HH:MM" local; fresh session daily. "" = never
	EnvFile     string            `yaml:"env_file,omitempty" json:"env_file,omitempty"`   // relative to the agent dir; default ".env" if present
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`             // relative paths are NOT expanded; use ${LOLABOT_HOME}
	Args        []string          `yaml:"args,omitempty" json:"args,omitempty"`           // extra harness argv
	Triggers    []Trigger         `yaml:"triggers,omitempty" json:"triggers,omitempty"`
}

// AutostartEnabled reports whether lolad should keep the agent up without being asked.
func (r Runtime) AutostartEnabled() bool { return r.Autostart == nil || *r.Autostart }

// Trigger turns something outside the agent into a queued message. Exactly one
// of Cron, Mail, Webhook is set.
type Trigger struct {
	Cron    string `yaml:"cron,omitempty" json:"cron,omitempty"`       // 5-field cron, local time
	Mail    string `yaml:"mail,omitempty" json:"mail,omitempty"`       // account for `tools/email.sh watch <account>`
	Webhook string `yaml:"webhook,omitempty" json:"webhook,omitempty"` // name; POST /hook/<agent>/<name>?token=<agent token>
	Every   string `yaml:"every,omitempty" json:"every,omitempty"`     // mail poll interval, Go duration; default 2m
	Prompt  string `yaml:"prompt,omitempty" json:"prompt,omitempty"`   // cron: the message text; webhook: prefix
}

// Kind names which field of the trigger is set.
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
	Name    string  `json:"name"`    // slug: the tmux session name, URL segment, CLI argument. [a-z0-9-]
	Display string  `json:"display"` // agent.name from lolabot.yaml, e.g. "Ultron"
	Role    string  `json:"role"`    // agent.role
	Dir     string  `json:"dir"`     // absolute instance dir
	Runtime Runtime `json:"runtime"`
}

// State is what an agent is doing, as far as lolad can tell.
type State string

const (
	StateStopped  State = "stopped"  // not running, and not supposed to be (asleep)
	StateStarting State = "starting" // launched, harness has not reported in yet
	StateIdle     State = "idle"     // waiting for input
	StateBusy     State = "busy"     // working on a turn
	StateWaiting  State = "waiting"  // blocked on a permission/approval prompt or a question
	StateCrashed  State = "crashed"  // exited when it should be running; restart pending (backoff)
	StateFailed   State = "failed"   // crashed too often; supervisor gave up until someone acts
	StateUnknown  State = "unknown"  // running, no signal either way
)

// AgentStatus is an Agent plus everything the dashboard shows about it.
type AgentStatus struct {
	Agent
	State        State     `json:"state"`
	Desired      string    `json:"desired"` // "up" | "down"
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`    // current process
	SessionSince time.Time `json:"session_since,omitempty"` // current conversation (reset by fresh/rotate)
	LastActivity time.Time `json:"last_activity,omitempty"`
	Restarts     int       `json:"restarts"`        // crash restarts in the last 24h
	NextRestart  time.Time `json:"next_restart,omitempty"`
	Queued       int       `json:"queued"`          // messages not yet delivered
	LastError    string    `json:"last_error,omitempty"`
	Detail       string    `json:"detail,omitempty"` // free text from the last hook, e.g. the tool waiting for approval
}

// MessageStatus is where a queued message is.
type MessageStatus string

const (
	MsgQueued    MessageStatus = "queued"
	MsgDelivered MessageStatus = "delivered"
	MsgFailed    MessageStatus = "failed"
	MsgDropped   MessageStatus = "dropped"
)

// Message is text that will be typed into an agent's session when it is idle.
type Message struct {
	ID          int64         `json:"id"`
	Agent       string        `json:"agent"`
	Source      string        `json:"source"` // "dashboard" | "cli" | "cron" | "mail" | "webhook:<name>" | "system"
	Text        string        `json:"text"`
	Status      MessageStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	DeliveredAt time.Time     `json:"delivered_at,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// Event is one line in an agent's history: lifecycle, state change, trigger, delivery.
type Event struct {
	ID     int64     `json:"id"`
	Agent  string    `json:"agent"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // "started" "exited" "crashed" "restarted" "state" "rotated" "message" "delivered" "trigger" "created" "error"
	Detail string    `json:"detail"`
}

// CreateRequest makes a new lolabot instance and registers it.
type CreateRequest struct {
	Name        string      `json:"name"`         // display name, e.g. "Ultron"; slug derived unless Slug set
	Slug        string      `json:"slug"`         // optional
	Role        string      `json:"role"`         // default "Personal Assistant"
	User        string      `json:"user"`         // operator's full name; default from config
	Dir         string      `json:"dir"`          // optional; default <config agents_dir>/<slug>
	Harness     Harness     `json:"harness"`      // required
	Model       string      `json:"model"`        // optional
	Permissions Permissions `json:"permissions"`  // default bypass
	OnWake      string      `json:"on_wake"`      // optional; sensible default
	Mission     string      `json:"mission"`      // optional: a paragraph about what this agent is for; becomes its first prompt
	Start       bool        `json:"start"`        // start it right away
}

// Terminal is a live attachment to an agent's tmux session. Read yields raw
// terminal output (ANSI included); Write sends keystrokes.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}

// Service is what lolad does, independent of how it is reached. The HTTP API,
// the dashboard and the CLI (through the API) all go through it.
type Service interface {
	List(ctx context.Context) ([]AgentStatus, error)
	Get(ctx context.Context, name string) (AgentStatus, error)

	Start(ctx context.Context, name string) error                 // desired=up
	Stop(ctx context.Context, name string) error                  // desired=down; graceful, then kill
	Restart(ctx context.Context, name string, fresh bool) error   // fresh=true: new conversation + on_wake
	Send(ctx context.Context, name, text, source string) (Message, error)
	Interrupt(ctx context.Context, name string) error             // Escape into the session

	Messages(ctx context.Context, name string, limit int) ([]Message, error)
	Events(ctx context.Context, name string, limit int) ([]Event, error) // name "" = all agents
	Subscribe(ctx context.Context) <-chan Event                          // closed when ctx ends

	Create(ctx context.Context, req CreateRequest) (AgentStatus, error)
	Reload(ctx context.Context) error // rescan the registry

	Attach(ctx context.Context, name string, readOnly bool) (Terminal, error)
	Capture(ctx context.Context, name string, lines int) (string, error) // plain text of the visible pane + scrollback
}

var (
	ErrNotFound   = errors.New("agent not found")
	ErrExists     = errors.New("agent already exists")
	ErrNotRunning = errors.New("agent is not running")
	ErrInvalid    = errors.New("invalid request")
)
