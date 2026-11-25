# lolad desktop app — packaging spec

Status: spec drafted 2026-11-10. An MVP of the window shell (§6.1, §6.2, §6.3
partly, §6.5) exists in [`desktop/`](../desktop/README.md), macOS only.
[ROADMAP.md](../ROADMAP.md) tracks progress.

### MVP findings (2026-11-11, Wails v3.1.0, macOS 26, WKWebView)

- **Wails v3.1.0 is a tagged stable release.** It is the pinned version.
- The window loads `http://127.0.0.1:<port>/` and the dashboard works as in a
  browser, including live updates (SSE, the sidebar shows "Live"). The
  WebSocket terminal has not been exercised inside the window yet (S2).
- **`WebviewWindow.ExecJS` does not work on a remote page.** It queues until the
  Wails runtime reports ready, and a page we don't serve never does. Use the
  window option `JS` instead: it runs after every navigation finish.
- **Downloads and `target=_blank` links do nothing in the webview** (no download
  manager, no popup handler). The MVP bridges them: injected JS posts a line to
  `RawMessageHandler`, which receives the page's origin. Go accepts it only from
  the daemon's origin, fetches the file itself, sanitizes the name, never
  overwrites, and reveals instead of opening. Verified end to end.
- The native **file picker** (`<input type=file>`) is implemented by Wails
  (`runOpenPanelWithParameters`). Not yet exercised with a real upload.
- Wails v3.1.0 has no navigation-policy hook. The window can therefore still
  navigate to an external site if the page does so itself; the bridge only
  covers links clicked in the dashboard.

## 1. Goal

Anyone on **macOS** or **Windows** downloads one file, clicks through one
install, and minutes later has agents running. lolad then starts at login on
its own, with or without the app window open, and updates itself.

There is **one UI**: the dashboard lolad already serves. It looks the same in
a browser, in the macOS app and in the Windows app, because it is literally
the same page served by the same daemon.

### Non-goals

- **Linux is not a packaged target.** The `lolad` binary keeps building and
  testing on Linux (CI, servers, federation peers), but there is no app,
  installer or tray for it.
- No Mac App Store or Microsoft Store. lolad starts arbitrary programs (the
  harnesses) and needs the user's real filesystem, which both sandboxes forbid.
- We don't bundle `claude`, `codex` or `codewhale`. Each has its own license,
  login and updater. The app installs them on request with the vendor's
  official installer.
- The Android client is unaffected. It remains the phone client over the
  documented remote API ([remote.md](remote.md)).

## 2. Decision: one UI, Wails as a shell

The native SwiftUI client (`clients/apple`) is retired (§13). The desktop app is
a **Wails v3** program that provides a native window, a tray / menu bar icon,
notifications, start at login, and updates. Its window shows the daemon's own
dashboard.

### Why the window loads the daemon's URL instead of embedding the UI

Wails can serve pages from Go through its asset server, but we don't use that
for the dashboard:

- The dashboard runs inside the daemon process (`internal/web` calls the
  supervisor, store and mailer directly). Serving it from the app process
  would mean rebuilding it as an API client: a rewrite, and two copies.
- The terminal uses a WebSocket (`static/term.js`) and live updates use
  `EventSource`. Wails' custom asset scheme (`wails://`) does not carry
  WebSockets, and a page on that origin calling `127.0.0.1` would be
  cross-origin, which the daemon's `http.CrossOriginProtection` correctly
  rejects.
- The app must not be required for the daemon: agents run at login without it.

So the app window navigates to `http://127.0.0.1:<port>/`. The page is then
same-origin with the API, so the WebSocket, SSE, uploads and cross-origin
protection all work unchanged. In this design Wails provides the native
pieces (window, tray, notifications, single instance, packaging), not the
page content.

**Pinned version:** one exact Wails v3 release, recorded in `desktop/go.mod`
and only upgraded deliberately. v3's labelling (beta vs 3.0.0) varies by
source; spike S1 confirms that the chosen release has everything in §6.

## 3. Processes

```
 login ──► lolad daemon ─────────────► tmux / psmux server ──► agent harnesses
           (background, always on)      (owns the sessions;     (claude, codex,
           serves dashboard + API        survives daemon         codewhale in
           on 127.0.0.1:<port>)          restarts)               their panes)
                 ▲
                 │ HTTP / SSE / WebSocket, same origin
                 │
 login ──► Lolad app (Wails) ── window: the dashboard
           (optional, tray icon)  tray: state, unread, questions
                                  notifications, updates, onboarding host
```

| Process | Starts | Stops | Owns |
|---|---|---|---|
| **lolad daemon** | at login (§5), or by the app if not running | quit from the tray menu ("Stop lolad"), uninstall, update | agents, mail, policy, the dashboard |
| **tmux / psmux server** | by the daemon | when the last session ends | agent terminals |
| **Lolad app** | by the user, or at login with no window (tray only) | Quit from the tray menu | nothing durable |

Rules:

- **Closing the window never stops agents.** Quitting the app never stops the
  daemon. Stopping the daemon never kills agents, because the daemon already
  adopts surviving sessions on boot. Stopping agents is always an explicit
  action in the dashboard.
- The app is only a client. It holds no state that the daemon doesn't also
  have, and it stores no secrets.
- **The daemon runs as a single instance:** an exclusive lock file in the state
  dir. A second `serve` exits 0 with "already running at <url>". This makes
  "start at login" plus "the app starts it if missing" safe.

## 4. What gets installed

The app and the daemon are separate executables, for two reasons. Wails needs
cgo and WebKit on macOS, while the `lolad` module stays pure Go with no cgo.
And on Windows a GUI program cannot serve as the console CLI that agents run
(`lolad msg …`).

Name collision rule: both filesystems are case-insensitive, so `Lolad` and
`lolad` can never sit in the same directory.

### macOS (`/Applications/Lolad.app`, universal arm64 + x86_64)

```
Lolad.app/Contents/
  MacOS/Lolad                         Wails app
  Helpers/lolad                       daemon + CLI (same binary as today)
  Helpers/tmux                        bundled tmux, pinned version
  Library/LaunchAgents/dev.caceres.lolad.plist
  Resources/lolabot/                  pinned lolabot release
  Resources/tmux.conf, icons, …
```

### Windows 11 (per user, no admin: `%LOCALAPPDATA%\Programs\Lolad\`, amd64; arm64 later)

```
Lolad.exe                 Wails app (GUI subsystem)
bin\lolad.exe             CLI (console subsystem): what agents and users run
bin\lolad-daemon.exe      same Go program built with -H windowsgui: no console window
bin\psmux\…               bundled psmux, pinned version
lolabot\                  pinned lolabot release
uninstall.exe
```

`bin\` is added to the per-user `Path` (HKCU), so `lolad` works in any new
terminal.

### Data (unchanged on both platforms)

Config in `~/.config/lolad` and state in `~/.local/share/lolad`
(`%USERPROFILE%\…` on Windows). Existing installs keep working, and the CLI and
app read the same files. Agent directories default to `~/Lolad Agents` (see
§7.3 for why not Documents).

## 5. Start at login

### macOS: SMAppService LaunchAgent

- The bundle carries `Contents/Library/LaunchAgents/dev.caceres.lolad.plist`
  with `BundleProgram = Contents/Helpers/lolad`,
  `ProgramArguments = [lolad, serve]`, `RunAtLoad = true`,
  `KeepAlive = {SuccessfulExit: false}` (restart on crash, not after a clean
  "Stop lolad"), `ProcessType = Interactive`, and stdout/stderr going to
  `~/.local/share/lolad/logs/daemon.log`.
- The app registers it with `SMAppService.agent(plistName:)` (macOS 13+; a
  small Objective-C file through cgo in `desktop/`). It shows under System
  Settings ▸ General ▸ Login Items as "Lolad", attributed to our Developer ID.
- If the user disabled it in System Settings, the app shows "lolad won't
  start at login" with a button that opens that pane
  (`SMAppService.openSystemSettingsLoginItems()`). It never silently
  re-enables it.
- The app itself registers as a login item (`SMAppService.mainApp`), launched
  with no window, tray only. This is a separate toggle and on by default.
- Minimum macOS: 13.

### Windows: HKCU Run plus a built-in watchdog

- `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\lolad` =
  `"…\bin\lolad-daemon.exe" serve --supervised`. No admin and no UAC prompt,
  and it appears in Task Manager ▸ Startup apps, where the user can see and
  disable it.
- `--supervised` (Windows only) runs `serve` as a child and restarts it on a
  crash, with backoff from 1 s doubling to a 60 s cap. After 10 crashes in 10
  minutes it stops, writes the reason to `daemon.log` and lets the app show
  it. A clean exit is not restarted.
- The app gets its own Run value (`"…\Lolad.exe" --background`, tray only),
  as a separate toggle that is on by default.
- **Rejected alternatives:**
  - A Windows Service runs in session 0 as another account. It can't own the
    user's console or ConPTY sessions, and it sees the wrong profile.
  - A Task Scheduler logon trigger needs admin to create.
  - MSIX virtualizes the filesystem and registry, which breaks spawning
    harnesses and writing to the profile.
- **No console windows:** `lolad-daemon.exe` has no console. Every child
  process it starts (psmux, git, setup steps) uses `CREATE_NO_WINDOW`. This
  belongs in `internal/tmux` and `internal/create`, owned by the runtime and
  create agents.

### Both platforms: `lolad service`

The CLI gets `lolad service install | uninstall | status | start | stop` doing
the same registration, so CLI-only installs (and Homebrew later) get the same
behavior. The app calls these and doesn't reimplement them, except
SMAppService, which only an app bundle can call. On macOS without the bundle,
`service install` writes a plain `~/Library/LaunchAgents` plist and bootstraps
it with `launchctl`.

## 6. The desktop app (Wails shell)

Source lives in `desktop/` with **its own `go.mod`** (like `clients/`), so
`go install github.com/CamiloCaceres/lolad@latest` never pulls Wails or cgo.
The app does not import `internal/…`. It talks to the daemon through the
documented API and the `lolad` CLI.

### 6.1 Finding and starting the daemon

1. Read `listen` from the config (default `127.0.0.1:4100`).
2. `GET /api/health` (new, §8). If it answers and the `product` field is
   lolad, use it.
3. If nothing answers: on first launch run onboarding (§7). Otherwise start
   the registered service, wait up to 10 s, and show "Starting lolad…".
4. If the port is held by something that is not lolad, the daemon handles it
   when it starts (`lolad serve`), whoever started it (the app, launchd or a
   terminal):
   - If the program on the port is a lolad (its `/api/health` says so), the
     new daemon exits and the running one is used: one lolad per port.
   - If it is another program and `config.yaml` has no `listen:` line, the
     daemon takes the first free loopback port from 4101 to 4199, saves it as
     `listen: "127.0.0.1:<port>"` in `config.yaml` (other lines and comments
     are kept; the file is replaced atomically), logs the move in
     `daemon.log`, and serves there. The CLI and agents read the new address
     from the config.
   - If `listen:` is set in `config.yaml`, the user chose that port, so the
     daemon does not move it: it exits with "port N is being used by another
     program…", naming the file to edit and a free port to use.

   The app only counts a real lolad (`/api/health` with `product: lolad`) as
   running. While the daemon does not answer, the app re-reads `listen:`
   about every 5 seconds and switches its window, tray links and live feed to
   the new address when it changes. If another program answers on the
   configured port, the tray status line reads "Another program is using port
   N" instead of "lolad is not running".
5. If the version differs from the app's bundled daemon (for example a
   developer build), show a non-blocking banner: "Connected to lolad
   <version> at <path>".

### 6.2 Window

- One main window. Its frame is remembered, with a minimum of 940×600. It
  loads `http://127.0.0.1:<port>/`.
- **Navigation policy:** only that origin loads in the window. Any other link
  (`target="_blank"`, external hrefs) opens in the default browser. The window
  never shows remote content.
- Light and dark follow the system; the dashboard already themes itself.
- Close (⌘W / the window's close button) hides the window. Quit is ⌘Q on macOS
  or the tray menu. On macOS the Dock icon is visible only while a window is
  open (switching activation policy between regular and accessory).
- A "lolad isn't running" state with Start and View log buttons, instead of a
  browser error page.

**Webview parity.** The page must behave the same as it does in a browser.
Each item below is checked in spike S2 on WKWebView and WebView2. Where the
webview lacks support, the app supplies it natively:

| Feature (where it's used) | Requirement | Fallback |
|---|---|---|
| WebSocket terminal (`term.js`) | works | none needed (same origin) |
| SSE (`EventSource` in `app.js`) | works, reconnects after sleep | — |
| File picker (`<input type=file>` in the inbox) | native open panel | the app implements the webview's open-panel callback |
| Drag and drop files onto the composer (`dataTransfer`) | the HTML5 drop reaches the page | turn off Wails' own file-drop interception for the main window |
| Downloads (`download=` links for attachments) | native save panel with progress | the app intercepts the download URL, fetches it in Go and saves through a save panel |
| Clipboard (`navigator.clipboard`, pasting images) | works | — |
| `localStorage` (terminal and UI preferences) | kept across launches | persistent webview data dir |

### 6.3 Tray / menu bar

The icon reflects the overall state: normal, needs you (an open `ask` or a
`waiting` agent), problem (an agent `failed`, or the daemon is down). Menu:

```
Open Lolad
─────────
3 questions waiting            → opens Inbox
Agents: 5 running, 1 failed    → opens Agents
─────────
Pause all agents / Resume all
Start lolad / Stop lolad
─────────
Start lolad at login      ✓
Open Lolad at login       ✓
Check for updates…
View logs
─────────
Quit Lolad (agents keep running)
```

The data comes from one SSE subscription to the loopback `/api/stream`, the
same stream the dashboard uses.

### 6.4 Notifications

Native notifications (Wails notifications service) for: a new `ask` from an
agent, an agent entering `failed`, a federation sender awaiting approval, and
a downloaded update. Clicking one opens the window at the matching deep URL
(thread, agent, connection). Per-kind toggles live in the dashboard's
Settings and are stored by the daemon. Notifications are rate-limited and
collapse bursts. On macOS the permission is requested after onboarding, not
at first launch.

### 6.5 Single instance

A second launch focuses the existing window, through Wails' single-instance
support.

### 6.6 Running inside the app

The dashboard detects it is running in the app (a `LoladApp/<version>` token
appended to the webview user agent) and only uses that to hide browser-only
hints and to point notification settings at the app. Everything else is
identical.

## 7. First run (onboarding)

Onboarding is **dashboard screens** (`/welcome`), so it is the same in a
browser on a CLI install and in the app. The app opens them on first launch.

### 7.1 Steps

1. **Start at login.** Explain in one sentence, then register the daemon (§5).
   On macOS this triggers the system's "Background item added" notice, which
   the screen anticipates.
2. **Harnesses.** Detect `claude`, `codex` and `codewhale` (§7.2). Each row
   shows: not installed / installed but not signed in / ready. A button runs
   the vendor's official installer or login in an **embedded dashboard
   terminal** (a lolad-managed tmux/psmux session, reusing the existing
   terminal view). The user sees exactly what runs, and interactive logins
   (browser OAuth, device codes) just work. At least one harness must be ready
   to continue.
3. **Agents folder.** Defaults to `~/Lolad Agents`. A folder picker allows
   another location, with a warning for macOS-protected locations (§7.3).
4. **Your name** (pre-filled from the OS account) → `org.yaml` `user`.
5. **First agent.** The existing New agent screen, with defaults chosen
   (name, harness = the first ready one, a starter mission), and Start.
6. **Done.** The inbox with the agent's first message, plus notification
   permission.

Onboarding progress is stored in the state dir, so quitting midway resumes.
`config.yaml` is written by onboarding; nobody edits YAML.

### 7.2 Finding harnesses with a clean environment

launchd gives the daemon a minimal `PATH`, and the Run key gives it
Explorer's. Neither includes shell init files. So:

- Onboarding runs the user's login shell **once** (`$SHELL -lic 'printf %s
  "$PATH"'` on macOS; the registry user and system Path on Windows) and stores
  **only `PATH`** in `config.yaml` (`path:`). No other variable is ever
  captured. This preserves the rule that no session tokens leak into agents.
- It also stores absolute `claude_bin`, `codex_bin` and `codewhale_bin`
  values when found.
- Well-known locations are probed too: `~/.local/bin`, `/opt/homebrew/bin`,
  `/usr/local/bin`, `%LOCALAPPDATA%\Programs`, the npm global prefix. Codex
  installed through npm needs `node` on `PATH`; detection checks for that.
- "Re-scan" in Settings repeats the detection.

### 7.3 macOS privacy (TCC)

Agents are started by our LaunchAgent, so access to Desktop, Documents,
Downloads, iCloud Drive and removable volumes prompts in **Lolad's** name, and
nobody is there to answer for an unattended agent. Therefore:

- The default agents folder is outside protected locations.
- Choosing a protected location shows a warning and offers to open
  Privacy ▸ Files and Folders / Full Disk Access.
- `lolad doctor` reports agent directories under protected locations.
- When macOS refuses an agent's folder, `.env` or settings ("operation not
  permitted", as after a reboot or an update took the permission away), the
  supervisor does not count a crash. The agent shows the problem
  `macos_permission` ("macOS is blocking access to “DevSSD”", needs you, with
  the Files & Folders switch to turn on) and is tried again every 15 s, with no
  backoff and no crash budget, so it is back within seconds of Allow. The app
  sends one notification for all blocked agents (the same one as the startup
  notice, §8).

### 7.4 lolabot without git or Bash

- A pinned lolabot release ships in the bundle (`Resources/lolabot`, or
  `lolabot\` on Windows). On first use it is copied to
  `~/.local/share/lolad/lolabot/<version>/`, and `lolabot_template` defaults
  to that path. A user-set `lolabot_template` still wins.
- `setup.sh` is ported to Go in `internal/create` (owned by the create agent).
  Bash is then not required on any platform, which is mandatory for Windows
  one-click. Until upstream adopts the Go port, a conformance test runs both
  against the pinned lolabot and compares the resulting instance trees.
- Claude Code hooks on Windows invoke `lolad.exe hook …` directly with no
  shell, so Git Bash is not needed for hooks either. Verify this against the
  Claude Code version pinned in CI.

## 8. Daemon changes this needs

These are owned by the agents listed in `CLAUDE.md`. The coordinator updates
`SPEC.md` and `internal/core` first.

| Change | Package |
|---|---|
| `GET /api/health` → `{product:"lolad", version, pid, started_at, exe, install:"app"\|"cli", state_dir, state, waiting_for, drive}`, cheap and no auth on loopback. It answers from the moment the port opens: `state` is `starting` while the daemon reads its agent folders, `waiting_for_macos_permission` (with the folder and its `drive`) when a read has been held for 5 s by a macOS privacy dialog, then `ready`. Until ready, other `/api/` paths answer 503 and the dashboard shows a page that says what to click and reloads itself. A missing `state` (older daemons) means ready. A daemon that is up also reports `waiting_for_macos_permission` (folder and `drive`) when macOS refused an agent folder outright ("operation not permitted"): the rest works, those agents are kept but skipped, and the folder is read again every 15 s until access is allowed. The tray reads `/api/health` on each refresh while the daemon is up: its status line then says "macOS is blocking “drive”: some assistants are paused" and one notification (opening Agents) says how to allow access, until health is `ready` again. The tray shows the state, and sends one notification while macOS is asking; for older daemons it does the same after 30 s of silence on the port. | api, cli |
| Single-instance lock on `serve` | app / cli |
| `lolad service install\|uninstall\|status\|start\|stop` | cli (+ small per-OS file) |
| `serve --supervised` watchdog (Windows) | cli |
| `cli_bin` config: the console CLI path used for `LOLAD_BIN`, the hook command and agents' `PATH` (on Windows the daemon executable is not the CLI) | core, harness, hooks |
| `path:` and `*_bin` written by onboarding; harness discovery uses them | registry, harness |
| `CREATE_NO_WINDOW` for every child process on Windows | tmux, create, harness |
| Bundled tmux / psmux path config (`tmux_bin` already exists), a private `TMUX_TMPDIR` under the state dir so the daemon, CLI and `lolad attach` always find the same socket, and a lolad tmux.conf with `default-terminal screen-256color` (a terminfo macOS ships) | tmux |
| `lolad doctor --json`, including harness, login, TCC and port checks | cli |
| Onboarding screens and their API (`/welcome`, `GET/POST /api/setup`) | web, api |
| Onboarding terminal: run a named install or login command in a throwaway session | supervisor, web |
| Notification preferences per kind | web, store |
| Log files under `<state>/logs/` with rotation | app |
| Go port of `setup.sh`, bundled-lolabot default | create |

## 9. Updates

One updater for both platforms, written in Go in `desktop/`. Sparkle and
WinSparkle are not used: they would be two systems for the same job, and
neither updates the daemon.

- **Manifest:** `https://github.com/CamiloCaceres/lolad/releases/latest/download/update.json`,
  listing version, notes URL, and per-platform artifact URL + SHA-256. The
  manifest is signed with **Ed25519**; the public key is compiled into the app.
  An unsigned or mismatched manifest is ignored.
- Checks once a day and on "Check for updates…". Downloads in the background,
  then notifies "Restart to update". Never updates on its own while the user
  is mid-onboarding.
- **macOS apply:** download the zipped `.app`, verify the hash, then verify
  `codesign --verify --deep --strict` and that the Team ID matches. Replace
  `/Applications/Lolad.app` (rename the old one aside, move the new one in,
  delete the old one), then `launchctl kickstart -k` the agent and relaunch
  the app.
- **Windows apply:** download the new installer, verify the hash and
  Authenticode signature, run it with `/S` (silent, per user). The installer
  stops the daemon and app, replaces files, and starts both.
- **Agents keep running across updates,** because the tmux / psmux server
  holds the sessions and the new daemon adopts them. **Exception:** if the
  bundled tmux or psmux version changes, the new client may refuse to talk to
  the old server ("protocol version mismatch"). Each release declares
  `multiplexer_version`. When it changes, the update notice says agents will
  restart, and the daemon performs a graceful stop and resume (`--continue`)
  of each agent after updating.
- Downgrades are refused except through a manual reinstall. Config and state
  migrations (roadmap, Toward 1.0) run on daemon start and are forward-only.

## 10. Building, signing, distributing

GitHub Actions on a version tag:

| Job | Runner | Produces |
|---|---|---|
| `lolad` CLI and daemon | ubuntu (cross, no cgo) | darwin arm64/amd64 (→ `lipo` universal), windows amd64 (+ `-H windowsgui` daemon), linux (CLI only, not packaged) |
| tmux | macos | universal tmux, built from the pinned source with libevent linked statically and system ncurses |
| Mac app | macos | Wails build → `Lolad.app` → sign every Mach-O (hardened runtime, Developer ID Application) → notarize (`notarytool`) → staple → `Lolad.dmg` + `Lolad.app.zip` (for updates) |
| Windows app | windows | Wails build → sign every `.exe` we build (Azure Trusted Signing) → NSIS per-user installer (Wails' NSIS template, customized) → signed `Lolad-Setup.exe` |
| Release | ubuntu | `update.json` signed with Ed25519, checksums, GitHub release |

Distribution:

- **macOS:** `Lolad.dmg` from the website / GitHub releases. A Homebrew cask
  (`brew install --cask lolad`) points at the same DMG.
- **Windows:** `Lolad-Setup.exe`. The winget manifest points at the same file.

### Mac signing without an Apple Developer account

Until there is a Developer ID, releases built on the release Mac
(`bin/release-local.sh`) are signed with a self-signed certificate,
**Lolad Local Signing**, instead of ad hoc.

- **What it fixes.** An ad-hoc signature is a hash of the binary, so to macOS
  privacy controls (TCC) every build is a new program, and each update asked
  again whether Lolad may use the drive or folder the agents live in. The
  daemon waited on that dialog until someone answered (§8, health state
  `waiting_for_macos_permission`). A certificate signature with a fixed
  identifier has the same designated requirement in every build:
  `identifier "dev.caceres.lolad" and certificate root = H"cc19…ef85"` for the
  daemon (`Helpers/lolad`), `dev.caceres.lolad.desktop` for the app,
  `dev.caceres.lolad.tmux` for tmux. A permission given once survives updates.
  The first install signed this way still asks once, since its identity
  differs from the ad-hoc builds before it.
- **What it does not fix.** Gatekeeper trusts only Apple-issued certificates,
  so on a new Mac a downloaded Lolad still needs System Settings ▸ Privacy &
  Security ▸ "Open Anyway" (or `xattr -dr com.apple.quarantine`). That needs a
  Developer ID and notarization (the `APPLE_*` variables of `bundle.sh`).
  The DMG is not signed with it.
- **Where it lives.** A dedicated keychain,
  `~/Library/Keychains/lolad-signing.keychain-db`, unlocked from
  `~/.local/share/lolad-macos-signing/keychain.password`, so a build never shows
  a keychain prompt. `desktop/packaging/macos/local-signing.sh` finds it,
  unlocks it and adds it to the keychain search list. The `.p12` backup and its
  password are kept with the release keys, outside the repo; use that same
  certificate on any other build machine, since a new certificate changes the
  requirement and macOS asks for every permission once more.
- **Fallback.** `bundle.sh` without the identity (CI, other machines) signs ad
  hoc and warns. `bin/release-local.sh` stops before building unless the
  identity is there, `APPLE_SIGN_IDENTITY` is set, or `--adhoc` is passed.
  Every binary carries its fixed identifier in all three modes.
- **Updates.** The updater treats a signature without a Team ID like an ad-hoc
  one (§9): it accepts any intact bundle, so ad-hoc installs update to locally
  signed builds, and those can update to a later Developer ID release.

Secrets (Apple ID notarization key, Developer ID certificate, Trusted Signing
credentials, Ed25519 update key) live only in GitHub Actions secrets, scoped
to the release workflow. The update key's offline backup procedure is
documented in `SECURITY.md`.

## 11. Uninstall

- **macOS:** Lolad ▸ "Uninstall…" in the tray menu unregisters the
  LaunchAgent and login item, optionally stops all agents, and moves the app to
  the Trash. Dragging the app to the Trash alone leaves an orphaned login
  entry, which macOS removes on its own once the bundle is gone. Data in
  `~/.config/lolad`, `~/.local/share/lolad` and the agents folder is **kept**;
  the dialog says where it is.
- **Windows:** Settings ▸ Apps ▸ Lolad ▸ Uninstall runs `uninstall.exe`. It
  stops the app and daemon, asks whether to stop the agents, and removes the
  Run values, the `Path` entry and the program files. Data is kept unless
  "Also delete my agents and data" is ticked (unticked by default, and
  confirmed a second time).

## 12. Migrating existing installs

People already running lolad from `go install`, PM2, launchd or a service:

- On first launch the app finds a running daemon through `/api/health`. If
  its `install` is `cli`, it offers **"Switch to the app's lolad"**:
  1. Show what will happen.
  2. Stop the old daemon. The user stops their PM2 / launchd entry; the app
     shows the exact command, because it never edits another service manager's
     config.
  3. Register the app's daemon. Agents are adopted unchanged, since the same
     state dir, config and tmux socket are used.
- Declining keeps the app as a pure client of the existing daemon (the §6.1
  banner).
- Existing config keys win over onboarding defaults; onboarding is skipped
  when agents already exist.
- The tmux socket must stay `-L lolad`. The new `TMUX_TMPDIR` (§8) falls back
  to the old default location when sessions already exist there.

## 13. Repo changes (when the work starts)

- New top-level `desktop/` with its own module, build scripts, `README.md` and
  `DESIGN.md`, owned by a new **desktop** agent (add it to the table in
  `CLAUDE.md` and `AGENTS.md`).
- `clients/apple` is retired: move it to the private archive, and remove it
  from `clients/README.md` and the README.
- `clients/README.md`: replace "Native per platform" with "One desktop UI: the
  dashboard in the Wails app. Phone clients stay native." Android remains.
- `README.md` Install section: download the app first, with the CLI install
  as the second option.
- `SPEC.md`: new sections for health, service, single instance, `cli_bin`,
  onboarding API.

## 14. Spikes (before building)

Each spike is a throwaway branch with a written result appended to this file.

| # | Question | Pass condition |
|---|---|---|
| S1 | Pinned Wails v3: a window on an `http://127.0.0.1` URL, tray, notifications, single instance, a navigation policy hook, a custom user agent | all work on macOS 13+ and Windows 11 |
| S2 | Webview parity table (§6.2) against the real dashboard | every row works or has a working fallback |
| S3 | Relocatable universal tmux in the bundle: signed and notarized, starts agents, `lolad attach` works from Terminal | an agent runs from a fresh Mac with no Homebrew |
| S4 | psmux: confirm its license allows bundling, and that it runs from `%LOCALAPPDATA%` with no install step | an agent runs on a fresh Windows 11 VM |
| S5 | SMAppService LaunchAgent with `BundleProgram`, called from the Wails app | the daemon is up after reboot without opening the app; it appears in Login Items |
| S6 | HKCU Run + `lolad-daemon.exe --supervised`, with no console window flashes, including psmux and harness children | the daemon is up after reboot; no windows appear |
| S7 | Harness install and login inside the dashboard terminal: Claude Code's official installer and login, Codex install (`npm` or standalone) and `codex login` | each reaches "ready" with no outside terminal |
| S8 | TCC behavior for an agent under `~/Documents` started by the LaunchAgent | the behavior is documented, and §7.3's warnings are confirmed or adjusted |
| S9 | Microsoft Defender / SmartScreen on a fresh VM with a signed installer | installs without a block screen |

## 15. Acceptance

- **Fresh Mac** (macOS 13+, no Homebrew, no Go, no tmux): download the DMG,
  drag to Applications, open. Onboarding → a harness signed in → first agent
  replying in the inbox **within 5 minutes**, with at most one admin prompt
  (none expected).
- **Fresh Windows 11** VM, standard user, no Git, no Go, no psmux: run
  `Lolad-Setup.exe` → **no UAC prompt** → the same outcome in the same time.
- **Reboot** both machines without opening the app: the daemon is up, agents
  run, and the tray icon appears if that toggle is on.
- Close the window, quit the app, update the app: agents never restart,
  except the declared multiplexer-version case.
- Uninstall leaves no login entries, no Run values and no `Path` entry, and
  keeps the data.
- The browser at `http://127.0.0.1:<port>/` shows exactly the same UI as the
  app window.

## 16. Spike results (M2.0)

### S3: relocatable tmux (2026-11-23, macOS 26.2, Apple Silicon + Rosetta) — pass here, fresh-Mac run pending

- `packaging/macos/build-tmux.sh` builds tmux 3.5a from pinned, checksummed
  source for arm64 and x86_64 (libevent 2.1.12 static) and `lipo`s them.
- `otool -L`, both slices: only `/usr/lib/libncurses.5.4.dylib`,
  `libSystem.B.dylib` and `libresolv.9.dylib`, all of which every Mac has.
  `minos 13.0` on both slices. No Homebrew paths.
- With `env -i PATH=/usr/bin:/bin` and the shipped `tmux.conf`, it starts a
  session that renders colour and UTF-8. `default-terminal` is
  `screen-256color`. The x86_64 slice (under Rosetta) talks to the same arm64
  server. Attaching from a pty (`script(1)`, like `lolad attach` in
  Terminal.app) works.
- **Pitfall:** a Unix socket path must fit in 104 bytes. A long
  `TMUX_TMPDIR` fails with "File name too long". The planned private
  `TMUX_TMPDIR` under the state dir (§8) is fine for normal home paths, but
  should be checked by `lolad doctor`. As of today `internal/tmux` only passes
  `TMUX_TMPDIR` through; it does not set a private one.
- Still to prove: an agent running on a Mac with no Homebrew (§15 run).

### S5: SMAppService LaunchAgent (2026-11-23) — works unsigned; beta keeps the plain LaunchAgent

- A throwaway ad-hoc-signed bundle (own label `dev.caceres.lolad.spike5*`)
  registered `SMAppService.agent(plistName:)` with
  `BundleProgram = Contents/Helpers/lolad`. Registration succeeded (status
  `enabled`) and launchd ran the helper at once with `argv = [lolad, serve]`,
  `HOME` set and `PATH=/usr/bin:/bin:/usr/sbin:/sbin`.
- **It also works when the whole bundle carries `com.apple.quarantine`** (as if
  downloaded and copied out of the DMG): launchd still spawned the quarantined,
  ad-hoc-signed helper. Gatekeeper only gates the user opening the app, which is
  the "Open Anyway" step.
- **Bug found and fixed:** the bundled `dev.caceres.lolad.plist` had
  `StandardOutPath = ~/.local/share/lolad/logs/daemon.log`. launchd does not
  expand `~`. The job fails with `last exit code = 78: EX_CONFIG` and the daemon
  never starts. The log keys are removed. With stdout on `/dev/null`, `serve`
  writes its own rotated `<state>/logs/daemon.log`.
- **Decision for v0.2.0-beta.1:** the app keeps calling `lolad service install`,
  which writes `~/Library/LaunchAgents/dev.caceres.lolad.plist` pointing at
  `Lolad.app/Contents/Helpers/lolad`. Reasons:
  1. It is what runs the production install today.
  2. It writes the captured `PATH` and absolute paths, which a static bundled
     plist cannot carry.
  3. It is the same code path as CLI installs.
  4. Moving an existing plain-plist job to SMAppService under the same label
     needs a migration step (bootout, remove, register).

  On an unsigned build the Login Items entry shows "lolad" from an
  unidentified developer. Switch to `SMAppService.agent` when a Developer ID
  exists; the bundled plist is kept correct for that.
- Not done by me: a reboot test. The production install's plain LaunchAgent
  has been starting the daemon at login (launchd `runs = 28`).

- **A clean exit is not restarted** (`KeepAlive = {SuccessfulExit: false}`),
  and `serve` exits 0 on SIGTERM, whoever sends it. On 2026-11-23 a stray
  `pkill` stopped the production daemon this way, and it stayed down until it
  was restarted by hand. **Decision: intended, kept for the beta.**
  - "Stop lolad" (tray, `lolad service stop`) is `launchctl kill SIGTERM`,
    and the user expects it to stay stopped.
  - At logout launchd also sends SIGTERM, and `RunAtLoad` starts the daemon at
    the next login anyway.
  - A daemon stopped from outside is visible, not silent: the tray icon shows
    the problem state, the status line reads "lolad is not running", and
    **Start lolad** is one click.

  To restart on any outside kill, the stop path has to change first: "Stop"
  would `launchctl bootout` the job (Start already bootstraps a job that is
  not loaded), and `serve` would exit non-zero on a signal it did not ask for.
  That is a daemon-side change (`internal/cli`), proposed for after the beta.

### S7: harness install and login in the dashboard terminal — commands verified, end-to-end run pending

- `internal/setup/terminal.go` runs `curl -fsSL https://claude.ai/install.sh | bash`,
  `<claude> auth login`, `npm install -g @openai/codex` and `<codex> login`.
  `claude auth login` (Claude Code 2.1.330) and `codex login` (0.170.0) exist
  as written.
- **Gap for a fresh Mac:** installing Codex needs Node.js, and a fresh Mac has
  none, so "Install Codex" fails there. Claude Code's installer needs only
  `curl`. This is acceptable for the beta (one ready harness is enough), but
  the Codex row should say "needs Node.js" before the user presses Install.
- The real run (no outside terminal, reaching "ready") needs a clean account,
  so it is part of the §15 run.

### S8: TCC for unattended agents — pending

Not run on the production Mac: the prompts would appear on the owner's screen
and attribute to the live install. It is part of the §15 run in the VM.

## 17. Windows: ready for a real PC (v0.2.0-beta.1)

Everything below builds and vets on macOS (`GOOS=windows`), and
`Lolad-Setup.exe` compiles with NSIS 3.13. None of it has run on Windows yet.

### Desk-checked so far

- **S4 psmux:** MIT (Copyright 2025 Godwin Sam Josh). Its LICENSE is installed
  beside `bin\psmux\psmux.exe`, as MIT requires. It runs from the per-user
  install dir with no install step of its own. The daemon finds it as
  `bin\psmux\psmux.exe` next to `lolad-daemon.exe` / `lolad.exe`
  (`internal/app/setupwire.go`), not through `PATH`. The zip also carries
  `tmux.exe` and `pmux.exe` copies; only `psmux.exe` is installed.
- **S6:** the installer writes `HKCU\…\Run\lolad =
  "…\bin\lolad-daemon.exe" serve --supervised` and `LoladApp =
  "…\Lolad.exe" --background` on a fresh install, and keeps the user's choice on
  updates. `RequestExecutionLevel user`, `InstallDir
  $LOCALAPPDATA\Programs\Lolad`: no UAC.
- **Path:** a per-user `Path` edit through `packaging/windows/path.ps1`. NSIS's
  `ReadRegStr` truncates at 1024 characters, so a long Path would otherwise have
  been replaced on install and deleted on uninstall (fixed 2026-11-23).
- **Updates (unsigned beta):** the app downloads the installer itself, so there
  is no Mark-of-the-Web and no SmartScreen. It checks the SHA-256 pinned by the
  signed `update.json`, accepts an unsigned installer while the app itself is
  unsigned, and runs it with `/S`. The installer then relaunches
  `Lolad.exe --background`.

### Test script for the real PC (standard user, ~45 min)

Run it as a **standard (non-admin) user**, ideally on a fresh Windows 11
account. Record the result of each step in this section.

1. **Download and SmartScreen.** Download `Lolad-Setup.exe` through a browser,
   so it gets Mark-of-the-Web. Expect the browser's "not commonly downloaded"
   prompt, then SmartScreen "More info → Run anyway". Screenshot each prompt
   for docs/install.md.
2. **Install.** Expect no UAC prompt. Files are in
   `%LOCALAPPDATA%\Programs\Lolad`. Check `reg query
   HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (both values), a new
   terminal's `where lolad`, Start menu ▸ Lolad, and Settings ▸ Apps.
   Test once with a user Path longer than 1024 characters (add dummy entries
   first) and confirm it survives install and uninstall.
3. **First run.** "Open Lolad now" → onboarding. **Watch for console window
   flashes** at every step: daemon start, psmux, git, harness install.
4. **S7 on Windows.** Install Claude Code from the onboarding terminal (`irm
   https://claude.ai/install.ps1 | iex`), then sign in. Expect it to reach
   "ready" with no outside terminal.
5. **First agent** replies in the inbox. Measure the time from download to
   reply (target ≤ 5 min).
6. **`lolad` CLI** in a new PowerShell: `lolad msg who`, `lolad attach <agent>`.
7. **Reboot without opening the app.** The daemon answers on
   `http://127.0.0.1:4100/api/health`, agents run, and the tray icon is there.
   Check Task Manager ▸ Startup apps shows both entries.
8. **Crash watchdog:** in Task Manager, end the `lolad-daemon.exe` child
   (`serve`) and expect a restart within seconds.
9. **Close, quit, reopen:** agents keep running.
10. **Update:** build a second version with the same test key (for example
    `UPDATE_SIGNING_KEY_FILE=<test key> UPDATE_PUBLIC_KEY=<its base64>
    bin/release-local.sh 0.2.0-beta.2`). Serve that folder on the PC with
    `python -m http.server --bind 127.0.0.1`, start `Lolad.exe` with
    `LOLAD_UPDATE_URL=http://127.0.0.1:8000/update.json`, then Check for
    updates → Restart to update. Expect: agents keep running, the new version
    starts, and no SmartScreen.
11. **`taskkill /T` risk:** the installer stops the old daemon with `taskkill
    /IM lolad-daemon.exe /T /F`. If the psmux server is a descendant of the
    daemon in the process tree, this kills every agent. Step 10 shows it. If
    agents die, the installer must stop the daemon without `/T` (or with
    `lolad service stop`).
12. **Uninstall** from Settings ▸ Apps: no Run values, no Path entry, data kept
    unless the box is ticked.
