# Runbook

How to build, deploy, install, and verify `ember`. For the system
design see [`ARCHITECTURE.md`](ARCHITECTURE.md); for the historical record see
[`HISTORY.md`](HISTORY.md).

## Build & test

Local Homebrew Go toolchain; do **not** set `GOROOT` manually (let `go env
GOROOT` resolve to Homebrew's `libexec`).

```sh
go test ./... -race
gofmt -w <files>
go vet ./...
go run ./cmd/ember -config config.example.json
```

If a sandboxed shell can't write the default build cache, use a repo-local one:
`GOCACHE="$PWD/.gocache" go test ./...`. In a shell with a stale
`GOROOT=/Users/dt/.go`, prefix with `unset GOROOT &&`.

## Local server deployment (dev Mac, OrbStack)

Docker is available via OrbStack; `docker buildx`, `scripts/image-smoke.sh`, and
ad-hoc `docker run` all work locally (no remote Docker host needed). The live
deployment is an ad-hoc `docker run` (no compose file) — its exact flags are
captured in `docker inspect ember`.

```sh
# build (from a normal checkout, NOT a worktree — see ARCHITECTURE gotchas)
docker buildx build -t ember:local .

# run: token via env, config bind-mounted, Pomodoro DB on a named volume
docker run -d --name ember --restart unless-stopped -p 8080:8080 \
  -e EMBER_TOKEN="$(cat ~/.config/ember/token)" \
  -v ~/.config/ember/config.json:/etc/ember/config.json \
  -v ember-pomodoro:/var/lib/ember \
  ember:local
```

- `config.json` carries device URL, `app_name` (`ember`), publish timeout.
- Token: `openssl rand -hex 32 > ~/.config/ember/token`.
- **When recreating the container, `docker inspect` it first** to replicate
  exact mount destinations / env names rather than reconstructing from memory.
- **After any render-side change, rebuild + redeploy from current `main`** —
  producer↔server version skew shows nothing rather than erroring (see
  ARCHITECTURE).
- Verify in-container: `docker exec ember /ember doctor`
  (expect all `[OK]`).

## Producer install / uninstall

**Claude** (per-Mac, hooks merged into the GLOBAL `~/.claude/settings.json` +
the heartbeat/statusline LaunchAgents):

```sh
go install ./cmd/ember-claude-producer   # rebuild the binary in ~/go/bin
ember-claude-producer install            # registers hooks + LaunchAgent
ember-claude-producer uninstall
```

`producer.env` (`~/.config/ember/producer.env`) holds: `source`,
`EMBER_SERVER_URL`, token, `EMBER_SOURCE_COLOR`, and the `EMBER_*` toggles
(below). The producer re-reads it each tick — no restart needed.

> **Gotcha:** the auto-mode classifier blocks a chained `go install + install`
> Bash call (it writes the global settings.json). Run them as **separate**
> commands, or the build silently doesn't happen and `install` uses a stale
> binary. `go install` alone is allowed; since the LaunchAgent/hook paths point
> at `~/go/bin`, rebuilding in place is usually enough — no re-`install` needed.

**Codex** daemon: installed as LaunchAgent `com.ember.codex`
(`KeepAlive=true`). Restart with `launchctl kickstart -k gui/$UID/com.ember.codex`.

**Menu app** (native SwiftUI, `macos/` — replaces the retired Go menu): generate the Xcode project and run it.

```sh
brew install xcodegen                                   # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/Ember.xcodeproj                          # pick your Dev team, then ⌘R
swift test --package-path macos                          # headless EmberKit tests
```

The generated `Ember.xcodeproj` is gitignored — regenerate it after pulling.
Launch-at-login is in-app (App tab → `SMAppService`), not a LaunchAgent. The app
reads `producer.env` for connection config and needs a server on a build that
includes `GET /v1/preview` (added 2026-05; older servers 401 that route).

## `EMBER_*` toggle reference (the "spine" flags)

Each is a render-opt-in boolean (default off unless noted). They gate the wire
field the producer sets, not the data capture. See ARCHITECTURE → "spine".

| Env var | Effect |
|---|---|
| `EMBER_CONTEXT_PCT_ENABLED` (default true) | context glass fill |
| `EMBER_CONTEXT_NUMBER_ENABLED` | `NN⌷` context-number card in the number slot |
| `EMBER_RATE_PCT_ENABLED` (default true) | 5h rate `NN%` card |
| `EMBER_RATE_BOTTOM_BAR` | 5h rate as the row-7 bottom bar |
| `EMBER_RATE_RESET` | `N⧗` reset-countdown card |
| `EMBER_ACTIVITY_DETAIL_ENABLED` | scrolling `Tool: detail` + `cardTool` |
| `EMBER_ACTIVITY_TRAIL_ENABLED` | last-N actions ticker (extends detail) |
| `EMBER_SOURCE_COLOR` | hex tint for robot + X/Y digits |

## On-device verification (no waiting for real data)

1. **Read the live matrix:** `GET http://192.168.0.14/api/screen` returns 256
   RGB ints (32×8 row-major). Render them as ANSI 24-bit colour blocks in the
   terminal to confirm pixels paint.
2. **Drive a widget to a chosen value:** `POST /v1/status` (token from
   `producer.env`) a crafted session (e.g. rate 75% → amber ⅔ bar, ctx 88% →
   near-full glass). Keep it alive with a re-POST loop faster than the
   running-staleness reap (`stale_seconds`, default 300); `DELETE` when done.
3. **Hook-free observation:** a background shell loop polling `/state` doesn't
   fire Claude hooks, so it's a clean observer of session age. Age sawtoothing
   ~5–10 s = heartbeat healthy; monotonic climb to the stale window (default
   300 s) then ABSENT = nothing refreshing it.
4. Before assuming a rebuild is needed, check `GET /version` (server revision)
   and the installed producer binary mtime — sometimes flipping `producer.env`
   toggles is the only step.

## Unraid / Docker Hub release (pending — operator dance)

Spec/plan for this live in the Obsidian vault
(`Superpowers Specs/ember/`). The release workflow (`.github/workflows/docker-publish.yml`)
fires when a strict-semver `vX.Y.Z` GitHub Release is published → multi-arch Docker Hub push. The post-merge dance:

1. Pre-flight `v0.0.1-rc1` → confirm the strict-semver gate fails red, no push; delete tag.
2. Sacrificial `v0.0.1` → confirm CI succeeds, image lands on Docker Hub.
3. Verify anonymous pull (`docker logout && docker pull docker.io/dtarakanov/ember:0.1.0`).
4. Real `v0.1.0` → confirm CI + image SHA matches the commit.
5. Install on Unraid (template URL → appdata → token → Apply); verify
   `docker exec … doctor` all `[OK]`; end-to-end producer wiring.

See `README.md` → "Unraid install" for the operator-facing walkthrough.

## Home Assistant / Node-RED

Node-RED is intentionally **disabled** (add-on stopped, manual boot, watchdog
off) — its old Pomodoro/Weather flows were brittle. Do not rebuild around
Node-RED unless the user explicitly reverses that decision.
