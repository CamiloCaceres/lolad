# Connect two lolads

Each person runs their own lolad and keeps their own agents, mailbox, keys and
organization. Pairing two lolads lets their humans message one another and
lets them reach agents the other person explicitly shares. Each new remote
sender needs approval for each local recipient. Pairing does not give access
to agent terminals, settings, files, secrets, device sessions or the local AMP API.

## Prepare both machines

Give each organization a different `tenant` in `org.yaml`, such as `milo` and
`jugui`. Addresses then look like `ultron@milo.lolad.local` and
`jugui@jugui.lolad.local`. Matching tenant names cannot pair: rename one first.

On each machine configure an HTTPS origin the other machine can reach:

```yaml
remote:
  listen: 127.0.0.1:4101
  public_url: https://your-machine.your-tailnet.ts.net:4100
federation:
  enabled: true
  display: Milo
  url: https://your-machine.your-tailnet.ts.net:4100
```

Federation is enabled by default. `display` defaults to the first word of the
organization user's display name; `url` defaults to `remote.public_url`.
Restart lolad after changing configuration. An empty URL prevents creating
invites; accepting an invite still requires reaching its origin. HTTP is
permitted only for loopback origins, for isolated tests on one machine.

Set up Tailscale Serve to proxy the HTTPS origin to the remote loopback
listener. Federation lives at `/v1/federation/` on both listeners and uses
provider signatures instead of dashboard device authentication. Every other
remote-listener rule is unchanged: local AMP `/v1/route`, participant credentials
and harness hooks are not exposed there. Device pairing (`lolad pair`) and
lolad pairing (`lolad connect`) are separate operations.

Both directions must work. **Tailscale node sharing is one-directional.** If
Jugui shared his Windows device into Milo's tailnet, that alone does not make
Milo's Mac reachable from Jugui's device. Share the Mac with Jugui as needed
and check the tailnet ACLs and each machine's HTTPS origin from the other machine.

## Pair once

On Milo's machine:

```sh
lolad connect invite
```

This one response contains an invite code. Treat it like a password: give it
to Jugui through a private channel, never a public issue, log or command
argument. It expires after 72 hours and can be used once. Invitations are
not available from the connections list or any GET endpoint.

Jugui accepts on his own machine, reading the code from stdin. Put only the
`lolad-invite:...` code (not the surrounding JSON) in a private file:

```sh
lolad connect accept < /private/path/to/invite-code
lolad connections
```

Delete the private invite file after pairing. Do not paste the code as an
argument to `accept`: the CLI refuses that so it cannot enter process arguments
or shell history. The Connections dashboard also supports invite/accept.

User reachability starts disabled. To allow the other lolad's participants to
request approval to message your human inbox, enable **User reachable** in
Connections, or use the trusted local API on each machine:

```sh
curl -X PATCH http://127.0.0.1:4100/api/connections/jugui.lolad.local \
  -H 'Content-Type: application/json' -d '{"user_reachable":true}'
```

On Jugui's machine the provider in that URL is `milo.lolad.local`.

Compare the peer's provider name and displayed key fingerprint with the other
person. Pairing pins the provider keys carried by the invite and the signed
acceptance; it does not trust whichever server answers an unsigned request.

## Share agents and approve senders

On Milo's machine:

```sh
lolad connect share jugui.lolad.local ultron
lolad connections
```

Jugui can discover Ultron in his dashboard contacts. His own agents see remote
contacts grouped by lolad in `lolad msg who`, and can use full addresses when
allowed. Jugui sends from his own dashboard or user CLI:

```sh
lolad send ultron@milo.lolad.local "Can you review this?"
```

The first message is held on Milo's lolad. It is not in Ultron's inbox and does
not wake or notify Ultron. Milo sees an approval card in Inbox and, when ntfy
is configured, a push such as “Jugui wants to message Ultron: Can you review
this?”. Jugui sees the outbound message as held until Milo decides:

```sh
lolad approvals
lolad approve jugui.lolad.local jugui@jugui.lolad.local ultron
# Or reject this sender for this recipient:
lolad block jugui.lolad.local jugui@jugui.lolad.local ultron
```

Allow releases held mail and marks Jugui's copy delivered. The approved pair
can then message in both directions. Block discards held mail and reports
blocked to the sender. Remote mail is labelled external in the dashboard,
`lolad msg read`, and agent session notices. Message IDs and reply threading
remain the same on both machines.

To pre-approve specific remote participants while sharing:

```sh
lolad connect share jugui.lolad.local ultron \
  --with jugui@jugui.lolad.local --with ana@jugui.lolad.local
lolad connect unshare jugui.lolad.local ultron
lolad connect pause jugui.lolad.local
lolad connect resume jugui.lolad.local
lolad connect rm jugui.lolad.local
```

Sharing replaces that agent's pre-approved address list. An empty list allows
new remote senders to request approval; it does not silently approve them.
Removing the final share removes that agent's remote reachability. User
reachability and the full shares list are also editable in Connections or
through `PATCH /api/connections/{provider}`.

A local human may initiate mail to a connected peer; the receiver still applies
its approvals. An agent needs an allowed pair or a reply to incoming mail.
The system sender `lolad` never writes remotely. A paused peer keeps queued
mail for later; removing a peer fails its pending outbound mail.

## Delivery and troubleshooting

`queued` means the peer has not yet accepted the message; `held` means it is
waiting for approval there; `delivered`, `blocked`, and `failed` are final
outcomes for that attempt. Offline/network failures, rate limits and paused
peers retry with backoff for up to seven days. A restart preserves the outbox.
Inspect Traffic and Connections for delivery errors, queue counts and last
successful contact. `lolad doctor` checks the provider key's presence, the
configured URL, self-reachability, and each peer's last error.

If mail stays queued, check both daemons, both URLs, Tailscale ACLs, shared-node
reachability, certificates and clock synchronization. Signed requests allow
five minutes of clock skew. A URL reachable from its own machine may still be
unreachable from the peer: test both directions. `held` requires an approval
on the recipient's lolad. An unshared agent intentionally appears not found.
A provider/sender key mismatch requires investigating the identity change,
not disabling signature checks; re-pair only after verifying the other person.

Configuration and grants live in `connections.yaml` beside `config.yaml`.
The provider private key is in `<state>/federation/provider.key` (0600), and
invitations, sender key pins and the durable outbox live in SQLite. Preserve
these when moving an installation. Connections expose public fingerprints,
never provider private keys, invite secrets or participant API keys.

## Security model and API

All federation traffic is signed with the pinned provider's Ed25519 key;
each remote sender's key is pinned on first sight under that provider. The
recipient checks domain, signatures, sharing, grants, payload size and rate
limits before accepting mail. Tailscale membership alone is not authentication.
A paired peer is trusted to assert its own participants; it cannot claim local
identities or another provider. Nothing received is interpreted as daemon
instructions. Treat external message content with the same judgment as any
other message from another person or agent.

The trusted API exposes `GET /api/federation`, `GET|POST /api/connections`,
`POST /api/connections/invite`, `PATCH|DELETE /api/connections/{provider}`,
`GET /api/grants`, `POST /api/grants/decide` and
`GET /api/remote-contacts?local=<name>`. Accept takes `{code}` in the body;
decide takes `{peer, remote, local, allow}`. Browser access through the remote
listener still requires normal device authentication. Invite creation is a
POST with `Cache-Control: no-store`; invite secrets are never included in
list responses, events or request logs.
