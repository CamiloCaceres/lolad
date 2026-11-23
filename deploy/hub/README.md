# Deploying lolad-hub

The hub is one static binary with an SQLite file behind a TLS proxy. See
SPEC.md "Hub (v5)". Two ways to run it: **Coolify** (Docker, below) or a plain
**systemd + Caddy** install (after that).

## What the operator (Heimdall) needs

| | |
|---|---|
| Build | Dockerfile `deploy/hub/Dockerfile`, build context = repo root |
| Port | `8095` inside the container, reached only by the platform's TLS proxy (no host port) |
| Volume | persistent, mounted at `/var/lib/lolad-hub` (holds `hub.db`: accounts and invites) |
| Health check | `GET /v1/hub/info` on port 8095 → 200 JSON (also built into the image) |
| Public URL | `LOLAD_HUB_PUBLIC_URL=https://<hub domain>` (production: `https://lolad-hub.caceresdev.com`), exactly the domain the proxy serves; it is baked into every member URL and into tunnel signatures, so changing it later breaks existing members |
| WebSockets | must pass through the proxy (`/v1/hub/tunnel`); Traefik and Caddy do by default |

Environment variables (names only; the image sets the defaults shown):

| Name | Required | Secret | Meaning |
|---|---|---|---|
| `LOLAD_HUB_PUBLIC_URL` | yes | no | `https://<hub domain>` |
| `LOLAD_HUB_ADMIN_TOKEN` | no | **yes** | Turns on the console (`/console`) and admin API (`/v1/hub/admin/`). At least 24 characters, e.g. `openssl rand -base64 32`. Unset: both are off (404) |
| `LOLAD_HUB_LISTEN` | no | no | default `0.0.0.0:8095` in the image (`127.0.0.1:8095` outside it) |
| `LOLAD_HUB_DB` | no | no | default `/var/lib/lolad-hub/hub.db` |
| `LOLAD_HUB_TRUSTED_PROXIES` | no | no | CIDRs whose `X-Forwarded-For` is believed; the image trusts the private ranges |

## Behind Traefik (Coolify): client IPs and X-Forwarded-For

The production hub is `https://lolad-hub.caceresdev.com` (console at
`/console`): Coolify builds `deploy/hub/Dockerfile` from GitHub, and Coolify's
Traefik ends TLS (Let's Encrypt) and proxies to the container on 8095 over
Coolify's private Docker network. Caddy is not involved.

Rate limits (sign-ups, admin sign-in, per-IP forwarding) need the caller's real
IP. The hub works it out like this:

1. If the TCP peer is loopback or inside `LOLAD_HUB_TRUSTED_PROXIES`, it takes
   the **rightmost** `X-Forwarded-For` entry, which is the one the proxy
   appended (a caller can forge entries to its left, never that one).
2. Otherwise it uses the TCP peer address and ignores `X-Forwarded-For`.

The image sets `LOLAD_HUB_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7`,
which covers the Docker networks Traefik connects from, so nothing needs
setting in Coolify. Two conditions keep this correct:

- **Only Traefik can reach port 8095.** Do not map it to a host port.
  Anything else on the private network could otherwise claim any IP.
- **Traefik must be the only proxy.** Leave the DNS record for
  `lolad-hub.caceresdev.com` unproxied (grey cloud in Cloudflare). With a
  second proxy in front, the rightmost entry is that proxy's address, and all
  callers would share one rate-limit bucket. Traefik needs no
  `forwardedHeaders` setting: by default it does not trust incoming
  `X-Forwarded-For` from the internet and appends the real client IP.

To narrow trust to Coolify's network only, set `LOLAD_HUB_TRUSTED_PROXIES` to
that network's subnet, from
`docker network inspect coolify --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}'`. Check it works: after a request, the console's Health
page and `/v1/hub/admin/logs` should count limits per real caller, and the
hub's JSON log `admin sign-in refused` lines show the caller's IP, not a
`10.x`/`172.x` address.

Tunnels are WebSockets that live for hours. The hub pings every 30 s, which
keeps them open through Traefik's idle timeouts with no extra Traefik
configuration.

## Console

With `LOLAD_HUB_ADMIN_TOKEN` set, open `https://<hub domain>/console` and sign
in with that secret. Three pages:

- **People:** who has joined, who is connected now, messages carried today,
  and a control to turn someone off (closes their tunnel at once) or back on.
- **Invites:** make a one-time code (shown once, on that page only), see codes
  waiting to be used and cancel them, and see who joined with which invite.
- **Health:** uptime, connections, messages per hour for the last 24 hours,
  rate-limited requests, and the recent request log.

The console knows people, invites and traffic metadata only: handle, path,
result and time taken. No mail, bodies, query strings or invite codes. The
request log and hourly counts are kept in memory and reset on restart.

Sign-in: a wrong secret is refused 5 times per IP per 15 minutes (100 in total
across all IPs), then 429. A session lasts 12 hours (cookie
`__Host-lolad-hub-console`, HttpOnly, Secure, SameSite=Strict, kept in memory,
so a redeploy signs you out). Forms carry a CSRF token and cross-site posts are
refused. The cookie is Secure, so use the HTTPS URL; for a local test, Chrome
and Firefox accept it on `http://127.0.0.1`.

Admin API (JSON, `Authorization: Bearer $LOLAD_HUB_ADMIN_TOKEN`; a console
session also works, with `X-CSRF-Token` on writes):

| | |
|---|---|
| `GET /v1/hub/admin/status` | version, uptime, accounts, online, 24 h traffic |
| `GET /v1/hub/admin/accounts` | accounts with `online`, `note`, `carried_today` |
| `POST /v1/hub/admin/accounts/{handle}/disable` / `enable` | turn off / on |
| `GET /v1/hub/admin/invites` | `{pending, used}`, never codes |
| `POST /v1/hub/admin/invites` `{note, ttl?}` | 201 with `code`, the only time it is returned |
| `DELETE /v1/hub/admin/invites/{id}` | cancel an unused invite |
| `GET /v1/hub/admin/logs?limit=100` | recent requests (handle, path, status, ms) |

```sh
curl -H "Authorization: Bearer $LOLAD_HUB_ADMIN_TOKEN" https://<hub domain>/v1/hub/admin/status
```

## Coolify (Docker)

`deploy/hub/Dockerfile` builds a ~32 MB Alpine image that runs as an
unprivileged user. Coolify's Traefik serves TLS and passes WebSockets through
with no extra config.

New resource → the lolad Git repo → Build Pack **Dockerfile**:

| Setting | Value |
|---|---|
| Base Directory | `/` |
| Dockerfile Location | `/deploy/hub/Dockerfile` |
| Ports Exposes | `8095` (do not map it to a host port) |
| Domain | `https://<hub domain>` |
| Persistent storage | volume mounted at `/var/lib/lolad-hub` |
| Env `LOLAD_HUB_PUBLIC_URL` | `https://<hub domain>` (same as Domain) |
| Env `LOLAD_HUB_ADMIN_TOKEN` | a random secret, 24+ characters (mark it as a secret); optional, turns on `/console` |
| Health check | `GET /v1/hub/info` on 8095 (also built into the image) |

The image already sets `LOLAD_HUB_LISTEN=0.0.0.0:8095`,
`LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db` and
`LOLAD_HUB_TRUSTED_PROXIES` (the private ranges Traefik connects from), so rate
limits see each caller's real IP. If you put Cloudflare's proxy in front,
client IPs become Cloudflare's; leave the DNS record unproxied.

The CLI still works too. Admin commands run inside the container (Coolify's Terminal tab, or
`docker exec` on the VPS):

```sh
lolad-hub invite --note "Jugui"
lolad-hub accounts
lolad-hub disable <handle>
```

Redeploying keeps accounts (they live in the volume). Tunnels reconnect within
seconds, and mail sent meanwhile waits in each sender's outbox.

Build and try it locally:

```sh
docker build -f deploy/hub/Dockerfile -t lolad-hub .
docker run --rm -p 127.0.0.1:8095:8095 -e LOLAD_HUB_PUBLIC_URL=http://127.0.0.1:8095 \
  -e LOLAD_HUB_ADMIN_TOKEN="$(openssl rand -base64 32)" lolad-hub
```

Or without Docker, with a throwaway data dir:

```sh
export LOLAD_HUB_ADMIN_TOKEN="$(openssl rand -base64 32)"; echo "$LOLAD_HUB_ADMIN_TOKEN"
go run ./cmd/lolad-hub serve --db "$(mktemp -d)/hub.db" --public-url http://127.0.0.1:8095
# open http://127.0.0.1:8095/console in Chrome or Firefox and sign in with the secret
```

## systemd + Caddy

### Build

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/lolad-hub-linux-amd64 ./cmd/lolad-hub
```

Use `GOARCH=arm64` for an ARM VPS.

### Install (once)

```sh
scp dist/lolad-hub-linux-amd64 vps:/tmp/lolad-hub
ssh vps 'sudo install -m 0755 /tmp/lolad-hub /usr/local/bin/lolad-hub &&
  sudo useradd --system --no-create-home --shell /usr/sbin/nologin lolad-hub || true'
scp deploy/hub/lolad-hub.service vps:/tmp/
ssh vps 'sudo install -m 0644 /tmp/lolad-hub.service /etc/systemd/system/ &&
  sudo systemctl daemon-reload && sudo systemctl enable --now lolad-hub'
```

1. Edit `LOLAD_HUB_PUBLIC_URL` in the unit to the hub's domain.
2. Add the `deploy/hub/Caddyfile` block to the VPS Caddyfile, with that same domain.
3. Reload Caddy.

Check that it works: `curl https://<domain>/v1/hub/info`.

### Update

Copy the new binary to `/usr/local/bin/lolad-hub`, then run `sudo systemctl
restart lolad-hub`. Tunnels reconnect by themselves within about a minute. While
the hub is down, mail waits in each sender's outbox.

## Admin

Under systemd, run these on the VPS as the service user, so the database stays owned by it:

```sh
sudo -u lolad-hub LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db lolad-hub invite --note "Jugui"
sudo -u lolad-hub LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db lolad-hub accounts
sudo -u lolad-hub LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db lolad-hub disable <handle>
```

`invite` prints a single-use `hubinv_…` code. Send it privately. The other
person joins with:

```sh
lolad hub join https://<domain> --handle <name>
```

That command reads the code from stdin. They can also join from the Hub card on
the dashboard's Connections page.

## What the hub sees

- Federation requests in transit: TLS ends at the hub, so it can read them. It
  cannot forge them, because every request is signed by the sending lolad and
  checked by the receiving one.
- Account handles and public keys.
- Logs: handle, path, status and duration. Never bodies, query strings,
  codes or the admin secret.

It stores no mail. End-to-end encryption is on the roadmap.
