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
| `GET /v1/devices/self/view` | The one poll: `v`, `epoch`, `config_version`, `mood`, `pomo`, `weather` (with reported caps: only for pages in `caps.pages` and on, "Capabilities"), `brightness`, then `quiet` (only while quiet hours are on), `nowplaying` (only with the `nowplaying` page on) and `diag_live_until` (live mode only). Strong `ETag`, `If-None-Match` answers 304. `?wait=N` long-polls up to the `X-Ember-View-Wait` cap (25 s). A 200 or 304 carries `X-Ember-Now` (server Unix seconds), the anchor for `ends_at` and `position_at`, and `X-Ember-View-Wait`; error replies (400, 401, 429) carry neither. |
| `GET /v1/devices/self/config` | `{config_version, config}`, the same pair a stale checkin carries. |
| `PUT /v1/devices/self/coredump?id=` | The core dump a checkin asked for. |
| `GET /v1/devices/self/firmware/{version}` | The image the checkin's `ota` offer names. |
| `POST /v1/pomodoro/{start,pause,resume,stop,skip}` | Pomodoro actions; the reply is the timer status (`pomodoro_action.json`). |
| `POST /v1/nowplaying/control` | Player control, optional `Idempotency-Key`. |
| `GET /v1/nowplaying/art?kind=&size=&v=` | Art for the view's `art_version`: `kind` `album`/`artist`/`backdrop`, `size` the square edge in px from a fixed list per kind (album 240 or 120, artist 64 or 120, backdrop 466; default the first, anything else 400), `v` the `art_version` (public, no token). |

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
   are `null` without a location or in polar day/night.
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
  "limits": {"view_bytes": 16383, "config_bytes": 1024}
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
  `config_bytes` ≥ the default config's compact size (434 B today; it
  follows `defaultKnobSettings`), so the knob can always hold a view and
  the config a new record starts with.
- Order carries no meaning. The server sorts `features`; `pages` are
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
  `limits.view_bytes` drops `nowplaying`, then `weather`, then `pomo`;
  `mood` stays. If it is still over, the server serves it and logs once per
  device (`knob view over its caps view_bytes`) until it fits again.
- **Config PUT** (owner): 400 when it turns on a page that is not in
  `caps.pages` (a page that was already on stays valid, and ids that are
  off, unknown ones included, are kept as before for the NVS round trip),
  and 400 when the changed config's compact JSON exceeds
  `limits.config_bytes` and is larger than the stored one (a config
  already over the limit can still shrink). A no-op PUT is never rejected.

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

Page ids in `caps.pages` need no registry: they are the config's page ids;
Ember.app lists a knob app only when its page is in `effective_caps.pages`.

**Compatibility matrix** (`devices_caps_test.go` unless noted):

| Server | Firmware | Expected | Test |
|---|---|---|---|
| new | legacy (no caps) | `effective_caps` from the table; view and ETag unchanged; no new 400s | `TestLegacyFirmwareViewIsUnchanged` (fw 0.8.3 and 0.9.41 against `view_full.json`), `TestLegacyKnobAlwaysGetsMood`, `TestLegacyCapsTable`, `TestLegacyKnobConfigPutIsNotCheckedAgainstTheTable` |
| new | new | caps stored and acked; view filtered by caps ∩ pages | `TestCapsKnobViewIsFilteredByCapsAndPages` (`view_caps_limited.json`), `TestDeviceCheckinCapsGolden`, `TestCapsAreStoredExposedAndClearedOnDowngrade`, `TestCapsCheckinWritesStoreOnlyWhenCapsChange`, `TestReorderedCapsDoNotWrite`, `TestCapsLimitFloors`, `TestInvalidCapsAreExposedOnceWithoutRewrites`; cinder host test: a view with no page block, `mood` included, parses |
| old | new | no `caps_ack`; the knob behaves as today | cinder host test with the pre-caps fixtures; Ember side: a reply without accepted caps has no `caps_ack` (`TestInvalidCapsAreDroppedWithoutAck`) |
| new | new, `v` outside range | server sends `v` = min(1, `caps.view[1]`); the knob keeps its last view and shows the update state | `TestCapsKnobViewMajorIsTheLowerOfServerAndFirmware`; cinder host test with a `v:99` fixture |
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
  default config is 434 B and the largest valid one 648 B, under cinder's
  `CFG_SETTINGS_MAX` (1024 B), which `TestKnobConfigFitsTheFirmwareStore`
  pins.

## Fixtures

`cmd/ember/testdata/devices/`, written by `devices_golden_test.go` through the
real handlers and view builder, with a fixed clock and fake data (no tokens,
`192.0.2.0/24` addresses, a locally administered BSSID, made-up hosts).
Indented JSON; key order is the wire order.

| File | Content |
|---|---|
| `view_full.json` | Every block: two-host `mood` with `lead`/`hosts`/`lead_color`/`tool`, counting `pomo` (`ends_at`), `weather` with sun times, `nowplaying` playing with art and volume, `diag_live_until` |
| `view_minimal.json` | Pomodoro and weather off (`null`), `nowplaying` page off (block absent) |
| `view_nowplaying_none.json` | `nowplaying` page on, nothing playing (`{"state":"none"}`); idle `pomo` (`remaining_sec`) |
| `view_quiet.json` | Quiet hours on: `"quiet":true` after `brightness`; Pomodoro and weather off |
| `view_caps_limited.json` | A knob whose caps list only `bot` and `weather`, every page on and every block live as in `view_full.json`: `pomo` and `nowplaying` left out |
| `view_single_host_paused.json` | One host: `source` set, `lead`/`hosts` left out; paused `pomo` with `remaining_sec`; `weather` without a location (`sunrise`/`sunset` `null`) |
| `checkin_req_minimal.json` | The base fields every firmware sends |
| `checkin_req_full.json` | Every block: `diag` with a crash, `ota` with `last`, `stats`, `wifi`, the display link |
| `checkin_req_caps.json` | The base fields plus `caps` (every page and feature of 0.9.41, its limits) |
| `checkin_reply_current.json` | Knob up to date: `config_version` only |
| `checkin_reply_config.json` | Stale knob: `config` included |
| `checkin_reply_coredump.json` | Live mode plus `coredump_wanted` |
| `checkin_reply_ota.json` | `coredump_ack` plus an `ota` offer |
| `checkin_reply_caps.json` | Caps accepted: `config_version` and `caps_ack` |
| `checkin_reply_rotation.json` | `new_token` from a real rotation, the token replaced by a dummy of the same length |
| `pomodoro_action.json` | A Pomodoro action reply (pause during a short break) |
| `config_default.json` | `GET …/self/config` for a new knob, `quiet` at its defaults (`calm` true, `dim_level` 20) |
| `config_custom.json` | Every setting changed (`quiet` calm off, `dim_level` 5), plus an unknown page id the knob must keep |

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

The host test fails on a listed path the firmware sends, on a listed path no
fixture has, and on a key the firmware sends that the fixture lacks, so the
firmware never sends a field before Ember's goldens have it. cinder's
`docs/workflow.md` has the same rules from its side.
