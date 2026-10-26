# lolad — working agreement

Read `SPEC.md` and `internal/core/` before writing code. They are the contract.

Several agents work in this same checkout at the same time, each owning
different packages:

| Owner | Packages |
|---|---|
| amp | `internal/amp`, `internal/policy`, `internal/msgcli` |
| runtime | `internal/tmux`, `internal/harness`, `internal/hooks`, `internal/deliver` |
| supervisor | `internal/registry`, `internal/supervisor` |
| create | `internal/create`, `README.md`, `docs/` |
| dashboard | `internal/web` |
| integrate (later) | `main.go`, `internal/app`, `internal/api`, `internal/cli` |
| coordinator | `internal/core`, `internal/store`, `SPEC.md`, `AGENTS.md`, `go.mod` |

Rules:
- Edit only your packages. Need something from another package? Define a small
  interface in yours and depend on that; the integrator wires it.
- Test your own packages: `go test ./internal/<pkg>/...`, `go vet` on them.
  Other packages may be mid-edit; don't "fix" them.
- New dependency: `go get <module>` only — never `go mod tidy` (others' WIP
  can make it fail or drop pins).
- Do not commit, stash, reset or checkout. The coordinator commits.
- Never touch AI Maestro (`localhost:23000`, `~/.agent-messaging`, `~/.aimaestro`),
  the running agents in `/Users/you/agents/{jugui,dsh-lola}`
  (read-only reference), or the user's global `~/.claude/settings.json`.
- Tests that need tmux use a unique `-L lolad-test-<rand>` socket and kill it.
