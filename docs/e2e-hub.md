# Two-network end-to-end test through the hub

A manual checklist for the live hub, `https://lolad-hub.caceresdev.com`. Two
lolads on different networks join the hub, connect with each other and
exchange mail. The run also checks that the hub survives a redeploy, that it
sees callers' real IPs behind Traefik, and that it keeps no mail or codes. With
a Windows PC as machine B, it doubles as the Windows check.

Background: [connect-a-friend.md](connect-a-friend.md) (the user-facing
steps) and `deploy/hub/README.md` (the hub, its console and its logs).

## Roles

| Who | Does |
|---|---|
| **Operator** (Camilo, or Heimdall) | Signs in to the hub console, makes the invites, turns accounts off and on. Holds the admin secret; nobody else needs it |
| **A** | Camilo's Mac, home network |
| **B** | A second machine on a **different network** (for example the Windows PC on another connection, or a phone hotspot), with no tailnet shared with A |
| **Heimdall** | Container logs and the redeploy (steps 3, 8, 9) |
| **lolad-hub-dev** | Hub checks that need no secret (step 0), and reading the results |

Record for each step: pass or fail, the time, and a screenshot or command
output. Note every time you had to wait.

## Before the run

- [ ] A and B run the same lolad build (note the version on both).
- [ ] B is **not** on A's network. On B, `tailscale status` shows A as
      absent, or Tailscale is off.
- [ ] Names differ. A is `home`. On B, check **Sharing → Advanced →
      Identity**. A fresh v0.2 install shows its own name (from the Windows
      account), so it is fine. If B shows `home.lolad.local`, rename it as in
      [connect-a-friend.md](connect-a-friend.md#before-you-start-give-your-lolad-its-own-name)
      before going on. Note which happened: it tests the new default.
- [ ] Write down B's public IP: open `https://ifconfig.me` on B.

## Steps

**0. The hub is up** (lolad-hub-dev, no secret)
- [ ] `curl https://lolad-hub.caceresdev.com/v1/hub/info` gives 200 with
      `"public_url":"https://lolad-hub.caceresdev.com"`.
- [ ] `/console` redirects to `/console/signin`, and
      `/v1/hub/admin/status` without a secret gives 401.
- [ ] The response has a `Strict-Transport-Security` header (asked of
      Heimdall; note if missing).

**1. Invites** (operator)
- [ ] Sign in at `/console`. People is empty or lists only known accounts.
- [ ] Invites → **Create invite** for "A", then again for "B". Each shows a
      `hubinv_…` code once, and the "Invite someone else" page no longer shows it.
- [ ] **Waiting to be used** lists both.
- [ ] Send each code privately to its machine. The hub address to give is
      `https://lolad-hub.caceresdev.com`.

**2. Join** (A, then B)
- [ ] **Sharing → Advanced → Hub**: hub address, a handle (A: `milo`, B: for
      example `camilo-pc`), the code, then **Join hub**.
- [ ] The hub card shows the handle and **Connected**. `lolad hub status`
      agrees.
- [ ] Console: People shows both **Connected**. Invites moves both codes to
      **Used** with the right handles.
- [ ] Try one wrong thing on purpose on B: join again with A's used code. B
      shows the "sign-up code is invalid, expired, or already used" message.

**3. Real client IP behind Traefik** (B, then Heimdall)
- [ ] On B, open `/console/signin` and sign in with a wrong secret once.
- [ ] Heimdall: the container log has one `admin sign-in refused` line whose
      `ip` is B's public IP from "Before the run", not a `10.x`, `172.x` or
      `192.168.x` address. The secret typed is nowhere in the log.

**4. Connect A and B**
- [ ] A: **Sharing → Make an invite code**, and send the `lolad-invite:` code
      privately to B.
- [ ] B: paste it under **Got an invite?**, then **Connect**.
- [ ] Both see the other under **Connected**, with distinct names
      (`home.lolad.local` and B's).

**5. Mail both ways**
- [ ] A: in B's card, turn on **People there can message me**, then **Save**.
- [ ] B sends A's human a message. A sees an approval card. Before approving,
      B shows the message as waiting.
- [ ] A approves. The message appears in A's inbox, and B's copy shows
      delivered. Time from approval to B seeing "delivered":  ____
- [ ] A replies, and B receives it. Time:  ____
- [ ] Console Health: the requests show as **Carried 200** on
      `/p/<handle>/v1/federation/…`, and "Carried in 24 hours" went up.

**6. Offline, then catch-up**
- [ ] Take B offline: turn off its network (Wi-Fi off or cable out). Do not
      stop processes.
- [ ] Within about 90 s, console People shows B **Offline**.
- [ ] A sends B a message. A's card shows it **on the way**. Console Health
      shows a **Not connected 503** for B's handle.
- [ ] Turn B's network back on. B's hub card returns to **Connected** within
      about 60 s, and the queued message arrives within seconds of that, not
      within the hour. Time from reconnect to arrival:  ____

**7. Turn off and on** (operator)
- [ ] People → **Turn off** B. B's hub card shows **Disabled by the hub**
      within seconds.
- [ ] A sends B a message. It does not arrive, and A's card shows it on the
      way. Console shows **Turned off 403**.
- [ ] **Turn back on**. B reconnects within about 60 s, and the queued message
      arrives.

**8. Redeploy keeps everything** (operator and Heimdall)
- [ ] Before: note the console's People list (handles, joined dates) and Used
      invites. Heimdall can also run `lolad-hub accounts` in the container.
- [ ] Heimdall redeploys the hub, with no change.
- [ ] After: `/v1/hub/info` is 200 again. The operator must sign in again
      (expected: sessions are in memory). Health charts start over (expected).
- [ ] The same accounts, joined dates and Used invites are there. **Nobody
      rejoined or used a new code.**
- [ ] A's and B's hub cards are back to **Connected** within about 60 s.
- [ ] A sends B a message after the redeploy, and it arrives.

**9. The hub keeps no mail or codes** (Heimdall)
- [ ] In the container logs, search for a distinctive word from the step 5
      and step 6 messages: no match.
- [ ] Search for `hubinv_` and `lolad-invite:`: no match.
- [ ] Log lines for forwards carry only handle, path, status and ms.

**10. Leave** (B)
- [ ] **Sharing → Advanced → Hub → Leave hub…** and confirm.
- [ ] Console People no longer lists B. B's Used invite row stays.
- [ ] A sends B a message. It is not delivered, and A's card shows B as
      unreachable ("recipient not found").

## Windows notes (when B is the Windows PC)

- [ ] The desktop app installs and onboarding completes. The name step shows
      "Your lolad's name is …" with the Windows account name.
- [ ] Settings live in `%USERPROFILE%\.config\lolad\`. `org.yaml` there has
      that same tenant.
- [ ] Note anything that differs from the Mac: labels, paths, notifications,
      and whether the hub card reconnects after sleep.

## After the run

- Send the filled checklist to lolad-manager.
- The operator turns off or removes test accounts that should not stay
  (console, or `lolad-hub rm <handle>` in the container).
- Failures go to lolad-manager with the step number, times and evidence.
