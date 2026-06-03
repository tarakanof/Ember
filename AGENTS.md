# Agent Instructions

## Project

`awtrix-ai-status` is a small Go service that shows Claude/Codex activity on an
Ulanzi TC001 running AWTRIX3, plus an integrated Pomodoro timer. Host-side
producers report agent status; the server aggregates and drives the clock over
direct HTTP; a macOS menu-bar app configures it.

Primary goals: stay lightweight enough for an Unraid Docker container; aggregate
multiple laptop/session statuses; enforce bearer-token auth on write endpoints
(`STATUS_TOKEN`); keep credentials and local machine details out of Git.

## Read first

- **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** — system model, components,
  the "spine" pattern, wire protocol, display layout, and hard-won gotchas
  (firmware, menu app, deploy). Read before any non-trivial change.
- **[`docs/RUNBOOK.md`](docs/RUNBOOK.md)** — build/test, deploy, producer & menu
  install, the `STATUS_*` toggle reference, on-device verification.
- **[`docs/STYLE.md`](docs/STYLE.md)** — coding guide. Read before non-trivial code.
- **[`docs/HISTORY.md`](docs/HISTORY.md)** — archived full session log (forensics).

## Local Context

- AWTRIX device ID / topic prefix: `awtrix_05ffb8`
- AWTRIX HTTP URL: `http://192.168.0.14` (firmware 0.98)
- Home Assistant: `http://192.168.0.36:8123`
- GitHub remote: `git@github.com:tarakanof/awtrix-ai-status.git`
- GHCR (post first release): `ghcr.io/tarakanof/awtrix-ai-status`

Do not write secrets into this repository. The write-endpoint bearer token is
supplied via the `STATUS_TOKEN` environment variable.

## Coding Guidelines

The deep dive is [`docs/STYLE.md`](docs/STYLE.md). Repository essentials:

- Keep the service small and boring. Stdlib by default; deps that clearly earn
  their slot are fine (the Go server's only one is now `modernc.org/sqlite`; the
  macOS menu is a separate native SwiftUI app under `macos/`).
- Preserve the config-file + env-var pattern. Secrets via env only — never in
  JSON, tests, logs, or docs.
- TDD by default for new behavior. Run `gofmt` and `go test ./... -race` before
  every commit.
- Don't leave long-running local test services on port `8080`.
- One logical change per commit. Commit body explains *why*, not *what*.
- **No `Co-Authored-By` trailers** in commits.
- AI assistants (Claude Code, Codex CLI, Gemini CLI): this guide governs current
  and future work here. If a rule conflicts with a user instruction, surface the
  conflict before complying.

## Endpoints (quick reference)

Write (bearer auth): `POST /v1/status`, `DELETE /v1/status`, `POST /v1/clear`,
`POST /v1/notify`, `POST /v1/pomodoro/{start,pause,resume,stop,skip}`,
`GET/PUT /v1/pomodoro/config`. Read (no auth): `GET /state`, `GET /healthz`,
`GET /v1/preview`, `GET /v1/pomodoro/{state,stats}`. Operator: `/admin/doctor`, `/admin/reload`,
`/version`, `/metrics`. Device-only (unauthenticated): `POST /hooks/awtrix/button`.

Full behavior (staleness, render priority, the coordinator, display hold) is in
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Obsidian workflow

The user's AI control-plane vault is at
`/Users/dt/Library/Mobile Documents/iCloud~md~obsidian/Documents/AI`. The project
task note is `AI/Tasks/AWTRIX AI status.md` — it holds **current status + the
canonical open-items/TODO list** (the repo no longer carries a `TODO.md`).

**Superpowers spec/plan docs live in the vault** under
`Superpowers Specs/awtrix-ai-status/` (flat — `…-design.md` specs alongside their
plans), **not** in this repo. The repo's old `docs/superpowers/` tree was migrated
there on 2026-05-29; `.gitignore` still blocks `docs/superpowers/` so it can't
reappear in-repo.

For vault updates: read `AI/SCHEMA.md`, then the relevant `AI/Rules/*.md` (esp.
`Core.md`, `Tasks.md`, `Security.md`); update the task note when meaningful work
lands; update `AI/Dashboard.md` if status/priority changes; append a concise
entry to `AI/log.md`.
