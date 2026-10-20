# API quick reference

The contract (request/response shapes, auth per route) is
[`openapi.yaml`](openapi.yaml); operations marked `x-internal` serve Ember.app
and may change.
The knob's routes also follow [`DEVICE-PROTOCOL.md`](DEVICE-PROTOCOL.md):
compatibility rules and the golden fixtures in `cmd/ember/testdata/devices`.

**Tokens.** `EMBER_TOKEN` (master) passes every bearer route below. Scoped
client tokens (`ekc_…`) and knob tokens are minted only by the master token with `POST /v1/devices
{"kind":"client","name","scopes"}` (at most 64; rename/rotate/delete also master-only;
listed by `GET /v1/clients`, owner or `admin`, kept out of the device
registry since #340) and pass only their scopes: `ingest` (`POST`/`DELETE /v1/status`,
`POST /v1/usage`, `POST /v1/notify`, `POST /v1/reminders/fire`), `control`
(`POST /v1/pomodoro/{start,pause,resume,stop,skip}`), `read` (the non-secret
settings GETs: apps, pomodoro/usage/display/brightness/quiet config,
`/v1/clock/stats`), `admin` (every bearer `/v1/` route except client-token
management). Wrong scope is 403. An optional `"sources":[…]` at mint (1-16,
each ≤64 chars, needs `ingest`; 400 with `admin`, which could still `POST /v1/clear`) binds the token: `POST`/`DELETE
/v1/status` and `POST /v1/usage` naming another `source` (usage: or none) are
403; without it any source passes. Usage ownership goes by writer (the
token, not the `source` label): an account belongs to the writer (token) that created it or last took it
over. Another writer's post updates it while it is stale (no post for 10
min) or its owner's token was deleted, and then takes it over; while it is
fresh a client token's post is 403 naming the owner's kind, and the master
token's is accepted and takes it over. No client token's cap ever evicts
an account the master token wrote. Sessions are keyed
`source/tool/session`, so a bound token's status `tool`/`session` and a minted
source may not contain `/` (400). Client tokens never pass the knob-only
routes, now-playing control or `/admin/*`. Details: RUNBOOK "Integrating a
source".

Server routes, grouped by auth. Behavior and wire shapes: [`ARCHITECTURE.md`](ARCHITECTURE.md) "Wire protocol". Setup and toggles: [`RUNBOOK.md`](RUNBOOK.md).

Write (bearer auth): `POST /v1/status`, `DELETE /v1/status`, `POST /v1/clear`,
`POST /v1/notify`, `POST /v1/pomodoro/{start,pause,resume,stop,skip}` and
`POST /v1/nowplaying/control` (also accept a knob device token; control takes an
optional `Idempotency-Key`), `GET/POST /v1/devices` (`GET` lists devices
only; a pre-#340 server also lists clients there, `kind: client`; each knob
carries `effective_caps`, its reported or legacy capabilities, #341,
DEVICE-PROTOCOL "Capabilities"; a knob with the `shown` caps feature also
carries `shown: {page, face|null, at}`, what it reported on screen, while a
poll carried it in the last 50 s and the current caps list the token,
absent otherwise, #609, DEVICE-PROTOCOL
"Shown page"; the `PATCH` reply too; no change long-poll, so a client
polls the list),
`GET /v1/clients` (`{"clients":[…]}`, same record shape; 404 before #340), `GET/PUT
/v1/devices/{id}/config` (same merge; includes `swipe_pages` (bool, default
true), `quiet{calm, dim_level}`, optional `idle{dim_after_s, dim_level, off_after_s}`
and `apps{pomodoro, weather, nowplaying}` (#656, DEVICE-PROTOCOL "Idle and
per-app options"; absent = firmware default, unknown keys 400) and
`display{fast_link, rotation 0|90|180|270}`;
a knob that reported `caps` gets a 400 for turning on a page outside them,
a `display.rotation` outside `caps.rotations` or a config over its `config_bytes`; for the
clock record, kind `awtrix-ng`, the clock's presentation config, stored in
`clock_config_json` once migrated, else a façade over the overlay slices;
`config_version` is a counter; ARCHITECTURE "Clock record and config façade"), `PATCH`/`DELETE /v1/devices/{id}`
(a deleted clock record returns at the next good probe),
`POST /v1/devices/{id}/rotate` (the knob registry — see ARCHITECTURE "Device
registry"; 400 for the clock, which has no token), `GET /v1/devices/{id}/stats?range=15m|1h|24h`,
`GET`/`PUT /v1/devices/{id}/ota` (knob firmware update: `{"mode":"manual|auto","target":"0.9.14"|null,"retry":true}` merge;
`target:null` also dismisses a failed or rolled-back attempt, 409 `ota_in_progress` while downloading;
a target or retry of a version no longer stored is 400; 409 `firmware_changed` if the store kept changing;
409 `no_rollback_bootloader`, or `ota_in_progress` while installing, restarting or verifying), `POST /v1/firmware?channel=release|test`
(raw cinder `.bin` ≤4 MiB: 201 stored, 200 same bytes, 409 other bytes,
400 `bad_image|wrong_chip|wrong_project|bad_version|dev_seed_build`; the
version a `?replace=1` upload stores, and each version retention evicts, is
dropped from every knob's OTA `blocked` list; 201 once the new bytes are in
place, even if removing the old copy or an evicted version failed, which the
server logs),
`GET /v1/firmware` (newest first), `PATCH`/`DELETE /v1/firmware/{version}`
(channel; 409 while targeted, indexed or not, unless the target is parked
after that version failed or rolled back; DELETE removes the version's
directory and any `.old-<version>-*` copy a replace left, also when the store
does not index it (404); once they are renamed away it drops the version from
every knob's OTA `blocked` list, which the OTA `available` field skips, and
makes every knob outside an active attempt forget it (a target naming it
cleared, a record whose last attempt it was reset unless done; eviction
does the same), so no Retry points at a missing image; a removal
that fails answers 500, keeps the block and keeps the version listed so the
DELETE can be retried; `DELETE …?keep=protected` also answers 409
`firmware_kept_in_use` for a version any knob runs (last check-in `fw`) or
targets, and `firmware_kept_newest` for the newest release image or the
newest test image when newer than that release, checked under the store
lock with the device registry held until the build is renamed off its
live path, so a check-in can't slip in; servers that support it list
`firmware_delete_keep` in `/version` `features`; any other `keep` is 400),
`PUT`/`GET /v1/firmware/{version}/elf`
(≤64 MiB, 400 `elf_mismatch`), `GET /v1/firmware/{version}/bin`,
`GET /v1/firmware/by-build/{build}/elf` (ARCHITECTURE "Knob firmware
updates"),
`POST`/`GET`/`DELETE /v1/devices/{id}/demo` and `GET /v1/devices/{id}/demo/scenes`
(knob demo mode, memory only: `{"scene":"…"}` or `{"scenes":[…]}` plus
`"hold"` (ignored with `scenes`; capped at 60 s by knob and server), newest
wins; unknown scene, both or neither of `scene` and `scenes` (even
`"scenes": []`) 400, a knob without the `demo` caps feature 409; status
`{state: idle|pending|playing|unanswered|restarted, scene, id, queue_left}`
(`restarted`: the knob rebooted mid-demo, demo and queue dropped; a view
poll with the knob token and no `demo_id` while a scene plays reads as that
reboot); the catalog `[{name, group, title}]` — DEVICE-PROTOCOL "Demo
mode"),
`POST /v1/devices/{id}/stats/live` (knob diagnostics, memory only — see
ARCHITECTURE "Knob diagnostics"), `GET /v1/devices/{id}/coredumps`
(stored knob core dumps, newest first) and `GET`/`DELETE
/v1/devices/{id}/coredumps/{dump}` (`application/octet-stream`,
`Content-Disposition: attachment; filename="<device>-<fw>-<dump>.bin"`; see
ARCHITECTURE "Knob checkin", RUNBOOK "Decoding a knob core dump"), `GET /v1/clock/stats?range=15m|1h|24h`
(clock probe history, memory only — ARCHITECTURE "Clock stats"),
`GET/PUT /v1/pomodoro/config` (a changed `sound_melody` that awtrix-ng 1.2
can't play is a 400: a name outside `[A-Za-z0-9_-]{1,32}`, or RTTTL that
isn't `name:defaults:notes` with a 1–24 character name and at most 512
characters; a value already stored is kept and never blocks other edits),
`GET/PUT /v1/apps` (per-tool clock visibility),
`POST /v1/usage` (optional `account_key`, 16 hex characters hashed on the
Mac, and `plan`; one entry per tool and account, newest wins, dropped after 7
days; each writer, a client token or the master token, keeps 8 per tool
and 16 in all and evicts only its own, and a client token's new account
past 64 in all is a 507, never the master's —
ARCHITECTURE "Usage per account"), `GET/PUT /v1/usage/config`,
`GET /v1/usage/accounts` (owner or `admin`: `{"accounts":[{id, tool, plan,
name, name_set, default_name, hidden, order, first_seen, updated_at,
stale}]}` in display order) and `PUT /v1/usage/accounts` (master token
only, every client token 403: `{"<id>":{"name","hidden","order"}|null}`,
id `<tool>:<account_key>` or the bare tool name for a keyless account,
merge by id, an unknown id 400; the knob's `usage` block follows it —
DEVICE-PROTOCOL "Usage"),
`POST /v1/nowplaying` (a
source's player report; a playing or paused report with no title and no
`track_id` counts as stopped and drops the entry), `GET /v1/nowplaying/commands?player=&wait=&watched=` (Ember.app's
long-poll for Music commands; master token only, every client token 403;
the answer's `watched`, always present, says whether a knob shows now
playing or an old knob that can't say polled in the last 50 s, false with
no knob paired, and a poll that names the value it knows wakes when it
changes —
ARCHITECTURE "Now playing" › Music) and `PUT /v1/nowplaying/art?source=&player=&kind=album|artist&track_id=`
(raw JPEG/PNG ≤2 MB; 409 once the track moved on — ARCHITECTURE "Now playing"), `GET/PUT /v1/display/config`,
`GET/PUT /v1/weather/config` (`provider` `open-meteo`, `met-no`,
`tomorrow-io` or `weatherapi`, and under Open-Meteo
`open_meteo_model`, one of `best_match` (default), `ecmwf_ifs`,
`ecmwf_ifs025`, `icon_seamless`, `gfs_seamless`, `ukmo_seamless`; anything
else is a 400 — ARCHITECTURE "Providers and models"; both answer
`providers_available`, which lists `tomorrow-io` only while
`EMBER_TOMORROW_API_KEY` is set and `weatherapi` only while
`EMBER_WEATHERAPI_KEY` is; a PUT switching to one without its key
is a 400, and so is a changed `severe_sound` under the Pomodoro
`sound_melody` rule), `POST /v1/reminders/fire` (optional
`Idempotency-Key` header dedupes retries for 10 min),
`GET/PUT /v1/meetings/config`, `GET/PUT /v1/quiet/config` (also drives the
knob view's `quiet`), `GET/PUT /v1/brightness/config` (every `…/config`
settings PUT above is merge semantics: the body is a JSON object whose omitted
keys keep their current value; an invalid merged result is a 400 and changes
nothing — see `settings_overlay.go`; the clock presentation keys of the
weather, meetings, usage and Pomodoro configs are aliases of the clock
config once it is migrated: stored there, returned merged here, kept for at
least two releases after #232), `GET/PUT /v1/device/config`
(`{"base_url"}`, the same merge: `{}` changes nothing, an empty or non-http(s)
URL is a 400; GET answers the effective URL and its `source` — see
`clock_url.go`; a PUT that changes the effective URL republishes and, in the
background, provisions icons on the new clock and installs or removes the
boot-ping script there, following `awtrix.boot_ping`),
`GET /v1/device/discover` (3 s of mDNS, then, only if mDNS found nothing,
3 s of `FIND_AWTRIXNG` UDP, then up to 1.5 s probing what answered: up
to ~7.5 s. 200 with `candidates`, `effective` and `source`, an empty
list when nothing answered as an awtrix-ng clock; 502 only when the server
has no clock browse wired, never on a cancel; 503 `clock_disabled` under
`EMBER_CLOCK=off`. A client that hangs up mid-browse stops the browse, and no
response is written),
`GET/PUT /v1/device/settings` (whitelisted
`PATCH /api/v1/settings` keys — see `device_settings.go`; the sound keys
follow the clock's audio API: `soundEnabled`/`buzzerVolume` on NG 1.0/1.1,
`volume`/`appVolume`/`alertVolume` (0–100) on NG 1.2; GET passes through
whichever the clock returns, and PUT refuses the other set with 400 once the
clock's API is known (from its last GET, else capabilities); during a Pomodoro
takeover `autoTransition`/`blockNavigation` read and write the saved prior,
flagged by an `X-Ember-Deferred-Keys` response header),
`GET/PUT /v1/device/display` (overlay, `PATCH /api/v1/display`),
`PUT /v1/device/display/power` (`{"power":bool}` — blanks/relights the
matrix, runtime-only), `POST /v1/device/audio/test` (built-in chime, or
`{"melody":"<name>"}` to preview a stored one, `[A-Za-z0-9_-]{1,32}`, at
most 24 characters on a 1.0/1.1 clock, or 400), `POST /v1/device/audio/stop`,
`GET /v1/device/audio/melodies` (NG's melody list; the audio routes answer
503 `unavailable` when cached capabilities show no buzzer, meaning neither
`audio.buzzer` nor NG 1.2's `audio.rtttl`, or no output),
`GET/PUT /v1/device/apps` (ordering + enable/disable,
`PUT /api/v1/apps/order`; the PUT body is `{"order":[…],"disabled":[…]}`,
both required, and `disabled` is the complete list of switched-off apps: a
missing or `null` field answers 400 rather than being sent on as `[]`, which
would switch every off app back on; the same holds for `rotation` in
`PUT /v1/devices/{id}/config`; these and the clock's
`GET/PUT /v1/devices/{id}/config` run one at a time and answer 503 when
another app order change holds them past the 11 s deadline),
`PUT /v1/device/apps/{name}/enabled` (`{"enabled":bool}`, switches one app
and leaves the others: tries NG's per-app route, and on a 404/405 remembers
the clock lacks it (until a URL, uid or firmware change or a reboot is
seen, or an hour passes) and reads the clock's list fresh to write the full
order with only that app changed; a name that isn't `[A-Za-z0-9_-]{1,32}`, is
NG-reserved or names a module answers 400; NG's 507 (applied, not saved)
is relayed as 507, a clock 404 as 502, 400/422 as is; same lock and 503 as
above; listed as `device_app_enabled` in `/version` `features`),
`GET/PUT /v1/device/sensors` (system
`tempOffset`/`humOffset` via read-merge-PUT of `/api/v1/system`; applies live,
no reboot), `GET /v1/device/buttons`, `PUT /v1/device/buttons` (read-merge-PUT
of `/api/v1/system.buttonCallback`),
`GET /v1/device/stats`, `GET /v1/device/screen` (proxies
`GET /api/v1/display/screen`, raw `{width,height,pixels}`),
`GET /v1/device/capabilities` (cached `GET /api/v1/capabilities` — the firmware's
effect/transition/overlay/palette lists; live proxy when the cache is cold),
`POST /v1/device/{reboot,notify/dismiss,app/next,app/previous}`. Read (no auth):
`GET /state` (each session carries `state_since` and, from producers that
send it, `project`, #485), `GET /healthz`, `GET /v1/display/brightness` (clock lux, else sun schedule), `GET /v1/preview`,
`GET /v1/{weather,pomodoro,reminders}/preview`, `GET /v1/meetings/{preview,state}`,
`GET /v1/pomodoro/{state,stats,heatmap,workhours}`,
`GET /v1/pomodoro/dashboard` (HTML), and the native-dashboard reads
(`dashboard_http.go`, `clock_health_http.go`): `GET /v1/usage` (latest usage
snapshot per tool — the POST stays authed), `GET /v1/activity/summary?days=`
(agent time per tool/source, waiting excluded), `GET /v1/weather/state` (cached
observation; label but no coordinates, sun times rounded to 5 min; `model`
names the Open-Meteo model, absent under MET Norway; `current.today_min_c`
and `today_max_c` are today's forecast range widened to the current
temperature, the knob Temperature face's gauge, `null` without a forecast
day; `current.cloud_pct` and `current.uv` are the knob view's `now.cloud` and
`now.uv` (same observation, whole numbers), `null` when the provider has none;
`sun.night` is the knob view's `night`, from the exact sun times, not the
rounded ones sent; `faces` is the knob view's `wx_faces` weather detail from
the same observation (offset, night, wind, current rain step, today's chance,
rain spells, next days, twilight rounded to 5 min, moon phase/lit/up without
rise or set, `up` evaluated on the same 5-min grid, AQI with the knob's air gate), null with `current`;
`error` gives the
reason weather is unavailable while it is on, e.g. `tomorrow-io` or `weatherapi` chosen
without its key),
`GET /v1/nowplaying/state`, `GET /v1/nowplaying/art?kind=album|artist|backdrop|cover&size=`
(square baseline JPEG, ETag; `cover` is the knob's prepared 466 px circle; rate-limited),
`GET /v1/clock/health` (24h publish counts + clock RSSI/heap/uptime/current
app, device probe cached 30s, latest NG release looked up on GitHub in the
background every 6h — `EMBER_FIRMWARE_CHECK=0` disables it). Dashboard
JSON: RFC 3339 whole-second times, `null` not zero sentinels, arrays of points,
units in keys; goldens in `cmd/ember/testdata/dashboard` (regenerate with
`-update`) are also EmberKit's decode fixtures. Operator: `/admin/doctor`, `/admin/reload`,
`/version` (`binary`, `version`, `revision`, `dirty`, `go_version`,
`features`: server capabilities clients gate on, today
`firmware_delete_keep` and `device_app_enabled`; `clock_disabled: true` under `EMBER_CLOCK=off`, omitted
otherwise, read without clock I/O), `/metrics`. Knob device token only: `POST /v1/devices/self/checkin`,
`GET /v1/devices/self/config`, `PUT /v1/devices/self/coredump?id=<dump>`
(the core dump a checkin asked for: `application/octet-stream`, ≤128 KiB or
413, CRC-checked or 400, 409 while another upload of this knob runs, 204 when
stored or already stored), `GET /v1/devices/self/firmware/{version}` (only
the image its checkin's `ota` offers: 409 for any other version, `Range`/`If-Range` (416 past the end),
`ETag: "<sha256>"`, per-device rate limit), `GET /v1/devices/self/view` (the knob's
single compact poll, ETag/304, long-poll `?wait=≤25` advertised by
`X-Ember-View-Wait`; `?demo_id=&demo_state=playing|done` report on a demo;
`?shown=<page>[:<face>]` reports what the knob shows (`shown` caps feature;
a malformed value is ignored, never a 400); `"quiet":true` while quiet hours are on, and a waiting
poll wakes at the quiet-hours edges — see ARCHITECTURE "Wire protocol") (`/state` carries `X-Ember-Devices-Epoch`, bumped
on any knob config change or rotation and on any clock record or clock config change). `POST /hooks/plex?key=…` (Plex webhook, `EMBER_PLEX_WEBHOOK_KEY`; only wakes
the Plex poller). Device-only (unauthenticated): `POST /hooks/awtrix/button`
(NG ≥1.1.1 posts JSON `{"button":"left|middle|right","state":bool,"uid"}`, older NG
the form `button=…&state=1|0&uid` — both accepted; `select` accepted as an
alias for `middle`; Pomodoro maps middle=play/pause/resume, left=stop,
right=skip — all on press; the left+right chord from AWTRIX3 is gone).

**Knob controls** (#515, cinder#144; ARCHITECTURE "Knob controls"):
`POST /v1/controls/action` `{"gesture":"push|double_push|long_push|turn","steps":±1..20}`
(`steps` for `turn` only; master or knob device token, client tokens 401,
optional `Idempotency-Key`; 202 `{"status":"queued","target"}`, 200
`duplicate`, 429, 503 no target or target not listening),
`GET /v1/controls/config` (`{"target"}` plus read-only `macs`
`[{name, listening, last_poll}]`; master or `admin` client), `PUT
/v1/controls/config` (merge; master token only, every client token 403) and
`GET /v1/controls/commands?mac=&wait=≤25` (Ember.app's long-poll,
`{"commands":[{"id","gesture","steps","age_ms"}]}`; master token only, every
client token 403, so no client can drain a Mac's queue or show up as
listening). The server never holds
or sends an action: the gesture-to-action mapping lives in Ember.app on the
target Mac.

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
