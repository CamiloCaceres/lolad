package mailprov

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
	"time"
)

type Settings struct {
	Provider  string   `json:"provider"`
	Domain    string   `json:"domain"`
	Pattern   string   `json:"pattern"`
	Operators []string `json:"operators"`
	Watch     string   `json:"watch"`
	TokenSet  bool     `json:"token_set"`
}
type ProviderUpdate struct {
	Settings
	Token string `json:"token,omitempty"`
}
type Request struct {
	Watch         string            `json:"watch,omitempty"`
	Address       string            `json:"address"`
	Account       core.EmailAccount `json:"account"`
	DeleteMailbox bool              `json:"delete_mailbox"`
	Confirm       string            `json:"confirm"`
}
type Result struct {
	Address string          `json:"address"`
	Message string          `json:"message"`
	Test    *core.EmailTest `json:"test,omitempty"`
}
type Row struct {
	TestAddress    string          `json:"test_address,omitempty"`
	Supported      bool            `json:"supported"`
	Name           string          `json:"name"`
	Display        string          `json:"display"`
	Address        string          `json:"address"`
	Provider       string          `json:"provider"`
	Watch          string          `json:"watch"`
	PendingRestart bool            `json:"pending_restart"`
	LastTest       *core.EmailTest `json:"last_test,omitempty"`
	TestedAt       time.Time       `json:"tested_at,omitzero"`
	Suggested      string          `json:"suggested"`
	Error          string          `json:"error,omitempty"`
}
type Fleet struct {
	Settings Settings `json:"settings"`
	Agents   []Row    `json:"agents"`
}

// Service is optional, allowing older integrations to keep core.Service stable.
type Service interface {
	CheckEmail(context.Context, string, string) (core.EmailTest, error)
	EmailFleet(context.Context) (Fleet, error)
	EmailProvider(context.Context, *ProviderUpdate) (Settings, error)
	ProvisionEmail(context.Context, string, Request) (Result, error)
	RotateEmail(context.Context, string, Request) (Result, error)
	DetachEmail(context.Context, string, Request) (Result, error)
}
type Backend interface {
	List(context.Context) ([]core.AgentStatus, error)
	Email(context.Context, string) (core.EmailSettings, error)
	SaveEmail(context.Context, string, core.EmailSettings) (core.EmailSettings, error)
	TestEmail(context.Context, string, string) (core.EmailTest, error)
	SetEnv(context.Context, string, string, string) error
}
