# Contributing

Bug reports, ideas and pull requests are welcome. Ember is deliberately small;
read the "Scope" section of the [README](README.md) before proposing a new
integration or display family. For a bigger change, open an issue first.

## Build and test

```sh
go vet ./...
go test ./... -race                       # server and producers
swift test --package-path macos           # the app's testable core
scripts/strings.sh check                  # app string catalog
```

The app itself builds with `xcodegen generate --spec macos/project.yml --project macos`
and Xcode; [`docs/RUNBOOK.md`](docs/RUNBOOK.md) has the details. Format Go with `gofmt`.

## Rules

- **Stdlib first.** The server uses the Go standard library plus a handful of
  small modules. A new module needs a reason in the pull request. See
  [`docs/STYLE.md`](docs/STYLE.md) §11.
- **No code comments.** The why goes in the commit message or pull request;
  the exceptions are listed in [`docs/STYLE.md`](docs/STYLE.md) §9.
- **Secrets come from the environment only.** Never put a token or password in
  JSON, tests, logs or docs.
- **Commits** use Conventional Commits (`feat(knob): …`, `fix: …`); see
  [`docs/STYLE.md`](docs/STYLE.md) Appendix A.
- Behaviour changes update the doc that owns them: routes in
  [`docs/API.md`](docs/API.md), design in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md),
  operations in [`docs/RUNBOOK.md`](docs/RUNBOOK.md).

[`AGENTS.md`](AGENTS.md) and [`docs/WORKFLOW.md`](docs/WORKFLOW.md) describe the
owner's own agent-driven workflow. Contributors don't have to follow them.

## Reporting vulnerabilities

Not here: see [`SECURITY.md`](SECURITY.md).
