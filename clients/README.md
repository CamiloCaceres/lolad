# lolad clients

Clients for a lolad daemon. The desktop app is one shared web UI in a thin native shell; the native SwiftUI client was retired. Each one is a thin client over the same
documented contract, [`docs/client-api.md`](../docs/client-api.md). The daemon
owns every agent, all mail and all policy. Clients render state and send
operator actions.

| Directory | Platform | Toolkit | Status |
|---|---|---|---|
| `android/` | Android 8+ | Kotlin, Android Views, Material 3 | shipping |
| `../desktop/` | macOS (Windows planned) | Wails shell around the dashboard | see [docs/desktop.md](../docs/desktop.md) |

## Rules

- **Native per platform.** Each client uses its platform's own UI toolkit, so it
  gets the real system materials (Liquid Glass and vibrancy on Apple, Mica and
  Acrylic on Windows), controls, text rendering and accessibility. Share the
  contract, fixtures and design language. Do not share UI code across platforms.
- **The API doc is the contract.** If a client needs something the API lacks,
  add it to `docs/client-api.md` and the server first, then build the client.
  Never scrape the HTML dashboard or depend on routes it uses internally.
- **Tolerate unknown fields.** Ignore unknown fields and treat unknown states as
  unknown, as the API doc requires.
- **One fixture server.** `android/scripts/emulator-server.go` starts the real
  app, API and auth against temporary state and synthetic agents. Every client
  tests against it, never against the live daemon. When a second client needs it,
  move it to a shared `clients/fixture/` directory.
- **Self-contained builds.** Each directory has its own dependencies, build
  script, `.gitignore`, `README.md`, `DESIGN.md` and `PRODUCT.md`. A client build
  never touches `go.mod`, and the Go build never depends on a client.
- **Credentials stay in OS storage.** Use Android Keystore, the Apple Keychain
  or Windows Credential Manager. Tokens never appear in logs, screenshots or
  crash reports.
