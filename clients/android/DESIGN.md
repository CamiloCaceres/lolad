---
name: lolad Android
description: A calm native Material 3 surface for talking to personal agents.
colors:
  ink: "#1F2124 / #E7EBF1"
  secondary: "#454A52 / #B4BECC"
  muted: "#5B6472 / #97A4B4"
  paper: "#FAFBFD / #1D2229"
  raised: "#FFFFFF / #262E38"
  backdrop: "#E9EDF3 / #14181E"
  quiet: "#E2E7EE / #2A313A"
  outline: "#737E8C / #7E8B9B"
  hairline: "#000000 14% / #FFFFFF 14%"
  action: "#1F2124 / #E7EBF1"
  action-text: "#FAFBFD / #1D2229"
  mine: "#1F2124 / #2C3642"
  on-mine: "#FAFBFD / #E7EBF1"
  card-on-mine: "#33383F / #3A4656"
  awake: "#336B45 / #86B797"
  attention: "#95500E / #D79A5B"
  error: "#9E3B2F / #E39A8B"
  terminal: "#F3F5F8 / #12171D"
  terminal-ink: "#2A2F36 / #D6DEE8"
typography:
  organization:
    fontFamily: "sans"
    fontSize: "27sp"
    fontWeight: 500
  title:
    fontFamily: "sans"
    fontSize: "23sp"
    fontWeight: 500
  headline:
    fontFamily: "sans"
    fontSize: "21sp"
    fontWeight: 600
  body:
    fontFamily: "sans"
    fontSize: "16sp"
    fontWeight: 400
  action:
    fontFamily: "sans"
    fontSize: "14sp"
    fontWeight: 600
  label:
    fontFamily: "sans"
    fontSize: "13sp"
    fontWeight: 500
  metadata:
    fontFamily: "sans"
    fontSize: "12sp"
    fontWeight: 400
  terminal:
    fontFamily: "monospace"
    fontSize: "13sp"
    fontWeight: 400
rounded:
  content: "24dp"
  row: "15dp"
  button: "14dp"
  field: "12dp"
  terminal: "16dp"
  segment: "10dp"
spacing:
  tight: "4dp"
  compact: "8dp"
  reading: "12dp"
  page: "16dp"
  group: "20dp"
---

# Design System: lolad Android

## Overview

**Creative North Star: "Pearl-gray Operate"**

lolad inherits the confirmed maestro-lite world: pearl-gray paper, charcoal ink, cool
backdrop, hairline separation and monochrome primary actions. It is the same language as
the macOS client, translated to native Material 3 rather than copied pixel for pixel.
Hue is reserved for status words, questions and connection state. The workspace is for
reading what agents sent and acting on it, not for watching a terminal; the only terminal
surface is a read-only snapshot.

The macOS client floats translucent glass panes. Android deliberately does **not**: blur
and live backdrop filters cost frames on mid-range phones and fight the platform's own
elevation model. The same visual intent is carried by opaque tonal surfaces, a hairline
outline and the pearl backdrop separating content from chrome. Nothing here is pure white
or pure black in either mode.

**Key Characteristics:**

- Paper reading surfaces on a pearl-gray backdrop; hairline lines instead of glass rims.
- Charcoal actions in light mode; paper-white actions in dark mode.
- Conversation content leads. Updates are quiet. Questions are explicit.
- Words, not just colour, carry state ("Waiting on you", "Connected · live updates").
- Lists and threads are virtualized; a thousand-message thread draws only what is visible.
- Theme is a reader preference: Follow system, Light or Dark, chosen in-app and kept in
  ordinary `SharedPreferences` (never next to the credential). The choice is applied by
  wrapping the activity base context in the chosen `uiMode`, so every resource, dialog and
  system bar follows it; `recreate()` restores screen, draft, focus, scroll and in-flight
  uploads.

## Colors

**The Muted Status Rule.** Navigation, selection and primary actions are monochrome.
Saturation appears only in small status marks and the "Waiting on you" treatment, always
beside a text label.

`ink` is the primary label colour and the light-mode action fill; `action-text` reverses
against it, so dark mode swaps the pair (paper-white fill, near-black label). `secondary`
is the secondary label colour and `muted` carries timestamps and hints. `paper` is the
opaque reading surface — message bodies and list cards. `raised` is the lighter elevated
surface for upload chips, fields and Material dialogs, which matters most at night where
paper sits only 1.3:1 above the backdrop. `backdrop` is the window behind everything.
`quiet` is the pale container for quiet buttons, the segmented control, attachment cards
and the selected-row wash. `hairline` is a 0.75dp separator at 14% black (14% white at
night). `mine`/`on-mine` are the outgoing bubble pair — charcoal in light, a calm lighter
slate in dark rather than a glaring white bubble — and `card-on-mine` is the attachment
card inside it. `awake` marks running work; `attention` marks questions, paused
connections, recoverable errors and the suspicious-file badge; `error` marks destructive
and revoked states. `terminal`/`terminal-ink` are the read-only snapshot's own pair, a
little deeper than paper in both modes.

These map to Material 3 roles in `values/styles.xml` and `values-night/styles.xml`:
`colorPrimary`/`colorOnPrimary` (action), `colorOnSurface` (ink), `colorSurface` (paper),
`colorOnSurfaceVariant` (secondary), `colorOutline`/`colorOutlineVariant` (outline and
hairline), `colorSecondaryContainer`/`colorOnSecondaryContainer` (quiet), `colorTertiary`
(awake), `colorError`, plus the five `colorSurfaceContainer*` roles pinned to paper so no
Material default introduces a second neutral. State hues are read from
`values/colors.xml` and `values-night/colors.xml`, so custom content never carries a
hand-written light or dark palette.

## Typography

Native Roboto (`sans`) carries the reading hierarchy; there is no separate display family.
`organization` is the "lolad" wordmark, `title` the agent/conversation name, `headline` a
deliverable subject and screen heading, `body` Markdown, field values and composer text,
`action` a primary button, `label` status and control text, `metadata` timestamps, counts
and bylines. The monospaced system font is used only for terminal output and the pairing
code. Sizes are listed in the frontmatter and scale with the system font setting; reading
text adds 3dp line spacing (1dp for labels, 2dp for terminal output).

Markdown is parsed by Markwon 4.6.2 (core, tables, strikethrough) into a `Spanned`.
Rendering happens once per message id on a background executor and the result is cached
in an LRU keyed by message id; binding only ever reads the cache. A cold miss shows plain
text for a frame and then rebinds, so a burst of new mail never blocks scrolling. Raw HTML
is never interpreted.

**The Reading Type Rule.** Conversation bodies are selectable native text at a comfortable
reading size; terminal output is monospaced and selectable but never editable.

## Layout

One phone pane at a time. Root gutters use page spacing plus status/navigation bar, cutout
and IME insets. The top bar holds context, Back and Disconnect. Secondary navigation and
recovery actions wrap into fully visible rows with compact spacing; they do not require
horizontal scrolling. ActionRows measures each native control within the available width
and lays out in the current left-to-right or right-to-left direction.

Inbox, Agents and the conversation thread are `RecyclerView`s with stable ids and a
`ListAdapter`/`DiffUtil` diff, so updates rebind changed rows only. Inbox and Agents
support pull to refresh. Conversation content scrolls independently above the fixed
composer and keeps its position while live updates arrive; a new message only auto-scrolls
when the reader was already at the bottom. Agent details scroll as one column, including
lifecycle actions. Inbox places open questions before recency and exposes unread counts in
text. Tablet adaptation remains unverified.

On launch the last-known inbox, agents and open-question counts are read from a small
local cache (no token, URL or pairing code) and drawn first, then refreshed; the banner
says "Saved state · refreshing…" until the network answers.

**The Visible Actions Rule.** Action groups wrap at the available width so navigation and
recovery controls remain discoverable without a horizontal gesture.

## Elevation & Depth

Depth is tonal, not material. Paper sits on backdrop, hairline lines separate rows and
sections, quiet containers are a flat wash and `raised` lifts chips, fields and dialogs one
step (white on pearl, a lighter slate at night). Reading containers carry a hairline rather
than a shadow; none of them blurs. Native dialogs, snackbars, text fields, ripples and focus
rings keep Material behaviour. Live updates do not animate the whole screen, and list item animations
are disabled so a diff never plays a cascade.

## Shapes

Reading surfaces and the composer use the content radius (24dp). List cards use the row
radius (15dp), primary and quiet buttons 14dp, connection fields 12dp, terminal snapshots
16dp and the segmented Inbox/Agents control 10dp — the macOS pane/card/button/field ratios
mapped to phone dimensions. Connection fields retain outlined Material construction and
persistent labels. The launcher mark is an original geometric vector.

## Components

### Buttons and action groups

Filled native Material buttons carry primary actions: charcoal fill with paper text in
light mode, reversed at night. Quiet actions use an outlined-style MaterialButton with the
quiet wash, no stroke and ink text; text actions are transparent with secondary text. All
retain native feedback and a 48dp minimum height. Reusable ActionRows groups wrap buttons
with 8dp gaps between controls and rows; control widths are measured against the available
width. Native disabled treatment communicates unavailable sends and decisions.

### Navigation

Inbox/Agents uses a segmented native control inside a quiet track with an explicit selected
label and state description. Conversation, detail and snapshot navigation use the same
action group; lifecycle actions wrap within the detail column. Opening a conversation or
agent records navigation history. System Back dismisses an open keyboard first; history
survives theme recreation. Agent details, Messages and Terminal snapshot remain explicit
destinations.

### Conversations

Incoming Markdown has a muted byline outside its paper surface; user replies use reversed
colors and a small leading inset. Updates are quiet full-length rows. Deliverables retain
their complete Markdown body and an emphasized subject. Open questions show "Waiting on
you", a filled first option and quiet remaining options; answered questions show the chosen
decision from authoritative mail.

The multiline composer remains outside the scrolling thread, is bounded to five visible
lines, and pairs an Attach icon with a labeled Send button. The upload tray above it is a
single horizontally scrolling row, so ten files never push the thread off screen. Empty
drafts with no finished upload, or an upload still in flight, disable Send. Failure
preserves the draft and the chips; the client never automatically replays a send.

### Reading containers and fields

List cards use page padding, reading spacing between rows, the row radius and the paper
surface. Message containers use the content radius and reading vertical padding; outgoing
message containers reverse ink and paper. The composer uses the field radius and padding, a
52dp minimum height and an 8dp gap before Send. Pairing fields use native outlined
TextInputLayout construction, body text, persistent labels and a 56dp minimum field height.
Focus, selection and errors remain native Material behaviour rather than custom effects.

### Agents and connection

List rows group identity, question/unread counts, runtime, state and relative activity.
Details shows selectable values with deliberately separated labels. Lifecycle actions
require a native confirmation dialog. Terminal snapshots remain selectable, read-only and
monospace; their output can scroll horizontally to preserve terminal lines. This is
separate from the wrapping action groups.

Pairing uses outlined server/code fields, a primary Pair device action, camera QR scanning
and a password token fallback. Network failures give Tailscale guidance; revoked devices
return to pairing with an explicit explanation.

### Attachments

Files are cards, not links. An image that the server marks clean is drawn from
`GET /api/mail/attachments/{id}?peer=…&inline=1` with bearer auth, downsampled off the main
thread into a 148dp thumbnail inside the bubble, memory- and disk-cached by attachment id,
and faded in over 120ms (no fade when animations are off). Any other file — PDFs, text,
SVG, or an image the server flags `suspicious` — is a type badge, a middle-truncated name,
the size, and an explicit "Suspicious · download only" pill where applicable. Tapping an
image opens a full-screen pinch/double-tap preview; every other tap streams the file to a
location the reader chooses with the system document picker, showing determinate progress
and offering Cancel. Nothing downloaded is ever opened, parsed as HTML or executed.

Upload chips sit above the composer in one horizontally scrolling row: a downsampled
thumbnail or type badge, the name, the size, a determinate progress bar, a remove control
and Retry after a failure. Uploads start one request per file the moment it is picked and
stream from the content URI in 64 KB buffers, so memory never grows with file size. Up to
ten files per message; the eleventh is refused in a one-line note instead of a silent
no-op. Send is enabled by text **or** at least one finished upload and disabled while an
upload is in flight. With no text the subject is derived from the file names
("Quarterly report.pdf", or "3 files"). Removing a chip before sending is local only.

Fast paths that avoid the file picker: **paste** turns clipboard images into
`Pasted image yyyy-MM-dd HH.mm.ss.png` and copied files into attachments, while plain text
keeps native paste behaviour and wins when the clipboard carries both; **drag and drop**
onto the composer or the thread shows a quiet drop-target wash with an awake-hairline
while hovering and uploads on release. Remote peers (`name@other.lolad`) cannot receive
files: Attach is hidden, drops and file pastes answer with one sentence, and the server's
403/404/413/422 statuses become short human messages on the chip.

An outgoing message with files is echoed into the thread immediately, with its chips drawn
from the local URI, and replaced by the authoritative mail row when the send returns.

## Motion and feedback

Ripples are native and present on every row, card and attachment chip, not only on
buttons. Controls keep at least 48dp of touch target at any font scale. Sends and decisions
give one short confirm haptic. The only app-drawn animation is the 120ms thumbnail fade;
it and all list item animators disappear when the system removes animations. The thread
stays pinned only when the reader was already at the bottom, and rotation restores the
first visible message and its offset. The composer sits above the keyboard, keeps its
height across IME changes and does not jump when a refresh lands.

## Do's and Don'ts

### Do:

- **Do** resolve custom content from Material theme roles and the `values-night` state hues.
- **Do** keep updates quiet and questions actionable, with status readable without colour.
- **Do** preserve scroll position, selections and drafts during live refreshes.
- **Do** virtualize any list that can grow, and parse Markdown off the main thread.
- **Do** wrap native action groups to the available width, including lifecycle and recovery
  controls.

### Don't:

- **Don't** add blur, glass or live backdrop filters; tonal surfaces carry the depth.
- **Don't** hold a file's bytes in memory for upload, thumbnail or download; stream it.
- **Don't** open, execute or render a downloaded file, and never inline a suspicious image.
- **Don't** turn a terminal snapshot into an interactive terminal or a substitute for the
  conversation.
- **Don't** introduce production data into demonstration screenshots.

Source: confirmed maestro-lite inheritance, aligned with `clients/apple/DESIGN.md`;
implemented in `app/src/main/res/values/{,night/}styles.xml`,
`values/{,night/}colors.xml`, `Ui.java`, `MainActivity.java`, `ActionRows.java`,
`ThreadAdapter.java`, `ConversationAdapter.java`, `AgentAdapter.java`,
`MarkdownCache.java`, `Attachment.java`, `Attachments.java`, `AttachmentViews.java`,
`Uploads.java`, `ImageCache.java`, `ImagePreview.java` and `ThemeMode.java`. Native Material defaults are inherited, not replaced by invented
token values. This record describes source behaviour; tablet layout remains unverified.
