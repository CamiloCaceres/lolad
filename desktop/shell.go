package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// shell is everything around the window: the tray icon and its menu, native
// notifications, login items, first-run and migration. It is a client of the
// daemon and keeps no state the daemon does not also have, except prefs.
type shell struct {
	app        *application.App
	win        *application.WebviewWindow
	base       string // "http://127.0.0.1:4100" (no trailing slash)
	prefs      *prefStore
	notif      *notifications.NotificationService
	background bool

	tray *application.SystemTray
	menu *application.Menu
	it   struct {
		inbox, agents, fed, status, switchTo, start, stop *application.MenuItem
		svcLogin, appLogin                                *application.MenuItem
		notify                                            map[string]*application.MenuItem
	}

	det   *detector
	batch *batcher

	mu        sync.Mutex
	last      Data
	up        bool
	seenUp    bool
	icon      iconKind
	iconSet   bool
	health    *Health
	setup     SetupState
	svc       ServiceStatus
	svcKnown  bool
	appLogOn  bool
	appLogMsg string
	linkByID  map[string]string
	targeted  bool // first-run navigation already done this launch
	offered   bool // migration offer already shown this launch
}

func newShell(app *application.App, win *application.WebviewWindow, base string, notif *notifications.NotificationService, background bool) *shell {
	s := &shell{app: app, win: win, base: strings.TrimSuffix(base, "/"), notif: notif, background: background,
		prefs: loadPrefs(filepath.Join(prefsDir(), "desktop.json")), det: newDetector(), linkByID: map[string]string{}}
	s.batch = newBatcher(2*time.Second, 12, s.deliver)
	return s
}

// ---------------------------------------------------------------- window

func (s *shell) showMain() {
	application.InvokeAsync(func() {
		setDockVisible(true)
		s.win.Show()
		s.win.Restore()
		s.win.Focus()
	})
}

func (s *shell) hideMain() {
	s.win.Hide()
	setDockVisible(false)
}

// openLink shows the window at a dashboard path ("/inbox/iris").
func (s *shell) openLink(path string) {
	s.mu.Lock()
	up := s.up
	s.mu.Unlock()
	if up {
		target := s.base + path
		application.InvokeAsync(func() { s.win.SetURL(target) })
	}
	s.showMain()
}

// restoreFrame applies the saved window frame when it is still on a screen.
func (s *shell) restoreFrame() {
	var screens [][4]int
	for _, sc := range s.app.Screen.GetAll() {
		screens = append(screens, [4]int{sc.Bounds.X, sc.Bounds.Y, sc.Bounds.Width, sc.Bounds.Height})
	}
	if f := usableFrame(s.prefs.Get().Frame, screens); f != nil {
		s.win.SetBounds(application.Rect{X: f.X, Y: f.Y, Width: f.W, Height: f.H})
	}
}

// watchFrame saves the frame (debounced) as the window moves or resizes.
func (s *shell) watchFrame() {
	var mu sync.Mutex
	var t *time.Timer
	save := func(*application.WindowEvent) {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
		}
		t = time.AfterFunc(600*time.Millisecond, func() {
			application.InvokeAsync(func() {
				if !s.win.IsVisible() {
					return
				}
				b := s.win.Bounds()
				if b.Width < 940 || b.Height < 600 {
					return
				}
				_ = s.prefs.Update(func(p *Prefs) { p.Frame = &Frame{b.X, b.Y, b.Width, b.Height} })
			})
		})
	}
	s.win.RegisterHook(events.Common.WindowDidMove, save)
	s.win.RegisterHook(events.Common.WindowDidResize, save)
}

// ---------------------------------------------------------------- tray

func (s *shell) buildTray() {
	s.tray = s.app.SystemTray.New()
	s.tray.SetTooltip("Lolad")
	s.applyIcon(iconNormal)

	m := s.app.NewMenu()
	s.menu = m
	m.Add("Open Lolad").OnClick(func(*application.Context) { s.openLink("/") })
	m.AddSeparator()
	s.it.inbox = m.Add("Inbox: nothing waiting").OnClick(func(*application.Context) { s.openLink("/") })
	s.it.agents = m.Add("Agents").OnClick(func(*application.Context) { s.openLink("/agents") })
	s.it.fed = m.Add("Senders awaiting approval").OnClick(func(*application.Context) { s.openLink("/connections") }).SetHidden(true)
	m.AddSeparator()
	s.it.status = m.Add("Checking…").SetEnabled(false)
	s.it.switchTo = m.Add("Switch to the app's lolad…").OnClick(func(*application.Context) { go s.offerMigration(true) }).SetHidden(true)
	s.it.start = m.Add("Start lolad").OnClick(func(*application.Context) { go s.startLolad() })
	s.it.stop = m.Add("Stop lolad").OnClick(func(*application.Context) { go s.stopLolad() }).SetHidden(true)
	// There is no pause/resume-all endpoint in the daemon API, so that
	// docs/desktop.md §6.3 item is left out until one exists.
	m.AddSeparator()
	s.it.svcLogin = m.AddCheckbox("Start lolad at login", false).OnClick(func(*application.Context) { go s.toggleServiceLogin() })
	s.it.appLogin = m.AddCheckbox("Open Lolad at login", false).OnClick(func(*application.Context) { go s.toggleAppLogin() })
	sub := m.AddSubmenu("Notifications")
	s.it.notify = map[string]*application.MenuItem{}
	for _, k := range notifyKinds {
		kind := k.Kind
		s.it.notify[kind] = sub.AddCheckbox(k.Label, s.prefs.NotifyOn(kind)).OnClick(func(ctx *application.Context) {
			on := !s.prefs.NotifyOn(kind)
			_ = s.prefs.Update(func(p *Prefs) {
				if p.Notify == nil {
					p.Notify = map[string]bool{}
				}
				p.Notify[kind] = on
			})
			s.it.notify[kind].SetChecked(on)
		})
	}
	m.AddSeparator()
	m.Add("Open in Browser").OnClick(func(*application.Context) { _ = s.app.Browser.OpenURL(s.base + "/") })
	m.Add("View logs").OnClick(func(*application.Context) { go s.viewLogs() })
	m.Add("Reload").OnClick(func(*application.Context) { application.InvokeAsync(func() { s.win.Reload() }) })
	InitUpdates(s.app, m, s.win, func(title, body string) {
		s.deliver(Note{ID: "update", Kind: "update", Title: title, Body: body, Link: "/"})
	})
	if runtime.GOOS == "darwin" {
		m.Add("Uninstall…").OnClick(func(*application.Context) { go s.uninstall() })
	}
	m.AddSeparator()
	m.Add("Quit Lolad (agents keep running)").OnClick(func(*application.Context) { s.app.Quit() })
	s.tray.SetMenu(m)
	// Left-click opens the window; the menu stays on right-click.
	s.tray.OnClick(func() { s.openLink("/") })
}

func (s *shell) applyIcon(k iconKind) {
	read := func(n string) []byte {
		b, err := assets.ReadFile("assets/icons/" + n + ".png")
		if err != nil {
			log.Printf("icon %s: %v", n, err)
		}
		return b
	}
	if runtime.GOOS == "darwin" {
		s.tray.SetTemplateIcon(read("tray-mac-" + k.String()))
		return
	}
	s.tray.SetIcon(read("tray-win-light-" + k.String()))
	s.tray.SetDarkModeIcon(read("tray-win-dark-" + k.String()))
}

// ---------------------------------------------------------------- data

// onData receives every refresh from the feed (on the feed's goroutine).
func (s *shell) onData(d Data) {
	s.mu.Lock()
	wasUp, seen := s.up, s.seenUp
	s.up, s.seenUp, s.last = d.Snap.Up, true, d
	icon, iconSet := d.Snap.Icon(), s.iconSet
	changedIcon := !iconSet || icon != s.icon
	s.icon, s.iconSet = icon, true
	health, ready := s.health, s.readyLocked()
	s.mu.Unlock()

	if changedIcon {
		s.applyIcon(icon)
	}
	s.tray.SetTooltip(d.Snap.Tooltip())

	if d.Snap.Up != wasUp || !seen {
		if d.Snap.Up {
			go s.onDaemonUp()
		} else {
			application.InvokeAsync(func() { s.win.SetURL("/") })
		}
		if !seen && !d.Snap.Up {
			go s.firstLaunchStart()
		}
	}

	if d.Snap.Up {
		for _, n := range s.det.Observe(d) {
			if ready && s.prefs.NotifyOn(n.Kind) {
				s.batch.Add(n)
			}
		}
	}
	s.refreshMenu(d, health)
}

// readyLocked: onboarding is finished, so notifications may appear and the
// permission may be requested.
func (s *shell) readyLocked() bool { return s.up && onboardingDone(s.setup, len(s.last.Agents)) }

func (s *shell) refreshMenu(d Data, health *Health) {
	s.mu.Lock()
	svc, svcKnown, appOn, appMsg := s.svc, s.svcKnown, s.appLogOn, s.appLogMsg
	s.mu.Unlock()
	application.InvokeAsync(func() {
		if !d.Snap.Up {
			s.it.inbox.SetHidden(true)
			s.it.agents.SetHidden(true)
			s.it.fed.SetHidden(true)
			s.it.status.SetLabel("lolad is not running")
		} else {
			s.it.inbox.SetHidden(false).SetLabel(d.Snap.InboxLabel())
			s.it.agents.SetHidden(false).SetLabel(d.Snap.AgentsLabel())
			s.it.fed.SetHidden(d.Snap.Pending == 0).SetLabel(plural(d.Snap.Pending, "sender awaiting approval", "senders awaiting approval"))
			s.it.status.SetLabel(connectedBanner(health))
		}
		s.it.switchTo.SetHidden(!(d.Snap.Up && health != nil && health.Install == "cli"))
		s.it.start.SetHidden(d.Snap.Up)
		s.it.stop.SetHidden(!d.Snap.Up)
		s.it.svcLogin.SetChecked(svcKnown && svc.Installed)
		s.it.appLogin.SetChecked(appOn)
		if appMsg != "" {
			s.it.appLogin.SetLabel("Open Lolad at login (" + appMsg + ")")
		} else {
			s.it.appLogin.SetLabel("Open Lolad at login")
		}
		s.menu.Update()
	})
}

// onDaemonUp runs each time the daemon comes up: load the window, read its
// identity, handle first run and migration.
func (s *shell) onDaemonUp() {
	s.refreshMeta()
	application.InvokeAsync(func() { s.win.SetURL(s.base + "/") })

	s.mu.Lock()
	agents := len(s.last.Agents)
	setup := s.setup
	done := s.targeted
	s.targeted = true
	s.mu.Unlock()
	if !done {
		target := firstRunTarget(setup, agents, pageExists(s.base, "/welcome"))
		if target != "" && target != "/" {
			application.InvokeAsync(func() { s.win.SetURL(s.base + target) })
			if !s.background {
				s.showMain()
			}
		}
	}
	s.afterOnboarding()
	s.mu.Lock()
	h := s.health
	offered := s.offered
	s.offered = true
	s.mu.Unlock()
	if h != nil && h.Install == "cli" && !offered && s.prefs.Get().MigrationDecl != migrationKey(h) && !s.background {
		s.offerMigration(false)
	}
}

// refreshMeta re-reads what changes rarely: daemon identity, setup state and
// both login toggles.
func (s *shell) refreshMeta() {
	h := fetchHealth(s.base)
	su := fetchSetup(s.base)
	on, msg := appLoginStatus()
	svc, err := serviceStatus()
	s.mu.Lock()
	s.health, s.setup = h, su
	s.svc, s.svcKnown = svc, err == nil
	s.appLogOn, s.appLogMsg = on, msg
	d := s.last
	s.mu.Unlock()
	s.refreshMenu(d, h)
}

func (s *shell) metaLoop(ctx context.Context) {
	t := time.NewTicker(45 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			up := s.up
			s.mu.Unlock()
			if up {
				s.refreshMeta()
				s.afterOnboarding()
			}
		}
	}
}

// afterOnboarding runs once setup is finished: ask for notification
// permission, and switch both login items on (docs/desktop.md §5: default on).
// Existing installs run by another manager (install "cli") and daemons too old
// to say are left alone: the user can still use the toggles.
func (s *shell) afterOnboarding() {
	s.mu.Lock()
	ready, h := s.readyLocked(), s.health
	s.mu.Unlock()
	if !ready {
		return
	}
	p := s.prefs.Get()
	if !p.NotifyAsked && s.notif != nil {
		_ = s.prefs.Update(func(p *Prefs) { p.NotifyAsked = true })
		if ok, err := s.notif.RequestNotificationAuthorization(); err != nil {
			log.Printf("notification permission: %v", err)
		} else if !ok {
			log.Printf("notification permission denied")
		}
	}
	if !p.DefaultsApplied && h != nil && h.Install == "app" {
		_ = s.prefs.Update(func(p *Prefs) { p.DefaultsApplied = true })
		if err := setAppLogin(true); err != nil {
			log.Printf("app login item: %v", err)
		}
		if err := serviceInstall(); err != nil {
			log.Printf("service install: %v", err)
		}
		s.refreshMeta()
	}
}

// ---------------------------------------------------------------- notifications

func (s *shell) setupNotifications() {
	if s.notif == nil {
		return
	}
	s.notif.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			return
		}
		link, _ := r.Response.UserInfo["link"].(string)
		s.mu.Lock()
		if link == "" {
			link = s.linkByID[r.Response.ID]
		}
		s.mu.Unlock()
		if link == "" || !strings.HasPrefix(link, "/") {
			link = "/"
		}
		s.openLink(link)
	})
}

func (s *shell) deliver(n Note) {
	if s.notif == nil {
		return
	}
	s.mu.Lock()
	s.linkByID[n.ID] = n.Link
	if len(s.linkByID) > 200 {
		s.linkByID = map[string]string{n.ID: n.Link}
	}
	s.mu.Unlock()
	err := s.notif.SendNotification(notifications.NotificationOptions{
		ID: n.ID, Title: n.Title, Body: n.Body, ThreadID: n.Kind,
		Data: map[string]interface{}{"link": n.Link},
	})
	if err != nil {
		log.Printf("notify: %v", err)
	}
}

// ---------------------------------------------------------------- actions

func (s *shell) fail(title string, err error) {
	application.InvokeAsync(func() {
		s.app.Dialog.Error().SetTitle(title).SetMessage(err.Error()).Show()
	})
}

func (s *shell) info(title, msg string) {
	application.InvokeAsync(func() { s.app.Dialog.Info().SetTitle(title).SetMessage(msg).Show() })
}

// firstLaunchStart starts the daemon when none answered at launch. A login
// service may be starting it right now, so wait a moment first.
func (s *shell) firstLaunchStart() {
	time.Sleep(4 * time.Second)
	s.mu.Lock()
	up := s.up
	s.mu.Unlock()
	if !up {
		s.startLolad()
	}
}

func (s *shell) startLolad() {
	s.mu.Lock()
	managed := s.svcKnown && s.svc.Installed
	s.mu.Unlock()
	var err error
	if managed {
		if err = serviceStart(); err != nil {
			err = startDaemon() // fall back to a plain detached start
		}
	} else {
		err = startDaemon()
	}
	if err != nil {
		s.fail("Could not start lolad", err)
	}
}

func (s *shell) stopLolad() {
	if err := serviceStop(); err != nil {
		s.fail("Could not stop lolad", fmt.Errorf("%v\n\nIf you started lolad yourself (PM2, a terminal), stop it there. Agents keep running either way.", err))
	}
}

func (s *shell) toggleServiceLogin() {
	s.mu.Lock()
	on := s.svcKnown && s.svc.Installed
	s.mu.Unlock()
	var err error
	if on {
		err = serviceUninstall()
	} else {
		err = serviceInstall()
	}
	if err != nil {
		s.fail("Could not change \"Start lolad at login\"", err)
	}
	s.refreshMeta()
}

func (s *shell) toggleAppLogin() {
	s.mu.Lock()
	on := s.appLogOn
	s.mu.Unlock()
	if err := setAppLogin(!on); err != nil {
		s.fail("Could not change \"Open Lolad at login\"", err)
	}
	s.refreshMeta()
	s.mu.Lock()
	msg := s.appLogMsg
	s.mu.Unlock()
	if strings.Contains(msg, "approval") {
		application.InvokeAsync(func() {
			d := s.app.Dialog.Question().SetTitle("Approve in System Settings").
				SetMessage("macOS needs you to allow Lolad under General > Login Items.")
			open := d.AddButton("Open System Settings")
			open.OnClick(openLoginSettings)
			d.AddButton("Not now")
			d.SetDefaultButton(open)
			d.Show()
		})
	}
}

func (s *shell) viewLogs() {
	dir := filepath.Join(stateDir(), "logs")
	s.mu.Lock()
	if s.health != nil && s.health.StateDir != "" {
		dir = filepath.Join(s.health.StateDir, "logs")
	}
	s.mu.Unlock()
	_ = os.MkdirAll(dir, 0o700)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		return
	}
	hideWindow(cmd)
	_ = cmd.Start()
}

// migrationKey identifies one foreign daemon, so declining is remembered per
// install and not forever.
func migrationKey(h *Health) string { return h.Exe + "@" + h.Version }

// offerMigration implements docs/desktop.md §12. The app shows the commands
// that stop the old daemon and never runs them: another manager owns it.
func (s *shell) offerMigration(manual bool) {
	s.mu.Lock()
	h := s.health
	s.mu.Unlock()
	if h == nil || h.Install != "cli" {
		return
	}
	var labels []string
	if runtime.GOOS == "darwin" {
		labels = launchdLabels()
	}
	text := migrationText(h, labels)
	application.InvokeAsync(func() {
		d := s.app.Dialog.Question().SetTitle("Switch to the app's lolad?").SetMessage(text)
		cont := d.AddButton("Continue")
		cont.OnClick(func() { go s.finishMigration(h) })
		keep := d.AddButton("Keep")
		keep.OnClick(func() { _ = s.prefs.Update(func(p *Prefs) { p.MigrationDecl = migrationKey(h) }) })
		d.SetDefaultButton(keep).SetCancelButton(keep)
		d.Show()
	})
}

// finishMigration waits for the old daemon to be gone, then registers the
// app's own. Agents are adopted because the config, state dir and tmux socket
// are shared.
func (s *shell) finishMigration(old *Health) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(s.base + "/") {
			break
		}
		time.Sleep(time.Second)
	}
	if alive(s.base + "/") {
		s.info("lolad is still running", "The old lolad still answers. Run the command that matches how you started it, then choose \"Switch to the app's lolad…\" from the menu again.")
		return
	}
	if err := serviceInstall(); err != nil {
		s.fail("Could not register the app's lolad", err)
		return
	}
	s.startLolad()
	s.info("Switched", "The app now runs lolad. Your agents were adopted unchanged.")
}

// uninstall implements docs/desktop.md §11 (macOS): unregister the login
// items, optionally stop the agents, move the app to the Trash, keep the data.
func (s *shell) uninstall() {
	exe, _ := os.Executable()
	bundle := bundlePath(exe)
	application.InvokeAsync(func() {
		d := s.app.Dialog.Question().SetTitle("Uninstall Lolad?").SetMessage(
			"This turns off \"Start lolad at login\" and \"Open Lolad at login\" and moves the Lolad app to the Trash.\n\n" +
				"Your data stays where it is: ~/.config/lolad (settings), ~/.local/share/lolad (mail and logs) and your agents folder (usually ~/Lolad Agents).")
		yes := d.AddButton("Uninstall")
		yes.OnClick(func() { go s.askStopAgents(bundle) })
		no := d.AddButton("Cancel")
		d.SetDefaultButton(no).SetCancelButton(no)
		d.Show()
	})
}

func (s *shell) askStopAgents(bundle string) {
	s.mu.Lock()
	var running []string
	for _, a := range s.last.Agents {
		if a.State != "stopped" && a.State != "" {
			running = append(running, a.Name)
		}
	}
	s.mu.Unlock()
	if len(running) == 0 {
		s.doUninstall(bundle, nil)
		return
	}
	application.InvokeAsync(func() {
		d := s.app.Dialog.Question().SetTitle("Stop your agents too?").SetMessage(
			fmt.Sprintf("%s still running. They keep running after the app is gone unless you stop them now.", plural(len(running), "agent is", "agents are")))
		keep := d.AddButton("Keep agents running")
		keep.OnClick(func() { go s.doUninstall(bundle, nil) })
		stop := d.AddButton("Stop agents")
		stop.OnClick(func() { go s.doUninstall(bundle, running) })
		d.SetDefaultButton(keep)
		d.Show()
	})
}

func (s *shell) doUninstall(bundle string, stopAgents []string) {
	var report []string
	for _, name := range stopAgents {
		req, _ := http.NewRequest(http.MethodPost, s.base+"/api/agents/"+neturl.PathEscape(name)+"/stop", nil)
		if resp, err := plainClient.Do(req); err != nil {
			report = append(report, "Could not stop "+name+": "+err.Error())
		} else {
			resp.Body.Close()
		}
	}
	if err := serviceUninstall(); err != nil {
		report = append(report, "Login service: "+err.Error())
	}
	if err := setAppLogin(false); err != nil {
		report = append(report, "Login item: "+err.Error())
	}
	if bundle == "" {
		report = append(report, "This copy is not running from a Lolad.app bundle, so there is nothing to move to the Trash.")
	} else if err := moveToTrash(bundle); err != nil {
		report = append(report, "Trash: "+err.Error())
	} else {
		report = append(report, "Moved "+bundle+" to the Trash.")
	}
	report = append(report, "Your data was kept: ~/.config/lolad, ~/.local/share/lolad and your agents folder.")
	application.InvokeAsync(func() {
		d := s.app.Dialog.Info().SetTitle("Lolad was uninstalled").SetMessage(strings.Join(report, "\n\n"))
		ok := d.AddButton("Quit")
		ok.OnClick(func() { s.app.Quit() })
		d.SetDefaultButton(ok)
		d.Show()
	})
}

// bundlePath returns the .app directory containing exe, or "".
func bundlePath(exe string) string {
	const marker = ".app/Contents/MacOS/"
	if i := strings.Index(exe, marker); i >= 0 {
		return exe[:i+len(".app")]
	}
	return ""
}
