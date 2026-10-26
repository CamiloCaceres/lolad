# Agent settings workspace

Settings keeps one shared horizontal section switcher, including Email. Model
& runtime separates model and permissions from wake and continuity controls.
Changes remain saved until a restart applies them; the shared pending banner
provides Restart to apply without starting a fresh conversation. Skills separates
installed copies from the user's searchable library and explains inheritance
before an agent's mode is changed. Secrets remain write-only password fields,
with autocomplete disabled and no values returned after either success or error.

Files is a workspace rather than a recursive path dump. The web presentation
orders instructions, lolabot.yaml and harness settings, brain, memory, and agent
skills first, regardless of the backend grouping. Each group collapses. Remaining
files form native expandable folder trees. Generated plugin snapshots, caches,
state, backups, node_modules and .git are hidden until Show everything is checked.
This changes only presentation: core.Service still controls every file access.

Find a file searches names and paths, case-insensitively. Submit with Enter or
Find; search results expand their ancestor folders. Press `/` outside a text
field to focus search (including from the phone editor). Filenames remain whole,
long parent paths shorten in the middle, hover exposes the entire path, and the
editor header wraps the selected full path. On a phone, selecting a file opens
the editor; its back button returns to the list. The editor uses the available
viewport height, with Markdown edit/preview and Cmd/Ctrl+S retained.

Leaving an edited file, including submitting a search or toggling Show everything,
asks before discarding unsaved text. Concurrent disk edits retain the draft and
offer Reload from disk or an explicitly confirmed Overwrite with mine. Neither
choice silently overwrites another version. The Iris fake includes generated
plugin noise and nested priority files for repeatable visual checks.

## Verification (v3 redesign)

- `go build ./...` and `go vet ./...` passed.
- `go test ./internal/web/...` and `go vet ./internal/web/...` passed,
  including priority ordering, hidden noise, case-insensitive search, expanded
  ancestors, empty results, full paths, write-only secrets, saves, conflicts,
  Markdown sanitization, skills changes and restart application.
- Browser review at 1440×1000 and 390×844, light and dark; 21 screenshots in
  `testdata/screenshots/v3-settings-*.png`. `/` from the phone editor focuses
  search, searching generated files expands their tree, and one-click restart
  clears pending state. The scoped Files editor WCAG A/AA axe check reported
  zero violations.
- The Impeccable detector reported no findings in its degraded regex mode;
  parser dependencies were unavailable, so browser validation was used too.
- The repository-wide `go test ./...` was interrupted after more than six
  minutes without a final result during concurrent worker builds. The
  coordinator should run the complete suite once the shared checkout settles.
