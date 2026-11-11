# Security policy

lolad runs AI agents with access to your files, shell and credentials, so
security reports are very welcome.

## Reporting a vulnerability

Please **do not open a public issue**. Report privately through GitHub:
**Security → Report a vulnerability** on this repository. Include what you
found, how to reproduce it, and the version (`lolad version`) or commit.

You should get a reply within a week. Fixes are released as soon as practical,
and reporters are credited unless they prefer otherwise.

## Scope

In scope: the daemon, its HTTP surfaces (`/api`, `/v1`, `/v1/federation`, the
remote listener), AMP signing and verification, federation trust and
approvals, secret handling, and the Android client.

Known design limits, not vulnerabilities:

- `/api` and the dashboard are unauthenticated on loopback by design. Anything
  that can reach `127.0.0.1:4100` as you can control lolad.
- An agent can read the secrets in its own `.env`; lolad's write-only rule
  applies to lolad's own surfaces, not to the agent.
- `permissions: bypass` runs a harness without approval prompts.

## Update signing key

Desktop updates are verified with an Ed25519 key. The app only installs an
`update.json` whose signature matches the public key compiled into the build
(`-ldflags -X github.com/CamiloCaceres/lolad/desktop/updater.PublicKeyB64=...`).
Whoever holds the private key can ship code to every installed copy, so treat it
like a root credential.

- Generate it once, on a trusted offline machine:
  `sign-manifest keygen --out lolad-update.pem`.
- The private key lives in two places only: the release workflow's GitHub
  Actions secret (scoped to the release environment) and an offline backup.
  Never commit it, never paste it in chat or logs.
- Backup: keep at least two encrypted copies (for example a hardware-encrypted
  USB drive and a password manager's secure file store) in different physical
  locations. Test that a restored copy signs a manifest that verifies.
- Losing the key means existing installs can never be updated by us again: they
  trust a key we no longer hold, and users must reinstall from a new download.
  Back it up before the first public release.
- If the key may be exposed: rotate. Generate a new key, ship a build that
  contains the new public key through a final update signed with the old key
  (if the old key is still ours), publish a security notice, and revoke the old
  GitHub secret. If an attacker may hold the old key, updates cannot be trusted
  and users must reinstall from the website.
- Builds made without the ldflag contain a public test key and refuse to run the
  updater unless `LOLAD_UPDATE_ALLOW_UNSIGNED=1` is set (development only).
