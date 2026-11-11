# Lolad desktop (MVP)

A native window, menu bar / tray icon and single-instance guard around the
dashboard the lolad daemon already serves. It replaces the browser tab: the UI
is the same page. Design and full plan: [`docs/desktop.md`](../docs/desktop.md).

This is its own Go module ([Wails v3.1.0](https://github.com/wailsapp/wails),
cgo on macOS). It does not import lolad's `internal/` packages, and the root
module never depends on it.

## What it does

- Connects to the daemon at `listen:` from `~/.config/lolad/config.yaml`
  (default `127.0.0.1:4100`). `LOLAD_URL` / `LOLAD_CONFIG` override it.
- If nothing answers at launch (after a short grace period, in case the login
  service is starting it) it starts the daemon: `lolad service start` when a
  login service is installed, otherwise `lolad serve` detached with a minimal
  environment (no tokens from the app's own environment reach the daemon or
  its agents).
- Shows a "Waiting for lolad" page while the daemon is down and connects by
  itself when it comes up.
- Closing the window (button or ⌘W) hides it. **Quit** is ⌘Q or the tray menu.
  Quitting never stops the daemon or any agent. Second launch focuses the window.
- macOS: the Dock icon exists only while the window is open (activation
  policy switches between regular and accessory, `macos_darwin.m`).
  `--background` starts tray only with no window. The window frame is saved in
  the app's prefs file and restored if it is still on a screen.
- Attachment **download** saves into `~/Downloads` (never overwriting) and
  reveals the file; it is never opened. **Open full size** and other external
  links open in the default browser (`bridge.go`).

### Tray / menu bar (`shell.go`, `feed.go`, `state.go`)

The app subscribes to the loopback `GET /api/stream` (reconnect with bounded
backoff, 1 s doubling to 30 s) and, on every event, re-reads `/api/agents`,
`/api/mail/conversations` and `/api/connections` (older daemons without the
last one are fine). It also refreshes every 5 s as a safety net. From that:

| Icon | When |
|---|---|
| normal | nothing needs you |
| needs you (dot) | an unanswered `ask`, a `waiting` agent, or a federation sender awaiting approval |
| problem (badge with `!`) | an agent is `failed`/`crashed`, or the daemon is down |

Menu: Open Lolad; "N questions waiting" (opens the inbox); "Agents: 5 running,
1 failed" (opens Agents); "N senders awaiting approval" (opens Connections,
only when > 0); the status line; Start/Stop lolad; **Start lolad at login**;
**Open Lolad at login**; Notifications (per-kind toggles); Open in Browser;
View logs (reveals `<state>/logs`); Reload; Uninstall… (macOS); Quit.

Not in the menu yet: **Pause/Resume all agents** (the daemon API has no such
endpoint) and **Check for updates…** (the updater is wired separately).

### Notifications

Native, through Wails' notifications service, for: a new `ask`, an agent
entering `failed`, a federation sender awaiting approval. Clicking one opens the
window at the matching dashboard page (`/inbox/<peer>`, `/agents/<name>`,
`/connections`). Bursts inside 2 s collapse into one summary and at most 12
are delivered per hour. Items that already exist when the app starts are never
announced. The permission is requested only after onboarding is done
(`GET /api/setup` says `done`, or the endpoint is missing and agents already
exist). Per-kind toggles are stored in the app's prefs file until the daemon's
Settings own them:

- macOS `~/Library/Application Support/Lolad/desktop.json`
- Windows `%AppData%\Lolad\desktop.json` (`LOLAD_DESKTOP_CONFIG` overrides the dir)

### Login items

- **Daemon** ("Start lolad at login"): calls `lolad service install|uninstall|
  status --json|start|stop` (contract: docs/desktop.md §5/§8, status JSON
  `{installed,running,mechanism,path}`). The CLI is found at `LOLAD_BIN`,
  `Contents/Helpers/lolad` (macOS bundle), `bin\lolad.exe` (Windows), then the
  usual install dirs. The app binary itself is never mistaken for the CLI on
  case-insensitive disks.
- **App** ("Open Lolad at login"): macOS `SMAppService.mainApp` (13+, from the
  installed app only); Windows `HKCU\...\Run\Lolad` =
  `"<Lolad.exe>" --background`.
- Both default to on once onboarding is done, but only when `/api/health` says
  `install: "app"`. Existing installs managed elsewhere are never changed
  behind the user's back.

### First run and existing installs

- If `/api/setup` says `done:false` the window opens `/welcome` (when the
  dashboard serves it, else the root). A daemon without `/api/setup` that
  already has agents is treated as set up.
- `/api/health` with `install:"cli"` shows "Connected to lolad <version> at
  <path>" in the menu and offers **Switch to the app's lolad**: a dialog with
  the exact commands that stop PM2 / launchd (`pm2 stop lolad && pm2 save`,
  `launchctl bootout gui/$(id -u)/<label>` with labels found by reading
  `launchctl list`). The app never runs them or edits another manager's
  config; after the user confirms it waits for the old daemon to stop, then
  runs `lolad service install`. Declining is remembered per install.

### Uninstall (macOS)

Tray ▸ Uninstall…: `lolad service uninstall`, turns off the app login item,
optionally stops the running agents (`POST /api/agents/<n>/stop`), moves
`Lolad.app` to the Trash and says where the kept data is
(`~/.config/lolad`, `~/.local/share/lolad`, the agents folder).

## Build and run

macOS 13+:

```sh
cd desktop
./build.sh              # builds desktop/Lolad.app (ad-hoc signed, with Lolad.icns)
./build.sh --install    # also copies it to /Applications
go vet ./... && go test ./...
```

Windows (GUI subsystem, so no console window opens; not run by the author):

```sh
cd desktop
GOOS=windows go vet ./...
GOOS=windows go build -trimpath -ldflags "-s -w -H windowsgui" -o Lolad.exe .
```

Every child process the app starts uses `CREATE_NO_WINDOW` (`hideWindow`,
`detach`). `go mod tidy` is fine here (this is its own module); never in the
repository root.

To run a second copy beside an installed one, set
`LOLAD_DESKTOP_INSTANCE=.test` (changes the single-instance id) and
`LOLAD_DESKTOP_CONFIG=<dir>` (separate prefs).

## Icons (`assets/icons/`)

`go run gen.go` (in that directory) draws everything from code, no outside
art: the mark is the lowercase word reduced to "l o l". It writes
`app-1024.png`, `Lolad.icns` (via `iconutil`), `Lolad.ico`, macOS template tray
icons `tray-mac-{normal,attention,problem}.png` and Windows
`tray-win-{light,dark}-*.png`.

## Release builds

```sh
desktop/build.sh --release                 # macOS: dist/Lolad.app, Lolad.app.zip, Lolad.dmg (universal, bundled tmux + lolabot)
desktop/packaging/windows/build.sh         # Windows: dist/windows/Lolad-Setup.exe (NSIS needs makensis)
```

Both stamp the updater with `VERSION` and `UPDATE_PUBLIC_KEY` (base64 Ed25519
public key from `sign-manifest keygen`). Without the key the updater refuses to
run. Signing and notarization happen only when the `APPLE_*` / `AZURE_*`
secrets are present, otherwise builds are ad-hoc signed (see
[docs/install.md](../docs/install.md) for the first-open steps).
`.github/workflows/release.yml` runs all of it on a `v*` tag.

## Layout

| Path | What |
|---|---|
| `main.go`, `shell.go`, `feed.go`, `state.go` | window, tray, live state from `/api/stream` |
| `notify_logic.go`, `prefs.go` | notifications and their per-kind toggles |
| `service.go`, `setup.go`, `login_*.go`, `macos_darwin.*` | login items, first run, migration, uninstall |
| `update_menu.go`, `updater/`, `tools/sign-manifest/` | signed self-update |
| `packaging/` | macOS bundle, Windows installer, Homebrew and winget templates |
| `assets/icons/` | generated icons (`go run ./assets/icons`) |

## Not verified yet

- Nothing has run on Windows (tray, login items, installer, updater); it
  compiles, vets and the installer builds.
- Signing, notarization and the release workflow have never run for real: no
  Apple Developer ID or Windows signing certificate has been configured.
- Login-item launch, notification clicks and the uninstall and migration
  dialogs need a real session to confirm.
