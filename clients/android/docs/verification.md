# Android verification — 2026-10-28 (dark mode, feel and attachments)

Scope: finish the resumed, uncommitted Android work (a machine restart had killed earlier
workers) without regressing the virtualization, Markdown caching and R8 work it already
contained. Everything stayed inside `clients/android`; no real daemon, Tailscale URL, AI
Maestro state, global agent settings or live agent directory was touched. **No emulator and
no device were started in this run** — the host was restarted twice under memory pressure
and the task forbids the emulator — so this section states plainly what was and was not
verified.

## What changed

- **Appearance choice.** Overflow menu (top bar) offers Theme → Follow system / Light /
  Dark. The choice is an ordinary `SharedPreferences` value (`lolad.prefs`), never stored
  with the credential; `ThemeMode.wrap` applies it by wrapping the activity base context in
  the chosen `uiMode`, so resources, Material dialogs and system-bar icon contrast all
  follow. Changing it calls `recreate()` with saved instance state: screen, conversation,
  draft, draft focus, list/thread scroll offsets and in-flight uploads survive.
- **Dark palette retuned.** Deep neutral-cool base (`#14181E` backdrop, `#1D2229` paper,
  `#262E38` raised), lighter elevated surfaces for chips/fields/dialogs, 14% hairlines,
  inverted primary buttons (`#E7EBF1` fill, `#1D2229` label), outgoing bubbles moved to a
  calm `#2C3642` instead of white, muted/attention/outline hues darkened in light mode and
  lightened at night, and a dedicated terminal pair (`#F3F5F8`/`#2A2F36` light,
  `#12171D`/`#D6DEE8` night). `colorSurfaceContainerHigh/Highest` now point at `raised`.
- **Feel.** Ripples on inbox/agent rows, message bubbles and attachment cards; 48dp targets;
  the segmented control and Refresh button no longer pin a fixed 48dp height that clipped at
  large font scales; one confirm haptic on send/decision; the composer sits above the IME
  (edge-to-edge insets already handle it); predictive back enabled in the manifest; explicit
  restoration of thread/list/agent/terminal scroll positions across rotation and theme
  changes; upload tray is a single horizontally scrolling row so ten chips never push the
  thread away.
- **Attachments (new).** Streamed upload per file (`POST /api/mail/attachments?peer=`), one
  part named `file`, from a `ContentResolver` stream in 64 KB buffers with determinate
  progress; chips with downsampled thumbnail/type badge, middle-truncated name, size,
  progress, remove and Retry; 10-file limit with an inline reason; send enabled by text or a
  finished upload and disabled while an upload is in flight; subject derived from file names
  when the body is empty; remote peers (`name@other.lolad`) hide Attach and explain drops and
  file pastes in one line; 403/404/413/422 mapped to short sentences. Sources are also the
  system picker (`OpenMultipleDocuments`, no storage permission), paste
  (`setOnReceiveContentListener`, text wins when the clipboard has text; pasted images become
  `Pasted image yyyy-MM-dd HH.mm.ss.png`) and drag-and-drop onto the composer or thread with
  a quiet wash + aware hairline while hovering.
- **Rendering.** `mail.attachments` (the top-level array) is decoded into immutable rows.
  Clean images load through `inline=1` with bearer auth, are downsampled off the main thread,
  cached in an 8 MB LRU and on disk by attachment id, and fade in over 120ms (skipped when
  animations are off); recycled rows cancel their pending load by key/tag. Other files show
  a type badge, name, size and a "Suspicious · download only" pill. Images open a pinch/
  double-tap preview; everything else streams to a `CreateDocument` destination with
  progress and Cancel. Nothing downloaded is opened or executed, and no HTML is rendered.
- **Optimistic send.** A message carrying files appears immediately with chipless local
  thumbnails ("You · Sending…") and is replaced by the authoritative mail row; on failure the
  echo is removed, the draft and chips are kept and the reason is shown.
- **Inbox rows** with no text now preview the attached file name or "N files".
- `emulator-smoke.py` was updated for the new overflow menu (Disconnect moved into it) and a
  `content-desc` tap helper, but was **not run** (no emulator). The Go fixture was not
  changed: no attachment seed data was needed.

## Automated checks

- `./scripts/build-apk.sh` — passed `testDebugUnitTest`, `lintRelease`, `assembleRelease`,
  zip alignment, signing and signature verification.
- **29 unit tests, 0 failures** (`--rerun-tasks` after the final edits):
  `ApiTest` 2, `ProtocolTest` 8, `AttachmentTest` 13, `UploadTest` 3,
  `TokenHygieneTest` 2, `ContrastTest` 1.
  New coverage: multipart has exactly one part named `file` and escapes quotes/newlines in
  the filename; a 512 KB temp file is streamed with monotonic progress and arrives
  byte-identical with `Authorization: Bearer`; the send payload carries attachment ids with
  an empty body and a derived subject; 11 files are refused locally; 403/404/413/422 map to
  sentences; pasted-image naming is deterministic across time zones; `mail.attachments` and
  inbox previews decode; theme night-bit arithmetic; no source line logs the token, no cache
  touches it, and the token appears only in the Authorization header; WCAG contrast is
  computed from the shipped `values{,-night}/colors.xml`.
- **Contrast (measured, both palettes):** lowest text pair light **4.81:1**
  (`muted` on `quiet`), night **5.18:1**; terminal output 12.34:1 / 13.27:1; control
  outlines ≥ 3.32:1. The audit also caught and fixed the former light-mode misses
  (`muted` 4.37 on paper / 3.64 on quiet, `attention` 4.05 on quiet, `outline` 2.96).
- Release lint: **0 errors, 31 warnings** — the pre-existing pinned-dependency,
  English-string, `commit()`, backup-declaration and `ViewConstructor` categories plus one
  suppressed touch-accessibility warning on the preview's gesture view.
- Signing: APK Signature Scheme v2 and v3 verified; key remains in
  `~/.local/share/lolad-android`. APK `dist/lolad.apk`, **2,288,262 bytes** (2,233,531
  before attachments), SHA-256
  `3f46026603fa5a6837870d102dd8b4359ed2592dd4a93bcb07b418dbf50a8732`.

## Performance: not re-measured

The 1,000-message numbers earlier in this file were measured on the `dt_test` emulator with
the pre-attachment build. **They were not re-measured on a device in this run** and the
attachment path has no device numbers at all. By construction the added work does not block
the UI thread: uploads stream from content URIs on a 2-thread pool in 64 KB chunks, images
decode off-main with `inSampleSize` and a bounded LRU + disk cache, thumbnails load only when
a row is bound, a recycled row's pending load is withdrawn, and downloads are OkHttp
enqueues. Idle CPU should stay at zero because no new timer, poll or wake-up was added, but
that claim is unmeasured here. A 1,000-message thread with many image attachments still needs
a device run (`-thread 1000` plus attachment seeding would be required).

## Not verified visually (stated plainly)

No emulator or hardware run happened, so **every screen in both palettes was reviewed by
reading the code and the computed contrast, not by looking at it**. The screenshots in
`docs/screenshots/` predate this change and do not show attachments or the retuned palette.
Specifically unverified: dark/light pairing, inbox, thread cards (incoming/outgoing/update/
deliverable/ask), the composer with chips at 130%/200% font, attachment cards and preview
zoom, agent detail, events/terminal snapshot, dialogs, empty/error/offline states,
pull-to-refresh, pressed states, IME behaviour on a real keyboard, drag-and-drop from a
desktop file manager into the emulator, pasted clipboard images on a real Android clipboard,
SAF download and cancel, and low-memory upload behaviour. `ACTION_SEND` share targets were
deliberately not registered (the permission/failure surface is larger than the benefit);
drag-out to Finder has no Android equivalent.

Cleanup: no servers, fixtures or emulators were started. Gradle daemons were stopped with
`./gradlew --stop` after the build.

# Android verification — 2026-10-28 (performance and visual alignment)

Scope: make the existing client fast and align it with the macOS client's design
language (`clients/apple/DESIGN.md`). Work stayed inside `clients/android`. No real daemon,
Tailscale URL, AI Maestro, global agent settings or live agent directory was contacted; only
the isolated fixture and the `dt_test` emulator were used, and both were stopped afterwards.

## What changed

- Inbox, Agents and the conversation thread are `RecyclerView`s (`ListAdapter` + `DiffUtil`,
  stable ids, no item animator). `MainActivity` no longer builds a whole thread with
  `ScrollView` + `addView`.
- Markdown is rendered once per message id on a background pool (`MarkdownCache`, 12 MB LRU)
  and bound from cache; scrolling defers late spans and flushes them in one range change.
- Network, JSON and cache I/O run on one IO executor; a snapshot of the last known inbox,
  agents and open questions (`LocalCache`, no token/URL/pairing code) is drawn before the
  first refresh. SSE events are coalesced into one refresh per 150 ms window and the scope
  is narrowed to the conversation or agent the event names.
- Release builds enable R8 (`minifyEnabled`, `shrinkResources`) with keep rules for OkHttp,
  Markwon, ZXing and Parcelable; theme colors moved to `values/colors.xml` and
  `values-night/colors.xml` and map to Material 3 roles; `DESIGN.md` was rewritten to match
  the implemented palette, type scale and radii.

## Automated checks

- `clients/android/scripts/build-apk.sh`: passed `testDebugUnitTest`, `lintRelease`,
  `assembleRelease`, zip alignment, signing and signature verification.
- 10 unit tests passed (`--rerun-tasks` to avoid an up-to-date shortcut), 0 failures.
- Release lint: 0 errors, 22 warnings — the pre-existing pinned-dependency, English-string,
  `commit()`, backup-declaration and `ViewConstructor` categories.
- Signing: APK Signature Scheme v2 and v3 verified; key remains outside the repository.
- APK: `dist/lolad.apk`, 2,233,531 bytes (was 5,995,064 before R8), SHA-256
  `583e93089bf92636a8750dfd96276a0edeffa1e01dabea731592e58fe01d22fb`.

## Performance: method

Emulator `dt_test`, Android 35, 1080×2400 at 420 dpi, `-gpu host` (Apple M4). The fixture ran
with `-thread 1000`, which seeds 1,000 synthetic messages into the iris conversation; ports
4193/4183 and default fixture behaviour are unchanged. “Before” is the pre-change APK built
from `git archive HEAD` in `/tmp` (source only, no checkout of this tree); “after” is the
signed release APK. Both were installed with `adb install --no-incremental`, the same script
drove pairing → inbox → open iris, and each run started from `pm clear`.

- Thread open: `adb shell dumpsys gfxinfo dev.caceres.lolad reset`, tap *Open conversation*,
  wait 4 s, read the CPU `HISTOGRAM`, percentiles and slow-UI/draw counters, plus the app's
  own `lolad.perf: thread-open-ms` line (after build only).
- Scroll: reset `gfxinfo`, then 14 flings up and 14 down (`input swipe`, 300 ms each) and
  read `Janky frames` as a percentage.
- Cold start with cached state: `input keyevent HOME`, `am force-stop`, `am start -W`, then
  read `lolad.start: cached-first-frame-ms` from logcat.

## Performance: results

Thread open, 1,000 messages:

| Metric | Before | After |
|---|---|---|
| Frames drawn to reach the thread | 59 | 15 |
| Frame p95 / p99 | 32 ms / 300 ms | 18 ms / 18 ms |
| Slowest frame | 329 ms | 18 ms |
| Frames ≥ 100 ms | 2 | 0 |
| `Number Slow issue draw commands` | 5 | 1 |
| App-logged first content | not instrumented | 64 ms (64–114 ms over runs) |

The before build visibly stalls while it inflates every card; the after build logs 64 ms from
tap to a laid-out thread.

Scroll through the same thread:

| Metric | Before | After |
|---|---|---|
| Frames | 320 | 301 |
| Janky frames | 3 (0.94%) | 2 (0.66%) |
| p50 / p90 / p99 | 24 / 27 / 36 ms | 17 / 17 / 20 ms |

Scroll was already acceptable once the old thread had inflated; the cost it paid was the
multi-second open and a 5.7 MB APK. Both builds stay under 1% janky on the host GPU. A
software-rendered cross-check (`-gpu swiftshader_indirect`) put both at 42–48% janky with
42–48 ms frames, which is the renderer's floor rather than app cost, so host GPU is the
recorded method.

Cold start to first rendered screen with cached state (release APK, three runs):

| Metric | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| `am start -W` TotalTime (COLD) | 156 ms | 179 ms | 108 ms |
| Cached first frame (`cached-first-frame-ms`) | 80 ms | 80 ms | 57 ms |
| Network refresh first frame | 756 ms | 710 ms | 815 ms |

The last-known inbox appears in under 100 ms; the network refresh lands under a second and
replaces it. The banner reads “Saved state · refreshing…” until it does.

## Main-thread discipline

`assembleDebug` installs StrictMode `detectAll` (disk and network on the UI thread) with
`penaltyLog`, plus VmPolicy leak/closeable checks. The full surface was driven on the debug
build — pairing, Inbox, Agents, agent detail, terminal snapshot, the 1,000-message thread
with scrolling, and disconnect (Keystore removal, cache clear, stream close).
`adb logcat` contained **zero** StrictMode records for the app's process id. Cache writes and
reads, JSON parsing and every HTTP call run on the IO executor; the Markdown pool posts only
cached spans back to the main thread.

## Baseline profile and R8

- R8 is on for release and the signed, shrunk APK installs and passes the full smoke flow.
  The APK already carries `assets/dexopt/baseline.prof` (library profiles, 1.6 KB) and
  `androidx.profileinstaller` arrives transitively, so installation compiles that profile.
- No app-specific baseline profile was added: generating one needs a macrobenchmark module
  and repeated device runs, which is not a clean fit here. Stated rather than silently
  skipped.

## Emulator evidence

`/usr/bin/python3 clients/android/scripts/emulator-smoke.py` passed against the fixture with
the signed release APK: pair on the trusted listener, list agents, open the conversation,
answer the ask with `Proceed`, send a reply verified against authoritative server mail, read
agent details and the terminal snapshot, pair through the authenticated listener, revoke the
device, recover to the pairing screen, then capture light, dark and enlarged-text states.
Screenshots in `docs/screenshots/` were regenerated from this run. 48 dp targets, system font
scale and native Back behaviour are unchanged.

## Not done / residual risk

- Physical-device QR scanning, TalkBack, Android 8–14 and tablet layout remain unverified on
  hardware, as before.
- The 1,000-message numbers come from one emulator on a host GPU; a mid-range phone will be
  slower in absolute terms, though the open path no longer inflates the thread.
- Paging beyond the latest 1,000 messages is still absent.

Cleanup: the fixture tmux session, its temporary state and tmux server, and the `dt_test`
emulator were stopped after verification. The pre-change APK in `/tmp/lolad-before` is
scratch data and can be deleted.

---

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
