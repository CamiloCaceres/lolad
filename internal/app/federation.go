package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/fed"
)

func (a *App) initFederation() error {
	display := a.Config.Federation.Display
	if display == "" {
		if words := strings.Fields(a.Me().Display); len(words) > 0 {
			display = words[0]
		} else {
			display = a.Me().Name
		}
	}
	engine, err := fed.New(fed.Options{DB: a.DB, StateDir: a.Config.StateDir, ConfigDir: filepath.Dir(a.configPath), Provider: a.Provider, Enabled: a.Config.Federation.Enabled, Display: display, URL: a.Config.Federation.URL, Org: a.Policy.Org, Agents: a.Registry.List, EventSink: a.RecordEvent})
	if err != nil {
		return err
	}
	a.Engine = engine
	engine.OnChange(func(m core.Mail) {
		a.RecordEvent(core.Event{Agent: m.FromName, Kind: "mail", Detail: m.ID + ": " + m.Delivery})
	})
	engine.OnApproval(a.federationApproval)
	return a.initHub()
}

// Run owns both background loops and waits for the outbox before DB shutdown.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.Engine.Run(ctx) }()
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); a.runHub(ctx) }()
	blockedDone := make(chan struct{})
	go func() { defer close(blockedDone); a.retryBlocked(ctx, blockedRetry) }()
	err := a.Supervisor.Run(ctx)
	cancel()
	<-hubDone
	<-blockedDone
	<-done
	return err
}

// remoteSender describes the sender of mail from another lolad for an
// agent's notice: name, person or agent, and whose lolad. It asks the peer's
// directory at most once a minute and never waits more than 3 s.
func (a *App) remoteSender(m core.Mail) core.RemoteContact {
	if a.Engine == nil {
		return core.RemoteContact{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, _ := a.Engine.Sender(ctx, m.FromName)
	return s
}

func (a *App) federationApproval(g core.Grant) {
	a.RecordEvent(core.Event{Agent: g.Local, Kind: "federation", Detail: g.Remote + " wants to message " + g.Local + ": " + g.FirstSubject})
	display := g.RemoteDisplay
	if display == "" {
		display = strings.Split(g.Remote, "@")[0]
	}
	local := g.Local
	if agent, err := a.Registry.Get(local); err == nil && agent.Display != "" {
		local = agent.Display
	}
	// Header values cannot contain remote-supplied newlines: push joins lines.
	a.push(display+" wants to message "+local+": "+g.FirstSubject, "federation approval notification")
}
