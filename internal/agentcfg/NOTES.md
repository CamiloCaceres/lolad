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

`assets/email-aim-secret.sh` is a read-only-source copy of dsh-lola's stock
wrapper, used to recognize stock content. Only a wrapper matching that content
(ignoring outer whitespace) is upgraded, protecting custom tools. Original
email.sh and any existing env_secret.py are saved as `.lolad-orig`, without
replacing an earlier backup.

The replacement scripts started as copies of jugui's email.sh/env_secret.py.
Two compatibility fixes are local to these embedded copies:

- env_secret.py parses exports, inline comments, escaped double-quoted values,
  and duplicate keys consistently with agentcfg. It uses no shell evaluation.
- email.sh first reuses already-exported named secrets, allowing the harness's
  custom runtime.env_file to work; standalone runs can select an env file with
  LOLAD_ENV_FILE, otherwise the reference `.env` behavior applies.

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
