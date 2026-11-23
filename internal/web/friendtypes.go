package web

import "github.com/CamiloCaceres/lolad/internal/core"

// Friend connect types (docs/friend-connect-design.md §6) live in core; these
// short names keep the dashboard and its fake readable.
type (
	FriendInviter       = core.FriendInviter
	FriendInviteRequest = core.FriendInviteRequest
	FriendInvite        = core.FriendInvite
	FriendInviteInfo    = core.FriendInviteInfo
	InvitePreview       = core.InvitePreview
	AgentOffer          = core.AgentOffer
	AcceptRequest       = core.AcceptRequest
	AcceptResult        = core.AcceptResult
)

// Accept states.
const (
	FriendConnected = core.FriendConnected
	FriendWaiting   = core.FriendWaiting
)
