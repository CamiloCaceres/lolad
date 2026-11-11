package main

import (
	"encoding/json"
	"fmt"
)

// Agent is the part of GET /api/agents the tray needs.
type Agent struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	State   string `json:"state"`
	Desired string `json:"desired"`
	Unread  int    `json:"unread"`
}

// Conversation is the part of GET /api/mail/conversations the tray needs.
type Conversation struct {
	Peer    string `json:"peer"`
	Display string `json:"display"`
	Unread  int    `json:"unread"`
	Pending bool   `json:"pending"`
	Last    struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
		Context struct {
			Userchat struct {
				Kind string `json:"kind"`
			} `json:"userchat"`
		} `json:"context"`
	} `json:"last"`
}

// IsAsk reports whether the last message is an ask still waiting for an answer.
func (c Conversation) IsAsk() bool {
	return c.Pending && c.Last.Context.Userchat.Kind == "ask"
}

// Connection is the part of GET /api/connections the tray needs.
type Connection struct {
	Provider string `json:"provider"`
	Display  string `json:"display"`
	Pending  int    `json:"pending"` // their senders waiting for approval
}

type iconKind int

const (
	iconNormal iconKind = iota
	iconAttention
	iconProblem
)

func (k iconKind) String() string { return [...]string{"normal", "attention", "problem"}[k] }

// Snapshot is everything the tray shows.
type Snapshot struct {
	Up        bool // the daemon answered
	Running   int  // starting, idle, busy
	Waiting   int
	Failed    int // failed or crashed
	Questions int // conversations whose last message is an unanswered ask
	Unread    int // unread messages
	Pending   int // federation senders waiting for approval
}

// Compute builds a Snapshot. A nil daemon-down snapshot is just Snapshot{}.
func Compute(agents []Agent, convs []Conversation, conns []Connection) Snapshot {
	s := Snapshot{Up: true}
	for _, a := range agents {
		switch a.State {
		case "starting", "idle", "busy":
			s.Running++
		case "waiting":
			s.Waiting++
			s.Running++ // a waiting agent is alive; it is also asking for you
		case "failed", "crashed":
			s.Failed++
		}
	}
	for _, c := range convs {
		s.Unread += c.Unread
		if c.IsAsk() {
			s.Questions++
		}
	}
	for _, c := range conns {
		s.Pending += c.Pending
	}
	return s
}

// Icon picks the tray icon: problem beats needs-you beats normal.
func (s Snapshot) Icon() iconKind {
	switch {
	case !s.Up || s.Failed > 0:
		return iconProblem
	case s.Questions > 0 || s.Waiting > 0 || s.Pending > 0:
		return iconAttention
	}
	return iconNormal
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// InboxLabel is the menu line that opens the inbox.
func (s Snapshot) InboxLabel() string {
	switch {
	case s.Questions > 0:
		return plural(s.Questions, "question waiting", "questions waiting")
	case s.Unread > 0:
		return plural(s.Unread, "unread message", "unread messages")
	}
	return "Inbox: nothing waiting"
}

// AgentsLabel is the menu line that opens the agent list.
func (s Snapshot) AgentsLabel() string {
	out := fmt.Sprintf("Agents: %d running", s.Running)
	if s.Waiting > 0 {
		out += fmt.Sprintf(", %d waiting", s.Waiting)
	}
	if s.Failed > 0 {
		out += fmt.Sprintf(", %d failed", s.Failed)
	}
	return out
}

// Tooltip is the hover text of the tray icon.
func (s Snapshot) Tooltip() string {
	if !s.Up {
		return "Lolad: lolad is not running"
	}
	t := "Lolad: " + s.AgentsLabel()
	if s.Questions > 0 || s.Unread > 0 {
		t += "; " + s.InboxLabel()
	}
	return t
}

func decodeList[T any](b []byte) ([]T, error) {
	var v []T // a null body decodes to nil
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}
