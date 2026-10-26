---
name: lolad Android
description: A calm native Material 3 surface for talking to personal agents.
colors:
  ink: "#24272D"
  paper: "#FAFBFD"
  backdrop: "#E9EDF3"
  muted: "#525D6C"
  outline: "#7F8D9F"
  ink-dark: "#E5E9F0"
  paper-dark: "#242A32"
  backdrop-dark: "#171C23"
  muted-dark: "#B6C0CE"
  outline-dark: "#8995A5"
typography:
  headline:
    fontFamily: "sans"
    fontSize: "24sp"
    fontWeight: 700
  title:
    fontFamily: "sans"
    fontSize: "20sp"
    fontWeight: 700
  body:
    fontFamily: "sans"
    fontSize: "16sp"
  label:
    fontFamily: "sans"
    fontSize: "14sp"
  metadata:
    fontFamily: "sans"
    fontSize: "12sp"
  terminal:
    fontFamily: "monospace"
    fontSize: "13sp"
rounded:
  content: "16dp"
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

Inherit maestro-lite’s confirmed native Material 3 world: charcoal actions, paper reading surfaces, a cool gray backdrop and restrained hierarchy. Native Android conventions govern controls, keyboard handling, navigation and dialogs. Hue stays muted; status always includes readable text.

**Key Characteristics:**

- Conversation content leads; terminal output is a separate diagnostic view.
- Light and dark palettes use corresponding Material color roles.
- Selectable reading text, scalable sp typography and 48dp minimum actions.

## Colors

### Primary

Charcoal ink and its pale dark-mode counterpart carry primary actions, reversing their tonal relationship in dark mode.

### Neutral

Cool paper and pearl-gray backdrop separate reading content from the surrounding pane; the corresponding night surfaces preserve that relationship. The theme maps these colors to `colorPrimary`, `colorOnPrimary`, `colorSurface`, `colorOnSurface`, `colorOnSurfaceVariant`, `colorOutline` and window background. Secondary metadata uses muted text. Outgoing mail reverses the reading surface; incoming content stays on paper.

**The Theme Roles Rule.** Custom content resolves its colors from the active native theme rather than choosing a separate light or dark palette.

## Typography

Native Roboto (`sans`) carries the reading hierarchy; there is no separate display family. Headline is for screen identity, title for agent names and deliverables, body for Markdown and field values, label for updates and controls, metadata for timestamps and field labels. Terminal output alone uses monospace. System font scale applies throughout; reading text adds 3dp line spacing. Sizes are listed in the frontmatter; headline and title use bold, while native controls retain Material typography.

**The Reading Type Rule.** Use native scalable text for conversation content and reserve monospace for terminal output.

## Layout

One phone pane at a time. Root gutters use page spacing plus status/navigation bar, cutout and IME insets. The top bar holds context, Back and Disconnect. Secondary navigation and recovery actions wrap into fully visible rows with compact spacing; they do not require horizontal scrolling. ActionRows measures each native control within the available width and lays out in the current left-to-right or right-to-left direction. Inbox and Agents support pull to refresh. Conversation content scrolls independently above the fixed composer. Agent details scroll as one column, including lifecycle actions. Inbox places open questions before recency and exposes unread counts in text. Tablet adaptation remains unverified.

**The Visible Actions Rule.** Action groups wrap at the available width so navigation and recovery controls remain discoverable without a horizontal gesture.

## Elevation & Depth

Content relies on tonal separation. Custom reading containers have no shadows or borders. Material buttons, dialogs, outlined connection fields and snackbars retain native focus, ripple and elevation behavior. Polling updates do not animate the entire screen.

## Shapes

Reading surfaces and the composer use the content radius. Primary controls use Material 3’s rounded button shape. Connection fields retain outlined Material construction and persistent labels. The launcher mark is an original geometric vector.

## Components

### Buttons and action groups

Filled native Material buttons carry primary actions. Quiet actions use an outlined-style MaterialButton with transparent background, no stroke and theme ink text. Both retain native feedback and a 48dp minimum height. Reusable ActionRows groups wrap buttons with 8dp gaps between controls and rows; control widths are measured against the available width. Native disabled treatment communicates unavailable sends and decisions.

### Navigation

Inbox/Agents uses a wrapping native text-action group and an explicit selected label. Conversation, detail and snapshot navigation use the same action group; lifecycle actions wrap within the detail column. Opening a conversation or agent records navigation history. System Back dismisses an open keyboard first; history survives theme recreation. Agent details, Messages and Terminal snapshot remain explicit destinations.

### Conversations

Incoming Markdown has a muted byline outside its paper surface; user replies use reversed colors and a small leading inset. Updates are quiet full-length rows. Deliverables retain their complete Markdown body and an emphasized subject. Open questions show “Waiting on you”, a filled first option and quiet remaining options; answered questions show the chosen decision from authoritative mail.

The multiline composer remains outside the scrolling thread, is bounded to five visible lines and has a labeled Send button. Empty drafts and in-flight sends disable Send. Failure preserves the draft; the client never automatically replays a send.

### Reading containers and fields

List containers use page padding and reading spacing between rows. Message containers use page horizontal padding and reading vertical padding. Both use the content radius and paper surface; outgoing message containers reverse ink and paper. The composer uses the same radius and padding, a 52dp minimum height and an 8dp gap before Send. Pairing fields use native outlined TextInputLayout construction, body text, persistent labels and a 56dp minimum field height. Focus, selection and errors remain native Material behavior rather than custom effects.

### Agents and connection

List rows group identity, question/unread counts, runtime, state and relative activity. Details shows selectable values with deliberately separated labels. Lifecycle actions require a native confirmation dialog. Terminal snapshots remain selectable, read-only and monospace; their output can scroll horizontally to preserve terminal lines. This is separate from the wrapping action groups.

Pairing uses outlined server/code fields, a primary Pair device action, camera QR scanning and a password token fallback. Network failures give Tailscale guidance; revoked devices return to pairing with an explicit explanation.

## Do's and Don'ts

### Do:

- **Do** inherit theme color roles for custom content and retain native control behavior.
- **Do** keep updates quiet and questions actionable, with status readable without color.
- **Do** preserve scroll and drafts during live refreshes.
- **Do** wrap native action groups to the available width, including lifecycle and recovery controls.

### Don't:

- **Don't** turn a terminal snapshot into an interactive terminal or a substitute for the conversation.
- **Don't** introduce production data into demonstration screenshots.

Source: confirmed maestro-lite inheritance; this client’s `app/src/main/res/values/styles.xml`, `values-night/styles.xml`, `MainActivity.java` and `ActionRows.java`. Native Material defaults are inherited, not replaced by invented token values. This record describes source behavior; tablet layout remains unverified.
