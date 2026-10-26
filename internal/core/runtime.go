package core

import (
	"context"
	"time"
)

// Interfaces between the runtime packages. internal/tmux implements Sessions,
// internal/harness implements Launcher, internal/deliver implements Notifier,
// internal/supervisor consumes all three and implements HookSink.

// Sessions is the tmux layer. Every call targets lolad's private tmux server
// (`tmux -L lolad`, with a lolad-owned config so the user's ~/.tmux.conf never
// changes window/pane numbering). Session name == agent slug.
type Sessions interface {
	Exists(name string) bool
	List() ([]string, error)
	// Start creates a detached session running argv directly (no interactive
	// shell in between: nothing is ever typed to launch a program).
	Start(name, dir string, env []string, argv []string, cols, rows int) error
	PanePID(name string) (int, error)
	PaneDead(name string) (bool, int, error) // remain-on-exit is on: dead pane + exit status
	Kill(name string) error
	SendLiteral(name, text string) error   // send-keys -l
	SendKeys(name string, keys ...string) error // named keys: Enter Escape BSpace C-c ...
	Capture(name string, lines int, withEscapes bool) (string, error)
	Attach(ctx context.Context, name string, readOnly bool) (Terminal, error)
}

// LaunchOpts says how this particular launch differs from the agent's defaults.
type LaunchOpts struct {
	Fresh       bool   // new conversation; false = resume the last one
	FirstPrompt string // passed as an argv prompt, never typed
	LoladBin    string // absolute path of the lolad binary (hooks and `lolad msg` call it)
	LoladURL    string // http://127.0.0.1:4100
	StateDir    string // lolad's state dir; harness-managed files for this agent go under <StateDir>/agents/<slug>/
}

// LaunchSpec is a ready-to-run process.
type LaunchSpec struct {
	Argv []string
	Env  []string // full environment for the process, KEY=VALUE
	Dir  string
}

// Launcher knows one harness: how to start, resume, report state and accept a prompt.
type Launcher interface {
	Harness() Harness
	// Prepare writes whatever managed config the launch needs (hook wiring
	// under StateDir, never the user's global config) and returns the spec.
	Prepare(a Agent, opts LaunchOpts) (LaunchSpec, error)
	// PromptReady reports whether a captured pane shows the harness's input
	// box ready for typing (used before notifying and as a fallback idle signal).
	PromptReady(pane string) bool
}

// NotifyResult is the proof level of one attempt to surface mail into a session.
type NotifyResult string

const (
	NotifyConfirmed NotifyResult = "confirmed" // submitted: the ref tag is above the input box
	NotifyStaged    NotifyResult = "staged"    // text sits unsubmitted in the input box
	NotifyBusy      NotifyResult = "busy"      // not attempted: session not ready
	NotifyFailed    NotifyResult = "failed"
)

// Notifier types a one-line notice of a message into an agent's session and
// proves it was submitted. The message itself stays in the mailbox; the agent
// reads it with `lolad msg read <id>`.
type Notifier interface {
	Notify(ctx context.Context, a Agent, m Mail) (NotifyResult, error)
}

// HookEvent is what an agent's harness hook reports (through `lolad hook <event>`).
type HookEvent struct {
	Agent     string    `json:"agent"`
	Event     string    `json:"event"` // session_start | prompt_submit | stop | notification | permission | session_end | pre_tool | post_tool
	State     State     `json:"state"` // the state this event implies ("" = no change)
	Detail    string    `json:"detail,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Seq       int64     `json:"seq"` // monotonic per agent process, for ordering
	At        time.Time `json:"at"`
}

// HookReply is what the daemon answers. For a Stop event with unread mail,
// Block=true and Reason lists the mail, so the agent reads it before idling
// (Claude Code's Stop hook {"decision":"block","reason":...}). For
// session_start, Context is injected as additional context.
type HookReply struct {
	Block   bool   `json:"block,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Context string `json:"context,omitempty"`
}

// HookSink receives hook events inside the daemon.
type HookSink interface {
	HandleHook(ctx context.Context, ev HookEvent) HookReply
}
