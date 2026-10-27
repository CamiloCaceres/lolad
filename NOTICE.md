# Credits and third-party notices

lolad is MIT licensed (see [LICENSE](LICENSE)). It exists because of the work
below. If you use lolad, please also star and credit these projects.

## Projects lolad builds on

### lolabot — the agent framework

[23blocks-OS/lolabot](https://github.com/23blocks-OS/lolabot) · MIT ·
Copyright (c) 2026 23blocks Inc.

Every lolad agent is a lolabot instance: its `brain/`, `memory/`, skills,
tools and `lolabot.yaml` come from lolabot. `lolad new` runs lolabot's
unchanged `setup.sh`, and `lolad adopt` takes over an existing instance.
`internal/agentcfg/assets/email.sh` and `email-aim-secret.sh` are adapted from
lolabot's `tools/email.sh` (the second is the stock version lolad recognises
and upgrades). lolabot's MIT license, included as
`internal/agentcfg/assets/LICENSE.lolabot`, applies to those files.

### AI Maestro — the pioneer

[23blocks-OS/ai-maestro](https://github.com/23blocks-OS/ai-maestro) · MIT ·
Copyright (c) 2025 Juan Peláez / 23blocks

AI Maestro proved that a fleet of long-running terminal agents can work as a
team, and lolad is a small sibling built after it. lolad's hardest rules come
from AI Maestro's experience: only type into an idle, prompt-ready pane; read
the pane back to confirm a notice landed; deliver mail at Claude's Stop hook;
never claim a delivery you cannot prove. AI Maestro's AMP provider was the
reference implementation lolad studied (no code was copied). If you want the
much broader platform (memory, code graph, task boards, multi-machine mesh,
gateways, many agent programs), use AI Maestro.

### Agent Messaging Protocol (AMP)

[agentmessaging/protocol](https://github.com/agentmessaging/protocol) ·
Apache-2.0 · [agentmessaging.org](https://agentmessaging.org)

lolad is an AMP provider: addresses, Ed25519-signed envelopes, the `/v1` API
and provider-to-provider federation follow the AMP specification
(v0.1.2-draft). Implementation choices and deviations are lolad's own; see
[SPEC.md](SPEC.md).

### Also

- [Claude Code](https://claude.com/claude-code) and Codewhale, the two
  harnesses lolad runs.
- [tmux](https://github.com/tmux/tmux) and, on Windows,
  [psmux](https://github.com/psmux/psmux).

## Bundled third-party code

| Path | Project | License |
|---|---|---|
| `internal/web/static/vendor/htmx.min.js` | [htmx](https://htmx.org) 2.0.11 | 0BSD, `LICENSE.htmx` |
| `internal/web/static/vendor/xterm.js`, `xterm.css`, `addon-fit.js` | [xterm.js](https://github.com/xtermjs/xterm.js) 6.0.0 / addon-fit 0.11.0 | MIT, `LICENSE.xterm` |
| `internal/auth/qr/` | [skip2/go-qrcode](https://github.com/skip2/go-qrcode) | MIT, `internal/auth/qr/LICENSE` |
| `clients/android/` | lolad's Android client, adapted from a GPL-3.0 client | **GPL-3.0**, `clients/android/LICENSE`, `clients/android/THIRD-PARTY.md` |

Go module dependencies are listed in `go.mod` and keep their own licenses.
The Android client is a separate program under GPL-3.0; the rest of the
repository is MIT.
