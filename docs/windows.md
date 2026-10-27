# Native Windows 11

lolad cross-builds for Windows amd64 and arm64 without cgo, WSL or Docker.
The native multiplexer is [psmux](https://github.com/psmux/psmux); CI pins
**psmux 3.4.0** from its x64 release zip. The arm64 lolad executable is
cross-built; a real arm64 Windows runtime still needs verification.

Install psmux with `winget install psmux` or download the release zip and add
its directory to PATH. Install the native Claude Code or Codewhale executable
and authenticate it separately. Claude Code's shell hooks and the existing
lolabot `setup.sh` require Git Bash; creating a new instance is not a pure
PowerShell operation. Adopting a prepared instance avoids running setup.sh.

Put `lolad.exe` on PATH. Configuration remains under the Windows home directory:
`%USERPROFILE%\.config\lolad\config.yaml`, with state under
`%USERPROFILE%\.local\share\lolad`. `LOLAD_CONFIG` and `LOLAD_STATE` override
these paths. Agent directories must be Windows paths; replace all example Mac
paths before starting.

```yaml
listen: 127.0.0.1:4100
tmux_bin: psmux
agents_dirs: ['C:\Users\Bob\agents']
new_agents_dir: 'C:\Users\Bob\agents'
# Set to the real native executable if it is not found on PATH:
claude_bin: 'C:\Users\Bob\.local\bin\claude.exe'
```

Without `tmux_bin`, Windows prefers `psmux` and then `tmux`; Unix defaults to
`tmux`. `LOLAD_TMUX_BIN` overrides default discovery for testing. The configured
`tmux_bin` takes precedence. Windows environment names are case-insensitive;
lolad normalizes Path for harness discovery and supplies HOME from USERPROFILE.

Run `lolad doctor`, then `lolad serve`. Keep the daemon terminal running or
configure your preferred Windows service/task launcher. No Windows service is
installed automatically. The dashboard is at <http://127.0.0.1:4100>.
See [federation](federation.md) for connecting two installations; Windows support
does not expose an unauthenticated listener or change Tailscale Serve.

## Runtime and compatibility

The supervisor uses the same private `-L lolad` namespace on both platforms.
psmux starts an independent background server which survives the launching
client. Agent launches use a private config with zero-based panes/windows and
`remain-on-exit`; user tmux configuration is excluded. psmux 3.4 supports `-f`,
`new-session -c/-x/-y`, direct argv following `--`, and the resize operation,
so no shell launch or dimension-flag fallback is needed for that release.
The source compatibility reference is
[psmux's compatibility document](https://github.com/psmux/psmux/blob/master/docs/compatibility.md).

Literal input normally uses `send-keys -l`; a trailing semicolon uses byte
hex transport so the command-list parser cannot consume it.

Agent environments travel through a private file, never command arguments or
`new-session -e`. `exec-env` deletes that file before starting the child,
uses the requested environment plus pane variables, inherits ConPTY stdio,
waits and forwards the child's exit status. Unix retains its exec behavior.
Private directories on Windows inherit the user's ACLs: keep config, state and
agent directories inside that user's profile, outside shared folders.

psmux 3.4's `pane_dead_status` does not retain the process exit code. On Windows
`exec-env` writes the real child code to a private per-session `.exit` file.
`PaneDead` reads it, including after a daemon restart. Window operations use
`=session:0`, while pane operations use `=session:0.0`; psmux rejects a pane
suffix on window commands. If the wrapper is
forcibly terminated before it records a status, lolad reports -1 (unknown),
rather than inventing a successful exit. Dead-pane detection still comes from
psmux, and the supervisor applies its normal crash restart policy.

Unix dashboard terminals still attach through a PTY. Windows terminals capture
ANSI pane snapshots about every 150 ms and forward literal text and named
keys through psmux; writable terminals resize the underlying window. Read-only
viewers cannot send input or resize it. Closing a writable viewer restores
the launch dimensions. This is a snapshot viewer, so output
can repaint and does not reproduce every intermediate animation frame. Common
navigation keys are supported; unusual application-specific escape sequences,
mouse passthrough and terminal queries need separate verification. `lolad attach`
uses psmux's native interactive client.

## Validation

`go test -v ./internal/tmux/...` runs `TestConformance` against the actual
multiplexer. Set `LOLAD_TMUX_BIN` to select a specific executable. Checks name
the lolad operation: detached start with directory/size/environment, exact
session targets, PID/list, literal Unicode and punctuation, Enter/Escape/
BSpace/C-c, ANSI joined history capture, resize, client-independent server
lifetime, retained dead panes with nonzero status, and kill. The fake agent
is compiled Go, not a shell script. Every test uses a fresh isolated namespace
and removes it. Existing POSIX shell/PTY tests remain enabled on Unix.

`lolad doctor` performs a shorter private-server check and prints the failing
operation. It never changes the configured live server. The release builder
`bin/build-release.sh [version]` writes `dist/lolad-<os>-<arch>[.exe]` for
Linux, macOS and Windows, with the version embedded through `-ldflags`.
CI runs build/vet/full tests on Linux/macOS, and native build/vet/tmux
conformance on Windows, then uploads amd64 and arm64 Windows executables.

## Bob's PC checklist

- Install psmux 3.4.0, the selected native harness, Git Bash where required,
  and Tailscale; confirm each intended executable is found on PATH.
- Set Windows agent directories and a private state/config location. Start
  `lolad doctor` and confirm the isolated tmux conformance result is OK.
- Adopt a throwaway agent first. Start it from the dashboard, confirm its
  native harness TUI renders, and finish any login/provider setup manually.
- **Open question requiring a real PC:** does Claude Code's actual TUI inside
  psmux produce the pane text expected by PromptReady, and do send-keys notices
  reach and submit its composer reliably? The fake agent cannot answer this.
- Test a Unicode message, a literal quote/semicolon, Enter, Escape, Backspace,
  Ctrl-C, arrow keys and resizing in the live terminal. Check read-only viewing
  leaves the agent's dimensions and input untouched.
- Send user mail while idle, while Claude is working, and while a permission
  dialog is open. Confirm notices only submit at the recognized idle composer.
- Kill a throwaway harness and confirm the dashboard records the actual exit
  code or unknown -1 and restarts with backoff. Restart only the daemon and
  confirm it adopts the surviving psmux agent session.
- Pair the two lolads and share a throwaway agent; verify a first remote sender
  is held until approval, then exercise messages and replies both ways.
- Close the launching PowerShell window and verify detached psmux sessions
  survive; separately configure and test daemon startup after login/reboot.

## Verification record (2026-10-26)

[CI run 40410142033](https://github.com/CamiloCaceres/lolad/actions/runs/40410142033)
is green on native Windows with psmux 3.4.0: build, vet, complete tmux
conformance (including capture-poll terminal control), Windows file-lock and
environment tests, private exit-status tests, the real `lolad doctor` probe,
and amd64/arm64 release artifacts. Linux build/vet/full tests are green. All
macOS tmux tests pass; macOS full-suite CI currently fails only the delivery
package's `TestRealDelivery/retry` (staged instead of confirmed after a
partial redraw). Running packages sequentially did not remove that failure.
There are no remaining failing psmux operations in the conformance suite.

Local checks passed against tmux 3.5a on this Mac:

- `go build ./...` and `go vet ./...`.
- `go test ./internal/tmux/... ./internal/harness/... ./internal/cli/...`.
- Windows amd64 and arm64 `go build ./...` and `go vet ./...`.
- `bin/build-release.sh v4-windows-ci` produced all six targets; the native
  executable reports the embedded `v4-windows-ci` version.
- `lolad doctor` passed the isolated multiplexer check with throwaway config
  and state; no live daemon or agent was contacted.

The approved `ci/windows` branch was committed and pushed only from an isolated
clone, not the shared checkout. It contains the Windows changes and an isolated
compatibility adjustment omitting `core.Service`'s new federation embedding,
because federation implementations were being edited by other workers. Shared
`internal/core` is untouched; integration CI must include completed federation.
The CI clone also contains `internal/deliver/execenv_test.go`, a small TestMain
entry point required when real tmux tests launch through the test executable's
`exec-env` command; that helper remains to be integrated by the delivery owner.
No new Go dependency was added; Windows locking uses the existing x/sys module.
