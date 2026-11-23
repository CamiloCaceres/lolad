# Connect with a friend

Connecting your lolad with a friend's lets the two of you message each other,
and lets your assistants work with the assistants you each choose to share.
You stay in charge: you pick which assistants they can reach, and you approve
every new person before their first message gets through.

Connecting does **not** give your friend your files, your settings, your
assistants' terminals or anything else on your computer. It only carries
messages, and only the ones you allow.

This guide is for the two people connecting. If you run a hub for others, see
[For the hub operator](#for-the-hub-operator) at the end.

## Before you start: give your lolad its own name

Every lolad has a short name that appears in your addresses, for example
`ultron@milo.lolad.local`. **Two connected lolads must have different names.**
A new lolad is called `home` unless you change it, so if both of you kept the
default, the connection is refused with "Rename the tenant on one side".

Check yours under **Sharing → Advanced → Identity**. If it says
`home.lolad.local`, pick a name for yourself (your first name works well) and
change it **before** you connect with anyone:

1. Open the file `org.yaml` in the `.config/lolad` folder in your home folder.
2. Change the line `tenant: home` to your name, for example `tenant: milo`.
   Use lowercase letters, digits and dashes only.
3. Quit lolad and open it again.

Only one of you has to do this, but it is simplest if you both do. Changing the
name after you have connected with someone breaks that connection, and you
would need to connect again.

## Pick a route

Your two lolads need a way to reach each other over the internet. There are
two:

| | **Hub** | **Tailscale** |
|---|---|---|
| Good for | Friends on different networks, nothing to install | People who already use Tailscale together |
| You need | A sign-up code from whoever runs the hub | Both computers on the same tailnet, reachable both ways |
| Who carries your messages | The hub passes them on; it keeps none of them | They go straight from one computer to the other |
| Effort | A few minutes, all in lolad | Some setup on each computer |

Not sure? Use the hub.

## Route 1: through a hub

A hub is a small relay server. Your lolad keeps a connection open to it, and
your friend's messages reach you through it, so neither of you needs to open
anything on your home network.

What the hub can and cannot do: it can see that a message went from one lolad
to another, and it could read it in transit, because messages are not yet
end-to-end encrypted (that is planned). It **cannot** fake a message from you,
because every message is signed by your own lolad and checked by your
friend's. It stores no mail. If your friend's lolad is off, your message waits
on your computer and goes out when they are back.

### 1. Get a sign-up code from the hub's operator

Ask whoever runs the hub (for lolad's own hub, that is Camilo) for an invite.
They make it in the hub's console and send you a code that starts with
`hubinv_`. Each of you needs your own code.

- A code works **once**, for **7 days**.
- Treat it like a password. It should arrive in a private chat, not a group
  or a public post. If you think someone else saw it, ask for a new one.

### 2. Join the hub

In lolad, open **Sharing**, then **Advanced**, then the **Hub** card:

1. **Hub address:** the address the operator gave you, for example
   `https://lolad-hub.caceresdev.com`.
2. **Your handle:** the name people on this hub will know your lolad by, for
   example `milo`. Lowercase letters, digits and dashes, 3 to 32 characters.
   If it is taken, lolad suggests another one.
3. **Sign-up code:** paste the `hubinv_…` code.
4. Choose **Join hub**.

The card then shows the hub, your handle and **Connected**. Your friend does
the same with their own code. You now each have a public address on the hub,
and you can connect with each other.

### 3. Connect with each other

This step is the same for both routes. Go to
[Connect](#connect-make-and-accept-an-invite).

## Route 2: over Tailscale

Use this when you and your friend already share a Tailscale network (a
tailnet), or are happy to set one up.

1. Each of you sets up lolad's remote access with Tailscale on your own
   computer. The steps are in [remote.md](remote.md). Your lolad's web address
   then looks like `https://your-computer.your-tailnet.ts.net:4100`.
2. Make sure **each computer can reach the other**. Sharing a computer through
   Tailscale only works in one direction, so if one of you shared a machine
   with the other, share the other one back too. Open the other person's
   address in a browser from your computer: if a lolad page loads, the route
   works.
3. Connect, as below.

The technical details (configuration keys, the command line) are in
[federation.md](federation.md).

## Connect: make and accept an invite

One of you invites, the other accepts. It does not matter who.

**If you are inviting:**

1. Open **Sharing** and choose **Make an invite code** under **Invite someone**.
2. Copy the code. It starts with `lolad-invite:` and is shown only this once.
3. Send it to your friend privately. It works once, for 3 days.

**If you are accepting:**

1. Open **Sharing** and paste the code under **Got an invite?**
2. Choose **Connect**.

You now see each other under **Connected**. Your friend's card shows their
lolad's name, for example `ana.lolad.local`. Check with them that it is the
name they see under **Sharing → Advanced → Identity**.

## Choose what to share

A new connection shares nothing. On the **Sharing** page, in your friend's
card:

- **People there can message me:** turn this on so your friend can write to
  you directly. You still approve each new person.
- **You share:** pick the assistants your friend can reach. For each, choose
  whether **anyone there can ask** (you approve each new person) or **only
  these people**.

The first time someone new writes to you or one of your assistants, the
message waits for you. You see a card in your inbox and choose to allow or
block them. Nothing reaches your assistant until you allow it.

## If something goes wrong

| What you see | What it means | What to do |
|---|---|---|
| "Rename the tenant on one side" | Both lolads have the same name | One of you renames, as in [Before you start](#before-you-start-give-your-lolad-its-own-name), then make a new invite |
| "sign-up code is invalid, expired, or already used" | The hub code was mistyped, used, or older than 7 days | Ask the operator for a new one |
| "handle is taken (try "milo-2")" | Someone on that hub already has that handle | Use the suggestion or pick another |
| Hub card says **Disabled by the hub** | The operator turned your lolad off on the hub | Ask the operator |
| Your friend shows **Unreachable** or **Not seen yet** | Their lolad is off, or (Tailscale) your computer cannot reach theirs | Ask them to open lolad. Your messages wait and go out when they are back |
| Your message shows **waiting for your OK** on their side | They have not approved you yet | Nothing to fix; they decide |
| The invite code expired | `lolad-invite:` codes last 3 days | Make a new one |

## Leaving

- **Disconnect from one friend:** in their card on **Sharing**, choose
  **Remove…**. No new messages pass between you.
- **Leave a hub:** **Sharing → Advanced → Hub → Leave hub…**. This releases
  your handle. Friends who reached you through the hub cannot reach you any
  more, so tell them first, or connect again another way.

## For the hub operator

You run the hub, so you hand out the sign-up codes.

1. Open the hub's console at `https://<hub address>/console` and sign in with
   the admin secret the hub was started with.
2. Go to **Invites**, type who the invite is for (only you see that note), and
   choose **Create invite**.
3. Copy the `hubinv_…` code. It is shown **only on this page, only this once**.
   The hub keeps only a fingerprint of it, so it cannot show it again.
4. Send it to that person privately (a direct message only they can read),
   with the hub address and this guide.
5. Under **Waiting to be used** you can cancel an invite you no longer want.
   Once they join, it moves to **Used** and they appear under **People**.

On **People** you can see who is connected and turn someone off. That closes
their connection at once and the hub stops carrying their messages. Their own
lolad keeps working, and you can turn them back on. **Health** shows how much
the hub is carrying. Neither page shows anyone's messages: the hub never
stores them.

No console? The same is possible on the hub's server with
`lolad-hub invite --note "Ana"`, `lolad-hub accounts` and
`lolad-hub disable <handle>` (see `deploy/hub/README.md`).
