# Device protocol

The contract between Ember (the owner) and the knob firmware, cinder (the
consumer, `tarakanof/cinder`). Request bodies and auth are in
[`openapi.yaml`](openapi.yaml) (`device-self` and `control` tags). The view's
field-by-field shape and the behaviour behind each route are in
[`ARCHITECTURE.md`](ARCHITECTURE.md) "Wire protocol" (Knob view, Knob checkin),
"Knob diagnostics" and "Knob firmware updates". The fixtures below are the
concrete shapes. This file holds the rules for changing them and the
fixtures both repos test against. Design: `Specs/ember/2026-10-09-device-protocol-capabilities-design.md`
in the vault.

## Scope

Every route the knob calls, all with its device token (`ekd_…`):

| Route | What |
|---|---|
| `POST /v1/devices/self/checkin` | Health report in, optionally with `caps` ("Capabilities" below); reply carries `config_version`, `caps_ack` when the caps were accepted, the `config` when the knob's is stale, `new_token` during a rotation, `diag_live_until`, `coredump_wanted`/`coredump_ack`, an `ota` offer. A 200 carries `X-Ember-Now`. |
| `GET /v1/devices/self/view` | The one poll: `v`, `epoch`, `config_version`, `mood`, `pomo`, `weather` (with reported caps: only for pages in `caps.pages` and on, "Capabilities"; with `wx_faces` it carries the detail keys, "Weather detail"), `brightness`, then `quiet` (only while quiet hours are on), `nowplaying` (only with the `nowplaying` page on; with `np_cover` it carries `accent` and `cover_version`, "Prepared cover") and `diag_live_until` (live mode only). Strong `ETag`, `If-None-Match` answers 304. `?wait=N` long-polls up to the `X-Ember-View-Wait` cap (25 s). A 200 or 304 carries `X-Ember-Now` (server Unix seconds), the anchor for `ends_at` and `position_at`, and `X-Ember-View-Wait`; error replies (400, 401, 429) carry neither. |
| `GET /v1/devices/self/config` | `{config_version, config}`, the same pair a stale checkin carries. |
| `PUT /v1/devices/self/coredump?id=` | The core dump a checkin asked for. |
| `GET /v1/devices/self/firmware/{version}` | The image the checkin's `ota` offer names. |
| `POST /v1/pomodoro/{start,pause,resume,stop,skip}` | Pomodoro actions; the reply is the timer status (`pomodoro_action.json`). |
| `POST /v1/nowplaying/control` | Player control, optional `Idempotency-Key`. |
| `GET /v1/nowplaying/art?kind=&size=&v=` | Art for the view's `art_version`: `kind` `album`/`artist`/`backdrop`/`cover`, `size` the square edge in px from a fixed list per kind (album 240 or 120, artist 64 or 120, backdrop 466, cover 466; default the first, anything else 400; `cover` is the prepared circle, "Prepared cover"), `v` the `art_version` (public, no token). |

The knob learns of a config change or a rotation when the view's `epoch` or
`config_version` moves, then checks in. The epoch is shared by every device
record, so a clock change (its record created, renamed or deleted, or its
composed config changed, #230) moves it too and costs the knob one checkin
that finds nothing new.

**Fallback (legacy) routes.** cinder drops to per-endpoint polling when the
view answers 404 or 405 (a server without the view; re-probe after 10 min),
401 or 403 (token rejected; re-probe after 60 s), or three 5xx in a row
(re-probe after 60 s). In that mode it polls `GET /state` (the `render`
counters and `sessions`; its `X-Ember-Devices-Epoch` header replaces the
view's `epoch` as the rotation and config signal), `GET /v1/display/brightness`,
`GET /v1/pomodoro/state` and `GET /v1/weather/state`. These keep a knob alive
after a revoked token or a server rollback, so they are part of the contract
and fall under the same rules as the view.

## Contract rules

1. **Additive by default.** A new field is optional on both sides. Receivers
   ignore fields they don't know: the server decodes checkins leniently and
   cinder's parsers skip unknown keys. A sender leaves out a block that does
   not apply rather than sending `null`. Existing exceptions, kept: the view's
   `pomo` and `weather` are `null` when off, and `weather.sunrise`/`sunset`
   are `null` without a location, in polar day/night, and both on a boundary
   day with only one crossing (a sunrise but no sunset, or the reverse). The
   last is deliberate (#423): the server knows the lone crossing and uses it
   for the clock's sun popup and moon, but the pair stays both-null or
   both-set on the wire.
2. **Removing or retyping a field is breaking.** In the view it needs a new
   view major `v` (today always `1`). The server keeps serving the old major
   to any device that doesn't ask for the new one, for at least two releases.
   The fallback routes, the checkin, the config and the action replies have
   no version field: a breaking change there needs a new route, with the old
   one kept for the same two releases.
3. **Behaviour keys on capabilities, never on `fw`.** `fw` is for diagnostics
   and OTA only. A knob without `caps` is a legacy knob: the server maps its
   `fw` to capabilities with one table (`legacyKnobCaps` in
   `devices_caps.go`), the only place in the server code where a version
   literal may appear (`TestVersionLiteralsOnlyInTheLegacyCapsTable`).
   Ember.app gates on `effective_caps`; its fw semver floors stay only as the
   fallback for a server that sends none, for two releases.
4. **Actions are idempotent or take `Idempotency-Key`.** A knob never replays
   a press made while offline. Today `pause`, `resume` and `stop` are
   idempotent and `nowplaying/control` dedupes on its key; `start` restarts
   the phase and `skip` advances again, so a knob must not retry those
   blindly. A new action route follows the rule.
5. **Goldens are the contract.** Any change to the view, checkin or config
   shape updates the fixtures below in the same PR, and the PR says so, so
   cinder can sync them.

## Capabilities

Since #341 the knob may say what it can do in every checkin (cinder#27):

```json
"caps": {
  "view": [1, 1],
  "pages": ["bot", "pomodoro", "weather", "nowplaying"],
  "features": ["view_wait", "np_control", "ota_rollback", "coredump", "stats_intervals"],
  "limits": {"view_bytes": 16383, "config_bytes": 1024},
  "rotations": [0, 180]
}
```

- `view`: the `[min, max]` view majors the firmware parses, 1 ≤ min ≤ max.
- `pages`: the page ids compiled in (cinder's page table), 1-32 ids matching
  the config's page id pattern, no duplicates.
- `features`: tokens from the registry below, at most 64, each
  `^[a-z][a-z0-9_]{0,31}$`, no duplicates. A token the server doesn't know is
  kept and shown, never an error. A new token is one row in the registry,
  no schema change; a token whose meaning changes gets a new name.
- `limits` (optional, each key optional, 0 = unknown): the largest body in
  bytes the firmware holds. `view_bytes` is the view buffer less its NUL
  (cinder `RESP_MAX` 16 KiB → 16383); `config_bytes` is the config as the
  knob stores it, compact JSON (cinder `CFG_SETTINGS_MAX`, 1024). Floors:
  `view_bytes` ≥ 4096 (a full view without `nowplaying` is under 700 B;
  the rest is headroom for long host labels and future fields) and
  `config_bytes` ≥ the default config's compact size (447 B today; it
  follows `defaultKnobSettings`), so the knob can always hold a view and
  the config a new record starts with.
- `rotations` (optional): the display rotations in degrees the firmware can
  apply, integers each one of `0`, `90`, `180`, `270`, no duplicates, `0`
  always listed (`null`, a fraction or a missing `0` drops the caps).
  Missing means `[0]`; the server reads an empty list the same. See
  "Display rotation".
- Order carries no meaning. The server sorts `features` and `rotations`; `pages` are
  compared as a set and keep the order first stored, so a knob that lists
  the same caps in another order causes no registry write.
- Unknown keys inside `caps` are ignored. Caps that break a rule above are
  dropped: no `caps_ack`, the knob falls back to the legacy table, the
  reason shows as `effective_caps.caps_error`, and the server logs it once
  at Warn per change (`device caps dropped`). The same invalid caps on
  every checkin write the registry once.
- **A device that sends `caps` must accept the absence of any page block**
  (`mood` included) in the view, and must not require `caps_ack`. Only a
  knob that sent caps can get a view without `mood`; a knob without caps
  always gets `mood`, `pomo` and `weather` (`TestLegacyKnobAlwaysGetsMood`).

**Server.** Stores the caps on the record; a checkin whose caps differ from
the stored ones (new, changed, or gone because a downgraded firmware sends
none) writes the registry at once, an unchanged one doesn't. The reply
carries `"caps_ack":true` after accepted caps and nothing otherwise, so new
firmware can tell an old server; firmware never requires it.
`effectiveCaps(record)` is the stored caps, else the legacy table for the
last checkin's `fw`. `GET /v1/devices` shows it on every knob as
`effective_caps` (the caps plus `"source":"reported"|"legacy"` and, after
dropped caps, `caps_error`; absent for the clock and on servers before
#341). With reported caps:

- **View:** `v` = min(server max, `caps.view[1]`); the server max is 1. Only
  blocks whose page is in `caps.pages` and on in the knob's `pages` are sent
  (`bot` → `mood`, `pomodoro` → `pomo`, `weather` → `weather`, `nowplaying`
  → `nowplaying`); a left-out block is absent, while a kept `pomo` or
  `weather` is still `null` when its feature is off. A body over
  `limits.view_bytes` drops the weather detail ("Weather detail"), then
  `nowplaying`, then `weather`, then `pomo`; `mood` stays. If it is still
  over, the server serves it and logs once per device (`knob view over
  its caps view_bytes`) until it fits again.
- **Config PUT** (owner): 400 when it turns on a page that is not in
  `caps.pages` (a page that was already on stays valid, and ids that are
  off, unknown ones included, are kept as before for the NVS round trip),
  and 400 when the changed config's compact JSON exceeds
  `limits.config_bytes` and is larger than the stored one (a config
  already over the limit can still shrink), and 400 when it changes
  `display.rotation` to a value not in `caps.rotations` (a stored value
  the caps no longer list stays valid). A no-op PUT is never rejected.

**Legacy knobs** (no `caps`) keep today's behaviour exactly: the same view
bytes and ETag as before #341, and no new 400s. The table feeds
`effective_caps` only. Reason: the table is a guess from `fw`, and an
unknown or empty `fw` (a knob that has not checked in yet, a dev build) maps
to the minimal set, which would start rejecting the live knob's valid
`nowplaying` page.

**Legacy table** (`legacyKnobCaps`, cumulative; `view` is `[1, 1]` for all;
an `fw` that isn't semver, or is older than 0.7.0, gets the first row; a
pre-release sorts before its release; `+build` metadata is ignored.
Ember.app's semver fallback, for servers without `effective_caps`, treats
pre-releases the same way):

| `fw` ≥ | Pages | Features | Limits | Source (cinder `docs/features.md`) |
|---|---|---|---|---|
| (any) | `bot`, `pomodoro`, `weather` | — | — | the pages Ember's defaults have always held |
| 0.7.0 | | `stats_intervals` | | Stats and live-mode intervals (#39) |
| 0.8.0 | | `view_wait` | | View long-poll (#27) |
| 0.9.0 | `nowplaying` | | | Now playing page (#14) |
| 0.9.6 | | `np_control` | | Now-playing controls |
| 0.9.14 | | `coredump` | | Core dump upload (#56) |
| 0.9.16 | | `ota_rollback` | | OTA from Ember (#10) |
| 0.9.28 | | | `view_bytes` 16383, `config_bytes` 1024 | `RESP_MAX`, `CFG_SETTINGS_MAX` at tag v0.9.28, the first tag (older history was squashed) |

**Feature-token registry:**

| Token | Meaning | Server or app use |
|---|---|---|
| `view_wait` | Long-polls the view with `?wait=` after it sees `X-Ember-View-Wait` | none (the header negotiates it) |
| `np_control` | The now-playing page sends `POST /v1/nowplaying/control` (volume, play/pause, next, previous) | none yet |
| `ota_rollback` | Installs Ember's OTA offers and rolls back a bad image | none yet: offers still need the checkin's `ota.rollback` (the bootloader) |
| `coredump` | Uploads a core dump on `coredump_wanted`, erases on `coredump_ack` | none yet |
| `stats_intervals` | Applies `stats_interval_s` and `live_interval_s` from its config | Ember.app shows the interval pickers |
| `wx_faces` | Parses the weather detail keys ("Weather detail") | The detail keys are added to `weather` only with this token |
| `np_cover` | Shows the prepared cover (`kind=cover`) and `accent` ("Prepared cover") | `nowplaying.accent` and `nowplaying.cover_version` only with this token |

Page ids in `caps.pages` need no registry: they are the config's page ids;
Ember.app lists a knob app only when its page is in `effective_caps.pages`.

**Compatibility matrix** (`devices_caps_test.go` unless noted):

| Server | Firmware | Expected | Test |
|---|---|---|---|
| new | legacy (no caps) | `effective_caps` from the table; view and ETag unchanged; no new 400s | `TestLegacyFirmwareViewIsUnchanged` (fw 0.8.3 and 0.9.41 against `view_full.json`), `TestLegacyKnobAlwaysGetsMood`, `TestLegacyCapsTable`, `TestLegacyKnobConfigPutIsNotCheckedAgainstTheTable` |
| new | new | caps stored and acked; view filtered by caps ∩ pages | `TestCapsKnobViewIsFilteredByCapsAndPages` (`view_caps_limited.json`), `TestDeviceCheckinCapsGolden`, `TestCapsAreStoredExposedAndClearedOnDowngrade`, `TestCapsCheckinWritesStoreOnlyWhenCapsChange`, `TestReorderedCapsDoNotWrite`, `TestCapsLimitFloors`, `TestInvalidCapsAreExposedOnceWithoutRewrites`; cinder host test: a view with no page block, `mood` included, parses |
| old | new | no `caps_ack`; the knob behaves as today | cinder host test with the pre-caps fixtures; Ember side: a reply without accepted caps has no `caps_ack` (`TestInvalidCapsAreDroppedWithoutAck`) |
| new | new, `v` outside range | server sends `v` = min(1, `caps.view[1]`); the knob keeps its last view and shows the update state | `TestCapsKnobViewMajorIsTheLowerOfServerAndFirmware`; cinder host test with a `v:99` fixture |
| new | new, `rotations` listed or not | `effective_caps.rotations` sorted, `[0]` when missing; config PUT 400 for a rotation outside it, a stored one kept; a legacy or downgraded knob is unchecked | `TestCapsRotationsPresentAndMissing`, `TestCapsKnobConfigPutRejectsRotationOutsideCaps`, `TestCapsKnobDowngradedToLegacyFirmwareKeepsAndFreesRotation`, `TestLegacyKnobConfigPutIsNotCheckedForRotation`, `TestKnobRotationRoundTripsAndReachesTheKnob` |
| new | new, `wx_faces` or not | detail only with the token and the `weather` block; a caps knob without it gets the legacy block; dropped first to fit `view_bytes`; the worst case stays under 8 KB | `TestKnobViewWeatherFacesGolden` (`view_faces*.json`, `devices_view_weather_test.go`), `TestCapsKnobWithoutWxFacesGetsNoWeatherDetail`, `TestCapsKnobWithWxFacesButWeatherPageOffGetsNoWeather`, `TestCapsKnobViewDropsWeatherDetailFirst`, `TestKnobViewWorstCaseFitsHalfTheBuffer` |
| new | any, Open-Meteo model set or not | `weather.model` after `provider` only for a non-default Open-Meteo model; default and MET views and ETags unchanged; MET stays the size worst case | `TestWeatherReadoutsNameTheOpenMeteoModel`, `TestKnobWeatherModelCostsAtMost24BytesAndStaysUnderTheMetWorstCase`, `TestLegacyFirmwareViewIsUnchanged` (default config) |
| new | new, `np_cover` or not | `accent` and `cover_version` only with the token, only when there is a picture, and the only change to `nowplaying`; `kind=cover` is a 466 px circle JPEG, immutable only at `v=<cover_version>` | `TestKnobViewNPCoverGolden` (`view_np_cover.json`, `devices_view_cover_test.go`), `TestKnobViewSendsAccentOnlyWithNPCover`, `TestKnobViewLeavesOutAccentWithoutAPicture`, `TestNowPlayingCoverRoute`, `TestNowPlayingCoverFallsBackToTheArtistPicture`, `TestKnobViewWorstCaseFitsHalfTheBuffer` |
| new | new, page not in caps | config PUT 400; the view omits the block | `TestCapsKnobConfigPutRejectsPagesOutsideCaps`, `TestCapsKnobKeepsAPageThatWasAlreadyOn`, `TestCapsKnobConfigPutChecksConfigBytes`, `TestCapsKnobMayShrinkAConfigAlreadyOverConfigBytes`, `TestCapsKnobViewDropsBlocksToFitViewBytes`, `TestCapsKnobViewStillOverViewBytesLogsOncePerDevice` |

## Quiet hours

Quiet hours are server state (`quiet_hours`, `GET/PUT /v1/quiet/config`),
read through one rule, `Config.quietAt`, by both the clock adapter and the
knob view. The knob has no speaker, so on the knob quiet is display only:

- **View:** `"quiet": true` while the window is on; absent otherwise (never
  `false`), so a view outside quiet hours is byte-identical to before #343.
  A long poll wakes at each window edge with a new ETag. Absent = not quiet.
- **Config:** `"quiet": {"calm": bool, "dim_level": int}`, per knob.
  `calm` (default `true`): the bot skips its attention and excited
  animations while quiet. `dim_level` 1-255 (default 20): while quiet the
  knob's brightness is at most this level; the brightness `floor` does not
  raise it. 20 is Ember's default `night_level` (`brightnessNightLevelDefault`),
  the level a knob following Ember already shows at night without a light
  reading, so quiet never makes the knob darker than an ordinary night by
  default, and it sits above the default knob `floor` (10).
- **Old firmware** skips both unknown keys. The config grows by 37 B: the
  default config was 434 B and the largest valid one 648 B (447 B and
  663 B since `display.rotation`), under cinder's
  `CFG_SETTINGS_MAX` (1024 B), which `TestKnobConfigFitsTheFirmwareStore`
  pins.

## Display rotation

`display.rotation` (#386, cinder#45) turns the knob's picture, per knob:

- **Config:** `"display": {"fast_link": bool, "rotation": int}`, `rotation`
  one of `0`, `90`, `180`, `270` (degrees), default `0`. Any other value is
  a 400 on the owner PUT, and like the rest of `display` it merges field by
  field (`{"display":{"rotation":180}}` keeps `fast_link`). It is always
  sent, `0` included, so the knob never has to guess a default. A record
  stored before #386 reads as `0` with no config push. It lives in
  `display` rather than at the top level because the clock's config has an
  unrelated top-level `rotation` (its app order) on the same route, and a
  checkin's `new_token` rotation is a third meaning.
- **Caps:** `"rotations": [int]`, what the firmware can apply ("Capabilities").
  A knob that reported caps gets a 400 for changing `display.rotation` to
  one it doesn't list. A legacy knob (no caps) gets no check: its firmware
  (0.9.43 `knob_settings_parse` reads only `display.fast_link`) skips the
  unknown key and stays at 0. `effective_caps.rotations` is always present
  on a knob (`[0]` without the field or for a legacy knob).
- **App:** Ember.app shows its Rotation picker when that list holds more
  than `0`, or when the stored value isn't `0` (so a knob whose new caps
  dropped it can still be set back). It offers the listed values; a stored
  value outside them shows only while selected, marked as not supported by
  the firmware.
- **Knob:** applies a value it supports and falls back to `0` for one it
  doesn't (cinder#45).
- **Size:** the default config grows by 13 B (447 B) and the largest valid
  one by 15 B (663 B), still under `CFG_SETTINGS_MAX`.

## Weather observation time

`weather.observed_at` (#355) is when the provider's data is valid, so the
knob can show how old the observation itself is, not only how long ago
Ember last fetched it (`stale`):

- **Format:** an integer of Unix seconds (UTC), like `ends_at` and
  `sunrise`/`sunset`, the last key of the legacy `weather` block; the
  `wx_faces` detail keys follow it
  (`"observed_at":1782041400`). The knob gets the age as
  `X-Ember-Now` minus it, with no date parsing.
- **Source:** the provider's own time for the current values. Open-Meteo:
  `current.time`, the time of its current 15-minute step. MET Norway: the
  `properties.timeseries[].time` of the entry whose instant values are
  shown, the latest one at or before now (it moves on each hour between
  fetches, with the values). Not Ember's fetch time.
- **Absent** when the provider sent no time (and in the `weather: null`
  cases). Never `null` or `0`.
- **Old firmware** skips the unknown key. The body grows by 25 B.
  A new fetch with a new provider time moves the ETag even when the
  shown values did not change.

## Weather model

`weather.model` (#427) names the Open-Meteo forecast model the values come
from, right after `provider` (`"provider":"open-meteo","model":"ecmwf_ifs"`).

- **Absent** under MET Norway and for Open-Meteo's default `best_match`,
  so a default view keeps its bytes and ETag; the goldens don't change.
- Codes stay WMO whatever the model; the knob needs no change to read them.
- **Old firmware** skips the unknown key (`TestWeatherReadoutsNameTheOpenMeteoModel`
  pins where it goes). The block grows by at most 24 B, and an Open-Meteo
  block with its longest model stays shorter than the MET block with the
  longest symbol code, so `TestKnobViewWorstCaseFitsHalfTheBuffer` (MET)
  remains the worst case
  (`TestKnobWeatherModelCostsAtMost24BytesAndStaysUnderTheMetWorstCase`).

## Weather provider Tomorrow.io

`weather.provider` can be `"tomorrow-io"` (#432). Ember translates its
weather codes to WMO before they reach `code` and `days[].code`, so the knob
reads them like Open-Meteo's and needs no change; an unknown code leaves
`code` empty and `cond` `clouds`. A day's `cond`/`code` is its worst hour (storm > snow >
rain > fog > clouds > clear), the daily `weatherCodeMax` only past the
hourly range. The detail keys are filled as for
Open-Meteo (15-min rain steps, `pop`, `uv`, gusts), and `observed_at` is
the time of the first minutely entry (else the latest hourly one at or
before now). The provider string (11 B) fits cinder's 16-byte field, and
with short WMO codes the block stays under the MET worst case
(`TestKnobViewWorstCaseFitsHalfTheBuffer`). Goldens unchanged.

## Weather detail

With `wx_faces` in its caps (#350, cinder#33/#34), a knob's `weather`
block gains detail keys after `observed_at`, for the new faces. The
existing keys are unchanged; the detail rides the block, so it is sent
only when `weather` is (page in `caps.pages` and on, weather enabled and
fetched). Legacy knobs and caps knobs without the token get the block
byte-identical to before (`view_full.json`, `view_caps_limited.json`).

```json
"weather": {
  "provider": "open-meteo", "cond": "rain", "code": "61", "temp_c": 15.4,
  "stale": false, "severe": false, "night": false,
  "sunrise": 1782013500, "sunset": 1782073200, "observed_at": 1782043200,
  "utc_offset": 3600,
  "now": {"cloud": 94, "uv": 3, "wind_kmh": 21, "gust_kmh": 38, "wind_dir": 225, "rain": 1},
  "today": {"min_c": 9, "max_c": 15, "uv_max": 4, "gust_max_kmh": 41, "pop": 83},
  "rain": {"until": 1782049500, "spells": [[1782042300, 1782049500, 2, 0], [1782060300, 1782063900, 1, 1]]},
  "days": [
    {"t": 1782082800, "cond": "clouds", "code": "3", "min_c": 7, "max_c": 16, "pop": 32, "precip_mm10": 0},
    {"t": 1782169200, "cond": "rain", "code": "61", "min_c": 11, "max_c": 17, "pop": 19, "precip_mm10": 8},
    {"t": 1782255600, "cond": "snow", "code": "71", "min_c": -4, "max_c": 3, "pop": 40, "precip_mm10": 13}
  ],
  "twilight": {"dawn": 1782010500, "blue_end": 1782011700, "golden_end": 1782016500,
               "golden_start": 1782069900, "blue_start": 1782075000, "dusk": 1782076200},
  "moon": {"phase": 24, "illum": 46, "up": true, "rise": 1782042000, "set": 1782085560},
  "aqi": 42
}
```

General rules:

- Times are Unix seconds (UTC). Every other value is an integer rounded
  half away from zero; units are metric always (°C, km/h, mm × 10, %,
  degrees). `temp_c` stays a float.
- A value the provider lacks is **left out**, never `null` or `0`; an
  object with nothing in it is left out too (except `rain` and `moon`).

Keys:

- **`utc_offset`**: the location's offset in seconds, for HH:MM labels and
  weekday names (the knob has no time zone). Open-Meteo's
  `utc_offset_seconds`; with MET Norway the air-quality call's while air
  is on and its reading fresh; otherwise `lon/15` whole hours (Ember's
  fallback, wrong across DST).
- **`now`**: the provider's current values. `cloud` 0-100; `uv`
  (Open-Meteo cloud-adjusted, MET clear-sky); `wind_kmh`, `gust_kmh`
  (MET: Nordic only); `wind_dir` 0-359, where the wind comes from;
  `rain` the intensity of the precipitation step containing now, 0 none,
  1 light, 2 moderate, 3 heavy (cinder's `wx_intensity_t`), by mm/h:
  < 0.1, < 2.5, < 7.6, else heavy. `rain` is left out when no step covers
  now.
- **`today`**: the local day containing now (left out, with `days`, when
  the observation has no such day, e.g. days old). `min_c`/`max_c`
  widened to include `temp_c`, so a gauge marker always sits on the ring
  (MET only knows the rest of today). `uv_max`, `gust_max_kmh`, `pop`
  when the provider has them (Open-Meteo; MET `pop` in the Nordic area
  only, `gust_max_kmh` too, and `uv_max` only in its short range).
- **`rain`**: present when a precipitation step covers now; left out
  when none does (the provider sent no steps, or the last fetch is old
  enough that every step is past), so the knob never reads missing data
  as dry. `spells` covers the next
  12 h: at most 4 `[start, end, intensity 1-3, kind]`, kind 0 rain, 1 snow
  or sleet (most of the spell's wet steps). A spell is a run of wet steps
  (≥ 0.1 mm/h; Open-Meteo 15-min steps, MET 1-h steps); gaps of ≤ 30 min
  merge; intensity is the peak. `start` may be in the past for an ongoing
  spell; ended spells are dropped when the view is built. `until` is the
  end of the spell containing now, absent when dry now and when that
  spell runs to the end of the provider's steps (its `end` is then the
  data's edge, not a forecast stop: wet until at least then). Now can
  fall in a merged dry gap: the spell is listed with `start` ≤ now <
  `end`, but `now.rain` is 0 and `until` is absent. That is a pause, not
  the end; the spell's rain resumes before `end`. A spell starting at
  now + 12 h or later is not listed. `"spells": []`
  means dry for the 12 h (as far as the provider's steps reach).
- **`days`**: the next 3 local days after today, fewer when the
  provider's range ends. `t` is local midnight. `cond`/`code` use the
  current mapping: Open-Meteo's daily `weather_code`, MET's worst symbol
  of the periods overlapping the day (storm > snow > rain > fog > clouds
  > clear; `next_1_hours` in its hourly range, `next_6_hours` beyond). `pop`
  (Open-Meteo, MET Nordic); `precip_mm10` the day's sum in tenths of mm
  (a MET period crossing midnight is split by its overlap).
- **`twilight`**: the local day's crossings, rounded to 5 min like
  `sunrise`/`sunset`. The day is the solar day around the local date's solar
  noon, not midnight to midnight: a `dusk` after local midnight (Reykjavík
  in late July) belongs to the date before. Crossings: `dawn` (−6° rising),
  `blue_end` (−4°), `golden_end` (+6°), `golden_start` (+6° setting),
  `blue_start` (−4°), `dusk` (−6°). The bands are `dawn`→`blue_end` (blue),
  `blue_end`→`golden_end` (golden) and mirrored in the evening. A crossing
  that does not happen (polar summer and winter) is left out; no location,
  no `twilight`.
- **`moon`**: `phase` 0-99, the position in the cycle (0 new, 25 first
  quarter, 50 full, 75 last quarter; waxing = `phase` < 50); `illum`
  0-100, the lit fraction. `up` is whether the moon is above the horizon
  now; read it, not the ends, because "up for days" and "down for days"
  both lack `rise` and `set`. `rise`/`set` give the current interval
  above the horizon when `up` (`rise` in the past) or the next one, to
  the minute, found within ±36 h of now. An end not found is left out
  (polar). No location: phase and lit fraction only, no `up`, `rise` or
  `set`.
- **`aqi`**: the European AQI while the air tile is on and its reading
  is fresh (and for this location: a location change clears the
  reading). Absent otherwise.

The server computes everything that needs the series or astronomy
(spells, `until`, `today`, twilight, moon, `utc_offset`); the knob owns
every trigger and threshold of its weather moments (rain starting or
stopping from `now.rain`/`rain.until`, storm and snow from `cond`, heat,
wind, frost). No raw hourly arrays: no face draws one, and each value
costs the knob's cJSON an allocation. A future series would ride a new
token.

**Size.** The example adds 769 B (compact; the whole `view_faces.json`
view is 1,096 B). The worst case (4 spells, 40-char MET codes in `days`,
`until`, every twilight crossing, the moon's `rise`, `set` and `up`,
negative temperatures and offset) is ~1.2 KB of `weather`, and the worst
whole view with every block (three 200-character fully escaped
now-playing strings, 64-character hosts) is 5.6 KB, a third of
`view_bytes` 16383 (`TestKnobViewWorstCaseFitsHalfTheBuffer` keeps it
under 8 KB). Bytes are not the constraint; the knob's cJSON allocations
are: the block goes from ~24 to ~150 nodes, which is why cinder#33
parses the view on PSRAM first.

**Cadence and ETag.** The detail changes on a fetch and at a few
boundaries (a spell ends, the 15-min rain step changes, the moon rises or
sets, the phase or lit fraction moves a percent, local midnight). Nothing
else moves with the clock: moon times come from a whole-hour scan grid,
so they don't shift with `now`.

**Old firmware** never sends the token, so it never gets the keys; a
firmware that sends it must skip keys it doesn't know inside the detail.

## Prepared cover

With `np_cover` in its caps (#412, cinder#35), a knob draws the
now-playing page from one prepared picture instead of the backdrop plus
the album circle.

- **`accent`** and **`cover_version`** are the last two keys of
  `nowplaying` (`"accent":"#6C22E6","cover_version":"fb43cf74b67a-r1"`).
  `accent` is `#RRGGBB` upper-case, like `mood.lead_color`;
  `cover_version` is `<art_version>-<recipe>`. Both are absent without the
  token and when the entry has no picture. Old firmware and caps knobs
  without the token get `nowplaying` byte-identical (`view_full.json`).
- **The pick:** the cover's picture (album, else artist) is centre-cropped
  and area-averaged to 32×32, and only the cells whose centre lies inside
  the inscribed circle count (the knob never shows the corners). Pixels
  with HSV saturation < 0.2 or value < 0.15 are skipped, and so are
  near-white ones (value > 0.95 and saturation < 0.3); the rest go into
  24 hue bins centred on 0°, 15°, … (reds either side of 0° share one),
  weighted by saturation × value. The heaviest bin's mean colour is raised
  in HSL lightness until it has at least 3:1 contrast on black (WCAG
  non-text). Under ~4 px of saturated weight it falls back to `#F4F4F2`.
  It is computed once when the picture arrives and stays fixed for the
  `art_version`.
- **`GET /v1/nowplaying/art?kind=cover&size=466&v=<cover_version>`**: a
  466 px baseline JPEG (q 80). The album picture, else the artist one,
  else 404 (the knob shows its vinyl). The recipe:
  1. centre-crop and area-average to 416 px;
  2. centre it on black 466 px, with an anti-aliased circle mask of
     r 208;
  3. a rim vignette: × 1 → 0.6 from r 170 to 208;
  4. a bottom scrim for the title: × 1 → 0.25 over the lowest 35 % of the
     disc.
  The corners are black, so the knob needs no mask of its own.
- **Caching:** `v` equal to the current `cover_version` gives
  `public, max-age=86400, immutable`; any other `v` (the bare
  `art_version` included) gives `no-cache`. A recipe bump changes
  `cover_version`, so the knob fetches a new URL instead of keeping a
  24 h immutable copy of the old recipe. The ETag
  (`"<hash>-cover-466-r1"`) carries the recipe too, so an unversioned
  revalidation after a deploy gets the new recipe rather than a 304.
- **Cost:** ~29 ms per track from a 1400 px JPEG on an M4 (decode ~18 ms
  of it), then from the 32-entry LRU. A synthetic photo-like picture
  gives ~16 KB.

## Fixtures

`cmd/ember/testdata/devices/`, written by `devices_golden_test.go` through the
real handlers and view builder, with a fixed clock and fake data (no tokens,
`192.0.2.0/24` addresses, a locally administered BSSID, made-up hosts).
Indented JSON; key order is the wire order.

| File | Content |
|---|---|
| `view_full.json` | Every block: two-host `mood` with `lead`/`hosts`/`lead_color`/`tool`, counting `pomo` (`ends_at`), `weather` with sun times and `observed_at`, `nowplaying` playing with art and volume, `diag_live_until` |
| `view_minimal.json` | Pomodoro and weather off (`null`), `nowplaying` page off (block absent) |
| `view_nowplaying_none.json` | `nowplaying` page on, nothing playing (`{"state":"none"}`); idle `pomo` (`remaining_sec`) |
| `view_quiet.json` | Quiet hours on: `"quiet":true` after `brightness`; Pomodoro and weather off |
| `view_caps_limited.json` | A knob whose caps list only `bot` and `weather`, every page on and every block live as in `view_full.json`: `pomo` and `nowplaying` left out |
| `view_faces.json` | A `wx_faces` knob (caps `bot`, `weather`), Open-Meteo through a stub provider: every detail key, two spells (one ongoing moderate rain, one snow), `until`, three days, all six twilight crossings, the moon up with `rise` in the past, `aqi`, `utc_offset` from Open-Meteo |
| `view_faces_met.json` | The same knob on MET Norway (non-Nordic: no `gust_kmh`, no `pop`), air off (no `aqi`, `utc_offset` the longitude's 0), 1-h spells, dry now (`now.rain` 0, no `until`), days from MET symbols |
| `view_faces_polar_dry.json` | Longyearbyen on 1 September: `"spells": []`, only the golden-hour crossings (the sun never gets to −4°), the moon up with no `set` within 36 h |
| `view_np_cover.json` | An `np_cover` knob (caps `bot`, `nowplaying`): playing with an album picture, `accent` lightened from a dark violet to 3:1 on black, `cover_version` |
| `view_single_host_paused.json` | One host: `source` set, `lead`/`hosts` left out; paused `pomo` with `remaining_sec`; `weather` without a location (`sunrise`/`sunset` `null`) and without a provider observation time (`observed_at` absent) |
| `checkin_req_minimal.json` | The base fields every firmware sends |
| `checkin_req_full.json` | Every block: `diag` with a crash, `ota` with `last`, `stats`, `wifi`, the display link |
| `checkin_req_caps.json` | The base fields plus `caps` (every page and feature of 0.9.41, its limits, `rotations` `[0, 180]`); not yet `wx_faces` (below) |
| `checkin_reply_current.json` | Knob up to date: `config_version` only |
| `checkin_reply_config.json` | Stale knob: `config` included |
| `checkin_reply_coredump.json` | Live mode plus `coredump_wanted` |
| `checkin_reply_ota.json` | `coredump_ack` plus an `ota` offer |
| `checkin_reply_caps.json` | Caps accepted: `config_version` and `caps_ack` |
| `checkin_reply_rotation.json` | `new_token` from a real rotation, the token replaced by a dummy of the same length |
| `pomodoro_action.json` | A Pomodoro action reply (pause during a short break) |
| `config_default.json` | `GET …/self/config` for a new knob, `quiet` at its defaults (`calm` true, `dim_level` 20), `display.rotation` 0 |
| `config_custom.json` | Every setting changed (`quiet` calm off, `dim_level` 5, `display.rotation` 180), plus an unknown page id the knob must keep |

The test also compares what the server stores for `checkin_req_full.json`
(the checkin record and the stats sample) with every value sent, so a request
field the server stops reading fails it. `TestDeviceViewHeadersContract` pins
the view and checkin header names and the 304 behaviour. Regenerate after an
intended change with `go test ./cmd/ember -run 'Golden|Caps|LegacyFirmware' -update`
and review the diff.

**cinder side** (cinder#24): `firmware/test/host/fixtures/ember/` holds a copy
pinned to an Ember tag or commit, refreshed by
`firmware/tools/sync-ember-fixtures.sh <ref>`. The host test
[`firmware/test/host/test_fixtures.c`](https://github.com/tarakanof/cinder/blob/main/firmware/test/host/test_fixtures.c)
parses every fixture with the firmware's own parsers, fails on a fixture it
has no test for, and rebuilds each `checkin_req_*.json` body with the
firmware's encoder; that file is the list of which parser covers which
fixture. A sync is a reviewed commit, so its diff is the contract change. No
submodule: the pinned copy keeps cinder's CI hermetic.

**New checkin request fields.** The rebuilt body must match the fixture key
for key and value for value, except for paths in `NOT_YET_SENT[]` in
`test_fixtures.c` (such as `".diag.foo"`). The flow:

1. The Ember PR adds the field to the server and to the `checkin_req_*`
   fixture (regenerated, not hand-edited).
2. The cinder sync commit for that ref adds the field's path to
   `NOT_YET_SENT`, since the firmware doesn't send it yet and the host test
   fails without the entry.
3. The firmware PR that starts sending the field removes the entry.

`NOT_YET_SENT` matches object keys only: `body_diff` compares an array
such as `caps.features` whole (`cJSON_Compare`), so it cannot exempt one
new feature token. `checkin_req_caps.json` therefore stays at 0.9.41's
caps until the firmware sends `wx_faces`; the cinder PR that adds the
token either teaches the host test an element exemption or lands with an
Ember PR that adds the token to the fixture, synced in the same step.

The host test fails on a listed path the firmware sends, on a listed path no
fixture has, and on a key the firmware sends that the fixture lacks, so the
firmware never sends a field before Ember's goldens have it. cinder's
`docs/workflow.md` has the same rules from its side.
