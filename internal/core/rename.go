package core

import (
	"context"
	"time"
)

// LoladName is this lolad's name (the tenant: "milo" in you@milo.lolad.local)
// and what renaming it would affect. GET /api/lolad/name.
type LoladName struct {
	Name     string `json:"name"`
	Provider string `json:"provider"` // <name>.lolad.local
	// Restart is "auto" when a service manager or the desktop app starts
	// lolad again after it exits, "manual" when the user has to.
	Restart     string       `json:"restart"`
	Connections []Connection `json:"connections"`       // ended by a rename; each friend needs a new link
	OpenLinks   int          `json:"open_links"`        // invite links and codes not used yet; cancelled
	Outbox      int          `json:"outbox"`            // remote mail still on the way; marked not sent
	Pending     *RenameStep  `json:"pending,omitempty"` // a rename waiting for the restart
	Last        *RenameStep  `json:"last,omitempty"`    // the last rename, finished or failed
}

// RenameStep is one rename: requested, finished, or rolled back with Error.
type RenameStep struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"` // set when it was rolled back
}

// NameCheck answers whether a typed name can be used. Name is the name it
// becomes ("Milo PC" -> "milo-pc"); Message says why not, or what addresses
// will look like.
type NameCheck struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// RenameRequest is POST /api/lolad/name. Anyway renames even with remote
// mail still on the way (it is marked not sent).
type RenameRequest struct {
	Name   string `json:"name"`
	Anyway bool   `json:"anyway,omitempty"`
}

// RenameAccepted is the 202 answer: lolad exits so its service manager
// starts it again, and the rename is applied at that start.
type RenameAccepted struct {
	Name       string `json:"name"`
	Restarting bool   `json:"restarting"` // false: the user must restart lolad
}

// LoladRenamer renames this lolad (docs: SPEC "Rename").
type LoladRenamer interface {
	LoladName(context.Context) (LoladName, error)
	CheckLoladName(context.Context, string) (NameCheck, error)
	RenameLolad(context.Context, RenameRequest) (RenameAccepted, error)
}
