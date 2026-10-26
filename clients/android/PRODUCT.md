# lolad for Android
<!-- impeccable:product-schema 1 -->

## Platform
android

## Stack
Native Android Views, Material 3 and Java 17, inherited from Milo’s maestro-lite Android client; Android 8+.

## Users
Milo talks to and checks his long-running personal agents from his phone over a private network.

## Product Purpose
Connect a paired device, see agent state, read terminal snapshots, and carry on user–agent conversations with explicit decisions.

## Capabilities and Constraints
Pairing links and QR, manual code and token fallback; Keystore-protected credentials. Inbox ordered by unanswered questions and recency. Markdown messages, quieter updates, deliverables and option decisions. SSE plus polling. No analytics, Firebase or notifications. Never test against the real daemon.

## Brand Commitments
App name lolad. Reuse maestro-lite’s calm native Material design with muted hues as scanning anchors. Light and dark modes, scalable type, native Back and 48dp actions.

## Evidence on Hand
SPEC.md, internal/core, internal/api/api.go and internal/web/inbox.go are initial protocol evidence; docs/client-api.md is authoritative when available. Emulator fixtures are synthetic.
