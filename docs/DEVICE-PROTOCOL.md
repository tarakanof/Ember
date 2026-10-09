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
| `POST /v1/devices/self/checkin` | Health report in; reply carries `config_version`, the `config` when the knob's is stale, `new_token` during a rotation, `diag_live_until`, `coredump_wanted`/`coredump_ack`, an `ota` offer. A 200 carries `X-Ember-Now`. |
| `GET /v1/devices/self/view` | The one poll: `v`, `epoch`, `config_version`, `mood`, `pomo`, `weather`, `brightness`, then `nowplaying` (only with the `nowplaying` page on) and `diag_live_until` (live mode only). Strong `ETag`, `If-None-Match` answers 304. `?wait=N` long-polls up to the `X-Ember-View-Wait` cap (25 s). A 200 or 304 carries `X-Ember-Now` (server Unix seconds), the anchor for `ends_at` and `position_at`, and `X-Ember-View-Wait`; error replies (400, 401, 429) carry neither. |
| `GET /v1/devices/self/config` | `{config_version, config}`, the same pair a stale checkin carries. |
| `PUT /v1/devices/self/coredump?id=` | The core dump a checkin asked for. |
| `GET /v1/devices/self/firmware/{version}` | The image the checkin's `ota` offer names. |
| `POST /v1/pomodoro/{start,pause,resume,stop,skip}` | Pomodoro actions; the reply is the timer status (`pomodoro_action.json`). |
| `POST /v1/nowplaying/control` | Player control, optional `Idempotency-Key`. |
| `GET /v1/nowplaying/art?kind=&size=&v=` | Art for the view's `art_version`: `kind` `album`/`artist`/`backdrop`, `size` the square edge in px from a fixed list per kind (album 240 or 120, artist 64 or 120, backdrop 466; default the first, anything else 400), `v` the `art_version` (public, no token). |

The knob learns of a config change or a rotation when the view's `epoch` or
`config_version` moves, then checks in.

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
   and OTA only. Current exception: Ember.app still gates now-playing and the
   stats intervals on the knob's fw semver (`KnobModels.swift`); capability
   negotiation (`caps` in the checkin) is planned in #341 and replaces it.
4. **Actions are idempotent or take `Idempotency-Key`.** A knob never replays
   a press made while offline. Today `pause`, `resume` and `stop` are
   idempotent and `nowplaying/control` dedupes on its key; `start` restarts
   the phase and `skip` advances again, so a knob must not retry those
   blindly. A new action route follows the rule.
5. **Goldens are the contract.** Any change to the view, checkin or config
   shape updates the fixtures below in the same PR, and the PR says so, so
   cinder can sync them.

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
| `view_single_host_paused.json` | One host: `source` set, `lead`/`hosts` left out; paused `pomo` with `remaining_sec`; `weather` without a location (`sunrise`/`sunset` `null`) |
| `checkin_req_minimal.json` | The base fields every firmware sends |
| `checkin_req_full.json` | Every block: `diag` with a crash, `ota` with `last`, `stats`, `wifi`, the display link |
| `checkin_reply_current.json` | Knob up to date: `config_version` only |
| `checkin_reply_config.json` | Stale knob: `config` included |
| `checkin_reply_coredump.json` | Live mode plus `coredump_wanted` |
| `checkin_reply_ota.json` | `coredump_ack` plus an `ota` offer |
| `checkin_reply_rotation.json` | `new_token` from a real rotation, the token replaced by a dummy of the same length |
| `pomodoro_action.json` | A Pomodoro action reply (pause during a short break) |
| `config_default.json` | `GET …/self/config` for a new knob |
| `config_custom.json` | Every setting changed, plus an unknown page id the knob must keep |

The test also compares what the server stores for `checkin_req_full.json`
(the checkin record and the stats sample) with every value sent, so a request
field the server stops reading fails it. `TestDeviceViewHeadersContract` pins
the view and checkin header names and the 304 behaviour. Regenerate after an
intended change with `go test ./cmd/ember -run 'TestDevice.*Golden' -update`
and review the diff.

**cinder side** (cinder#24): `firmware/test/host/fixtures/ember/` holds a copy
pinned to an Ember tag or commit, refreshed by
`tools/sync-ember-fixtures.sh <ref>`. Host tests parse every fixture with the
real `knob_view` and `device_api` parsers. A sync is a reviewed commit, so its
diff is the contract change. No submodule: the pinned copy keeps cinder's CI
hermetic.
