# Connections and external mail

Federation extends the existing operator console. The visual system stays neutral;
color identifies work waiting on the operator or a delivery failure. Connections
uses an identity block, a two-column pairing area (stacked on phones), then a
list of connected lolads. Sharing stays in a disclosure so a large fleet does
not bury connection health or the next connection.

## Controls

- **Create invitation** renders the code in that POST response only, with an
  explicit expiry and a copy button. No GET stores or retrieves it. The entire
  page disables htmx history snapshots; all responses remain `no-store`.
- **Accept an invite** is a password input with autocomplete disabled. The code
  is posted in the body and never echoed, including when the service's error
  includes its input. The dashboard renders a generic recovery message instead.
- **Agents they can reach** replaces the complete share list. Checked agents
  permit either anyone there (each new sender requires approval) or a nonempty
  set of pre-approved remote contacts. An empty specific selection is rejected,
  never widened to everyone. Unchecking every agent sends a non-nil empty list.
- **People there can message me** controls the local human's reachability.
- **Remove connection** opens an inline confirmation, then requires an explicit
  second submit. Pause and resume preserve existing shares.

Inbox approvals show the remote sender, destination, first subject, and held
count. Allow and Block forward the exact peer/address/local tuple. The remote
conversation picker and thread headers identify the other lolad. Every outbound
remote message shows its delivery state; failures also show the reason. Delivered
receipts use the existing quiet message-receipt treatment.

The agent page lists shared connections, pre-approved addresses, and allowed
senders. Org places external boxes beside the chart on large screens and below
it on phones, with labelled edges to the shared agents and a jump link. Traffic
shows the external provider, delivery status/reason, and a connection filter
that applies to both mail and policy records.

## Live updates

The stream classifies `federation` separately. app.js publishes
`lolad:federation` and refreshes mail after an approval change. Connections,
Inbox approvals, the agent sharing section, and external Org boxes refresh
through htmx. Delivery changes arrive as `mail`; existing mail refreshes update
receipts and Traffic. Reconnecting resynchronizes both classes.

## Verification

Run `go build ./internal/web/...`, `go vet ./internal/web/...`, and
`go test ./internal/web/...`. Federation handler tests exercise secret handling,
mutation arguments, share validation, remote recipients, and filtered audit
pages. Fake service tests cover isolated connection copies and approval/pause
transitions. The fake includes Jugui/Nova, a pending request with two held
messages, an allowed sender, mixed remote delivery states, and paused Studio.

Browser review uses the fake webdev server on 127.0.0.1:4198, never the real
service. Final screenshots are `testdata/screenshots/v4-*.png`: 1440px desktop
and 390px phone in light and dark for Connections, Inbox, the remote thread,
the agent page, Traffic, and Org. The invite screenshot uses only the explicit
nonfunctional demo code. Static Impeccable detection found no patterns in the
new template/CSS but ran in regex-only mode because parser modules are absent;
it does not constitute a computed-contrast audit.

Interactive browser checks also verified that saving Ultron's specific Nova
selection survives a new GET, the invitation copy button reports Copied, an
approval triggers `lolad:federation` and removes its card without navigation,
and sending remote mail triggers `lolad:mail-visible` and adds the queued
message to the open thread. Expanded sharing editors were checked and captured
in both sizes/themes. The fake package additionally passes `go test -race`.
An independent Impeccable finish review was requested from the coordinator for
integration; no named finish-reviewer agent is registered in this worker.
