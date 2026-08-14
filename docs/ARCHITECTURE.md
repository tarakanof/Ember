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
  on fresh lock acquisition (`attention_chime`, via `POST /api/v1/sounds/play`), the
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
    link degrading before it starts costing frames.
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
  Reserved for attention: only the **locked** waiting/error frame (and the idle
  hot-usage frame) triggers a forced `PUT /api/v1/apps/active` on the hold
  edge, and the app's own `durationMs == lifetimeMs` then sustains it for the
  attention window (switching happens strictly **after** a successful push —
  `apps/active` 404s on an app the device doesn't know yet). Merely-running
  frames ask for no forced switch and a short `durationMs` (6 s, same as the
  weather/forecast tiles) so an active agent rotates alongside the other apps
  instead of owning the screen. `autoTransition:false` outranks any per-app
  dwell entirely and is reserved for the Pomodoro takeover — hold precedence is
  `holdPomodoro > holdAttention > holdNone` (`cmd/ember/coordinator.go`). Every
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
  code: Codable models mirroring the wire shapes, `APIClient`, Status/Pomodoro/
  Preview services, `pickWinning`, `EnvFile` + validation, the settings types,
  and the `@Observable AppModel` + poller. Headless `swift test`.
- **`Ember` (`macos/Ember/`, thin Xcode app)** — an `LSUIElement` agent
  app: a `MenuBarExtra` (status + Pomodoro controls + dynamic tray glyph), a
  sidebar `Settings` window (**Connection / Device / Agent / Pomodoro / Weather /
  Reminders / App**),
  and a status + preview **dashboard** `Window`. App-only prefs (icon palette,
  tray glyphs) live in `UserDefaults`; launch-at-login is `SMAppService`.

This replaced the retired Go menu (`fyne.io/systray` + DarwinKit). The Agent tab's
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
plus a forced `PUT /api/v1/apps/active` on start, and restoring both settings
on stop. Because a **device reboot** drops pushed apps and both settings while
the coordinator's `hold` flag stays set, recovery arrives via the shared
device-watch/boot-ping republish path (see "Device discovery & control" below)
rather than a Pomodoro-specific re-assert loop: `RepublishAll` resets `hold` to
force a fresh edge, which re-applies the takeover settings and the forced
switch. The
cycle **auto-advances** by default (`auto_start_next: true`) and **auto-stops**
after a wall-clock budget (`max_session_minutes`, default 480 = 8h, `0` = off) so
it never runs overnight; focus is configurable up to 8h. Stats persist in pure-Go
SQLite (`modernc.org/sqlite`, no CGO → distroless build intact). Runtime config
edits persist to the SQLite store (key `settings_json`, re-applied over the
**read-only** bind-mounted `config.json` baseline at boot) — so the menu can
change durations/colours/cap without a writable config file. API:
`POST /v1/pomodoro/{start,pause,resume,stop,skip}` + `GET/PUT /v1/pomodoro/config`
(bearer; PUT is **merge semantics** since #84 — omitted fields keep their
current value); open `GET /v1/pomodoro/{state,stats}`; **unauthenticated**
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
  in the free area + a 2-px-tall per-hour **forecast strip** (rows 6–7),
  coloured by a cold→warm temperature gradient (`render.TempColor`). On a
  **clear night** the icon becomes the current **moon phase** (`moon_phase`;
  phase computed locally in `cmd/ember/astro.go`, no API).
- **`ember-forecast`** (`forecast_tile`, default on) — **full-width hourly
  temperature bars** (no icon/temp — those live on the conditions tile, so the
  two tiles read differently at a glance); bars are stretched evenly across
  all 32 columns (`forecast_hours`, 6..24; bar height + colour = temperature).
- **`ember-air`** (`air_tile`, default on) — **air quality**: 8×8 drawn wind
  icon + the current **European AQI** value, both in the official EEA bucket
  colour (good→extreme; `render.AQIColor`/`AQIWord`, discrete — the scale is
  bucketed), + a 2-px per-hour **AQI trend strip** (rows 6–7, next 24 h, each
  pixel its own bucket colour). Data comes from the **Open-Meteo air-quality
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
name lists, which the macOS Device tab uses to populate its transition picker
instead of guessing at a static enum. `/admin/doctor` reports the cached
counts as its `capabilities` check.

**App ordering** (`GET/PUT /v1/device/apps`) proxies `GET /api/v1/apps` /
`PUT /api/v1/apps/order` — ordering plus enable/disable of the device's own
apps, replacing the AWTRIX3 settings keys `TIM`/`DAT`/`TEMP`/`HUM`/`BAT` that
NG has no equivalent for (name only what you want to change). The ambient
weather overlay is a separate concern, `PATCH /api/v1/display` via
`GET/PUT /v1/device/display` — not part of app ordering. The device's own
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
to detect a reboot (uptime going backwards, or the device answering again
after a gap) and triggers `RepublishAll` — pushed apps are RAM-only on
awtrix-ng, so a reboot silently drops every app the coordinator believes is
still on the device, and this is what pushes them all back. The 30s interval
was chosen to match the old Pomodoro-only 30s re-assert loop it replaced, so
worst-case recovery latency didn't regress; the Berry boot-ping hook (#73)
(`POST /hooks/awtrix/boot`, an unauthenticated device-side hook, config toggle
`awtrix.boot_ping`) calls `RepublishAll` directly on boot instead of waiting
for the next tick, making recovery near-instant with the 30s watch as
fallback. Swaps are **in-memory
only** — `config.json` and the writable store are never rewritten, so a
config/store edit still takes effect the next time its source URL goes
unreachable. The whole probe loop is gated by `awtrix.auto_rediscover` (config,
default on; `/admin/doctor`'s `clock` check reports the source, reachability,
and last re-discovery time/result). The server also advertises
itself as `_ember._tcp` so the menu app can discover it (gated by
`EMBER_MDNS_ADVERTISE`). Both directions require host/macvlan networking.

The menu's Device tab manages the clock's *own* firmware settings — but **the
server stays the only writer to the device**: the tab calls `/v1/device/settings`
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
  (reject unknown fields / trailing tokens, 413 via `http.MaxBytesReader`).
- **Auth:** bearer token on write endpoints, via `EMBER_TOKEN` env only —
  never argv/URL/logs. `slog.LogValuer` redaction throughout. **Fails closed:**
  an unset `EMBER_TOKEN` rejects every `/v1` write with 401 (same policy as the
  `/admin` surface); the token is compared in constant time, and the per-IP
  rate limiter sits *outside* auth so rejected 401s still consume budget (a
  wrong-token flood is throttled to 429).
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
Every usage face replaces the context glass with a gray **window unit label**
at the right edge (`drawUsageUnit`, cols 25–31, the same span the glass now
takes): `5h` on the clock/reset/pct
faces, `7d` / `OP` / `SO` on the weekly faces — the glass is a session metric
and only non-usage cards draw it. Per-tool show/hide reuses `/v1/apps`; the widget + per-model
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

- **8×8 tool icon** — cols 0–7. Body painted in the session's **source colour**
  (`EMBER_SOURCE_COLOR` / `source_color` wire field; neutral `#CCCCCC` fallback
  when absent or invalid), so each machine has a persistent identity colour.
  State is shown by the inner feature: Claude **eye sockets** / Codex **`_`
  cursor** painted in the state colour (green=run, amber=wait, red=err,
  blue=done). Idle dim frame: body drops to ~40% gray; eye sockets / cursor stay
  dark, preserving the silhouette. Shares the usage card sprites
  (Claude robot-face / Codex chevron) via `drawToolIcon8`.
- **Number slot** — cols 9–24 (`numStart=9`), a **rotating set of cards**:
  **source-name card** (source uppercased, truncated to 4 glyphs, tinted in the
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
- **Bottom row (row 7)** — three-way: the 5h rate bar (`drawRateBar`, when
  `rate_bottom_bar` on + rate present), styled as the **dimmed (~55%) threshold
  bar** over content cols 8–31; else the session-pixel bar (1 px per non-idle
  session, priority-sorted, when `session_bar` on); else off.
- **Locked attention view** — 8×8 tool icon in cols 0–7, firmware-native
  blinking text `WAIT <SOURCE>` / `ERR <SOURCE>` at `textOffsetX:9` (with
  `textCenter:false` — see the gotcha below); scrolls when the label overflows
  the 23 free columns. Activity detail no longer substitutes here — the label
  always names which agent/computer needs attention.
- **Pomodoro view** — NG **built-in animated icon** (`icon` field: tomato
  `29802` focus / coffee `6396` break) + native MM:SS countdown + native progress
  bar; paused dims the phase colour. (Not a drawn bitmap; the drawn
  `RenderPomodoro` is retained for tests and the `GET /v1/pomodoro/preview`
  endpoint.)

## Gotchas & constraints (hard-won)

### awtrix-ng firmware (verified on 1.0.13)
- **No multi-frame `draw` arrays.** A 2-frame pulse payload triggers a
  validation error on the device. Use firmware-native `blinkText` instead.
  Several *bitmap ops* in one `draw` array are fine — that is not an animation.
- **A full-panel `draw` op suppresses the text layer entirely.** Verified on
  1.0.15: a payload with `["bitmap",0,0,32,8,…]` plus `text` renders the bitmap
  and simply drops the text — no error, no pixels. Splitting the same pixels
  into ops that leave the text box clear makes the text appear, with a bar-row
  op underneath it unaffected (NG's text occupies rows 1–5). This is why the
  source card emits three ops (`drawOpsAround`) instead of one full-frame
  bitmap, and why `detailPayload` gets away with a single 8×8 icon op.
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
  (`button=left|middle|right&state=1|0&uid`, `select` accepted as an alias for
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
