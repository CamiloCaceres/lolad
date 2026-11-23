# lolad for Android (WebView shell)

A thin Android shell around the lolad dashboard, the same page the browser and
the desktop app show. Application ID `dev.caceres.lolad.web`, so it installs
next to the native client (`../android`).

It adds only: a server setting, file upload and download, and navigation limits
(the WebView stays on your server's HTTPS origin; other links open in the
browser). No JavaScript bridge, no analytics, no background notifications.

## Use

1. Remote access must be set up first: [docs/remote.md](../../docs/remote.md)
   (`remote.public_url`, `tailscale serve`).
2. `./scripts/build-apk.sh`, then `adb install -r dist/lolad-web.apk`.
3. Turn on Tailscale, open the app, enter `my-mac.my-tailnet.ts.net:4100`.
4. On the Mac dashboard open Devices, create a pairing code, and type it on the
   app's Sign in page. The session cookie lasts 30 days.

Long-press the app icon, then Change server, to point it elsewhere.

## Not done

Background notifications (the page only updates while the app is open), pairing
by QR scan, offline cache, blob: downloads. Needs device verification.
