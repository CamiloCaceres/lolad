# Org screen

Org is organized around the operator's decisions: structure first, message access,
exceptions, then defaults. The chart spans the page and nests departments under
their parent team; at phone widths the same reporting lines become a vertical tree.
Create a team, pick its head and tap member checkboxes. Selecting an agent already
in another team moves that agent; a head is never also saved as a member.

Message access begins with one sender and a readable answer for each recipient.
The expandable comparison matrix shows all senders and recipients, with a reason
in every actionable cell. Both views open the same connection editor, bringing it
into view on a phone. There is no separate message-testing workflow. Actual
verdicts always come from core.Service.Check; the original policy explanation is
available under Policy detail.

Exceptions use named agents, You, Everyone, or named team subtrees in selects.
New UI exceptions are inserted first, so a specific override takes effect even
when an earlier broad rule denied the connection. Existing API form clients that
omit priority=first keep append behavior. Editing preserves the rule's position;
up/down changes its priority. Both ways uses the policy's Both flag.

Only I may message an agent adds two leading rules: allow user to the agent, then
deny everyone to that agent. This intentionally overrides prior incoming allows,
preserves outgoing access, and never overrides the system sender's unconditional
access. Both rules remain visible and individually editable in Exceptions.

Defaults describe user_to_agents, agents_to_user, vertical and independents in
sentences. Independent access applies when either endpoint is independent;
human defaults apply first. Direct reporting access is only head/member or heads
of immediate parent/child teams, not arbitrary ancestors. Open-team access still
applies when vertical is disabled. All saves go through core.Service.SaveOrg,
which validates and persists org.yaml; the web package has no policy store.

Traffic links to #org-rule-N, where N is the one-based rule order, or
#org-defaults. These targets are visibly highlighted. Rule numbers track current
order, as in the policy explanations.

The existing fake company supplies ten agents, a chief of staff, two departments,
an office member, independent agents and three ordered rules. It already exercises
three reporting levels and cross-team exceptions; no replacement fixture or new
Service method is necessary. Screenshot fixtures are v3-org-*.png.

Validation: Org round trips, sender selection, create-team disclosure, editing,
precedence, both-way exceptions and protected recipients have HTTP tests. The new
precedence/protection tests also evaluate the resulting organization with the real
internal/policy implementation rather than relying only on the fake evaluator.

## Verification record (v3)

- `go build ./...` and `go vet ./...` passed.
- `go vet ./internal/web/...` and the complete `go test ./internal/web/...`
  passed after the concurrent settings edits settled.
- The broader `go test ./...` run failed in `internal/deliver`
  (`TestRealDelivery/retry`: staged instead of confirmed) and in a live settings
  template reload (`pathMid` was added while an older test binary was running).
  Neither failure was changed by the Org worker.
- Browser verification used the isolated fake server on 127.0.0.1:4197,
  1440 × 1100 and 390 × 844, with light and dark preferences. The Iris/Ultron
  both-way override saved and appeared first; create-team opened correctly;
  phone document width stayed at 390 and the editor cleared sticky navigation.
- Impeccable's mechanical detector returned no findings in degraded regex mode;
  optional HTML parser modules were unavailable, so contrast and layout were
  checked in the browser. No dependencies were installed.
