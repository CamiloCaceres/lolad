# Traffic audit

Traffic answers who messaged whom, about what, whether a recipient was notified,
and which sends policy blocked. It uses only `core.Service.Traffic`, `Events`,
`List`, and `Me`; opening an audit row never marks a message read.

The timeline interleaves accepted mail and `policy` events, newest first.
Participant, direction, and time filters are URL-backed and work without
JavaScript. Direction is relative to the selected participant, or Milo when
Everyone is selected. Denied and Queued tabs retain those filters. Queued means
AMP mail is unread and the recipient has not been notified; Notified means it
was surfaced to the recipient, and Read means the recipient fetched/opened it.
These are notification states, not SMTP delivery receipts.

By pair groups the displayed page by unordered participant pair and ranks by
volume, making bursts and acknowledgement loops visible. Expand a pair to read
its messages, or focus it to inspect all available exchanges between those two
participants. Pair volume is explicitly page-local. Older traffic preserves
all selected filters and Newest returns to the live end.

Rows disclose full message content, envelope metadata, notification attempts,
read time, signature status and context. Sender and recipient links open agent
pages when those participants are registered agents. Policy links jump to the
Org exception's one-based rule number, or to defaults. The recorded denial
reason remains historical evidence; Org shows the current configuration, so
rule order can differ after later edits. Denials contain no stored subject or
body, and the UI says so rather than inventing a message.

Mail scanning is bounded at 2,000 records per requested page window. Older
pages continue through the mail archive. `Events` offers no before cursor or
kind filter, so only denials in the latest 2,000 lifecycle events are available.
A visible audit-window notice appears whenever either bound is reached. Counts
refer to the scanned window after participant, direction and time filters;
Denied and Queued narrow the displayed entries. Each page shows at most 60.

Mail and lifecycle SSE events refresh the newest page. An expanded message or
pair pauses refresh so the operator can inspect it without losing their place.
Older pages stay still. Theme follows the shared dashboard tokens; phone rows
reflow into time/state, route, subject, and a two-line preview.

The fake adds 78 agent-to-agent messages, an acknowledgement burst, a queued
bookkeeper backlog, and eight policy denials without adding human decision mail.

## Validation

Verified 1440px desktop and 390px phone in light and dark, with final screenshots
under `testdata/screenshots/v3-traffic-*.png`. Browser checks confirmed native
Enter-key expansion, navigation to the actual Org rule anchor, pair expansion,
and no horizontal overflow. Axe reported zero violations (native select
contrast requires manual review); the Impeccable detector reported no findings
in its degraded regex-only mode.

`go build ./...`, `go vet ./...`, `go test ./internal/web/...`, and
`go test ./internal/web/... -race` passed during the run. Final Traffic/fake
focused tests and web vet passed after the outside-participant coverage was
added. A concurrent full `go test ./...` run failed in `TestDevDirReloads`
because another worker's live Settings template referenced `pathMid` before
that helper reached the compiled binary; the remaining packages passed.
No dependency, configuration, migration, or deployment change is required.
