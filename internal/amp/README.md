# Local AMP provider integration

```go
org, err := policy.Open(orgPath, agentNames...)
// Handle err; refresh the known names with org.SetKnown after registry reloads.
mailer, err := amp.New(db, stateDir, org.Org().Tenant, org,
    amp.WithAgents(func() []core.Agent { return registrySnapshot() }),
    amp.WithOrg(org.Org),
    amp.WithEventSink(recordEvent),
)
// Handle err. The caller opens/configures/closes db; amp creates only amp_*.
// Ensure identities for every agent, org.Org().User.Name (human), and lolad
// (system) before sending. EnsureIdentity is safe to call repeatedly.
mux.Handle("/v1/", mailer) // ServeHTTP expects the /v1 prefix to remain intact.
```

`Provider` implements `core.Mailer` and `http.Handler`; `Handler()` also returns
it as an `http.Handler`. `OnMail` callbacks each run in their own goroutine,
after the database commit, with independent mail/context snapshots. Policy
denials call the injected event sink with kind `policy`. The provider retains
the policy interface supplied to `New`; pass the policy Store for live edits.
Provider tenant/address namespace is fixed at construction: restart after a
tenant change (previous identity files belong to the previous tenant).

`policy.New(org, names...)` constructs an immutable `core.Policy` snapshot.
`policy.Open(path, names...)` returns a concurrency-safe Store implementing
`core.Policy`, with `Org()`, `Save(org)`, `Reload()`, `SetKnown(names...)`, and
`Subscribe(ctx) <-chan core.Org`. Subscribers receive changed snapshots; a slow
subscriber receives the latest snapshot. `Load(path)` applies defaults before
reading YAML, `Validate(org, names...)` returns warnings separately from errors,
and `Save(path, org)` writes atomically. YAML comments are not retained.

Store mutations are validated, persisted, and published atomically:
`AddTeam(core.Team)`, `UpdateTeam(core.Team)`, `RemoveTeam(id)`,
`SetHead(teamID, name)`, `AddMember(teamID, name)`,
`RemoveMember(teamID, name)`, `AddRule(core.Rule)`, `RemoveRule(index)`,
`MoveRule(fromIndex, toIndex)`. Rule indices are zero-based. Adding a member or
setting a head moves that agent out of any previous team. Removing a referenced
parent/team fails validation; edit the referencing children/rules first or
submit one complete revised org with Save.

## HTTP details and spec additions

The `/v1/route` request is the protocol's **flat** shape (to, subject, priority,
in_reply_to, signature, payload, idempotency_key). Caller-supplied from/id/time
are ignored; the authenticated identity determines from. All agent route calls
require an Ed25519 signature. Human/system route calls without signatures are
signed using their server-managed private key; supplied signatures are always
verified. Context and unknown payload fields survive unchanged in the stored
wire payload. Attachments are unsupported and rejected.

Pending pickup includes unread and notified mail, never marks it read, and
returns `{messages, count, remaining}`. Every message carries its id,
envelope, payload, queued_at, and local status. DELETE pending, batch ack, and
POST read mark recipient mail read. Senders may inspect their sent messages but
cannot acknowledge recipients' messages. Batch acknowledgement is atomic.

Two authenticated history endpoints extend the SPEC endpoint list so the
specified CLI can read older/read messages and show full conversations:

- `GET /v1/messages?box=inbox|sent|all&peer=name&limit=100&before=<RFC3339>`
  returns `{messages,count}`, newest first. Default box is all; limit caps at
  1000. Optional `thread=<thread_id>` filters the returned page.
- `GET /v1/messages/{id}` fetches one message. Only its sender/recipient may
  fetch it. POST `/v1/messages/{id}/read` also returns the message.

Idempotency keys are scoped to the authenticated sender, retained at least 24h,
and stored in the same transaction as their mail. Identical HTTP request bodies
replay the original result; a different body with an unexpired key returns 409
`duplicate_idempotency_key`. A supplied client from/id/time remains part of the
request-body fingerprint even though it never controls the delivered envelope.

`msgcli.Run(ctx, args, stdout, stderr)` reads LOLAD_AGENT/--as, LOLAD_STATE and
LOLAD_URL. Help needs no identity. Every send is signed and every mail operation
uses `/v1` HTTP; local disk reads are limited to credentials/private keys. HTTP
403 prints the server message and exits 2; other failures exit 1. Read resolves
full IDs or unambiguous last-8 suffixes. `who` prints reachable identities.
`tell-user` resolves the human identity from the directory and writes
`core.UserChat` at payload.context.userchat.

## Validation performed

- `go test ./internal/amp/... ./internal/policy/... ./internal/msgcli/...`
- `go vet ./internal/amp/... ./internal/policy/... ./internal/msgcli/...`
- `go test -race ./internal/amp/... ./internal/policy/... ./internal/msgcli/...`
- Protocol clone tests/test-vectors.json payload hash and canonical string
  vectors passed (tests skip only if the clone is absent).
- Real `jq -cS .payload` and `/opt/homebrew/opt/openssl@3/bin/openssl pkeyutl
  -sign -rawin` produced identical canonical bytes/signatures to Go, including
  nested keys, Unicode, HTML characters, and a large integer. No installed AMP
  script was executed and no AI Maestro state was read or written.
- httptest exercises authentication/from derivation, signature failures, policy
  denial/event, body limit, 24h deduplication, conflicting/concurrent retries,
  transaction rollback, pending/ack/read lifecycle, mailbox isolation,
  registration, directory resolution, nonblocking subscribers, and full CLI
  send/inbox/read/reply/thread/tell-user ask with user options.

No core contract changes or dependency changes were needed. Variations from
SPEC are the additive history endpoints, fixed tenant until restart, and YAML
comment loss (explicitly permitted by SPEC). Tests use only loopback HTTP and
temporary SQLite/state directories; messages are database-only.

## Federation (v4)

Attach an `amp.Remote` implementation with `Provider.SetRemote`; `fed.New` does
this automatically. `CheckOutbound(ctx, from, to, inReplyTo)` authorizes foreign
recipients. `Enqueue(ctx, tx, mail, wire)` runs inside the mail/idempotency
transaction and must use that transaction without network calls. The hook also
supplies `Enabled()` and `RemoteContacts(ctx, local)` for `/v1/info`, health, and
the caller's `/v1/agents` directory. A hook implementing the optional
`Check(from, to) core.Decision` method supplies a read-only policy preview in
both directions; the federation engine implements it.

`Provider.AcceptRemote` verifies the sender signature, preserves message/thread
IDs, stores full remote sender names, and deduplicates same-participant IDs;
a different-participant collision is rejected. Authorization and provider/key
pinning are the federation engine's responsibility. `Held` and `ResolveHeld`
allow approval decisions and receipt enqueueing to commit atomically, with
`OnMail` notifications only after commit. `ReleaseHeld` is the single-message
helper. Held mail is excluded from pickup, inbox/history, read/ack, and normal
notifications. `Traffic` remains an owner-level audit view.

Existing databases are migrated additively with nullable `remote`, `delivery`,
and `delivery_error` columns. Old mail survives and repeated migration is safe.
`core.Mail` and the HTTP `amp.Message` wire shape expose these fields. Outgoing
remote mail uses its full recipient address as `ToName`, starts as `queued`,
and commits with an outbox entry. `SetDelivery` protects final delivery states
from stale retry/held responses. `Wire` and `PublicKey` provide the outbound
federation transport with the original signed envelope and public sender key.
