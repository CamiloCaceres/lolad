package core

import (
	"context"
	"time"
)

// Friend connect (docs/friend-connect-design.md §6): one invite link that
// pairs two lolads, joins the hub when needed and pre-approves each side.

// FriendInviter makes and accepts friend invite links. Optional on a Service:
// the API mounts /api/friends/* only when the service implements it. Every
// refusal implements UserMessage() string, safe to show as is.
type FriendInviter interface {
	CreateFriendInvite(ctx context.Context, r FriendInviteRequest) (FriendInvite, error) // link returned once
	FriendInvites(ctx context.Context) ([]FriendInviteInfo, error)                       // open and used, never codes
	CancelFriendInvite(ctx context.Context, id string) error
	PreviewFriendInvite(ctx context.Context, link string) (InvitePreview, error) // parse only: no network, no side effects
	AcceptFriendInvite(ctx context.Context, link string, r AcceptRequest) (AcceptResult, error)
}

// FriendInviteRequest: the agents offered to the friend, and a private note
// ("Ben") the inviter sees in their list of links.
type FriendInviteRequest struct {
	Agents []string `json:"agents"`
	For    string   `json:"for,omitempty"`
}

// FriendInvite is shown once. Link is a secret: anyone with it can connect.
type FriendInvite struct {
	ID        string    `json:"id"`
	Link      string    `json:"link"`
	ExpiresAt time.Time `json:"expires_at"`
}

// FriendInviteInfo describes an open or used link, never its code.
type FriendInviteInfo struct {
	ID            string    `json:"id"`
	For           string    `json:"for,omitempty"`
	Agents        []string  `json:"agents"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	UsedByDisplay string    `json:"used_by_display,omitempty"`
	UsedAt        time.Time `json:"used_at,omitzero"`
}

// InvitePreview is what a link says, read locally before accepting.
type InvitePreview struct {
	FromDisplay      string       `json:"from_display"`
	Agents           []AgentOffer `json:"agents"`
	ExpiresAt        time.Time    `json:"expires_at"`
	Expired          bool         `json:"expired"`
	SameName         bool         `json:"same_name"`
	AlreadyConnected bool         `json:"already_connected"`
	JoinsNetwork     bool         `json:"joins_network"`
}

// AgentOffer is one agent the inviter offers.
type AgentOffer struct {
	Slug    string `json:"slug"`
	Display string `json:"display"`
}

// AcceptRequest: the agents this side offers back.
type AcceptRequest struct {
	Agents []string `json:"agents"`
}

// AcceptResult: State is FriendConnected or FriendWaiting (the inviter's lolad
// is offline; the connection completes on its own when it comes back).
type AcceptResult struct {
	Connection Connection `json:"connection"`
	State      string     `json:"state"`
	Message    string     `json:"message,omitempty"`
}

// AcceptResult states.
const (
	FriendConnected = "connected"
	FriendWaiting   = "waiting"
)
