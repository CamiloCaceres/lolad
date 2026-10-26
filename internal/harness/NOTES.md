# Runtime verification

Verified on the development Mac on 2026-10-25 (UTC logs cross into 2026-10-26).
Only throwaway directories and unique `lolad-test-*` tmux sockets were used.
Neither the real dsh-lola/jugui sessions nor their configurations were changed.
The reference `dsh-lola/lola` and `.codewhale-home/config.toml` were read only.
No credentials were copied into the throwaway Codewhale home.

## Claude Code 2.1.310

Read `~/.local/bin/claude --help` and `--version`, bypassing the zsh function.
The real help confirms `--continue` / `-c`, `--resume` / `-r`, `--model`,
`--permission-mode bypassPermissions`, `--setting-sources project,local`,
`--settings <file-or-json>`, and a positional initial prompt. Resume uses
`--continue` because lolad resumes the last conversation for the working dir;
a fresh launch omits it. Model identifiers, including `[1m]`, stay single argv
values. Adapter.Binary is the `claude_bin` wiring point; Adapter.Path supplies
the configured PATH without consulting shell functions.

An interactive Haiku session in the throwaway directory first showed its
workspace trust dialog. After selecting “Yes, I trust this folder”, the
`SessionStart` command in an explicit `--settings overlay.json` wrote
`overlay-fired` to a marker file. Thus overlay hooks do execute with only
project/local sources. The overlay installs SessionStart, UserPromptSubmit,
Stop, Notification, PermissionRequest, SessionEnd, PreToolUse and PostToolUse.
Each command calls the quoted absolute lolad binary, carries LOLAD_AGENT,
and has a three-second harness timeout; Run has its own 2.5-second budget.

A second session enabled the already installed
`skill-creator@claude-plugins-official` only in the throwaway project's
`.claude/settings.json`. With `--setting-sources project,local`, debug logs
reported `Loaded 1 skills from plugin skill-creator default directory` and
five enabled plugins (four were built-ins). Project enabledPlugins therefore
loads without merging it into the overlay. An earlier deliberately missing
marketplace entry was also recognized and skipped as orphaned. The user's
global settings file was never read or edited by this work, and its hooks
were excluded by the launch flag. Claude itself reads installed plugin
metadata and may register the temporary project's plugin scope there.
Evidence is retained in `testdata/claude-plugin-hook-evidence.txt`.

The actual idle composer has a `❯` row between horizontal rules, including
nonbreaking spaces and a suggestion placeholder. Trust/permission dialogs
and `esc to interrupt` are rejected. Readiness also permits wrapped composer
text so delivery can recognize and clear staged notices.
`testdata/claude-idle.txt` and `claude-initial.txt` are real pane captures;
the latter is the trust dialog. No model request was necessary for these checks.

## Codewhale 0.10.1 (fead51eeea0f)

Read `codewhale --help`, `run --help`, `config --help`, `features list`, and
`--version`. Root flags include `--continue` / `-c`, `--resume` / `-r`,
`--approval-policy never`, `--model`, `--fresh`, `--config <path>`, and a
positional initial prompt. `run` is a forwarding command; the adapter uses
the root TUI directly. `--set` only advertises specific runtime keys, so it
is not used to inject hooks. `features list` has no hook feature gate.

The interactive `/hooks list-events` output lists:

- session_start, session_end, turn_end, message_submit
- tool_call_before, tool_call_after, mode_change, on_error
- subagent_spawn, subagent_complete, shell_env
- session_idle, session_error, waiting_for_user, session_busy

Hooks are a TUI runtime feature; exec, the CLI dispatcher, app-server, and
workflow do not fire them. Only message_submit, tool_call_before, and shell_env
can steer a turn. turn_end is observer-only and cannot force continuation.
The command output is retained in `testdata/cw-hook-events.txt`.

A throwaway CODEWHALE_HOME containing only the following config successfully
ran a SessionStart marker command. `config doctor` accepted the timeout and
continue-on-error settings without warnings:

```toml
telemetry = false
[[hooks.hooks]]
event = "session_start"
command = "true"
timeout_secs = 3
continue_on_error = true
```

A native session_start command received empty stdin and
CODEWHALE_SESSION_ID / DEEPSEEK_SESSION_ID environment variables. A
message_submit command received JSON on stdin (`event`, `text`, `session_id`,
`workspace`, `mode`, `model`, `total_tokens`, and text size fields) plus
DEEPSEEK_MESSAGE in the environment. The sanitized actual payload is
`testdata/codewhale-message-submit.json`. Run handles both JSON and empty
stdin, with CODEWHALE_SESSION_ID fallback, and emits no Claude continuation
JSON for Codewhale.

Least invasive installation: write a 0600 per-run config overlay at
`<state>/agents/<slug>/codewhale-config.toml`, preserving the existing agent
home config and appending native `[[hooks.hooks]]` entries. Pass it with
`--config`; keep CODEWHALE_HOME pointing at the agent's `.codewhale-home`
for its sessions, prompts and plugins. If that home does not exist, use a
managed home under state rather than the user's global home. The source
config is never edited. Explicit per-run `--config` avoids project hook
approval and avoids repeated block mutations. A source config that itself
contains credentials remains private in the 0600 managed overlay; no source
config or credentials were copied for empirical tests.

The adapter enables the base-prompt override only if the selected home's
`prompts/constitution.md` exists, matching the reference launcher.

The actual idle composer has `│❯` within a rounded box, with a `╰` lower
border. `cw-idle.txt` and `cw-initial.txt` are real captures. Without credentials
the TUI opens and reports “model not connected”; submitting the probe text
fired message_submit, then opened provider onboarding. That pane is retained
as `cw-no-provider.txt` and is not prompt-ready. No provider credentials were
copied, no model call was attempted beyond that local preflight, and
turn_end/tool hooks during a paid model turn remain unverified.

## Integration and limits

- `tmux.New(stateDir, socket)` writes tmux.conf; empty socket means `lolad`.
  All commands select that private socket and exact session/pane targets.
  A one-element argv is prefixed with `/usr/bin/env --`: tmux otherwise
  interprets a lone argument as shell text, and env execs the executable
  without a shell or any additional arguments to the program. Harness
  launches already have multiple arguments and need no wrapper.
- `harness.New(kind, binary)` implements core.Launcher. Empty binary resolves
  through the effective PATH. Env precedence is process, env file, runtime.env,
  then managed identity variables. Env files support literal KEY=VALUE,
  optional export and paired quotes; they never execute shell code.
- LOLAD/AMP credentials and inherited Orca hook transport variables are removed
  from process env. LOLAD_STATE is explicitly set for agent messaging with a
  nondefault state directory. The tmux layer also blanks absent server env
  values per session, including when adopting a preexisting server.
- `hooks.NewHandler(sink)` mounts at `/api/hook`; `hooks.Run` implements the CLI.
- `deliver.New(sessions, launcher, stateCallback)` needs the last
  hook state and timestamp. Nil callback refuses typing; pane readiness alone
  cannot satisfy the hook-reported idle requirement. `deliver.NewRouter` takes
  a launcher map and selects the recipient harness. `deliver.NewTracker(sink)`
  wraps HookSink and provides the State callback independently of supervisor
  locks: construct with nil Sink, pass tracker.State to NewRouter, construct
  the supervisor, assign tracker.Sink, and mount hooks.NewHandler(tracker).
  Do not call supervisor.Get from StateFunc: delivery can run under its lock.
  The supervisor owns queue
  policy and scheduling. The notifier serializes attempts, dedupes for ten
  seconds and retries a staged notice once after BSpace clearing.
- No SPEC requirement was intentionally omitted. The lone-argv native env
  wrapper above is a necessary tmux-specific implementation detail.
  Additions are the independent hook-state tracker/router, Pre/PostToolUse
  wiring, Codewhale idle/busy/waiting hooks, managed Codewhale home fallback,
  and LOLAD_STATE propagation. Codewhale paid-turn observation remains the
  empirical limitation described above.

Validation: package tests and go vet cover these four packages only. Tests
exercise real private tmux sockets, direct argv/env, dead panes/status,
PTY attach/resize, adopted-server environment isolation, hook translation,
HTTP replies/fail-open behavior, captured TUI readiness, and a raw-mode fake
TUI in real tmux for confirmed, staged-with-retry, always-staged and failed
delivery, ten-second dedupe, permission refusal and hook-state freshness.
Every test socket is killed in cleanup.

## Integration E2E corrections (2026-10-25)

Real DeepSeek turns exposed that Codewhale 0.10.1 `--approval-policy never`
means deny approval-gated tools, including `bash`; adding a filesystem sandbox
flag alone does not fix that. Its CLI rejects `--approval-policy full-access`,
even though the underlying policy parser supports the alias. The tested daemon
recipe is `CODEWHALE_YOLO=true`, `CODEWHALE_ALLOW_SHELL=1`, and
`--sandbox-mode danger-full-access`, omitting the CLI approval flag. Since an
explicit root `approval_policy` prevents the compatibility environment setting
from selecting Full Access, bypass launches remove that root key only from the
private per-run overlay; the source config is preserved byte-for-byte. Actual
panes showed Full Access and executed `lolad msg read` and `tell-user`, with
signed replies visible through the conversation API. This supersedes the
unverified `never` recipe above. See `docs/e2e.md` for command/result evidence.

Per-agent `CODEWHALE_HOME` does not see credentials stored in the user's global
Codewhale secret store. Supply the provider environment variable (for DeepSeek,
`DEEPSEEK_API_KEY`) through `runtime.env_file`, referencing a private file without
copying it into instructions or configuration overlays.

Both TUIs can resume directly into an empty composer without firing a fresh
idle hook. The supervisor now supplies a brief inbox-check turn while retaining
`--continue`, so a real Stop hook re-establishes idle delivery after a crash.

Read-only tmux attachments use `attach -r -f ignore-size` and ignore Resize,
so a dashboard viewer cannot shrink the supervised terminal. Write attachments
retain PTY resizing.


## Unattended startup dialogs (2026-10-25)

Claude Code 2.1.310 was checked with a throwaway HOME and explicit
CLAUDE_CONFIG_DIR, with onboarding metadata seeded solely to reach trust.
Accepting the real workspace screen wrote
`projects[<canonical absolute workspace>].hasTrustDialogAccepted = true`.
No supported workspace-trust acceptance flag is advertised by `--help`.
The adapter merges only that key before every managed launch, covering new,
adopted, wake, rotation and crash-resume paths. Default location is
`$HOME/.claude.json`; an explicit CLAUDE_CONFIG_DIR uses
`$CLAUDE_CONFIG_DIR/.claude.json`. It resolves workspace symlinks as Claude does.
This is deliberately an exception to the normal per-agent-overlay-only rule:
project trust lives outside settings overlays. Unknown JSON values and sibling
projects are retained, malformed JSON and symlinked config files are refused,
and writes use a private temporary file plus atomic rename. A sibling
`.lolad-lock` serializes lolad writers across processes; external Claude writers
do not participate in that lock, so it does not promise transactional updates
against simultaneous external config edits. Already trusted entries are not
rewritten. The user's global settings.json is never changed.

Codewhale 0.10.1 stores its working-agreement decision in
`$CODEWHALE_HOME/setup_state.json`: schema_version 1,
constitution_choice `bundled`, constitution_checkpoint_completed_for `0.9.4`.
The checkpoint lane differs from the binary version. Verified against its
[versioned setup state source](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/config/src/setup_state.rs)
and [checkpoint implementation](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/tui/src/tui/setup/mod.rs).
Creation and adoption seed only absent/unset choices in the private agent home;
launch preparation also covers existing registered agents and managed fallback
homes. Existing custom choices, checkpoint versions and constitution files
remain intact. Provider onboarding is intentionally still visible when a key is
missing; `.onboarded` and `--skip-onboarding` are not set by lolad.

Adapter.StartupDialog is an optional Launcher capability; no shared core change
was needed. Real pane fixtures are claude-trust.txt (No selected by default)
and cw-working-agreement.txt (Enter use recommended). Claude fallback checks
which row is selected and sends Down/Enter or Enter accordingly. Codewhale
fallback accepts only its specific working-agreement heading and explanation.
Provider, authentication and tool approval dialogs are never automatically
confirmed. The supervisor inspects starting panes on its two-second tick,
accepts each recognized dialog at most once per launch, and records a `dialog`
event. At two minutes, a non-ready startup becomes waiting with a bounded first
content line in Detail and an event. Busy turns leave startup detection; a
session_start hook alone does not hide a blocker. Ready-composer fallback can
update displayed state, while mail delivery still requires its independent idle
hook tracker. Adopted sessions get the same startup inspection. Blocked Detail
survives the ordinary stale-hook fallback until another hook or restart.

The isolated real `lolad new` verification and cleanup are recorded in
[docs/e2e.md](../../docs/e2e.md#unattended-first-run-dialogs--2026-10-25).
