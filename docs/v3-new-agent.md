# New agent surface

Mode: Operate. Inherit lolad's neutral control-room palette, system typography,
state vocabulary and light/dark tokens. This redesign changes the task structure
within the shared visual system.

## Critique and direction

The previous screen asked for implementation details before purpose: identity,
slug, role and harness came before the optional mission. Its primary action sat
below several full-width fieldsets. Adoption exposed only a path box and offered
no discovery evidence. Setup consequences, active progress, skill isolation and
partial-failure recovery were absent.

The replacement prioritizes name and job, visible runtime defaults, and one
advanced disclosure. A review rail previews the destination and first message;
on phones it follows the form as a final review. The files disclosure describes
the exact setup contract without making infrastructure jargon the introduction.
Purpose feeds both first mail and the default fresh-conversation instructions.
Adoption puts actionable discovered directories first and folds registered
agents away. Both forms use native controls, inline errors, retained values and
an indeterminate request state. Arrival opens a conversation directly; email
and team placement remain optional next steps.

## Evidence

Tests cover discovery boundaries/metadata, malformed-YAML diagnostic privacy,
first-launch skills persistence, stopped creations with a queued mission,
required purpose, known slug/path collisions, partial failure, echoed adoption
settings, and success conversation destinations. Browser evidence covers 1440px
and 390px in light/dark, discovery selection, local previews and validation,
keyboard-operable forms and an accessibility scan.

## Final verification

- `go build ./...` and `go vet ./...` passed.
- `GOMAXPROCS=2 go test -p 2 ./... -timeout 90s` passed across the repository.
  An earlier full run encountered concurrent remote/traffic edits; the final
  complete run was green.
- Browser: 1440 × 1000 and 390 × 844, both light and dark, with final screenshots
  under `internal/web/testdata/screenshots/v3-new-*.png`.
- Discovery selection filled the actual path/model and updated the review.
  Typing an existing name opened the slug disclosure and blocked submission.
- A real htmx `beforeSend` observation confirmed the submit button was disabled
  and the indeterminate progress block was visible during the request. Aborting
  the request produced the explicit uncertain-completion recovery message.
- Successful fake creation opened `/inbox/maple?created=1` with the purpose mail,
  email/team links and ready conversation composer.
- Axe found zero violations on both forms. Its native-select contrast checks
  required manual review; computed dark-theme text/background contrast was
  15.59:1, and the light theme was visually checked. The Impeccable detector
  returned no findings using its degraded regex fallback (parser modules absent).
- No new dependencies, migrations, secrets, production daemon calls, or deployment
  config changes. Fake webdev remains available on `127.0.0.1:4198` for review.
