# Dashboard redesign: "lolad Night"

The dashboard is being redesigned for people who don't write code. The
target screens are a Claude Design canvas (27 boards), which is the source of
truth for layout and copy. This file tracks how the boards map onto this
package and what is done.

## Decisions

- **Keep every route and form.** Only labels, markup and styles change, so
  the API, the CLI and the phone clients keep working. New pages are added
  only where a board has no route today (`/settings`).
- **Dark only for now.** The boards only define a dark theme. A light theme
  is backlog.
- **Assistants are orbs; people are rings.** The `avatar` partial draws an
  orb for agents and a plain ring with an initial for people (the operator).
  Orb colour comes from a hash of the name over six identity hues, unless the
  agent has its own colour.
- **Plain words.** Agent → assistant, Inbox → Home, Agents → Assistants,
  Org → Team, Connections → Sharing, Traffic → Activity (under Settings).
  States: idle → Ready, busy → Working, waiting → Needs you,
  stopped → Asleep, starting → Waking up, crashed → Restarting,
  failed → Can't start, unknown → Not sure.
- **Periwinkle is your turn** (v2 canvas). `--you` is #7AA2FF, shared with
  the primary action. Amber (`--warn`) is only for real warnings: unsaved
  edits, holding an assistant's keyboard, Activity's warning line. Failures
  use rose.
- **Type.** Sora (display, 200–300) and Manrope (UI), served from
  `static/vendor/fonts`. Mono only behind Advanced.
- **Nav.** Home, Assistants, Team, Sharing, Settings. Email, Devices and
  Activity move under Settings.

## Board → screen

| Board | Route | Templates | Status |
|---|---|---|---|
| Design language | all | `app.css` tokens, `icons.html` avatar/state, nav in `layout.html` | done |
| Home, Home-Empty | `/` | `pages/inbox.html`, `partials/inbox.html` | done |
| Thread | `/inbox/{peer}` | `partials/inbox.html` | done |
| Assistants | `/agents` | `pages/agents.html`, `partials/agents.html` | done |
| Assistant, Assistant-Fix | `/agents/{name}` | `pages/agent.html`, `partials/agents.html` | done (fix panel shows when failed) |
| New-Assistant | `/new` | `pages/new.html`, `partials/new.html`, `new.js` | done (job presets, live orb) |
| Set-Basics … Set-Memory | `/agents/{name}/settings/{tab}` | `pages/settings.html`, `partials/settings.html` | done (tabs renamed; same forms) |
| Team | `/org` | `pages/org.html`, `partials/org.html` | done (rules and defaults under Advanced) |
| Sharing | `/connections` | `pages/connections.html` | done (identity and hub under Advanced) |
| Your phone | `/devices` | `pages/devices.html` | done |
| Activity | `/traffic` | `pages/traffic.html`, `partials/traffic.html` | done |
| App settings | `/settings` (new) | `appsettings.go`, `pages/appsettings.html` | done (only real settings) |
| Edge states | `/login`, 404 | `pages/login.html`, `pages/notfound.html` | done ("lolad isn't running" lives in the desktop app's waiting page) |
| First run (7 boards) | `/welcome` | `welcome*` | v2 done: Hello, Claude, Name, Job, Shape, Waking, Ready on setup's unchanged steps (see WELCOME.md) |

## Backlog

- **Orb motion.** The orbs are CSS-only and their motion is subtle. Match
  the movement of the Lottie orb used in the taxflow app (silk bands that
  sweep and fold, colour that pools and drifts) without copying its files.
  Options: an SVG with SMIL or CSS-animated paths, a small canvas/WebGL
  shader, or our own Lottie file.
- Light theme.
- Notifications on first-run Ready (lolad-desktop).
- Activity's "Group by conversation" and per-message plain-word reasons
  ("Different team, so it has to go through Lola").
- Assistant colour picker, on Ob-Shape and in settings (needs a colour field in `lolabot.yaml`).
- Logo: `static/logo.svg` and `desktop/assets/icons/gen.go` share one drawing.
