# Federation engine integration

`fed` owns the provider key, connection/grant configuration, invite hashes,
sender-key pins, inbound idempotency tombstones, and durable delivery/receipt
outbox. It implements `core.Federator` and `amp.Remote` without new dependencies.

```go
engine, err := fed.New(fed.Options{
    DB: db, StateDir: stateDir, ConfigDir: configDir,
    Provider: mailer, Enabled: config.Federation.Enabled,
    Display: config.Federation.Display, URL: config.Federation.URL,
    Org: policyStore.Org, Agents: registry.List,
    EventSink: recordEvent,
})
// Handle err before serving requests.
engine.OnApproval(notifyApproval)
engine.OnChange(notifyMailDeliveryChanged)
mux.Handle("/v1/federation/", engine.Handler())
// Mount on both listeners, outside device authentication; provider signatures
// authenticate every request. Preserve the full URL path.
go engine.Run(ctx)
```

`New` attaches the AMP hook itself. Ensure local participant identities as usual.
Run one outbox loop per engine and wait for it to return before closing the DB.
The loop stops with its context; all HTTP calls carry that context. `HTTPClient`,
`Clock`, and `Backoff` options support loopback/fake-clock tests. Default request
timeout is 15 seconds and redirects are disabled. Outbound `Send` performs no
network I/O: mail, HTTP idempotency record, and its outbox entry commit together.
The outbox tries immediately, then after 10 seconds, 30 seconds, 2 minutes,
10 minutes, 30 minutes, and hourly, for at most seven days. Network failures,
429, 5xx, and malformed acknowledgments retry; other 4xx fail permanently.

All `core.Federator` methods are available. Additional methods:

- `Handler() http.Handler`: signed pair/deliver/receipt/directory/unpair endpoints.
- `Run(context.Context)`: blocking outbox loop and approval recovery.
- `OnApproval(func(core.Grant))`: asynchronous pending-approval notification.
- `OnChange(func(core.Mail))`: asynchronous outbound delivery-state notification.
- `Check(from, to string) core.Decision`: read-only federation policy preview,
  including remote senders; unlike sending, it does not initiate a grant.
- `Reload() error`: reload `connections.yaml`, retaining the last valid snapshot
  if parsing or validation fails. YAML changes are serialized with API writes.

Provider key files live in `<state>/federation/`; the private key is created
once with mode 0600 and is never returned by an API. `connections.yaml` uses
snake_case field names, atomic mode-0600 replacement, and preserves public-key
pins and grants independently of the database. YAML comments are not retained.
Deleting the database loses queued mail, invite hashes and sender-key pins, while
paired provider keys and approved/blocked grants remain in configuration.
Invites return their code once; only a SHA-256 hash is stored in `fed_invites`.
Pairing consumes the invite once, with idempotent retries for the same pinned
provider/key. Invite codes and provider private keys must never enter logs.

The configured URL is the reachable listener base URL (usually the HTTPS
Tailscale URL); plain HTTP is restricted to loopback hosts. The app supplies its
`remote.public_url` fallback. Without a URL this engine can accept a reachable
peer's invite, but cannot create an invite or receive peer-initiated delivery
until it advertises a reachable endpoint. Pairing rejects identical provider
names and key changes. A removed-by-peer connection stays listed and paused
until locally removed. Pausing preserves queued messages for later retry.

Remote participant names inside AMP are full addresses. Local names remain
bare. Incoming mail retains its ID and thread across both providers and verifies
both provider and sender signatures. A sender key is pinned on first sight.
Held mail is excluded from inbox, history, pending pickup, read/ack operations,
and notifications; the owner sees it through grants/traffic. Approval or block
commits held-mail changes, idempotency tombstones and receipt outbox entries in
one transaction, then notifies released mail through AMP `OnMail`. Startup
recovers grant decisions interrupted before that transaction committed. Adding
an explicit pre-approved share also releases that pair's pending mail.

Inbound limits are 100 messages per minute per provider, 20 per recipient within
a provider, 50 pending grant pairs per provider, and 10 failed pairing attempts
per hour per source address. Signed duplicate deliveries are idempotent and do
not consume the message rate budget. The directory caches peer contacts for
about 60 seconds and adds approved remote conversation participants. Events
contain only federation metadata; no message body or invite code is logged.

Validation uses temporary SQLite databases, state/config directories, and
loopback `httptest` servers. No live daemon, external service or agent directory
is accessed:

```sh
go build ./...
go vet ./internal/fed/... ./internal/amp/...
go test ./internal/fed/... ./internal/amp/...
go test -race ./internal/fed/... ./internal/amp/...
```
