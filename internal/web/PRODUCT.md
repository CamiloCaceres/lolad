# Product

## Register

product

## Users
One operator, milo (Camilo Caceres), running a small staff of long-running AI
agents (roughly 5-15) under lolad. He checks in several times a day, at a desk
or on a phone between other things. His primary job on the dashboard is to read
and answer agent mail fast, above all the `ask` messages that block an agent
until he decides. His secondary job is to notice an agent that is crashed,
failed or stuck waiting, and kick it. He talks to agents only through mail; he
never wants to read terminals or transcripts. The terminal exists for debugging.

## Product Purpose
The lolad dashboard is the operator's console for that staff: an inbox that
feels like a focused messaging client, a fleet view, the org chart and its
messaging policy, agent creation, and an audit log of all mail. Success is the
operator answering what agents need in seconds, and trusting at a glance that
everyone else is fine.

## Brand Personality
Calm, exact, quiet. A well-run control room, not a hacker terminal. Dense but
legible; nothing shouts unless it needs the operator.
References: Linear's density and keyboardability; Apple Mail / Messages for the
inbox thread feel; Grafana-like discipline about state color, but far quieter.

## Anti-references
- Neon-on-black "AI ops" dashboards.
- SaaS hero-metric KPI cards.
- An inbox that reads like a log viewer.
- Glassmorphism.
- Cream / paper / sand backgrounds.

## Design Principles
1. The inbox is home. Everything else is one click away and secondary.
2. Hue means state. Neutrals carry the interface; color appears only where the
   operator should look (an agent's state, something waiting on him).
3. Your turn is unmistakable. Pending asks and unread mail are the loudest
   things on any screen, and nothing else competes with them.
4. Density with air. Many rows, small type, but generous line height and a
   strict rhythm so it never feels cramped.
5. Familiar over clever. Standard controls and patterns; the tool disappears
   into the task.

## Accessibility & Inclusion
WCAG 2.2 AA contrast. State is never conveyed by hue alone: always a word or a
glyph too. Full keyboard use. prefers-reduced-motion respected. Usable at phone
width.
