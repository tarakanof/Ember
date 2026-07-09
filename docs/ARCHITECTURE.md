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
- **Display hold.** Reserved for attention: only the **locked** waiting/error
  frame (and the idle hot-usage frame) is published with `prio:true` +
  `force:true` + `duration=lifetime`, snapping the clock to it and holding it
  for the attention window. Merely-running frames carry no `prio`/`force` and a
  short `duration` (6 s, same as the weather/forecast tiles) so an active agent
  rotates alongside the other apps instead of owning the screen. Every frame
  still has a per-frame `lifetime`, so the clock **crash-safely** returns to
  native apps if the server dies (vs the sticky, reboot-requiring
  `/api/settings` primitive — deliberately not used).

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

**Icon provisioning** (`ensureWeatherIcons`): the device's own on-demand
gallery downloads proved unreliable (observed failing for hours → iconless
tile), so the **server provisions icons**: on startup and on every weather
config apply it lists the clock's `/ICONS` folder (`GET /list?dir=/ICONS`),
downloads any missing configured icon ID from the LaMetric gallery
(`.gif`→`.jpg` fallback), and uploads it (`multipart POST /edit`,
`Publisher.ListIcons`/`PutIcon`). List failures abort the run; per-icon
failures log and retry on the next apply/restart.

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
(`UID|start` key), with a 2-minute grace window covering a missed tick. The chime
is played separately via `POST /api/rtttl`: the AWTRIX firmware silently drops a
notification's own `sound`/`rtttl` when the payload also carries a `draw`/`icon`,
so the chime must be a standalone request — the same pattern used by
the severe-weather and 5h-reset alarms. Quiet hours mute the chime (audio only —
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

Sensor calibration (`GET/PUT /v1/device/sensors`) follows the same proxy rule
but targets `dev.json` on the clock's LittleFS, since the firmware has no
settings-API key for `temp_offset`/`hum_offset`. The PUT read-merges the
existing file (preserving unrelated keys — notably `button_callback`, which
Pomodoro buttons depend on), uploads it through the device's multipart `/edit`
route (the icon-provisioning mechanism), and reboots the clock because dev.json
is only read at boot. The Ulanzi firmware default is `temp_offset:-9`
(self-heating compensation); a dev.json value replaces that default, so the
menu treats −9/0 — not 0/0 — as the baseline.

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
at the right edge (`drawUsageUnit`, cols 25–31): `5h` on the clock/reset/pct
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
Notify payloads lose their `sound`/`rtttl` keys and `PlayRTTTL`/`PlaySound`
no-op, so every sound source is covered at one choke point. Visual output is
untouched; sounds resume on the first event after the window.

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
  and the scrolling tool/trail card. Wire fields `source_card` / `session_bar`
  are `*bool` (absent = on; a producer that predates them never regresses the
  display).
- **Context glass** — right edge (interior cols ~26–29 × rows 1–4), 16-level
  per-pixel bottom-up fill, state-coloured. Non-usage cards only — usage faces
  paint the gray window unit label (`5h`/`7d`/`OP`/`SO`) in this slot instead.
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
- **Hue shifts with brightness — the `GAMMA` setting is ignored.** The
  firmware's `gammaCorrection()` computes gamma from the current brightness:
  `logMap(actualBri, 2, 180, 0.535, 2.3, 1.9)`. At the auto-brightness floor
  (`min_brightness` default **2**, i.e. any dark room) gamma ≈ 0.535, which
  *boosts* mid-range channels: orange `#FF7F00` (G=50%) displays as ≈`#FFB000`
  — yellow. At bright daylight (BRI→180) gamma → 2.3 and the same orange goes
  deep red-orange. No payload colour fixes this; raise `min_brightness` in the
  device's `dev.json` (~20–40) to keep night-time gamma near neutral.

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
