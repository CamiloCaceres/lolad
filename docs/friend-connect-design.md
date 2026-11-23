# Friend connect: technical design

Status: decided, being built (lolad-hub-dev, 2026-11-24). Decisions applied:
open sign-up is the default mode; agents offered on both sides talk without
asking; links last 7 days; limits are env-tunable defaults.

Draft 1 assumed an invite chain. Camilo chose **open sign-up**, so member
seat tokens are gone.

Product brief: `lolad-manager/docs/briefs/friend-connect.md`. Decisions:
- 1 = **open sign-up** on the default hub, with abuse controls, and
  operator invites kept as an optional mode;
- 2 = beta.1 waits for this;
- 3 = **paste the link**, with no URL scheme.

## Goal

Two people who have never heard of a hub connect their lolads with one link.

- **Ana:** **Invite a friend**, tick agents, **Create invite link**, and send
  it.
- **Ben:** install Lolad, paste the link, then **Connect with Ana**.

Afterwards each human can message the other and the agents the other
offered, with no approval cards. The words hub, handle, tenant and provider
appear only under Advanced.

## 0. Summary

| Piece | Where | What |
|---|---|---|
| Open sign-up | `internal/hub` | Register with **no code**, proving possession of the provider key, bound to this hub. The mode is `open` or `invite`: env default, switchable live in the console. |
| Abuse controls | `internal/hub` | New-account limits per IP, per /24 (/56) and per key; hourly, daily and total caps; reserved handles; banned keys; reaping sign-ups that never connected; the existing traffic limits. The operator can pause sign-ups, turn accounts off and ban. |
| Console | `internal/hub` | People sorted by newest, with "joined via open sign-up / invite". Health shows "New sign-ups (24 h)" and the sign-up mode with a pause switch. |
| Friend link | `internal/fed` | `https://lolad.caceresdev.com/join#2.<code>` carries pairing, display name and offered agents. Typical code 241 characters, worst case 1,053. |
| Auto-join | `internal/fed` plus the `HubMember` interface the app implements | **Creating** a link joins the default hub first if Ana is on none. **Accepting** joins the inviter's hub if Ben is on none. The handle comes from the tenant, with collision suffixes. |
| One-call accept | `internal/fed` | Local checks, then join, pair, shares and pre-approvals. If Ana is offline, it waits and finishes by itself. |
| Default hub | config | `federation.hub_url`, default `https://lolad-hub.caceresdev.com`. |
| Old paths | unchanged | `lolad-invite:` v1 codes, `hubinv_` codes and the Advanced hub card keep working. |

## 1. Hub: open sign-up

### 1.1 Mode

- `LOLAD_HUB_SIGNUP=open|invite` sets the default. It is **`open`** when
  unset (decided). A self-hosted hub that wants invite-only sets `invite`.
- A new `hub_settings(key TEXT PRIMARY KEY, value TEXT)` row `signup_mode`
  **overrides** the env once the operator changes it in the console. That
  makes **Pause sign-ups** an instant incident switch, with no redeploy.
- `GET /v1/hub/info` already returns `signup`. It now reports the live mode:
  `"open"`, `"invite"` or `"closed"`. `closed` means the account cap was
  reached or sign-ups are paused. Clients read it before trying.
- In `invite` mode everything behaves as today. In `open` mode a `hubinv_`
  code is still accepted (and consumed) if present, which lets the operator
  admit someone past a cap.

### 1.2 Register without a code: proof of possession

`POST /v1/hub/register` is unchanged in shape, with two body changes:
- `code` becomes optional;
- a new `hub` field must equal this hub's public URL.

The request is already signed by the new key: `X-AMP-Provider` is the handle,
and the signature covers `<ts>.<body>`, with `public_key` inside the body,
within ±300 s. That **proves possession** of the key.

Binding `hub` into the signed body stops a captured request being replayed
on another hub. A replay on the same hub, within the window, only repeats an
idempotent registration of the same key and handle.

No proof-of-work, at least at first. §1.4 keeps it as a lever.

**Responses, new or changed:**

| Case | Status | Body |
|---|---|---|
| created | 200 | `{handle, url}`, as today |
| same key and handle again | 200 | idempotent, as today |
| key already holds another handle | 409 `key_registered` | **now includes `handle`**, so the client adopts it |
| handle taken | 409 `name_taken` | `suggestion`, as today |
| handle reserved or invalid | 400 `invalid_handle` | `suggestion` when one exists |
| key banned | 403 `signup_refused` | generic "This Lolad can't join this hub" |
| a limit hit (§1.3) | 429 `rate_limited` | `Retry-After` |
| caps reached or paused | 503 `signup_closed` | `Retry-After` |
| `invite` mode, no or bad code | 403 `invalid_invite` | as today |

### 1.3 Abuse controls

A sign-up costs an attacker only a fresh key. So the real limits are on
**where sign-ups come from** and **how many happen in total**.

All values are env-tunable. They are enforced in this order, cheapest first.

| Limit | Default | Env |
|---|---|---|
| successful sign-ups per source IP | 3 / 24 h | `LOLAD_HUB_SIGNUPS_PER_IP` |
| successful sign-ups per IPv4 /24 or IPv6 /56 | 10 / 24 h | `LOLAD_HUB_SIGNUPS_PER_NET` |
| failed sign-ups per IP (bad signature, bad handle) | 10 / h (existing) | — |
| sign-ups hub-wide | 60 / h and 300 / 24 h | `LOLAD_HUB_SIGNUPS_HOURLY`, `LOLAD_HUB_SIGNUPS_DAILY` |
| accounts in total | 2,000 | `LOLAD_HUB_MAX_ACCOUNTS` (over it: `closed`) |
| one handle per key | always (existing) | — |
| banned keys | operator list | console |

The source is the real client IP, via the trusted-proxy rule that is already
in place (Traefik).

- **Counting:** in-memory windows are enough for per-IP and per-net limits.
  The hourly, daily and total caps are counted from `hub_accounts.created_at`
  in SQLite, so they survive restarts and redeploys.
- **Handle rules:** `[a-z0-9-]{3,32}` as before. The reserved list grows:
  `admin api hub www p v1 lolad support help root operator security console
  status mail abuse info`. It stays a constant in code.
- **Reaping:** an account that has **never opened a tunnel** within 72 h of
  signing up is removed by the housekeeping sweep, freeing its handle. This
  stops handle squatting by scripts that only register.
  - Accounts that connected at least once are never reaped automatically.
  - `LOLAD_HUB_REAP_UNUSED=72h`, where `0` turns it off.
- **Operator actions:**
  - Turn off and Turn back on (existing).
  - **Remove and block**: removes the account and adds its key to
    `hub_banned_keys`, so it cannot register again.
  - **Pause sign-ups** (§1.1).
- **The relay itself** stays bounded by today's rules:
  - only `/v1/federation/*`, GET or POST, 1 MB bodies, to registered handles
    with a live tunnel;
  - 300 req/min per handle and 600 req/min per IP;
  - 32 requests in flight per tunnel;
  - the hub cannot be pointed at arbitrary hosts.

  New, as a lever and a metric: **bytes carried per handle per 24 h**,
  shown in the console with an optional cap `LOLAD_HUB_BYTES_PER_DAY`
  (default off).
- **Reserve levers, not built now:** proof-of-work on register, a per-ASN
  limit, and a CAPTCHA-free email check. These are documented only, in case
  scripted sign-ups appear.

### 1.4 Store migration

The migration is idempotent and runs in `OpenStore`. It checks
`pragma table_info` before each `ALTER`.

```sql
ALTER TABLE hub_accounts ADD COLUMN joined_via TEXT NOT NULL DEFAULT 'invite'; -- 'open' | 'invite'
ALTER TABLE hub_accounts ADD COLUMN first_tunnel INTEGER;                     -- unix; NULL = never connected (reaping)
CREATE TABLE IF NOT EXISTS hub_settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS hub_banned_keys(public_key TEXT PRIMARY KEY, handle TEXT, banned_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS hub_accounts_created ON hub_accounts(created_at);
```

- The first `tunnel up` sets `first_tunnel` if it is NULL.
- Existing accounts get `first_tunnel = last_seen` when `last_seen > 0`, so
  they are never reaped.
- No sign-up IP is stored in the database. The JSON log line `registered`
  carries `ip` and `via` for investigations. The container log retention is
  the retention.
- Old binaries ignore the new columns and tables, so a rollback is safe.

### 1.5 Console and admin API

**People:**
- sorted newest first by default (with a "Sort: name" link);
- a "New" badge for the last 24 h;
- "Joined 24 Nov, 10:42 · open sign-up" or "· invite for Ana";
- "Never connected" for reaping candidates;
- "Carried today" in messages and MB;
- actions Turn off, Turn back on, **Remove and block** (with a confirm).

**Health:**
- "New sign-ups: 12 in 24 h";
- "Sign-ups: Open / Paused / Invite only (full)", with the **Pause
  sign-ups** / **Open sign-ups** switch (POST + CSRF);
- the remaining capacity ("1,840 of 2,000 places left").

**Invites:** unchanged. It notes "Not needed while sign-ups are open; use it
to let someone in past a pause or the cap."

**Admin API additions:**
- `GET /v1/hub/admin/status` adds `signup_mode`, `signups_24h` and
  `accounts_max`.
- `PUT /v1/hub/admin/signup {mode: "open"|"invite"}`.
- `POST /v1/hub/admin/accounts/{h}/block`.
- `GET /v1/hub/admin/accounts` adds `joined_via`, `first_tunnel` and
  `bytes_today`.

## 2. The friend link

### 2.1 Shape

```
https://lolad.caceresdev.com/join#2.<base64url(compact JSON)>
```

- **The fragment never reaches a server.** Browsers do not send it, and link
  previews in Slack, WhatsApp and iMessage don't see it. The site's `/join`
  page decodes it **in the browser** only to greet: "Ana invited you to
  Lolad".
- **The `/join` page must** load no third-party scripts or analytics, set
  `Referrer-Policy: no-referrer`, and never send the code in any request or
  query string.
- **Lolad accepts any of:** the full link, the part after `#`, the same with
  chat-app whitespace or line breaks, or a v1 `lolad-invite:` code.

### 2.2 Payload (v2)

| Key | Meaning | Size |
|---|---|---|
| `t` | inviter tenant (provider is `t` + `.lolad.local`) | ≤ 32 |
| `d` | inviter display name (a greeting hint, never trusted) | ≤ 40 |
| `n` | inviter human's local name (`n@t.lolad.local`) | ≤ 32 |
| `k` | inviter provider key, raw Ed25519, base64url | 43 |
| `s` | pairing secret, 16 bytes, base64url | 22 |
| `h` | inviter's handle on its hub. URL = `<hub>/p/<h>` | ≤ 32 |
| `H` | that hub's URL, **only when not the default** | ≤ 60 |
| `u` | inviter URL, only when not on a hub (Tailscale or direct) | ≤ 100 |
| `a` | offered agents, `[[slug, display], …]`, at most 8 | ≤ 8 × 50 |
| `e` | expiry, unix seconds (7 days) | 10 |

**Sizes**, measured with sample payloads:
- A typical link with 2 agents is **241** characters of code, 275 with the
  URL.
- The worst case (8 agents with 32-character slugs, every field at its
  maximum, a non-default hub) is **1,053** (1,087 with the URL).

**Hard limits:**
- refuse to create a link over 1,500 characters (lolad drops agent display
  names first);
- the accept side refuses input over 4,096.

**Trust.** `k` pins Ana's key, as in v1: pairing must answer with it.
`d`, `n` and `a` are display hints only.
- What Ana actually shares and pre-approves is stored **on Ana's side** when
  she makes the link.
- After pairing, Ben's lolad refreshes the agent list from Ana's directory.

## 3. Create and accept (internal/fed)

### 3.1 Create, on Ana's side

`CreateFriendInvite(ctx, {Agents []string, For string})`:

1. **Make sure Ana is reachable.**
   - If she is on a hub, or has a direct `federation.url`: keep it.
   - Otherwise, if `federation.hub_url` is set, run **auto-join** (§3.3) on
     the default hub.
   - If that fails (hub down, closed, refused): return a `UserMessage`, for
     example "Lolad couldn't reach its network to make the link. Try again in
     a few minutes." or "The Lolad network isn't taking new people right now.
     Try again later."
2. **The fed invite row:**
   - the secret's hash;
   - `expires = now + 7 days`;
   - new columns `kind = 'friend'`, `offer` (JSON slugs) and `for_note` (her
     private "Ben").
3. **Return the link once,** as today. Open links are listed without codes
   afterwards (§6).

`CancelFriendInvite(id)` deletes the row; the link stops working.

### 3.2 Accept, on Ben's side: one call

`AcceptFriendInvite(ctx, link, {Agents []string})` returns
`{Connection, State: "connected" | "waiting", Message}`.

Ordered so that **nothing changes until all local checks pass**:

1. **Parse and validate,** with no side effects:
   - expiry `e`;
   - the same tenant, which gives the existing `sameNameError`;
   - an existing peer:
     - with another key: denied;
     - with the same key: only apply the choices.
   - Ben's offered agents exist.
2. **Make sure Ben is reachable.**
   - If he is already on any hub, or has a direct URL that Ana can reach:
     keep it.
   - Rule for v1: a direct-URL-only Ben still joins if the link points at a
     hub, because a hub member can rarely reach a tailnet URL.
   - Otherwise, **auto-join the inviter's hub** (`H`, or the default), §3.3.
3. **Pair,** the existing `POST pair` to Ana's URL. `pairBody` gains two
   optional fields:
   - `user`: Ben's human's local name;
   - `offer`: Ben's offered slugs.

   Old lolads ignore them, so the version stays 1.
   - **If Ana is offline (503 `provider_offline`, or unreachable):** store a
     **pending accept** and return `State: "waiting"` (§3.4).
4. **Apply on Ben's side:**
   - the peer gets `UserReachable = true`;
   - `Shares = [{Local: Y}]` for each agent Ben offered (Remote empty, so
     anyone there can ask);
   - **grants** with `State allowed` and `Origin "invited"`:
     - `n@t` (Ana) → Ben's human;
     - `n@t` → each Y.
5. **Ana's side, in the `pair` handler, when the secret's row is
   `kind = 'friend'`:** the mirror image from her stored `offer`:
   - `UserReachable`;
   - shares for her X's;
   - grants `user@ben` → Ana's human and `user@ben` → each X.

   It adds the event and notification "Ben connected using your invite link
   (made 24 Nov for 'Ben')", so a link used by the wrong person is noticed.

**Pre-approved:** each human and each offered agent, to the other side's
human and offered agents (all pairs, both directions).

Shares stay "anyone there can ask" rather than `Remote: [..]`, because a
non-empty `Remote` would mean "only these people" and shut out the other
side's agents.

**Offered agents talk without asking (decided).** Each side also grants
every pair across `{other human} ∪ {other side's offered agents}` →
`{own human} ∪ {own offered agents}`, with `Origin "invited"`. Ana's side
learns Ben's offer from `pairBody.offer`; Ben's side learns Ana's from the
link `a`, checked against Ana's directory after pairing.

Agents that were **not** offered still need approval. The invite and accept
screens show one plain line: "Ben and his chosen agents can message these
agents of yours without asking."

**Refusals carry `UserMessage()`** (the dashboard already shows these):

| Case | Message |
|---|---|
| expired | "This invite has expired. Ask Ana for a new link." |
| used | "This invite was already used. If that wasn't you, tell Ana." |
| same name | the existing same-name message |
| hub closed | "The Lolad network isn't taking new people right now. Try again later." |
| hub unreachable | "Lolad couldn't reach its network. Check your internet and try again." |

### 3.3 Auto-join and the auto-handle

`HubMember.Join(ctx, hubURL, preferred)`:

1. **Read the hub's info.**
   - `signup: "invite"`: stop with "This network is invite-only". It is
     joinable under Advanced with a `hubinv_` code.
   - `"closed"`: the closed message.
2. **Preferred handle:** the tenant if valid and not reserved. Otherwise its
   slug, padded to 3 characters and suffixed `-1`.
3. **Register,** with no code:
   - **409 `name_taken`:** retry with the hub's `suggestion`, then `-2` and
     `-3`, so at most 4 attempts, which do not count as failures on the hub.
   - **409 `key_registered` with `handle`:** adopt that handle. This happens
     when Ben reinstalls and keeps his state.
   - **400 `invalid_handle` with `suggestion`:** use the suggestion.
4. **Write `hub.yaml`,** set the federation URL to `<hub>/p/<handle>`, and
   start the tunnel. This is today's `JoinHub` tail, refactored to be
   code-optional.

The handle is visible only under Advanced, which keeps today's Hub card.

### 3.4 Waiting for Ana

- A pending accept (`fed_pending` row) is retried in these cases:
  - when the hub announces Ana `online`. Pending handles join the tunnel's
    watch list, so the existing presence machinery wakes it;
  - every 10 min as a fallback;
  - at startup.
- On expiry: the event `friend_expired`, and "Ana's Lolad wasn't online
  before the invite expired. Ask her for a new link."
- The dashboard shows "Waiting for Ana's Lolad to come online. You'll be
  connected automatically."
- **Retries are safe.** The pair secret stays valid until consumed, and
  pairing is already idempotent for the same consumed secret, so a lost
  response does not break the connection.

### 3.5 Boundaries

fed does not import `internal/hub` or `internal/app`. It declares:

```go
// HubMember is this lolad's hub membership; the app implements it.
type HubMember interface {
	Membership() (hubURL, handle string, ok bool)
	Join(ctx context.Context, hubURL, preferredHandle string) (memberURL string, err error) // open sign-up
}
// Wired with Engine.SetHub(member, defaultHub, joinURL). Pending accepts need
// no Watch: their URLs are added to PeerURLs, which the tunnel client already
// watches, and RetryURL retries them when the hub reports the inviter online.
```

- `internal/hub` gains the codeless `Register`, the `handle` in the
  `key_registered` error, and `Info` (the info client).
- The app implements `HubMember` by refactoring `JoinHub`. The Advanced card
  keeps calling `JoinHub` with a code.

## 4. Default hub

- **Config:**
  - `federation.hub_url`, default `https://lolad-hub.caceresdev.com`;
  - env override `LOLAD_HUB_URL`;
  - empty turns auto-join off. Friend links then need an existing hub or a
    direct URL.
- **Join link base:** `federation.join_url`, default
  `https://lolad.caceresdev.com/join`.
- Both keys belong to the registry or integrator (config schema).
- **Changing the default later:**
  - Links carry `H` whenever the inviter's hub isn't the default **of the
    build that made the link**.
  - A link without `H` means that build's default.
  - So change the default only in a release, and keep the old hub running
    for the 7-day link TTL. Members stay on the hub written in their
    `hub.yaml`.

## 5. Security trade-offs

**Open sign-up makes the hub a public relay.** What limits it:
- It relays only to registered handles with a live tunnel, only
  `/v1/federation/*`, with 1 MB bodies, and rate limited per handle and per
  IP.
- It cannot fetch or forward to arbitrary hosts.
- Federation itself is signed and checked by the receiving lolad. The hub
  cannot forge, and unsolicited mail to a lolad still meets pairing and
  approvals.

The realistic abuses, and their controls:
1. **Account farming and handle squatting:** the per-IP and per-net sign-up
   limits, the hourly, daily and total caps, and reaping never-connected
   accounts.
2. **Using a seat as a cheap tunnel for something else:** only the
   federation paths pass, plus the per-handle byte metric and an optional
   cap.
3. **Flooding a member:** the per-handle and per-IP request limits (existing).
   The member can turn sharing off; the operator can turn the account off.
- **Pause sign-ups** is the one-click incident switch.

**A leaked friend link.** Whoever has it, once, within 7 days, can pair with
Ana and is pre-approved to message Ana and the agents she offered.
- Ben's later attempt fails with "already used. If that wasn't you, tell
  Ana."
- Ana is notified on use, with the accepter's name and time, and can remove
  the connection.
- Ana can cancel open links.
- Nothing reaches terminals, files or settings (unchanged).

**Compared with a v1 code leak,** the new exposure is the pre-approval: the
price of no toggles. The invite screen says: "Anyone with this link can
connect as your friend. Send it in a private chat."

**The hub is down:**
- **Creating** a link needs the hub only when Ana must first join. A member
  whose hub is down can still create a link; Ben's accept then waits.
- **Accepting** needs the hub to join; it fails with the unreachable message.
  When the join succeeded but Ana is unreachable, it waits (§3.4).
- Existing connections queue in the outbox, as today.
- Heimdall's monitor on `/v1/hub/info` covers it.

**Privacy:**
- The link shows Ana's display name, tenant, handle and the offered agent
  names to whoever holds it.
- The hub sees handles, keys, traffic metadata and, transiently, request
  contents. TLS still ends at the hub (unchanged); end-to-end encryption
  remains on the roadmap. The invite screen links to "What the network can
  see".
- With open sign-up, handles of anyone are guessable URLs (`/p/<handle>`). A
  stranger can reach a lolad's `pair` endpoint, as today with a public URL,
  but it needs a valid secret, and failures are rate limited.
- Tenants derived from OS logins become handles. People who mind can rename
  before joining (Advanced).

**Display names are self-chosen.** The provider stays visible under Advanced
for anyone checking.

## 6. What the dashboard (lolad-web) needs from me

`core.FriendInviter` is an optional interface on the service. The API mounts
`/api/friends/*` only when the service implements it.

```go
type FriendInviter interface {
	CreateFriendInvite(ctx context.Context, r FriendInviteRequest) (FriendInvite, error) // link returned once
	FriendInvites(ctx context.Context) ([]FriendInviteInfo, error)                         // open and used, never codes
	CancelFriendInvite(ctx context.Context, id string) error
	PreviewFriendInvite(ctx context.Context, link string) (InvitePreview, error)           // parse only: no network, no side effects
	AcceptFriendInvite(ctx context.Context, link string, r AcceptRequest) (AcceptResult, error)
}
type FriendInviteRequest struct{ Agents []string; For string }
type FriendInvite struct{ ID, Link string; ExpiresAt time.Time }
type FriendInviteInfo struct{ ID, For string; Agents []string; CreatedAt, ExpiresAt time.Time; UsedByDisplay string; UsedAt time.Time }
type InvitePreview struct{ FromDisplay string; Agents []AgentOffer; ExpiresAt time.Time; Expired, SameName, AlreadyConnected, JoinsNetwork bool }
type AgentOffer struct{ Slug, Display string }
type AcceptRequest struct{ Agents []string }
type AcceptResult struct{ Connection Connection; State, Message string } // State: connected | waiting
```

- Every refusal implements `UserMessage() string`.
- **Events:** `friend_connected` (both sides, with the other's display name),
  `friend_waiting` and `friend_expired`.
- **Routes:**
  - `POST /api/friends/invites`
  - `GET /api/friends/invites`
  - `DELETE /api/friends/invites/{id}`
  - `POST /api/friends/preview {link}`
  - `POST /api/friends/accept {link, agents}`

  The link goes in POST bodies only.
- **Onboarding** ("Got an invite from a friend?") uses Preview then Accept,
  with `Agents` empty before the first agent exists. Ben can share agents
  later from the friend's card.
- The **Advanced** section keeps today's hub card: status, handle, Leave, and
  joining with a `hubinv_` code for invite-only hubs.

## 7. SPEC.md changes (I draft them with the code; lolad-manager reviews)

**Hub (v5):**
- The sign-up mode (env plus the console override) and `info.signup` values.
- Codeless register: `hub` bound in the signed body, the response table.
- The abuse controls and their env vars, and reaping.
- `hub_settings` and `hub_banned_keys`; `joined_via` and `first_tunnel`.
- Console: new sign-ups, Pause, Remove and block; admin API additions.
- The reserved handle list.
- The "Invite-only for now" line becomes "open or invite-only, per hub".

**Federation:**
- Invite v2 (the link, the payload, the size limits, the fragment rule, the
  7-day TTL).
- `pairBody.user` and `pairBody.offer`.
- `fed_invites.kind/offer/for_note` and `fed_pending`.
- Grant origin `"invited"`.
- The one-call accept order and pending accepts.
- `core.FriendInviter`.
- v1 codes unchanged.

**Config:** `federation.hub_url` and `federation.join_url`.

**Site:** `/join` (fragment only, no third-party scripts, `no-referrer`).

## 8. Test plan

**Hub:**
- **Codeless register:**
  - open mode succeeds;
  - invite mode without a code is refused;
  - a `hub` mismatch is refused;
  - a replay is idempotent;
  - a bad signature counts as a failure.
- **Limits:** per IP, per /24 and /56, hourly, daily and total caps (with a
  fake clock and seeded `created_at`), and `Retry-After` on each.
- **Console override:**
  - the console's Pause beats the env;
  - `info.signup` reflects it.
- **Accounts:**
  - ban, then a re-register with the same key is refused;
  - reaping: never-connected accounts go after 72 h, connected ones never;
  - the migration backfills `first_tunnel`;
  - the 409 `key_registered` carries `handle`, and `invalid_handle` carries a
    suggestion.
- **Migration:** on a DB from today's schema; opening twice is a no-op.
- **Console and admin API:** People order, the badges, the Pause switch with
  CSRF, block.
- `TestNoSecretsInLogs` extended.

**Fed**, with the existing httptest hub and two engines:
- **Codec:**
  - round trip;
  - `FuzzParseFriendLink`, which never panics and refuses input over 4,096;
  - worst case under 1,500, dropping display names when needed;
  - mangled whitespace;
  - v1 codes still accepted.
- **Create:** auto-joins when Ana is on no hub; a member doesn't rejoin; a
  closed or invite-only hub gives the right `UserMessage`.
- **Accept, happy path:**
  - Ben auto-joins (handle collision suggestion used; `key_registered`
    adopted), then pairs;
  - both sides end with `UserReachable`, the shares and the `invited` grants;
  - Ben's human messages Ana's offered agent with no card;
  - Ben's agent → Ana's agent raises exactly one card.
- **No side effects on refusal:** same name, expired, wrong key: no hub
  account is created.
- **Ana offline:** waiting, then `online`, then connected with no user action;
  expiry gives `friend_expired`.
- **Replay:** a second accept from another lolad is refused, and Ana is
  notified of the first.
- **Cancel:** the link stops pairing.

**App:**
- the `HubMember` implementation;
- the default `hub_url`, its env override, and empty turns auto-join off;
- `JoinHub` with a code still works.

**Manual:** a new section in `docs/e2e-hub.md`, "two fresh installs, one
link". It is the rehearsal for Camilo's hands-off acceptance test, run once
with A on Mac and B on Windows.

## 9. Size estimate

| Part | Code | Tests |
|---|---|---|
| hub: mode and settings, codeless register, limits and caps, reaping, ban, migration, console and admin API, `Info` client | ~400 | ~350 |
| fed: v2 codec, create (auto-join), one-call accept, pair-side apply, pending and retry | ~600 | ~550 |
| app: `HubMember`, `JoinHub` refactor, config defaults | ~120 | ~80 |
| core and api: `FriendInviter` types and `/api/friends/*` | ~150 | ~100 |

That is about **1,270 lines of code and 1,080 of tests**, roughly **3 working
days** for me.

**Order:**
1. **Hub** (day 1). The default is `open`, so if it is deployed before the
   clients ship, Heimdall sets `LOLAD_HUB_SIGNUP=invite` until the release.
2. **fed and app** (days 2 and 3).
3. **The API.**

lolad-web can build against `PreviewFriendInvite` and `AcceptFriendInvite`
fakes from day 1.

**Outside my usual paths:**
- `internal/app/hub.go` (`HubMember`, the `JoinHub` refactor);
- `internal/core/federation.go` (types);
- `internal/api` (routes);
- config defaults.

These need a one-off permission, as before, or the integrator.

## 10. Open questions

All decided (2026-11-24):
1. Offered agents talk without asking: **on** (§3.2).
2. Limits: the §1.3 defaults, set by env.
3. The default mode is `open`.
4. Links last **7 days**.
