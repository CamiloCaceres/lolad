# Federation wiring proof — 2026-10-26

Two real throwaway daemon processes ran on this Mac, using separate private
config/state directories under `/tmp/lolad-v4-wiring-*`. Daemon A used tenant
`milo`, local listener 4196, remote listener 4296, federation URL
`http://127.0.0.1:4296`, and tmux socket `lolad-v4-wiring-a`. B used tenant
`bob`, ports 4197/4297 and socket `lolad-v4-wiring-b`.

A had only a temporary Ultron registry entry with `autostart: false` and
`wake_on_message: false`. B had no agents. No harness was launched. The real
lolad, its agents, configuration/state directories, PM2 and Tailscale Serve
were never used or changed.

The transcript below uses A/B to mean the same built binary with that
instance's `LOLAD_CONFIG`, `LOLAD_STATE`, and `LOLAD_URL` environment. Invites
were captured in memory and passed to B on stdin; no code or private key was
printed, written to this report, or passed in argv.

```text
A serve
B serve
PASS A and B /healthz

A connect invite
[decode only the code from the JSON response, privately in memory]
B connect accept < [private stdin stream]
PASS paired through remote listeners, without device authentication

A connect share bob.lolad.local ultron
A org check milo bob@bob.lolad.local
A approvals
PASS outbound policy preview permits the human but creates no grants
A org check milo ultron@milo.lolad.local
PASS local full-address policy remains allowed
PATCH A /api/connections/bob.lolad.local {"user_reachable":true}
PATCH B /api/connections/milo.lolad.local {"user_reachable":true}
PASS explicit user reachability on both connections

B send ultron@milo.lolad.local "Mailbox proof" --subject Review
PASS same message ID appears on A with status=held
PASS B sender copy delivery=held
A approvals
PASS pending grant for bob@bob.lolad.local → ultron
A org check bob@bob.lolad.local ultron
PASS sender preview denied while pending
A approve bob.lolad.local bob@bob.lolad.local ultron
A org check bob@bob.lolad.local ultron
PASS sender preview allowed after approval
PASS A recipient copy becomes unread
PASS B sender copy becomes delivered through a signed receipt

B send milo@milo.lolad.local "User thread proof" --subject "User review"
PASS A holds this separate user↔user first message
A approve bob.lolad.local bob@bob.lolad.local milo
PASS B sender copy becomes delivered
A send bob@bob.lolad.local Reviewed --subject "Re: User review" --in-reply-to [user-message-id]
PASS reply arrives unread on B
PASS matching thread_id and in_reply_to on both daemons

[terminate B cleanly and wait for its process to exit]
A send bob@bob.lolad.local "Offline proof" --subject "Queued while offline"
PASS A delivery=queued
[allow one failed delivery attempt]
B serve
PASS B health, using the same config/state as before
PASS queued message eventually delivery=delivered on A
PASS B receives exactly the sender's message ID

A msg --as ultron read [original-agent-message-id]
PASS output contains "External — from bob@bob.lolad.local via Bob's lolad"

[terminate both daemons and wait for process exit]
tmux -L lolad-v4-wiring-a kill-server
tmux -L lolad-v4-wiring-b kill-server
[remove both complete temporary trees, including keys, mail and config]
PASS cleanup complete
```

The reply test uses a user↔user conversation because AMP preserves participant
identity: Alice cannot impersonate Ultron by replying as the user to a message
addressed to Ultron. The agent mailbox path and user reply path were both
exercised independently.

Validation used constrained Go parallelism (`GOMAXPROCS=2`, `-p 1`) because
this shared host was near its process limit. `go build ./...` and
`go vet ./...` passed, as did the complete app, API, CLI, registry and msgcli
package suites. The delivery notice tests, including the new external marker,
passed. The existing `deliver.TestRealDelivery` could not complete: its private
tmux panes could not fork (`resource temporarily unavailable` / `Device not
configured`) or stayed empty, while a read-only process inventory showed more
than 1,400 tmux processes on the host. This environmental failure was escalated
to the coordinator; none of those other processes were terminated by this worker.
