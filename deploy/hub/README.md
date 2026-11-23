# Deploying lolad-hub

The hub is one static binary with an SQLite file behind a TLS proxy. See
SPEC.md "Hub (v5)". Two ways to run it: **Coolify** (Docker, below) or a plain
**systemd + Caddy** install (after that).

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
| Health check | `GET /v1/hub/info` on 8095 (also built into the image) |

The image already sets `LOLAD_HUB_LISTEN=0.0.0.0:8095`,
`LOLAD_HUB_DB=/var/lib/lolad-hub/hub.db` and
`LOLAD_HUB_TRUSTED_PROXIES` (the private ranges Traefik connects from), so rate
limits see each caller's real IP. If you put Cloudflare's proxy in front,
client IPs become Cloudflare's; leave the DNS record unproxied.

Admin commands run inside the container (Coolify's Terminal tab, or
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
docker run --rm -p 127.0.0.1:8095:8095 -e LOLAD_HUB_PUBLIC_URL=http://127.0.0.1:8095 lolad-hub
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
- Logs: handle, path, status and duration. Never bodies or codes.

It stores no mail. End-to-end encryption is on the roadmap.
