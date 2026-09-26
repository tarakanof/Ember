# Architecture

How `ember` is put together: the system model, the components, the
wire protocol, the display layout, and the hard-won gotchas. This is the
canonical design reference — read it before non-trivial changes. For operations
(deploy / install / verify) see [`RUNBOOK.md`](RUNBOOK.md).

## System model — the "Buddy" bridge

The project mirrors Anthropic's `claude-desktop-buddy` shape:

- The **AWTRIX clock** (Ulanzi TC001 running awtrix-ng firmware) is the display
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
  `GET /v1/preview` (per-card 32×8 grids for the menu preview — see below), and
  the dashboard reads `GET /v1/usage`, `/v1/activity/summary`,
  `/v1/weather/state`, `/v1/clock/health` (see "Dashboard read API").
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
  an aggregate label is shown. One Go ordering, `render.StatePriority`: the
  `/state` render and the preview pick their winner with `render.PickWinning`
  (most recent within a state), while the clock's rotation and session bar
  order by it via `render.SortedActiveKeys` (ties by source/tool/session). The
  menu's Swift `pickWinning` is a port of `PickWinning`;
  `TestPickWinningTable` is the case table a Swift test should mirror.
- **The coordinator** (single-writer goroutine) owns all publish timing and
  device state. Responsibilities: rotation across sessions by **stable session
  key** (not slice index), attention **preempt** (jump to a waiting/error
  session) with an attention hold (`ack_timeout_seconds`, default 30 s, **read
  live** so a runtime PUT applies to the current lock) and an optional **chime**
  on fresh lock acquisition (`attention_chime`, via `POST /api/v1/audio/play`), the
  **number-slot card cursor** (rotates cards within a session — see Display),
  publish **dedup** (skip identical payloads until the renewal margin — see
  "Publishing over a lossy link" below), and
  the **idle tri-state machine**: `ACTIVE` (sessions present → rotation/locked
  render) → `DIMMED` (no sessions, countdown < `idle_restore_seconds`, default
  120 s → dim-white icon) → `OFF` (countdown elapsed → stop publishing; device
  auto-evicts via frame lifetime; a runtime value of 0 skips the dim phase
  entirely). `Send` is **non-blocking**: producer commands (upsert/delete/clear)
  are dropped (with `ember_coordinator_commands_dropped_total` incremented)
  rather than blocking the caller when the command buffer is full — a dropped
  fresh-attention upsert permanently loses that edge's preempt+chime, an
  accepted tradeoff versus back-pressuring every producer. The three behavior knobs are runtime-editable via
  `GET/PUT /v1/display/config` using the standard **baseline + store-override**
  pattern (config.json baseline; SQLite `display_json` override wins, survives
  restarts and `/admin/reload`) — same shape as weather/pomodoro/usage config.
- **Publishing over a lossy link.** The clock is a battery/Wi-Fi ESP32, and a
  weak link drops frame pushes wholesale rather than slowing them down (observed
  in the field: ~44 % of pushes timing out for days, `ember_publish_total`
  fail ≈ ok, while GETs from a healthy host answered in 40 ms). Three
  properties keep that from clearing the panel:
  - **Bounded attempts.** A pushed-app write gets `publishAttemptTimeout`
    (2.5 s) per attempt and `publishAttempts` (2) attempts, rather than the full
    `awtrix.timeout_seconds`. The coordinator is the single writer, so every
    second it waits is a tick it doesn't serve — and missed ticks become
    dropped commands. Only a transport failure or a 5xx/429 is retried: any
    other 4xx is the device's verdict on the payload and will not change.
  - **Retry inside the tick.** The device evicts a pushed app on *wallclock*
    lifetime, not on attempts, so a lost push is retried immediately instead of
    a dwell later. A retried-then-successful push is still one `ok` in
    `ember_publish_total`, so `ember_publish_retries_total` is what shows the
    link degrading before it starts costing frames. The display-hold writes
    (settings read/PATCH, forced switch) share this policy via `retryDevice`
    and count in the same metric.
  - **Renewal margin.** `renewalDedupWindow` holds an unchanged frame for
    `lifetime − max(lifetime/3, dwell + retry budget + 1)`. The last tick before
    the window opens can land a full dwell early, so the wallclock slack before
    eviction is `margin − dwell` — the floor is what guarantees one whole
    pushApp budget fits in it. The old one-dwell margin bought exactly one
    attempt, and a single lost push took `ember` out of the rotation until the
    frame changed.
  - **Not** a lever here: `frame_lifetime_seconds`. It is also `durationMs` on
    every held frame (see "Display hold"), so raising it to buy eviction
    headroom silently triples how long an attention lock or the idle-dim frame
    monopolises the panel. Widen the margin instead.
- **Display hold.** awtrix-ng has no per-payload priority — the AWTRIX3
  `prio:true`/`force:true`/`duration=lifetime` combination 422s on NG entirely.
  Reserved for attention: only the **locked** waiting/error frame triggers a
  forced `PUT /api/v1/apps/active` on the hold edge, and the app's own
  `durationMs == lifetimeMs` then sustains it for the attention window
  (switching happens strictly **after** a successful push — `apps/active`
  404s on an app the device doesn't know yet). The idle frames (dimmed icon,
  hot-usage) are `holdNone`: long dwell, no forced switch. The attention
  switch sends NG's `fast:true` to skip the ~1 s transition; the Pomodoro
  start's switch keeps the animation. Merely-running
  frames ask for no forced switch and a short `durationMs` (6 s, same as the
  weather/forecast tiles) so an active agent rotates alongside the other apps
  instead of owning the screen. `autoTransition:false` outranks any per-app
  dwell entirely and is reserved for the Pomodoro takeover — hold precedence is
  `holdPomodoro > holdAttention > holdNone` (`cmd/ember/coordinator_hold.go`).
  The hold state is committed only after the device accepts the edge's writes;
  a lost switch or settings PATCH is retried on the next tick. Every
  held app still expires at its `lifetimeMs` (`lifetimeExpiry` default
  `"remove"`, which **deletes** the pushed app outright) and the display
  **crash-safely** returns to native rotation if the server dies — re-pushing
  the same app is idempotent on NG (blink phase and dwell are both unaffected
  by a re-push, measured on firmware 1.0.13), so the coordinator's publish
  dedupe survives only as device/network thrift, not as a correctness
  requirement.

### Producers

All producers share `internal/producer` (HTTP client + `ReadEnvFile` +
`RotateLogIfLarge`) and are configured via `~/.config/ember/producer.env`.
**Shared marker directory contract:** producers write session markers into the
same `~/.local/state/ember/sessions/` directory, but each daemon only owns
markers whose `tool` field matches its own (e.g. the Claude daemon skips a
marker with `tool: "codex"`); a marker with a missing/empty `tool` (a legacy
marker written before the field existed) is treated as Claude's so old
markers still get reaped.

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
  `POST /v1/usage`. On 401 (or any non-200/transient error) it just skips that
  poll and retries at the next tick — it keeps polling every ~5 min forever
  until the user re-auths via Claude Code, it never stops the loop.
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
  code: Codable models mirroring the wire shapes (`Models/`), `APIClient`, one
  service per endpoint group (`Services/`, `*Service.swift`), `pickWinning`,
  `EnvFile` + validation, the settings types, and the app foundations (#119):
  `Live/` (`LiveModel`, `RefreshCoordinator`, `ActionRunner`), `Config/`
  (`ConfigModel` as `ServerConfigModel`/`EnvConfigModel`, `SettingsModels`)
  and `Presentation/` (display names, formatters, and `MenuRows`, the menu's
  row rules), plus `Device/` (`DeviceSettingsModel`: the clock's settings
  saved as a patch of the keys that changed, overlay, sensors, apps, buttons,
  audio) and `Settings/` (pane ids with the pre-restructure names mapped,
  melody choices, the Connection probe). Headless `swift test`.
- **`Ember` (`macos/Ember/`, thin Xcode app)** — an `LSUIElement` agent
  app: a `MenuBarExtra` (`.menu` style: session header and activity, other
  sessions, 5h usage per tool, next meeting or reminder, Pomodoro status and
  controls, today vs the goal, the last failed action, and a Clock submenu
  with next/previous app, dismiss, display power and Show on Clock; rows a
  server lacks are hidden, e.g. usage falls back to `/state` and display
  power needs 0.28+), the animated bot or tool glyph as its icon, a
  fixed-width sidebar `Settings` window (height resizable only, like System
  Settings; **General / Connection / Clock / Agents / Focus /
  Weather / Calendar / Sounds & Alerts**; the title follows the pane, the
  subtitle is the one save status of every config model, controls stay
  disabled until their model has loaded), a resizable **Dashboard** window ("Ember", ⌘0), and a Dock
  menu while a window is open. `Ember/Shared/` holds the views all three
  surfaces use (`LiveMatrixMirror`, `FeedStateView`, `StatTile`, `StaleChip`,
  `PhaseBadge`, `EmberColors`). Every 32×8 matrix is drawn by
  `MatrixScreenView`, which sizes itself via EmberKit's `LEDMatrixLayout`:
  square cells at a whole-point pitch (3–20 pt), centred, so it never
  stretches, and its black `ledBezel()` hugs the panel. App-only prefs (icon palette, tray glyphs) live
  in `UserDefaults`; launch-at-login is `SMAppService`.

**Live data and polling.** Every server read the UI shows is a `Feed` in
`LiveModel`, one `Loadable<T>` each (`.loading` / `.loaded(value, at:)` /
`.failed(FeedError, last:, lastAt:)`, so a failure keeps the last value as
stale). `RefreshCoordinator` runs one loop per active feed:

| Tier | Feeds | Cadence |
|---|---|---|
| A, always | `state`, `pomodoroState` | 3 s; 15 s after 3 failures, 60 s after 10 |
| B, always | `stats`, `usage`, `meetings`, `apps` | 60 s; 30 s (`stats`, `usage`) while a view holds them |
| C, only while held | `screen` 1 s, `clockHealth` 15 s, `weather`/`activity`/`workhours`/`heatmap` 5 min | — |

A view holds feeds for its lifetime with `.task { await env.live.track(…) }`
(refcounted). A feed has at most one request in flight; a second caller joins
it. 429s back off through `RateLimitBackoff`; a 404 or 405 (an older server
without the route) is `.featureOff`, which views show as "off", never as stale
data. An unchanged poll doesn't republish its value. System sleep pauses every
loop and wake refetches at once. A `/state` failure keeps the snapshot live for
3 polls (`degraded`), then marks it stale and the connection offline; the bot
only follows a live snapshot. The menu adds no polling: opening it catches up
`stats`/`meetings`/`usage` only if they're over 60 s old, and every fetch pushes
the next poll back, so stats stay at one request a minute. Actions (Pomodoro,
clock, app visibility) go through `ActionRunner`, which keeps the last failure
for 10 s and refreshes the feeds the action touched (stats follow a Pomodoro
phase change). With no window open the app makes about 2,640 requests an hour:
1,200 each to `/state` and `/v1/pomodoro/state`, 60 each to stats, usage,
meetings and apps (the old poller made about 4,800, 1,200 of them stats).

This replaced the retired Go menu (`fyne.io/systray` + DarwinKit). The Agents pane's
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
**top-priority preempt inside the coordinator** (`holdPomodoro`, which outranks
`holdAttention`) — an active timer renders `render.PomodoroPayload` (a built-in
animated icon + native MM:SS + progress) and holds the slot, edge-triggering
device `autoTransition:false`/`blockNavigation:true` (`PATCH /api/v1/settings`)
plus a forced `PUT /api/v1/apps/active` on start. Before the takeover the
coordinator reads `GET /api/v1/settings` and snapshots the user's own
`autoTransition`/`blockNavigation`; on stop it writes **those** back, not the
firmware defaults (a lost read delays the takeover a tick; a 4xx, or a reading
that equals the takeover itself — a clock left mid-takeover by an older
server — falls back to the defaults). Device-tab edits to either key made
**during** a focus block are overwritten by the snapshot on release, and
turning `autoTransition` back on mid-focus breaks the takeover until the next
edge. A lost restore backs off `restoreBackoffTicks` (5) publishes so an
offline clock doesn't stall the coordinator every tick. NG persists settings across reboots, so a takeover left behind
by a dead server would stick: the snapshot is therefore also persisted to the
store (key `pomo_takeover_prior`) for as long as the takeover is in force, and
a server that starts with one left over restores it on its first publish. On
SIGTERM, `main` waits (bounded, `shutdownTimeout` 8 s) for the coordinator's
exit restore before closing the store; if the clock is unreachable the
snapshot stays for the next start. Every hold write (read, takeover, restore,
switch) uses `pushApp`'s retry policy, and `hold` only moves once the edge's
writes have landed, so a write lost on the lossy link is replayed next tick.
Because a **device reboot** drops pushed apps while the coordinator's `hold`
flag stays set, recovery arrives via the shared
device-watch/boot-ping republish path (see "Device discovery & control" below)
rather than a Pomodoro-specific re-assert loop: `RepublishAll` resets `hold` to
force a fresh edge, which re-applies the takeover settings and the forced
switch (keeping the original snapshot). The
cycle **auto-advances** by default (`auto_start_next: true`) and **auto-stops**
after a wall-clock budget (`max_session_minutes`, default 480 = 8h, `0` = off) so
it never runs overnight; focus is configurable up to 8h. Stats persist in pure-Go
SQLite (`modernc.org/sqlite`, no CGO → distroless build intact). Runtime config
edits persist to the SQLite store (key `settings_json`, re-applied over the
**read-only** bind-mounted `config.json` baseline at boot) — so the menu can
change durations/colours/cap/goals without a writable config file. API:
`POST /v1/pomodoro/{start,pause,resume,stop,skip}` + `GET/PUT /v1/pomodoro/config`
(bearer; PUT is **merge semantics** since #84 — omitted fields keep their
current value; `daily_goal_sessions`/`weekly_goal_days` round-trip here too,
validated against `[0, 50]`/`[0, 7]`, `0` = goal off); open
`GET /v1/pomodoro/{state,stats,heatmap,workhours}` (`stats.goal` reports
progress against those two config fields — `workhours` reports
`work_start`/`work_end` as `null` on a day with no work);
**unauthenticated**
`POST /hooks/awtrix/button` (the device can't send a token) mapping
middle=pause/resume/start, right=skip, left=stop — all on press (the AWTRIX3-era
left+right chord is removed).

### Weather — `cmd/ember/weather.go`

A standalone widget that shows current conditions. The server fetches them
**itself** (not via a producer) from a free, key-less provider — **Open-Meteo**
by default, **MET Norway** selectable — on a `refresh_minutes` cadence. Provider
codes (WMO for Open-Meteo, `symbol_code` for MET) map to six render buckets
(`clear/clouds/fog/rain/snow/storm`) + a `severe` flag (`internal/render`
`weather.go`). The fetch also pulls the next ~24 **hourly temperatures** and (Open-Meteo
only) the location's **UTC offset** (`&timezone=auto` → `utc_offset_seconds`). The
latest observation lives in an in-memory `weatherStore`; the coordinator reconciles
three rotating tiles with the same change-and-staleness dedupe as the usage card:

- **`ember-weather`** — 8×8 condition icon + the current temperature **centred**
  in the free area (rows 1–5), its digits in the strip's `TempColor` gradient
  colour (degree sign white; coloured by °C in either unit) + a per-hour
  **forecast strip** on the bottom bar
  (row 7, cols 8–31; each hour takes `24/N` columns from col 8, see
  `hourSlot`),
  coloured by a cold→warm temperature gradient (`render.TempColor`). On a
  **clear night** the icon becomes the current **moon phase** (`moon_phase`;
  phase computed locally in `cmd/ember/astro.go`, no API).
- **`ember-forecast`** (`forecast_tile`, default on) — **full-width hourly
  temperature bars** (no icon/temp — those live on the conditions tile, so the
  two tiles read differently at a glance); the bars sit on the same hour grid as
  the strips (cols 8–31, `24/N` columns each), so hour *i* lines up across the
  tiles and bar widths never alternate (`forecast_hours`, 6..24; bar height +
  colour = temperature). The bars stay a drawn bitmap rather than NG's native
  `barChart` (#109): `barChart` takes at most 16 values (24 h won't fit),
  spreads them over the chart area right of the icon column (col 9, or col 0
  with no icon, never col 8) with a 1-px gap between bars, so hour *i* can't
  sit in its `hourSlot` under the strips; and a palette colours a bar by its
  value within the chart's own range, not by absolute °C like `TempColor`.
- **`ember-air`** (`air_tile`, default on) — **air quality**: 8×8 drawn wind
  icon + the current **European AQI** value (rows 1–5), both in the EEA bucket
  colour (good→extreme; `render.AQIColor`/`AQIWord`, discrete — the scale is
  bucketed; the top two buckets keep the EEA hue at full LED brightness), + a
  per-hour **AQI trend strip** on the bottom bar (row 7, cols 8–31, next 24 h,
  one column per hour, each in its own bucket colour). Data comes from the **Open-Meteo air-quality
  API** (`fetchAirQuality`, always Open-Meteo regardless of `provider` — MET
  Norway has no AQ product), riding `pollWeather`'s due-gate but fetched
  independently so one provider failing never starves the other.
  **`air_popup_threshold`** (0=off; file-load default 80 = "very poor") fires
  an edge-triggered `AIR <WORD> <N>` popup when the AQI crosses up over the
  threshold (re-arms below; a first reading already above it also fires, so a
  restart mid-episode still alerts — severe-weather precedent). No sound.

**Native tile icon** (`tile_native_icons`, default off): the **conditions
tile** swaps its drawn 8×8 sprite for the **native animated AWTRIX/LaMetric
icon** (same `icon_ids` mapping as popups) while digits/strip stay drawn,
emitted as a **partial bitmap** (`db` over cols 8–31). Device-verified
(2026-06-12): db coords are absolute and render alongside `icon`. The **moon
phase wins** over native icons on clear nights (no per-phase gallery set).
The forecast tile has no icon slot. Independent of `use_native_icons`
(popup-only).

**Precipitation overlay** (`overlay`, default on): while it is precipitating,
the conditions tile and weather popups carry NG's per-app `overlay`, which the
firmware animates over the finished page (drawn last, it never clears the
text or bitmap). The provider code picks it, finer than the six buckets, by
one rule for both providers: rain of any kind, freezing rain and sleet →
`rain`, or `storm` when heavy (WMO 65/67/82, MET `heavy…rain`/`heavy…sleet`);
snow → `snow`; thunder → `thunder`; drizzle → `drizzle` (WMO 51–57 only —
MET has no drizzle symbol, and its light rain is WMO's slight rain 61/80);
rime fog 48 → `frost`.
Plain fog, clear and cloudy send no key, so the device's global overlay (if
the user set one) still shows. Costs ~17 bytes per push, only while
precipitating. The previews can't animate it and don't draw it.

**Icon provisioning** (`ensureNativeIcons`): the device's own on-demand
gallery downloads proved unreliable (observed failing for hours → iconless
tile), so the **server provisions icons**: on startup and on every weather or
Pomodoro config apply it lists the clock's `/ICONS` folder (`GET
/list?dir=/ICONS`), downloads any missing configured icon ID from the
LaMetric gallery (`.gif`→`.jpg` fallback, then the extensionless URL as a
last resort — some IDs, e.g. the Pomodoro tomato `29802`, exist only as a
PNG there, which is decoded and re-encoded as GIF locally since awtrix-ng's
upload only accepts GIF/JPEG magic bytes), and uploads it (`multipart
POST /edit`, `Publisher.ListIcons`/`PutIcon`). List failures abort the run;
per-icon failures log and retry on the next apply/restart. Covers both the
weather condition icons and the Pomodoro tomato/coffee icons (`29802`/`6396`)
whenever their owning feature is enabled.

**Weather preview** — `GET /v1/weather/preview` (open, read-only, mirrors
`/v1/preview`): renders the tiles under draft query params (`rotate_in_apps`,
`forecast_tile`, `air_tile`, `forecast_hours`, `units`) into the same
`{frames}` grids, using the live observations when present, else canned
samples (21 °C clouds, sinusoidal 24 h arc; AQI 42 easing off overnight) so it
never renders blank. Native-icon mode previews with
the drawn sprite (the canvas can't animate gallery icons). Feeds the menu's
Weather tab "Display" section, which also folds Location/Tile/Forecast/Popups
into collapsible sections and overlays a "1 of N" cycle indicator
(`PreviewCanvas`, shared with the Display tab).

**Pomodoro / Reminders previews** — same open, read-only pattern:
`GET /v1/pomodoro/preview` renders one drawn frame per phase
(`focus`/`short_break`/`long_break` via `RenderPomodoro`, 70 % of the phase
remaining so the progress bar reads mid-session) under draft params
(`focus_minutes`, `short_break_minutes`, `long_break_minutes`, `focus_color`,
`break_color`); the device itself shows the native animated icon + firmware
text (`PomodoroPayload`). `GET /v1/reminders/preview` renders the bell alarm
popup (`ReminderPopupFrame`, optional `text` param). Both feed the stacked
"Display" sections on top of the menu app's Pomodoro and Reminders tabs.

A 1-min poll loop (`StartWeather`) fetches when due and fires
`POST /api/v1/notifications` **popups**: on condition change
(`popup_on_change`), on a fixed cadence (`popup_interval_minutes`, `0`=off), a
**sound alert** on severe-weather onset (`severe_alert`), and
**sunrise/sunset** popups (`sun_popups`) — sun times computed locally from
lat/lon (`astro.go` `sunTimes`, polar-safe), fired once per UTC day per event
within a 2-min window; the label uses the location's real UTC offset (longitude
fallback for MET). Popups use a drawn icon by default; `use_native_icons` swaps in a
native AWTRIX/LaMetric animated weather icon by ID — per-condition IDs default to
widely-used gallery icons and are overridable from the menu (`icon_ids`) so the user
can curate from developer.lametric.com/icons. awtrix-ng honors a notification's own
`sound`/`soundRtttl` even when it also carries a `draw`/`icon` (the AWTRIX3-era
gotcha that forced chimes out-of-band via `/api/rtttl`/`/api/sound` is gone), so the
severe-alert chime rides directly on the popup payload. Config is
fully runtime-editable (`GET/PUT /v1/weather/config`, persisted to store key
`weather_json`), including `enabled` — so the menu can turn the whole widget on/off.

### Reminders — Apple Reminders + `POST /v1/reminders/fire`

Reminders are sourced from the user's **Apple Reminders** (macOS), not an
internal list. The **menu app** (`ReminderWatcher`, EventKit) polls incomplete
reminders that have a due *time* and, when one comes due (within a short grace
window, honoring an optional lead time), POSTs **`POST /v1/reminders/fire`**
`{text, sound, duration, native_icon_id, hold, repeat_sound}` to the server,
which renders the
bell-icon popup (`render.ReminderPopupPayload`) and pushes it to the device. A
reminder chimes once. With the opt-in `repeat_sound` (menu: "Repeat sound
until dismissed", default off) a `hold` alarm with sound loops its chime
(`soundLoop`, a melody with a trailing rest) until dismissed. The server
caps that loop, since an alarm nobody is there to dismiss would ring for
hours: `StartReminderLoopGuard` checks every 15 s and dismisses the alarm by
name when its 15-min hold window runs out, and at quiet-hours start dismisses
it and re-pushes it held but silent (`quietPublisher` strips sound only at
push time). A button acknowledgement just forgets the loop; a failed dismiss
is retried on the next check. An unheld reminder carries `repeat:1`, so a
long text scrolls through fully before it leaves (the meeting and weather
popups do the same). The server keeps only that in-memory loop state for
reminders — no list, no schedule, no stored config;
all settings (enable/sound/hold/repeat/lead/duration/icon) live app-side in
UserDefaults.
Consequence: reminders fire only while the Mac is awake and Ember is running (the
Linux server can't read Apple Reminders). Each POST carries an `Idempotency-Key`
header (the occurrence's `id|due` key); the server remembers keys for 10 min and
answers a repeat with 200 without pushing again (a failed push releases the key).
The app waits up to 20s for the answer (the server holds the request while it
pushes to the clock, up to 10s) and retries on the next poll, inside the grace
window, only when the failure proves nothing was sent: connection refused/no
route, 429, or another 4xx. A timeout or 5xx (e.g. 502 after a lost clock ack)
may have rung the clock, so it is not retried. The watcher logs through
`os.Logger` (subsystem `com.ember.Ember`, category `reminders`) with reminder
titles marked `.private`.

> **Shared store.** Weather config + hidden-apps + Pomodoro stats all live in the
> one SQLite store. Opening it is hoisted into `ensureStore` (out of
> `initPomodoro`) so weather config persists even when Pomodoro is disabled;
> `/admin/reload` re-applies all persisted settings over the reloaded file
> config. The clock URL is the exception: a reload keeps the running URL
> (menu override or mDNS-discovered clock) unless the file's
> `awtrix.http_base_url` itself changed, and even then a store override wins.

### Meetings — next-meeting countdown (`internal/meetings`, `cmd/ember/meetings*.go`)

A server-side ICS poller that puts a rotating **`ember-meet`** countdown tile on
the device before each calendar meeting, optionally with a T-minus popup and
chime.

**ICS parser — `internal/meetings`.** Pure functions, no I/O. Parses one or more
ICS feeds (`github.com/arran4/golang-ical`) and expands recurring events into
concrete occurrences (`github.com/teambition/rrule-go`). These are the server's
3rd and 4th non-stdlib deps (after `modernc.org/sqlite` and `brutella/dnssd`).
Both earn their slot: ICS folding/escaping and RRULE/DST-correct recurrence
expansion are not hand-rollable safely — a weekly 09:00 meeting must stay at
09:00 across DST transitions, which requires iterating in the original timezone.
All-day events (`VALUE=DATE`) and `STATUS:CANCELLED` events are actively skipped;
EXDATE exclusions and `RECURRENCE-ID` overrides are applied. Floating-time values
(no TZID, no trailing `Z`) fall back to server-local (UTC in the container) — a
documented limitation. The binary imports `time/tzdata` so the distroless image
carries the embedded tz database needed for `TZID` resolution.

**Feed URLs — env only.** ICS feed URLs are **credentials** (possession = calendar
read access). They live solely in `EMBER_MEETINGS_ICS_URLS` (comma-separated;
`webcal://` and `webcals://` are accepted and rewritten to `https://`;
scheme comparison is case-insensitive). They are never stored in the JSON config,
the SQLite store, logs, or any API response. `GET /v1/meetings/config` returns an
`ics_urls_configured` count only.

**Poller (`StartMeetings`, `pollMeetings`).** Mirrors `StartWeather` exactly: a
1-min ticker, an initial fetch for a prompt first tile, and two-level timing:
- **5-min due-gate** (`meetingsRefreshInterval`): feeds are fetched at most once
  per 5 minutes. The gate is set on both success and failure so a failing feed
  backs off the full interval between attempts (not every tick).
- **36-hour recurrence horizon** (`meetingsHorizon`): `Expand` generates only
  occurrences within the next 36 hours — enough for any workday-ahead view.
- **60-min staleness guard** (`meetingsStaleTTL`): the tile and popup only act
  while the last *successful* fetch is less than 60 minutes old. If feeds go
  dark, the tile and popup silently stop rather than ghost a cancelled meeting.
  A stale feed is a non-fatal `WARN` in `/admin/doctor`; it never causes a 503.
- **Per-URL failure isolation**: if one feed fails to fetch or parse, the others
  still contribute to the upcoming list; `lastFetchOK` advances only when at
  least one feed succeeds.

**Coordinator (`reconcileMeetingApp`).** Uses the shared `reconcileTile` helper
(also used by `ember-weather`, `ember-forecast`, and `ember-air`) for the
clear/dedupe/re-push state machine. `ember-meet` joins the rotation when the next
meeting is within `tile_lead_minutes` (default 60) and the feed is fresh; it
leaves the rotation at meeting start (the tile never shows "0m"). The countdown
payload changes each minute, so the payload-bytes diff naturally re-pushes without
a dedicated timer — the same mechanism that refreshes the weather tiles.
The tile reads `<N>M <TITLE>`: the countdown leads, because a long title
scrolls and minutes at the end of it were off screen for most of the dwell.

**Popup and chime.** An edge-triggered T-minus popup fires at
`start − popup_lead_minutes` (default 2; 0 = off), deduped per occurrence
(`UID|start` key), with a 2-minute grace window covering a missed tick. The
chime rides directly on the notification's `soundRtttl` key — awtrix-ng plays
it alongside the popup's own `draw`/`icon` (unlike AWTRIX3, which silently
dropped a notification's `sound`/`rtttl` whenever the payload also carried a
`draw`/`icon`, forcing the chime out via a standalone `/api/rtttl` call). The
severe-weather and 5h-reset alarms ride the same in-band pattern now; only the
attention chime (`PlayRTTTL`, coordinator.go) stays a separate call, because it
has no notification of its own to ride. Quiet hours mute the chime (audio only —
the popup visual always shows).

**Config and persistence.** `MeetingsConfig` (`enabled`, `tile_lead_minutes`,
`popup_lead_minutes`, `chime`) is persisted to SQLite store key `meetings_json`
via the same baseline + store-override pattern as weather/pomodoro/usage config.
API: `GET/PUT /v1/meetings/config` (bearer auth).

**Read endpoints (no auth).** `GET /v1/meetings/preview` renders the `ember-meet`
tile into a 32×8 frame grid, using the live next occurrence when present and a
STANDUP/12 min sample otherwise (never blank). `GET /v1/meetings/state` returns
up to 5 upcoming occurrences (`{title, start}` RFC3339 whole-seconds) plus
`fetched_at` for the menu's Upcoming list.

**macOS Meetings tab.** Preview, enable toggle, feed-count status (count only —
the tab can't see or set URLs; it shows the count from `ics_urls_configured` and
points the user at the env var), tile-lead and popup-lead steppers, chime toggle,
and the upcoming-meetings list.

**Limitations.** Declined-event filtering is best-effort: the server only skips
`CANCELLED` and all-day events. Declined-but-`CONFIRMED` events appear if the
feed includes them (most ICS exports from Google/iCloud omit declined events, but
that is feed-side behaviour, not enforced here). Floating-time ICS values fall
back to UTC in the container. Calendars without an ICS export URL (e.g. shared
Exchange calendars without a subscription link) do not appear.

### Per-app clock visibility — `/v1/apps`

The menu can hide an AI app (tool) from the device. A server-held hidden-tool set
(persisted to the SQLite store key `display_hidden_apps`) is consulted by the
coordinator's `filteredSnapshot` + `keyHidden`, which drop hidden tools from the
**display path only** — the rotation pointer *and* the attention lock.
`GET /state` (Dashboard) stays unfiltered, so the Dashboard still lists every
app. `GET /v1/apps` returns the known tools (baseline `claude`+`codex` ∪ tools
seen in the live snapshot ∪ hidden) each with an `enabled` flag; `PUT /v1/apps`
`{app,enabled}` toggles one and nudges a re-render. (The hidden set shares the
Pomodoro SQLite store, but `ensureStore` is called unconditionally at boot —
independent of whether Pomodoro itself is enabled — so persistence is active
regardless of the Pomodoro setting; see "Shared store" below.)

### NG indicators, capabilities, and app ordering (#70)

**Corner LED indicators** (`coordinator_indicators.go`) are opt-in
(`display.indicators`, default off) and carry ambient status that survives
whatever frame is on the matrix: LED1 dim green while any session is
`running`, LED2 blinks amber (waiting) or red (error) while the coordinator
holds the attention lock — following the lock, not any individual waiting
session, since the lock is what's actually holding the screen — and LED3 dim
blue during quiet hours. `applyIndicators` only writes an LED whose desired
state changed (`PUT /api/v1/indicators/{1-3}`, or `DELETE` to turn one off —
NG keeps the stored colour/blinkMs on a bare `PUT`, so only `DELETE` truly
resets one), so the steady state costs no extra device traffic.

**Firmware capabilities** (`GET /api/v1/capabilities`) are fetched at startup
and again on every rediscovery, cached in-process, and served at
`GET /v1/device/capabilities` (falling back to a live proxy fetch when the
cache is cold) — the firmware's supported effect/transition/overlay/palette
name lists plus its `audio{buzzer,track,mp3,radio}` outputs (NG 1.1.0 replaced
the old top-level `radio` flag), relayed as the device's own document so no
key is dropped. Settings › Clock and Sounds use them to populate its transition
picker and to gate the buzzer-volume row, instead of guessing at a static
enum. `/admin/doctor` reports the cached counts (and whether a buzzer is
present) as its `capabilities` check.

**App ordering** (`GET/PUT /v1/device/apps`) proxies `GET /api/v1/apps` /
`PUT /api/v1/apps/order` — ordering plus enable/disable of the device's own
apps, replacing the AWTRIX3 settings keys `TIM`/`DAT`/`TEMP`/`HUM`/`BAT` that
NG has no equivalent for (name only what you want to change). The ambient
weather overlay is a separate concern, `PATCH /api/v1/display` via
`GET/PUT /v1/device/display` — not part of app ordering. Display power is
its own route, `PUT /v1/device/display/power {"power":bool}`, which sends
`power` alone so an overlay edit can never blank the panel and a power toggle
can never clear the overlay; the blank is runtime-only (a reboot relights the
matrix) and a `wakeup` notification still punches through it. The device's own
rotation needs at least two apps enabled to actually rotate; with only one
enabled app it just stays on it.

### Device discovery & control — `internal/discovery`, `cmd/ember/device*.go`

The server finds the clock on the LAN by mDNS (browse the awtrix-ng-specific
`_awtrixng._tcp` service type — NG registers its own, so the browse no longer
sweeps every web server on the LAN like the old generic `_http._tcp` browse
did) with a `FIND_AWTRIXNG` UDP broadcast fallback (broadcast to `:4210`, reply
collected on a fixed `:4211`; directed broadcasts are needed in practice on
some networks) for when multicast doesn't make it through. A resolved host is
fingerprinted via `GET /api/v1/device`: it counts as the clock only when it
reports both a non-empty `uid` **and** `boardType == "awtrixng"` — the AWTRIX3
`/api/stats` fingerprint doesn't exist on NG. The effective clock URL resolves
as **writable-store override > reachable `config.json` baseline > mDNS
auto-pick** — while the pinned URL (store override or config baseline)
answers, that precedence holds as before. But the pin is
**reachability-tested, not just trusted**: a 1.5s HTTP probe runs at boot and
again every 30s from a background watcher (`StartDeviceWatch`), and if the
currently-effective URL (store override included) stops answering, the server
falls through to a fresh mDNS auto-pick so the clock keeps working after a
DHCP renumbering. The same watch tick also reads the device's `uptimeSeconds`
to detect a reboot and triggers `RepublishAll` — pushed apps are RAM-only on
awtrix-ng, so a reboot silently drops every app the coordinator believes is
still on the device, and this is what pushes them all back. The 30s interval
was chosen to match the old Pomodoro-only 30s re-assert loop it replaced, so
worst-case recovery latency didn't regress; the Berry boot-ping hook (#73)
(`POST /hooks/awtrix/boot`, an unauthenticated device-side hook, config toggle
`awtrix.boot_ping`) calls `RepublishAll` directly on boot instead of waiting
for the next tick, making recovery near-instant with the 30s watch as
fallback. Only the uptime counter decides a reboot: it went backwards, or it
fell more than 10s behind wall time since the last answered probe (a reboot
during a long gap). A missed probe on its own is **not** a reboot — the
server→clock link drops a large share of requests, and the old "unreachable,
then answering" rule republished (and re-switched the screen) every few ticks.
For the same reason the reachability check retries once before it falls back
to an mDNS browse, and a browse that finds the clock at the URL already in use
is not a swap. A real swap to a new URL does republish. `RepublishAll`
coalesces calls less than 10s apart into one immediate plus one deferred
republish, and both `/hooks/awtrix/*` routes sit behind the per-IP rate
limiter (the button hook also caps its body at 1 KB), so an unauthenticated
flood can't turn into a republish storm. Swaps are **in-memory
only** — `config.json` and the writable store are never rewritten, so a
config/store edit still takes effect the next time its source URL goes
unreachable. The whole probe loop is gated by `awtrix.auto_rediscover` (config,
default on; `/admin/doctor`'s `clock` check reports the source, reachability,
and last re-discovery time/result). The server also advertises
itself as `_ember._tcp` so the menu app can discover it (gated by
`EMBER_MDNS_ADVERTISE`). Both directions require host/macvlan networking.

Settings › Clock (and the clock half of Sounds & Alerts) manages the clock's
*own* firmware settings — but **the server stays the only writer to the
device**: the app sends only the keys that changed to `/v1/device/settings`
(bearer auth), and the server whitelists + range-validates each NG settings key
(`device_settings.go`'s `deviceSettingRules`) before forwarding to the clock's
unauthenticated `PATCH /api/v1/settings`. `autoTransition`/`blockNavigation`
(NG's replacements for AWTRIX3's `ATRANS`/`BLOCKN`) remain transiently owned by
the Pomodoro coordinator during a focus block — the menu can still read them,
but writing them mid-focus-block would race the coordinator. Time/date are
discrete typed fields on NG (`timeMode`, `dateOrder`, `dateSeparator`, …) with
no format strings to validate, unlike AWTRIX3's `TFORMAT`/`DFORMAT` strftime
strings. `buttonCallback` is set separately via `PUT /v1/device/buttons`
(below) because it lives on `/api/v1/system`, not `/api/v1/settings`.
The whitelist tracks NG 1.1.x: `soundEnabled` (device mute) and
`buzzerVolume` (0–100) replaced the pre-1.1.0 `volume` (0–30), and
`smoothScroll` (an AWTRIX3 key NG never had; `scroll.mode` replaces it) is
gone — NG rejects unknown keys with 422, so one stale key fails the whole
PATCH. The per-app colours (`timeColor`, `dateColor`, `temperatureColor`,
`humidityColor`, `batteryColor`) accept `null`, which returns the app to
inheriting `textColor` (Settings' "Same as text"). A 0.27.x server filters its
GET to its older whitelist, so the app treats missing `soundEnabled`/
`buzzerVolume` as "no NG 1.1 support" and hides mute, volume and "Same as
text" there; a 404 on the audio routes hides the test chime and melody list.

When the clock refuses a proxied request, the `/v1/device/*` handlers relay
its NG error envelope instead of a bare 502: the menu gets
`{"error":"clock returned 422: <message> (field <key>)","code","field"}` with
the device's status for request errors (400/404/409/413/415/422) and 503
(busy / no such hardware), and 502 for everything else — a device 401/403
included, so it can't be mistaken for a bad Ember token.

**Audio** (`device_audio.go`, NG 1.1.x `/api/v1/audio/*`):
`POST /v1/device/audio/test` plays a built-in test chime (inline RTTTL) or,
with `{"melody":"<name>"}`, a melody stored on the clock;
`POST /v1/device/audio/stop` silences every output; `GET
/v1/device/audio/melodies` relays NG's melody list (name, RTTTL, parsed
note count and duration, validity) for the menu's melody pickers. They are
gated on the cached `capabilities.audio`: test and melodies need the buzzer,
stop needs any output, and a clock without it gets 503 `unavailable` without
a round trip. That matches what NG's `/api/v1/audio/play` answers for an
absent output; NG's melodies and stop routes never 503, so there the refusal
is Ember's own. A cold cache lets the call through so the clock decides; the
cache is emptied when the menu switches clocks (`PUT /v1/device/config`) and
when a rediscovery swap's capabilities fetch fails, so a stale entry can't
refuse on the previous clock's word. The test chime is an explicit user action,
so it plays during quiet hours; the clock's own `soundEnabled` mute still
applies. These handlers (and display power) go through `internal/awtrix`
client methods rather than the raw proxy; a client `*APIError` is relayed by
the same envelope mapping.

Sensor calibration (`GET/PUT /v1/device/sensors`) targets `tempOffset`/
`humOffset` on `/api/v1/system` — NG has no dedicated settings-API key for
them, and the old AWTRIX3 `dev.json`-on-LittleFS contract is gone entirely. The
PUT read-merges the existing `/api/v1/system` object (preserving unrelated
keys — notably `buttonCallback`, which Pomodoro buttons depend on, and the
Wi-Fi credentials the device needs to boot) and writes it back with a plain
`PUT`; NG applies system changes **live, no reboot**, unlike `dev.json`, which
only took effect at boot. The Ulanzi firmware default is `tempOffset:-9`
(self-heating compensation); an explicit `null` in a sensors PUT resets to that
default (or `0` for humidity), so the menu treats −9/0 — not 0/0 — as the
baseline.

### Dashboard read API — `cmd/ember/dashboard_http.go`, `clock_health_http.go` (#110)

Open (no token) reads for the native macOS dashboard, alongside the existing
`GET /v1/pomodoro/{stats,heatmap,workhours}`:

- **`GET /v1/usage`** — the latest `UsageStore` snapshot per tool (5h/7d windows,
  `models` keyed by model name, `stale` past `usageStaleTTL`). Before this the
  snapshot was write-only; `/state` leaked just the 5h percent.
- **`GET /v1/activity/summary?days=7`** (1..90, per-IP rate-limited) — agent
  activity from the `activity` table: `today` and `period` windows with
  `total`/`by_tool`/`by_source` rows (`active_sec`, `sessions`, `attention`;
  source rows carry `source_color`, explicit `null` when unknown, as in
  `daily_by_source`), plus zero-filled `daily` (per tool) and
  `daily_by_source` series. Active time counts **running/error rows only**
  (producers re-post an unanswered waiting marker for hours), reuses the
  work-hours span reconstruction (rows ≤ 5 min apart form a span) and unions
  spans within a group, so concurrent sessions of one tool count once.
  `attention` counts waiting episodes. `source_color` is remembered in memory
  from status posts, so it is null for a source that hasn't posted since
  restart. `recording` mirrors `work_hours_include_activity`: rows are only
  stored while it is on. Rows are throttled to one per session per 2 min,
  **except a transition into waiting**, which is written once at least 10 s
  have passed since the session's last row, so a short prompt isn't lost.
- **`GET /v1/weather/state`** — the poller's cached observation (condition,
  the provider's raw `condition_code`, `temp_c`, hourly points stamped with the
  provider's own series start), air quality, the user's `location_name` label
  and today's sunrise/sunset **rounded to 5 min** (to the second they'd pin the
  coordinates). No provider call; the coordinates are never echoed.
- **`GET /v1/clock/health`** (per-IP rate-limited) — publish counts for the last
  24 h (hourly buckets fed by `recordPublish`) and since start, the last publish,
  plus the clock's `currentApp`, `wifiRssi`, heap, uptime, `wifi.connects`,
  `matrixPower`, battery and sensors from `GET /api/v1/device`, **cached 30 s**
  and probed detached from the caller's cancellation, so polling can't add
  traffic on the clock's lossy Wi-Fi and a disconnecting viewer can't cache
  "unreachable". `latest_firmware`/`update_available` come from GitHub's
  awtrix-ng latest-release API: the server's only call to the internet for this.
  A background goroutine does the lookup (single in-flight), so the endpoint
  serves the cached answer and never waits. It runs at most every 6 h, 30 min
  after a failure (logged at Warn), fails soft to `null`, and is off with
  `EMBER_FIRMWARE_CHECK=0`.
  The clock's IP, SSID host, UID, hostname and button presses are not served.

Wire conventions (for Swift's `JSONDecoder` `.iso8601` and Swift Charts):
RFC 3339 timestamps with **whole seconds** (`.iso8601` rejects fractions),
`null` instead of zero sentinels (work hours' empty days emit
`work_start`/`work_end: null`, not `0001-01-01`), series as arrays of points,
and the unit in every key (`_sec`, `_percent`, `_c`, `_dbm`, `_bytes`,
`_ugm3`). Storage errors are logged, not returned. Handlers wrap `build*`
methods that take `now`; `TestDashboardGolden` renders them at a fixed instant
into `cmd/ember/testdata/dashboard/*.json` (`go test ./cmd/ember -run
TestDashboardGolden -update` to regenerate), and EmberKit's decode tests read
those same files. EmberKit's models are in `Sources/EmberKit/Models/`, with one
service per feed in `Sources/EmberKit/Services/`.

**The Dashboard window (#111)** is a card grid (`macos/Ember/Dashboard/`):
Clock (live mirror + next/previous/dismiss/power), Focus, Usage, Upcoming,
Agents, Last 7 days, 12 weeks, Work hours, When you focus (weekday × hour
heatmap + 12-week strip), Agent time, Clock health, Weather. 3/2/1 columns
at ≥1040/≥700 pt; a wide card waits for a half-filled row to fill. Every
card reads plain values (`DashboardData`, built from `LiveModel` by
`DashboardWindow`) and renders through `FeedStateView`, so previews and
snapshot renders use fixtures (`Dashboard/Preview/`, the goldens above plus
synthetic history). The window holds its tier-C feeds with one `.task` for as
long as it's open. Chart transforms (bucketing, zero-fill, goal line, DST-safe
day keys, wall-clock work spans, locale week order) live in
`Sources/EmberKit/Dashboard/` with unit tests. A pre-0.28 server shows
"Needs server 0.28" on the cards whose routes 404; Usage falls back to the
sessions' 5-hour percentages.

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

> **Retired toggles (2026-06 single-app display rework):** `EMBER_RATE_PCT_ENABLED`,
> `EMBER_CONTEXT_NUMBER_ENABLED`, and `EMBER_RATE_RESET` are **no-ops** — the server
> no longer reads the `rate_pct`, `context_number`, and `rate_reset` session fields.
> Producers still parse and post them (no-op at the wire level), so existing
> `producer.env` files with these keys are safe; they can be removed at any time.

## Wire protocol

- **Required identity:** `source` / `tool` / `session` / `state`. Optional
  enrichment fields (`context_pct`, `source_color`, `rate_window_pct`,
  `rate_reset_at`, `activity`, the `EMBER_*` booleans, …).
- **Strict vs forward-compat decode:** `handleStatus` (`POST /v1/status`) decodes
  **non-strict** (unknown fields ignored) so newer producers can post fields an
  older server doesn't know. `handleDeleteStatus` + `handleNotify` stay **strict**
  (reject unknown fields / trailing tokens). Every JSON handler decodes through
  `decodeOrReject` (`server.go`): a body past the 1 MB cap answers **413**, any
  other decode failure 400, both with a `request rejected` log line.
- **Auth:** bearer token on write endpoints, via `EMBER_TOKEN` env only —
  never argv/URL/logs. `slog.LogValuer` redaction throughout. **Fails closed:**
  an unset `EMBER_TOKEN` rejects every `/v1` write with 401 (same policy as the
  `/admin` surface); the token is compared in constant time, and the per-IP
  rate limiter sits *outside* auth so rejected 401s still consume budget (a
  wrong-token flood is throttled to 429). The unauthenticated device hooks
  (`/hooks/awtrix/{button,boot}`) share the same per-IP limiter.
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
| usage 5h / weekly / per-model | `api/oauth/usage` (Keychain, always-on) | rollout `rate_limits.primary`+`secondary` (session-only) | drives the usage card inside the main `ember` app (threshold-gated) |

### AI usage card (threshold-gated, single app)

Account-global subscription usage renders inside the main `ember` app as a
**usage card** in the number-slot rotation — no standalone apps. The flow:
producers `POST /v1/usage` → in-memory `UsageStore` (per tool; **not persisted**
— every entry refreshes ≤5 min so a restart self-heals) → the coordinator
builds `UsageView` structs from `effectiveFiveHour` each tick and includes a
usage card for a tool **only when its 5h window ≥ `usage_threshold_pct`**
(default 60; `0` = always show). The usage card rotates through up to five faces per
tool (sessions-bar mode): **5h clock** (fully-drawn tight-colon), **reset**
(HH:MM reset clock), **7d** (percent in threshold colour, via
`drawUnitPctFace`), **model-A** and **model-B** (`OP`/`SO` weekly frames).
Every usage face drops the context glass, which is a session metric that only
non-usage cards draw. Percentage faces show a gray **window unit label** in its
place (`drawUsageUnit`, cols 25–31): `5h` on the 5h pct face and the hourglass
fallback, `7d` / `OP` / `SO` on the weekly faces. HH:MM reset-clock faces show a
gray hourglass at cols 27–29 instead: a bare HH:MM reads as the time of day next
to NG's Time app, and a `5h` one column after the clock (which ends at col 23)
read as part of it. Per-tool show/hide reuses `/v1/apps`; the widget + per-model
toggles remain server config (`usage_widget`, `usage_per_model`, default on);
`usage_threshold_pct` is also server config (`GET/PUT /v1/usage/config`, store
key `usage_json`, default 60, 0 = always). **Claude 5h fallback:** when the
authoritative endpoint usage is stale/absent (idle daemon, 401), the
coordinator synthesises a 5h face from the live session's statusline
`rate_window_pct` + a host-local `rate_reset_label` (the statusline producer
formats the label on the Mac and posts it on the marker, so the UTC container
renders it verbatim — no server-side timezone math). The endpoint supersedes
the fallback the moment fresh usage arrives (and only then are 7d + per-model
shown). On startup the coordinator **clears any legacy `ember-usage-*` apps**
left on the device from the previous standalone model.

**Idle usage frame.** When all sessions expire and a tool is over threshold,
the coordinator publishes a **dimmed usage frame** (the same usage card content
at ~40% brightness) during the `DIMMED` phase instead of going dark
immediately. Under threshold the app leaves the device rotation normally.

**5h limit-reset alarm.** A small per-tool state machine in the coordinator
(`usage_alarm.go`, checked each tick): when the effective 5h window — fresh
endpoint usage, else the live-session statusline fallback — reads **≥ 99.5 %
with a known future reset**, it arms for that `resets_at`; once the reset
passes (+60 s grace, never early) it fires **one** auto-dismiss notification
(`CLAUDE 5H RESET` / `CODEX 5H RESET`, drawn tool icon) plus an RTTTL chime.
Drifted reset estimates re-arm instead of firing; an unreachable device retries
next tick (armed state preserved); fired alarms dedupe per `(tool, resets_at)`.
State is in-memory by design — a restart mid-window re-arms from the next
snapshot. Gated only by `limit_alarm` (usage config, default on); deliberately
independent of the usage card threshold (the alarm is about resuming work, not
tiles).

**Quiet hours.** A global night mute (`quiet_hours` config: `enabled`,
`start`/`end` `"HH:MM"`, default off / 22:00–08:00; runtime override via
`GET/PUT /v1/quiet/config`, store key `quiet_json`). Enforced by a
`quietPublisher` decorator around the device publisher — during the window
(server-local wall clock; overnight wrap supported; `start == end` = never)
Notify payloads lose their `sound`/`soundRtttl`/`soundLoop` keys (NG's three
notification sound fields; AWTRIX3 spelled the latter two `rtttl`/`loopSound`)
and `PlayRTTTL` no-ops, so every sound source is covered at one choke point.
Visual output is untouched — an attention hold still takes the screen at
night, just silently — and sounds resume on the first event after the window.

## Display layout (32×8 matrix)

Each metric owns a screen region as a **graphic**; numeric readouts are opt-in
and disambiguated by a pictogram (graphics-first). Icon-left language throughout
(redesign 2026-06-06).

**Column grid.** Every app uses one grid, defined once in
`internal/render/layout.go` and copied from awtrix-ng's own layout for an app
with an 8px icon, so nothing jumps sideways as the device rotates between apps:

| Cols | Rows | Element | Const |
|---|---|---|---|
| 0–7 | 0–7 | icon (drawn 8×8 sprite or native `icon`) | `iconW` |
| 8 | 0–6 | icon gap: blank. Drawn icons on text payloads are sent as a 9-wide op (`iconOp`) whose col 8 is zeros, so scrolling native text disappears at col 9 instead of touching the icon | `iconOpW` |
| 9–24 | 1–5 | content: 3×5 digits and native text (centred in 9–31 for weather/air/Pomodoro, left-aligned at 9 for agent cards) | `contentX`, `textRow` |
| 25–31 | 1–5 | right slot: context glass or usage unit label | `rightSlotX` |
| — | 6 | blank spacer | |
| 8–31 | 7 | bottom bar: session bar, rate bar, usage bar, weather/AQI strip, Pomodoro progress (NG's native progress also starts at x=8 under an icon) | `barX0`, `barW`, `barRow` |

`TestEveryBottomBarStartsAtBarX0` pins every app's row-7 bar to `barX0`.
Hourly data (weather and AQI strips, forecast bars) share one rule, `hourSlot`:
hour *i* of an N-hour window owns `24/N` whole columns from col 8. Windows that
divide 24 fill the bar; others (22 h) leave an even dark tail on the right
rather than doubling some hours.

- **8×8 tool icon** — cols 0–7. Body painted in the session's **source colour**
  (`EMBER_SOURCE_COLOR` / `source_color` wire field; neutral `#CCCCCC` fallback
  when absent or invalid), so each machine has a persistent identity colour.
  State is shown by the inner feature: Claude **eye sockets** / Codex **`_`
  cursor** painted in the state colour (green=run, amber=wait, red=err,
  blue=done). Idle dim frame: body drops to ~40% gray; eye sockets / cursor stay
  dark, preserving the silhouette. Shares the usage card sprites
  (Claude robot-face / Codex chevron) via `drawToolIcon8`.
- **Number slot** — cols 9–24 (`contentX=9`), a **rotating set of cards**:
  **source-name card** (source uppercased, cut to 15 px using the AWTRIX
  panel font's real ink widths — M/W 5, N/Q 4, I 1, non-ASCII counted as 5 —
  so it never runs under the glass; tinted in the
  source colour or white), **usage card** (when 5h ≥ `usage_threshold_pct`:
  5h clock → reset clock → 7d → per-model faces, rotating), context `NN⌷`,
  and the scrolling tool/trail card. The **source card's name is
  firmware-rendered**, not drawn (`Frame.Native` → `text`/`textOffsetX`): the
  in-house 3×5 font has no room for a real M/N/W. `/v1/preview` approximates it
  back in `font3x5` so the Settings mirror doesn't show an empty slot — the only
  place device and preview differ by design, and only in letterform. Wire fields `source_card` / `session_bar`
  are `*bool` (absent = on; a producer that predates them never regresses the
  display).
- **Context glass** — right edge, cols 25–31 (interior 26–30 × rows 1–4), so it
  owns the panel's last column. 20-level per-pixel bottom-up fill (5 % per
  pixel), state-coloured; the topmost partial row fills left-to-right. Non-usage
  cards only — usage faces paint the gray window unit label
  (`5h`/`7d`/`OP`/`SO`) in this slot instead.
- **Bottom row (row 7, cols 8–31)** — three-way (`drawBottomBar`): the 5h
  rate bar (`drawRateBar`, when `rate_bottom_bar` on + rate present), styled as
  the **dimmed (~55%) threshold bar**; else the session-pixel bar (1 px per
  non-idle session from col 8, priority-sorted, when `session_bar` on); else
  off. Every card of the app carries it, including the scrolling tool card and
  the locked attention card (as a 24×1 row-7 op), so row 7 does not blink as
  the cards rotate.
- **Usage colours** — usage percentages (digits, bars, reset urgency) use one
  threshold palette (`usageThreshold`: green <70, amber 70–89, red ≥90), kept
  apart from the agent-state colours. HH:MM reset-clock faces carry a gray
  hourglass at cols 27–29 rather than `5h`: the clock ends at col 23, and a
  `5h` at col 25 read as `17:305h`.
- **Locked attention view** — 8×8 tool icon in cols 0–7, firmware-native
  blinking text `WAIT <SOURCE>` / `ERR <SOURCE>` at `textOffsetX:9` (with
  `textCenter:false` — see the gotcha below); scrolls when the label overflows
  the 23 free columns. Activity detail no longer substitutes here — the label
  always names which agent/computer needs attention.
- **Pomodoro view** — NG **built-in animated icon** (`icon` field: tomato
  `29802` focus / coffee `6396` break) + native MM:SS countdown + native progress
  bar; paused dims the phase colour and fades the countdown (`textFadeMs`),
  since the animated icon stays at full brightness. (Not a drawn bitmap; the
  drawn `RenderPomodoro` backs `GET /v1/pomodoro/preview` and copies the device
  layout: mug for both breaks, time centred in cols 9–31, progress from col 8.)

## Gotchas & constraints (hard-won)

### awtrix-ng firmware (verified on 1.0.13)
- **No multi-frame `draw` arrays.** A 2-frame pulse payload triggers a
  validation error on the device. Use firmware-native `textBlinkMs` instead.
  Several *bitmap ops* in one `draw` array are fine — that is not an animation.
- **`draw` ops paint over the text, zeros included.** NG's `textInFront`
  defaults to `false`: text is drawn first and decorations (`draw` ops, then
  progress, then charts) on top. Bitmap zeros are opaque black, so a
  `["bitmap",0,0,32,8,…]` op plus `text` shows only the bitmap: the text is
  drawn and then painted over (seen on 1.0.15, and the reason the old notes
  said a full-panel op "suppresses" text). Ops that leave the text box (rows
  1–5) clear let the text show, with a row-7 op under it unaffected. This is
  why the source card emits three ops (`drawOpsAround`) instead of one
  full-frame bitmap, why `detailPayload` sends only the icon op and a row-7 bar
  op, and why a drawn icon's op is 9 wide (`iconOp`): without a native icon,
  NG scrolls text across all 32 columns, and the blank col 8 keeps it out of
  the gap.
- **`textInFront` is deliberately not used (#109).** The docs are clear on
  z-order only: with `true` the text is painted over the decorations. That
  would let the source card send one full-frame bitmap, but the three ops do
  two jobs a single op can't. They are a clip mask: NG's font is variable
  width and `sourceCardText` only estimates it, so a name that overruns
  col 24 is cut by the right-hand op today, and would paint over the context
  glass with the text in front. On scrolling cards (tool, attention, popups)
  text in front would run over the drawn icon, which the 9-wide `iconOp` now
  masks. And the single op is larger: +200 B on a running source card (951 →
  1151 B, measured), on a link that already loses pushes. The docs also don't
  say whether the text layer paints only lit glyph pixels or its whole box
  (the `textBlinkMs` note says off-phase glyphs are painted black), which
  only a device test can settle.
- **Text payloads inherit casing and scroll from the clock's globals.**
  `textCase` defaults to `inherit` (the global `uppercase`, on by default) and
  every `scroll` field inherits one by one from the global `scroll`. The
  previews draw only uppercase, so every payload carrying free text (agent
  cards pin `scroll` only; reminders, meetings, `/v1/notify` also pin
  `textCase:"upper"`, via `pinText`) sets both explicitly, and the device
  matches the preview whatever the user set in the web UI. `/v1/notify` has no
  preview, so its caller may override the case with `text_case`
  (`inherit`/`upper`/`asTyped`, validated; anything else is a 400).
- **NG's font is 3px wide + 1px spacing, variable for wide letters.** "STUD"
  lands exactly in cols 9–23; "M" is 5 wide. This is what the source card buys
  by handing its text to the firmware: the in-house `font3x5` cannot form an
  M/N/W in three columns.
- **`textOffsetX` stacks on top of centering** — carried over from AWTRIX3
  under new key names. Custom apps default `textCenter:true`, and the firmware
  *adds* `textOffsetX` to the centred position → text clips past col 31. Set
  `textCenter:false` to make `textOffsetX` the literal start column. A drawn
  `bitmap` indents nothing on its own; only a **native `icon`** reserves the
  left 9px — with a native icon present, text auto-centers in the remaining
  region instead.
- **No per-payload priority.** AWTRIX3's `prio:true`/`force:true`/
  `duration=lifetime` combination 422s on NG outright. The only levers are a
  forced `PUT /api/v1/apps/active` (device-level, not payload) plus the app's
  own `durationMs`/`lifetimeMs` — see "Display hold" above. `lifetimeExpiry`
  defaults to `"remove"`: at `lifetimeMs` the app is **deleted**, not merely
  hidden, which is what keeps Ember's idle model crash-safe. Re-pushing the
  same app is idempotent (blink phase and dwell survive a re-push unaffected),
  so the coordinator's dedupe is a network/CPU nicety, not a correctness
  requirement.
- **Pushed apps are RAM-only.** A device reboot drops every app Ember pushed
  and the `autoTransition`/`blockNavigation` settings pair, even though the
  coordinator's own bookkeeping doesn't know that happened until the next
  device-watch probe (or boot-ping, #73) triggers a republish.
- **Device button input** comes via a plain HTTP POST per press
  (JSON `{"button":"left|middle|right","state":bool,"uid"}` on NG ≥1.1.1, the
  form `button=…&state=1|0&uid` before it — both accepted; `select` accepted as an alias for
  `middle`, ~300ms budget) to whatever URL `buttonCallback` (`/api/v1/system`)
  names; the device can't attach a token, hence the unauthenticated hook. NG
  documents — and Ember has verified — that a configured `buttonCallback` does
  **not** consume the press: the buttons keep their normal firmware job, and a
  `select`/`middle` press dismisses the showing notification even under
  `blockNavigation:true` (the AWTRIX3-era "won't self-dismiss while a callback
  is configured" gotcha is **false** on NG). Ember still dismisses its own
  `ember-reminder` popup by name as belt-and-braces (a 404 there is fine — the
  firmware likely already cleared it). The AWTRIX3 left+right chord is
  removed (#81): left=stop, right=skip, middle=start/pause/resume, all on
  press only.
- **Verify on-device** by reading `GET /api/v1/display/screen`, which wraps
  the framebuffer as `{"width":32,"height":8,"pixels":[256 ints]}` (AWTRIX3
  returned the bare 256-int array — consumers must unwrap the new shape) — see
  RUNBOOK for the ANSI-render + crafted-session technique.
- **The gamma/brightness hue-shift analysis that used to live here was derived
  from AWTRIX3 source and has not been re-derived for awtrix-ng** — deleted
  rather than carried forward unverified. If NG exhibits the same brightness-
  dependent hue shift, re-derive and re-document it against NG's own gamma
  code before relying on it.

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
- **An SMAppService agent can be "enabled" and not running.**
  `SMAppService.status` reads the Background Items database; after
  `launchctl bootout` of an app-registered job, launchd drops it for good but
  the status stays `.enabled` until the next login. The CLI producers share
  the app's labels, so they must never boot out a job whose `launchctl print`
  shows `managed_by = com.apple.xpc.ServiceManagement` (a test calling the
  real `uninstall` did, #142). The app probes `launchctl print` at launch and
  in Settings › Agents and re-registers a missing job. `CFBundleVersion` was
  "1" for every release, so update detection keys on a digest of the bundled
  helpers too.
- **Syntactically-valid-but-wrong config defeats validation.** A
  `EMBER_SERVER_URL` typo (`:800` for `:3627`) passed the URL validator but
  dropped every POST. When "nothing shows," check the producer→server path first:
  env URL, token match, `/state` contents. Reject semantic sentinels (e.g. a `0`
  context window) and force the explicit blank instead.
- **Pure-Go SQLite keeps the distroless static build** (`CGO_ENABLED=0`). Use a
  Docker **named volume** for the writable DB as nonroot; open WAL +
  `SetMaxOpenConns(1)`; `Close()` on shutdown to checkpoint the WAL — only
  after the background workers have stopped (`App.shutdown`), or a last
  `pomoTick` writes to a closed DB.
- **`/admin/reload` reverts runtime-persisted settings** unless the feature
  re-applies them after the config `Store` (Pomodoro durations live in SQLite,
  not the file).
- **Building inside a git *worktree* hides the VCS revision** from Docker
  `buildvcs` (the worktree `.git` is a file) → `version: unknown`. Build from a
  normal checkout / CI.

## Conventions

- **Stdlib-first, but deps that earn their slot are welcome** (STYLE.md §11).
  The Go server's third-party deps are `modernc.org/sqlite` (pure-Go SQLite,
  keeps CGO off), `brutella/dnssd` (mDNS), `arran4/golang-ical` (ICS
  tokenising), and `teambition/rrule-go` (RRULE/DST-correct recurrence). The
  `fyne.io/systray` + `progrium/darwinkit` menu deps were dropped when the menu
  became a native SwiftUI app (`macos/`). Hand-rolling protocol clients (the
  deleted MQTT 3.1.1 client) was not worth the purity tax.
- **Decompose "build feature X" into A/B/C sub-projects** with their own
  spec → plan → implementation cycle when the work hides interface contracts;
  keep them loosely coupled through a single locked protocol.
- See [`STYLE.md`](STYLE.md) for the full coding guide.
