# Welcome (first-run onboarding)

`/welcome` takes a new user from zero to a first agent replying, in a browser
or in the desktop app (docs/desktop.md section 7). It is a standalone page
(its own layout, no navigation) in the dashboard's normal look.

## Behaviour

* `/` redirects to `/welcome` only while onboarding is not finished, nobody has
  left it ("Set up later") and there are **no agents**. Anyone with agents is
  never redirected. Without `Options.Setup` nothing changes.
* Steps: 1 start at login, 2 agent tools (claude, codex, codewhale: Ready /
  Not signed in / Not installed, Install and Sign in open the embedded terminal,
  Re-scan, Continue needs one Ready), 3 agents folder (default
  `~/Lolad Agents`, live macOS privacy warning), 4 your name (saved to
  `org.yaml` through `SaveOrg`), 5 first agent (name, job, tool; defaults filled
  in, directory under the chosen folder), then done with notification opt-in.
* Progress is in `<state>/setup.json`; `/welcome` resumes at the first
  incomplete step. Finished steps can be revisited (`?step=`), later ones cannot.
* Every step is a plain form post that redirects, so it works without scripts;
  htmx only keeps the tool list live (polling `/welcome/harnesses` every 3 s
  while an installer or login runs) and checks the folder as you type.
* The terminal is the agent terminal panel (term.js) with `data-term-path` and
  `data-term-control="on"`, pointed at `/welcome/terminal/<session>`.

## Files

`welcome.go` (handlers), `templates/pages/welcome.html` (layout and steps),
`templates/partials/welcome.html` (live fragments), `static/pages/welcome.css`,
`static/welcome.js` (notification button only), `welcome_test.go`. Shared
files touched: `web.go` (the `Setup` option and `welcomeRoutes`), `inbox.go`
(the redirect), `static/term.js` (`data-term-path`, `data-term-control`,
`data-autostart`). The logic and API are in `internal/setup`; see
docs/setup-api.md. `go run ./internal/web/cmd/webdev -empty` serves the flow
against a canned `setup.Demo`.

Screenshots: `testdata/screenshots/welcome-*`.
