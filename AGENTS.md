# Agent Instructions

## Project

`ember` is a small Go service that shows Claude/Codex activity on an
Ulanzi TC001 running AWTRIX3, plus an integrated Pomodoro timer. Host-side
producers report agent status; the server aggregates and drives the clock over
direct HTTP; a macOS menu-bar app configures it.

Primary goals: stay lightweight enough for an Unraid Docker container; aggregate
multiple laptop/session statuses; enforce bearer-token auth on write endpoints
(`EMBER_TOKEN`); keep credentials and local machine details out of Git.

## Read first

- **[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)** — system model, components,
  the "spine" pattern, wire protocol, display layout, and hard-won gotchas
  (firmware, menu app, deploy). Read before any non-trivial change.
- **[`docs/RUNBOOK.md`](docs/RUNBOOK.md)** — build/test, deploy, producer & menu
  install, the `EMBER_*` toggle reference, on-device verification.
- **[`docs/STYLE.md`](docs/STYLE.md)** — coding guide. Read before non-trivial code.

## Local Context

- GitHub remote: `git@github.com:tarakanof/ember.git`
- Docker Hub (post first release): `docker.io/dtarakanov/ember`
- Deployment-specific values (the AWTRIX device ID / topic prefix, its HTTP URL,
  the Home Assistant host) live in `AGENTS.local.md` — a gitignored, local-only
  file. Copy `AGENTS.local.md.example` to `AGENTS.local.md` and fill in your own.

Do not write secrets into this repository. The write-endpoint bearer token is
supplied via the `EMBER_TOKEN` environment variable.

## Coding Guidelines

The deep dive is [`docs/STYLE.md`](docs/STYLE.md). Repository essentials:

- Keep the service small and boring. Stdlib by default; deps that clearly earn
  their slot are fine (the Go server's only one is now `modernc.org/sqlite`; the
  macOS menu is a separate native SwiftUI app under `macos/`).
- Preserve the config-file + env-var pattern. Secrets via env only — never in
  JSON, tests, logs, or docs.
- TDD by default for new behavior. Run `gofmt` and `go test ./... -race` before
  every commit.
- Don't leave long-running local test services on port `3627`.
- One logical change per commit. Commit body explains *why*, not *what*.
- **No `Co-Authored-By` trailers** in commits.
- AI assistants (Claude Code, Codex CLI, Gemini CLI): this guide governs current
  and future work here. If a rule conflicts with a user instruction, surface the
  conflict before complying.

## Endpoints (quick reference)

Write (bearer auth): `POST /v1/status`, `DELETE /v1/status`, `POST /v1/clear`,
`POST /v1/notify`, `POST /v1/pomodoro/{start,pause,resume,stop,skip}`,
`GET/PUT /v1/pomodoro/config`, `GET/PUT /v1/weather/config`,
`POST /v1/reminders/fire`, `GET/PUT /v1/device/config`, `GET /v1/device/discover`,
`GET/PUT /v1/device/settings`, `GET /v1/device/stats`,
`POST /v1/device/{reboot,notify/dismiss}`. Read (no auth): `GET /state`, `GET /healthz`,
`GET /v1/preview`, `GET /v1/pomodoro/{state,stats,heatmap,workhours}`,
`GET /v1/pomodoro/dashboard` (HTML). Operator: `/admin/doctor`, `/admin/reload`,
`/version`, `/metrics`. Device-only (unauthenticated): `POST /hooks/awtrix/button`
(middle=play/pause on press; left=stop, right=skip on release; left+right held=toggle).

The `/v1/device/*` group discovers the clock (mDNS) and proxies its
`/api/settings|stats|reboot|notify` to the menu's Device tab; the effective clock
URL resolves as store override > reachable `config.json` baseline > mDNS auto-pick.
The server also advertises itself as `_ember._tcp` (default on; `EMBER_MDNS_ADVERTISE=0`
to disable). **mDNS in both directions needs the container on host (or macvlan)
networking** — multicast doesn't cross the default Docker bridge.

Full behavior (staleness, render priority, the coordinator, display hold) is in
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Obsidian workflow

Documentation lives in Obsidian (the vault path is recorded in the local-only
`AGENTS.local.md`). The project task note is `AI/Tasks/Ember.md` — it holds
**current status + the canonical open-items/TODO list** (the repo no longer
carries a `TODO.md`).

**Superpowers spec/plan docs live in the vault** under
`Superpowers Specs/ember/` (flat — `…-design.md` specs alongside their
plans), **not** in this repo. The repo's old `docs/superpowers/` tree was migrated
there on 2026-05-29; `.gitignore` still blocks `docs/superpowers/` so it can't
reappear in-repo.

For vault updates: read `AI/SCHEMA.md`, then the relevant `AI/Rules/*.md` (esp.
`Core.md`, `Tasks.md`, `Security.md`); update the task note when meaningful work
lands; update `AI/Dashboard.md` if status/priority changes; append a concise
entry to `AI/log.md`.
