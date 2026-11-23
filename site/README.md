# lolad landing page

The public page people land on to download lolad. Plain static HTML and CSS:
no build step, no JavaScript, no analytics, no third-party requests.

Design source: `lolad-manager/designs/landing-hub/project/Main.dc.html`
(translated by hand; the dc runtime is not shipped).

## Files

| File | What it is |
|---|---|
| `index.html` | The page |
| `styles.css` | All styles, including the orb component shared with the dashboard |
| `logo.svg`, `favicon.svg` | Copied from `internal/web/static/` |
| `fonts/` | Sora and Manrope (OFL), copied from `internal/web/static/vendor/fonts/` |
| `join/index.html`, `join/join.js` | The page a friend link opens (`/join#<invite>`), see below |
| `og.png` | 1200×630 social preview (a screenshot of the hero) |
| `Dockerfile`, `Caddyfile` | Container for Coolify: Caddy serves the folder on port 80 |

## Run locally

```sh
python3 -m http.server -d site 8088   # from the repo root
open http://localhost:8088/
```

## Container

```sh
docker build -t lolad-site site
docker run --rm -p 8088:80 lolad-site
```

Caddy listens on plain HTTP :80; TLS is expected at the proxy (Coolify/Traefik).
The Caddyfile sets a strict CSP (`script-src 'none'`), so adding a script later
means loosening it.

## Domain

The site is `https://lolad.caceresdev.com`; the hub is `https://lolad-hub.caceresdev.com`.
The site domain lives only in the `<head>` of `index.html` (canonical, og:url, og:image,
twitter:image), under the `SITE DOMAIN` comment. The hub address appears in the hub
diagram and the footer "Hub status" link. To move domains later:

```sh
sed -i '' 's#lolad\.caceresdev\.com#NEW-SITE-DOMAIN#g; s#lolad-hub\.caceresdev\.com#NEW-HUB-DOMAIN#g' site/index.html
```

(macOS sed; drop `''` on Linux.)

## Links

- Downloads go straight to the release assets: `releases/latest/download/Lolad.dmg` and
  `releases/latest/download/Lolad-Setup.exe`. An "All downloads" link goes to `releases/latest`.
  These asset names must stay stable across releases.
  The header and hero "Download" buttons scroll to that section first,
  so people pick their platform and see the first-open note before downloading.
- Source: `https://github.com/CamiloCaceres/lolad`
- Install guides: `docs/install.md#mac` and `docs/install.md#windows` on GitHub

## Notes

- The buttons inside the example cards ("Send to Ana", "Archive them", …) are pictures of
  the app, not controls: they are rendered as non-focusable spans.
- `prefers-reduced-motion: reduce` stops every animation.
- To regenerate `og.png`, screenshot the page at a 1200×630 viewport.

## /join

Friend links look like `https://lolad.caceresdev.com/join#2.<invite>`. The page
greets the visitor ("Ana invited you to Lolad"), lists what Ana shares, offers
the download for their computer (both on other systems; on a phone it says to
open the link on a computer), three install steps, and a button to copy the
link to paste into Lolad.

The invite is in the `#fragment`, which browsers never send to a server.
`join.js` decodes it in the browser only, writes it with `textContent`, and
makes no requests. Caddy serves `/join` with `Referrer-Policy: no-referrer`,
`Cache-Control: no-store` and a CSP allowing only this site's script with
`connect-src 'none'`; every other page keeps `script-src 'none'`.
