package core

import (
	"context"
	"time"
)

// Federation: two lolads (Milo's and Jugui's) pair once and then exchange AMP
// mail provider-to-provider. See SPEC.md "Federation (v4)".
//
// Inside lolad a remote participant's name is its full AMP address
// ("ana@jugui.lolad.local"). Local names never contain "@", so
// IsRemoteName tells the two apart everywhere a name appears: Mail.FromName,
// Mail.ToName, Conversation.Peer, Policy checks, Outgoing.To.

// MailHeld: inbound remote mail waiting for the user to approve its sender.
// It is not in the recipient's inbox and nobody is notified until approved.
const MailHeld MailStatus = "held"

// Delivery states of an outbound remote message, on the sender's copy.
const (
	DeliveryQueued    = "queued"    // waiting in the outbox (peer offline, retrying)
	DeliveryHeld      = "held"      // the peer accepted it, waiting for its owner's approval
	DeliveryDelivered = "delivered" // in the recipient's mailbox
	DeliveryBlocked   = "blocked"   // the peer's owner blocked this sender
	DeliveryFailed    = "failed"    // permanent error or gave up after 7 days; DeliveryError says why
)

// Grant states: one per (peer, remote address, local name) pair.
const (
	GrantPending = "pending"
	GrantAllowed = "allowed"
	GrantBlocked = "blocked"
)

// FederationInfo is this lolad as other lolads see it.
type FederationInfo struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`    // e.g. "milo.lolad.local"
	Display     string `json:"display"`     // "Milo"; shown on the other side as "Milo's lolad"
	URL         string `json:"url"`         // base URL peers deliver to; "" = not reachable, invites disabled
	Fingerprint string `json:"fingerprint"` // SHA256:… of the provider key
	Problem     string `json:"problem,omitempty"`
}

// Connection is one paired lolad.
type Connection struct {
	Provider      string    `json:"provider"` // its provider name, e.g. "jugui.lolad.local"
	Display       string    `json:"display"`  // "Jugui"
	URL           string    `json:"url"`
	Fingerprint   string    `json:"fingerprint"`
	Login         string    `json:"login,omitempty"` // Tailscale login seen when pairing; later requests must match
	Paused        bool      `json:"paused"`          // nothing in or out; their outbox keeps retrying
	UserReachable bool      `json:"user_reachable"`  // their participants may message the user (first message still needs approval)
	Shares        []Share   `json:"shares"`
	CreatedAt     time.Time `json:"created_at"`
	LastSeen      time.Time `json:"last_seen,omitzero"` // last successful request either way
	LastError     string    `json:"last_error,omitempty"`
	Queued        int       `json:"queued"`  // our messages waiting to reach them
	Pending       int       `json:"pending"` // their senders waiting for approval
}

// Share makes one local agent reachable from a connection.
type Share struct {
	Local string `json:"local"` // agent slug
	// Remote lists their addresses that are pre-approved, both ways. Empty =
	// anyone on that lolad may write, and each new sender's first message
	// needs approval.
	Remote []string `json:"remote,omitempty"`
}

// ConnectionUpdate changes a connection; nil fields stay as they are.
type ConnectionUpdate struct {
	Display       *string `json:"display,omitempty"`
	Paused        *bool   `json:"paused,omitempty"`
	UserReachable *bool   `json:"user_reachable,omitempty"`
	Shares        []Share `json:"shares,omitempty"` // replaces all shares when non-nil
}

// Invite is shown once to the user who sends it to the other person. Code is
// a secret (single use, 72 h): treat it like a password.
type Invite struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Grant is the approval state of one remote sender ↔ local participant pair.
// Once allowed, both directions flow freely.
type Grant struct {
	Peer          string    `json:"peer"`   // provider
	Remote        string    `json:"remote"` // their full address
	RemoteDisplay string    `json:"remote_display,omitempty"`
	Local         string    `json:"local"` // agent slug or the user's name
	State         string    `json:"state"`
	Origin        string    `json:"origin"` // "approved" (user allowed it) | "shared" (pre-approved in a share) | "initiated" (we wrote first)
	Held          int       `json:"held"`   // messages waiting while pending
	FirstSubject  string    `json:"first_subject,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	DecidedAt     time.Time `json:"decided_at,omitzero"`
}

// RemoteContact is a participant on a connected lolad that we may write to.
type RemoteContact struct {
	Address     string `json:"address"` // full AMP address (= its name inside lolad)
	Name        string `json:"name"`    // local part
	Display     string `json:"display"`
	Role        string `json:"role,omitempty"`
	Kind        string `json:"kind"` // agent | human
	Peer        string `json:"peer"`
	PeerDisplay string `json:"peer_display"`
}

// Federator is the federation engine as the rest of lolad sees it
// (internal/fed implements it; internal/app exposes it through Service).
type Federator interface {
	Info(ctx context.Context) FederationInfo
	Connections(ctx context.Context) ([]Connection, error)
	CreateInvite(ctx context.Context) (Invite, error)
	AcceptInvite(ctx context.Context, code string) (Connection, error)
	UpdateConnection(ctx context.Context, provider string, u ConnectionUpdate) (Connection, error)
	RemoveConnection(ctx context.Context, provider string) error // tells the peer, best effort
	Grants(ctx context.Context) ([]Grant, error)                 // pending first, then newest decided
	DecideGrant(ctx context.Context, peer, remote, local string, allow bool) (Grant, error)
	RemoteContacts(ctx context.Context, local string) ([]RemoteContact, error) // whom `local` may write to remotely
}

// IsRemoteName reports whether a participant name is a full remote address.
func IsRemoteName(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] == '@' {
			return true
		}
	}
	return false
}
