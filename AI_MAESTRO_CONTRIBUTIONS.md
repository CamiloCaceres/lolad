# Contributions to AI Maestro

This file plans what lolad gives back to
[AI Maestro](https://github.com/23blocks-OS/ai-maestro), and in what order.
lolad learned its delivery rules from AI Maestro. This is the return trip:
pieces lolad has run in production that AI Maestro's own backlog asks for.

Written 2026-11-20 against AI Maestro `main` at 0.106.242 and its `backlog/`.
Re-check every item against their current code before starting it. They
ship almost daily, and an item listed here may already be done.

## Ground rules

1. **Runtime only.** Contributions cover the runtime: tmux and psmux, delivery
   and wake-ups. Product UI is out of scope. Do not propose screens, layouts or
   UX changes upstream.
2. **No federation between people.** Their F043 (invite codes, held senders)
   came from lolad, but it is not something we propose. If they build it, we
   answer questions. We do not send code.
3. **Fully tested and approved by the lolad owner before anything is sent.**
   Each PR is built and tested in a fork first. CI and the manual checks listed
   below must be green, and the owner must review and approve it. Only then does
   it go upstream.
4. **Issue first for anything big.** Their CONTRIBUTING.md asks for an issue
   before a major change. Small fixes (item 0) can go straight to a PR.
5. **Speak their language.** Reference their backlog ids (F027, F041, F045 and
   so on). Follow their style: short sentences, plain words, "what we verified"
   kept apart from "what we believe". Credit lolad as the source the way
   their backlog already does.
6. **Port, don't paste.** AI Maestro is TypeScript/Node, and lolad is Go. Port
   the behaviour and the tests, not the code.

## Order

| # | Contribution | Their item | Size | Why this position | Status |
|---|---|---|---|---|---|
| 0 | `amp-send.sh` prints the refusal reason, not only the error code | F042 "Known limits" (upstream `agentmessaging/claude-plugin`) | XS, one line | They already named the fix. It is a no-risk first PR that opens the channel. | Not started |
| 1 | tmux self-check and conformance probe | F045 | S–M | It is the probe that F041 needs, and it is useful on Linux and macOS too. It touches no behaviour, so it carries no risk. | Not started |
| 2 | Native Windows with psmux, tmux layer | F041 (part 1) | M | Item 1 tells us exactly what fails. We fix only that. | Not started |
| 3 | Native Windows: server and paths | F041 (part 2) | M–L | Covers `server.mjs`, path handling, `node-pty` and the `~/.aimaestro` layout on Windows. | Not started |
| 4 | Native Windows: plugin shell scripts | F041 (part 3) | L, needs their decision | The `.sh` AMP scripts assume a POSIX shell. They choose between Git Bash, a Node port or PowerShell. Issue first. | Not started |
| 5 | Wake a *stopped* agent when mail arrives | F073, F088 (the "stopped, on a host with AI Maestro" case) | M | Their sweeper (F027) wakes only live sessions. lolad's `wake_on_message` covers this case. Verify the gap first (see below). | Verify first |
| 6 | Wake-up gaps found while testing F027 | F027 phase 3 | ? | Only what item 5's investigation proves is missing. Nothing speculative. | Verify first |

Items 0–2 can go out while items 3–6 are still being worked on. Item 5 must not
start before its verification step is written up.

## 0 · Refusal reason in `amp-send.sh`

- **Where:** `agentmessaging/claude-plugin`, the send script's error branch.
- **What:** print `.message` (for example "denied: rule 3, payroll may only
  message its head") instead of only `.error` (`policy_denied`).
- **Test:** a denied send in a test harness shows the reason, and a successful
  send's output is unchanged.
- **Note:** their F042 write-up names this exact one-line change.

## 1 · tmux self-check (F045)

Port of `lolad doctor` and `internal/tmux/conformance_test.go`.

- Starts a private server (`-L aim-selfcheck-<rand>`) and runs every operation AI
  Maestro relies on against a tiny fake agent. It prints the failing operation by
  name and always kills its own server.
- Operations from their F041 table: `new-session -d -s -c`, `capture-pane -p -e`,
  `send-keys -l` and `-H`, `has-session`, `display-message -p`, `list-sessions -F`,
  `kill-session`, `rename-session -t`, `set-environment`, `set-option`,
  `load-buffer` and `paste-buffer`, `copy-mode`. Plus lolad's extra checks:
  Unicode and punctuation literals, Enter, Escape, BSpace, C-c, resize, a server
  that outlives its client, and a dead pane that keeps its status.
- **Safety, from both projects' history:** private `-L` only, never a bare
  `kill-server`, a canary before the suite, and a guard against re-entry. A lolad
  test once started about 1,800 tmux servers.
- **Deliverable:** a script, a vitest test, and a Settings → Doctor check if
  their Doctor has a slot for it (wiring only, no UI design).
- **Verified before sending:** passes on macOS tmux 3.5, Linux tmux 3.4 (CI) and
  Windows psmux 3.4.0, and fails readably on a deliberately broken binary.

## 2–4 · Native Windows with psmux (F041)

What lolad learned running psmux 3.4.0 in production (`docs/windows.md`):

- **Environment.** Pass the agent environment through a private file that a
  wrapper deletes before it starts the child, never through command arguments or
  `new-session -e`. This also keeps secrets out of process lists on every OS.
- **Exit codes.** `pane_dead_status` does not keep the exit code on psmux 3.4.
  The wrapper writes the child's real code to a private per-session `.exit` file.
  Report -1 (unknown) rather than inventing success.
- **Targets.** Window operations use `=session:0`, and pane operations use
  `=session:0.0`. psmux rejects a pane suffix on window commands.
- **A trailing `;` in literal text** is eaten by the command-list parser. Send
  it through hex (`send-keys -H`).
- **Panes die with their shell.** psmux kills leftover children when a pane's
  shell exits (`@kill-descendants`). Start the agent directly through
  `-- argv`, not from a shell that exits.
- **Console flicker.** Every helper process on Windows needs a hidden console
  window, or polling makes windows flash (lolad `2649a3f`).
- **No symlink privilege.** Creating an agent must not need symlinks on Windows
  (lolad `cac69c4`).
- **Terminal view.** There is no PTY attach on Windows. lolad shows ANSI pane
  snapshots about every 150 ms and forwards keys through psmux. A writable
  viewer resizes the window, and closing it restores the size.
- **Private directories** inherit the user's ACLs, so keep state inside the
  user's profile, outside shared folders.

Their F041 gap list to close in part 2: `rename-session -t`, `set-environment`
(5 call sites), `TMUX_TMPDIR` (use `-L` namespaces instead), a session started
with `bash <script>`, and `list-sessions -F` variables psmux does not document.

**Verified before sending:** the full item 1 probe green on Windows 11 with psmux
3.4.0. Create, wake, message and hibernate an agent on a clean Windows machine
with no WSL. Then close the launching PowerShell window and confirm the sessions
survive and the server re-adopts them.

## 5–6 · Wake-ups (only what is missing)

**What they have (from their backlog, not yet run by us):**
- F027 phase 1: a server-side inbox sweeper that wakes *live* sessions every
  60 s, with per-message backoff and caps.
- F027 phase 2: a doorbell call from the send script.
- F027 phase 3: still open.

**What we believe is missing:**
- Starting a *stopped or hibernated* agent when mail arrives (F073: "a new
  message or a board instruction does not [wake it]").
- Resuming the last conversation on such a wake, instead of starting fresh.

**Verification step (do this first and write it up here):**
1. Install AI Maestro on a test machine. Send mail to a live idle agent, a busy
   agent, a hibernated agent and an agent whose session crashed.
2. Record what wakes, how long it takes and whether anything is typed into a
   busy pane.
3. Compare with lolad's behaviour. lolad types a one-line notice only when the
   prompt is idle and confirmed by reading the pane back. It delivers at the
   Stop hook when the agent is busy. It starts a stopped agent with
   `wake_on_message` and its `on_wake` prompt, and it resumes the conversation
   after a crash but not after a planned rotation.

Then propose only the measured gap, as an issue against F073 first.

## Candidates, not planned

These are lolad features that AI Maestro's F044 checklist lists. They are not
part of this plan. Each needs the lolad owner's explicit go-ahead:

- Restart with backoff (5 s to 5 min), and a `failed` state after 6 crashes in 30 min.
- A `startup blocked: <first line>` report after 2 minutes without a ready prompt.
- Daily conversation rotation (a crash resumes instead).
- Agents isolated from the global `~/.claude/settings.json` (`--setting-sources project,local`).
- A "Restart to apply" marker for settings that need a restart.

## Out of scope

- Federation between people (F043).
- Any UI, dashboard or onboarding work.
- Messaging policy (F042). They already ported it.
- lolad's authenticated remote listener. It conflicts with their "the network is
  the trust boundary" rule.

## Per-PR checklist

- [ ] Re-read the matching backlog item and their latest code. Is it still open?
- [ ] Fork branch, tests written first, their test suite green.
- [ ] Manual checks listed for the item, with output saved to the PR description.
- [ ] Description says what was verified and what was not, separately.
- [ ] lolad owner reviewed and approved.
- [ ] Status column above updated.
