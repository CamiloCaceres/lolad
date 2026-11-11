# Creator integration and verification

`internal/create.New(create.Deps{…})` returns a Creator implementing
`Create(context.Context, core.CreateRequest) (core.AgentStatus, error)` and
`Adopt(context.Context, core.AdoptRequest) (core.AgentStatus, error)`.
It imports core and the embedded messaging guide, without depending on registry,
AMP, policy, or supervisor implementations.

## Wiring

The dependency interfaces live in `internal/create`:

| Dependency | Required operations |
| --- | --- |
| `Runtime` | `WriteRuntime(dir string, runtime core.Runtime) error` |
| `Identity` | `EnsureIdentity(ctx, name, kind) (core.Identity, error)` |
| `Org` | `Org(ctx) (core.Org, error)`, `SaveOrg(ctx, core.Org) error` |
| `Contacts` | `Contacts(name string) []string` |
| `Mail` | `Send(ctx, fromName, core.Outgoing) (core.Mail, error)`; required for a mission |
| `Starter` | `Start(ctx, name) error`; required for `Start: true` |
| `Registrar` | Optional `Reload(ctx) error` for discovery before identity registration |

Set `TemplateDir` and `AgentsDir` from daemon configuration. Runtime writes are
delegated to registry's byte-preserving writer through an adapter if needed.
`SetupEnv` is an optional complete environment override for the legacy
`setup.sh` fallback only; the native scaffold ignores it.

`Registrar` should refresh registry discovery, without starting an unregistered
agent. Creation writes runtime and harness files, refreshes discovery, ensures
identity, updates organization, regenerates its instructions, sends the optional
mission through policy, and calls the starter if requested. Avoid reconciling an
agent with default autostart before identity, org, and instructions are ready.
Partial failure retains files, returns the available status, and identifies the
failed stage; recovery uses adoption rather than overwriting a directory.

After **any** organization change, reload policy and call
`creator.RegenerateSection(agent)` for **every registered agent**, passing each
full `core.Agent` from the registry (or `status.Agent` from the service's
`List(ctx)` results), not just the agent that moved. The injected `Org.Org(ctx)`
and `Contacts.Contacts(name)` must already read the updated organization and
policy; `RegenerateSection` derives the address and reporting line from that
organization and the allowed recipients from that contacts lookup. Collect and
return or record regeneration errors so stale instructions are visible. This
includes rules and default changes: permitted contacts can change even when
team membership does not. The service's organization save
path owns this fleet-wide loop, since Creator has no registry dependency or
background subscription. Creation also refreshes the new agent's own section
after its membership update. The source template is
[`agent-messaging.md`](agent-messaging.md), embedded by `docs/embed.go`.

## Contract limits and implementation choices

- Core's `AdoptRequest` carries only directory, slug, and runtime. Adoption
  therefore ensures identity and reflects existing organization membership in
  instructions; it cannot receive team/head, mission, or start options. Those
  actions are separate service/dashboard operations. No core types were changed.
- Slugs follow the updated core contract: `runtime.slug`, otherwise sanitized
  `agent.name`, otherwise the directory basename. Adoption preserves an existing
  runtime slug; explicit request slugs override it. The existing `jugui` and
  `dsh-lola` directories consequently use `ultron` and `lola`. Creation always
  writes `runtime.slug`; adoption writes it when it differs from the basename
  or an explicit/stored slug is present, stabilizing identity across renames.
- Existing Codewhale config and launcher files are retained byte-for-byte to
  preserve user settings and credentials. New files follow the local Lola
  reference without credentials. Requested runtime models for adopted configs
  are applied by the harness launch argv; manual launchers keep their local
  provider defaults.
- Historical Maestro guidance remains as user content, but the standard
  upstream section gains an explicit link to the active lolad section and a
  superseded notice. The generated section explicitly disallows Maestro paths.
- The managed section is generated after identity and organization updates so
  its reporting line and contacts reflect the updated policy. All files are
  ready before the explicit starter runs.
- Setup no longer runs a shell. See "Native scaffold" below; the notes about
  GNU `realpath -m`/`sed -i` wrappers and rejected `| & " \` characters now
  apply only to the legacy `setup.sh` fallback.

## Native scaffold (no Bash, no git)

`create.Scaffold(templateDir, ScaffoldParams)` is a Go port of lolabot's
`setup.sh` for a new instance. It needs no Bash, `sed`, `cp`, `git` or Python, so
creation works on a fresh Windows 11 or Mac with no lolabot checkout. Paths use
`filepath` throughout.

What it reproduces (same tree, contents and modes as `setup.sh --yes`):

- the directories (`brain memory indexes emails/<account>/{inbox,sent,drafts,attachments}
  articles/{drafts,published} prompts tools scripts skills tests docs`);
- `cp -n tools/*`: regular files directly in `tools/`, dotfiles and
  subdirectories skipped (shell glob and non-recursive `cp`), permission bits
  kept, never overwriting;
- `cp -rn skills/*`: recursive, top-level dotfiles skipped, no clobber;
- only `brain/{eisenhower,agents,README}.md` and `memory/README.md` (a file the
  template lacks is skipped). `scripts/`, `docs/` and `tests/` are created
  empty, as upstream does;
- `lolabot.yaml`, `CLAUDE.md` (the 20 `{{PLACEHOLDER}}`s applied one after
  another in `setup.sh`'s order), the `{{AGENT_NAME}}`/`{{USER_NAME}}` pass over
  the two brain files, the starter `memory/<first>-profile.md`, `goals.md`,
  `journal-<year>.md`, and `.gitignore`;
- refusal when the target already contains `CLAUDE.md`; the target is resolved
  like `realpath -m` (symlinks in existing parts resolved).

Differences from `setup.sh` (all intended):

| Difference | Why |
| --- | --- |
| No `.venv` (`uv`/`python3 -m venv`, `memvid-sdk`) and no `git init` | They are best-effort warnings upstream and need external tools. The agent can run `pa-onboarding`/its own tooling later; `.gitignore` is still written. |
| Values are written literally, and `"` and `\` are escaped inside the YAML strings | sed treats `&`, `\` and `|` specially and YAML breaks on `"` and `\`; Windows paths contain `\`. Only newlines are rejected. Identical output for every input `setup.sh` handled correctly. |
| System facts are detected in Go (`os.Hostname`, sysctl/`/proc`/`GlobalMemoryStatusEx`, `statfs`/`GetDiskFreeSpaceEx`, `/etc/localtime`); `OS` is `Darwin`, `Linux` or `Windows`; RAM and storage read `16 GB` / `1.8 TB` (not `df -h`'s `460Gi`) | Descriptive text for CLAUDE.md only. Environment presets such as `SYSTEM_RAM` are not read. |
| Timezone falls back to the `/etc/localtime` zone name before `UTC` | `setup.sh` printed `UTC` on macOS. |
| First name lower-casing uses Unicode, not `tr`; the first-name split is on a space | Same results for ASCII names. |
| The "Generated … on <time>" comment is RFC 3339 | `date -Iseconds` differs between GNU and BSD. |
| Nothing is printed and nothing is asked | Creation is always non-interactive. |

`internal/create/scaffold_test.go` runs `setup.sh` and the port into two
directories with identical inputs and compares the whole tree (names, kinds,
modes, bytes), masking only the timestamp comment and the temporary root. It
runs against a synthetic template (every copy rule) and the real lolabot (skips
when bash or the checkout is unavailable; set `LOLAD_TEST_TEMPLATE`).

### Template resolution

`create.ResolveTemplate(create.TemplateConfig{Configured, StateDir})`:

1. a user-set `lolabot_template` that contains `CLAUDE.TEMPLATE.md` wins;
2. otherwise `<state>/lolabot/<version>/` (copied atomically on first use from
   the bundle: `$LOLAD_LOLABOT_BUNDLE`, else `../Resources/lolabot`,
   `../lolabot` or `lolabot/` relative to the executable, so inside `Lolad.app`
   or the Windows install directory; `<version>` is the bundle's `VERSION` file,
   else a 12-hex digest of `CLAUDE.TEMPLATE.md` and `setup.sh`). The bundle is
   never written to; `.git`, `.venv` and `node_modules` are not copied;
3. otherwise an error that names the missing pieces and `LOLAD_LOLABOT_BUNDLE`.

Wiring: set `Deps.Template` to a closure over `ResolveTemplate`. Pass
`Configured` only when the user set it; the registry's built-in default
(`~/lolabot`) must not be passed, or it counts as user-set. `Deps.TemplateDir`
still works and is treated as user-set.

### Legacy `setup.sh` fallback

Only a **user-set** template (never the bundle or state copy) whose `setup.sh`
is not a revision the port was checked against (SHA-256 with CRLF normalised,
`knownSetupSHA256`) runs its own `setup.sh` through bash, with the old macOS
`realpath`/`sed` shims. It is skipped on Windows and when bash is missing; the
Go port is used then. Fields containing `" \ | &` are refused on that path
only. To teach the port a new upstream revision, port the change, extend the
conformance test, then add the digest.

## Verification on 2026-10-25

- `go test ./internal/create/...` passed, including real setup into temporary
  instances for Claude and Codewhale from a copy of
  the lolabot template.
- `go vet ./internal/create/...` passed.
- Integration assertions cover the actual generated tree, parsed runtime block,
  model settings, Claude instruction symlink, Codewhale imports/skill isolation,
  launcher shell syntax and execution with a stub binary, identity/mission/start
  dependency calls, two identical regenerations, and adoption of copied fixtures.
- Additional checks cover invalid requests, non-empty-directory refusal, sorted
  contacts, changed reporting lines, existing project plugins/config/launchers,
  malformed fences without data loss, and recovery from an identity failure.
- Slug follow-up tests cover an Ultron fixture in `jugui`, Lola in `dsh-lola`,
  written `runtime.slug`, directory fallback, retained slugs after display-name
  changes, request overrides, and creation with a slug distinct from both its
  directory and display name; both creation harnesses still run real setup.
- Read the installed `codewhale --help` and `~/.local/bin/claude --help`; the
  documented harness flags are present. Ran the installed Codewhale's
  `config doctor` against a secret-free generated-equivalent config with a
  temporary HOME, config, and workspace: exit 0, credentials/endpoints clean.
- No real agent was started; real jugui/dsh-lola trees were reference-only.
  Template `brain/eisenhower.md` is absent in this checkout, so assertions use
  the actual supplied brain files rather than the script's optional copy target.

Created files: `README.md`, `docs/agent-messaging.md`, `docs/embed.go`,
`docs/creation-integration.md`, `internal/create/create.go`,
`internal/create/files.go`, and `internal/create/create_test.go`.

## Runtime integration findings

Per-agent `CODEWHALE_HOME` isolates Codewhale from the global secret store. Set
`runtime.env_file` to a private file containing `DEEPSEEK_API_KEY` (or the
selected provider's environment credential); the provider/model config alone
does not authenticate. Keep that file outside generated instructions and source
control. The harness loads it directly into the child environment.

For `runtime.permissions: bypass`, Codewhale 0.10.1 needs
`CODEWHALE_YOLO=true`, `CODEWHALE_ALLOW_SHELL=1`, and
`--sandbox-mode danger-full-access`. Its `--approval-policy never` is a deny
posture, and its CLI rejects the `full-access` alias even though the underlying
policy parser recognizes it. An explicit root `approval_policy` also prevents
the compatibility environment setting from selecting Full Access. The adapter
therefore omits the approval flag and removes the root approval key only in the
private per-run overlay, preserving the source config. This was verified with
real `lolad msg read` and `tell-user` execution in the TUI. See
[the upstream 0.10.1 policy parser](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/execpolicy/src/approval_mode.rs)
and [startup policy precedence](https://github.com/codewhale-hq/CodeWhale/blob/v0.10.1/crates/tui/src/tui/app/init.rs).

## Dashboard creation flow (v3)

The New agent screen leads with name and purpose. The purpose is queued as the
first task mail and, unless custom `on_wake` instructions were supplied, is
included in the runtime wake-up prompt for future fresh conversations. A role
label remains a separate optional setup field. Claude Code, the harness default
model, inherited skills, unattended permissions and immediate start are the UI
defaults. Advanced options retain explicit slug, directory, operator, team/head
and custom wake-up instructions.

`CreateRequest.skills` accepts `inherit`, `barebones`, or empty (inherit).
Creator validates it and writes it into runtime before identity/mission/start,
so the first launch already uses the selected skill isolation. A creation with
`start: false` writes both `autostart: false` and `wake_on_message: false`; its
queued first mission must not silently wake it. The operator can start it from
the conversation or change these settings later.

`Service.CreateOptions(ctx)` returns `core.CreateOptions{DefaultDir, Candidates}`;
`GET /api/create/options` exposes the same JSON shape behind the normal API auth
boundary. The real service scans configured `agents_dirs` and `new_agents_dir`
one level deep, plus explicit `agents` paths, deduplicates/sorts them, and reads
only `lolabot.yaml`. Candidate fields are `dir`, `name`, `slug`, `harness`,
`model`, `registered`, and optional `error`. It checks for `CLAUDE.md` without
reading it. Malformed YAML errors are generic, since parser diagnostics can
otherwise echo credential-bearing source lines. Registered directories appear
under a collapsed disclosure; incomplete directories show the recovery needed.

The page previews its destination, first message and generated files. Known slug
and directory collisions are checked locally and again through the service
before creation; the Creator still owns the final nonempty-directory check.
Setup is synchronous: the page shows indeterminate progress for the outstanding
request, without claiming individual stages have completed. Failure keeps every
entered value and offers adoption of partially created files. A lost connection
asks the operator to check Agents before retrying, since completion is uncertain.
Successful creation opens `/inbox/<slug>?created=1`; adoption opens
`/inbox/<slug>?adopted=1`, with optional Email and Org links in the arrival banner.
Adoption stays stopped and does not send a new mission.

Frontend verification uses only webdev's fake service on `127.0.0.1:4198`.
Screenshots are named `internal/web/testdata/screenshots/v3-new-*.png` and
`v3-new-adopt-*.png`. The in-memory fake exposes ready, incomplete and registered
examples, and applies creation skills/autostart without touching agent files.
