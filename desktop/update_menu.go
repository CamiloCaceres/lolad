package main

import (
	"context"
	"fmt"
	"os"
	"sync"
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

// InitUpdates adds "Check for updates…" to the tray menu and starts a daily
// background check. A found update is downloaded in the background and the
// menu item turns into "Restart to update…" (plus a notification). Nothing is
// installed without the user choosing it, and never during onboarding.
//
// Dev environment: LOLAD_UPDATE_URL overrides the manifest URL,
// LOLAD_UPDATE_ALLOW_UNSIGNED=1 permits the public test key and unsigned
// dev builds.
func InitUpdates(app *application.App, menu *application.Menu, win *application.WebviewWindow, notify func(title, body string)) {
	if notify == nil {
		notify = func(string, string) {}
	}
	u, err := updater.New(updater.Config{
		CurrentVersion: updateVersion,
		ManifestURL:    os.Getenv("LOLAD_UPDATE_URL"),
		AllowUnsigned:  os.Getenv("LOLAD_UPDATE_ALLOW_UNSIGNED") == "1",
		Quit:           func() { app.Quit() },
	})
	if err != nil {
		menu.Add("Updates unavailable").SetEnabled(false)
		menu.Update()
		return
	}
	c := &updateController{app: app, u: u, win: win, notify: notify}
	c.item = menu.Add("Check for updates…").OnClick(func(*application.Context) { c.onClick() })
	menu.Update()
	go c.loop()
}

type updateController struct {
	app    *application.App
	u      *updater.Updater
	win    *application.WebviewWindow
	notify func(title, body string)
	item   *application.MenuItem

	mu    sync.Mutex
	busy  bool
	ready string // downloaded artifact waiting for "Restart to update"
	res   *updater.CheckResult
}

func (c *updateController) setLabel(s string, enabled bool) {
	application.InvokeAsync(func() {
		c.item.SetLabel(s).SetEnabled(enabled)
	})
}

func (c *updateController) loop() {
	time.Sleep(30 * time.Second) // let the app settle first
	for {
		c.backgroundCheck()
		time.Sleep(updateCheckEvery)
	}
}

func (c *updateController) backgroundCheck() {
	if !c.begin() {
		return
	}
	defer c.end()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := c.u.Check(ctx)
	if err != nil || !res.Available || res.ManualInstall || res.OnboardingBlocked {
		return // quiet: unsigned, older, offline or mid-onboarding
	}
	c.download(ctx, res)
	c.mu.Lock()
	ready := c.ready != ""
	c.mu.Unlock()
	if ready {
		c.notify("Lolad "+res.Version+" is ready", res.Message+" Restart to update.")
	}
}

func (c *updateController) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy {
		return false
	}
	c.busy = true
	return true
}

func (c *updateController) end() { c.mu.Lock(); c.busy = false; c.mu.Unlock() }

// download fetches res and, on success, remembers it and relabels the menu.
func (c *updateController) download(ctx context.Context, res *updater.CheckResult) {
	last := -1
	path, err := c.u.Download(ctx, res, func(done, total int64) {
		if total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct != last && pct%5 == 0 {
			last = pct
			c.setLabel(fmt.Sprintf("Downloading update… %d%%", pct), false)
		}
	})
	if err != nil {
		c.setLabel("Check for updates…", true)
		return
	}
	c.mu.Lock()
	c.ready, c.res = path, res
	c.mu.Unlock()
	c.setLabel("Restart to update to "+res.Version+"…", true)
}

func (c *updateController) onClick() {
	c.mu.Lock()
	ready, res := c.ready, c.res
	c.mu.Unlock()
	if ready != "" {
		go c.confirmAndApply(ready, res)
		return
	}
	go c.manualCheck()
}

func (c *updateController) info(title, msg string) {
	application.InvokeAsync(func() {
		c.app.Dialog.Info().SetTitle(title).SetMessage(msg).Show()
	})
}

func (c *updateController) manualCheck() {
	if !c.begin() {
		return
	}
	defer c.end()
	c.setLabel("Checking for updates…", false)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := c.u.Check(ctx)
	c.setLabel("Check for updates…", true)
	switch {
	case err != nil:
		c.info("Could not check for updates", err.Error())
		return
	case !res.Available:
		c.info("Lolad is up to date", "You have the latest version ("+updateVersion+").")
		return
	case res.ManualInstall:
		c.info("Update available", res.Message+"\n\nRelease notes: "+res.NotesURL)
		return
	case res.OnboardingBlocked:
		c.info("Update available", "Lolad "+res.Version+" is available. Finish setting up Lolad first, then check again.")
		return
	}
	c.download(ctx, res)
	c.mu.Lock()
	ready, r := c.ready, c.res
	c.mu.Unlock()
	if ready == "" {
		c.info("Update failed", "The update could not be downloaded or did not pass verification.")
		return
	}
	c.confirmAndApply(ready, r)
}

func (c *updateController) confirmAndApply(path string, res *updater.CheckResult) {
	if c.u.OnboardingInProgress() {
		c.info("Update", "Finish setting up Lolad first; the update will wait.")
		return
	}
	application.InvokeAsync(func() {
		d := c.app.Dialog.Question().SetTitle("Update Lolad").
			SetMessage(res.Message + "\n\nRelease notes: " + res.NotesURL)
		yes := d.AddButton("Restart to update")
		no := d.AddButton("Later")
		d.SetDefaultButton(yes).SetCancelButton(no)
		yes.OnClick(func() { go c.apply(path) })
		d.Show()
	})
}

func (c *updateController) apply(path string) {
	if err := c.u.Apply(path); err != nil {
		c.info("Update failed", err.Error())
		return
	}
	// Apply asks the app to quit itself once the new version is in place.
}
