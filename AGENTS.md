# Agent Instructions

`ember` is a small Go service that shows Claude Code / Codex / T3 Code activity
on desk displays, plus a Pomodoro timer. Host producers report agent status; the
server aggregates it, **pushes** frames to an Ulanzi TC001 (awtrix-ng) and is
**pulled** by the round knob (firmware: `tarakanof/cinder`, `~/Github/cinder`).
A macOS menu-bar app (`macos/`) configures both. It runs as a Docker container
on Unraid, kept small: stdlib Go plus `modernc.org/sqlite`.

## Docs

- [`docs/WORKFLOW.md`](docs/WORKFLOW.md): issue → spec → worktree → PR → review
  → merge → release → deploy/install → report. Read before starting any change.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): system model, components, the
  "spine", wire protocol, display layout, hard-won gotchas. Read before any
  non-trivial change.
- [`docs/API.md`](docs/API.md): every route by auth. Read before adding or
  calling an endpoint.
- [`docs/RUNBOOK.md`](docs/RUNBOOK.md): build/test, deploy, producer and app
  install, headless Linux producers, the `EMBER_*` toggles, on-device checks.
- [`docs/STYLE.md`](docs/STYLE.md): coding and commit guide. Read before
  non-trivial code.
- [`docs/MENU-BOT.md`](docs/MENU-BOT.md): the animated menu-bar bot. Read before
  touching `macos/**/Bot*`.
- `AGENTS.local.md` (gitignored; template `AGENTS.local.md.example`): device
  hosts, server URL, Obsidian vault path.

## Hard rules

- **Secrets**: never print or commit `EMBER_TOKEN`, device tokens, Wi-Fi
  passwords, `sdkconfig.secrets`. Secrets come from env only, never JSON, tests,
  logs or docs.
- **Live state is off limits** to tests and reviews: `~/.config/ember`,
  `~/.claude`, `~/.codex`, `~/Library/LaunchAgents`, `/Applications/Ember.app`.
  Don't launch a build with the installed bundle id (it re-registers producer
  agents). Installing a released app follows WORKFLOW step 6.
- **Smoke runs**: scratch port (never `:3627`) and temp DB,
  `EMBER_MDNS_ADVERTISE=0` (unset is *on*) and `EMBER_CLOCK=off` (no clock I/O,
  so it can't reach the real TC001). A run that must exercise the clock drops
  `EMBER_CLOCK=off` but sets `awtrix.auto_rediscover: false` **and** points the
  clock URL at a local stub answering `GET /api/v1/device` with an awtrix-ng
  fingerprint (otherwise it rediscovers the real clock). Prefer measuring the
  live server.
- **The knob is live and paired** (`knob-61fc8c`): never repoint it to a scratch
  server or token, re-mint it, change its Wi-Fi or factory-reset it without
  asking. If allowed, restore it and confirm a live checkin before reporting.
- **Knob USB** (`/dev/cu.usbmodem*`, 303a:1001): open it only for a user-asked
  action; never toggle DTR/RTS (RTS resets the board).
- **Install software only with the user's approval.**
- **Scratch files**: unique names per task (parallel agents share one scratchpad).
- Conventional Commits, no `Co-Authored-By`. If a rule conflicts with a user
  instruction, say so before complying.

## Local context

- Remote `git@github.com:tarakanof/ember.git`; image `docker.io/dtarakanov/ember`.
- Producers read `~/.config/ember/producer.env` (0600: `EMBER_SOURCE`,
  `EMBER_SERVER_URL` — empty/`auto` = mDNS, `EMBER_TOKEN`). Claude Code hooks
  ship as the `ember@ember` plugin; run `ember-claude-producer configure` after
  installing it so `settings.json` keeps no duplicate hooks.
- Local app builds sign with `~/.config/ember/signing-identity`
  (`scripts/build-local.sh`).
