# lolad client API (API 1)

Use the configured HTTPS `remote.public_url` as the base URL. HTTPS is supplied by
Tailscale Serve; being on the tailnet alone does **not** grant access. Requests
and responses use JSON, UTF-8, and RFC3339 timestamps. Unknown response fields
must be ignored. The API represents the operator; device credentials grant full
operator access, not an agent identity. Never use AMP `/v1` or `/api/hook` remotely.

## Connect and authenticate

`GET /api/version` is public: `{"version":"0.1.0","api":1,"remote":true}`.
`GET /healthz` is public: `{"ok":true,"version":"0.1.0"}`.

Create a code on the trusted dashboard's Devices page or with `lolad pair`.
Codes have eight characters, expire after ten minutes and can be redeemed once.
The QR contains `lolad://pair?u=<percent-encoded HTTPS base URL>&c=<code>`.

`POST /api/pair` with `Content-Type: application/json`:

```json
{"code":"ABCD2345","device":"Pixel 8","kind":"android"}
```

Successful response (the token below is a placeholder):

```json
{"token":"lol_<random token shown only here>","device_id":"<device id>","me":{"name":"milo","display":"Camilo Caceres"}}
```

Kinds: `android`, `ios`, `macos`, `browser`, `cli`. Store the token in Android's
encrypted local storage or the Apple Keychain.
All subsequent requests, **including SSE**, send
`Authorization: Bearer lol_<token>`. Tokens are never returned by device listing.
A revoked token immediately returns 401; remove it locally and offer pairing.
Invalid/expired/used codes return 401; repeated authentication failures return
429 with `Retry-After`. Auth errors have `{"error":"authentication required"}`
or another short error string. Do not automatically retry 401 or 429 in a loop.
A temporarily unavailable credential store returns 503 with
`{"error":"authentication unavailable"}`; retry it with backoff.
No token belongs in a URL, log, screenshot, notification or crash report.

Browsers can instead POST a pairing code or device token to `/login` and receive
an HttpOnly, Secure, SameSite=Lax session cookie with a 30 day sliding lifetime.
`POST /logout` clears the current session. Revocation invalidates all sessions
for that device. An explicitly allowed Tailscale login can also enter through
Tailscale Serve; arbitrary shared-in devices cannot.

## Read the operator and agents

- `GET /api/me`: `{"name":"milo","display":"Camilo Caceres"}` (optional `ntfy`).
- `GET /api/agents`: array of agent statuses; an empty store may return `null`.
- `GET /api/agents/{name}`: one status, with name/display/role/dir/runtime,
  address, state, desired, restarts, unread, and optional pid, team, is_head,
  started_at, session_since, last_activity, detail, last_error, pending_restart,
  pending_reason. States: stopped, starting, idle, busy, waiting, crashed,
  failed, unknown. Treat future states as unknown.
- `GET /api/agents/{name}/capture?lines=100`: `{"capture":"terminal text"}`.
  This is a diagnostic snapshot, not the conversation.
- `POST /api/agents/{name}/{start|stop|restart|interrupt}`: `{"ok":true}`;
  restart accepts `?fresh=true`. Clients should confirm fresh restart.

## Chat

`GET /api/mail/conversations` returns an array of
`{"peer":"iris","display":"Iris","last":{...mail},"unread":1,"pending":true,"last_at":"2026-10-26T12:00:00Z"}`.
`pending` describes the last message only; find unanswered asks in the thread as
the dashboard does (later operator mail answers earlier asks).

`GET /api/mail/conversation/{peer}?limit=100&before=<RFC3339Nano>` returns mail
oldest first within the selected page. Default limit 100, maximum 1000. Fetch
older pages using the oldest returned `created_at`, merge by id. Opening a thread
does not mark it read; POST each received unread message to `/api/mail/{id}/read`.
The mark-read response is `{"ok":true}`. Mail shape:

```json
{"id":"msg_1793016000_example","thread_id":"msg_1793016000_example","from":"iris@personal.lolad.local","to":"milo@personal.lolad.local","from_name":"iris","to_name":"milo","subject":"Deploy?","priority":"normal","type":"request","body":"Deploy this version?","context":{"userchat":{"kind":"ask","options":["Deploy","Wait"]}},"signed":true,"verified":true,"created_at":"2026-10-26T12:00:00Z","status":"unread","attempts":0}
```

Optional fields: in_reply_to, notified_at, read_at. Status: unread, notified,
read. Render body as plain text or sanitized Markdown, never raw HTML. Userchat
kinds received from agents: say, update, deliver, ask; options exist for asks.
Some system/legacy mail has no userchat context. Fall back to subject/body.

`POST /api/mail/send` sends as the operator and returns the complete created Mail:

```json
{"to":"iris","subject":"Today's plan","body":"Please prepare a plan.","type":"request","context":{"userchat":{"kind":"say"}}}
```

To reply to a message, set `in_reply_to`, type `response`, and userchat kind
`reply`. To answer an ask, match `internal/web/inbox.go` exactly: choose one of
its options, set that option as body and decision, and use the ask id:

```json
{"to":"iris","subject":"Re: Deploy?","body":"Deploy","type":"response","in_reply_to":"msg_1793016000_example","context":{"userchat":{"kind":"decision","decision":"Deploy"}}}
```

Send requests are not idempotent; do not blindly replay after a network timeout.
Mail is authoritative; refresh the thread to check whether it was sent.

## Files (attachments)

Files use Bearer authentication like every other `/api` call. Upload each file
on its own request, then send the returned IDs with the message. Only local
agents accept files: sending to a remote (`name@other.lolad`) conversation
fails, so hide or disable "attach" there.

`POST /api/mail/attachments?peer=<agent>` with `multipart/form-data` and exactly
one file part named `file` (send the file's bytes as the part body, `Content-Type`
optional; the provider sniffs and may mark it `suspicious`):

```text
POST /api/mail/attachments?peer=iris
Content-Type: multipart/form-data; boundary=...

--...
Content-Disposition: form-data; name="file"; filename="Quarterly report.pdf"
Content-Type: application/pdf

<bytes>
--...--
```

`201 Created` with the `core.Attachment` that the message will carry:

```json
{"storage":"provider","id":"att_1793179924_5f2c1d0e8a7b4c3d2e1f0a9b","filename":"Quarterly report.pdf","content_type":"application/pdf","size":184320,"digest":"sha256:9d1c…64 hex…","url":"/v1/attachments/att_1793179924_5f2c1d0e8a7b4c3d2e1f0a9b/download","scan_status":"basic_clean","uploaded_at":"2026-10-28T09:12:04.512345Z","expires_at":"2026-11-27T09:12:04.512345Z"}
```

Limits and errors: 500 MB per file (413 when larger, 422 when empty or when the
body is not one `file` part), 10 files per message, and unsent uploads are
deleted two hours after upload (`expires_at` is the stored file's 30 day life
once sent). An unknown `peer` is 404, a remote peer is 422, and org policy
refusal is 403. `url` is the daemon's AMP path for agents, not for clients.
Uploads are streamed to disk; clients should stream the request body too.

`GET /api/mail/attachments/{id}?peer=<agent>` streams the file. It is readable
only from the conversation that carries it, or (before sending) from the
conversation it was uploaded in; anything else is `404`. Responses carry
`Content-Disposition: attachment; filename="ascii…"; filename*=UTF-8''…`,
`X-Content-Type-Options: nosniff`, an `ETag` equal to the quoted digest, and
support `Range` (`206 Partial Content`) and `HEAD`. Add `&inline=1` to preview
in place: that is honoured only for `image/png`, `image/jpeg`, `image/gif` and
`image/webp` whose `scan_status` is `basic_clean`; everything else — SVG,
PDF, text, a `suspicious` image — is served as an `application/octet-stream`
attachment even when `inline=1` is asked for. Treat `inline=1` as a hint: decide
what to render from the response `Content-Type`, and never execute scripts from
a downloaded file. A missing `peer` is 422.

`POST /api/mail/send` accepts optional `attachments` — IDs from uploads by this
token's operator, at most 10, each used in one message only:

```json
{"to":"iris","subject":"Quarterly report","body":"","type":"request","attachments":["att_1793179924_5f2c1d0e8a7b4c3d2e1f0a9b"]}
```

`body` may be empty when `attachments` is present; `subject` is still required
(1–256 characters), so derive one from the file names when the user typed no
text. Unknown, already-sent or foreign IDs are 422 (`attachment … not found`),
more than 10 is 422. The returned Mail carries the files at the top level of
the mail object, field `attachments` — the same array in signed order the
recipient sees:

```json
{"id":"msg_1793179925_1a2b3c4d5e6f7a8b9c0d1e2f","thread_id":"msg_1793179925_1a2b3c4d5e6f7a8b9c0d1e2f","from":"milo@personal.lolad.local","to":"iris@personal.lolad.local","from_name":"milo","to_name":"iris","subject":"Quarterly report","priority":"normal","type":"request","body":"","context":{"userchat":{"kind":"say"}},"signed":true,"verified":true,"created_at":"2026-10-28T09:12:04.512345Z","status":"unread","attempts":0,"attachments":[{"storage":"provider","id":"att_1793179924_5f2c1d0e8a7b4c3d2e1f0a9b","filename":"Quarterly report.pdf","content_type":"application/pdf","size":184320,"digest":"sha256:9d1c…64 hex…","url":"/v1/attachments/att_1793179924_5f2c1d0e8a7b4c3d2e1f0a9b/download","scan_status":"basic_clean","uploaded_at":"2026-10-28T09:12:04.512345Z","expires_at":"2026-11-27T09:12:04.512345Z"}]}
```

So an inbox row or thread message shows files from `mail.attachments` (field
absent when there are none), and downloads each one with
`GET /api/mail/attachments/<attachment.id>?peer=<mail.from_name or
mail.to_name>` — the conversation peer, never the local `url`. `scan_status` is
`basic_clean` or `suspicious` (magic bytes contradict the declared type); show
`suspicious` as such and still allow an explicit download.

## Live updates

`GET /api/stream` with the Bearer header returns `text/event-stream`. Event name
is the core event's `kind`: started, exited, crashed, restarted, state, rotated,
mail, notified, trigger, created, policy, error (and future kinds). Each data
payload is a core Event:

```text
event: mail
data: {"id":42,"agent":"iris","at":"2026-10-26T12:00:00Z","kind":"mail","detail":"msg_1793016000_example"}

```

A comment keepalive arrives every 20 seconds. There is no replay cursor or
Last-Event-ID guarantee: reconnect with backoff and refresh conversations and
agents on every reconnect. On mail/notified refresh conversations and the open
thread; on other events refresh the affected agent. Use authenticated streaming
HTTP on Android (browser EventSource cannot supply a bearer header). The web
page's `/stream` is a separate presentation stream with `event: lolad` and an
extra `class` field; clients must use `/api/stream`.

## Errors and downloads

Service errors: `{"error":"Forbidden","message":"...reason..."}` with 403 for
policy denial, 404 missing resource, 409 conflict, 422 invalid input/not running,
500 unexpected failure. JSON bodies are limited to 1 MiB. Handle non-JSON proxy
errors too. An unauthenticated API returns 401 JSON; browser navigations redirect
to `/login?next=...`.

`GET /download/{file}` is public and serves a named attachment from the daemon's
downloads directory. No listing, subdirectories or traversal. Use this for APK
installation; do not store confidential files there.

The illustrative examples above follow the concrete JSON tags and handlers in
internal/core and internal/api. Actual throwaway-daemon responses follow.

## Validated throwaway-daemon examples

Captured on 2026-10-26 from ports 4190/4191 (build `v3-remote-test`), with a
synthetic stopped agent and synthetic mail; no production data was accessed.
The directory is replaced below; all other fields are actual response values.

### GET /api/me

```json
{
  "name": "milo",
  "display": "Remote Test Operator"
}
```

### GET /api/agents/demo

```json
{
  "name": "demo",
  "display": "Demo",
  "role": "Throwaway validation agent",
  "dir": "<throwaway agent directory>",
  "runtime": {
    "harness": "claude",
    "permissions": "bypass",
    "autostart": false
  },
  "address": "demo@remote-test.lolad.local",
  "state": "stopped",
  "desired": "up",
  "restarts": 0,
  "unread": 1
}
```

### GET /api/mail/conversation/demo

```json
[
  {
    "id": "msg_1792995063_69466f334bf6ce0c02e3f28b",
    "thread_id": "msg_1792995063_69466f334bf6ce0c02e3f28b",
    "from": "demo@remote-test.lolad.local",
    "to": "milo@remote-test.lolad.local",
    "from_name": "demo",
    "to_name": "milo",
    "subject": "Deploy this version?",
    "priority": "normal",
    "type": "request",
    "body": "Deploy this version?",
    "context": {
      "userchat": {
        "kind": "ask",
        "options": [
          "Deploy",
          "Wait"
        ]
      }
    },
    "signed": true,
    "verified": true,
    "created_at": "2026-10-26T06:11:03.714361Z",
    "status": "unread",
    "attempts": 0
  },
  {
    "id": "msg_1792995063_0741f2d2cf8bb9f58f6248cd",
    "thread_id": "msg_1792995063_69466f334bf6ce0c02e3f28b",
    "in_reply_to": "msg_1792995063_69466f334bf6ce0c02e3f28b",
    "from": "milo@remote-test.lolad.local",
    "to": "demo@remote-test.lolad.local",
    "from_name": "milo",
    "to_name": "demo",
    "subject": "Re: Deploy this version?",
    "priority": "normal",
    "type": "response",
    "body": "Deploy",
    "context": {
      "userchat": {
        "decision": "Deploy",
        "kind": "decision"
      }
    },
    "signed": true,
    "verified": true,
    "created_at": "2026-10-26T06:11:03.847947Z",
    "status": "unread",
    "attempts": 0
  }
]
```

The ask above was sent with `lolad msg tell-user ask`; the decision was sent
through POST `/api/mail/send` using the exact context shown in the contract.
Remote pairing returned the documented token/device_id/me object, authenticated
GET `/api/agents` returned 200, and the same token returned 401 after revocation.
