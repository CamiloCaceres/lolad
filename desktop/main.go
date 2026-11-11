// Lolad desktop: a native window, tray icon and single-instance guard around
// the dashboard the lolad daemon already serves. See docs/desktop.md.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed assets
var assets embed.FS

// hasFlag reports whether the process was started with the given flag. The
// flag package is avoided on purpose: macOS adds its own arguments.
func hasFlag(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

func main() {
	url := dashboardURL(listenAddr())
	// --background (the login item) starts tray only, with no window.
	background := hasFlag("--background")
	notif := notifications.New()
	var app *application.App
	app = application.New(application.Options{
		Name:        "Lolad",
		Description: "Your agents, in one window",
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Services:    []application.Service{application.NewService(notif)},
		Mac: application.MacOptions{
			// Closing the window must never end the app: the tray keeps running.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
			// No Dock icon until a window opens (see shell.showMain).
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		RawMessageHandler: func(w application.Window, msg string, o *application.OriginInfo) {
			handleBridge(app, originOf(url))(w, msg, o)
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.caceres.lolad.desktop" + os.Getenv("LOLAD_DESKTOP_INSTANCE"), // suffix lets tests run beside an installed copy
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				showMain()
			},
		},
	})

	installAppMenu(app)

	// Offline until the daemon answers; the poller below navigates to it.
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "Lolad",
		Width:     1280,
		Height:    860,
		MinWidth:  940,
		MinHeight: 600,
		URL:       "/",
		Hidden:    true, // shown below, once the saved frame is applied
		// Runs at document start on every page. ExecJS can't be used: it waits
		// for the Wails runtime, which a remote page never loads.
		JS: bridgeJS,
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarDefault,
		},
	})
	sh = newShell(app, win, url, notif, background)
	// ⌘W and the close button hide the window; the tray keeps running.
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		sh.hideMain()
		e.Cancel()
	})
	sh.watchFrame()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		sh.restoreFrame()
		// A login item launch has no --background argument on macOS
		// (SMAppService cannot pass one), so also ask the launch event.
		if !sh.background && !launchedAtLogin() {
			sh.showMain()
		}
	})

	sh.setupNotifications()
	sh.buildTray()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFeed(url, sh.onData)
	go f.Run(ctx)
	go sh.metaLoop(ctx)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

var win *application.WebviewWindow
var sh *shell

// showMain brings the window forward (and the Dock icon with it).
func showMain() { sh.showMain() }

// installAppMenu gives macOS the standard menus. Without an Edit menu, ⌘C/⌘V
// do not work in the webview.
func installAppMenu(app *application.App) {
	if runtime.GOOS != "darwin" {
		return
	}
	m := app.NewMenu()
	m.AddRole(application.AppMenu)
	m.AddRole(application.FileMenu) // Close, ⌘W (hides the window)
	m.AddRole(application.EditMenu)
	m.AddRole(application.ViewMenu)
	m.AddRole(application.WindowMenu)
	app.Menu.SetApplicationMenu(m)
}
