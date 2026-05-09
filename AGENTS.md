# Agent Instructions

## Project

This repository contains `awtrix-ai-status`, a small Go service for displaying Claude/Codex activity on an Ulanzi TC001 running AWTRIX3.

Primary goals:

- stay lightweight enough for an Unraid Docker container
- aggregate multiple laptop/session statuses
- publish compact display state to AWTRIX3 over direct HTTP
- enforce bearer-token auth on write endpoints (configured via STATUS_TOKEN)
- keep credentials and local machine details out of Git

## Local Context

Current known infrastructure:

- AWTRIX device ID/topic prefix: `awtrix_05ffb8`
- AWTRIX HTTP URL: `http://192.168.0.14`
- Home Assistant: `http://192.168.0.36:8123`
- GitHub remote: `git@github.com:tarakanof/awtrix-ai-status.git`
- Obsidian task note: `AI/Tasks/AWTRIX AI status.md`

Do not write secrets into this repository. The bearer token for write endpoints is supplied via the `STATUS_TOKEN` environment variable.

## Build And Test

Use the local Homebrew Go toolchain. `GOROOT` should not be set manually.

Common commands:

```sh
go test ./...
gofmt -w cmd/awtrix-ai-status/main.go cmd/awtrix-ai-status/main_test.go
go run ./cmd/awtrix-ai-status -config config.example.json
```

If sandboxed Go cannot write to the default build cache, use a repo-local cache:

```sh
GOCACHE="$PWD/.gocache" go test ./...
```

Docker is available locally via OrbStack on the dev laptop. `docker buildx build`, `scripts/image-smoke.sh`, and ad-hoc `docker run` work directly. No need to push to a remote Docker host for development. (Stale earlier note that "Docker is not installed" no longer applies — OrbStack was added during sub-project E.1.)

## Coding Guidelines

The deep dive lives in [`docs/STYLE.md`](docs/STYLE.md). Read it before any non-trivial code change.

Repository-specific essentials:

- Keep the service small and boring. Stdlib only unless a dep clearly earns its cost.
- Preserve the config-file + env-var pattern. Secrets via env only — never in JSON, tests, logs, or docs.
- TDD by default for new behavior. Run `gofmt` and `go test ./... -race` before every commit.
- Don't leave long-running local test services on port `8080`.
- One logical change per commit. Commit body explains *why*, not *what*. Reference relevant `docs/superpowers/specs/*` paths.
- AI assistants (Claude Code, Codex CLI, Gemini CLI): this guide governs current and future work in this repo. If a rule conflicts with the user's instruction, surface the conflict before silently complying.

## Runtime Behavior

The service exposes HTTP endpoints:

Write (bearer-token auth, gated by STATUS_TOKEN):

- `POST /v1/status` — upsert (event or heartbeat)
- `DELETE /v1/status` — drop a single session
- `POST /v1/clear` — admin: wipe all sessions
- `POST /v1/notify` — ad-hoc AWTRIX notification

Read (no auth):

- `GET /state` — snapshot
- `GET /healthz` — liveness

AWTRIX output supports:

- HTTP custom app publishing
- notification publishing
- indicator updates (waiting / running / done-or-error linger)

Direct AWTRIX HTTP output was live-tested successfully. The protocol contract (sub-project A of the AWTRIX AI status decomposition) is implemented; see `docs/superpowers/specs/2026-05-08-protocol-contract-design.md` and the matching plan in `docs/superpowers/plans/`.

## Home Assistant / Node-RED

Node-RED was intentionally disabled because the old Pomodoro and Weather flows were brittle:

- Node-RED add-on is stopped
- boot mode is manual
- watchdog is disabled

Do not rebuild this project around Node-RED unless the user explicitly reverses that decision.

## Obsidian Workflow

The user's vault is at:

```text
/Users/dt/Library/Mobile Documents/iCloud~md~obsidian/Documents
```

The AI control-plane folder is:

```text
/Users/dt/Library/Mobile Documents/iCloud~md~obsidian/Documents/AI
```

For vault updates:

1. Read `AI/SCHEMA.md`.
2. Read relevant rule files, especially `AI/Rules/Core.md`, `AI/Rules/Tasks.md`, and `AI/Rules/Security.md`.
3. Update the project task note when meaningful work is done.
4. Update `AI/Dashboard.md` if task status/priority changes.
5. Append a concise entry to `AI/log.md`.

An `obsidian-vault` MCP server is configured in Codex config for future sessions. If it is unavailable, direct Markdown file edits are acceptable, but still follow the vault rules.
