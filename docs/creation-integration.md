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
`SetupEnv` is an optional complete environment override for the setup subprocess;
normal operation inherits the daemon environment.

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
- On macOS, the real upstream setup script needs GNU `realpath -m` and `sed -i`
  compatibility. Private temporary wrappers translate those spellings to BSD
  tools; no upstream template file is modified. The target directory already
  exists before `realpath` resolves it.
- Setup inputs containing double quotes, backslashes, newlines, `|`, or `&` are
  rejected to protect upstream YAML and sed interpolation. Optional Python
  setup runs normally in production; tests replace `uv` with a failing private
  stub to prevent dependency downloads. Upstream treats that failure as optional.

## Verification on 2026-10-25

- `go test ./internal/create/...` passed, including real setup into temporary
  instances for Claude and Codewhale from a copy of
  `/Users/you/lolabot`.
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
