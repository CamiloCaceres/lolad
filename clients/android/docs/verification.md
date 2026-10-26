# Android verification — 2026-10-26

Delivered under `clients/android` only. Contract re-read from `docs/client-api.md` after remote-worker integration. No production daemon, real Tailscale URL, AI Maestro, global agent settings, or existing agent directories were contacted or changed.

## Automated checks

- `clients/android/scripts/build-apk.sh`: passed `testDebugUnitTest`, `lintRelease`, `assembleRelease`, zip alignment, signing and signature verification.
- 10 unit tests passed: URL validation, pairing links/codes, message kind fallback, exact decision payload, reply/new mail payload, SSE comments/multiline/reset, ANSI stripping, token storage abstraction, JSON/SSE Bearer headers, 401 and Retry-After errors.
- Release lint: zero errors; warnings retain existing pinned dependency versions, English strings, synchronous credential removal, and the older-Android backup declaration (backup is disabled; Android 12+ transfers are explicitly excluded).
- Signing: APK Signature Scheme v2 and v3 verified; new private lolad-only PKCS12 key outside the repository. Signing directory `0700`, key/password `0600`. No key, password, token or pairing code appears in repository artifacts.
- Repository `go build ./...`, `go vet ./...`, and `go test ./...`: all passed during this run. No Go dependencies added.

## Emulator evidence

Android 35 `dt_test` ARM64 phone emulator, physical 1080×2400, logical 411dp width, compile/target SDK 36, minimum SDK 26. Installed the signed release APK using a normal streamed install. An early incremental install under heavy host load caused an emulator ANR; normal installation and the completed flows did not reproduce it.

`emulator-server.go` composes the actual app, API, auth, provider, registry and supervisor with temporary `LOLAD_CONFIG`/`LOLAD_STATE`, synthetic Iris/Ultron mail and autostart/wake-on-message disabled. Trusted listener: 4193; authenticated remote listener: 4183. The emulator reached them as `10.0.2.2`. The isolated `lolad-v3-android` tmux session contains only a test banner and sleep process. No harness was launched.

Verified in emulator checkpoints, with assertions against server mail where applicable:

1. Deep link fills server/code; explicit Pair device redeems POST /api/pair and stores the credential.
2. Inbox shows waiting-on-you, unread counts and recency; Agents shows runtime and state.
3. Markdown update/deliver/ask content renders; Proceed sends the exact decision context and ask ID. Server mail confirms the decision and the UI shows the answered state.
4. A typed reply persists as operator mail with kind `reply` and `in_reply_to`; this was checked against the authoritative server conversation, not just the composer text.
5. Details shows harness/model/directory/activity; terminal displays the real capture, read-only, with visible refresh controls.
6. Pairing through the remote listener authenticates JSON and SSE. Revocation cancels the active stream, produces 401 on reconnect, removes the token and displays “This device was revoked — pair again.”
7. Light/dark variants and 1.3× system font scale captured. Keyboard Back dismisses the keyboard before navigating; screen history survives theme recreation.

The smoke automation was hardened for IME layout changes and immediate SSE revocation; initial test-driver failures were corrected without changing API contracts. Its final complete run passed, including authoritative server assertions for the typed reply. `emulator-smoke.py` contains the reproducible sequence and checkpoint resume options.

## Screenshots

`docs/screenshots/`: inbox, agents, agent details, chat and terminal in light/dark; incoming ask and sent reply in light; revoked-device recovery; enlarged-text dark terminal. Native screenshots are emulator captures, not browser mocks. All content is synthetic. No HTML/CSS detector was run because this is a native Android surface.

## Remaining verification and follow-ups

Physical QR camera scanning, TalkBack interaction, Android 8–14 behavior, simulated send timeouts and tablet adaptation need device coverage. The app loads the latest 1,000 messages per conversation; older-history pagination, attachment previews/downloads and background notifications are deferred. No deployment, Tailscale Serve or PM2 change is needed from this worker; the coordinator can publish the APK through its chosen download surface.

A fresh independent internal Impeccable file review found one material issue: clipped secondary actions. Native ActionRows now wraps those controls visibly with 8dp gaps; the signed build and complete emulator smoke sequence passed again. The reviewer scored that fix resolved with disposition `ship`; that verdict covers the scored fix. See interface-review.md for the original matrix and final scoring. Design documentation was reconciled by a separate internal documenter. No extra Orca worker or runtime state was created.

APK: `dist/lolad.apk` (5995064 bytes), SHA-256 `6291ba41dd0790a68339539648f99b94c85e850a7a02e65847c184e0480de065`.

Cleanup: the throwaway HTTP server, temporary state and private tmux server were removed after verification; the owned emulator was stopped.
