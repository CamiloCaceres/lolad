package app

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/mailprov"
	"sync"
)

var mailManagers sync.Map

func (a *App) mailManager() *mailprov.Manager {
	if m, ok := mailManagers.Load(a); ok {
		return m.(*mailprov.Manager)
	}
	m := mailprov.New(a, a.Config.StateDir, func(name, key string) (string, error) {
		v, d, e := a.settings(name)
		if e != nil {
			return "", e
		}
		return d.ProvisionPassword(v.Runtime.EnvFile, key)
	})
	actual, _ := mailManagers.LoadOrStore(a, m)
	return actual.(*mailprov.Manager)
}
func (a *App) EmailFleet(c context.Context) (mailprov.Fleet, error) {
	return a.mailManager().EmailFleet(c)
}
func (a *App) EmailProvider(c context.Context, u *mailprov.ProviderUpdate) (mailprov.Settings, error) {
	return a.mailManager().EmailProvider(c, u)
}
func (a *App) ProvisionEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return a.mailManager().ProvisionEmail(c, n, r)
}
func (a *App) RotateEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return a.mailManager().RotateEmail(c, n, r)
}
func (a *App) DetachEmail(c context.Context, n string, r mailprov.Request) (mailprov.Result, error) {
	return a.mailManager().DetachEmail(c, n, r)
}

func (a *App) CheckEmail(c context.Context, n, address string) (core.EmailTest, error) {
	return a.mailManager().CheckEmail(c, n, address)
}
