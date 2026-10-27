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
