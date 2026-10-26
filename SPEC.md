# lolad — spec

lolad keeps lolabot agents alive and lets them, and the user, talk over AMP.
One Go binary: daemon, CLI, agent-side messaging client, harness hook.

**The user never needs a terminal.** They message agents from the dashboard
inbox as an AMP participant; agents answer with `lolad msg tell-user`. The live
terminal exists for debugging only.

Shared types and interfaces live in `internal/core` — read `core.go` and
`runtime.go` first. They are the contract between packages. Change them only if
you must, keep changes additive, and say so in your report.

## Concepts

- **Agent** = a lolabot instance directory whose `lolabot.yaml` has a
  `runtime:` block (`core.Runtime`). The slug (`core.Agent.Name`) is the tmux
  session name, the AMP local name, the URL segment and the CLI argument.
- **Harness** = what runs it: `claude` (Claude Code, with its mods/plugins from
  the agent's own `.claude/settings.json`) or `codewhale` (with its plugins and
  `.codewhale-home`). Nothing else is supported.
- **Mail** = AMP messages. lolad *is* the AMP provider. Every message — user →
  agent, agent → agent, agent → user, trigger → agent — goes through
  `core.Mailer.Send`, which enforces the policy. Agents never write another
  participant's mailbox; there is no direct-file delivery path.
- **Org** = teams (nestable, each with a head and members) plus explicit rules,
  in `~/.config/lolad/org.yaml`. `core.Policy` decides who may message whom.
- **Long-running** ≠ one endless conversation. Crash → resume the last
  conversation with backoff. Daily `rotate` → fresh conversation with
  `on_wake`. lolabot's `brain/` and `memory/` carry identity across.

## Paths

| What | Where |
|---|---|
| Config | `~/.config/lolad/config.yaml` (`LOLAD_CONFIG` overrides) |
| Org/policy | `~/.config/lolad/org.yaml` |
| State dir | `~/.local/share/lolad/` (`LOLAD_STATE` overrides) |
| Database | `<state>/lolad.db` (SQLite via `modernc.org/sqlite`, WAL; no cgo) |
| AMP identities | `<state>/identities/<name>/{private.pem,public.pem,identity.json}` (0600 key) |
| Per-agent managed files | `<state>/agents/<slug>/` (claude settings overlay, codewhale hook config, logs) |
| tmux | `tmux -L lolad -f <state>/tmux.conf` — private server; conf sets `base-index 0`, `pane-base-index 0`, `remain-on-exit on`, `history-limit 50000`, `mouse off`, no status bar |
| Daemon | `127.0.0.1:4100` (`listen` in config) |

`config.yaml`:

```yaml
listen: 127.0.0.1:4100
agents_dirs: [/Users/you/agents]   # scanned one level deep for lolabot.yaml with runtime:
agents: []                                          # extra explicit agent dirs
lolabot_template: /Users/you/lolabot   # setup.sh used by `lolad new`
new_agents_dir: /Users/you/agents
```

Each section's tables are prefixed by owning package (`amp_*`, `sup_*`, …) and
created by that package from its own embedded schema with `CREATE TABLE IF NOT
EXISTS`. `internal/store` only opens the DB (WAL, busy_timeout, foreign keys)
and hands out `*sql.DB`. Deleting the DB loses mail and history but no
configuration — config and org stay in YAML.

## AMP provider (`internal/amp`)

Spec: https://github.com/agentmessaging/protocol v0.1.2-draft (a clone is at
`/tmp/amp-spec/protocol`; `spec/04-messages.md`,
`spec/08-api.md`). Reference implementation to learn from, not copy:
`/Users/you/ai-maestro/services/amp-service.ts`.

- Address: `<name>@<tenant>.lolad.local`; bare names expand. Provider name
  `<tenant>.lolad.local`. Case-insensitive, stored lowercase.
- Identities: kinds `agent`, `human` (the user, from `org.user.name`), `system`
  (`lolad`, used by triggers; may always send). lolad generates Ed25519 keys
  for agents and the user (`EnsureIdentity`). Each identity has an API key
  (`amp_live_sk_…`, stored as sha256, plaintext only in `identity.json` 0600).
- Signing: canonical `from|to|subject|priority|in_reply_to|payload_hash`,
  `payload_hash = base64(sha256(json of payload with keys sorted at every
  level, compact, SetEscapeHTML(false), numbers preserved))`, Ed25519 over the
  UTF-8 bytes, base64 in `envelope.signature`. `lolad msg` always signs. The
  server verifies whenever a signature is present and rejects a bad one (403
  `invalid_signature`); for `/v1/route` an unsigned message from an agent is
  rejected too (signatures are cheap here and make `from` provable). Messages
  sent in-process (dashboard as the user, triggers as `lolad`) are signed by the
  server with that identity's key.
- HTTP, mounted at `/v1` (spec shapes; errors `{error, message}`):
  `GET /v1/health`, `GET /v1/info`, `POST /v1/register` (self-registration:
  only for a name that already exists as a lolad agent and has no key yet, or
  returns 409 — agents get identities from lolad, not by inventing them),
  `POST /v1/route` (Bearer auth; `from` = authenticated identity; server makes
  `id` `msg_<unix>_<hex>`, `timestamp`, `thread_id` = in_reply_to's thread or
  its own id; idempotency_key honoured 24h; 1 MB limit → 413; policy denial →
  403 `{error:"forbidden", message:<Decision.Reason>}`),
  `GET /v1/messages/pending?limit=` (the caller's unread+notified mail, spec
  shape), `GET /v1/messages/pending/count`, `DELETE /v1/messages/pending/{id}`
  and `POST /v1/messages/pending/ack {ids}` (= mark read),
  `POST /v1/messages/{id}/read`, `GET /v1/agents` (directory: name, address,
  kind, display, role, team, and whether the caller may message it),
  `GET /v1/agents/resolve/{address}` (public key + fingerprint),
  `GET /v1/agents/me`.
  Skip: attachments, websocket, webhooks, federation, key rotation.
- Policy is enforced inside `Send` (both directions of every path). A denial is
  recorded as an Event (`kind: policy`) so the user can see attempts.
- After a successful Send, `OnMail` subscribers run (delivery, SSE, ntfy).

## Policy (`internal/policy`)

`org.yaml` ↔ `core.Org`. `Check(from, to)` order:
1. `from == "lolad"` → allow (system).
2. `rules`, in order, first match wins (`both` matches the reverse too).
   Patterns: local name, `user`, `team:<id>` (members and head of that team and
   all its sub-teams), `*`.
3. User → agent: `defaults.user_to_agents` (`all` | `heads` | `none`).
4. Agent → user: `defaults.agents_to_user` (`all` | `heads` | `none`).
5. `defaults.vertical` (default true): a head ↔ its own members; a head ↔ the
   head of its parent team; a head ↔ heads of its child teams.
6. Same team and `team.open` → allow.
7. An agent in no team: `defaults.independents` = `user-only` (deny here) | `all`.
8. Otherwise deny, reason `default: no path`.
Every Decision carries a human-readable reason. Validation: unique team ids,
parents exist, no cycles, heads/members are known agents (warning, not error,
for unknown names), an agent belongs to at most one team (as member or head).
`Contacts(from)` lists everyone `from` may reach. Provide mutation helpers the
dashboard uses (add/remove team, set head, move member, add/remove rule) and
`Save` that writes YAML atomically and keeps comments where practical
(yaml.v3 Node is fine; losing comments is acceptable if documented).

## Runtime

### tmux (`internal/tmux`) implements `core.Sessions`
Programs are started directly as the session command (`new-session -d -s
<slug> -c <dir> -x 220 -y 50 -e K=V … -- argv…`), never typed into a shell.
`Attach` runs `tmux -L lolad attach -t <slug>` (`-r` for read-only) inside a
PTY (`github.com/creack/pty`) and exposes it as `core.Terminal`.

### Harness adapters (`internal/harness`) implement `core.Launcher`
**claude**: argv `claude [--continue] --model <m> --permission-mode
bypassPermissions (when bypass) --setting-sources project,local --settings
<state>/agents/<slug>/claude-settings.json [args…] [first prompt]`.
- `--setting-sources project,local` keeps the user's global
  `~/.claude/settings.json` (and AI Maestro's global hook in it) out of agent
  sessions; the agent's own `.claude/settings.json` (its mods/plugins) still
  loads. **Verify** plugins enabled in the agent's project settings still load
  with this flag; if they don't, find what does (e.g. merge `enabledPlugins`
  into the overlay) and document it.
- The overlay JSON adds hooks for SessionStart, UserPromptSubmit, Stop,
  Notification, PermissionRequest (if supported), SessionEnd, each running
  `<lolad> hook <event>` with `LOLAD_AGENT=<slug>` in env. Hook command must
  finish < 3 s; it POSTs to `/api/hook` and prints Claude's expected JSON.
- `--continue` resumes the last conversation in that dir; a fresh launch omits
  it and passes `on_wake` (or the given first prompt) as the prompt argument.
- Note `claude` is a zsh function on this Mac; use the binary
  `~/.local/bin/claude` (resolve via `exec.LookPath` with PATH from config,
  allow `claude_bin` override in config).
- `[1m]` model ids are passed as one argv element (no shell, no globbing).
**codewhale**: argv `codewhale [--continue|-c] [--approval-policy never when
bypass] [--model m] [args…] [first prompt]`; env `CODEWHALE_HOME=<dir>/.codewhale-home`
when that directory exists, `CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE=1` when
`<home>/prompts/constitution.md` exists (mirror `dsh-lola/lola`). Hooks:
Codewhale supports `[[hooks.hooks]]` entries with `event = "..."` (see
`codewhale` `/hooks list-events`, and project `.codewhale/hooks.toml` which
needs approval). Find the least invasive way to install lolad's hooks per
agent (a lolad-managed, clearly fenced block in the agent's codewhale
config, or a per-run `--config` override) and document it. Codewhale's Stop
cannot force continuation, so mail reaches codewhale agents via the notifier.
Verify everything against the real binaries (`codewhale --help`, `claude --help`).
Both: env = process env (PATH etc.) + agent `env_file` + `runtime.env` +
`LOLABOT_HOME=<dir>`, `LOLAD_AGENT=<slug>`, `LOLAD_URL`, `LOLAD_BIN`, and
`PATH` prefixed with the dir of the lolad binary so `lolad msg` works. Never
pass lolad's own secrets or the user's identity into agents.
`PromptReady(pane)` recognises each TUI's idle input box.

### Hooks (`internal/hooks`)
`lolad hook <event>`: reads the harness's hook JSON on stdin, maps it to a
`core.HookEvent` (state: prompt_submit/pre_tool → busy, stop → idle,
notification(idle_prompt) → idle, notification(permission)/permission →
waiting, session_end → stopped/unknown), POSTs `/api/hook`, and prints the
harness's reply format (Claude Stop with unread mail → `{"decision":"block",
"reason":...}`; SessionStart → `hookSpecificOutput.additionalContext`). If the
daemon is unreachable it prints nothing and exits 0 within the time budget —
a hook must never break the agent.

### Delivery (`internal/deliver`) implements `core.Notifier`
Lessons from AI Maestro, all mandatory:
- Only type into a session whose state is idle (hook-reported, < 15 min old)
  **and** whose pane `PromptReady`. Never type into a permission prompt.
- One line: `[#<last8 of id>] [MESSAGE] from <from_name>: <subject> — <body ≤ 300 chars, newlines → space>. Read: lolad msg read <id>`.
- `send-keys -l <text>`, pause 150 ms, `Enter` as a separate call.
- Read back (`capture-pane -e`, last ~40 lines): ref tag above the input box =
  confirmed; still inside the input box = staged → clear with BSpace (not C-u),
  retry once; nothing = failed.
- Dedupe: never type the same message twice within 10 s.
The supervisor owns *when* (queue per agent, wait for idle, retries at
0/30 s/2 m/10 m, max 3 re-notifications of unread mail, Stop-hook delivery for
claude which is preferred whenever the agent finishes a turn). The notifier
owns *how*.

### Supervisor (`internal/supervisor`) implements `core.HookSink`
- Registry (`internal/registry`): load config.yaml, scan `agents_dirs` + `agents`,
  parse lolabot.yaml (`agent.name`, `agent.role`, `runtime`), validate, derive
  slug: `runtime.slug` if set, else `agent.name` sanitised ("Ultron" → `ultron`),
  else the dir basename; `^[a-z0-9][a-z0-9-]{0,62}$`. A duplicate slug is
  rejected with an error event, never silently renamed. `Reload` diffs. Writing the runtime block into an existing
  lolabot.yaml must preserve the rest of the file byte-for-byte (append or
  replace only the `runtime:` block).
- Per agent: desired state persisted in `sup_agents` (default up when
  autostart). Reconcile loop every 2 s: desired up and no session → start
  (resume unless never started or fresh requested); pane dead → record exit,
  `crashed`, restart with backoff 5 s · 2^n capped at 5 min; ≥ 6 crashes in
  30 min → `failed` until Start/Restart. Desired down → graceful stop
  (Escape, then `C-c` twice, wait 10 s, kill-session).
- Daemon restart must not kill agents: on boot adopt existing tmux sessions.
- State from hooks with Seq ordering + TTLs; fallback: pane `PromptReady` ⇒
  idle, otherwise unknown. A turn longer than 15 min stays busy if the last
  event was prompt_submit/pre_tool and the pane is not prompt-ready.
- Rotation at `rotate` HH:MM: when idle (or wait up to 30 min for idle, then
  do it anyway), restart fresh with on_wake.
- Mail: subscribe to `Mailer.OnMail`; mail to an agent → enqueue; if stopped
  and `wake_on_message` → start it (fresh launch's first prompt mentions the
  waiting mail). Deliver via Stop-hook reply (claude) or Notifier when idle.
  Sweep every 60 s for unread mail older than 90 s.
- Triggers: cron (`github.com/robfig/cron/v3`), mail (`<dir>/tools/email.sh
  watch <account>` every `every`, parse the last JSON line `{new:[{id, from,
  subject, trust}]}` — see `jugui/mods/ultron-mail/hooks/register.ts`; trusted
  `operator` mail → mail from `lolad` to the agent with the read command;
  external → event only), webhook (`POST /hook/<agent>/<name>` with token from
  `<state>/agents/<slug>/webhook.token`). All produce mail from `lolad`.
- Every lifecycle change → `core.Event` in `sup_events` and on the
  Subscribe bus.

## Agent creation (`internal/create`)
`lolad new --name Ultron --harness claude [--role …] [--team lumio [--head]] [--mission "…"] [--start]`
and the dashboard form → `Service.Create`:
1. Run `<lolabot_template>/setup.sh --name N --role R --user U --yes <dir>` (no TTY).
2. Append the `runtime:` block to lolabot.yaml.
3. Harness files: claude → `.claude/settings.json` with `model` (keep existing),
   `AGENTS.md` → `CLAUDE.md` symlink if missing; codewhale → `.codewhale-home/config.toml`
   modelled on `/Users/you/agents/dsh-lola/.codewhale-home/config.toml`
   (`project_instruction_imports = ["claude"]`, `[skills] scan_codewhale_only`,
   no secrets) and a `./<slug>` launcher like `dsh-lola/lola` for manual runs.
4. Write a fenced `<!-- lolad:begin --> … <!-- lolad:end -->` section into
   CLAUDE.md: its AMP address, who it reports to / who it may message (from the
   policy, regenerated whenever org changes for every agent), how to use
   `lolad msg` (inbox, read, send, reply, tell-user say/update/deliver/ask, who),
   "reply only when needed; no acknowledgement ping-pong", and that a
   `[MESSAGE]` line in the session means mail is waiting. Replace any existing
   lolabot "Working with other agents (optional)" / AI Maestro guidance
   reference by pointing at this section (don't delete user content).
5. Register identity, add to org (team/head), optionally send Mission as the
   first mail from the user, optionally start.
`lolad adopt <dir> --harness …` does steps 2–5 for an existing lolabot
instance (e.g. `jugui`, `dsh-lola`).

## `lolad msg` (agent-side client, `internal/msgcli`)
Identity: `LOLAD_AGENT` (or `--as`), keys from `<state>/identities/<name>/`,
server `LOLAD_URL`. Talks only the `/v1` AMP API (signs every send).
```
lolad msg inbox [--all] [--json]          # unread by default; one line each
lolad msg read <id|last8>                 # prints the message, marks read
lolad msg send <to> "<subject>" "<body>" [--type request] [--priority high] [--context '{json}']
lolad msg reply <id> "<body>"
lolad msg who                             # directory: who I may message, with roles/teams
lolad msg tell-user say|update|deliver "<text>"
lolad msg tell-user ask "<question>" --option "Yes" --option "No"
lolad msg thread <id>
```
Exit non-zero with the server's reason on 403 so agents see policy denials.

## JSON API (`internal/api`) and CLI
REST over `core.Service` at `/api/…` for the CLI (and anything else), plus
`POST /api/hook` (hook sink) and `POST /hook/<agent>/<name>` (webhooks).
CLI: `lolad serve`, `lolad ls`, `lolad status <a>`, `lolad start|stop|restart [--fresh] <a>`,
`lolad attach <a>` (execs `tmux -L lolad attach`), `lolad send <a> "<text>"`
(mail as the user), `lolad logs <a>` (events), `lolad new …`, `lolad adopt …`,
`lolad org show|check <from> <to>`, `lolad msg …`, `lolad hook …`, `lolad version`.
Loopback only; no auth on `/api` (same as Control). `/v1` uses AMP bearer keys.

## Dashboard (`internal/web`)
Server-rendered Go `html/template` + htmx + SSE, assets embedded with
`go:embed`, no CDN, no node build step at runtime (vendored htmx and xterm.js
files are fine). Works against `core.Service` only. Pages, in priority order:
1. **Inbox** (home): the user's conversations with agents, chat thread view,
   composer, `ask` messages render their options as buttons that send a
   `decision`, unread badges, live via SSE.
2. **Agents**: fleet list with state, last activity, unread mail, restarts,
   controls (start/stop/restart/fresh/interrupt); agent detail with events
   timeline, its mail traffic, config summary, and a read-only live terminal
   (xterm.js over websocket, `Attach(readOnly)`), with an explicit "take
   control" toggle for write mode.
3. **Org**: teams as a tree (nested), heads and members, add/edit teams and
   rules, and a who-can-message-whom matrix computed from `Check` with the
   reason on hover; "test" box (from/to → decision).
4. **New agent**: form for `CreateRequest` (and adopt existing dir).
5. **Traffic**: all mail, newest first, filter by participant — the audit log.
Muted, desaturated palette; hue only as a state/scanning anchor. Light and dark.

## Conventions
- Go 1.25, `gofmt`, `go vet`, table tests. `go test ./...` must pass for your
  packages without network, real tmux sessions are OK in tests only under a
  unique `-L` socket name and must be cleaned up.
- No global state; constructors take dependencies (`New(…)`).
- Log with `log/slog`.
- Don't edit packages you don't own; if you need something from one, put the
  interface in your package or ask the coordinator.
