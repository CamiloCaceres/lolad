# Agents v3 implementation

The Agents roster now organizes agents by team and places agents needing attention first within each team. Search matches name, role, model and team; the attention filter includes failed, crashed, waiting and unknown states as well as pending configuration restarts. Filters remain active across SSE refreshes and lifecycle actions. No matches is distinct from first-run onboarding.

Each row exposes state/current work, last signal, model/harness, uptime, crash restarts in 24 hours, unread agent mail and the next cron trigger. Cron preview uses the existing cron dependency and the daemon's local time zone. The Service contract does not expose the last mail poll, so mail and webhook triggers show their event-driven nature without an invented next-run time.

Actions explain consequences: Restart resumes the conversation; Wake starts a fresh conversation and on_wake while retaining brain/ and memory/; Stop interrupts current work and explicitly mentions whether new mail may wake the agent. Start/retry remains visible for stopped/failed/crashed agents. Forms work without htmx and preserve the roster filter on redirect. Menus support native keyboard disclosure, Escape and outside-click dismissal; background status requests pause while a menu is being read.

Agent detail puts the current situation and vitals first, preserves pending-restart actions and the existing read-only terminal attach, and combines hook/lifecycle events, triggers and incoming/outgoing mail into a descending timeline. Corresponding mail events are deduplicated. Configuration exposes safe address-only email information, skills mode, harness/model and directory links to the relevant settings tabs. Section links make configuration reachable on phones below a long timeline. Runtime environment values, extra arguments and on_wake prompt contents are not rendered in the summary.

## Verification

- `go build ./...` passed.
- `go vet ./...` passed.
- `go test ./internal/web/... -timeout 30s` passed.
- `go vet ./internal/web/...` passed.
- New tests cover roster search/attention/empty results, filtered controls, resume/fresh semantics, wake-on-mail consequences, timeline entries/deduplication, next cron preview and unknown health state.
- Existing websocket tests preserve watch/control behavior and ignore browser input in read-only mode.
- Browser checked 1440×1000 and 390×844, light and dark; final screenshots in `internal/web/testdata/screenshots/v3-agents-*` and `v3-agent-*`.
- Browser start from a filtered roster retained the selected agent and updated its state; Escape closed the action menu; no horizontal overflow on checked phone views.
- Scoped axe checks found zero violations; terminal glyph and select-background contrast checks were incomplete.
- Impeccable detector ran in degraded regex mode with zero reported findings; computed styles were not certified by that detector.
- Independent finish review: **SHIP** at Agents list/detail scope after correcting phone detail menu anchoring. Reviewer verified the one material correction resolved in light and dark captures.

## Integration

All reads and mutations continue through `core.Service`; no new backend methods, dependencies, deployment steps or configuration changes. Shared shell files and other workers' screen code were not modified. The existing fake already provides realistic health states, pending restart and scheduled triggers; it required no edits for this task.

Repository-wide `go test ./...` was started and passed the packages reported through internal/store, but remains running in unrelated supervisor tests at handoff (go process PID 31071). This is not a repository-wide test pass; the coordinator should complete or investigate that check before deployment. The separate complete web package test/vet runs passed, including fake and webdev packages.
