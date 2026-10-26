---
name: lolad Agents
description: Calm operating roster and situation-first agent detail
colors:
  bg: "oklch(1 0 0)"
  bg-2: "oklch(0.977 0 0)"
  bg-3: "oklch(0.953 0 0)"
  bg-4: "oklch(0.925 0 0)"
  line: "oklch(0.912 0 0)"
  line-2: "oklch(0.85 0 0)"
  ink: "oklch(0.21 0 0)"
  ink-2: "oklch(0.37 0 0)"
  muted: "oklch(0.49 0 0)"
  faint: "oklch(0.7 0 0)"
  btn-primary: "oklch(0.24 0 0)"
  btn-primary-ink: "oklch(0.99 0 0)"
  focus: "oklch(0.56 0.13 250)"
  you: "oklch(0.81 0.135 80)"
  you-ink: "oklch(0.3 0.06 70)"
  you-text: "oklch(0.5 0.11 70)"
  you-wash: "oklch(0.97 0.03 85)"
  you-line: "oklch(0.85 0.09 82)"
  st-idle: "oklch(0.66 0.1 155)"
  st-busy: "oklch(0.6 0.11 245)"
  st-starting: "oklch(0.72 0.07 230)"
  st-crashed: "oklch(0.66 0.14 52)"
  st-failed: "oklch(0.57 0.17 27)"
  st-stopped: "oklch(0.76 0 0)"
  st-unknown: "oklch(0.76 0 0)"
  st-crashed-text: "oklch(0.52 0.13 50)"
  st-failed-text: "oklch(0.5 0.16 27)"
  st-crashed-wash: "oklch(0.975 0.02 55)"
  st-failed-wash: "oklch(0.97 0.02 27)"
  term-bg: "oklch(0.19 0 0)"
  term-ink: "oklch(0.88 0 0)"
typography:
  title:
    fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
    fontSize: "1.375rem"
    fontWeight: 620
    letterSpacing: "-0.02em"
  section:
    fontSize: "1.0625rem"
    fontWeight: 620
  body:
    fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  row:
    fontSize: "0.8125rem"
  label:
    fontSize: "0.75rem"
  mono:
    fontFamily: 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace'
rounded:
  r-sm: "6px"
  r: "8px"
  r-lg: "12px"
spacing:
  s1: "4px"
  s2: "8px"
  s3: "12px"
  s4: "16px"
  s5: "24px"
  s6: "32px"
  s7: "48px"
components:
  button:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.ink}"
    rounded: "{rounded.r}"
    padding: "0 12px"
  button-primary:
    backgroundColor: "{colors.btn-primary}"
    textColor: "{colors.btn-primary-ink}"
    rounded: "{rounded.r}"
    padding: "0 12px"
  search:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.ink}"
    rounded: "{rounded.r}"
    padding: "7px 10px"
    height: "40px"
  action-disclosure:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.ink}"
    rounded: "{rounded.r}"
    padding: "6px"
    width: "min(310px, 82vw)"
---

# Design System: lolad Agents

## Overview

**Creative North Star: “A well-run control room.”**

This scoped record describes the built `/agents` roster and `/agents/{name}` detail. It inherits the dashboard's calm, exact, quiet personality from `../../PRODUCT.md`. One operator checks a small staff at a desk and on a phone; current work and recovery consequences must be legible immediately. Mail remains the way to talk to an agent. The terminal is a debugging disclosure.

Source of truth: `../../static/app.css`, `../../static/pages/agents.css`, `../../static/pages/agent.css`, `../../templates/pages/{agents,agent}.html`, `../../templates/partials/agents.html`, and the existing `core.Service` contract. This file records screen-specific choices and inherited primitives; it does not replace a global design system. No new service API or secret-reading surface is implied.

**Key Characteristics:**

- Neutral surfaces and compact typography with air between operating rows.
- Team boundaries and attention-first ordering within each team.
- Situation and vitals before history and configuration.
- Native controls, explicit action consequences, and light/dark preference support.

## Colors

The palette is achromatic in its surfaces; subdued hue communicates state or operator attention. Frontmatter keys retain the corresponding CSS custom-property names without `--` and record light-mode values. The actual CSS variables remain normative at runtime.

### Primary

The primary action is neutral: `--btn-primary` with `--btn-primary-ink`. “Open conversation” receives this emphasis on detail. Focus blue is an accessibility affordance, not a branding fill.

### Neutral

`--bg` is the page and control surface; `--bg-2` is the shell, terminal summary, and roster hover; `--bg-3` is control hover/open disclosure feedback; `--bg-4` is pressed/active emphasis. `--line` separates rows and sections; `--line-2` outlines controls. `--ink`, `--ink-2`, `--muted`, and `--faint` form the text hierarchy.

### State and attention

Use `--st-idle`, `--st-busy`, `--st-starting`, `--st-crashed`, `--st-failed`, `--st-stopped`, and `--st-unknown` for the named states. `--st-waiting` aliases `--you`; the alias remains in CSS rather than becoming a new independent color primitive. Honey `--you-text` calls out pending restart information. `--st-failed-text` marks crashed/error activity titles. Always pair state hue with words or a glyph.

Dark mode is selected by `prefers-color-scheme: dark`; the same names change values:

| CSS token | Dark value |
| --- | --- |
| `--bg` | `oklch(0.185 0 0)` |
| `--bg-2` | `oklch(0.162 0 0)` |
| `--bg-3` | `oklch(0.235 0 0)` |
| `--bg-4` | `oklch(0.27 0 0)` |
| `--line` | `oklch(0.275 0 0)` |
| `--line-2` | `oklch(0.36 0 0)` |
| `--ink` | `oklch(0.94 0 0)` |
| `--ink-2` | `oklch(0.82 0 0)` |
| `--muted` | `oklch(0.68 0 0)` |
| `--faint` | `oklch(0.48 0 0)` |
| `--btn-primary` | `oklch(0.92 0 0)` |
| `--btn-primary-ink` | `oklch(0.18 0 0)` |
| `--focus` | `oklch(0.7 0.12 250)` |
| `--you` | `oklch(0.8 0.13 80)` |
| `--you-ink` | `oklch(0.25 0.05 70)` |
| `--you-text` | `oklch(0.84 0.12 82)` |
| `--you-wash` | `oklch(0.255 0.035 80)` |
| `--you-line` | `oklch(0.45 0.08 80)` |
| `--st-idle` | `oklch(0.72 0.1 155)` |
| `--st-busy` | `oklch(0.7 0.1 245)` |
| `--st-starting` | `oklch(0.76 0.06 230)` |
| `--st-crashed` | `oklch(0.72 0.13 55)` |
| `--st-failed` | `oklch(0.66 0.16 27)` |
| `--st-stopped`, `--st-unknown` | `oklch(0.55 0 0)` |
| `--st-crashed-text` | `oklch(0.8 0.12 60)` |
| `--st-failed-text` | `oklch(0.76 0.13 27)` |
| `--st-crashed-wash` | `oklch(0.235 0.03 55)` |
| `--st-failed-wash` | `oklch(0.23 0.03 27)` |
| `--term-bg` | `oklch(0.14 0 0)` |

`--term-ink` retains its light value in dark mode.

## Typography

Use `--font` for UI and prose; reserve `--mono` for addresses, paths, identifiers, and terminal text. Body inherits `400 0.875rem/1.5`. Shared font features are `"cv11", "ss01"`.

- `--t-xl: 1.375rem` (22px at a 16px root): page and agent title, weight 620, tracking `-0.02em`; agent title line height is 1.2.
- `--t-lg: 1.0625rem` (17px): detail section headings, weight 620.
- `--t-md: 0.875rem` (14px): current situation, vitals, team headings, search/select, standard controls.
- `--t-sm: 0.8125rem` (13px): row work, event title/body, section links, configuration values, small controls.
- `--t-xs: 0.75rem` (12px): field labels, state timing, runtime metadata, event time, vital labels.

Names and activity titles use weight 600. Current work wraps and is clamped to two lines on the desktop roster, three on phones. Timeline body line height is 1.55. Times and vital numbers use tabular numerals. There is no display-font or marketing type role.

## Layout

Inherited `.page` has a 1240px maximum width and desktop padding `24px 32px 48px`. Final shell navigation width is 196px, reduced to 64px from 761–1180px; page padding becomes `24px 24px 48px` at 1180px. At 760px and below, page padding is `16px 16px 32px` and the final v3 shell uses sticky top navigation with a horizontally scrollable destination row. Earlier bottom-navigation rules are superseded.

### Operating roster

Header, search/filter, textual fleet summary, then team sections. Search has a 460px maximum width and matches name, display name, role, model, or team without case sensitivity. “Needs attention” is a filter, not a second view. Fleet totals describe all agents; team row counts describe matches. Team groups sort by name; within each group attention cases precede other agents, then slug sorts. Unknown signal and pending restart count as attention alongside problem/waiting states. Without organization teams the heading is “All agents”; unaffiliated agents otherwise appear as “Independent”.

Desktop rows use five columns: `minmax(145px,.9fr) minmax(220px,1.65fr) minmax(145px,.95fr) minmax(128px,.8fr) auto`, 16px gaps, and `18px 4px` padding. Read identity/role, state/current work/last signal, model/harness/uptime/crash restarts, unread/next trigger, then actions. Team sections start 28px apart. The summary is compact text with a divider, never a band of metric cards.

At 701–1250px, use `150px minmax(200px,1fr) 150px`; actions sit in column three on row one, runtime and next trigger move into columns two and three on row two.

At 700px and below, use `minmax(0,1fr) auto`: identity and actions first, full-width current work second, runtime and right-aligned next/unread third. Hide avatars, the separate Message button, and aggregate state counts. Message remains in Actions as “Open conversation”. Search spans the full width, with Show and Filter below. Actions retain 40px minimum touch height. Team spacing becomes 26px.

### Agent detail

Breadcrumb, identity/actions, full-width situation with divider, then vitals: last signal, uptime, crash restarts/24h, unread agent mail, next scheduled trigger. The header uses 20px gaps and `24px 0` margin; situation uses 18px vertical padding. Vitals wrap with `16px 36px` gaps and 20px top margin.

Section jump links lead to Recent activity, Configuration, and Live terminal with 20px target scroll margin. A closed terminal disclosure precedes the content grid. Desktop activity/configuration grid is `minmax(0,1.45fr) minmax(290px,1fr)` with a 28px gap. Activity and configuration are flat sections. Configuration facts form one column beside the timeline.

At 1050px and below, configuration follows activity; its separator and 24px top padding establish the transition. Facts become two columns. At 700px and below, the header becomes one column, actions align left with 40px minimum button height, current state and explanation stack, vitals use two columns with 18px gaps, facts use one column, and grid gap becomes 26px. Activity time/content tracks change from `78px minmax(0,1fr)` with 16px gap to `65px minmax(0,1fr)` with 12px gap. The inherited header already stacks below 760px.

Reference viewport targets are desktop 1440px and phone 390px in both color schemes. These are review targets; they do not create additional CSS breakpoints or imply a browser validation report.

## Elevation & Depth

Rows and detail sections are flat, separated by 1px `--line` strokes and whitespace. Detail `.panel` explicitly removes background, border, radius, padding, and shadow. The action disclosure uses a `--line-2` border and z-index 30 without a shadow. Do not add floating cards to this surface.

Inherited `--shadow` is `0 1px 2px oklch(0 0 0 / 0.06), 0 8px 24px oklch(0 0 0 / 0.08)` in light mode and `0 1px 2px oklch(0 0 0 / 0.4), 0 12px 32px oklch(0 0 0 / 0.5)` in dark mode; it belongs to transient shared UI such as toasts rather than the roster or detail sections. Keyboard focus uses a 2px `--focus` outline offset 2px. Fields instead use the focus border and a 3px ring mixed at 22% opacity.

## Shapes

Use inherited `--r-sm: 6px` for action options/focus, `--r: 8px` for controls/disclosure, and `--r-lg: 12px` for the terminal container. Roster rows and detail sections remain square and open. The small inherited `head`/trigger tag is 20px high, has a 5px radius, neutral background, and 1px `--line` border. Rounded shapes do not imply a card around each agent.

## Components

### Search and attention filter

Labeled native search/select fields are 40px minimum height. Search updates the fleet fragment after a 250ms changed-input delay; select updates on change. The Filter submit button and GET form remain available. Empty results explain how to clear the query/filter. A fleet with no agents uses the shared first-run content.

### Operating row and current situation

Every row pairs a named state with a readable explanation and last signal. Crashed agents expose automatic retry timing/error; stopped agents explain wake-on-mail behavior; unknown signal is explicit rather than represented as healthy. Pending restart links to detail and carries the reason when available. Unread count is mail addressed to the agent, not the user's inbox count. Next trigger previews the earliest valid cron occurrence; mail/webhook triggers are described without inventing a next polling time. No scheduled trigger is also explicit.

Detail raises this same situation above vitals. Configuration drift uses the existing pending-restart treatment and applies through a resume restart.

### Buttons and native action disclosure

Shared buttons are neutral, 34px minimum height with `0 12px` padding, 8px radius, 1px `--line-2` border, weight 550. Small buttons use 30px minimum height, `0 10px`, and 13px type. Hover uses `--bg-3`; pressed uses `--bg-4`; disabled uses opacity .45. Detail promotes Open conversation with neutral primary colors. Preserve visible keyboard focus.

Actions uses native `details`/`summary`, with a right-aligned menu 6px below the trigger, `min(310px,82vw)` width, 6px padding, and 8px radius. On detail at 700px and below, the menu anchors to the left (`left:0; right:auto`) to stay inside the phone viewport; roster menus retain right anchoring. Options use `11px 10px` padding, 6px radius, readable labels, and separate consequence copy. Open summary uses `--bg-3`; disabled menu options use .5 opacity. Keep the semantic distinctions:

- **Start / Retry now:** resume the last conversation and clear crash backoff; Retry now names crashed recovery.
- **Restart · resume:** restart the process, continue the conversation, apply pending settings.
- **Interrupt turn:** send Escape while keeping the process running; enabled for busy/waiting only.
- **Stop agent:** end current work; disclose whether new mail can wake it again. Confirmation repeats that consequence.
- **Wake · fresh conversation:** run `on_wake` again while preserving `brain/` and `memory/`; confirmation explains that current work ends.
- **Open conversation / Change settings:** navigational escape routes retained in the menu.

### Unified activity

A single ordered stream interleaves mail and lifecycle/turn/trigger events by timestamp, newest first. Each entry has time, title, and detail; mail includes its direction/peer. Entries have 16px vertical padding and a bottom divider. Errors/crashes use state text color. Do not render the old independent mail list alongside this stream. “All mail traffic” links to the existing traffic view.

### Configuration summary

Expose address, directory with Browse files, harness/model, skills mode, email accounts, permissions, autostart, wake on mail, rotation, triggers, environment key names/file reference, and available process metadata. Change links go to existing settings destinations. Environment values stay hidden; never introduce a secret value into markup, event descriptions, screenshots, or documentation. Email loading failure and absent configuration have explicit text.

### Terminal disclosure

Native `details` starts closed, with a 52px minimum-height summary. It is read-only by default and offers an explicit Take control toggle with pressed state. Status is announced through `role="status"`. The terminal's dark surface uses `--term-bg`/`--term-ink` in both themes. Read-only height is automatic with a 120px minimum; control mode inherits the viewport-based terminal height. Control mode adds the existing honey mode chip and 2px inset ring. On phones, explanatory summary copy hides and connection status moves to its own line. Debugging never displaces conversation as the primary action.

### Live refresh and motion

Existing htmx fragments refresh from agent/mail events while including active fleet filters. Preserve native forms, labels, links, details controls, and focus visibility. Shared transitions use `--ease: cubic-bezier(0.22,1,0.36,1)` with 120ms control feedback and a 160ms terminal chevron rotation. Reduced-motion preference reduces animations/transitions to 1ms.

## Do's and Don'ts

### Do:

- **Do** lead with readable current work, state, timing, and the consequence of the next action.
- **Do** preserve team grouping and attention-first ordering within teams.
- **Do** check 1440px and 390px in light and dark, including long names, models, paths, and failed/unknown states.
- **Do** retain mail conversation access when phone layouts hide the separate Message button.
- **Do** use the existing Service, native semantics, and write-only secret contract.

### Don't:

- **Don't** introduce saturated branding, neon-on-black styling, cream surfaces, glass effects, or hero metric cards.
- **Don't** rely on hue alone or make ordinary healthy agents compete with attention cases.
- **Don't** collapse resume restart and fresh wake into an ambiguous Restart action.
- **Don't** promise a stopped agent stays stopped when wake on mail is enabled.
- **Don't** open the terminal automatically or show secret values in the configuration summary.
