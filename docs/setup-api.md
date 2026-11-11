# First-run setup API

Onboarding (`/welcome` in the dashboard, the desktop app's first launch) is a
thin layer over this API, so a client can drive it too. Package:
`internal/setup`. Mount `setup.Handler(svc)` for `/api/setup` and
`/api/setup/` ahead of the general `/api/` handler. Everything is JSON; errors
are `{"error":"..."}` with 422 for a bad value, 404 for an unknown session, 501
when this build has no such feature.

State lives in `<state>/setup.json` (progress only, nothing secret), so
quitting midway resumes. Detection results are written to
`~/.config/lolad/config.yaml` (`$LOLAD_CONFIG` overrides); only the keys
listed below are touched, every other key and comment stays byte for byte.

## `GET /api/setup`

```json
{
  "done": false,
  "step": "harnesses",
  "harnesses": [
    {"id": "claude", "name": "Claude Code", "installed": true,
     "path": "/Users/me/.local/bin/claude", "version": "2.1.7",
     "logged_in": true, "install_hint": "Run: curl -fsSL https://claude.ai/install.sh | bash",
     "login_checked": true, "ready": true, "can_install": true, "can_login": true},
    {"id": "codex", "name": "Codex", "installed": false, "logged_in": false,
     "install_hint": "Run: npm install -g @openai/codex (needs Node.js ...)",
     "login_checked": true, "ready": false, "can_install": true, "can_login": true},
    {"id": "codewhale", "...": "..."}
  ],
  "agents_dir": "/Users/me/Lolad Agents",
  "agents_dir_warning": "",
  "user_name": "Camilo Caceres",
  "has_agents": false,
  "service": {"available": true, "installed": false, "running": false},
  "capture_path": true, "dismissed": false, "skipped_agent": false,
  "service_done": false, "folder_set": false, "name_set": false,
  "platform": "darwin", "config_path": "/Users/me/.config/lolad/config.yaml",
  "terminals": [{"session": "setup-install-claude", "terminal_url": "/welcome?terminal=setup-install-claude", "action": "install-claude"}]
}
```

* `step` is the first incomplete one: `login` (start at login), `harnesses`
  (at least one ready), `folder`, `name`, `agent`, then `done`.
* `done` is true when a harness is `ready`, an agents folder was chosen, and
  agents exist or the user chose to add them later. Once true it stays true
  (`completed` in `setup.json`).
* `ready` = installed, runs (`--version` works), and signed in. For harnesses
  whose sign-in lolad cannot check (`login_checked: false`, today Codewhale),
  installed is enough. A harness that is installed but unusable carries a
  `problem` sentence (for example Codex installed through npm with no `node`).
* `agents_dir_warning` is set on macOS when the folder is under `~/Desktop`,
  `~/Documents`, `~/Downloads`, iCloud Drive, a cloud-synced folder or `/Volumes`:
  agents run in the background and cannot answer macOS's access prompts.
  The default folder is `~/Lolad Agents`.
* `user_name` is the saved name, else the OS account's full name.

Detection looks in the login shell's `PATH`, the daemon's `PATH`, `~/.local/bin`,
`~/.claude/local`, `/opt/homebrew/bin`, `/usr/local/bin`, `%LOCALAPPDATA%\Programs`,
`%APPDATA%\npm` and the npm global prefix, and tries the `*_bin` already in
`config.yaml` first. Sign-in: Claude Code by `claude auth status`, then its
credentials file, `~/.claude.json` and (macOS) the keychain entry's existence;
Codex by `$CODEX_HOME/auth.json` (default `~/.codex`); Codewhale best effort.
Secrets are never read into the response.

## `POST /api/setup`

Every field is optional; omitted ones are left alone. Returns the new status.

```json
{"agents_dir": "~/Lolad Agents", "user_name": "Ada", "capture_path": true,
 "service_done": true, "skip_agent": false, "dismiss": false}
```

* `agents_dir` is expanded (`~`), created, added to `agents_dirs` (existing
  entries stay) and set as `new_agents_dir`.
* `user_name` sets `user.display` in `org.yaml`. `name_saved: true` says the
  caller already stored it through the daemon and only the progress is recorded.
* `capture_path` turns the login-shell PATH capture on or off (default on).
* `service_done` marks "start at login" finished or skipped; `skip_agent`
  accepts "I'll add an agent later"; `dismiss` leaves setup so `/` stops
  redirecting to `/welcome`.

## `POST /api/setup/rescan`

Repeats detection. With the capture on it first runs the user's login shell once
(`$SHELL -lic` printing `$PATH` between markers, 5 s timeout; the registry user
and machine `Path` on Windows) and keeps **only PATH**. Writes `path:` and the
`claude_bin`, `codex_bin`, `codewhale_bin` it found. Returns the status.

## `POST /api/setup/terminal`

```json
{"action": "install-claude"}
```

`action` is one of `install-claude`, `login-claude`, `install-codex`,
`login-codex`, `login-codewhale`. Starts the vendor's official command in a
throwaway lolad-managed session named `setup-<action>`:

| action | runs |
|---|---|
| `install-claude` | `curl -fsSL https://claude.ai/install.sh \| bash` (Windows: `irm https://claude.ai/install.ps1 \| iex`) |
| `install-codex` | `npm install -g @openai/codex` |
| `login-claude` | `<claude> auth login` |
| `login-codex` / `login-codewhale` | `<codex\|codewhale> login` |

Login actions need the binary found first (422 otherwise). Response:

```json
{"session": "setup-install-claude", "terminal_url": "/welcome?terminal=setup-install-claude",
 "action": "install-claude", "title": "Installing Claude Code"}
```

The dashboard shows it in the existing terminal view: the websocket is
`/welcome/terminal/<session>` (`?mode=control` to type), which accepts only
`setup-*` sessions of these actions, never an agent's. A running session is
reused by a second request. When the command ends the harnesses are re-scanned
and the session is killed after about 30 s so its output can be read; a session
is always killed after 30 minutes. The session's environment is a short
allowlist (HOME, USER, SHELL, LANG, TMPDIR, PATH, ...), never the daemon's own
variables.

## `POST /api/setup/service`

Registers lolad to start at login through the integrator's `ServiceControl`
(`lolad service install`). 501 when none is wired.

## Wiring (integrator)

```go
mgr := setup.New(setup.Options{
    StateDir:    cfg.StateDir,
    ConfigPath:  configPath,              // optional, default registry.ConfigPath()
    Sessions:    tmuxSessions,            // *tmux.Sessions satisfies setup.Sessions
    Service:     serviceControl,          // Install(ctx) error; Status(ctx) (installed, running bool, err error)
    CountAgents: func(ctx context.Context) (int, error) { l, err := a.List(ctx); return len(l), err },
    SaveUserName: func(ctx context.Context, n string) error { /* Org + SaveOrg */ },
    Reload:      func(ctx context.Context) error { return a.Reload(ctx) }, // pick up a changed config.yaml
})
mux.Handle("/api/setup", setup.Handler(mgr))
mux.Handle("/api/setup/", setup.Handler(mgr))
web.New(a, web.Options{..., Setup: mgr})
```

`config.yaml` keys written: `path`, `agents_dirs`, `new_agents_dir`,
`claude_bin`, `codex_bin`, `codewhale_bin`. The registry's `Config` has no
fields for `path`, `codex_bin` or `codewhale_bin` yet; they are ignored until it
gains them.
