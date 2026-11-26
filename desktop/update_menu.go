package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/desktop/updater"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// updateVersion is the running app's version; release builds set it:
//
//	-ldflags "-X main.updateVersion=1.2.3"
var updateVersion = "0.0.0-dev"

// updateCheckEvery is how often the background check runs.
const updateCheckEvery = 24 * time.Hour

// updatesPath is the Settings section the tray and the notice open.
const updatesPath = "/settings#updates"

// updates is the app's one update flow (nil until InitUpdates). The tray item,
// the "ready" notice and Settings → Updates (through the bridge) all drive it.
var updates *updateController

// InitUpdates adds the update item to the tray menu and starts a daily
// background check. A found update is downloaded in the background and the
// item turns into "Restart to update…" (plus a notification). Settings →
// Updates shows the same state and runs the same steps. Nothing is installed
// without the user choosing it, and never during onboarding.
//
// Dev environment: LOLAD_UPDATE_URL overrides the manifest URL,
// LOLAD_UPDATE_ALLOW_UNSIGNED=1 permits the public test key and unsigned
// dev builds.
func InitUpdates(app *application.App, menu *application.Menu, win *application.WebviewWindow, notify func(title, body string), open func(path string)) {
	if notify == nil {
		notify = func(string, string) {}
	}
	u, err := updater.New(updater.Config{
		CurrentVersion: updateVersion,
		ManifestURL:    os.Getenv("LOLAD_UPDATE_URL"),
		AllowUnsigned:  os.Getenv("LOLAD_UPDATE_ALLOW_UNSIGNED") == "1",
		Quit:           func() { app.Quit() },
		StopDaemon:     stopAppDaemon,
	})
	var backend updateBackend
	if err == nil {
		backend = u
	}
	flow := newUpdateFlow(backend, updateVersion, updater.StateDir())
	if err != nil {
		flow.st.Error = unavailableReason(err)
	}
	flow.notify = notify
	c := &updateController{app: app, win: win, open: open, flow: flow}
	flow.onChange = c.changed
	label, enabled := trayUpdateLabel(flow.State())
	c.item = menu.Add(label).SetEnabled(enabled).OnClick(func(*application.Context) { c.onClick() })
	menu.Update()
	updates = c
	if backend != nil {
		go c.loop()
	}
}

func unavailableReason(err error) string {
	if errors.Is(err, updater.ErrTestKey) {
		return "This is a development build. It doesn't update itself."
	}
	return "This copy of Lolad can't update itself: " + clip(err.Error(), 160)
}

type updateController struct {
	app  *application.App
	win  *application.WebviewWindow
	open func(path string)
	flow *updateFlow
	item *application.MenuItem
}

// trayUpdateLabel is the tray item for a state.
func trayUpdateLabel(s UpdateState) (string, bool) {
	switch s.Phase {
	case phUnavailable:
		return "Updates unavailable", false
	case phChecking:
		return "Checking for updates…", false
	case phAvailable, phOnboarding:
		return "Update to " + s.Version + "…", true
	case phDownloading:
		return "Downloading update… " + strconv.Itoa(s.Percent) + "%", false
	case phReady:
		return "Restart to update to " + s.Version + "…", true
	case phRestarting:
		return "Updating…", false
	case phFailed:
		return "Update failed: try again…", true
	}
	return "Check for updates…", true
}

// changed mirrors a new state into the tray and the open page.
func (c *updateController) changed(s UpdateState) {
	label, enabled := trayUpdateLabel(s)
	application.InvokeAsync(func() { c.item.SetLabel(label).SetEnabled(enabled) })
	c.push(s)
}

// push hands the state to Settings → Updates (static/updates.js). ExecJS
// runs once the page has reported ready (bridgeJS does that on every page),
// and the call is a no-op on pages without the section.
func (c *updateController) push(s UpdateState) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	js := "window.__loladUpdate&&window.__loladUpdate(" + string(b) + ")"
	go c.win.ExecJS(js)
}

func (c *updateController) loop() {
	time.Sleep(30 * time.Second) // let the app settle first
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		c.flow.Check(ctx, true)
		cancel()
		time.Sleep(updateCheckEvery)
	}
}

// onClick: a ready update asks to restart, as before; anything else opens
// Settings → Updates, checking first when there is nothing to show yet.
func (c *updateController) onClick() {
	s := c.flow.State()
	if s.Phase == phReady {
		c.confirmRestart(s)
		return
	}
	c.open(updatesPath)
	switch s.Phase {
	case phIdle, phUpToDate:
		go c.action("check")
	case phFailed:
		go c.action("retry")
	}
}

func (c *updateController) confirmRestart(s UpdateState) {
	application.InvokeAsync(func() {
		msg := "Lolad " + s.Version + " is downloaded and checked."
		if s.AgentsRestart {
			msg += " Your assistants will restart during the update."
		} else {
			msg += " Your assistants keep running."
		}
		d := c.app.Dialog.Question().SetTitle("Update Lolad").SetMessage(msg)
		yes := d.AddButton("Restart to update")
		no := d.AddButton("Later")
		d.SetDefaultButton(yes).SetCancelButton(no)
		yes.OnClick(func() {
			go func() {
				c.flow.Restart()
				if c.flow.State().Phase == phFailed {
					c.open(updatesPath) // the reason is shown there
				}
			}()
		})
		d.Show()
	})
}

// action runs one step asked for by the page or the tray. It blocks; callers
// run it on a goroutine.
func (c *updateController) action(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	switch name {
	case "state":
		c.push(c.flow.State())
	case "check":
		c.flow.Check(ctx, false)
	case "download":
		c.flow.Download(ctx)
	case "restart":
		c.flow.Restart()
	case "retry":
		c.flow.Retry(ctx)
	}
}

// handleUpdateMessage is the bridge's "lolad:update\n<action>" verb.
func handleUpdateMessage(action string) {
	if updates == nil {
		return
	}
	action = strings.TrimSpace(action)
	switch action {
	case "state", "check", "download", "restart", "retry":
		go updates.action(action)
	}
}
