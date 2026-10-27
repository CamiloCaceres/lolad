# Agent email

Open **Email** (`/email`) to see every agent's external address, provider, watch
owner, pending restart, and latest provisioning/rotation connection test in this
daemon session. External unread counts and watcher check times are not currently
available; these are distinct from lolad's AMP inbox counts. Connection tests log
in to IMAP and SMTP over TLS and send no messages.

Choose **Provider settings**, pick Purelymail, set its API token, your mail
domain (for example `mail.example.com`) and operator sender addresses (for
example `operator@example.com`), and save. The address pattern defaults to
`{slug}@{domain}` and lolad watches new mailboxes. A fresh install starts on
the `manual` provider. Confirm an agent's prefilled address
and choose **Give an email**. lolad creates a mailbox with a random 256-bit
password, saves its account via the existing agent settings path, upgrades the
known stock email tool when needed, and tests its connection. An existing mail
mod keeps ownership; otherwise the default watch mode applies. Restart the agent
to load its changed environment. Unsupported agents need lolabot's email tool
installed before provisioning.

For any other provider, choose **Manual** and use **Attach existing mailbox** in
the agent's Email tab. Supply the address, TLS server names/ports, login, and
password. Existing accounts are not changed by global defaults. Manual accounts
are managed at their provider; automatic reset/deletion is only for Purelymail.

The Email tab also supports **Rotate password** and **Detach mailbox**. Detach
normally leaves the remote mailbox and environment secret intact. Deleting the
remote Purelymail mailbox requires both selecting the deletion checkbox and
typing its full address; deletion permanently removes its mail. Delete unused
password keys separately under Secrets after checking they are no longer used.

## Storage and security

- `<state>/email.json`: provider, domain, pattern, default operator senders and
  watch mode. This is daemon state rather than daemon config YAML (0600).
- `<state>/secrets/purelymail-token`: raw provider token, 0600 in a 0700
  directory. API/UI expose only `token_set`, with a replacement input.
- Agent `lolabot.yaml`: account addresses and servers; `brain/credentials.yaml`:
  usernames and password key references; the agent's configured env file
  (`.env` by default): passwords, 0600. No response displays a password.
- `<state>/email-recovery/<slug>.json`: non-secret mailbox operation journal.
  Generated credentials are staged only in the agent environment. Keep this
  journal and the environment together when recovering an interrupted operation.

If mailbox creation succeeded but agent saving failed, retry the same agent and
address. A confirmed create is not repeated, including after daemon restart.
If a network interruption makes the creation result uncertain and the address
now exists, lolad stops instead of taking over an existing mailbox: verify it
with the provider and attach manually. A pending operation blocks a different
address until resolved; remove its journal only after operator verification.
If rotation reaches the provider but agent saving fails, the replacement remains
in the agent env under a unique `EMAIL_PASSWORD_LOLAD_ROTATE_*` key; retry rotation
to repair configuration. A failed provider reset leaves the active credential
reference unchanged. Spare staging keys may be removed from Secrets afterward.

Provider response bodies and network errors are never echoed, since providers
may include credentials. API tokens and passwords are write-only on all surfaces.
The token authorizes mailbox creation/deletion and should be scoped as narrowly
as Purelymail permits. Filesystem access to state and agent env files remains
privileged. Back up private state securely.

## API and CLI

`GET /api/email`, `GET/PUT /api/email/provider`, and
`POST /api/agents/{name}/email/provision`, `/rotate`, `/detach` use JSON.
Provider PUT accepts the settings fields plus optional `token`; a token-only
update retains settings. Provision accepts optional `address` and `watch` (`lolad` or `none`); a detected mail mod keeps ownership. To attach manually,
provide `address` plus `account` containing `imap_server`, `smtp_server`, optional
ports/username, and write-only `password`. Detach accepts `address`, optional
`delete_mailbox: true`, and `confirm` equal to that address. Existing per-agent
email settings and test endpoints remain supported.

```sh
lolad email ls
lolad email provider
lolad email provider set-token < /private/provider-token
lolad email iris provision
lolad email iris provision iris@mail.example.com
lolad email iris rotate
lolad email iris detach
lolad email iris test iris@mail.example.com
```

`set-token` reads one token from stdin only; extra arguments are rejected without
printing their values. Never put a token in argv, shell history or a URL.

## Provider contract and development

The Purelymail client uses authenticated JSON POSTs to
`https://purelymail.com/api/v0/<method>` with `Purelymail-Api-Token`.
The supplied `/docs/api` URL returned 404 on 2026-10-26. Request/response shapes
were verified against Purelymail's official current
[Swagger specification](https://news.purelymail.com/api/swagger-spec.js):
`createUser` splits `userName` (local part) and `domainName`; `modifyUser` resets
using full `userName` and `newPassword`; `deleteUser` takes full `userName`;
`listUser` returns `result.users` strings; `listDomains` returns domain objects.
Unknown/error responses stop the operation and expose only a redacted error.

Tests use httptest and temporary agent directories; never a live provider token.
Webdev supplies an in-memory provider, seeds Ultron on Purelymail with mod watch
and Iris without an address, and supports the same provisioning flow. Its private
mail state is temporary and is unrelated to real daemon state.
