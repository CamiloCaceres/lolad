# lolad

lolad keeps lolabot assistants alive
and lets agents and their human talk through signed AMP mail. One Go binary
provides the daemon, CLI, agent messaging client, hooks, and local dashboard.
The dashboard inbox is the normal way to work with agents; terminals are for debugging.

## Install and run

Requires Go 1.26, tmux, a lolabot template checkout, and the `claude` or
`codewhale` binary for each agent's harness. On this Mac Claude's executable is
`~/.local/bin/claude`; a shell function named `claude` is not an executable.

```sh
mkdir -p ~/.local/bin ~/.config/lolad
go build -o ~/.local/bin/lolad .
lolad serve
```

Open <http://127.0.0.1:4100>. `/api` is loopback-only and has no authentication;
AMP `/v1` endpoints require each participant's bearer identity and signed sends.

Use **PM2**, rather than launchd, on this Mac: macOS blocks launchd jobs from
accessing the DevSSD. Launch PM2 with an empty environment so session tokens
from the current terminal cannot enter PM2's saved process environment. Review
an existing PM2 daemon's environment before reusing it: `env -i` cleans this
invocation, but does not erase variables already held by an older PM2 daemon.

```sh
env -i HOME="$HOME" USER="$USER" PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/bin:/bin" \
  /opt/homebrew/bin/pm2 start "$HOME/.local/bin/lolad" --name lolad --interpreter none -- serve
env -i HOME="$HOME" USER="$USER" PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/bin:/bin" \
  /opt/homebrew/bin/pm2 save
```

Set up PM2's boot restoration for your machine separately. Avoid starting lolad
from a credential-filled agent session or saving that session's environment.

## Configuration and teams

Save this as `~/.config/lolad/config.yaml`:

```yaml
listen: 127.0.0.1:4100
agents_dirs:
  - /Users/you/agents
agents: []
lolabot_template: /Users/you/lolabot
new_agents_dir: /Users/you/agents
```

Scanning is one directory level deep and selects only `lolabot.yaml` files with
a `runtime:` block. `agents` adds explicit instance directories outside that
scan. `LOLAD_CONFIG` overrides the config path; `LOLAD_STATE` overrides the
state directory, normally `~/.local/share/lolad`.

Save the organization as `~/.config/lolad/org.yaml`. This example puts the user
above Lola, chief of staff and head of the root `company` team, with department
heads reporting upward and department members reporting to their own head.
Ultron remains independent. Team membership uses agent slugs: `lola` for Lola
in `dsh-lola`, and `ultron` for Ultron in `jugui`.

```yaml
tenant: milo
user:
  name: milo
  display: Camilo Caceres
teams:
  - id: company
    name: Company (Lola — Chief of Staff)
    head: lola
    members: []
    open: false
  - id: engineering
    name: Engineering
    parent: company
    head: eng-lead
    members: [builder]
    open: true
  - id: operations
    name: Operations
    parent: company
    head: ops-lead
    members: [bookkeeper]
    open: false
rules:
  - from: lola
    to: ultron
    allow: true
    both: true
    note: Chief of staff may consult Ultron
  - from: team:operations
    to: team:engineering
    allow: false
    note: Coordinate through department heads
defaults:
  user_to_agents: all
  agents_to_user: all
  vertical: true
  independents: user-only
```

Create/adopt the department agents before relying on their names. Unknown
members produce policy warnings. An agent may head or belong to only one team;
heads of child teams connect to the root head through `parent`, rather than
also appearing in the root team's `members`.

## Create and adopt

```sh
lolad new --name Researcher --harness claude --role "Research assistant" \
  --team engineering --mission "Track changes and report useful findings" --start
lolad new --name Analyst --harness codewhale --role "Operations analyst"
```

Without `--start`, creation and adoption write `autostart: false` and leave
the agent stopped, including after registry reload and daemon restart. Mail
can still start it later: `wake_on_message` defaults to true, so a mission
or subsequent message wakes it unless `runtime.wake_on_message: false` is set.

The agent's slug comes from its name (for example `researcher`) unless an
explicit slug is supplied. Creation always pins it in `runtime.slug`, so a
later display-name change cannot change the identity. The creator refuses an
existing non-empty directory. It invokes the template's
`setup.sh --name … --role … --user … --yes <dir>` with no terminal, appends the
runtime configuration, writes harness files, adds messaging instructions,
ensures the AMP identity, updates the team, and optionally mails the mission
from the user and starts the agent. `--head` with `--team` selects an unoccupied
team head position. Model and runtime options are also available in the dashboard.

Existing assistants can be adopted instead of recreated. For this installation,
Ultron lives in `jugui`, running Claude with its own `ultron-mail` mod; Lola
lives in `dsh-lola`, running Codewhale:

```sh
lolad adopt /Users/you/agents/jugui --harness claude
lolad adopt /Users/you/agents/dsh-lola --harness codewhale
lolad start ultron
lolad start lola
```

These are operator commands; this repository's tests never modify those live
directories. Adoption keeps the existing brain, memory, tools, project plugins,
and harness configuration. Claude's `enabledPlugins` survives settings updates,
so adoption does not install or remove Ultron's mail mod. Install the mod in the
instance separately if it is missing. Codewhale retains its local config,
provider credentials and constitution; a missing config is initialized without
secrets, with `project_instruction_imports = ["claude"]` and
`skills.scan_codewhale_only = true`. The manual launcher exports `LOLAD_AGENT`
and the instance's `CODEWHALE_HOME`, loads its `.env`, and forwards arguments.

Slugs use `runtime.slug` when set, otherwise the sanitized `agent.name`, and
finally the directory basename if no usable agent name exists. These instances
are addressed as `ultron@milo.lolad.local` and `lola@milo.lolad.local`. Adoption
writes `runtime.slug` when the identity differs from the directory basename,
keeping it stable if the display name changes later. Slugs are lowercase and
match `[a-z0-9][a-z0-9-]{0,62}`. Adoption changes the runtime files and identity
registration; use the Org dashboard to place an adopted agent in a team. Start it explicitly when ready.

A runtime block can look like this (append it to an instance's `lolabot.yaml`):

```yaml
runtime:
  slug: ultron
  harness: claude
  model: sonnet
  permissions: bypass
  autostart: true
  wake_on_message: true
  on_wake: Read CLAUDE.md, check lolad msg inbox, and continue your mission.
  rotate: "04:00"
  env_file: .env
```

`bypass` is intended for unattended operation; `default` allows harness approval
prompts. Daily rotation starts a fresh conversation, while crash recovery
resumes the previous conversation. Durable identity lives in `brain/` and
`memory/`. Newly created setup fields must not contain quotes, backslashes,
newlines, `|`, or `&`, because the upstream shell template interpolates them
into YAML and sed commands.

## Messaging and policy

All paths use the same policy-enforced AMP provider: dashboard messages,
agent replies, agent-to-agent messages, and system triggers. Agents do not
write mail files into one another's directories. Local names expand to
`<name>@<tenant>.lolad.local`; addresses are case-insensitive.

```sh
lolad send ultron "Please review today's priorities"
lolad org show
lolad org check lola ultron
```

Inside an agent, use `lolad msg` with its supplied `LOLAD_AGENT` identity.
The full [agent messaging guide](docs/agent-messaging.md) is also the source
for the generated `<!-- lolad:begin -->` section in `CLAUDE.md`; the daemon
refreshes each agent's section after organization changes.

```sh
lolad msg inbox
lolad msg read <id-or-last8>
lolad msg send lola "Review" "The report is ready"
lolad msg reply <id> "Here is the result"
lolad msg tell-user update "The review is underway"
lolad msg tell-user deliver "Report: docs/report.md"
lolad msg tell-user ask "Publish the report?" --option "Yes" --option "No"
lolad msg who
lolad msg thread <id>
```

The guide's `{{…}}` fields are filled with the agent's address, reporting line,
and permitted contacts. A `[MESSAGE]` notification means mail is waiting;
read the message and its envelope before acting. Reply only when useful;
acknowledgement ping-pong is unnecessary. AI Maestro and AMP-via-maestro
instructions retained elsewhere in a lolabot file are superseded by lolad.

Policy evaluates paths in this order:

1. The system sender `lolad` may always send.
2. Explicit rules are checked in file order; the first match wins. `both`
   includes the reverse direction; `team:<id>` includes descendant teams.
3. User-to-agent and agent-to-user defaults allow `all`, `heads`, or `none`.
4. With `vertical: true`, heads and their members may message both ways,
   as may heads of directly related parent and child teams.
5. Members of the same open team may message one another.
6. Independents may reach other agents only with `independents: all`, unless
   an earlier rule grants access. `user-only` leaves their human access to
   the user defaults above.
7. Everything else is denied with a reason, visible in the UI and CLI.

The Traffic view records every successful message, and policy denials produce
a policy event. Do not bypass denials by using another identity.

## Federation

Connect another person's lolad, share selected agents and approve each remote
sender with the [federation guide](docs/federation.md). Both installations keep
their own keys, agents and mailbox; the durable outbox retries offline peers.

## Dashboard tour

- **Inbox** is home: conversations, unread counts, threaded replies, and a
  composer. Agent `ask` messages offer decision buttons.
- **Agents** shows state, last activity, unread mail, and restart counts.
  Open an agent for its history, runtime config, controls and live terminal.
  The terminal is read-only until you explicitly take control.
- **Org** shows the team tree, heads, members, rules, and the messaging matrix.
  Test a sender/recipient pair to see the policy's explanation.
- **New agent** creates a fresh lolabot instance or adopts an existing one.
- **Traffic** is the shared mail audit trail, filterable by participant.

## Troubleshooting

```sh
lolad ls
lolad status ultron
lolad logs ultron
lolad attach ultron
lolad restart ultron
lolad restart --fresh ultron
```

Attach is for diagnosis; detach with tmux's `Ctrl-b d`. lolad uses a private
`tmux -L lolad` server and leaves other tmux sessions alone. Daemon restarts
adopt surviving sessions rather than killing them. PM2's `pm2 logs lolad`
shows daemon output; agent lifecycle and policy events appear in `lolad logs`.

For registered agent launches, lolad pre-accepts Claude workspace trust for that
exact directory in `.claude.json` and records the bundled Codewhale working
agreement in the private agent home. Existing Claude config and custom
Codewhale agreements are preserved. The global `.claude/settings.json` is
excluded from launches.

During startup, lolad checks the pane every two seconds and accepts only known
workspace-trust or working-agreement screens, recording `dialog` events. After
two minutes without a ready composer or an active-turn hook, an unrecognized
screen becomes `waiting` with `startup blocked: <first pane line>`. Check
`lolad logs <agent>` and the terminal for missing provider credentials, login,
or a changed TUI. Supply credentials through the agent environment or resolve
the displayed setup step; a subsequent hook updates its state. Restart after
changing configuration. Mail remains queued until hook-confirmed idle.

`waiting` means the harness needs permission or input. `unknown` means state
signals are missing; inspect the terminal and hook events. Mail waits for a
confirmed idle prompt, so it is never typed into an active turn or permission
prompt. Codewhale receives mail notifications when idle; Claude can also receive
waiting mail at its Stop hook.

A crash restarts with backoff from 5 seconds to 5 minutes. Six crashes within
30 minutes move an agent to `failed`; fix the cause, then Start or Restart to
clear it. Fresh restart begins a new conversation with `on_wake`. If creation
fails after scaffolding, its files remain for inspection and adoption; do not
rerun `new` over the non-empty directory.

State lives under `~/.local/share/lolad`: the SQLite database, identities, and
per-agent managed hooks and logs. Configuration and organization stay in YAML.
Deleting the database loses mail and history. Private identity files must stay
private, and provider credentials belong in each instance's local environment,
never in generated config or PM2's saved session environment.

## Development

```sh
go test ./internal/create/...
go vet ./internal/create/...
```

The creation integration test copies the real lolabot template, runs its
unchanged setup script into temporary directories for both harnesses, and
adopts a copied fixture. It disables optional Python dependency installation
for offline testing. Set `LOLAD_TEST_TEMPLATE` to a local template checkout
when the standard DevSSD path is unavailable; without a template that test
reports a skip. On macOS, private temporary wrappers adapt upstream GNU
`realpath -m` and `sed -i` spellings to the built-in BSD tools.

## Agent settings

Open an agent's settings in the dashboard to change its model, skills mode,
secrets, text files, or email accounts. Settings stay in the agent directory.
A running agent shows **Pending restart** when its runtime, env file, skills,
or email differs from the last launch; use **Restart to apply** to resume its
conversation with the changes.

```sh
lolad model iris                         # inspect its model
lolad model iris 'claude-sonnet-5-5[1m]'
lolad skills iris                        # agent skills and the user library
lolad skills iris mode barebones         # only agent skills/project plugins
lolad skills iris mode inherit           # also personal skills/user plugins
lolad skills iris add user:example
lolad skills iris rm agent:example
lolad env iris ls                        # keys, set status and length only
lolad env iris set TOKEN < /private/path/to/token
lolad env iris rm TOKEN
lolad email iris show
lolad email iris test iris@example.com   # IMAP/SMTP login checks; sends no mail
lolad restart iris                       # apply pending settings, keep conversation
```

Env values are read from stdin, never command arguments, and are write-only
through the UI/API/CLI. Use a private file or your secret manager as stdin;
avoid putting a literal token in shell history. Email passwords use the same
private env file. File edits check the version read before saving. Email watch
can be owned by lolad or the agent's mail mod; disable the mod before enabling
lolad watching to avoid duplicate notifications. Model choices are suggestions,
and custom model IDs remain accepted.

An existing config file scans only the `agents_dirs` you explicitly set;
omitting that key discovers only the individual `agents` entries. The built-in
search directory applies when no config file exists. Restarts use the exact
harness session ID recorded by hooks when available, so another conversation
opened in the same directory does not take over an agent's resume.
