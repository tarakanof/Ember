# API quick reference

The contract (request/response shapes, auth per route) is
[`openapi.yaml`](openapi.yaml); operations marked `x-internal` serve Ember.app
and may change.

**Tokens.** `EMBER_TOKEN` (master) passes every bearer route below. Scoped
client tokens (`ekc_…`) and knob tokens are minted only by the master token with `POST /v1/devices
{"kind":"client","name","scopes"}` (at most 64; rotate/delete also master-only)
and pass only their scopes: `ingest` (`POST`/`DELETE /v1/status`,
`POST /v1/usage`, `POST /v1/notify`, `POST /v1/reminders/fire`), `control`
(`POST /v1/pomodoro/{start,pause,resume,stop,skip}`), `read` (the non-secret
settings GETs: apps, pomodoro/usage/display/brightness/quiet config,
`/v1/clock/stats`), `admin` (every bearer `/v1/` route except client-token
management). Wrong scope is 403. An optional `"sources":[…]` at mint (1-16,
each ≤64 chars, needs `ingest`; 400 with `admin`, which could still `POST /v1/clear`) binds the token: `POST`/`DELETE
/v1/status` and `POST /v1/usage` naming another `source` (usage: or none) are
403; without it any source passes. Usage stays keyed by tool, so a bound
token's usage post is also 403 when that tool's stored entry came from a
source outside its list (no entry yet: accepted). Sessions are keyed
`source/tool/session`, so a bound token's status `tool`/`session` and a minted
source may not contain `/` (400). Client tokens never pass the knob-only
routes, now-playing control or `/admin/*`. Details: RUNBOOK "Integrating a
source".

Server routes, grouped by auth. Behavior and wire shapes: [`ARCHITECTURE.md`](ARCHITECTURE.md) "Wire protocol". Setup and toggles: [`RUNBOOK.md`](RUNBOOK.md).

Write (bearer auth): `POST /v1/status`, `DELETE /v1/status`, `POST /v1/clear`,
`POST /v1/notify`, `POST /v1/pomodoro/{start,pause,resume,stop,skip}` and
`POST /v1/nowplaying/control` (also accept a knob device token; control takes an
optional `Idempotency-Key`), `GET/POST /v1/devices`, `GET/PUT
/v1/devices/{id}/config` (same merge), `PATCH`/`DELETE /v1/devices/{id}`,
`POST /v1/devices/{id}/rotate` (the knob registry — see ARCHITECTURE "Device
registry"), `GET /v1/devices/{id}/stats?range=15m|1h|24h`,
`GET`/`PUT /v1/devices/{id}/ota` (knob firmware update: `{"mode":"manual|auto","target":"0.9.14"|null,"retry":true}` merge;
409 `no_rollback_bootloader`, or `ota_in_progress` while installing, restarting or verifying), `POST /v1/firmware?channel=release|test`
(raw cinder `.bin` ≤4 MiB: 201 stored, 200 same bytes, 409 other bytes,
400 `bad_image|wrong_chip|wrong_project|bad_version|dev_seed_build`; the
version a `?replace=1` upload stores, and each version retention evicts, is
dropped from every knob's OTA `blocked` list; 201 once the new bytes are in
place, even if removing the old copy or an evicted version failed, which the
server logs),
`GET /v1/firmware` (newest first), `PATCH`/`DELETE /v1/firmware/{version}`
(channel; 409 while targeted, indexed or not; DELETE removes the version's
directory and any `.old-<version>-*` copy a replace left, also when the store
does not index it (404); once they are gone it drops the version from every
knob's OTA `blocked` list, which the OTA `available` field skips; a removal
that fails answers 500, keeps the block and keeps the version listed so the
DELETE can be retried), `PUT`/`GET /v1/firmware/{version}/elf`
(≤64 MiB, 400 `elf_mismatch`), `GET /v1/firmware/{version}/bin`,
`GET /v1/firmware/by-build/{build}/elf` (ARCHITECTURE "Knob firmware
updates"),
`POST /v1/devices/{id}/stats/live` (knob diagnostics, memory only — see
ARCHITECTURE "Knob diagnostics"), `GET /v1/devices/{id}/coredumps`
(stored knob core dumps, newest first) and `GET`/`DELETE
/v1/devices/{id}/coredumps/{dump}` (`application/octet-stream`,
`Content-Disposition: attachment; filename="<device>-<fw>-<dump>.bin"`; see
ARCHITECTURE "Knob checkin", RUNBOOK "Decoding a knob core dump"), `GET /v1/clock/stats?range=15m|1h|24h`
(clock probe history, memory only — ARCHITECTURE "Clock stats"),
`GET/PUT /v1/pomodoro/config`, `GET/PUT /v1/apps` (per-tool clock visibility),
`POST /v1/usage`, `GET/PUT /v1/usage/config`, `POST /v1/nowplaying` (a
source's player report), `GET /v1/nowplaying/commands?player=&wait=` (Ember.app's
long-poll for Music commands) and `PUT /v1/nowplaying/art?source=&player=&kind=album|artist&track_id=`
(raw JPEG/PNG ≤2 MB; 409 once the track moved on — ARCHITECTURE "Now playing"), `GET/PUT /v1/display/config`,
`GET/PUT /v1/weather/config`, `POST /v1/reminders/fire` (optional
`Idempotency-Key` header dedupes retries for 10 min),
`GET/PUT /v1/meetings/config`, `GET/PUT /v1/quiet/config`, `GET/PUT /v1/brightness/config` (every `…/config`
settings PUT above is merge semantics: the body is a JSON object whose omitted
keys keep their current value; an invalid merged result is a 400 and changes
nothing — see `settings_overlay.go`), `GET/PUT /v1/device/config`
(`{"base_url"}`, the same merge: `{}` changes nothing, an empty or non-http(s)
URL is a 400; GET answers the effective URL and its `source` — see
`clock_url.go`),
`GET /v1/device/discover`, `GET/PUT /v1/device/settings` (whitelisted
`PATCH /api/v1/settings` keys — see `device_settings.go`; during a Pomodoro
takeover `autoTransition`/`blockNavigation` read and write the saved prior,
flagged by an `X-Ember-Deferred-Keys` response header),
`GET/PUT /v1/device/display` (overlay, `PATCH /api/v1/display`),
`PUT /v1/device/display/power` (`{"power":bool}` — blanks/relights the
matrix, runtime-only), `POST /v1/device/audio/test` (built-in chime, or
`{"melody":"<name>"}` to preview a stored one), `POST /v1/device/audio/stop`,
`GET /v1/device/audio/melodies` (NG's melody list; the audio routes answer
503 `unavailable` when cached capabilities show no buzzer / no output),
`GET/PUT /v1/device/apps` (ordering + enable/disable,
`PUT /api/v1/apps/order`), `GET/PUT /v1/device/sensors` (system
`tempOffset`/`humOffset` via read-merge-PUT of `/api/v1/system`; applies live,
no reboot), `GET /v1/device/buttons`, `PUT /v1/device/buttons` (read-merge-PUT
of `/api/v1/system.buttonCallback`),
`GET /v1/device/stats`, `GET /v1/device/screen` (proxies
`GET /api/v1/display/screen`, raw `{width,height,pixels}`),
`GET /v1/device/capabilities` (cached `GET /api/v1/capabilities` — the firmware's
effect/transition/overlay/palette lists; live proxy when the cache is cold),
`POST /v1/device/{reboot,notify/dismiss,app/next,app/previous}`. Read (no auth):
`GET /state`, `GET /healthz`, `GET /v1/display/brightness` (clock lux, else sun schedule), `GET /v1/preview`,
`GET /v1/{weather,pomodoro,reminders}/preview`, `GET /v1/meetings/{preview,state}`,
`GET /v1/pomodoro/{state,stats,heatmap,workhours}`,
`GET /v1/pomodoro/dashboard` (HTML), and the native-dashboard reads
(`dashboard_http.go`, `clock_health_http.go`): `GET /v1/usage` (latest usage
snapshot per tool — the POST stays authed), `GET /v1/activity/summary?days=`
(agent time per tool/source, waiting excluded), `GET /v1/weather/state` (cached
observation; label but no coordinates, sun times rounded to 5 min),
`GET /v1/nowplaying/state`, `GET /v1/nowplaying/art?kind=album|artist|backdrop&size=`
(square baseline JPEG, ETag; rate-limited),
`GET /v1/clock/health` (24h publish counts + clock RSSI/heap/uptime/current
app, device probe cached 30s, latest NG release looked up on GitHub in the
background every 6h — `EMBER_FIRMWARE_CHECK=0` disables it). Dashboard
JSON: RFC 3339 whole-second times, `null` not zero sentinels, arrays of points,
units in keys; goldens in `cmd/ember/testdata/dashboard` (regenerate with
`-update`) are also EmberKit's decode fixtures. Operator: `/admin/doctor`, `/admin/reload`,
`/version`, `/metrics`. Knob device token only: `POST /v1/devices/self/checkin`,
`GET /v1/devices/self/config`, `PUT /v1/devices/self/coredump?id=<dump>`
(the core dump a checkin asked for: `application/octet-stream`, ≤128 KiB or
413, CRC-checked or 400, 409 while another upload of this knob runs, 204 when
stored or already stored), `GET /v1/devices/self/firmware/{version}` (only
the image its checkin's `ota` offers: 409 for any other version, `Range`/`If-Range` (416 past the end),
`ETag: "<sha256>"`, per-device rate limit), `GET /v1/devices/self/view` (the knob's
single compact poll, ETag/304, long-poll `?wait=≤25` advertised by
`X-Ember-View-Wait` — see ARCHITECTURE "Wire protocol") (`/state` carries `X-Ember-Devices-Epoch`, bumped
on any knob config change or rotation). `POST /hooks/plex?key=…` (Plex webhook, `EMBER_PLEX_WEBHOOK_KEY`; only wakes
the Plex poller). Device-only (unauthenticated): `POST /hooks/awtrix/button`
(NG ≥1.1.1 posts JSON `{"button":"left|middle|right","state":bool,"uid"}`, older NG
the form `button=…&state=1|0&uid` — both accepted; `select` accepted as an
alias for `middle`; Pomodoro maps middle=play/pause/resume, left=stop,
right=skip — all on press; the left+right chord from AWTRIX3 is gone).

The `/v1/device/*` group discovers the clock (mDNS `_awtrixng._tcp` browse +
`FIND_AWTRIXNG` UDP fallback, fingerprinted via `GET /api/v1/device`) and
proxies its NG API to the menu's Settings › Clock pane; the effective clock URL is the
menu override, else `config.json` baseline; if that fails its probes, an in-memory mDNS swap replaces it (the pin included) until a PUT naming `base_url` or a reload that changes the file URL (see `clock_url.go`). A 30s
device-watch probe re-discovers on IP change and detects a clock reboot (via
falling `uptimeSeconds`) to trigger a republish of every pushed app — issue
#73's Berry boot-ping hook (`POST /hooks/awtrix/boot`, config toggle
`awtrix.boot_ping`) is the fast path that republishes instantly instead of
waiting on the 30s watch. The server also advertises itself as `_ember._tcp`
(default on; `EMBER_MDNS_ADVERTISE=0` to disable; TXT `version`, `path`); the
macOS app and headless producers with an empty/`auto` `EMBER_SERVER_URL` find
it that way (RUNBOOK "Headless / Linux producers"). **mDNS in both directions
needs the container on host (or macvlan) networking** — multicast doesn't
cross the default Docker bridge.

Full behavior (staleness, render priority, the coordinator, display hold) is in
[`ARCHITECTURE.md`](ARCHITECTURE.md).
