# Agent settings backend

The public API matches the `internal/agentcfg` section of SPEC.md, with no
signature changes or additional dependencies. All mutations stay in the agent
directory except when the caller explicitly supplies an absolute env-file path.
Separate `Dir` instances for the same canonical directory share a process-local
mutex, including file hash checks and writes.

## Secret handling

Env and email response structs contain metadata only. Errors never include
configuration parser diagnostics, server replies, or submitted values. The only
secret-returning function is the specified `EmailPassword`, for the service's
TestEmail implementation. TLS certificate and hostname verification remain on;
the TLS tests inject a private trust pool into unexported helpers.

The editor additionally excludes the configured runtime env file (even if its
name has an ordinary text extension), `brain/credentials.yaml`,
`brain/companies-credentials.yaml`, the file named by `paths.credentials` in
lolabot.yaml, and aliases to these files. Excluding credentials is a deliberate tightening of SPEC's file
allowlist: existing credential files can contain legacy plaintext passwords.
This keeps the write-only secret guarantee intact; use Email/SaveEmail for email
configuration. Existing absolute env-file paths remain supported by Env/SetEnv.

Default email secret names are `EMAIL_PASSWORD_` plus the uppercased config key,
with punctuation replaced by `_` so dotted or hyphenated local parts still yield
valid env keys. A supplied PasswordKey must satisfy the env-key grammar.
Existing env passwords take precedence over legacy plaintext during migration.

## Email serialization and tool upgrades

Only the top-level `email:` block in lolabot.yaml is replaced. Bytes before and
after it, including adjacent comments and blank lines, are retained. The email
block itself is serialized in a stable field order. `brain/credentials.yaml` is
parsed and reserialized using yaml.v3: other top-level keys and other fields of
retained accounts survive, but formatting and comments may change. Removed
accounts are removed from email_accounts; their old env variables are retained
until explicitly deleted. Each individual file write is atomic; SaveEmail is
not a transaction across multiple files.

### Email tools (`email_tools.go`)

lolad installs one email tool set into lolabot agents: `assets/email.sh`,
`env_secret.py`, `email_client.py`, `config.py`, `attachment_guard.py` and
`email-send.sh` (tools/<same name>). The Python files are based on a newer
fork of lolabot's client (password_secret, `secrets-needed`, `watch <account>`,
delivery records, attachment allowlist), scrubbed of personal examples and
paths; they carry the lolabot MIT notice (`assets/LICENSE.lolabot`). Local
fixes: the client takes server settings (imap/smtp server and port) from
lolabot.yaml `email.accounts`, where lolad writes them, over the credentials
file, defaults `config_key` to the address's local part and `username` to the
address, and its messages point at lolad instead of aim-secret. `email_sanitizer.py`
is not shipped: the template's copy is used as is (it must exist).

Detection is by exact content (SHA-256 of the file without outer whitespace),
never by guesses:

- **Template set** (lolabot's plain `email.sh` or `email_client.py`, hashes of
  every lolabot release in `email_tools.go`): install the whole set. Any other
  tool file with unknown content blocks the upgrade (nothing is written, a Note
  names the file). `email-send.sh` is optional: replaced only if stock, kept
  with a Note if customised, never added.
- **aim-secret wrapper** (`assets/email-aim-secret.sh`, unchanged behaviour):
  replace email.sh and env_secret.py only.
- **Earlier lolad set**: files whose hash is in a tool's `shipped` list are
  refreshed; everything else stays. When an asset changes, append the old
  version's hash to `shipped` (TestEmailToolsHashLists guards the current one).
- **Customised email.sh** (unknown content): nothing is touched; Note.

`env_secret.py` is lolad's own helper for email.sh (it parses exports, inline
comments, escaped double-quoted values and duplicate keys as agentcfg does,
without shell evaluation) and is restored whenever it differs. Every replaced file is first copied to `<file>.lolad-orig`; an
existing backup is never overwritten, so it keeps the pre-lolad original.
Files are written dependencies first and email.sh last; an interrupted upgrade
is simply planned again.

When it runs: `SaveEmail` (dashboard email settings, mailbox provision, rotate,
detach), and also `Email` (every read of the email settings) when every
password_secret in the credentials is set in the agent's env file, i.e. the
email is lolad-managed and the old tools cannot log in. The read path only does
template and earlier-lolad upgrades, never the aim-secret one.

`assets/email.sh` does not rely on the caller's shell: it adds the usual
install dirs to PATH, uses `.venv/bin/python` when present, and installs PyYAML
into `.venv` when that interpreter lacks it (uv, else pip/ensurepip; it creates
the venv if missing), with progress on stderr and exit 1 on failure. Secrets
come from the exported environment, else the env file (`LOLAD_ENV_FILE`, else
lolabot.yaml `runtime.env_file`, else `.env`), else aim-secret if installed; a
name missing from the env file is left unset so only the account that needs it
fails. Values are never printed.

No live agent files, user settings, or vault services were changed or contacted.
SMTP testing authenticates and quits; it never sends MAIL, RCPT, or DATA.

## Skill discovery

Claude agent skills: `.claude/skills`; Claude user skills: `~/.claude/skills`.
Enabled user plugins are resolved from the v2 installed_plugins.json catalog and
user enabledPlugins map, using their recorded installPath/skills directories.
Codewhale agent skills: `.codewhale-home/skills`; user roots, in precedence order:
`~/.codewhale/skills`, `~/.agents/skills`, `~/.claude/skills`. Duplicate user skill
directory names use the first root. Frontmatter supplies display names and
descriptions; IDs use stable source plus directory basename. `<agent>/skills`
is an instruction folder and is included only in Files, as specified.

AddSkill copies into a temporary directory and dereferences links, rejects
cycles/nonregular files, then renames into place. RemoveSkill accepts agent IDs
only. Fingerprints hash runtime JSON, raw env content, local harness skill
names/mtimes, and the email block plus parsed email_accounts. In inherit mode,
the skills part also includes user skill roots and enabled user plugin roots
(names and recursive mtimes); barebones mode excludes those inherited sources.

## Validation

Package tests cover env metadata/order/comments/quotes/mode/gitignore, custom
secret-file and symlink exclusions, traversal/binary/size/hash-conflict checks,
email byte preservation (including a temporary copy of jugui/lolabot.yaml),
legacy password migration, stock upgrades/custom tool protection, inherited
email secrets, fake-home skill discovery/copy/removal, and each fingerprint
component. Local generated-certificate servers exercise TLS IMAP and SMTP,
SMTP STARTTLS, PLAIN/LOGIN, bad passwords, untrusted certs, and unreachable ports.
The optional jugui fixture test skips if that reference file is unavailable.

Run `go build ./internal/agentcfg/`, `go test -race ./internal/agentcfg/...`, and
`go vet ./internal/agentcfg/...`.

Final verification: build and vet passed; race-enabled tests passed with 85.6%
statement coverage. No public API deviations.
