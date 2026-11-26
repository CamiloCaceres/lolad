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
agents_dirs: [~/agents]   # scanned one level deep for lolabot.yaml with runtime:
agents: []                                          # extra explicit agent dirs
lolabot_template: ~/lolabot   # setup.sh used by `lolad new`
new_agents_dir: ~/agents
```

Each section's tables are prefixed by owning package (`amp_*`, `sup_*`, …) and
created by that package from its own embedded schema with `CREATE TABLE IF NOT
EXISTS`. `internal/store` only opens the DB (WAL, busy_timeout, foreign keys)
and hands out `*sql.DB`. Deleting the DB loses mail and history but no
configuration — config and org stay in YAML.

**Versions.** The DB file carries a schema version in `PRAGMA user_version`.
`internal/store` keeps an ordered, forward-only list of migrations; version 1
is the baseline (the tables each package creates itself), so a pre-versioning
file (0) moves to 1 unchanged. On open the store runs `PRAGMA quick_check`
(a damaged file is refused with a message pointing at `<state>/backups`; it
never auto-restores), refuses a file whose version is newer than the binary
knows ("written by a newer version of Lolad") without touching it, and
otherwise copies an existing file to `lolad.db.v<old>-before-v<new>-<time>.bak`
before running each pending step in its own transaction together with the
version bump. Steps are only ever appended. `config.yaml` may carry a
top-level `version:` (absent = 1); a higher one than the binary supports is
refused, and the file is never rewritten just to add it. YAML errors in
`config.yaml`, `org.yaml` and `lolabot.yaml` read "<path>, line N: …".

**Port conflict.** When `listen` is busy at `lolad serve`: another lolad →
exit (single instance); another program and no `listen:` in config.yaml →
first free `127.0.0.1:4101…4199`, saved as `listen:` in config.yaml; an
explicit `listen:` → exit with a plain message (docs/desktop.md §6.1).

## AMP provider (`internal/amp`)

Spec: https://github.com/agentmessaging/protocol v0.1.2-draft (see
`spec/04-messages.md`,
`spec/08-api.md`). Reference implementation to learn from, not copy:
[AI Maestro's `services/amp-service.ts`](https://github.com/23blocks-OS/ai-maestro/blob/main/services/amp-service.ts).

- Address: `<name>@<tenant>.lolad.local`; bare names expand. Provider name
  `<tenant>.lolad.local`. Case-insensitive, stored lowercase.
- Default tenant (`policy.DefaultTenant`): the OS login as a slug (a-z, 0-9,
  `-`; common accented Latin letters folded, e.g. `José` → `jose`; at most 27
  characters). A login that is generic (`user`, `admin`, `root`, `home`,
  `lolad`, …), shorter than 2 characters or empty after slugging gets a random
  4-character suffix: `user-k3p7`, `lolad-9xqa`. It is chosen **once**: on
  the first start with no `org.yaml` and no agents, `policy.Open` writes
  `org.yaml` (0600, never replacing an existing file) with that tenant;
  onboarding creates the same file the same way if it runs first. An existing
  `org.yaml` is never modified, and one without `tenant` means `home`. An
  install with agents but no `org.yaml` predates this rule and keeps running
  as `home` (nothing is written). Before v0.2.0-beta.1 every install defaulted
  to `home`.
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
  Attachments (spec 04, provider storage; `internal/amp/attachments.go`):
  `POST /v1/attachments/upload` (multipart first file part, or raw body with
  `?filename=`), checked synchronously (size, SHA-256 digest, MIME type and
  magic bytes → `basic_clean`/`suspicious`), so `GET /v1/attachments/{id}` and
  `POST /v1/attachments/{id}/confirm` just return the object;
  `GET /v1/attachments/{id}/download` (uploader or a participant of the one
  message carrying it; served as octet-stream, nosniff). Bytes live in
  `<state>/attachments/<id>`, metadata in `amp_attachments`. The objects go in
  `payload.attachments`, so the signature covers them; acceptance checks each
  ID is the sender's, unsent, and matches the upload. Max 10 per message;
  local mail allows 500 MB per file (twice that per message) because it never
  leaves the machine; files to another lolad are refused for now (federation
  will apply the spec's 25 MB / 100 MB). Unsent uploads are deleted after 2 h,
  sent ones after 30 days. `lolad msg send|reply|tell-user --attach FILE`;
  `lolad msg read` saves a received message's files to
  `$LOLABOT_HOME/attachments/<msg-id>/` (digest checked, suspicious skipped,
  `attachments/` added to an existing `.gitignore`); `lolad msg download`.
  Skip: websocket, webhooks, AFP attachments, key rotation.
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
- Note `claude` may be a shell function; use the binary
  `~/.local/bin/claude` (resolve via `exec.LookPath` with PATH from config,
  allow `claude_bin` override in config).
- `[1m]` model ids are passed as one argv element (no shell, no globbing).
**codewhale**: argv `codewhale [--continue|-c] [--approval-policy never when
bypass] [--model m] [args…] [first prompt]`; env `CODEWHALE_HOME=<dir>/.codewhale-home`
when that directory exists, `CODEWHALE_ALLOW_BASE_PROMPT_OVERRIDE=1` when
`<home>/prompts/constitution.md` exists (mirror a hand-run Codewhale lolabot instance). Hooks:
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
- Only type into a session whose last hook-reported state is idle **and**
  whose pane is `PromptReady` at that moment. Idle does not expire: an agent
  idle for hours is woken within one reconcile tick (≈ 2 s) of new mail.
  Never type into a permission prompt.
- One line: `[#<last8 of id>] [MESSAGE] from <from_name>: <subject> — <body ≤ 300 chars, newlines → space>[ (N files attached)]. Read: lolad msg read <id>`.
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
- Problems (`AgentStatus.problem`, plain words for the dashboard): a turn
  ended by an API error (Claude Code `StopFailure` with its error type,
  Codewhale `session_error`; both map to hook event `stop_failure`) is
  classified. Only the user can fix login, billing, account, model and
  rejected-request errors, so they set `waiting` with `problem.needs_you`.
  Usage and rate limits, an overloaded service and network errors set
  `paused`, with `problem.until` at the limit's reset (parsed from the
  message) or a few minutes away. At `until` lolad clears the problem and
  re-checks the pane. Mail is never typed into a paused or waiting agent, and
  notices that failed meanwhile are re-armed when the problem clears. Work
  hooks (prompt_submit, pre/post_tool, session_start) clear a problem; the
  idle hook after the failure does not. A restart clears it. A busy turn with no
  hook for 45 min gets a `stuck` problem (state stays busy) until the next
  hook. A blocking startup dialog sets a `dialog` or `login` problem read
  off the screen. It clears when the prompt shows. A dead pane's last printed
  line is kept in `last_error`.
- Mail notices that fail all 4 attempts unconfirmed get a new round 30 min later
  (the sweep), so unread mail is never left unannounced.
- Rotation at `rotate` HH:MM: when idle (or wait up to 30 min for idle, then
  do it anyway), restart fresh with on_wake.
- Mail: subscribe to `Mailer.OnMail`; mail to an agent → enqueue; if stopped
  and `wake_on_message` → start it (fresh launch's first prompt mentions the
  waiting mail). Deliver via Stop-hook reply (claude) or Notifier when idle.
  Sweep every 60 s for unread mail older than 90 s.
- Triggers: cron (`github.com/robfig/cron/v3`), mail (every `every`: for an
  account in `email.accounts`, lolad checks the mailbox itself
  (`agentcfg.MailWatcher`: read-only EXAMINE, highest UID seen kept in
  `<state>/mail-watch/`, headers via BODY.PEEK, trust `operator` = sender in
  `email.operator_emails`); any other account falls back to `<dir>/tools/email.sh
  watch <account>`. Either way the last JSON line is `{new:[{id, from,
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
   modelled on a working Codewhale lolabot instance's `.codewhale-home/config.toml`
   (`project_instruction_imports = ["claude"]`, `[skills] scan_codewhale_only`,
   no secrets) and a `./<slug>` launcher for manual runs.
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
instance.

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
lolad msg tell-user ask "<question>" --field "Label[?][:text|longtext|number|date|choice:A|B]"... --secret ENV_NAME[:Label]...
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
   composer, open `ask` messages answered in place (see Requests below),
   unread badges, live via SSE.
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

### Requests (`internal/web/requests.go`, `core/ask.go`)
An agent's `ask` is a request the user answers when ready. `UserChat` carries
`Options` (buttons; an option written "Label — consequence", split on the first
" — ", shows the consequence as a quiet line under the label), `Fields`
(`AskField`: text, longtext, number, date, choice, secret, logs, file; at most
12), or neither (an answer in words).
`Task` marks an ask for something the user does themselves: its two
`Options` are the done and couldn't buttons, and the user may add a note.
- An open request stays where it was asked, in the thread, with a small
  "Answer" under it that opens its choices, form or text box only when the
  user wants. One quiet line above the composer counts waiting requests
  ("Show" jumps to the oldest) and, once answers are staged, offers Send.
  Home lists one row per assistant linking to the question
  (`/inbox/{peer}#m-<id>`). Chatting never answers one: the composer's
  `reply_to` skips open asks and `send` drops an ask parent.
- Each answer is staged (choice, words, form, or Skip) in the dashboard's
  memory; nothing is sent until **Send**, which mails all staged answers to
  that assistant at once: `userchat {kind: "answers", answers: [AskAnswer…]}`,
  a readable body for the agent, `in_reply_to` the last ask.
- Secret fields are saved with `SetEnv` when the card is staged (local agents
  only); the answer lists the keys in `secrets`, never values. An empty secret
  input keeps a key that is already set.
- An ask is closed by a `decision` replying to it (`POST /inbox/{peer}/decide`
  still works), an `answers` mail naming it, or a reply to it from before this
  feature. Asks older than 2026-11-21 04:00 UTC keep the old rule: anything the
  user wrote after them closes them.
- File fields upload when the card is staged (multipart, the composer's
  limits); the answers mail carries the files (at most 10) and `AskAnswer.Files`
  maps each field to its attachment IDs. Logs fields keep the text verbatim.
- Routes: `POST /inbox/{peer}/requests/{id}` (`do` = save | skip | change, or
  `option` with an optional `text`) and `…/requests/send`; both answer with
  the thread refresh.

### User tools and blocks (`internal/msgcli/mcp.go`, `tools.go`, `inbox.go`, `core/blocks.go`)
lolad implements the user-channel spec (github.com/CamiloCaceres, repo
user-channel, draft 0.1) at its Requests level with the secret, file, attach
and push capabilities; `internal/msgcli/testdata/userchannel` holds the spec's
fixtures and `go test ./internal/msgcli` runs them. Agents reach the user
through three MCP tools served by `lolad msg mcp` (stdio, newline JSON-RPC,
server name `user`, the spec's tools.json embedded as `usertools.json` and its
AGENTS.md as the server instructions): `message_user` (say | update |
deliver), `ask_user` (options, fields, task, or words) and `get_user_messages`.
The harness lists the server at launch: Claude gets
`--mcp-config=<state>/agents/<name>/mcp.json` (key `user`) and an allow rule for
the three tools; Codewhale gets a `user` entry merged into
`$CODEWHALE_HOME/mcp.json` (replacing the old `lolad` entry). The MCP env carries
`LOLAD_HARNESS` and `LOLAD_ENV_FILE`. `lolad msg tell-user <kind> --json` takes
the same arguments; `lolad msg get-user-messages [--json]` is the CLI twin of
the third tool. Attach paths follow one rule on every path (the
tools, `tell-user`, `send` and `reply --attach`): relative to the session's
working directory, absolute anywhere, existing regular files only (folders:
zip first). Unknown keys, nulls and broken rules are errors returned to the
model so it corrects the call.
- `get_user_messages` returns unread mail from the local user to the agent as
  the spec's records: an answers or decision mail becomes answer records
  (`choice`, `outcome` for tasks, `values`, `secrets`, `files` with the
  task's "details" files as `note`, `question`), anything else a user message.
  Each mail is claimed with `POST /v1/messages/{id}/claim` (marks it read only
  if unread; 409 otherwise), so two readers never both return it. Every mail
  is prepared (files saved) before any is claimed, and one call stops
  fetching after 20 s: the rest stays unread and the result says "More from
  the user is waiting" (`more: true`). Notes such as a secrets file that
  could not be written go in the text and in `notes`, never as an error once
  mail is claimed. Files are
  saved to `.user-channel/files/<id>/<safe name>` in the agent dir (with a
  `.gitignore` of `*`; Windows reserved names such as `nul.log` get a `_`
  prefix). A file that can't be saved (expired after 30 days, damaged, disk
  full) never fails the call or holds back later mail: its record lists it
  under `unavailable` (`name`, `reason`), the text form says "File not
  received: …", and the mail is claimed like any other. Secrets: Claude agents get "saved (in your
  environment)"; Codewhale agents get the spec's secrets file
  (`.user-channel/secrets.env`, copied from the env file). `open` counts asks
  with no answers/decision mail (asks before 2026-11-21 04:00 UTC: no later
  user mail).
- Push: mail from the local user to a Claude agent whose session was launched
  with the `user` server (its mcp.json says so) is announced as
  `[#ref] The user sent you something. Call get_user_messages to read it.`
  (`deliver.UserNotice`); other mail, and sessions launched by an older lolad,
  keep the `[MESSAGE] … Read: lolad msg read <id>` line.
- A message is laid out as `UserChat.Blocks`: `text` (Markdown), `command`
  (code, where, admin), `code` (a snippet to paste), `values` (label/value rows),
  `link` (http/https only), `steps` (a checklist). A message that is only text
  carries no blocks. The body always holds the same content as plain text
  (`BlocksText`) for email, agents and older dashboards.
- The dashboard draws blocks in the bubble: Copy on commands and snippets, one
  click per value (and Copy all), the link's host beside its label, step ticks
  kept in the browser. Inline code in an assistant's message also copies on
  click. Message bodies keep single line breaks.

## Conventions
- Go 1.25, `gofmt`, `go vet`, table tests. `go test ./...` must pass for your
  packages without network, real tmux sessions are OK in tests only under a
  unique `-L` socket name and must be cleaned up.
- No global state; constructors take dependencies (`New(…)`).
- Log with `log/slog`.
- Don't edit packages you don't own; if you need something from one, put the
  interface in your package or ask the coordinator.

## Agent settings (v2)

The dashboard manages each agent's model, skills, `.env` secrets, config
files and email. Everything is stored in the agent's own directory (lolabot
files), never in lolad's DB. Types: `internal/core/settings.go`; Service
methods at the end of `core.Service`.

**Security model.** The dashboard is loopback-only, with no auth (same as
Control); it relies on `http.CrossOriginProtection`. Secrets are
**write-only** through every lolad surface (UI, API, CLI): you can set,
replace or delete an env var and see whether it is set and its length, but no
endpoint returns a value. Env files are written atomically with mode 0600.
If the agent dir has a `.gitignore` (or is a git repo), lolad ensures the env
file is ignored. The agent itself does see its env values (lolad exports the
env file into its process, and its tools read `.env`); that is the point —
it is how tokens are shared with the agent. Values are never logged, never put
in events, and never sent in mail.

**Applying changes without a restart.** lolad fingerprints the agent's
settings at launch and applies what a running session can take on:
- lolad-side runtime fields (`triggers`, `on_wake`, `autostart`,
  `wake_on_message`, `rotate`) take effect on the next reload; email settings
  are files the agent's tools read when they run.
- claude, env file: the SessionStart hook adds one line to Claude Code's
  `CLAUDE_ENV_FILE` that runs `lolad env-export` before every Bash command,
  re-reading the env file (launch parsing; keys pinned by `runtime.env`, PATH
  and `LOLAD_*` keep their launch values, deleted keys are unset). Claude Code
  itself and MCP servers keep their launch environment.
- claude, skills and plugin updates: when the agent is idle with an empty
  input box (checked at most every 15 s), lolad types `/reload-plugins` if an
  enabled plugin's files changed (e.g. an auto-update), then `/reload-skills`
  if skill folders changed. Both are local commands: no model turn, no hooks.
  `/reload-plugins` does not re-read the `--settings` overlay, so enabling or
  disabling a plugin stays a restart.
**Pending restart** is what is left: model (Claude Code's `/model` asks for
confirmation and rewrites the user's global default, so lolad never types it),
skills mode, plugins enabled or disabled, harness, permissions, args,
`env_file` and `runtime.env`, plus the
env file for sessions launched without live env and env/skills for codewhale.
`AgentStatus.PendingRestart`/`PendingReason` show it, and the UI offers
"Restart to apply" (a resume restart, not a fresh conversation).

### `internal/agentcfg` (new): one agent dir's settings
```go
type Options struct{ UserHome string } // default os.UserHomeDir(); tests point it at a temp dir
func New(agentDir string, h core.Harness, o Options) *Dir
func (d *Dir) Env(envFile string) ([]core.EnvVar, error) // envFile "" = ".env"; relative to agent dir or absolute
func (d *Dir) SetEnv(envFile, key, value string) error   // key ^[A-Za-z_][A-Za-z0-9_]*$; preserve order/comments; quote when needed
func (d *Dir) DeleteEnv(envFile, key string) error
func (d *Dir) Files() ([]core.FileInfo, error)
func (d *Dir) ReadFile(rel string) (core.FileContent, error)
func (d *Dir) WriteFile(rel, content, baseHash string) (core.FileContent, error) // core.ErrConflict on stale hash
func (d *Dir) Email(envFile string) (core.EmailSettings, error) // Watch filled with "mod" when detected, else "none"; the app overlays "lolad" from runtime triggers
func (d *Dir) SaveEmail(envFile string, s core.EmailSettings) (core.EmailSettings, error)
func TestEmail(ctx context.Context, a core.EmailAccount, password string) core.EmailTest
func (d *Dir) EmailPassword(envFile, address string) (string, error) // for TestEmail only
func (d *Dir) Skills(mode core.SkillsMode) ([]core.Skill, error)
func (d *Dir) AddSkill(fromID string) (core.Skill, error)
func (d *Dir) RemoveSkill(id string) error
func (d *Dir) Fingerprint(envFile string, rt core.Runtime) (sum string, parts map[string]string) // parts: "runtime","env","skills","plugins","plugin_set","email" -> hash; names plus file mtimes (directory mtimes, plugin .in_use markers and .last-complete-round are ignored)
```
- **Files**: text files the dashboard may edit — `*.md`, `*.yaml`/`*.yml`,
  `*.toml`, `*.json`, `*.txt` — up to 1 MB, inside the agent dir. Never: env
  files (`.env*`), `.git`, `.venv`, `node_modules`, `emails/`, `indexes/`,
  `outbox/`, `__pycache__`, `.lolad-backup-*`, symlinks that resolve outside
  the dir, binary content. Writes are atomic, keep the file mode, and check
  `baseHash`. Groups and notes: CLAUDE.md/AGENTS.md "instructions" (note:
  the lolad section between its markers is regenerated); lolabot.yaml "config"
  (note: the runtime block is managed by lolad — edits to it are validated by
  reload); `.claude/settings.json`, `.codewhale-home/config.toml` "config";
  `brain/**`, `memory/**`, `skills/**`, `.claude/skills/**` by folder.
- **Email**: lolabot's layout. Accounts in `lolabot.yaml` `email.accounts[]`
  (`address, config_key, imap_server, imap_port, smtp_server, smtp_port`) and
  `email.operator_emails`; the login in `brain/credentials.yaml`
  `email_accounts.<config_key>` (`username`, `password_secret: <ENV NAME>`);
  the password in the env file under that name. Edit lolabot.yaml surgically
  (replace only the `email:` block; every other byte stays). If the agent's
  `tools/email.sh` is the stock lolabot one that only knows `aim-secret`
  (AI Maestro's vault), upgrade it to read the named secrets from `.env`
  exactly as `internal/agentcfg/assets/email.sh` does
  (and add `tools/env_secret.py` from jugui), keeping a `.lolad-orig` copy.
  If the agent has the plain lolabot template tool set (its `email_client.py`
  reads a plaintext `password` and has no `watch`), install lolad's whole set
  (`email.sh`, `email_client.py`, `config.py`, `attachment_guard.py`,
  `env_secret.py`, stock `email-send.sh`); older lolad versions are refreshed.
  Files are recognised by SHA-256 of known versions only (email_tools.go); a
  customised file is never overwritten (a Note says why). This also runs on
  email-settings reads once every `password_secret` is set. `email.sh`
  installs PyYAML into `.venv` on first use when it is missing.
  TestEmail does its own TLS IMAP LOGIN/LOGOUT and SMTP AUTH/QUIT in Go with a
  10 s timeout (never sends mail) and reports each server separately.
  Mod detection: a plugin enabled in the agent's `.claude/settings.json`
  whose name contains "mail" means `Watch: "mod"`.
- **Skills**: sources — `agent` (`<dir>/.claude/skills/*/SKILL.md` for
  claude; the codewhale agent's skills dir for codewhale), `user`
  (`~/.claude/skills/*` for claude, plus codewhale's user skill dirs),
  `plugin:<name>` (skills inside installed plugins from
  `~/.claude/plugins/` that the user has enabled). `Active` reflects the
  mode: barebones → only `agent`. AddSkill copies the skill directory into the
  agent (dereferencing symlinks; refusing if the name exists); RemoveSkill
  deletes only `agent` skills. lolabot's own `<dir>/skills/` folder is
  instructions referenced by CLAUDE.md, not harness skills: it appears in
  Files, not in Skills.

### Skills mode in the harness
- claude: `inherit` = what a normal Claude Code session in that dir sees from
  user space: `~/.claude/skills` and the user's enabled plugins. Because
  agents run with `--setting-sources project,local`, user `enabledPlugins` must
  be merged into the overlay for inherit. Hooks and other user settings stay
  excluded. `barebones` = no user skills and no user plugins: only the agent's
  `.claude/skills` and its project plugins (e.g. ultron-mail). Verify the
  mechanism against the installed Claude Code; record it in
  `internal/harness/NOTES.md`.
- codewhale: `[skills] scan_codewhale_only` (barebones = true, inherit =
  false), set per run without editing the agent's config if possible.

### Creation metadata and first-launch skills (v3)

`CreateRequest.Skills` is written into `runtime.skills` before the initial
mission/start. Empty means inherit. When `Start` is false, creation disables both
autostart and wake-on-message so its own queued mission cannot start it.
`Service.CreateOptions(ctx) (CreateOptions, error)` supplies `DefaultDir` and
`[]AdoptCandidate` (Dir, Name, Slug, Harness, Model, Registered, Error) from the
configured discovery roots and explicit agent paths. It is read-only and exposed
at `GET /api/create/options`. The New screen previews setup, validates known
collisions inline, shows indeterminate setup progress and retained-input recovery,
and lands in the agent conversation with optional Email/Org setup links.

## Federation (v4)

Two people each run a lolad (for example a Mac and a Windows 11 PC). Both machines
are on one tailnet. They pair once; then each human (on their own
dashboard) and their agents can message the other's agents that were shared with them,
and the other way round. Direct provider-to-provider AMP federation
(AMP `spec/06-federation.md`, allowlist trust): no shared server, no relay.
Types are in `internal/core/federation.go`; `core.Service` embeds
`core.Federator`.

**Trust model.** The tailnet is not authentication (it also contains other
people's shared-in devices). A peer is a pinned Ed25519 *provider key*
exchanged at pairing. Every federation request is signed with it. Each remote
*sender* key is pinned on first sight under that peer. Nothing a peer sends is
an instruction to lolad: it is mail, delivered to a mailbox, marked external.

### Names and storage
- Provider name stays `<tenant>.lolad.local`. Pairing refuses a peer with our
  own provider name, on accept and on `pair`, with a message safe to show
  as is: "Both lolads are called "home", and two connected lolads need
  different names. Rename yours under Settings, then make a new invite." The
  accept error is `core.ErrInvalid` and also has `UserMessage() string`
  carrying that text, and `Fix() string` = `rename_lolad`, which the API
  returns as `fix` (see Rename).
- A remote participant's name inside lolad is its full address
  (`ana@jugui.lolad.local`); `core.IsRemoteName`. It is what `Mail.FromName` /
  `ToName`, `Conversation.Peer`, `Outgoing.To` and policy checks carry. Mail
  keeps the **same message id on both sides** (the sender's `msg_…`), so
  `in_reply_to` threads across lolads; a receiver rejects an id that already
  exists with different participants.
- Provider key: `<state>/federation/provider.key` (PEM, 0600) + `provider.pub`.
- Configuration (survives deleting the DB): `<config dir>/connections.yaml`
  (0600, atomic writes): `peers: [{provider, display, url, public_key, login,
  created_at, paused, user_reachable, shares: [{local, remote: []}]}]`,
  `grants: [{peer, remote, remote_display, local, state, origin, created_at,
  decided_at, first_subject}]`.
- DB (`fed_*` tables, owned by `internal/fed`): `fed_invites` (secret sha256,
  expires, used_at), `fed_outbox` (mail id, peer, attempts, next_at,
  last_error), `fed_sender_keys` (peer, address, public key, first_seen),
  `fed_rate` if needed. `amp_messages` gains nullable `remote`, `delivery`,
  `delivery_error` columns (migrate with `ALTER TABLE … ADD COLUMN` when
  missing) and the `held` status.
- `config.yaml` gains `federation: {enabled: true, display: "", url: ""}`.
  `display` defaults to `org.user.display`'s first word; `url` defaults to
  `remote.public_url`. `url` may be plain `http://` only for a loopback host
  (two-daemon tests). Without a URL, federation can accept invites only if the
  peer is reachable, and cannot create invites (FederationInfo.Problem says why).

### Wire protocol (mounted at `/v1/federation/` on **both** listeners, outside device auth)
Every request is signed by the caller's provider key:
`X-AMP-Provider: <provider>`, `X-AMP-Timestamp: <unix seconds>`,
`X-AMP-Signature: base64(ed25519(<timestamp> + "." + <raw body>))` (empty body
for GET). Reject when the timestamp is off by more than 300 s, the provider is
unknown (except `pair`), the signature fails, or — when the peer has a pinned
`login` and the request carries `Tailscale-User-Login` — the login differs.
Bodies ≤ 1 MB. Errors `{error, message}` with AMP codes.
- `POST pair` — `{secret, provider, display, url, public_key, version}`,
  signed by the *new* key (proof of possession); the secret must be a live
  unused invite (single use, 72 h, stored hashed, constant-time compare, 10
  failed attempts per hour per source → 429). Response `{provider, display,
  url, public_key}`. Same provider + same key again → idempotent 200.
- `POST deliver` — AMP federation body `{envelope, payload,
  sender_public_key}`. Checks in order: peer connected and not paused (paused →
  503 `provider_paused`, their outbox retries); `envelope.from` domain equals
  the peer's provider (else 403 `provider_not_trusted`); envelope signature
  verifies with `sender_public_key`, and that key equals the pinned key for that
  address (pin on first sight; mismatch → 403 `invalid_signature` + event);
  recipient resolves to a local participant reachable from this peer (see
  Inbound) else 404 `recipient_not_found` (don't reveal unshared agents);
  attachments → 422; rate limits 100/min per peer, 20/min per peer+recipient →
  429 with `Retry-After`; duplicate id with the same participants → same answer
  as the first time (idempotent retry). Response 202
  `{status: "delivered"|"held", id}`.
- `POST receipt` — `{id, status: "delivered"|"blocked", at}`, sent when a held
  message is released or its sender blocked. Updates our copy's `Delivery`.
- `GET directory` — participants reachable by the caller:
  `[{address, name, display, role, kind}]` (shared agents, plus the user when
  `user_reachable`).
- `POST unpair` — the peer removed us: mark the connection removed (keep it
  listed as "removed by them" until the user deletes it; nothing flows).

**Invite code** (what the user copies to the other person):
`lolad-invite:` + base64url(JSON `{v:1, provider, display, url, public_key,
secret}`). The acceptor pins the inviter's key from the code, so neither side
trusts on first use.

### Inbound (a peer delivers to one of ours)
Reachable locals for a peer: agents in its `shares`, plus the user if
`user_reachable`. For a deliver from remote `R` to local `L`:
1. grant(peer, R, L) `allowed` → deliver. `blocked` → 403 `forbidden`.
2. R is listed in L's share `remote` → create grant `allowed` origin `shared`, deliver.
3. `in_reply_to` is a message L sent to R → deliver (the grant exists from rule 5 anyway).
4. Otherwise create/keep grant `pending` (FirstSubject, Held++), store the mail
   with status `held`, emit event `federation` + the approval hook (ntfy push,
   SSE), respond `held`. At most 50 pending grants per peer (then 429).
`DecideGrant(allow)` releases held mail (status `unread`, OnMail fires → normal
delivery to the agent) and sends `receipt delivered` per message;
`DecideGrant(block)` deletes the held mail and sends `receipt blocked`.
Remote mail is marked: `Mail.Remote = <peer provider>`; the session notice and
`lolad msg read` say it is external and from which lolad.

### Outbound (one of ours writes to a remote address)
`Send`/`/v1/route` with a foreign domain → `CheckOutbound(from, to, in_reply_to)`:
- peer must be connected and not paused, else deny with a reason;
- the user may write to anyone on a connected peer (their side decides);
  doing so creates grant(peer, to, user) `allowed` origin `initiated`;
- an agent may write only when grant(peer, to, agent) is `allowed` or it is
  replying (`in_reply_to` a message from `to` to it);
- `lolad` (system) never writes remotely.
Allowed mail is stored (sender's copy, `Remote`, `Delivery=queued`) and put in
`fed_outbox`; `Send` returns immediately. The outbox worker POSTs `deliver`
at once, then backs off 10 s, 30 s, 2 m, 10 m, 30 m, 1 h … for up to 7 days;
429/503/network errors retry, other 4xx fail permanently with the server's
message. Updates `Delivery` and fires OnMail-like change events so the UI
updates. A paused or removed connection keeps the outbox untouched (removed →
fail all).

### Surfaces
- `Service` (via `core.Federator`): Info, Connections, CreateInvite,
  AcceptInvite, UpdateConnection, RemoveConnection, Grants, DecideGrant,
  RemoteContacts. `Check(from, to)` answers for remote names too.
- `/v1/info` reports `federation: true` when enabled. `/v1/agents` (the
  directory for `lolad msg who`) appends the caller's remote contacts with
  `remote: true` and the peer display.
- API: `GET /api/federation`, `GET|POST /api/connections` (POST = accept
  invite, code in body), `POST /api/connections/invite`, `PATCH|DELETE
  /api/connections/{provider}`, `GET /api/grants`, `POST
  /api/grants/decide {peer, remote, local, allow}`, `GET
  /api/remote-contacts?local=`.
- CLI: `lolad connect invite`, `lolad connect accept` (code from stdin or a
  prompt, never argv), `lolad connections`, `lolad connect share <provider>
  <agent> [--with <address>…]`, `lolad connect unshare <provider> <agent>`,
  `lolad connect pause|resume|rm <provider>`, `lolad approvals`, `lolad
  approve|block <peer> <remote> <local>`.
- Dashboard: Connections page (pair, invite, shares per agent, pause/remove,
  queue + last error, reachability); approval cards at the top of the Inbox
  (Allow / Block with the first subject and who it is for); remote contacts and
  conversations in the Inbox, labelled with the peer; the agent page shows who
  it is shared with; Traffic shows delivery state; Org shows connected lolads
  as external boxes with their shared edges.

## Hub (v5)

Federation needs each lolad to be reachable at its `federation.url`. Two people
on different networks with no shared tailnet have no such URL. The **hub**
gives them one. It is a small server (`lolad-hub`) on a VPS that each lolad
dials out to. Peers deliver to the hub's public HTTPS URL, and the hub passes
the request down the tunnel to the right lolad.

**The hub is a pipe, not a provider.**
- **No change to trust:** Pairing, grants, shares, sender-key pins, signatures,
  the outbox and the approval flow are exactly as in Federation.
- **What the hub can and cannot do:** It cannot forge a request, because every
  request is signed by the caller's provider key and checked by the receiving
  lolad. It can drop or delay requests, and it can read them, since TLS ends at
  the hub. End-to-end encryption is on the roadmap.
- **No mail storage:** The hub stores no mail. A peer that is offline gets 503
  and the sender's outbox retries as today.
- **Direct and hub peers mix:** A lolad on the hub can pair with one reached
  directly, because `url` is per peer.
- **Sign-up is open or invite-only, per hub (v6):** In `open` mode (the
  default) any lolad joins with no code, proving possession of its key, behind
  the limits in "Sign-up". In `invite` mode only operator codes work. Operator
  codes work in both modes.

### Names
- **Handle:** A lolad joins the hub with a handle, `[a-z0-9-]{3,32}` and unique
  on the hub. Reserved handles are `admin`, `api`, `hub`, `www`, `p`, `v1`,
  `lolad`, `support`, `help`, `root`, `operator`, `security`, `console`,
  `status`, `mail`, `abuse` and `info`. A taken handle gets 409 `name_taken`
  with a `suggestion`; an invalid or reserved one gets 400 `invalid_handle`,
  with a `suggestion` when one exists.
- **URL:** Its federation URL becomes `<hub>/p/<handle>`. Everything else
  appends to that unchanged, for example `<hub>/p/<handle>/v1/federation/deliver`.
- **Provider name:** Unchanged (`<tenant>.lolad.local`), and the duplicate rule
  between paired peers is unchanged. Globally unique provider names are a
  public-hub item on the roadmap.
- **Account:** The hub account is the provider public key. One key holds one
  handle.

### Hub server (`internal/hub`, `cmd/lolad-hub`)
- **Shape:** One static binary with SQLite (`modernc.org/sqlite`, already a
  dependency) and `coder/websocket`. It listens on loopback (default
  `127.0.0.1:8095`) behind Caddy, which serves TLS.
- **Flags:** `lolad-hub serve --listen --db --public-url` (for example
  `https://hub.example.com`). `LOLAD_HUB_DB` and `LOLAD_HUB_PUBLIC_URL` set the
  defaults (the database defaults to `/var/lib/lolad-hub/hub.db`). No other
  config.
- **Tables:**
  - `hub_accounts`: handle, public_key, display, created_at, last_seen,
    disabled, joined_via (`open` | `invite`), first_tunnel (NULL until the
    first tunnel).
  - `hub_invites`: code sha256, note, expires_at, used_by, used_at.
  - `hub_settings`: key, value (`signup_mode`).
  - `hub_banned_keys`: public_key, handle, banned_at.
  - `OpenStore` migrates older databases in place (idempotent `ALTER`s; old
    accounts get `joined_via = invite` and `first_tunnel = last_seen`).
- **Admin:** The CLI below, run on the VPS against the DB. When
  `LOLAD_HUB_ADMIN_TOKEN` (24+ chars) is set, there is also an operator console
  at `/console` and a JSON admin API under `/v1/hub/admin/`, both behind that
  secret (Bearer, or a console session with CSRF). Unset, both are 404. The
  console shows people, invites and traffic metadata only; an invite code is
  shown once, at creation. See `deploy/hub/README.md`.
  - `lolad-hub invite [--note ""] [--ttl 168h]` prints a `hubinv_…` code once.
    It is single use.
  - `lolad-hub accounts`
  - `lolad-hub disable|enable <handle>`: disabled means the tunnel is refused
    and requests to it get 403.
  - `lolad-hub rm <handle>`
  - `lolad-hub block <handle>`: removes the account and bans its key from
    signing up again.
  - `lolad-hub signup open|invite`: overrides `LOLAD_HUB_SIGNUP` (the console's
    Pause sign-ups switch does the same).
  - The CLI writes the DB directly. A running hub checks accounts on every
    forward and closes tunnels of disabled or removed accounts within 30 s.
- **Logs:** Metadata only: handle, path, status, size and duration. Never a body
  or an invite code. Query strings are not logged. The console's request log
  and hourly counts are in memory (24 h) and reset on restart.

**Endpoints.** Signed requests use the Federation header scheme
(`X-AMP-Provider` = handle or `-`, `X-AMP-Timestamp`, `X-AMP-Signature` over
`<ts>.<body>`), with a ±300 s window. Errors are `{error, message}`.
- `GET /v1/hub/info` (unsigned): `{name, version, public_url, signup}`, where
  `signup` is `open`, `invite` or `closed` (open, but a cap is reached).
- `POST /v1/hub/register` (signed by the new key):
  - Body: `{code?, hub, handle, display, public_key}`. `hub` is this hub's
    public URL, binding the signature to it; it is required without a code
    (else 400 `wrong_hub`).
  - Response: `{handle, url}`.
  - With a code, the code must be live and unused. Without one, the hub must be
    in `open` mode (else 403 `invalid_invite`) and within the sign-up limits.
    Failures are limited to 10 per hour per IP, then 429.
  - The same key again with the same handle gives an idempotent 200, never
    counted against the limits. A key that already holds a different handle
    gets 409 `key_registered` with `handle`. A banned key gets 403
    `signup_refused`.

**Sign-up (v6).** Open sign-ups are limited, all by env (0 turns one off):
- per source IP 3 and per IPv4 /24 or IPv6 /56 10 successful sign-ups per 24 h
  (`LOLAD_HUB_SIGNUPS_PER_IP`, `LOLAD_HUB_SIGNUPS_PER_NET`), then 429;
- hub-wide 60 per hour and 300 per 24 h (`LOLAD_HUB_SIGNUPS_HOURLY`,
  `LOLAD_HUB_SIGNUPS_DAILY`), counted from the database, then 503
  `signup_closed` with `Retry-After`;
- 2,000 accounts in total (`LOLAD_HUB_MAX_ACCOUNTS`), then `closed`.
- Open sign-ups that never open a tunnel are removed after 72 h
  (`LOLAD_HUB_REAP_UNUSED`). Invited accounts are never reaped.
- An optional cap on bytes carried per handle per 24 h
  (`LOLAD_HUB_BYTES_PER_DAY`, default off), then 429.
- The mode is `LOLAD_HUB_SIGNUP` (`open` by default), overridden by the
  `hub_settings` value the console or CLI sets. The sign-up IP is logged with
  `registered`, never stored.
- `DELETE /v1/hub/account` (signed): releases the handle and closes the
  tunnel.
- `GET /v1/hub/tunnel` (WebSocket): the lolad side of the pipe. See Tunnel.
- `ANY /p/<handle>/v1/federation/<rest>`: the public side of the pipe.
  - Only `GET` and `POST` are allowed (else 405). Bodies over 1 MB get 413.
  - An unknown handle gets 404 `recipient_not_found`. A disabled handle gets
    403 `provider_disabled`. An offline handle gets 503 `provider_offline`
    with `Retry-After: 30`.
  - The source IP is the connection's address. Behind a loopback or trusted
    proxy (see Deploy) it is the rightmost `X-Forwarded-For` entry, the one
    the proxy added.
  - The tunnel has 12 s to answer, else 504.
  - At most 32 requests in flight per tunnel, else 503.
  - At most 300 requests per minute per handle and 600 per minute per source
    IP, else 429.
  - The hub does not check signatures here. The receiving lolad does.
- Any other path gets 404.

### Tunnel
- **Opening:** The lolad opens `wss://<hub>/v1/hub/tunnel`.
- **Authentication:** The first frame from the hub is `{type: "challenge",
  nonce}`. The lolad answers `{type: "auth", handle, sig}`, with
  `sig = ed25519("lolad-hub-tunnel." + <hub public URL> + "." + nonce)`.
  Including the URL binds the signature to this hub. Then the hub sends
  `{type: "ready", url}`. A bad signature, an unknown handle or a disabled
  handle closes the connection with code 4401 or 4403.
- **One tunnel per handle:** A new authenticated tunnel replaces the old one,
  which is closed with code 4409. This covers restarts.
- **Frames:** JSON text. Bodies are base64.
  - Hub to lolad: `{type: "req", id, method, path, headers, body,
    client_ip}`. `path` is `/v1/federation/<rest>` with the query string.
  - Lolad to hub: `{type: "res", id, status, headers, body}`.
  - Several requests can be in flight at once, matched by `id`.
- **Forwarded headers:** Only an allowlist is passed through in each direction:
  `Content-Type`, `X-AMP-Provider`, `X-AMP-Timestamp`, `X-AMP-Signature`, and
  `Retry-After` on responses. Everything else is stripped, notably
  `Tailscale-*` and `X-Forwarded-*`.
- **Keepalive:** A ping every 30 s. After 90 s of silence the connection is
  dropped.

**Tunnel client** (`hub.Client`, in `internal/hub`, wired by the integrator):
- **Inputs:** `hub.ClientOptions{HubURL, Handle, Signer, Handler, Peers,
  OnPeerOnline, OnState}`. `Signer` wraps `fed.Engine.ProviderSign` and
  `ProviderPublicKey`; the private key stays in fed. `Handler` is
  `fed.Engine.Handler()` **only**, never the daemon mux, so nothing else of
  lolad is reachable through the hub.
- **Path check:** Before `Handler`, the client refuses any path outside
  `/v1/federation/` itself, with 404.
- **Requests:** It builds an `http.Request` with `RemoteAddr = client_ip:0`, so
  fed's per-source pairing limit counts the real caller.
- **Reconnects:** With backoff 1 s, 2 s, 5 s, 15 s, 30 s, then 60 s (with
  jitter). The state is published for the UI.
- **Presence:**
  - The client sends `{type: "watch", handles: [...]}`, listing the handles of
    peers whose `url` starts with `<hub>/p/` (from `fed.Engine.PeerURLs`). It
    sends it after `ready`, then whenever the list changes. The list is
    checked every 2 s, or at once on `Client.Refresh()`.
  - The hub sends `{type: "online", handle}` when a watched handle's tunnel
    becomes ready. It also sends one for each watched handle that is already
    online when it receives a watch, so mail queued while this lolad was away
    goes out at once.
  - The client then calls `OnPeerOnline(<hub>/p/<handle>)`.
- **Flushing queued mail:** `fed.Engine.RetryURL(url)` makes queued outbox
  entries for peers with that URL due now. This is new and small. Without it,
  mail queued while a peer was offline could wait up to an hour.

### lolad side
- **Membership file:** `<config dir>/hub.yaml` (0600, atomic writes):
  `{url, handle, joined_at}`. It holds no secret, because the tunnel
  authenticates with the provider key. `config.yaml` is not touched.
- **URL:** While joined, the effective federation URL is `<url>/p/<handle>`,
  over `federation.url` and `remote.public_url`. Leaving restores the
  configured one (`fed.Engine.SetURL`).
- **Wiring:** `internal/app/hub.go`. The tunnel runs for the life of
  `App.Run`. `App.Info` adds `Hub` to the engine's info. `App` implements
  `core.Hubber`, and the API and dashboard use it when the service has it.
- **Status:** `core.FederationInfo` gains `Hub *HubStatus`
  (`{url, handle, state: "connected"|"connecting"|"error"|"disabled", error,
  since}`). `Problem` reports a hub that is unreachable or refusing.
- **CLI:**
  - `lolad hub join <hub-url> --handle <h>` reads the sign-up code (one line)
    from stdin, never argv. It registers with the provider key, writes
    `hub.yaml` and starts the tunnel. A lolad can be on one hub at a time;
    joining another needs `leave` first.
  - `lolad hub status`
  - `lolad hub leave` closes the tunnel and releases the handle. Releasing is
    best effort: an unreachable hub is recorded in an event, and the operator
    can `rm` the handle. Then it removes `hub.yaml`. Peers keep the old URL and
    their deliveries start failing, so tell them or unpair first.
- **API:** `GET /api/hub` (`null` when not joined), `POST /api/hub/join
  {url, handle, code}`, `DELETE /api/hub`. The code is never echoed.
- **Dashboard:** The Connections page shows a "Hub" card between the
  identity and pairing sections.
  - Not joined: a join form (`POST /connections/hub/join`). The code field is a
    password field and the code is never echoed. Errors show the hub's fixed
    refusal message, for example "handle is taken (try "milo-2")".
  - Joined: the hub, the handle, the status and the last error, with a leave
    control that asks for confirmation (`POST /connections/hub/leave`).
  - The invite flow works unchanged once the URL is set.
- **Peers moving to the hub:** Peers paired before joining keep the old URL.
  Pairing again with the same key and a new invite updates `url` and
  `display` on both sides. The inviter takes the acceptor's URL from the
  `pair` body, and the acceptor takes the inviter's from the response.

### Deploy
`deploy/hub/` contains:
- A `Dockerfile` (with `Dockerfile.dockerignore`) for Coolify or any Docker
  host: build from the repo root, a volume on `/var/lib/lolad-hub`, port 8095
  behind the platform's TLS proxy.
- A systemd unit (`lolad-hub.service`, an unprivileged user, state in
  `/var/lib/lolad-hub`) and a Caddyfile snippet
  (`hub.example.com { reverse_proxy 127.0.0.1:8095 }`).

`serve` reads `LOLAD_HUB_PUBLIC_URL`, `LOLAD_HUB_LISTEN` (default
`127.0.0.1:8095`), `LOLAD_HUB_DB`, `LOLAD_HUB_TRUSTED_PROXIES` and the secret
`LOLAD_HUB_ADMIN_TOKEN` (environment only, never a flag), plus the sign-up
settings above. The client IP
used for rate limits is the rightmost `X-Forwarded-For` entry only when the
connection comes from loopback or a trusted proxy range (the Docker image
trusts the private ranges); otherwise it is the connection's address.

### Tests
- **Hub:** `httptest` hub plus two in-process fed engines on temporary DBs, each
  behind a tunnel client. Cover:
  - Pairing through the hub.
  - Delivery, receipt and directory requests.
  - An offline peer getting 503, then the outbox flushing on `online`.
  - Tunnel takeover.
  - Path and header filtering.
  - Rate limits.
  - A disabled handle.
- No real VPS or live daemon in tests.

## Friend links (v6)

One link connects two lolads (docs/friend-connect-design.md). It pairs them,
joins the hub when needed, and pre-approves each side.

- **Link:** `<join_url>#2.<base64url(JSON)>`, where `join_url` defaults to
  `https://lolad.caceresdev.com/join`.
  - The secret is in the fragment, which browsers never send.
  - Keys: `t` tenant, `d` display, `n` the human's local name, `k` raw Ed25519
    provider key, `s` a 16-byte pairing secret, `h` the hub handle (with `H`, the
    hub URL, only when not the default) or `u` a direct URL, `a`
    `[[slug, display]…]` (at most 8 offered agents), and `e` expiry.
  - Valid 7 days, single use.
  - A link is at most 1,500 characters when made (display names are dropped
    first) and 4,096 when read.
  - `k` pins the inviter's key. `d`, `n` and `a` are hints. What the inviter
    shares is stored on its side (`fed_invites.kind = 'friend'`, `offer`,
    `for_note`).
- **Create:** if the lolad is on no hub and has no direct URL, it joins
  `federation.hub_url` (default `https://lolad-hub.caceresdev.com`, env
  `LOLAD_HUB_URL`; empty turns this off) with open sign-up. The handle is the
  tenant, or the hub's suggestion when that is taken.
- **Accept, in one call:**
  1. Local checks, with no side effects: format, expiry, same name, key
     mismatch, and the offered agents.
  2. Join the inviter's hub if on none.
  3. `pair` with `pairBody.user` and `pairBody.offer` (both optional; older
     lolads ignore them).
- **Pre-approval, on both sides:**
  - the peer gets `user_reachable`;
  - each own offered agent is shared ("anyone there can ask");
  - every pair of {other human and other offered agents} × {own human and own
    offered agents} gets an allowed grant with `origin: "invited"`, unless
    blocked.
  - Agents that were not offered still need approval.
- **Offline inviter:** a 503 or an unreachable inviter stores a `fed_pending`
  row and returns `waiting`. It is retried when the hub reports the inviter
  online (pending URLs are in the watch list), every 10 min, and at start,
  until the link expires. `Connections()` lists it with `waiting: true` and
  `waiting_until` (an entry of its own until paired), and `RemoveConnection`
  cancels it.
- **Events:** `friend_connected` (both sides), `friend_waiting`,
  `friend_expired`, `friend_failed`.
- **v1 codes:** `lolad-invite:` codes keep working, through the same accept
  call.
- **API:** `core.FriendInviter` and `/api/friends/*`:
  - `POST invites`, `GET invites`, `DELETE invites/{id}`;
  - `POST preview`, `POST accept`.

  Links go in POST bodies only. Refusals carry `UserMessage()`, which the API
  returns as `message`.

## Rename (beta.3)

Renames this lolad: its tenant, so `you@home.lolad.local` becomes
`you@milo-pc.lolad.local`. It exists for installs that kept `home` from
before v0.2 and so cannot connect with another `home`.

- **Name rules** (`policy.CheckRename`): what the user typed is slugged
  (`policy.TenantSlug`: "Milo PC" -> `milo-pc`), then must be 2 to 32 of
  a-z, 0-9 and `-`. Empty, unchanged, and `home` are refused. Refusals are
  `policy.RenameError` (`ErrInvalid` plus `UserMessage`).
- **What changes:** `org.yaml` `tenant` (comments kept; written last);
  `amp_identities.address`; the `from`/`to` of stored mail JSON under our old
  domain; `identities/*/identity.json` `address` and `provider`; agents'
  CLAUDE.md (regenerated at the start that applies it).
- **What stays:** the `wire` column (the signed record), bare names
  (`from_name`, `to_name`, policy, teams, `LOLAD_AGENT`), keys and API keys,
  the federation key, devices, and the hub account and handle.
- **Running agents keep working (beta.4):** agent sessions outlive the
  restart, and their `lolad msg mcp` servers loaded the old address. The MCP
  server re-reads `identity.json` before each call. For servers that cannot
  (started from an older binary), the provider accepts a local identity's
  signature made under a former name (`rename-former.json`, plus
  `rename-last.json` for renames made by beta.3) with that identity's own key,
  and re-signs the envelope under the current address, so the stored and
  forwarded wire verifies normally. The caller is already authenticated as
  that identity, so this window has no time limit.
- **Connections are redone** (v1): every peer gets a best-effort signed
  `unpair` at request time (their card shows "removed by them"), and the
  rename empties `connections.yaml`. Open invite links and codes are
  cancelled (they carry the old name). Queued remote mail leaves the outbox
  and is marked `failed`, "not sent: this lolad was renamed" (its wire was
  signed under the old name). A renamed-announce that keeps connections is
  in the backlog.
- **Request** (running daemon): validate; refuse with `409` and `outbox: n`
  while remote mail is queued, unless `anyway`; refuse a second request
  while one waits; notify peers; write `<state>/rename.json`
  `{from, to, at, anyway}`; answer `202`; then serve exits with
  `app.ErrRestart` (exit code 1). launchd, systemd, the Windows watchdog and
  the desktop app start lolad again; under `lolad serve` in a terminal the
  user does (`restart: "manual"`).
- **Apply** (next start, in `app.New` before the database opens), steps in
  order: `backup` (`<state>/rename-backup-<ts>-*/`: the DB by `VACUUM INTO`,
  `org.yaml`, `connections.yaml`, every `identity.json`, absent files noted,
  a `complete` marker last; its path goes into the plan), `database` (one
  transaction; the WAL is checkpointed after), `identities`, `connections`,
  `org` (the commit point), `finish` (`rename-last.json`, plan removed).
  Each step is idempotent, so a crash anywhere is finished by the next
  start. An error before `org` restores the backup and starts under the old
  name, with the reason in `rename-last.json`; once `org.yaml` has the new
  name, a rename only rolls forward. A plan whose `from` no longer matches
  `org.yaml` is dropped. Backups are deleted after 7 days.
- **API** (`core.LoladRenamer`):
  - `GET /api/lolad/name` -> `{name, provider, restart: auto|manual,
    connections, open_links, outbox, pending?, last?}` (`pending`/`last`:
    `{from, to, at, error?}`).
  - `GET /api/lolad/name/check?name=` -> `{ok, name, message}`.
  - `POST /api/lolad/name {name, anyway?}` -> `202 {name, restarting}`;
    `422 {message}` for a name; `409 {message, outbox?}` for queued mail or a
    rename already waiting.
  - Refusals with a `Fix()` (same name) carry `fix: "rename_lolad"` on
    `/api/friends/*` and `POST /api/connections`.

## Windows (v4)

lolad runs natively on Windows 11: no WSL, no Docker. The tmux layer
stays: on Windows lolad drives **psmux** (github.com/psmux/psmux, a native
tmux-compatible multiplexer on ConPTY, installed with `winget install psmux`;
it ships a `tmux` binary). Same `core.Sessions`, same supervisor, same live
terminal.
- `config.yaml` gains `tmux_bin` (default: `tmux` from PATH; on Windows
  prefer `psmux`, then `tmux`).
- A **conformance suite** (`internal/tmux/conformance_test.go`, run against
  `LOLAD_TMUX_BIN` or the default) exercises every tmux operation lolad relies
  on with a small fake agent program: private `-L` server, start detached with
  dir/size/env, `remain-on-exit` + dead pane + exit status, pane pid, list,
  exact-name targeting (`=name`), `send-keys -l` with unicode/quotes/
  semicolons, named keys Enter/Escape/BSpace/C-c, `capture-pane -p -e -J -S`,
  resize, kill, server survives client exit. `lolad doctor` runs a short
  version and names the failing operation.
- Gaps get portable workarounds used on both platforms where possible: set
  server options with `set-option` after the server starts instead of `-f`;
  resize after creating; on Windows the dashboard terminal is a capture-poll
  stream (≈150 ms, keys forwarded with send-keys) instead of a PTY attach
  (`creack/pty` is Unix-only).
- `exec-env` on Windows cannot `exec`: it starts the child with the env from
  the file, inherits stdio (the ConPTY), forwards exit status. Secrets still
  never travel in argv (no `new-session -e` for env values on any platform).
- `GOOS=windows go build ./...` and `go vet` pass; Unix-only code sits behind
  build tags. CI (`.github/workflows/ci.yml`): Linux/macOS with tmux, Windows
  with psmux, running `go test ./...` and the conformance suite.

## Desktop app support (daemon contract)

See `docs/desktop.md`. The daemon side:

- `GET /api/health` (no auth on the loopback listener) returns
  `{"product":"lolad","version","pid","started_at"(RFC3339),"exe","install":"app"|"cli","state_dir"}`.
  `install` is `app` when `LOLAD_INSTALL=app` or the executable lives inside a
  `*.app` bundle or a `\Programs\Lolad\` directory, else `cli`.
- `lolad serve` holds an exclusive lock on `<state>/lolad.lock` (flock /
  LockFileEx). A second `serve` prints `already running at <url>` and exits 0.
- `lolad serve --supervised` runs `serve` as a child and restarts it on a
  crash (backoff 1s doubling to 60s; gives up after 10 crashes in 10 minutes,
  reason in the daemon log; exit code 0 is not restarted).
- When stderr is not a terminal, `serve` logs to `<state>/logs/daemon.log`
  (rotated, 5 files x 5 MB).
- `lolad service install|uninstall|status|start|stop [--json]`;
  `status --json` -> `{"installed","running","mechanism","path"}`.
- `lolad doctor --json` -> array of `{"id","status":"ok|warn|fail","message","fix"}`.
- `config.yaml` keys (fields only; consumers are harness/registry/hooks):
  `cli_bin` (console CLI used for `LOLAD_BIN`, the hook command and agents'
  PATH; `registry.Config.CLIPath(exe)` returns it or `exe`), `path` (PATH
  string for harness discovery and agents), `claude_bin`, `codex_bin`,
  `codewhale_bin`.
