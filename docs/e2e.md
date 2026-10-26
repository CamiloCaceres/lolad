# Real-agent integration proof — 2026-10-25

All requested checks passed with real Claude Code (Haiku) and Codewhale 0.10.1
(DeepSeek Flash), after the integration fixes below. Times in the transcript
are America/Bogota; API mail timestamps use UTC. Model prompts were short and
limited to mail, ACKs, and the one deliberate policy denial.

## Isolation and setup

The test used a newly created `lolad-e2e-*` temporary directory containing
`config.yaml`, `org.yaml`, `state/`, and `agents/`. No real agent directories
were registered. The daemon listened only on `127.0.0.1:4198`; its private tmux
socket was `lolad-e2e`. Port 4100, PM2, AI Maestro, global Claude settings, and
global Codewhale state were untouched by this worker.

Below, `$E2E` denotes that temporary directory; `$PRIVATE_PROVIDER_ENV` denotes
a coordinator-provided private environment-file path. Commands were executed
with the built `bin/lolad`, using `LOLAD_URL=http://127.0.0.1:4198` for clients.
The environment file was referenced only by path, never printed or copied.

```yaml
# $E2E/config.yaml
listen: 127.0.0.1:4198
tmux_socket: lolad-e2e
agents_dirs: [$E2E/agents]
new_agents_dir: $E2E/agents
```

```sh
LOLAD_CONFIG="$E2E/config.yaml" LOLAD_STATE="$E2E/state" bin/lolad serve
# lolad listening on 127.0.0.1:4198

bin/lolad new E2EHead --slug e2e-head --harness claude --model haiku \
  --on-wake '<short instruction to wait for lolad mail and use lolad msg>'
bin/lolad new E2EMember --slug e2e-member --harness codewhale \
  --on-wake '<same short messaging instruction>'
bin/lolad new E2EIndependent --slug e2e-independent --harness claude --model haiku \
  --on-wake '<same short messaging instruction>'
# All three created by the real setup script and returned registered AMP addresses.
```

The Codewhale runtime was set to `model: deepseek-flash` and
`env_file: $PRIVATE_PROVIDER_ENV`; its generated config used
`provider = "deepseek"` and `[providers.deepseek] model = "deepseek-flash"`.
Only provider/model settings were read from the dsh-lola config. The originally
requested dsh-lola `.env` was tried first, but it contained no provider API key
and Codewhale correctly displayed `DeepSeek needs key`. The coordinator then
provided an authorized private file containing only the provider credential.
With per-agent `CODEWHALE_HOME`, the global credential store is unavailable:
real deployments need their provider key in `runtime.env_file`.

The Claude workspace trust screens were accepted through the isolated tmux
panes. Codewhale's initial working-agreement screen was accepted there too.
The agent project settings, managed hook overlays, and private Codewhale home
were used; the user’s global Claude settings were excluded by
`--setting-sources project,local`.

`bin/lolad org put` received JSON from `GET /api/org` with this added team:

```json
{"id":"e2e","name":"E2E","head":"e2e-head","members":["e2e-member"],"open":false}
```

The user was `tester@e2e.lolad.local`. Defaults allowed user/agent messaging,
vertical messaging, and `independents: user-only`. `e2e-independent` belonged
to no team. The org save regenerated messaging instructions for all agents.

## Commands and observed results

| Required behavior | Result and evidence |
| --- | --- |
| Claude and Codewhale start and reach hook-reported idle | PASS. Head idle event 25, independent idle event 27; Codewhale idle event 111, and after final permission fix idle event 458. Captured hooks include session_start, prompt_submit/pre_tool/post_tool, and stop. |
| User mail produces `[MESSAGE]`, agent reads it, replies through `tell-user` | PASS for all three agents. Actual pane commands and signed API reply IDs below. |
| `tell-user ask` with options and a decision round-trip | PASS. Ask options were `red`, `blue`; decision `blue` was read and acknowledged with `DECISION_ACK_blue`. |
| Head → member is allowed and delivered | PASS. Two real head sends reached Codewhale as `[MESSAGE]`; Codewhale read them and returned `VERTICAL_ACK` and `VERTICAL_LIVE_ACK` to the user. |
| Independent → member is denied, with reason and policy event | PASS. Real agent CLI exited 2 on HTTP 403; reason `default: independents user-only`; persisted event 83 recorded the attempted edge. |
| Killing a harness triggers crash detection and a resumed restart | PASS. SIGKILL to isolated Claude PID 59335 generated crash event 188 and started event 191 `fresh=false`. A repeated check after the resume fix generated crash event 370, started event 373 `fresh=false`, and hook idle event 382. Conversation state was retained. |
| Daemon restart adopts existing sessions | PASS. After SIGTERM and restart, all three pane PIDs were unchanged: head 57848, independent 59335, member 70903. Events 175–177 say `adopted existing session`. |
| `lolad stop` stops cleanly | PASS. Head reached stopped/down with event 393 `graceful stop complete`. Final stop of all three left each stopped/down and no tmux sessions. |
| Mail to stopped agent wakes it | PASS. User mail to stopped head changed desired to up, resumed it at PID 22534, and returned `WAKE_ACK`; idle event 420 followed. |

### User → agent → user

```sh
bin/lolad send e2e-head \
  'Read this mail with lolad msg read, then run lolad msg tell-user say "HEAD_ACK". Do not send any other message.'
# msg_1792978259_3d0b9593c4a75a3e531805a6

# Real head pane:
# ❯ [#531805a6] [MESSAGE] from tester: Message ...
# Bash(lolad msg read msg_1792978259_3d0b9593c4a75a3e531805a6)
# Bash(lolad msg tell-user say "HEAD_ACK")
# msg_1792978263_bf2b95d96fd006ede473a1a3

bin/lolad send e2e-member '<same instruction, requesting MEMBER_ACK>'
# msg_1792978259_705aabdcee26972abc2956bb
bin/lolad send e2e-independent '<same instruction, requesting INDEPENDENT_ACK>'
# msg_1792978259_88c1d57d6ba0b35055670e72

curl -s http://127.0.0.1:4198/api/mail/conversation/e2e-head
# HEAD_ACK, from e2e-head to tester, signed:true, verified:true
curl -s http://127.0.0.1:4198/api/mail/conversation/e2e-member
# MEMBER_ACK: msg_1792979258_b1de7c1f4172f4c0d1885d0e
curl -s http://127.0.0.1:4198/api/mail/conversation/e2e-independent
# INDEPENDENT_ACK: msg_1792978263_82a29f804d42cc355eba0a2b
```

After the Codewhale permission fix, another real user send confirmed the live
notifier path, independently of initial-prompt inbox pickup:

```sh
bin/lolad send e2e-member \
  'Read this mail using lolad msg read then run lolad msg tell-user say LIVE_MEMBER_ACK. Keep brief.'
# msg_1792979299_ba7b60f5a952e7700b03c2dd
# Real Codewhale pane: [#0b03c2dd] [MESSAGE] from tester ...
# run: lolad msg read msg_1792979299_ba7b60f5a952e7700b03c2dd
# run: lolad msg tell-user say LIVE_MEMBER_ACK
# API reply: msg_1792979305_02f273bb81e36d5082a3102a
```

### Ask/decision

The head was sent a short instruction to ask for a color. It executed the
actual CLI syntax (repeated `--option`, rather than a comma-separated flag):

```sh
# Executed by the real head:
lolad msg tell-user ask 'Choose an E2E color' --option red --option blue
# msg_1792978304_ac703223d1e79216e7d89a5b
# API context.userchat = {kind:"ask",options:["red","blue"]}

# Executed as the user:
bin/lolad send e2e-head blue --decision blue \
  --in-reply-to msg_1792978304_ac703223d1e79216e7d89a5b
# msg_1792978361_f284e7b8b74fee4dde5f4f95
# API context.userchat = {kind:"decision",decision:"blue"}
# Same thread_id as the ask.

# Head read the decision and ran lolad msg tell-user say DECISION_ACK_blue.
# API reply: msg_1792978380_b193bc8caf3970cce8262318
```

### Vertical allow and independent deny

```sh
# Requested by user mail, executed by real head:
lolad msg send e2e-member VerticalProbe \
  'Read this message with lolad msg read and send lolad msg tell-user say VERTICAL_ACK'
# msg_1792978415_9f453644b3a5f89330d5e791
# Member pane: [#30d5e791] [MESSAGE] from e2e-head: VerticalProbe ...
# Member read it and sent VERTICAL_ACK:
# msg_1792979258_937fd513308e527b10675100

# Repeated after the final runtime fix, executed by head:
lolad msg send e2e-member VerticalLive \
  'Read this mail with lolad msg read and run lolad msg tell-user say VERTICAL_LIVE_ACK'
# Member API reply: msg_1792979311_3c07b48e59dd74cb31ca76e1

# Requested by user mail, executed by real independent agent:
lolad msg send e2e-member PolicyProbe 'This must be denied'
# Error: Exit code 2
# default: independents user-only

bin/lolad logs e2e-independent --limit 100
# event 83, kind policy:
# e2e-independent → e2e-member: default: independents user-only
# Agent's tell-user report: msg_1792978305_4629eaa771f968673b704971
```

### Crash, daemon adoption, stop and wake

```sh
# PID obtained from isolated /api/agents/e2e-independent:
kill -KILL 59335
bin/lolad logs e2e-independent --limit 20
# 20:35:44 crashed; 20:35:50 started fresh=false; restarts=1
# A subsequent repeat with the corrected resumed-turn prompt:
# 20:45:26 crashed; 20:45:32 started fresh=false; 20:45:36 hook idle.
# Existing mail was read after resume; RESUME_ACK:
# msg_1792978900_440c80fe0e590cefba554e3f

# SIGTERM only the isolated daemon PID, verify panes remain live:
tmux -L lolad-e2e list-panes -a -F '#{session_name} #{pane_pid} #{pane_dead}'
# e2e-head 57848 0
# e2e-independent 59335 0
# e2e-member 70903 0
LOLAD_CONFIG="$E2E/config.yaml" LOLAD_STATE="$E2E/state" bin/lolad serve
# /api/agents returned exactly the same three PIDs; adopted events 175–177.

bin/lolad stop e2e-head
# After graceful deadline: state=stopped, desired=down; event 393.
bin/lolad send e2e-head \
  'Read this mail with lolad msg read then run lolad msg tell-user say WAKE_ACK. Keep brief.'
# msg_1792979222_a2ef0b7fd45d2cfda89ee8fd
# Head resumed (fresh=false), PID 22534; WAKE_ACK:
# msg_1792979227_dccb0d06f6b398f685c482fd
```

## Bugs exposed and corrected

1. Added `registry.Config.TmuxSocket` (`tmux_socket`, default `lolad`) and used
   it for daemon sessions and CLI attachment. No shared core changes.
2. Fixed Codewhale bypass launch semantics. `never` means deny approval-gated
   tools, even when the filesystem sandbox is disabled. The installed CLI also
   rejected `full-access` as an argument, despite the underlying policy parser
   recognizing that name. The final adapter uses `CODEWHALE_YOLO=true`,
   `CODEWHALE_ALLOW_SHELL=1`, and `--sandbox-mode danger-full-access`, and omits
   the conflicting root approval key only in the private per-run overlay.
   Original agent config remains unchanged. The real pane showed `Full Access`
   and shell-based mail subsequently worked. This deliberately corrects the
   SPEC’s empirically incorrect `--approval-policy never` recipe. See
   [upstream policy values](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/execpolicy/src/approval_mode.rs)
   and [startup precedence](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/tui/src/tui/app/init.rs).
3. Added a short inbox-check prompt to resumed supervisor launches. Both real
   TUIs could otherwise resume into an empty composer, emit only session_start,
   and remain `starting`, preventing hook-only mail notification. The prompt
   preserves `--continue`, conversation age, and history; it causes a normal
   turn and a real stop/idle hook. The repeated crash test verified recovery.

Regression tests cover the two runtime fixes, HTTP CLI requests/down-daemon
errors, service composition/identities, org policy/denial events, hook tracker,
mail/conversation/read isolation, and adoption outside scan roots surviving
reload. The app persists those adopted/created external paths as explicit
config entries. `internal/deps` was removed after every pinned dependency had
a real production import. No `internal/web`, `internal/core`, or `go.mod` edits
were made by integration.

## Verification and cleanup

```sh
go build -o bin/lolad .  # PASS
go vet ./...            # PASS
go test ./...           # PASS, no exclusions; dashboard included

bin/lolad stop e2e-head
bin/lolad stop e2e-member
bin/lolad stop e2e-independent
# All three: stopped/down. tmux list-sessions: no server running.
# Explicitly kill lolad-e2e if any server remains, SIGTERM isolated daemon,
# then remove only the temporary E2E directory and worker scratch files.
```

No requested E2E behavior remains failed. Earlier provider-authentication,
Codewhale permission, and resumed-idle failures are retained above with their
observed causes and verified fixes. The provided credential file remains owned
by the coordinator and was not deleted by this worker.

## Final dashboard and viewer follow-up

After cleaning up the real-agent run, a second isolated daemon on the same test
port/socket was started with a non-autostarting, non-waking `e2e-head` fixture.
No model or tmux session was needed for this HTTP rendering smoke test. The real
CLI sent `DASHBOARD_SMOKE` as tester (mail
`msg_1792979807_2f9acd723b555d2502be51ef`); the signed agent-side CLI sent
`DASHBOARD_REPLY` (`msg_1792979807_a07199d929a66401e9a0539b`). Actual dashboard
responses against that daemon were:

| Request | Result |
| --- | --- |
| `GET /` | 200; inbox contains E2E Head and DASHBOARD_REPLY |
| `GET /agents` | 200; contains E2E Head |
| `GET /org` | 200; renders live service organization |
| `GET /inbox/e2e-head` | 200; contains both DASHBOARD_SMOKE and DASHBOARD_REPLY |

Service-composition regression coverage also subscribes before each of four
real provider sends (user→agent, agent→agent, agent→user, system→agent) and checks
that `Subscribe` receives `Kind: mail`, the exact mail ID, and an agent slug.
Supervisor's provider callback publishes mail for agent recipients; App's
provider callback publishes mail to the user under the sender agent slug. Both
use the same supervisor event bus consumed by the dashboard.

A coordinator-requested tmux correction was added: read-only attachments now use
`attach -r -f ignore-size` and ignore `Terminal.Resize`. The regression uses a
unique private test socket, waits for an actual client with `ignore-size`, sends
a resize, and verifies that the 80×24 session remains 80×24. Write-mode resizing
continues to use the PTY. This is the fourth small runtime/config package change
outside integration ownership (in addition to registry, harness, supervisor).
The final Codewhale recipe and resume behavior are also recorded in
`internal/harness/NOTES.md`. The second daemon and its temporary directory were
removed after this smoke test.

## Unattended first-run dialogs — 2026-10-25

PASS with Claude Code 2.1.310 and Codewhale 0.10.1. This follow-up used a new
`lolad-dialogs-*` temporary root, loopback `127.0.0.1:4197`, and the private
`lolad-test-dialogs-e2e` tmux socket. TUI discovery used a separate
`lolad-dialogs-probe` socket, throwaway HOME directories and workspaces.
Port 4100, socket `lolad`, AI Maestro, PM2, the jugui/dsh-lola instances, and the
user's global `.claude/settings.json` were untouched. One short Haiku turn
(`Reply only READY.`) was used; Codewhale made no model call and was given no
provider credentials. A private `uv` stub skipped optional setup downloads.

The discovery probe accepted Claude's real trust screen in a throwaway HOME,
then inspected only its project trust metadata. The installed version writes
`projects[<canonical absolute directory>].hasTrustDialogAccepted = true`.
An explicit `CLAUDE_CONFIG_DIR` relocates `.claude.json` inside that directory;
otherwise the file is `$HOME/.claude.json`. The new launcher atomically merges
only the trust key for the registered agent directory, preserving sibling
projects, MCP entries, authentication metadata, unknown values and numbers.
It refuses malformed JSON and symlinked config files. These writes happen
before every managed launch, including wake, rotation and crash resume.
Global settings remain excluded by `--setting-sources project,local`.

A separate real Codewhale `/setup constitution` screen was captured, then its
recommended agreement was accepted in the probe. The resulting state contained
`schema_version: 1`, `constitution_choice: "bundled"`, and
`constitution_checkpoint_completed_for: "0.9.4"`. That matches the
[versioned state schema](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/config/src/setup_state.rs)
and [checkpoint lane](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/tui/src/tui/setup/mod.rs).
The same fields were present in the newly created agent's private
`.codewhale-home/setup_state.json`. Creation/adoption and launch preparation
seed missing acceptance without replacing existing custom choices or
constitution files. No global Codewhale state is changed, and lolad does not
mark provider onboarding complete or use `--skip-onboarding`.

Actual unattended creation commands, where `$D` is the temporary root:

```sh
# $D/config.yaml: listen 127.0.0.1:4197, tmux_socket lolad-test-dialogs-e2e,
# agents_dirs [$D/agents], new_agents_dir $D/agents.
LOLAD_CONFIG="$D/config.yaml" LOLAD_STATE="$D/state" /tmp/lolad-dialogs-probe/lolad serve
export LOLAD_URL=http://127.0.0.1:4197
/tmp/lolad-dialogs-probe/lolad new Dialogsclaude --slug dialogs-claude \
  --harness claude --model haiku --on-wake 'Reply only READY.' --start
/tmp/lolad-dialogs-probe/lolad new Dialogscodewhale --slug dialogs-codewhale \
  --harness codewhale --on-wake 'Reply only READY.' --start
```

Neither new agent received any manual keystroke. The new Claude agent used its
normal existing authentication and only its own temporary project trust entry
was added to the user's `.claude.json`; that entry was removed during cleanup.
The separate throwaway-HOME probe needed no authentication or model turn.

| Observation | Persisted evidence (America/Bogota) |
| --- | --- |
| Claude starts without trust screen and reaches hook-confirmed idle | Event 6 `started fresh=true`, 21:05:14.779; event 10 `session_start`, 21:05:15.936; event 12 `prompt_submit`, 21:05:16.037; event 13 `idle` and event 14 `stop`, 21:05:17.347. Actual pane displayed `READY`. |
| Codewhale has accepted working-agreement state and reaches provider setup | Event 8 `started fresh=true`, 21:05:14.804; event 9 `session_start`, 21:05:15.095. Actual pane showed `Choose your model provider`, DeepSeek `needs key`. |
| Missing provider credentials become visible instead of hanging | Event 16 `waiting` and event 17 `dialog`, 21:07:16.635. Status Detail: `startup blocked: ┌ Choose your model provider ─…┐`. PID remained 77950, desired stayed up, restarts stayed zero. |

The optional `StartupDialog` Launcher capability handles only positively
identified workspace-trust and working-agreement screens. Claude's real screen
selected `No, exit` by default, so fallback sends Down then Enter; if Yes is
already selected it sends only Enter. Codewhale's specific working-agreement
screen uses Enter. Each acceptance records a `dialog` event and is attempted at
most once per launch. Every two-second reconciliation checks starting panes;
after two minutes a non-ready pane becomes waiting with its first content line
in Detail. SessionStart alone does not mask a blocker, active turns are not
subject to the startup deadline, and ordinary stale-state fallback preserves
startup-blocked details. Hook-confirmed idle remains required for mail typing.
Existing sessions adopted on daemon boot also receive startup inspection,
while a persisted busy turn retains its busy state.

Real captured fixtures are `internal/harness/testdata/claude-trust.txt`,
`cw-working-agreement.txt`, and `cw-provider-chooser.txt`. Regression coverage
checks their exact recognition and key sequences, refusal of provider/tool
prompts, one acceptance per launch, timeout timing, recovery by hook,
ready-composer fallback, busy turns, JSON preservation and concurrent lolad
merges. Full repository validation passed:

```sh
go build ./...
go vet ./...
go test ./...
```

Cleanup stopped both isolated agents, terminated only the test daemon, killed
both private test tmux servers, removed temporary probe/E2E files, and removed
only this run's temporary project entries from `.claude.json` with an atomic
merge. The private `.claude.json.lolad-lock` sidecar is retained for future
lolad writers. No requested behavior remains failed; Codewhale correctly waits
for credentials rather than silently treating provider setup as an idle prompt.
