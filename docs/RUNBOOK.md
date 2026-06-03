# Runbook

How to build, deploy, install, and verify `awtrix-ai-status`. For the system
design see [`ARCHITECTURE.md`](ARCHITECTURE.md); for the historical record see
[`HISTORY.md`](HISTORY.md).

## Build & test

Local Homebrew Go toolchain; do **not** set `GOROOT` manually (let `go env
GOROOT` resolve to Homebrew's `libexec`).

```sh
go test ./... -race
gofmt -w <files>
go vet ./...
go run ./cmd/awtrix-ai-status -config config.example.json
```

If a sandboxed shell can't write the default build cache, use a repo-local one:
`GOCACHE="$PWD/.gocache" go test ./...`. In a shell with a stale
`GOROOT=/Users/dt/.go`, prefix with `unset GOROOT &&`.

## Local server deployment (dev Mac, OrbStack)

Docker is available via OrbStack; `docker buildx`, `scripts/image-smoke.sh`, and
ad-hoc `docker run` all work locally (no remote Docker host needed). The live
deployment is an ad-hoc `docker run` (no compose file) — its exact flags are
captured in `docker inspect awtrix-ai-status`.

```sh
# build (from a normal checkout, NOT a worktree — see ARCHITECTURE gotchas)
docker buildx build -t awtrix-ai-status:local .

# run: token via env, config bind-mounted, Pomodoro DB on a named volume
docker run -d --name awtrix-ai-status --restart unless-stopped -p 8080:8080 \
  -e STATUS_TOKEN="$(cat ~/.config/awtrix-ai-status/token)" \
  -v ~/.config/awtrix-ai-status/config.json:/etc/awtrix-ai-status/config.json \
  -v awtrix-pomodoro:/var/lib/awtrix-ai-status \
  awtrix-ai-status:local
```

- `config.json` carries device URL, `app_name` (`ai_status`), publish timeout.
- Token: `openssl rand -hex 32 > ~/.config/awtrix-ai-status/token`.
- **When recreating the container, `docker inspect` it first** to replicate
  exact mount destinations / env names rather than reconstructing from memory.
- **After any render-side change, rebuild + redeploy from current `main`** —
  producer↔server version skew shows nothing rather than erroring (see
  ARCHITECTURE).
- Verify in-container: `docker exec awtrix-ai-status /awtrix-ai-status doctor`
  (expect all `[OK]`).

## Producer install / uninstall

**Claude** (per-Mac, hooks merged into the GLOBAL `~/.claude/settings.json` +
the heartbeat/statusline LaunchAgents):

```sh
go install ./cmd/awtrix-claude-producer   # rebuild the binary in ~/go/bin
awtrix-claude-producer install            # registers hooks + LaunchAgent
awtrix-claude-producer uninstall
```

`producer.env` (`~/.config/awtrix-ai-status/producer.env`) holds: `source`,
`STATUS_SERVER_URL`, token, `STATUS_SOURCE_COLOR`, and the `STATUS_*` toggles
(below). The producer re-reads it each tick — no restart needed.

> **Gotcha:** the auto-mode classifier blocks a chained `go install + install`
> Bash call (it writes the global settings.json). Run them as **separate**
> commands, or the build silently doesn't happen and `install` uses a stale
> binary. `go install` alone is allowed; since the LaunchAgent/hook paths point
> at `~/go/bin`, rebuilding in place is usually enough — no re-`install` needed.

**Codex** daemon: installed as LaunchAgent `com.awtrix-ai-status.codex`
(`KeepAlive=true`). Restart with `launchctl kickstart -k gui/$UID/com.awtrix-ai-status.codex`.

**Menu app** (native SwiftUI, `macos/` — replaces the retired Go
`cmd/awtrix-menu`): generate the Xcode project and run it.

```sh
brew install xcodegen                                   # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/AwtrixMenu.xcodeproj                          # pick your Dev team, then ⌘R
swift test --package-path macos                          # headless AwtrixMenuKit tests
```

The generated `AwtrixMenu.xcodeproj` is gitignored — regenerate it after pulling.
Launch-at-login is in-app (App tab → `SMAppService`), not a LaunchAgent. The app
reads `producer.env` for connection config and needs a server on a build that
includes `GET /v1/preview` (added 2026-05; older servers 401 that route).

## `STATUS_*` toggle reference (the "spine" flags)

Each is a render-opt-in boolean (default off unless noted). They gate the wire
field the producer sets, not the data capture. See ARCHITECTURE → "spine".

| Env var | Effect |
|---|---|
| `STATUS_CONTEXT_PCT_ENABLED` (default true) | context glass fill |
| `STATUS_CONTEXT_NUMBER_ENABLED` | `NN⌷` context-number card in the number slot |
| `STATUS_RATE_PCT_ENABLED` (default true) | 5h rate `NN%` card |
| `STATUS_RATE_BOTTOM_BAR` | 5h rate as the row-7 bottom bar |
| `STATUS_RATE_RESET` | `N⧗` reset-countdown card |
| `STATUS_ACTIVITY_DETAIL_ENABLED` | scrolling `Tool: detail` + `cardTool` |
| `STATUS_ACTIVITY_TRAIL_ENABLED` | last-N actions ticker (extends detail) |
| `STATUS_SOURCE_COLOR` | hex tint for robot + X/Y digits |

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

## Unraid / GHCR release (pending — operator dance)

Spec/plan for this live in the Obsidian vault
(`Superpowers Specs/awtrix-ai-status/`). The release workflow (`.github/workflows/release.yml`) fires on a
strict-semver tag → multi-arch GHCR push. The post-merge dance:

1. Pre-flight `v0.0.1-rc1` → confirm the strict-semver gate fails red, no push; delete tag.
2. Sacrificial `v0.0.1` → confirm CI succeeds, image lands in GHCR (private).
3. Toggle the GHCR package to public (browser-only step).
4. Verify anonymous pull (`docker logout ghcr.io && docker pull`).
5. Real `v0.1.0` → confirm CI + image SHA matches the commit.
6. Install on Unraid (template URL → appdata → token → Apply); verify
   `docker exec … doctor` all `[OK]`; end-to-end producer wiring.

See `README.md` → "Unraid install" for the operator-facing walkthrough.

## Home Assistant / Node-RED

Node-RED is intentionally **disabled** (add-on stopped, manual boot, watchdog
off) — its old Pomodoro/Weather flows were brittle. Do not rebuild around
Node-RED unless the user explicitly reverses that decision.
