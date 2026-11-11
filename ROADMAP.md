# lolad roadmap

Updated 2026-11-11. The goal is to go from "an experiment you run from a Go
checkout" to "an app anyone installs in one click, with agents running and
lolad starting at login".

## Decisions

- **One UI.** The dashboard lolad already serves is the only UI. It is the same
  page in a browser, in the macOS app and in the Windows app.
- **Wails v3 shell.** The desktop app is a thin Wails program that provides a
  window, tray / menu bar, notifications, start at login and updates. Its
  window shows the daemon's own dashboard. Full design:
  [docs/desktop.md](docs/desktop.md).
- **The native macOS client (`clients/apple`) is retired.** The Android client
  stays as the phone client.
- **Targets: macOS and Windows.** Linux keeps the CLI and daemon (CI, servers,
  federation peers) but gets no app, installer or tray.
- **The daemon is independent of the app.** It runs at login on its own;
  closing or quitting the app never stops agents.

## Milestones

Ordered by dependency. M1 (Codex) and M2 (desktop) are independent and can run
in parallel.

### M1 — Codex harness

Codex 0.165 already has most of what lolad needs. The flags and features below
are from `codex --help` and `codex features list` on the dev Mac; verify each
one against the real binary, as was done for Codewhale in
`internal/harness/NOTES.md`.

- [ ] `core.HarnessCodex` in the contract (coordinator) plus SPEC section.
- [ ] Adapter in `internal/harness`: argv `codex [resume <session-id>] --model m`
      plus permissions. Bypass is `--dangerously-bypass-approvals-and-sandbox`.
      Default is `--sandbox workspace-write` with approvals going to the
      operator.
- [ ] **Isolation:** a per-agent `CODEX_HOME` so the user's `~/.codex/config.toml`
      is not loaded. Open question: auth lives in `CODEX_HOME/auth.json`.
      Options are per-agent login, a read-only link to the user's auth, or an
      API key in the agent's write-only `.env`.
- [ ] **Hooks:** the `hooks` feature is stable. Install lolad's hooks per agent,
      map the events to `core.HookEvent`, and handle hook trust without
      `--dangerously-bypass-hook-trust` if possible.
- [ ] **Delivery without typing:** if `codex queue --thread <id> --message`
      works against a TUI session, make it the preferred Codex delivery path
      and keep typing into the pane as the fallback.
- [ ] `PromptReady` for the Codex composer, and the startup dialogs (directory
      trust, login, update notice; set `check_for_update_on_startup=false`).
- [ ] **Instructions file:** Codex reads `AGENTS.md`, while lolabot and
      `internal/create` write `CLAUDE.md`. Write the messaging section there
      too. Skills mode for Codex.
- [ ] Models list, harness picker, `lolad new/adopt --harness codex`.
- [ ] Onboarding support: detection, install and `codex login` from the
      dashboard terminal (desktop spec §7, spike S7).
- [ ] Real-binary e2e: create, start, deliver, rotate, crash-restart, resume.

### M2 — Desktop app: one-click install, start at login

**Status 2026-11-11:** everything below is built, and daemon-side parts are
deployed. Not yet run for real: Windows (compiles, installer builds), code
signing/notarization (needs accounts), the release workflow, and the acceptance
runs on a fresh Mac and a fresh Windows VM (§15). `[x]` = built and tested here;
`[~]` = built, needs a real-world run.

Spec: [docs/desktop.md](docs/desktop.md). Section numbers (§) below refer to
it.

**M2.0 Spikes** (§14) — run them before building, and record the results in
the spec. An MVP window shell now exists in `desktop/` (macOS), which settled S1
in part; findings are at the top of the spec.

- [~] S1 Wails v3 basics: window on a loopback URL ✓, tray ✓, single instance ✓;
      notifications, navigation policy (no hook in v3.1.0), user agent still open
- [~] S2 Webview parity: SSE ✓, downloads and external links ✓ (bridged);
      terminal WebSocket, file upload, drag and drop, clipboard still to verify
- [ ] S3 Bundled relocatable tmux on macOS
- [ ] S4 Bundled psmux on Windows (license, runs from `%LOCALAPPDATA%`)
- [ ] S5 SMAppService LaunchAgent from the Wails app
- [ ] S6 HKCU Run + `--supervised` with no console windows
- [ ] S7 Harness install and login inside the dashboard terminal
- [ ] S8 macOS TCC prompts for unattended agents
- [ ] S9 Defender / SmartScreen with a signed installer

**M2.1 Daemon readiness** (§8). Useful even before the app exists.

- [x] `/api/health`, single-instance lock on `serve`
- [x] `lolad service install|uninstall|status|start|stop` (macOS, Windows)
- [x] `serve --supervised` watchdog (Windows), `lolad-daemon.exe` GUI-subsystem build
- [x] `cli_bin`, stored `path:` and `*_bin`, `CREATE_NO_WINDOW` for all children
- [x] Private `TMUX_TMPDIR`, lolad tmux.conf, bundled-multiplexer paths
- [x] Log files with rotation; `lolad doctor --json`
- [x] Bundled lolabot as the default template
- [x] **Go port of lolabot's `setup.sh`** (no Bash on any platform) + conformance test

**M2.2 Onboarding** (§7), as dashboard screens that work in the browser too.

- [x] `/welcome` flow: start at login → harnesses → agents folder → name → first agent
- [x] Setup API, onboarding terminal for vendor installers and logins
- [x] Harness detection from the login-shell `PATH`, re-scan in Settings
- [x] TCC-aware agents folder default and warnings

**M2.3 Wails shell** (§6), in `desktop/` with its own `go.mod`.

MVP done (macOS, `desktop/README.md`): window on the daemon URL, starts the
daemon if absent, offline page, hide on close, tray with Open / Open in Browser /
Start / Reload / Quit, single instance, standard Edit menu, download and
external-link bridge.

- [x] Daemon discovery and start, version banner; [ ] port conflict handling
- [x] Main window, close vs quit, Dock behavior; [ ] navigation policy (no hook in Wails v3.1.0)
- [x] Webview parity fallbacks from S2
- [x] Tray / menu bar with state from `/api/stream`
- [x] Native notifications with deep links; per-kind preferences
- [x] Login-item toggles for the daemon and the app

**M2.4 macOS packaging** (§4, §5, §10)

- [x] `Lolad.app` layout with `Helpers/lolad`, `Helpers/tmux`, LaunchAgent plist
- [~] Universal builds ✓, DMG ✓; Developer ID signing and notarization need an account
- [~] Homebrew cask (template; filled in by the release job)

**M2.5 Windows packaging** (§4, §5, §10)

- [~] Per-user NSIS installer, no UAC; Run values, `Path`, uninstaller (builds; never run on Windows)
- [~] Azure Trusted Signing (needs an account); winget manifest (template)

**M2.6 Updates and release pipeline** (§9, §10)

- [~] Tag-driven GitHub Actions: CLI, tmux, Mac app, Windows installer (actionlint clean; never run)
- [~] Ed25519-signed `update.json`; in-app updater for both platforms (tested with fakes; needs a signed build)
- [x] Multiplexer-version rule: declared agent restart when tmux / psmux changes

**M2.7 Migration and cleanup** (§12, §13)

- [~] "Switch to the app's lolad" for existing CLI / PM2 installs (dialog untested)
- [ ] Retire `clients/apple`; update `clients/README.md`, README, SPEC (ownership table done)
- [ ] Acceptance runs on a fresh Mac and a fresh Windows 11 VM (§15)

### M3 — Own the session layer (optional, after M2 ships)

tmux and psmux remain our largest external runtime dependency, and on Windows
the terminal is only a snapshot viewer.

- [ ] Structured harness protocols where they exist (Codex app-server, Claude
      stream-json / Agent SDK) for state and delivery. Keep the TUI for "take
      control".
- [ ] A lolad PTY host (creack/pty, ConPTY) that outlives daemon restarts and
      keeps a VT screen, replacing `core.Sessions`. This would also remove the
      bundled multiplexer and the M2.6 version rule.

Measure first: if the bundled tmux and psmux prove reliable, this can wait.

### Toward 1.0 (cross-cutting)

- [ ] Config and state schema versions with forward-only migrations (needed by M2.6)
- [ ] API versioning so the Android client and daemon can update independently
- [ ] Local API hardening for a general audience: the loopback dashboard has no
      auth and relies on cross-origin protection; consider a per-install token
      for non-browser local callers
- [ ] Opt-in crash reports, no telemetry by default
- [ ] User docs: install, first agent, Codex vs Claude, federation

## Open questions

1. Codex auth with an isolated `CODEX_HOME`: per agent, shared, or API key?
2. Does lolabot want the Go `setup.sh` port and an `AGENTS.md` template
   upstream, or does lolad maintain them?
3. Apple Developer and Windows signing accounts: who owns them, and in whose
   name?
4. Windows arm64: ship at launch or later (psmux arm64 is unverified)?
5. Bundle ID / identifier (`dev.caceres.lolad` is a placeholder) and the
   product name in menus ("Lolad").
