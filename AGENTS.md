# Agent Instructions

## Project

This repository contains `awtrix-ai-status`, a small Go service for displaying Claude/Codex activity on an Ulanzi TC001 running AWTRIX3.

Primary goals:

- stay lightweight enough for an Unraid Docker container
- aggregate multiple laptop/session statuses
- publish compact display state to AWTRIX3 over MQTT or direct HTTP
- keep credentials and local machine details out of Git

## Local Context

Current known infrastructure:

- AWTRIX device ID/topic prefix: `awtrix_05ffb8`
- AWTRIX HTTP URL: `http://192.168.0.14`
- Home Assistant: `http://192.168.0.36:8123`
- MQTT broker: `192.168.0.36:1883`
- GitHub remote: `git@github.com:tarakanof/awtrix-ai-status.git`
- Obsidian task note: `AI/Tasks/AWTRIX AI status.md`

Do not write secrets into this repository. MQTT credentials should be supplied through environment variables, especially `MQTT_USERNAME` and `MQTT_PASSWORD`.

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

Docker is not installed on the current laptop, so container builds may need to run on Unraid or another Docker host.

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

The service exposes HTTP input endpoints:

- `POST /v1/status`
- `GET /state`
- `POST /v1/clear`
- `POST /v1/notify`

AWTRIX output supports:

- HTTP custom app publishing
- MQTT QoS 0 custom app publishing
- notification publishing
- indicator updates

Direct AWTRIX HTTP output was live-tested successfully. MQTT output is implemented, but still needs validation from the final Unraid/container network.

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
