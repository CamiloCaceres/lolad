# Contributing

Thanks for helping. lolad is small on purpose, so please open an issue
before a large change and say which problem it solves.

## Ground rules

- Read [SPEC.md](SPEC.md) and `internal/core/` first. They are the contract
  between packages; keep changes to them additive and explain them in the PR.
- Keep the scope narrow. Features that [AI Maestro](https://github.com/23blocks-OS/ai-maestro)
  already does well (memory, task boards, gateways, cloud agents) belong there.
- No cgo. The binary must keep cross-building for macOS, Linux and Windows.
- Add a dependency with `go get <module>`; don't run `go mod tidy` casually.
- Tests must pass without network access. Tests that need tmux use a unique
  `-L lolad-test-<rand>` socket and kill it afterwards.
- Never put real credentials, personal addresses or hostnames in code, tests
  or docs. Use `example.com`, `example.test` and made-up names.

## Before you open a PR

```sh
gofmt -l .
go vet ./...
go test -p 1 ./...
GOOS=windows go build ./...
```

Describe what changed, why, and how you verified it. Screenshots help for
dashboard changes.

## Coding agents

If an AI agent works on this repository, point it at [AGENTS.md](AGENTS.md).

## License

By contributing you agree that your contribution is licensed under the
[MIT License](LICENSE), or GPL-3.0 for changes in `clients/android`.
