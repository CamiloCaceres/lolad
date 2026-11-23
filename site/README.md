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

- Download: `https://github.com/CamiloCaceres/lolad/releases/latest` (the "Download Lolad.dmg" button).
  The header and hero "Download for Mac" buttons scroll to that section first, as in the design,
  so people see the macOS requirement and Gatekeeper note before downloading.
- Source: `https://github.com/CamiloCaceres/lolad`
- Install guide: `docs/install.md` on GitHub

## Notes

- The buttons inside the example cards ("Send to Ana", "Archive them", …) are pictures of
  the app, not controls: they are rendered as non-focusable spans.
- `prefers-reduced-motion: reduce` stops every animation.
- To regenerate `og.png`, screenshot the page at a 1200×630 viewport.
