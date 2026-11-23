# Welcome (first-run onboarding)

`/welcome` takes a new user from zero to a first agent replying, in a browser
or in the desktop app (docs/desktop.md section 7). It is a standalone page
(its own layout, no navigation) in the dashboard's normal look.

## Behaviour

* `/` redirects to `/welcome` only while onboarding is not finished, nobody has
  left it ("Set up later") and there are **no agents**. Anyone with agents is
  never redirected. Without `Options.Setup` nothing changes.
* Screens follow the Ob-* boards (redesign v2), on top of setup's unchanged
  steps:
  - **Hello** (setup's login step): "Let's wake up your first assistant." Get
    started saves `ServiceDone`; starting at login is offered on Ready.
  - **Claude** (harnesses): Ready / Not signed in / Not installed; Install and
    Sign in open the embedded terminal; Check again; Continue needs Claude
    Ready and also saves the default assistants folder (setup's folder step),
    so the folder has no screen of its own unless saving it fails.
  - **Name**: the first name, saved to `org.yaml` through `SaveOrg`.
  - **Job** and **Shape** (setup's agent step): pick a first job (six
    presets in `welcomeJobs`, or your own words), then a name with ideas,
    "Asks you first (in Lolad)" or "Works on its own", and Advanced (brain
    and folder). "Asks you first" keeps PermBypass and adds an instruction to
    the mission and on-wake text: check in through ask_user before anything
    new, costly or outward-facing. The orb's colour follows the name live
    (same hash as the server); there is no stored colour yet.
  - **Waking**: a checklist polled from `/welcome/waking` until the assistant
    is up (or 60 s), then **Ready**: "Atlas is awake.", the assistant's real
    first message once it arrives (never a made-up greeting), the job's
    suggestion chips and a composer that write to it, "Keep Atlas running"
    (InstallService) and "Connect your phone".
  - Done (no assistant made) keeps the notification opt-in.
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
`static/welcome.js` (notification button, Shape's live name and orb), `welcome_test.go`. Shared
files touched: `web.go` (the `Setup` option and `welcomeRoutes`), `inbox.go`
(the redirect), `static/term.js` (`data-term-path`, `data-term-control`,
`data-autostart`). The logic and API are in `internal/setup`; see
docs/setup-api.md. `go run ./internal/web/cmd/webdev -empty` serves the flow
against a canned `setup.Demo`.

Screenshots: `testdata/screenshots/welcome-*`.
