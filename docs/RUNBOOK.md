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

The menu dropdown also runs the Pomodoro (Start/Pause/Resume/Skip/Stop) and has
per-app clock toggles (`PUT /v1/apps`). Settings → Pomodoro exposes the focus
duration (to 8h), the auto-stop cap ("Auto-stop after: N h", `0` = off), and
colours; Settings → App picks the Dock/app icon + the menu-bar tray glyphs.

## Device button → Pomodoro control

The clock's three physical buttons drive the timer via the AWTRIX3 **developer**
`button_callback` (an HTTP POST per press; no MQTT broker). On the clock's web
file manager (`http://192.168.0.14`), add the key to `dev.json`:

```json
{ "button_callback": "http://<mac-lan-ip>:8080/hooks/awtrix/button" }
```

- Use the **Mac's LAN IP** (where the container publishes `:8080`) — *not*
  `localhost`; it's DHCP, so a reservation keeps it stable.
- `dev.json` applies **at boot only** — reboot the clock after editing. Other
  dev keys (`temp_offset`, `hum_offset`) coexist; don't clobber them.
- Ember's `pomodoro.button_callback` config bool must be `true` (default).
- Mapping (press-down only): middle/select = pause / resume / start-from-idle,
  right = skip, left = stop. Ember sets `BLOCKN:true` while a timer runs so the
  buttons drive the timer instead of switching apps, restoring native nav on stop.
- Smoke-test the server half without the device:
  `curl -X POST -d "button=select&state=1" http://localhost:8080/hooks/awtrix/button`
  (should start a focus).

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

## Docker Hub release

`v0.1.0`–`v0.1.2` are published (public, multi-arch). The release workflow
(`.github/workflows/docker-publish.yml`) fires when a strict-semver `vX.Y.Z`
GitHub Release is published → multi-arch Docker Hub push (SBOM + provenance).
Requires repo var `DOCKERHUB_USERNAME` + secret `DOCKERHUB_TOKEN` — **set the
token newline-safe** (`printf '%s' val | gh secret set DOCKERHUB_TOKEN`); a
trailing newline causes a `malformed HTTP Authorization header` login failure.

To cut a release + repoint the live container:

```sh
gh release create vX.Y.Z --target main --title vX.Y.Z --notes "…"   # triggers the workflow
gh run watch <run-id> --exit-status                                  # multi-arch build ~2 min
docker pull dtarakanov/ember:X.Y.Z
docker rm -f ember && docker run -d --name ember … dtarakanov/ember:X.Y.Z   # same flags as `docker inspect ember`
```

Verify: `GET /version` reports `dirty:false`; `docker exec ember /ember doctor`
all `[OK]`. For Unraid, see `README.md` → "Unraid install" + the
`deploy/unraid/ember.xml` template. Spec/plan history lives in the Obsidian vault
(`Superpowers Specs/ember/`).

## Home Assistant / Node-RED

Node-RED is intentionally **disabled** (add-on stopped, manual boot, watchdog
off) — its old Pomodoro/Weather flows were brittle. Do not rebuild around
Node-RED unless the user explicitly reverses that decision.
