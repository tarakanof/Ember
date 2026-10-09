# Device protocol

The contract between Ember (the owner) and the knob firmware, cinder (the
consumer, `tarakanof/cinder`). Shapes and auth live in
[`openapi.yaml`](openapi.yaml) (`device-self` and `control` tags); behaviour in
[`ARCHITECTURE.md`](ARCHITECTURE.md) "Wire protocol", "Knob diagnostics" and
"Knob firmware updates". This file holds the rules for changing them and the
fixtures both repos test against. Design: `Specs/ember/2026-10-09-device-protocol-capabilities-design.md`
in the vault.

## Scope

Every route the knob calls, all with its device token (`ekd_…`):

| Route | What |
|---|---|
| `POST /v1/devices/self/checkin` | Health report in; reply carries `config_version`, the `config` when the knob's is stale, `new_token` during a rotation, `diag_live_until`, `coredump_wanted`/`coredump_ack`, an `ota` offer. `X-Ember-Now` header. |
| `GET /v1/devices/self/view` | The one poll: `v`, `epoch`, `config_version`, `mood`, `pomo`, `weather`, `brightness`, then `nowplaying` (only with the `nowplaying` page on) and `diag_live_until` (live mode only). Strong `ETag`, `If-None-Match` answers 304. `?wait=N` long-polls up to the `X-Ember-View-Wait` cap (25 s). Every answer, 304 included, carries `X-Ember-Now` (server Unix seconds), the anchor for `ends_at` and `position_at`. |
| `GET /v1/devices/self/config` | `{config_version, config}`, the same pair a stale checkin carries. |
| `PUT /v1/devices/self/coredump?id=` | The core dump a checkin asked for. |
| `GET /v1/devices/self/firmware/{version}` | The image the checkin's `ota` offer names. |
| `POST /v1/pomodoro/{start,pause,resume,stop,skip}` | Pomodoro actions. |
| `POST /v1/nowplaying/control` | Player control, optional `Idempotency-Key`. |
| `GET /v1/nowplaying/art?kind=&v=` | Art for the view's `art_version` (public, no token). |

The knob learns of a config change or a rotation when the view's `epoch` or
`config_version` moves, then checks in.

## Contract rules

1. **Additive by default.** A new field is optional on both sides. Receivers
   ignore fields they don't know: the server decodes checkins leniently and
   cinder's parsers skip unknown keys. A sender leaves out a block that does
   not apply rather than sending `null`. Existing exception, kept: the view's
   `pomo` and `weather` are `null` when off.
2. **Removing or retyping a field is breaking.** It needs a new view major
   `v` (today always `1`). The server keeps serving the old major to any
   device that doesn't ask for the new one, for at least two releases.
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
| `checkin_req_minimal.json` | The base fields every firmware sends |
| `checkin_req_full.json` | Every block: `diag` with a crash, `ota`, `stats`, `wifi`, the display link |
| `checkin_reply_current.json` | Knob up to date: `config_version` only |
| `checkin_reply_config.json` | Stale knob: `config` included |
| `checkin_reply_coredump.json` | Live mode plus `coredump_wanted` |
| `checkin_reply_ota.json` | `coredump_ack` plus an `ota` offer |
| `config_default.json` | `GET …/self/config` for a new knob |
| `config_custom.json` | Every setting changed, plus an unknown page id the knob must keep |

The test also checks the server stores every block of `checkin_req_full.json`,
so a request field the server stops reading fails it. Regenerate after an
intended change with `go test ./cmd/ember -run 'TestDevice.*Golden' -update`
and review the diff.

**cinder side** (cinder#24): `firmware/test/host/fixtures/ember/` holds a copy
pinned to an Ember tag or commit, refreshed by
`tools/sync-ember-fixtures.sh <ref>`. Host tests parse every fixture with the
real `knob_view` and `device_api` parsers. A sync is a reviewed commit, so its
diff is the contract change. No submodule: the pinned copy keeps cinder's CI
hermetic.
