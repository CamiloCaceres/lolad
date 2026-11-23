# Inbox and shell, v3

Inbox is a messaging workspace for the operator. Home presents unanswered agent
questions first, then unread conversations, then read conversations by recency.
Questions are requests (see requests.go and SPEC "Requests"). Each is an
open card in the conversation where it was asked (Agent-UI and Thread-Asks
boards): the choices, fields or steps are right there, nothing hides behind
an "Answer" toggle. A choice card selects, then **Send answer** commits; the
last row is always "Something else…", which opens a box for the user's own
words. Skip is a quiet button ("carry on without me"). Each card sends its one
answer at once (an answers mail with one entry), then folds into a one-line
receipt ("You chose: …", "Skipped", "OpenAI key: Saved"); the question stays
one click away inside it, and the answers mail itself isn't shown again.
An option may carry one line of consequence after " — " ("Book the 9:40 —
€212 · lands 12:10"): the label is bold, the rest small, and the whole string
is still the value sent back. The waiting bar above the composer names the
assistant and the question, and shows only while that card is out of view;
"Jump to it" scrolls to it and focuses the first choice. Home shows each
assistant's oldest request as a card: choices answer in one tap there,
anything longer opens the conversation. Chatting doesn't answer a request.
Typed answers, the picked choice, chosen files and an open "I couldn't do it"
survive live refreshes (app.js); key fields are never kept. Periwinkle means
only "waiting on you". A post without send=1 still stages an answer for
the older send-together flow (POST /inbox/{peer}/requests/send).

Agents lay messages out with blocks (SPEC "User tools and blocks";
partials/blocks.html): commands and snippets with Copy, values that copy one
click at a time, a link button showing its host, and a checklist. A task card
leads with its done button (the first option); "I couldn't do it" (the last
option) swaps in a "What went wrong?" box and a drop zone for a screenshot or
log, sent with the answer. File and logs fields answer "send me a screenshot"
and "paste the error". Say is a bubble. Update is one quiet line, with a
spinner while it is the newest message and the assistant is working; an
update followed directly by another from the same assistant is hidden, so a
run of progress shows only its latest line. Deliver is a card with a teal
"Finished" tag, its title, its files and a Copy the text button.

Home has one reading column on desktop and phone. A conversation rail appears
only inside a thread on desktop. Phone threads replace home, with a visible back
button, agent state, settings and agent detail links. The composer remains at the
bottom of the viewport while messages scroll; safe area insets are respected.
Desktop keyboards use Enter to send and Shift+Enter for a newline. Touch devices
retain multiline input and an explicit send button. Drafts survive navigation.

Messages render with the existing escaped Markdown renderer: headings, lists,
links, tables and fenced code. Deliverables expose their original Markdown to a
Copy the text button. If Clipboard API access is unavailable (including plain
HTTP remote access), a readonly selected text field provides manual copying.
Progress updates use quiet, unboxed text.

The shell preserves ordinary additive nav links for Email and devices. On phones
navigation occupies a sticky top bar with horizontally scrolling destinations,
leaving space for more links without shrinking tap targets. In a thread the shell
recedes so the composer and conversation have the full viewport. The live status
remains visible on phone home. Icon-only desktop navigation has accessible names.

Creating an agent may redirect to /inbox/<slug>?created=1, or ?adopted=1 when
adopting. These show a dismissible welcome with Email settings and Org links.
No new Service method, dependency, daemon configuration or migration is required.

## Files

The composer attaches files by drag and drop onto the conversation (a dashed
drop overlay, announced through a polite live region), the paperclip's
multi-file picker (phones included) or pasting images, which are named
`pasted-image-<yyyy-mm-dd-hhmmss>.png`. Each file uploads at once, one request per
file and three at a time, to `POST /inbox/{peer}/attachments` (multipart,
streamed to `UploadAttachment`, capped by `MaxBytesReader` just above the 500 MB
provider limit, read deadline extended to 30 minutes). Chips show progress,
errors and a remove button; Send waits for uploads and for failed files to be
removed. The form posts the IDs as `attachment` fields (at most 10, checked on
both sides). Files without text get a derived body ("Sent 2 files: …") that the
thread hides. Conversations with peers on another lolad disable attaching and
say why.

Messages show clean PNG/JPEG/GIF/WebP files as fixed-height thumbnails that
open full size, and everything else as a download row with icon, type and size.
`suspicious` files carry a visible warning and download behind a confirm.
`GET /inbox/{peer}/attachments/{id}` serves a file only when a message in that
conversation carries it (or it is the user's unsent upload from there), always
with `nosniff` and `CSP: sandbox`; only `?inline=1` on a clean raster image is
served inline with its stored type, everything else as an
`application/octet-stream` attachment with an RFC 5987 `filename*`. Range works
because the reader is seekable. `attachments_test.go` covers this.

## Verification

`inbox_v3_test.go` covers home priority, live refresh wiring, conversation rail,
read-to-recent transition, Markdown escaping, deliverable controls, one-tap
answers and creation/adoption welcome banners. Browser checks use only webdev on
127.0.0.1:4194 against the in-memory fake, at 1440 and 390 pixels in both themes.
Screenshots live under testdata/screenshots/v3-inbox-*.png. Browser checks verified
Enter submission, Shift+Enter newline, back navigation and a 390px thread with no
horizontal overflow. Automated axe inspection found no violations on the final
phone thread; some offscreen contrast checks were inconclusive. Full web package tests,
repository-wide build and repository-wide vet passed after concurrent owners
finished their related changes.

Impeccable review used the task's pinned calm, muted Operate direction. Initial
critique: duplicate home columns, hidden phone overview/answer buttons, weak
hierarchy among questions, ordinary mail and deliverables. The redesign replaces
the duplicate home structure and promotes questions consistently across devices.
Mechanical detector ran once in degraded regex mode because its optional parser
modules were unavailable; it reported no findings. Visual review was performed
in this worker context; no independent finish reviewer was dispatched.

Final integration note: the latest full web test run passed. The final duplicate
`go test ./...` attempt was stopped while waiting alongside the other workers'
repository-wide jobs; all packages that had reported results were passing. The
coordinator should run the full suite once after the shared checkout settles.
The worker's webdev server on 4194 was stopped after screenshot capture.
