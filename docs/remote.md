# Remote access over Tailscale

lolad keeps its trusted agent/CLI listener on loopback. A **separate** loopback
listener authenticates operator devices, and Tailscale Serve provides HTTPS.
Keep the trusted listener off the network and never point Serve at it.

```yaml
listen: 127.0.0.1:4100
remote:
  listen: 127.0.0.1:4101
  public_url: https://my-mac.example-tailnet.ts.net:4100
  tailscale_logins:
    - operator@example.com
```

An empty or omitted `remote.listen` disables remote access. `public_url` must
be the exact HTTPS origin used by browsers, including its port; it supplies
pairing links/QRs and the explicit trusted origin for HTTP and terminal sockets.
`tailscale_logins` is optional: an empty list requires a device token or a paired
browser cookie even for the owner. Restart lolad after configuration changes.

On the Mac, after restarting lolad, run exactly:

```sh
tailscale serve --bg --https=4100 http://127.0.0.1:4101
tailscale serve status --json
lolad doctor
```

Do not serve `http://127.0.0.1:4100`: that listener intentionally trusts local
agents and exposes AMP and harness hooks. Do not bind either listener to a LAN,
Tailscale or wildcard address. The remote backend must be a numeric loopback address; the trusted local
listener also accepts `localhost` for compatibility.
Tailscale Serve strips client-supplied Tailscale identity headers and supplies
`Tailscale-User-Login`. lolad honors that header **only** on the remote loopback
listener and only for a login explicitly listed in the config.

## Pair a PC browser

Open **Devices** from the dashboard's operator area on a trusted local browser
and choose **Generate pairing code**, or run `lolad pair --name "Work PC"`.
Open the configured HTTPS URL on the other PC and enter the code at **Sign in**.
The eight-character code expires in ten minutes and works once. The browser
receives a Secure, HttpOnly, SameSite=Lax cookie that expires 30 days after its
last authenticated request. The code is never a permanent password.

An allowed Tailscale login enters directly and gets its own browser session.
It still appears as a device. If a revoked or expired cookie is present, sign
out or use a new pairing code to sign back in.

## Pair an Android phone

Install the Android APK the operator placed at `/download/lolad.apk` (or the
published filename). Open the Android app's pairing scanner and scan the QR on
**Devices**, or enter the HTTPS URL and code manually. The QR encodes
`lolad://pair?u=<URL>&c=<CODE>` and is generated locally; no QR service sees it.
The app exchanges the code at `/api/pair` for a permanent device token and stores
it privately. For API details see [client-api.md](client-api.md).

`lolad pair` also prints the phone pairing URI. It never prints an existing
device token. A newly exchanged token is shown only once in the pairing response.
A code is a short-lived secret: avoid sharing screenshots of a live code or QR.

## Revoke and sign out

**Devices → Revoke** removes a token and all its browser sessions, and closes
active authenticated streams/sockets. CLI equivalents:

```sh
lolad devices ls
lolad devices rm dev_ID
```

Sign out removes the current browser session. An allowlisted Tailscale identity
can authenticate again even after its browser device is revoked: remove its
login from `remote.tailscale_logins` and restart to withdraw that access.
Device tokens have full operator authority, including device management. There
are no per-device scopes. Name devices clearly and revoke ones you lose.

## Threat model

The tailnet can include shared-in devices belonging to family and friends.
Network membership alone is **not authentication**. Such a device can reach
Tailscale Serve but needs an operator-issued code/token or an explicitly allowed
Tailscale login. Remote `/v1` and `/api/hook` are never available. `/hook/{agent}/{name}`
is available only with its separate agent webhook token; a device token is not
a substitute for that token.

The trust boundary includes the Mac's local processes, filesystem permissions,
Tailscale daemon and Serve. A local process can reach the intentionally trusted
local listener and can impersonate the Serve backend; this design does not
isolate hostile programs running under the operator's account. Use OS account
isolation and tailnet ACLs as defense in depth. Never expose the remote backend
with a different proxy that forwards arbitrary Tailscale identity headers.

Public routes are `/login`, `/static/*`, `/healthz`, `/api/version`, POST
`/api/pair`, `/download/*`, and token-authenticated webhooks. Health returns only
ok and build version; version returns build version, API level and listener kind.
Downloads have no authentication, listing, traversal or escaping symlinks:
**only public release files belong in `<state>/downloads`**. Authentication
failures are limited per connection IP and supplied login. Because Serve is a
loopback proxy, IP limits may apply collectively to remote clients; login limits
provide a second bucket. Forwarded IP headers are not trusted.

Cross-origin protection covers both listeners. The remote HTTPS origin is
explicitly trusted; arbitrary websites cannot submit cookie-authenticated writes.
The browser login and device pages have no third-party assets. Device token,
session and pairing-code hashes are stored in the existing SQLite database;
plaintext tokens are not persisted by lolad. Request logs omit bodies, cookies,
authorization and query strings. Runtime environment values remain write-only.

## Operations and deploy

Build with a version stamp:

```sh
go build -ldflags "-X main.version=$(git rev-parse --short HEAD)" -o /path/to/lolad .
```

The stamp appears in `lolad version`, `/api/version`, `/healthz` and the navigation
foot. Restart the daemon with SIGTERM and the normal supervisor/process manager;
lolad drains HTTP and leaves existing tmux agent sessions running for adoption.
Long-lived streams may use the ten-second shutdown grace; they are then closed.

SQLite gets a consistent `VACUUM INTO` backup on startup if needed and daily
while running, under `<state>/backups`, retaining the newest seven snapshots.
Backup files and their directory are private. A snapshot includes device hashes
and session hashes and must be protected like the main state. This does not back
up org/config YAML or agent directories. `lolad doctor` checks directories,
harness binaries, the tmux socket, both ports, health, read-only Serve status and
backup freshness, and prints fixes. An empty tmux server is normal with no agents.

## Validation evidence (v3 remote worker)

Validated with a throwaway config/state, `lolad-v3-remote` tmux socket, and
listeners 4190/4191; no production daemon, agent directories, Tailscale Serve
configuration or PM2 configuration were used.

- CLI pairing → POST `/api/pair` → Bearer GET `/api/agents` returned 200;
  revoking that device changed the same request to 401.
- An actual Chromium browser logged in with a one-use code, opened Devices,
  generated an eight-character code/QR/countdown, and received 401 on its API
  request after its browser device was revoked.
- Login and Devices screenshots at 1440 and 390 in light/dark are under
  `internal/web/testdata/screenshots/v3-{login,devices}-*.png`; live pairing codes
  and tokens are absent from the screenshots. Devices axe audit: zero desktop
  and phone violations; phone had one inconclusive contrast check for the shared
  navigation's partially clipped New agent label.
- Auth tests cover expiry, atomic single use, token hashing, sliding cookies,
  revocation/cancellation, bearer/cookie/Tailscale/no-auth paths, nonloopback
  header rejection, local header ignoring and failure limits. Integration tests
  cover remote AMP/harness-hook exclusion, exact HTTPS origin acceptance and
  rejection, terminal websocket origin/revocation, SSE cancellation, download
  traversal/escaping-symlink rejection, request log redaction and panic recovery.
- Backup tests validate snapshot contents and retention of seven copies.
  Vendored QR byte/alphanumeric/Reed-Solomon vectors run without external tools.
  `LOLAD_QR_VISION_TEST=1 go test ./internal/auth -run TestQRRoundTrip` additionally
  decodes the pairing URI with Apple's independent CPU-only Vision decoder.
- SIGTERM closed an active authenticated API SSE stream, exited with status 0,
  and left an inert session on the throwaway tmux server alive. That test session
  and the browser were removed after verification.

The QR encoder is vendored with its MIT license from the pre-existing module
cache (`github.com/skip2/go-qrcode`, commit `da1b6568686e`). No dependency install,
network QR service, or go.mod/go.sum change is needed.

Final checkout validation passed: `go build ./...`, `go vet ./...`, and
`go test ./...`; the optional CPU-only Vision QR round-trip also passed.
Earlier shared-checkout Traffic/Email/Create fixture failures were resolved by
those workers before this final run.

A separate runtime security finding was escalated to the coordinator: existing
tmux launches put inherited environment variables in process arguments, and a
process inspection unexpectedly exposed such values in tool output. Do not copy
that output; review credential rotation and have the runtime owner remove this
argv exposure. This finding is outside the remote worker's runtime ownership.
