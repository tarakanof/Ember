# Architecture

How `ember` is put together: the system model, the components, the
wire protocol, the display layout, and the hard-won gotchas. This is the
canonical design reference — read it before non-trivial changes. For operations
(deploy / install / verify) see [`RUNBOOK.md`](RUNBOOK.md).

## System model — the "Buddy" bridge

The project mirrors Anthropic's `claude-desktop-buddy` shape:

- The **AWTRIX clock** (Ulanzi TC001 running AWTRIX3 firmware) is the display
  device — the "Buddy". Its firmware is **unmodified**; we drive it over HTTP.
- **Host-side producers** (one per laptop/agent) are the bridge: they watch
  local AI-agent activity and POST compact status to the server.
- The **server** aggregates sessions from all producers, decides what to show,
  and publishes frames to the clock.

We own both halves of the bridge (producer + server); the firmware stays stock.
A small stateful Go service was chosen over Node-RED because it can own session
expiry, prioritization, display rotation, and multi-laptop fan-in cleanly.

```
 Claude Code (hooks)  ─┐
 Codex CLI (rollout)  ─┤  producers → POST /v1/status →  ┌─────────┐   HTTP   ┌──────────┐
 statusline           ─┘  (bearer token, per-session)    │ server  │ ───────► │ AWTRIX   │
                                                          │ + coord │  frames  │ TC001    │
 menu-bar app (macOS) ── HTTP client /state, /v1/preview ─┘         │          └──────────┘
                          edits producer.env                        └─────────┘
```

## Components

### Server — `cmd/ember`

The aggregator and the only writer to the device.

- **HTTP endpoints.** Write (bearer-token auth via `EMBER_TOKEN`):
  `POST /v1/status` (upsert: event or heartbeat), `DELETE /v1/status` (drop one
  session, idempotent 204), `POST /v1/clear` (admin wipe), `POST /v1/notify`
  (ad-hoc notification), `POST /v1/usage` (per-tool subscription usage → the
  always-on usage widget; see below). Read (no auth): `GET /state` (snapshot), `GET /healthz`,
  `GET /v1/preview` (per-card 32×8 grids for the menu preview — see below).
  Operator/introspection: `/admin/doctor`, `/admin/reload`, `/version`,
  `/metrics` (hand-rolled Prometheus, no client lib). Pomodoro, Weather
  (`GET/PUT /v1/weather/config`), and Reminders (`POST /v1/reminders/fire`)
  endpoints — see below.
- **Session model & staleness.** Each session is keyed by `(source, tool,
  session)`. Per-state staleness: `stale_seconds` (default 300) for
  running/waiting/idle, a 30 s `done_ttl_seconds` linger for done/error.
  Sessions are reaped when stale.
- **Render priority.** `waiting > error > running > done`; `idle` never wins
  (it cedes the slot, publishing nothing). For ≥2 sessions in the winning group,
  an aggregate label is shown.
- **The coordinator** (single-writer goroutine) owns all publish timing and
  device state. Responsibilities: rotation across sessions by **stable session
  key** (not slice index), attention **preempt** (jump to a waiting/error
  session) with an attention hold (`ack_timeout_seconds`, default 30 s, **read
  live** so a runtime PUT applies to the current lock) and an optional **chime**
  on fresh lock acquisition (`attention_chime`, via `/api/rtttl`), the
  **number-slot card cursor** (rotates cards within a session — see Display),
  publish **dedup** (skip identical payloads inside the lifetime window), and
  the **idle tri-state machine**: `ACTIVE` (sessions present → rotation/locked
  render) → `DIMMED` (no sessions, countdown < `idle_restore_seconds`, default
  120 s → dim-white icon) → `OFF` (countdown elapsed → stop publishing; device
  auto-evicts via frame lifetime; a runtime value of 0 skips the dim phase
  entirely). The three behavior knobs are runtime-editable via
  `GET/PUT /v1/display/config` using the standard **baseline + store-override**
  pattern (config.json baseline; SQLite `display_json` override wins, survives
  restarts and `/admin/reload`) — same shape as weather/pomodoro/usage config.
- **Display hold.** Non-idle frames are published with `prio:true` + `force:true`
  + a per-frame `lifetime` so the clock suppresses its native apps while we're
  active, but **crash-safely** returns to them if the server dies (vs the sticky,
  reboot-requiring `/api/settings` primitive — deliberately not used).

### Producers

All producers share `internal/producer` (HTTP client + `ReadEnvFile` +
`RotateLogIfLarge`) and are configured via `~/.config/ember/producer.env`.

- **Claude Code producer — `cmd/ember-claude-producer`.** Hook-based: Claude
  fires hooks per invocation; the producer maps 8 events to states
  (SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, Notification,
  Stop→DELETE, StopFailure→error, SessionEnd). Per-session flock + atomic
  temp+rename marker writes. A long-lived **`run` daemon** (LaunchAgent
  `com.ember.heartbeat`, `KeepAlive=true`) ticks every 10 s to
  re-POST/reap; it reloads `producer.env` each pass so settings-window edits
  apply live. A `statusline` subcommand reads Claude's statusline JSON (the only
  surface exposing rate %, reset, cost, model, PR) and enriches the marker. The
  daemon also runs a **usage poller goroutine** (~5 min) that reads the OAuth
  token from the **macOS login Keychain** (item `Claude Code-credentials`; never
  refreshes it — rotation races the Claude Code daemon) and GETs
  `api.anthropic.com/api/oauth/usage`, posting the 5h/weekly/per-model windows to
  `POST /v1/usage`. On 401 it stops (the user re-auths via Claude Code).
- **Codex CLI producer — `cmd/ember-codex-producer`.** Codex has **no hook
  system**, so this is a long-lived **daemon that tails rollout JSONL**
  (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`). Single-goroutine poll loop
  (2 s), live-session map keyed by rollout UUID, byte-offset tailing, keepalive
  re-POST (15 s) to stay under the server staleness reap. Filters to interactive
  `session_meta.source == "cli"`. It also writes
  `~/.local/state/ember/sessions/<uuid>.json` markers so Codex shows
  in the menu app. Codex gets a distinct **chevron+underscore** 8×8 icon vs
  Claude's robot-face (the `_` cursor overlaid in the state colour). It
  also reads `rate_limits.primary` (5h) **and `secondary`** (weekly) from the
  rollout `token_count` events and posts both to `POST /v1/usage` alongside each
  status post (host-local reset labels formatted producer-side).

### Menu-bar app — `macos/` (native SwiftUI)

macOS menu-bar companion, a **pure HTTP client** of the server (it reads
`GET /state`, `GET /v1/preview`, and drives the Pomodoro endpoints — it does
**not** read the producers' local markers). The Connection tab edits
`producer.env` (shared with the Go producers) and rebuilds the live client
without relaunch. Hybrid layout:

- **`EmberKit` (`macos/Sources/`, SwiftPM)** — all testable logic, no scene
  code: Codable models mirroring the wire shapes, `APIClient`, Status/Pomodoro/
  Preview services, `pickWinning`, `EnvFile` + validation, the settings types,
  and the `@Observable AppModel` + poller. Headless `swift test`.
- **`Ember` (`macos/Ember/`, thin Xcode app)** — an `LSUIElement` agent
  app: a `MenuBarExtra` (status + Pomodoro controls + dynamic tray glyph), a
  `Settings` scene with four tabs (**Connection / Display / Pomodoro / App**),
  and a status + preview **dashboard** `Window`. App-only prefs (icon palette,
  tray glyphs) live in `UserDefaults`; launch-at-login is `SMAppService`.

This replaced the retired Go menu (`fyne.io/systray` + DarwinKit). The Display tab's
preview is **pixel-accurate** because it renders the server's `/v1/preview` grids
— produced by the same `internal/render` core the device uses (see below).

### Render core — `internal/render`

**Single source of truth** for both the device output and the menu preview, so
the preview can't drift from the device. Holds sprites/glyphs (`font3x5`,
8×8 tool icons, overlay sprites, glass, reset/hourglass), frame primitives
(`Frame`, `RGB`, paint helpers), composition (`ComposeFrame`, cards, color
logic, layout consts), and the `Session`/`Snapshot` types. `cmd/ember` refers via type
aliases; HTTP-payload shaping (`frameToCustomApp`) stays in the status binary.
The menu preview is served by `GET /v1/preview`: the same core builds
`PreviewSession` + `PreviewFrames` (per-card 32×8 color grids) as JSON, so the
SwiftUI app paints exactly what the device shows without linking any Go. (A
`RenderRGBA` helper still bridges a `Frame` to an RGBA buffer for any
bitmap consumer.) `usage.go` adds the usage-widget primitives (8×8 tool icons,
threshold/dimmed bar colours, tight-colon clock) and the three payload builders
(`UsageFiveHourPayload` / `UsageWeeklyPayload` / `UsageModelPayload`); the
`font3x5` map gained `: h d O P S` glyphs for them.

### Pomodoro — `internal/pomodoro`

A focus timer integrated into the **same service/container** (not a separate
app). A pure `Engine` state machine (focus/short/long, pause/resume/skip/stop) drives a
**top-priority preempt inside the coordinator** — an active timer renders
`render.PomodoroPayload` (a built-in animated icon + native MM:SS + progress)
and holds the slot, edge-triggering device `ATRANS:false`/`BLOCKN:true` +
`/api/switch` on start and restoring on stop. Because a **device reboot**
silently clears those settings and the forced slot while the coordinator's flag
stays set, the takeover is **re-asserted every 30s** (`pomoReassertInterval`)
while a timer stays active, so a reboot can't strand the timer in the app
rotation. The
cycle **auto-advances** by default (`auto_start_next: true`) and **auto-stops**
after a wall-clock budget (`max_session_minutes`, default 480 = 8h, `0` = off) so
it never runs overnight; focus is configurable up to 8h. Stats persist in pure-Go
SQLite (`modernc.org/sqlite`, no CGO → distroless build intact). Runtime config
edits persist to the SQLite store (key `settings_json`, re-applied over the
**read-only** bind-mounted `config.json` baseline at boot) — so the menu can
change durations/colours/cap without a writable config file. API:
`POST /v1/pomodoro/{start,pause,resume,stop,skip}` + `GET/PUT /v1/pomodoro/config`
(bearer); open `GET /v1/pomodoro/{state,stats}`; **unauthenticated**
`POST /hooks/awtrix/button` (the device can't send a token) mapping
middle=pause/resume/start, right=skip, left=stop.

### Weather — `cmd/ember/weather.go`

A standalone widget that shows current conditions. The server fetches them
**itself** (not via a producer) from a free, key-less provider — **Open-Meteo**
by default, **MET Norway** selectable — on a `refresh_minutes` cadence. Provider
codes (WMO for Open-Meteo, `symbol_code` for MET) map to six render buckets
(`clear/clouds/fog/rain/snow/storm`) + a `severe` flag (`internal/render`
`weather.go`). The fetch also pulls the next ~24 **hourly temperatures** and (Open-Meteo
only) the location's **UTC offset** (`&timezone=auto` → `utc_offset_seconds`). The
latest observation lives in an in-memory `weatherStore`; the coordinator reconciles
two rotating tiles with the same change-and-staleness dedupe as the usage apps:

- **`ember-weather`** — 8×8 condition icon + current temperature + a 1-px
  per-hour **forecast strip** along the bottom row, coloured by a cold→warm
  temperature gradient (`render.TempColor`). On a **clear night** the icon becomes
  the current **moon phase** (`moon_phase`; phase computed locally in
  `cmd/ember/astro.go`, no API).
- **`ember-forecast`** (`forecast_tile`, default on) — same icon+temp header, then
  vertical **hourly temperature bars** filling the right (`forecast_hours`, 6..24;
  bar height + colour = temperature).

A 1-min poll loop (`StartWeather`) fetches when due and fires `/api/notify`
**popups**: on condition change (`popup_on_change`), on a fixed cadence
(`popup_interval_minutes`, `0`=off), a **sound alert** on severe-weather onset
(`severe_alert`), and **sunrise/sunset** popups (`sun_popups`) — sun times computed
locally from lat/lon (`astro.go` `sunTimes`, polar-safe), fired once per UTC day per
event within a 2-min window; the label uses the location's real UTC offset (longitude
fallback for MET). Popups use a drawn icon by default; `use_native_icons` swaps in a
native AWTRIX/LaMetric animated weather icon by ID — per-condition IDs default to
widely-used gallery icons and are overridable from the menu (`icon_ids`) so the user
can curate from developer.lametric.com/icons. Firmware quirk: a notification's own
`sound`/`rtttl` is silently dropped when it also carries a `draw`/`icon`, so chimes
(severe, sun) are played separately via `POST /api/rtttl`|`/api/sound`. Config is
fully runtime-editable (`GET/PUT /v1/weather/config`, persisted to store key
`weather_json`), including `enabled` — so the menu can turn the whole widget on/off.

### Reminders — Apple Reminders + `POST /v1/reminders/fire`

Reminders are sourced from the user's **Apple Reminders** (macOS), not an
internal list. The **menu app** (`ReminderWatcher`, EventKit) polls incomplete
reminders that have a due *time* and, when one comes due (within a short grace
window, honoring an optional lead time), POSTs **`POST /v1/reminders/fire`**
`{text, sound, duration, native_icon_id}` to the server, which renders the
bell-icon popup (`render.ReminderPopupPayload`) and pushes it to the device. The
server is **stateless** for reminders — no list, no schedule, no stored config;
all settings (enable/sound/lead/duration/icon) live app-side in UserDefaults.
Consequence: reminders fire only while the Mac is awake and Ember is running (the
Linux server can't read Apple Reminders).

> **Shared store.** Weather config + hidden-apps + Pomodoro stats all live in the
> one SQLite store. Opening it is hoisted into `ensureStore` (out of
> `initPomodoro`) so weather config persists even when Pomodoro is disabled;
> `/admin/reload` re-applies all persisted settings over the reloaded file
> config.

### Per-app clock visibility — `/v1/apps`

The menu can hide an AI app (tool) from the device. A server-held hidden-tool set
(persisted to the SQLite store key `display_hidden_apps`) is consulted by the
coordinator's `filteredSnapshot` + `keyHidden`, which drop hidden tools from the
**display path only** — the rotation pointer *and* the attention lock.
`GET /state` (Dashboard) stays unfiltered, so the Dashboard still lists every
app. `GET /v1/apps` returns the known tools (baseline `claude`+`codex` ∪ tools
seen in the live snapshot ∪ hidden) each with an `enabled` flag; `PUT /v1/apps`
`{app,enabled}` toggles one and nudges a re-render. (The hidden set shares the
Pomodoro store, so persistence is active whenever Pomodoro is enabled.)

### Device discovery & control — `internal/discovery`, `cmd/ember/device*.go`

The server finds the clock on the LAN by mDNS (browse `_http._tcp`, then a
`/api/stats` fingerprint keyed on a non-empty `uid`) instead of relying on a
hardcoded address. The effective clock URL resolves as **writable-store override
> reachable `config.json` baseline > mDNS auto-pick** (the auto-pick is in-memory
only; the read-only `config.json` is never written). The server also advertises
itself as `_ember._tcp` so the menu app can discover it (gated by
`EMBER_MDNS_ADVERTISE`). Both directions require host/macvlan networking.

The menu's Device tab manages the clock's *own* firmware settings — but **the
server stays the only writer to the device**: the tab calls `/v1/device/settings`
(bearer auth), and the server whitelists + range-validates each AWTRIX key before
forwarding to the clock's unauthenticated `/api/settings`. `ATRANS`/`BLOCKN`
remain transiently owned by the Pomodoro coordinator during a focus block.

## The "spine" — how display widgets are added

Every configurable display signal follows one pattern:

```
menu checkbox  →  producer includes the wire field  →  render draws-if-present
```

`EMBER_CONTEXT_PCT_ENABLED` / the context glass is the canonical template. The
`EMBER_*` flags gate only the **boolean wire fields** the producer sets
(`context_number`, `rate_bottom_bar`, `rate_reset`, …); they do **not** gate the
underlying data capture (the statusline producer enriches `rate_window_pct` /
`rate_reset_at` / `context_pct` unconditionally). They are pure render-opt-in
switches. To add a widget: add the toggle + wire field in the producer, render
draws-if-present in `internal/render`, add a menu checkbox.

## Wire protocol

- **Required identity:** `source` / `tool` / `session` / `state`. Optional
  enrichment fields (`context_pct`, `source_color`, `rate_window_pct`,
  `rate_reset_at`, `activity`, the `EMBER_*` booleans, …).
- **Strict vs forward-compat decode:** `handleStatus` (`POST /v1/status`) decodes
  **non-strict** (unknown fields ignored) so newer producers can post fields an
  older server doesn't know. `handleDeleteStatus` + `handleNotify` stay **strict**
  (reject unknown fields / trailing tokens, 413 via `http.MaxBytesReader`).
- **Auth:** bearer token on write endpoints, via `EMBER_TOKEN` env only —
  never argv/URL/logs. `slog.LogValuer` redaction throughout.
- **Liveness fields stay local:** process-liveness data (`owner_pid`,
  `owner_start`) lives only in the local marker, embedded so the wire decoder
  ignores it — never in the `StatusRequest` body.

### Data reality (what flows from where)

| Signal | Claude | Codex | Notes |
|---|---|---|---|
| state / tool / session | hooks | rollout JSONL | |
| `context_pct` | statusline `context_window.used_percentage` | rollout token_count | transcript heuristic was removed (over-read) |
| `rate_window_pct` (5h) | statusline `five_hour.used_percentage` | rollout `rate_limits.primary.used_percent` | |
| `rate_reset_at` | statusline `…five_hour.resets_at` | rollout `…primary.resets_at` | epoch secs; countdown computed at render time (TZ-independent) |
| `activity` / trail | hooks (`Tool: detail`) | rollout (`exec:`/`edit:`/`web:`/`mcp:`) | shared `PrependTrail` ring buffer |
| `source_card` | producer.env `EMBER_SOURCE_CARD` | producer.env `EMBER_SOURCE_CARD` | `*bool`; absent = on; hides source-name card when false |
| `session_bar` | producer.env `EMBER_SESSION_BAR` | producer.env `EMBER_SESSION_BAR` | `*bool`; absent = on; hides session-pixel bar when false |
| `tokens_today`, cost, model, PR | — | — | wire field exists for tokens_today; **no producer fills it yet** |
| usage 5h / weekly / per-model | `api/oauth/usage` (Keychain, always-on) | rollout `rate_limits.primary`+`secondary` (session-only) | drives the standalone usage widget, not per-session cards |

### AI usage widget (standalone apps)

Account-global subscription usage doesn't fit the per-session card model, so it
renders as its **own AWTRIX custom apps** that rotate natively alongside the main
app + Pomodoro. The flow: producers `POST /v1/usage` → in-memory `UsageStore`
(per tool; **not persisted** — every entry refreshes ≤5 min so a restart
self-heals) → the coordinator **reconciles** `ember-usage-<tool>-{5h,7d,opus,sonnet}`
apps each tick: push changed payloads, re-push an unchanged app once
`usageRefreshInterval` (4 min) elapses so it never outlives its on-device
`lifetime` (600 s) without a refresh, and `ClearApp` stale/hidden/disabled ones.
Per-tool show/hide reuses `/v1/apps`; the widget +
per-model toggles are server config (`usage_widget`, `usage_per_model`, default
on). Staleness TTL ≈ 2× the poll interval (10 min). Frame recipe: 8×8 tool icon
(`db`), **5h = fully-drawn tight-colon clock** (single full-frame `db`),
**7d = native-font day name** (`center:false` + `textOffset:9` + `noScroll`) over
drawn icon/unit/bar (3 `db` ops), flush-right `5h`/`7d` unit (per-model swaps it
for a gray `OP`/`SO` marker), and a dimmed (~55%) content-area threshold bar on
row 7. **Claude 5h fallback:** when the authoritative endpoint usage is
stale/absent (idle daemon, 401), the coordinator synthesises `ember-usage-claude-5h`
from the live session's statusline `rate_window_pct` + a host-local
`rate_reset_label` (the statusline producer formats the label on the Mac and
posts it on the marker, so the UTC container renders it verbatim — no server-side
timezone math). The endpoint path supersedes the fallback the moment fresh usage
arrives (and only then are 7d + per-model shown).

**5h limit-reset alarm.** A small per-tool state machine in the coordinator
(`usage_alarm.go`, checked each tick after the usage reconcile): when the
effective 5h window — fresh endpoint usage, else the live-session statusline
fallback — reads **≥ 99.5 % with a known future reset**, it arms for that
`resets_at`; once the reset passes (+60 s grace, never early) it fires **one**
auto-dismiss notification (`CLAUDE 5H RESET` / `CODEX 5H RESET`, drawn tool
icon) plus an RTTTL chime. Drifted reset estimates re-arm instead of firing; an
unreachable device retries next tick (armed state preserved); fired alarms
dedupe per `(tool, resets_at)`. State is in-memory by design — a restart
mid-window re-arms from the next snapshot. Gated only by `limit_alarm` (usage
config, default on); deliberately independent of usage-app visibility (the
alarm is about resuming work, not about tiles).

## Display layout (32×8 matrix)

Each metric owns a screen region as a **graphic**; numeric readouts are opt-in
and disambiguated by a pictogram (graphics-first). Shares the usage widget's
icon-left language (redesign 2026-06-06).

- **8×8 tool icon** — cols 0–7. Body painted in the session's **source colour**
  (`EMBER_SOURCE_COLOR` / `source_color` wire field; neutral `#CCCCCC` fallback
  when absent or invalid), so each machine has a persistent identity colour.
  State is shown by the inner feature: Claude **eye sockets** / Codex **`_`
  cursor** painted in the state colour (green=run, amber=wait, red=err,
  blue=done). Idle dim frame: body drops to ~40% gray; eye sockets / cursor stay
  dark, preserving the silhouette. Reuses the usage-widget sprites
  (Claude robot-face / Codex chevron) via `drawToolIcon8`.
- **Number slot** — cols 9–24 (`numStart=9`), a **rotating set of cards**:
  **source-name card** (source uppercased, truncated to 4 glyphs, tinted in the
  source colour or white; replaces the old `X/Y` rotation card), context `NN⌷`,
  rate `NN%` (green<70 / amber / red≥90), reset = the **HH:MM reset clock**
  (drawn tight-colon, from the session's host-local `rate_reset_label`; falls
  back to `N⧗` ceil-hours when no label, e.g. Codex), and the scrolling
  tool/trail card. Wire fields `source_card` / `session_bar` are `*bool`
  (absent = on; a producer that predates them never regresses the display).
- **Context glass** — right edge (interior cols ~26–29 × rows 1–4), 16-level
  per-pixel bottom-up fill, state-coloured.
- **Bottom row (row 7)** — three-way: the 5h rate bar (`drawRateBar`, when
  `rate_bottom_bar` on + rate present), styled as the **dimmed (~55%) threshold
  bar** over content cols 8–31; else the session-pixel bar (1 px per non-idle
  session, priority-sorted, when `session_bar` on); else off.
- **Locked attention view** — 8×8 tool icon in cols 0–7, firmware-native
  blinking text `WAIT <SOURCE>` / `ERR <SOURCE>` at `textOffset:9`; scrolls when
  the label overflows the 23 free columns. Activity detail no longer substitutes
  here — the label always names which agent/computer needs attention.
- **Pomodoro view** — AWTRIX **built-in animated icon** (`icon` field: tomato
  `3591` focus / coffee `6396` break) + native MM:SS countdown + native progress
  bar; paused dims the phase colour. (Not a drawn `db`; the drawn
  `RenderPomodoro` is retained for tests.)

## Gotchas & constraints (hard-won)

### AWTRIX firmware (0.98)
- **No multi-frame `draw` arrays.** A 2-frame pulse payload triggers `500
  ErrorParsingJson` on the device. Use firmware-native `blinkText` instead.
- **`textOffset` stacks on top of centering.** Custom apps default
  `center:true`, and the firmware *adds* `textOffset` to the centered position →
  text clips past col 31. Set `center:false` to make `textOffset` the literal
  start column.
- **Native-app suppression:** use `prio:true` + `force:true` + per-frame
  `lifetime`, never `/api/settings` (sticky, some toggles need a reboot, not
  crash-safe).
- **Device button input** comes via the HTTP `button_callback` (no MQTT broker
  needed); the device can't attach a token, hence the unauthenticated hook.
- **Verify on-device** by reading `GET /api/screen` (256 RGB ints, 32×8
  row-major) — see RUNBOOK for the ANSI-render + crafted-session technique.

### DarwinKit / AppKit (retired Go menu)
The retired Go menu was replaced by the native SwiftUI app (`macos/`), so its
hard-won DarwinKit/AppKit retention crashes (weak `NSWindow.delegate`, libffi
`NSTimer`-block frees, `NSBitmapImageRep planes`, bundle-less activation policy,
uncommitted `NSTextField` edits) are no longer live constraints.

### Producer / deploy
- **Process-liveness, not file-existence, detects session close.** A heartbeat
  that re-posts any young marker keeps dead sessions alive for hours and defeats
  every server staleness window. `SessionEnd` is unreliable (skipped on
  window-close / Cmd-Q / SIGHUP / crash). Walk the hook's process ancestry past
  the `sh` wrapper to the owning `claude`/`node` PID, record PID + `ps lstart`
  (guards PID reuse), reap when it dies (~10 s). Audit any code that round-trips
  the marker through the wire struct (it can silently strip the liveness fields).
- **Producer↔server version skew is silent.** When a feature's render is in the
  server but its data comes from a producer, shipping the producer alone shows
  nothing — the lenient decoder drops the unknown field with no error. After any
  render-side change, redeploy the server from current `main`; diagnose with
  `GET /version` vs merge history.
- **Syntactically-valid-but-wrong config defeats validation.** A
  `EMBER_SERVER_URL` typo (`:800` for `:3627`) passed the URL validator but
  dropped every POST. When "nothing shows," check the producer→server path first:
  env URL, token match, `/state` contents. Reject semantic sentinels (e.g. a `0`
  context window) and force the explicit blank instead.
- **Pure-Go SQLite keeps the distroless static build** (`CGO_ENABLED=0`). Use a
  Docker **named volume** for the writable DB as nonroot; open WAL +
  `SetMaxOpenConns(1)`; `Close()` on shutdown to checkpoint the WAL.
- **`/admin/reload` reverts runtime-persisted settings** unless the feature
  re-applies them after the config `Store` (Pomodoro durations live in SQLite,
  not the file).
- **Building inside a git *worktree* hides the VCS revision** from Docker
  `buildvcs` (the worktree `.git` is a file) → `version: unknown`. Build from a
  normal checkout / CI.

## Conventions

- **Stdlib-first, but deps that earn their slot are welcome** (STYLE.md §11).
  The Go server's only third-party dep is now `modernc.org/sqlite` — the
  `fyne.io/systray` + `progrium/darwinkit` menu deps were dropped when the menu
  became a native SwiftUI app (`macos/`). Hand-rolling protocol clients (the
  deleted MQTT 3.1.1 client) was not worth the purity tax.
- **Decompose "build feature X" into A/B/C sub-projects** with their own
  spec → plan → implementation cycle when the work hides interface contracts;
  keep them loosely coupled through a single locked protocol.
- See [`STYLE.md`](STYLE.md) for the full coding guide.
