# lolad for Android

A native Android 8+ client for connecting to lolad, reading and replying to agent messages, answering questions, and viewing agent state and terminal snapshots. Application ID: `dev.caceres.lolad`. No Firebase, analytics or background notifications.

## Install and pair

Install `dist/lolad.apk` on the phone (or `adb install -r dist/lolad.apk`). Enable Tailscale and open lolad. The default server is `https://my-mac.example-tailnet.ts.net:4100`; change it when connecting to another server.

On the trusted dashboard, open Devices and create a pairing code. Tap **Scan dashboard QR code** or type the eight-character code and tap **Pair device**. Opening a `lolad://pair?u=<encoded-server>&c=<code>` link fills the same form; pairing requires the explicit Pair device action. Camera permission is requested only when scanning. If a device token was supplied separately, use the password field and **Connect with token**.

The server and token are encrypted together with AES-GCM; the non-exportable key lives in Android Keystore. Credentials are excluded from backups and transfers. All authenticated JSON and SSE requests send `Authorization: Bearer`. Disconnect removes the locally stored credential. A 401 removes it and offers pairing again; a 429 pauses automatic requests until you explicitly retry after the server’s wait period. Connection errors mention Tailscale. Use HTTPS for a real deployment; HTTP is supported for isolated emulator/private development.

## Use

Inbox orders conversations with unanswered questions first, then newest activity. Agents shows live status, harness, model, last activity, user unread messages and open questions. Pull either list to refresh. Open a conversation to read sanitized Markdown, quiet updates, full deliverable bodies, and questions with decision buttons. Replies target the latest incoming message; a decision targets its specific ask, with the same payload as the web inbox. Incoming mail is marked read only while its conversation is visible. The latest 1,000 messages are loaded per conversation.

**Agent details** shows state, runtime, directory, activity, and pending restart information. Wake, restart and stop require confirmation. **Terminal snapshot** is read-only; ANSI controls are stripped. It refreshes while visible and offers a manual refresh action. The client never attaches to an interactive terminal.

SSE uses the API stream with Bearer auth. Connection loss falls back to foreground polling every ten seconds and bounded reconnect backoff. Backgrounding closes the stream and stops polling. A failed send preserves the draft; refresh to check authoritative mail before retrying because sends are not idempotent.

## Build a signed APK

Requires JDK 17+, Android SDK 36 and build-tools 35.0.0. The script defaults to this machine’s shared SDK/JDK/Gradle cache paths; override `JAVA_HOME`, `ANDROID_HOME` and `GRADLE_USER_HOME` elsewhere.

```sh
./scripts/build-apk.sh
```

Runs unit tests, release lint, and release assembly, aligns and signs `dist/lolad.apk`, verifies its signature and writes `dist/SHA256SUMS`. First run creates a new personal signing key in `~/.local/share/lolad-android/release.p12` and a `0600` password file alongside it. The directory is `0700`; no key or password enters the repository. Override its location with `LOLAD_ANDROID_SIGNING_DIR`. Retain this directory privately to sign future updates. Build outputs and local configuration are ignored.

Dependency versions, native theme resources, Markdown approach and build wrapper come from Milo’s existing maestro-lite client, with camera scanning and pull-to-refresh added. See THIRD-PARTY.md and LICENSE. No Go modules were added.

## Isolated verification

From the repository root:

```sh
go run clients/android/scripts/emulator-server.go
```

This uses the real lolad app/API/auth/provider with a temporary config, database and two synthetic agents. Trusted API: `127.0.0.1:4193` (emulator `10.0.2.2:4193`); authenticated remote API: `127.0.0.1:4183` (emulator `10.0.2.2:4183`). It disables agent autostart and wake-on-message, launches no harness, and creates only a private `lolad-v3-android` tmux session for a read-only snapshot. The session and temporary state are removed on shutdown. It never opens the real daemon or Tailscale URL.

With the `dt_test` phone emulator running and the APK installed:

```sh
/usr/bin/python3 clients/android/scripts/emulator-smoke.py
```

The smoke test resets only this app’s emulator data, pairs via a deep link, lists agents, opens a conversation, replies, answers an ask, reads the snapshot, pairs against the authenticated listener, revokes that test device and verifies recovery. Screenshots go to `docs/screenshots/` in light and dark modes, including enlarged text. The Python script’s ADB path can be edited for a different SDK installation. Do not run it against production.

## Follow-ups

Background notifications are deliberately deferred. Physical-device QR camera scanning, TalkBack interaction, Android 8–14 behavior and tablets need device verification; current evidence is an Android 35 phone emulator. Attachment previews/downloads and terminal input are outside this client’s scope. Conversation history beyond the latest 1,000 messages is a future addition.
