# Product

## Register

product

## Users
People who don't write code and want a few AI assistants (roughly 1-15) working
for them on their own computer: a personal assistant, an inbox helper, a
writer, a bookkeeper. They check in several times a day, at a desk or on a
phone between other things. Their main job in lolad is to answer what an
assistant needs from them, above all the questions that block an assistant
until they decide. Their second job is to notice an assistant that can't start
or is stuck, and fix it with one guided step. They talk to assistants only by
message. Terminals, models, folders and policy rules exist, but behind
Advanced.

## Product Purpose
lolad is home for a small team of assistants: Home (what needs you, then your
conversations), Assistants, Team (who works with whom), Sharing (with a
friend's lolad) and Settings. Success is answering what an assistant needs in
seconds, and trusting at a glance that everyone else is fine.

## Brand Personality
Calm, warm, a little magical. "lolad Night": a midnight sky where each
assistant is a softly moving orb of light. Plain words everywhere: assistant,
not agent; Asleep, not stopped; "Can't start", not failed.
References: a calm night-mode messaging app; the orb as a living presence, not
a status chip.

## Anti-references
- Hacker terminals and log viewers on the main path.
- SaaS hero-metric KPI cards.
- Jargon: harness, slug, daemon, policy, exception, on the main path.
- Fake controls: a toggle on screen does something real, or it isn't there.
- Cream / paper / sand backgrounds.

## Design Principles
1. Home is home. What needs you first, then everything else is one click away.
2. Assistants are orbs; people are rings. Each assistant has its own colour
   from its name, and its orb moves with its state.
3. Bright periwinkle (#7AA2FF) means your turn: a ripple ring, a dot, the
   word "Needs you". It is also the primary-button colour, so it is never
   used as decoration. Amber is a real warning and rare; rose means
   something broke.
4. Plain words, one idea per sentence. Explain what happened and what to do.
5. Advanced is one click away, never in the way. Every technical control still
   exists, folded under Advanced.

## Visual system
- Dark only for now. Midnight gradient background (#07080F), cards at 3.5%
  white, hairlines at 8%.
- Type: Sora 200-300 for display, Manrope for UI, mono only behind Advanced.
  Both self-hosted in `static/vendor/fonts`.
- Pills for buttons, chips and tabs (44px tall); 22px card radius.
- Identity hues: Ion #6C9BFF, Lilac #A98BFF, Plasma #FF6BD6, Aqua #3DE8D0,
  Volt #C4F25A, Frost #DCE6FF. Your turn #FFB547. Failure #FF5C7A. Action
  #7AA2FF.
- Logo: a periwinkle ring with a dot (Main board), the "your turn" ring at
  rest (`static/logo.svg`, also the favicon and the site's logo; the desktop
  icons in `desktop/assets/icons/gen.go` are to be redrawn from it).

## Accessibility & Inclusion
WCAG 2.2 AA contrast. State is never conveyed by colour or motion alone: always
a word too. Full keyboard use. prefers-reduced-motion stops orb motion. Usable
at phone width.
