package app

import (
	"context"
	"net/http"
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
	return nil
}

// Run owns both background loops and waits for the outbox before DB shutdown.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.Engine.Run(ctx) }()
	err := a.Supervisor.Run(ctx)
	cancel()
	<-done
	return err
}

func (a *App) federationApproval(g core.Grant) {
	a.RecordEvent(core.Event{Agent: g.Local, Kind: "federation", Detail: g.Remote + " wants to message " + g.Local + ": " + g.FirstSubject})
	u := a.Me()
	if u.Ntfy == "" {
		return
	}
	display := g.RemoteDisplay
	if display == "" {
		display = strings.Split(g.Remote, "@")[0]
	}
	local := g.Local
	if agent, err := a.Registry.Get(local); err == nil && agent.Display != "" {
		local = agent.Display
	}
	title := display + " wants to message " + local + ": " + g.FirstSubject
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Ntfy, strings.NewReader(title))
	if err != nil {
		return
	}
	// Header values cannot contain remote-supplied newlines.
	req.Header.Set("Title", strings.Join(strings.Fields(title), " "))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.RecordEvent(core.Event{Kind: "error", Detail: "federation approval notification failed"})
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		a.RecordEvent(core.Event{Kind: "error", Detail: "federation approval notification rejected"})
	}
}
