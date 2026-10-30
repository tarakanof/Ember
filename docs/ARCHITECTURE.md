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
  Knob device registry (`/v1/devices`, own device tokens — see "Device
  registry" below). Operator/introspection: `/admin/doctor`, `/admin/reload`, `/version`,
  `/metrics` (hand-rolled Prometheus, no client lib). Pomodoro, Weather
  (`GET/PUT /v1/weather/config`), and Reminders (`POST /v1/reminders/fire`)
  endpoints — see below.
- **Session model & staleness.** Each session is keyed by `(source, tool,
  session)`. Per-state staleness: `stale_seconds` (default 300) for
  running/waiting/idle, a 30 s `done_ttl_seconds` linger for done/error.
  Sessions are reaped when stale. The session registry (`internal/sessions`)
  owns the map, this policy (read from the live config on every call), the
  `UpdatedAt` stamp and the winner/count view, on an injected clock. Every
  registry access (upsert, delete, `/state`, the coordinator tick, `/metrics`,
  `/admin/doctor`) reaps first, so no reader ever sees a stale session and
  reaping doesn't depend on anything rendering. Each reap logs `session reaped`
  and bumps `ember_sessions_evicted_total`.
- **Mood state** (`cmd/ember/mood_state.go`, #338). The agent-session readers
  go through one projection of a registry view, `App.moodState()` →
  `moodState` (`newMoodState(view, idleText)` for tests): the sessions, the
  legacy `render` (counts, winner text, colour, `source`/`tool`) and the
  `moodLead` (who leads the winning state, see "Knob view"). It knows nothing
  about devices. `App.Snapshot()` (`GET /state`, the coordinator, the
  preview) is its `snapshot()`; the knob's `mood` is its render plus lead;
  `Upsert` notifies when either moves. The clock's hidden apps are not part
  of it: the coordinator filters its own copy of the sessions
  (`filteredSnapshot`), and the knob, `/state` and the render counters stay
  unfiltered (user decision 2026-10-09: each device keeps its own app list).
- **Attention state** (`cmd/ember/attention_state.go`, #351). Which session
  holds attention, since when, and why it ends, as app state with no device
  knowledge. `attentionState.onTransition(key, prior, next)` decides a status
  edge: `attentionAcquire` (a session entered waiting/error from outside it,
  which steals any current hold), `attentionRenew` (a waiting↔error shift on
  the holder), `attentionDrain` (the holder left waiting/error) or
  `attentionKeep`. `ended(active, sessions, now, ackTimeout)` says whether the
  hold is over: `reap` (the holder is not active), `drain` (it left
  waiting/error) or `ack_timeout`, checked in that order. It never reads the
  clock's hidden apps. The coordinator owns the clock's instance (embedded,
  so the fields are `c.locked`/`c.lockedKey`/`c.lockEnteredAt`) and applies
  the clock gate when it maps a decision to the device: an acquire for a
  hidden tool (`keyHidden`) is dropped, so no pointer jump, lock or chime, and
  `ended` reads the clock's `filteredSnapshot`, so hiding the locked tool
  releases the hold as `reap`. A knob that wants attention later builds its
  own instance from its own app list. The rendering (pointer, card cursor,
  ack timer, chime, hold, corner LED) stays in the coordinator.
- **Render priority.** `waiting > error > running > done`; `idle` never wins
  (it cedes the slot, publishing nothing). For ≥2 sessions in the winning group,
  an aggregate label is shown. One Go ordering, `render.StatePriority`: the
  `/state` render and the preview pick their winner with `render.PickWinning`
  (most recent within a state: the later `state_since`, else the later
  `updated_at`), while the clock's rotation and session bar
  order by it via `render.SortedActiveKeys` (waiting sessions newest
  `state_since` first, #485; other ties by source/tool/session). The
  menu's Swift `pickWinning` is a port of `PickWinning`;
  `TestPickWinningTable` is the case table a Swift test should mirror.
- **Session identity (#485).** `state_since` is set by the registry when a
  session enters a state and kept by a same-state repost, so the 10 s
  heartbeats never reorder two waiting sessions. `project` (optional, ≤32
  characters) names the session: the cwd basename from the Claude and Codex
  producers (none for a session in `$HOME`, which would name the user), the
  thread title from T3 only while its activity trail is on (titles are often
  prompt text and `/state` has no auth), none with `EMBER_SEND_PROJECT=0`
  or from older producers. The server drops control characters, collapses
  whitespace and keeps 32 characters. `POST /v1/status` has always ignored
  unknown fields, but should a server answer 400 naming `project` as an
  unknown field, `producer.Client` resends without it and leaves it out
  for an hour (per process, so each Claude hook retries once; a POST in
  flight when another dropped it is resent too). The display name (`render.DisplayName`, Swift
  `displayName`) is the project, prefixed with the host only when more than
  one host has a non-idle session: `M5/learn-language` on the clock,
  `m5 · learn-language` in the app. Without a project every surface keeps
  its old host/tool text.
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
  restarts and `/admin/reload`) — one of the settings-overlay registrations
  (see "Runtime settings overlay" below).
- **Coordinator ownership and locking.** The coordinator goroutine owns every
  write to the rotation: the session app, tiles, corner LEDs and the display
  hold. One-shot popups (`/v1/notify`, reminders, weather, air, sun and
  meeting popups, the Pomodoro phase-end alert, the usage alarm) are
  **notices**: app code builds a `notice` and hands it to the clock adapter
  (`coordinator_notices.go`, see "Notices" below), which is the only code that
  calls `Publisher.Notify`. The menu's `/v1/device` proxy uses `clockAccess`.
  - **Channels.** State-change commands (upsert/delete/clear/shutdown) use a
    64-slot channel; ticks use a 1-slot channel that drops on full, because a
    stale tick carries no information. `Send` never blocks: the goroutine can
    sit tens of seconds inside `onTick` against an unreachable clock, and a
    blocking `Send` would wedge the producer HTTP handlers. A dropped command
    self-heals except a fresh attention upsert, because lock acquisition is
    edge-triggered and lives only in `onUpsert` (a heartbeat re-POST of a
    waiting session is waiting→waiting, no transition); that edge's preempt and
    chime are lost until the session's next transition. The drop warning is
    throttled to about one a minute. `Run` drains the command channel before
    ticks (Go's `select` is random when both are ready) so preempt latency
    beats rotation jitter.
  - **`stateMu`** guards the pointer, `cardCursor`, the lock fields and
    `idleSince`. Only the coordinator goroutine writes them, taking `stateMu`
    for each write, so its own reads skip the lock; every other goroutine must
    `RLock`. The idle usage-face cursor shares `cardCursor`. The attention
    decisions (`onTransition`, `ended`) and `keyHidden` run on the coordinator
    goroutine before `stateMu` is taken; only applying them
    (`applyAttentionLocked`, `releaseLockLocked`: lock fields, pointer, cursor
    and the `lockReleaseTimer`) happens under it. `keyHidden` takes the App's
    hidden-apps lock, so it is never called with `stateMu` held, and the
    `time.AfterFunc` callback only `Send`s a tick, never touching `stateMu`.
  - **Attention lock release.** The decision is `attentionState.ended` (see
    "Attention state"). A lock ends on ack timeout (`ackTimeoutDur`,
    read live), on drain (the locked session left the attention state;
    released at once, not at the next dwell), or on reap (the locked key is no
    longer active). A waiting↔error shift on the locked session resets the ack
    timer without re-targeting the pointer or re-chiming. `lockReleaseTimer` is
    a wallclock safety net that fires a tick after the hold, so release still
    happens when dwell is configured longer than the hold; it is armed with the
    value at arm time, so after a config PUT shortening the hold the tick-driven
    check releases promptly and the timer may fire later. The corner-LED
    attention indicator follows the lock, not any waiting session.
  - **Republish.** `onRepublish` forgets everything believed to be on the
    device: push dedupe, the tiles ledger, corner LEDs and `c.hold`, because
    pushed apps are RAM-only. Resetting `c.hold` makes the next
    `applyDisplayHold` a fresh edge, so an in-flight focus block or attention
    hold re-asserts its forced switch and settings.
  - **Shutdown.** `Run` undoes an active Pomodoro takeover with a fresh context
    (the run context is already cancelled), and `StartCoordinator` returns only
    after that restore finishes so `main` can wait for it. If the clock is
    unreachable the snapshot stays in the store and the next start restores it.
- **Rotating tiles — one module** (`cmd/ember/coordinator_tiles.go`, #145).
  The standalone apps Ember owns (`ember-weather`, `ember-forecast`,
  `ember-air`, `ember-meet`) are one `tile` value each in `tiles`: app name,
  preview card, `toggle` (the tile's own switch), `live` (device-only gate:
  feature on, data fresh, meeting inside the lead window) and `view`, which
  resolves the content once into both the device payload and the preview
  frame (built lazily, so device ticks don't render it). `tileSet` owns the pushed-app ledger (`pushedApp{body, at}`, the same
  type as the main app's dedupe): `adopt` seeds it from the device loop on the
  first reachable tick (tiles plus legacy `ember-usage-*` leftovers, never the
  base app), `reconcile` runs every tick — clear anything tracked that isn't
  wanted, push a wanted tile when its bytes changed or `usageRefreshInterval`
  passed, move the ledger only on success — and `forget` drops it on
  `cmdRepublish`. Writes go through the coordinator's `tileWriter` adapter
  (`pushApp` retry, `ClearApp`), so the coordinator stays the single writer.
  The previews call `previewTiles` with draft inputs, which renders the same
  `view` minus the `live` gate: **preview = pushed frame by construction**
  (pinned by `tile_preview_parity_test.go`). Payload-only, since the canvas
  can't animate them: the NG overlay and a native gallery icon. A tile over
  existing inputs is one `tile` value; one with a new data source also adds
  its fields to `tileInputs` and fills them in `coordinator.tileInputs` (and
  in its preview handler). A zero-value `pushedApp` means "on device, content
  unknown" (adopted after a restart): it is never current, so it is re-pushed
  or cleared. `forget` runs after a reboot so a stale ledger cannot suppress a
  re-push for a whole refresh interval. Freshness: `usageAppLifetime` outlives
  the ~5 min producer refresh so a reconcile gap never blanks the usage app;
  `usageStaleTTL` (about twice the poll) clears a silent tool's apps;
  `usageRefreshInterval` (below the lifetime) re-pushes an unchanged usage app
  so the device doesn't evict it. `usageViews` reads the usage state (see "AI
  usage card"), the same one the limit alarm reads; hidden and
  below-threshold tools are absent. Weather tiles are live only
  while the weather state is fresh (`weatherStaleAfter`, 30 min, see
  "Weather"), so a wedged poller leaves no stale temperature. The meeting countdown is the ceiling of the remaining time (at
  least 1) and needs no timer: the text changes each minute, so the bytes diff
  re-pushes. The device renders that text natively while the preview draws
  `font3x5`.
- **Clock access — one module** (`cmd/ember/clock_access.go`, #146). Every
  server→clock call goes through `clockAccess`. It resolves the clock from the
  live config on every call (`Config.clockURL()`, see "Runtime settings
  overlay"), so a rediscovery swap or `PUT /v1/device/config`
  applies to the next request. It holds the URL to the same `validDeviceURL`
  rule used wherever a URL is stored, and gives each call class one timeout:
  `callPublish` = `awtrix.timeout_seconds`, `callMenu` 8 s, `callProbe`
  1.5 s, `callCapabilities` 2 s, `callDoctor` = the config's or 2 s. It is
  the only `awtrix.NewClient` site in `cmd/ember`. It also owns:
  - the retry rule (`retryClockCall`, `retryableClockErr`: transport, 5xx and
    429 retry, any other 4xx is final);
  - the menu error map (`writeClockError`: a refusal is relayed with its NG
    envelope, anything else is 502);
  - the `/api/v1/system` read-merge-PUT (`updateSystem`, serialised; a
    waiter whose request is cancelled or out of budget stops waiting);
  - the write budget (`clockWriteBudget`, 25 s, #187) for the menu handlers
    that chain clock calls behind a lock. Unbounded, sensors and buttons PUT
    stack to 40 s (16 s `systemLock` wait, 8 s read, 8 s PUT, 8 s re-read)
    and settings PUT to 32 s (8 s `priorMu` wait, 8 s PATCH, 8 s `priorMu`
    again, 8 s reconcile PATCH), past the server's 30 s `WriteTimeout`,
    which doesn't stop a handler but drops its late answer. Under the budget
    every lock wait and call shares 25 s, leaving 5 s of the `WriteTimeout`
    for the request body and the answer. Out of budget, the handler answers
    504 in the clock error shape: `{"error":"clock didn't finish within 25s:
    …","code":"clock_timeout","write":…}`, where `write` is `not_sent`
    (budget spent in a lock wait or the read: the clock is unchanged),
    `unknown` (a write went out unanswered: it may have landed) or `applied`
    (the settings PATCH landed but its Pomodoro reconcile didn't run). A
    sensors/buttons PUT whose write landed but whose re-read ran out answers
    200 from the object it wrote. The app maps the 504 to
    `APIError.clockTimedOut` and shows "The clock didn't finish in time."
    with the fate ("Nothing was changed." / "The change may not have been
    saved." / "Saved, but not fully applied yet.").
  - the read budget (`clockReadBudget`, 25 s, #190) for `GET
    /v1/device/settings`, which reads the takeover snapshot under `priorMu`
    before and after its settings read: 8 s wait, 8 s read, 8 s wait with one
    holder each side, plus 8 s per edit queued ahead. Both waits and the read
    share 25 s; out of it the handler answers the same 504 without `write`
    (`{"error":"clock didn't finish within 25s","code":"clock_timeout"}`),
    which the app maps to `clockTimedOut(nil)` and shows as "The clock
    didn't finish in time." with no fate. The other device GETs make one
    clock call and take no lock, so `callMenu`'s 8 s bounds them.

  The app's side of these budgets is `RequestBudget` in EmberKit's
  `APIClient` (request / whole-request timeout): `.server` 5 s / 10 s for
  server-only work (`/healthz`, `/state`, settings), so a dead server shows
  fast; `.clock` 12 s / 15 s for one clock call through the server (above
  `callMenu`'s 8 s, and discovery's ~8 s); `.clockLong` 35 s / 40 s for
  requests that chain clock calls or wait on a lock first (sensors/buttons
  read-merge-PUT plus re-read, device settings read or edit behind the
  takeover snapshot's lock, a reminder fire). `.clockLong` sits above both
  the server's 25 s write and read budgets, so sensors/buttons/settings PUT
  and settings GET answer (504 at worst) before the app gives up, and its
  30 s `WriteTimeout`. Should any other `.clockLong` handler run past the
  `WriteTimeout`, the server drops the connection, which the app reports
  (`networkConnectionLost` under `.clockLong`) as a timeout, not
  "unreachable". `DeviceService` picks a budget per call
  (pinned by `RequestBudgetTests`); change a server budget here, check the
  app's. A timeout (`URLError.timedOut`) maps to
  `APIError`/`FeedError.timedOut`, shown as "The server didn't answer in
  time" / "not responding"; "unreachable" stays for requests that never
  left (refused, no route, DNS). A dead or powered-off host usually times
  out rather than refusing, so it now reads "not responding" too. A Local
  Network refusal is checked first and wins.

  Server-initiated writes cross the `Publisher` seam. Its real adapter,
  `clockPublisher` (one field: the `clockAccess`), is built only by
  `NewApp(cfg, nil, …)`. Tests pass a fake instead. Sound policy (the notice
  adapter) and the coordinator's retries sit above that seam, so a fake sees
  exactly what the clock would. `a.clock` has no `Notify`/`PlayRTTTL`, and
  `Publisher.Notify`/`DismissNotifyByName`/`PlayRTTTL` are called only from
  `coordinator_notices.go`, so nothing can sound the clock past the quiet
  gate. `clock_access_guard_test.go` checks these rules on the type-checked
  package.

  The `Publisher` is the seam for every server-initiated clock write.
  `clockPublisher` builds a fresh client per call, so a rediscovery URL swap
  applies to the next write. Pushed apps are RAM-only on NG; on startup
  `ListApps` adopts Ember-managed apps left from a prior run so they can be
  reconciled or cleared although the in-memory push trackers start empty.
  `PlayRTTTL` is only for chimes with no notification of their own (the
  attention lock); popups carry their melody in `soundRtttl` (on NG 1.2,
  `"sound":{"rtttl":…}`; see the awtrix-ng gotchas). Every
  notification Ember pushes carries a `name` (NG's queue holds 32), so
  `DELETE /api/v1/notifications/{name}` retracts Ember's own popup rather than
  whatever is showing; a 404 on dismiss-by-name means it is already gone
  (the firmware's button handling often clears it first) and is the expected
  outcome (`isAPINotFound`).

  Further `clockAccess` constraints:
  - **Clients and classes.** Each call builds a fresh `awtrix` client; clients
    share `http.DefaultTransport`, so the keep-alive pool survives. Besides
    `callPublish` and `callMenu`, `callProbe` serves the watch loop,
    rediscovery and doctor's clock check (it must not outlive its watch tick),
    `callCapabilities` the startup/rediscovery fetch (a dark clock must not
    delay boot) and `callDoctor` also runs offline against a bare config. A
    caller's context can only shorten a class timeout. `reachable(base)` takes
    an explicit base because rediscovery probes the URL it is about to judge.
    It reads `/api/v1/device` through the same `connect` seam as every other
    call and checks the NG fingerprint (`discovery.IsNG`). Tests stub that seam
    and `browseFn` together (`stubRemoteClock`): a non-loopback host answers as
    a fingerprinted clock, a `.invalid` host fails like a dead one, and mDNS
    finds nothing. `TestMain` wraps `http.DefaultTransport` once, before any
    test runs, in a guard that refuses and reports any non-loopback request; no
    test swaps it afterwards, since goroutines leaked from earlier tests still
    read it (#594, pinned by `TestOnlyTestMainWritesTheDefaultTransport`). The
    guard sees only clients on `DefaultTransport`; a client with its own
    `Transport` bypasses it, and mDNS (UDP) is covered by the stubbed
    `browseFn`, not the guard. `NewApp` defaults `browseFn` to a no-LAN browse
    that returns `errBrowseNotWired`. `newServerApp`, which `main` calls, is the
    one place that wires `discovery.BrowseAWTRIX` (pinned by
    `TestNewServerApp_WiresRealClockBrowse`), so a test that builds its App with
    `NewApp` cannot reach the LAN through it. A test that needs candidates stubs
    `browseFn`. A cancelled browse has one outcome: `BrowseAWTRIX` (after mDNS
    and after the probes; a probe on a cancelled ctx sends nothing) and
    `BrowseEmber` return `ctx.Err()` and no results, never a partial list. The
    UDP reads (the clock's `FIND_AWTRIXNG` and the `_ember._tcp` legacy unicast
    query) end on cancel, so a shutdown or a client hang-up never waits out a
    3 s browse. `rediscoverClock` then records `cancelled`, not `no-device`, and
    `/v1/device/discover` writes no response to a client that went away.
  - **`systemLock`.** `/api/v1/system` holds Wi-Fi credentials, sensor offsets
    and the button callback, and NG only offers a full replace, so two
    unserialised writers lose a write. The lock is a `ctxLock` (a channel-of-1
    mutex whose waiters can give up; `Unlock` on an unheld lock panics like
    `sync.Mutex`) so a stuck clock cannot trap cancelled requests; a holder can
    take two menu calls (16 s). `updateSystem` is always a full
    read-merge-write, never a partial PUT: a partial PUT that the firmware
    treated as a replace would drop the stored Wi-Fi password, whatever the
    docs say about partial support.
  - **Write fate.** A write that failed after sending is wrapped as
    `sentWriteError` ("may have landed"); one that never went out (context
    already ended) returns `ctx.Err()` unsent. In `writeBudgetError` an
    unanswered write wins over an earlier landed one: a settings edit whose
    first PATCH landed and whose reconcile re-write went unanswered does not
    know the clock's value. `readBudgetError` is the same 504 without `write`.
  - **Error map.** Only `*awtrix.APIError` is relayed (`writeDeviceAPIError`).
    Request errors (bad value, unknown key, missing app, wrong media type) and
    a busy or absent-hardware 503 pass through so the caller can tell "refused"
    from "broken"; everything else is 502, and a device 401/403 must never read
    as the menu's own bearer token being wrong. NG's envelope
    `{"error":{code,message,field}}` is flattened to the server shape (`error`
    stays a string) with `code`/`field` beside it; a raw non-envelope body is
    capped at 200 runes without splitting a rune.
  - **Retry rule.** 5xx and 429 are transient because this clock watchdog-resets
    and runs its HTTP server on the task that drives the panel (a 503 while busy
    is transient); other 4xx (422, 413) answer identically forever. `retryDevice`
    uses the smaller of `publishAttemptTimeout` and `awtrix.timeout_seconds`.
  - **Helpers.** `raw` returns non-2xx verbatim for callers that give a status
    meaning (a 404 script is "absent"); `fetch` turns non-2xx into
    `*awtrix.APIError`. The `awtrix` client drains response bodies to EOF before
    closing: Go's transport only returns a connection to the keep-alive pool
    when the body was read to EOF, and on the lossy link every avoided TCP
    handshake is one less packet to lose (`TestKeepAliveReusesConnection`).

  `clock_parity_test.go` replays a scripted run against a fake clock. The
  fake drops 0/44/60 % of requests and answers one request per call class
  past the short budgets. It also scripts coordinator-push faults: 1.8 s
  (inside `publishAttemptTimeout`), 3.5 s (times out, retried) and a 503
  (retried). Notices and chimes run with quiet hours off, then on at 23:00. The
  harness compares the whole device call log with goldens generated on the
  pre-refactor code. Changing `publishAttemptTimeout`, the 5xx retry, a call
  class's timeout or the quiet gate fails it. Keep-alive reuse is checked
  as a bound, not per request.
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
    2.5 s is chosen because a frame push is a ~2.4 KB PUT to a same-LAN device
    that answers well under a second when healthy (0.04 s small, 0.55-0.68 s at
    3 KB), so a push silent for 2.5 s is almost certainly dropped;
    `awtrix.timeout_seconds` (10 s) is the ceiling for any call and the wrong
    budget here. A `pushApp` retries 5xx and 429 `*awtrix.APIError`s
    (`retryableClockErr`) but stops at once on any other API error (a 422 won't
    become a 200) or a cancelled coordinator context.
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
    pushApp budget fits in it. The target is a third of the lifetime, with a
    floor that fits one full `pushApp` budget plus dwell jitter; if even the
    floor doesn't fit the window, it bottoms out at 1 s and every tick
    re-pushes (the dedupe is thrift, keeping the app alive is correctness). The old one-dwell margin bought exactly one
    attempt, and a single lost push took `ember` out of the rotation until the
    frame changed.
  - **Not** a lever here: `frame_lifetime_seconds`. It is also `durationMs` on
    every held frame (see "Display hold"), so raising it to buy eviction
    headroom silently triples how long an attention lock or the idle-dim frame
    monopolises the panel. Widen the margin instead.
  - **Dedupe is for cost only.** On AWTRIX3 a re-POST reset render state (a
    blinking label restarted); on awtrix-ng it does not (20 re-pushes of a
    byte-identical `textBlinkMs:1000` payload in 6 s left the blink on its
    original phase, and the slot kept the same ~8.7 s dwell). The dedupe stays
    because an unchanged frame otherwise costs a ~2.4 KB JSON parse on the same
    ESP32 task that drives the panel, every tick.
  - **Corner LEDs** (`applyIndicators`) write only LEDs whose desired state
    changed; a failed write is not recorded as applied, so the next publish
    retries. The zero state is `DELETE /api/v1/indicators/{id}`, because NG
    keeps `blinkMs` and the stored colour on a `PUT`. Indicators run on every
    publish path, including the dedupe skip and the nothing-to-show return, and
    turning the opt-in flag off turns the LEDs off through the same change
    detection. A reboot also drops LEDs, so `onRepublish` forgets them.
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
  - **Why the hold is edge-triggered.** `holdNone` lets the Ember app take its
    turn in the clock's loop like any tile; idle frames ask for a long dwell so
    they linger once reached but are not urgent enough to push native apps off
    screen. `holdAttention` force-switches via `PUT /api/v1/apps/active` and the
    app's own long `durationMs` sustains it (measured on 1.0.13: switch plus
    `durationMs=30000` held 31 s, `6000` held 6.7 s, so the switch supplies the
    jump and `durationMs` the length). That covers 30 s but not a 25-minute
    focus block, so `holdPomodoro` also sets `autoTransition:false` and
    `blockNavigation:true`. `holdAttention` touches no settings, which keeps it
    crash-safe: a dead server leaves the clock to expire the dwell. Writes
    happen only on the edge (a per-tick re-assert spams `apps/active` and
    retriggers the transition animation).
  - **`c.hold` moves only after every write of the edge landed**, because
    about 44 % of writes to this clock are lost and a hold marked done after a
    lost write is never retried (attention frame not forced, timer rotates
    away, or rotation left off with no timer). Writes are idempotent, so replay
    is safe. A restore is pending whenever a snapshot exists, which covers
    settings landed but switch not, and a snapshot left by a previous process.
    A write counts as settled when it succeeded or got an error a retry cannot
    change; a transport failure or 5xx is left for the next tick. The switch
    happens only after a successful push, and an identical (deduped) frame can
    still carry a new hold edge (a paused Pomodoro with a byte-identical
    payload).
  - **Snapshot rules.** A settings read that equals the takeover itself is
    treated as unknown (the clock is probably still in an un-restored takeover;
    recording it would make every later restore turn rotation off for good).
    The restore writes the user's values, not firmware defaults, which would
    clobber a Device-tab choice. `restoreBackoffTicks` exists because the
    restore is owed even when the frame is nil or deduped, so an offline clock
    would otherwise block the single coordinator goroutine for a full retry
    budget every tick and delay attention upserts. `exitRestoreBudget` (5 s)
    fits both retry attempts and stays inside `main`'s shutdown wait. A store
    failure in `persistPrior` only costs crash recovery.
  - **Menu edits during a takeover.** `priorMu` is a leaf lock held across a
    device call: the store write and one device exchange are the only work done
    under it. A menu edit holds it for one menu-class PATCH (8 s); the snapshot
    read and the restore hold it for one `retryDevice` (2 attempts × 2.5 s).
    An edit's waits give up at its `clockWriteBudget`; every other taker
    blocks (it is a `ctxLock`). `GET /v1/device/settings` takes it on both sides
    of its clock read and uses the earlier snapshot if a restore completed
    during the read (the device then answers with takeover values and no
    snapshot remains). Edits made with no snapshot write unlocked and relock to
    reconcile: `priorGen` (bumped by every `setPrior`) tells the edit a
    takeover edge ran during its write, and per-edit sequence numbers (`editSeq`,
    `keySeq`/`keyVal`, `claimKeys`) stop a slow older edit from overwriting a
    newer one. `reconcileRacedEdit`: if a snapshot now exists, the edit's read
    may predate it, so the newer keys go into the snapshot and the takeover
    values are written back for the edited keys; if none exists, a whole
    takeover and restore ran during the write, so each edited key is written
    again with its newest value. An error from `applyMenuSettings` means
    "possibly partly applied" (a raced edit whose first write landed but whose
    re-write over a restore was lost answers 502 although the clock briefly
    held it); the menu shows failure and re-reads. If the second `priorMu`
    wait gives up, the reconcile and `claimKeys` are skipped, so a takeover
    edge that raced the write can leave the edit showing mid-focus or undone by
    the restore; the handler answers 504 with `write` `applied`, and the app
    saves again on its next load (`ServerConfigModel`), which heals it.

### Producers

All producers share `internal/producer` and are configured via
`~/.config/ember/producer.env`: the HTTP client, `ReadEnvFile`, the common
keys (`Common`: source, server, token, card toggles; `Gauges`: context/rate
toggles for Claude and Codex; `Bool` for every toggle), `WriteFileAtomic`
(synced temp+rename that writes through a symlink), `Repost` (POST on change
or every 15 s keepalive), `StartDaemonLog`, and `Service` (LaunchAgent plist,
launchctl reload, systemd unit, uninstall, doctor status) with each
producer's label/unit as data.
**Source default (#208):** when `EMBER_SOURCE` is empty or the template
placeholder `set-me-to-this-laptop-id`, producers use a short host id via
`producer.ResolveSource`: `scutil --get LocalHostName` on macOS (else, and on
Linux only, `os.Hostname`),
lowercased, first label, `[a-z0-9_-]`; the `<owner>s-` prefix is dropped and
model words abbreviated (`dmitrys-macbook-pro` -> `mbp`, `-air` -> `mba`,
`mac-mini` -> `mini`, trailing `-2` kept) so Macs stay distinct on the ~4-glyph
clock card; max 24 chars. `install`/`configure`/`doctor` rewrite an empty or
placeholder `EMBER_SOURCE` in `producer.env` once (`EnsureSourceInEnv`, 0600
kept) so hook hot paths never fork `scutil`, and print the resolved value.
**Headless mode and server discovery (#255):** the producers build for
linux/amd64, linux/arm64 and darwin and run without Ember.app. Service
management is per OS: LaunchAgent on macOS, a generated systemd `--user` unit
on Linux (`internal/producer/systemd.go`: write unit, `daemon-reload`,
`enable`, `restart`; `doctor` reports active/enabled/linger). Logs go to
`producer.LogDir` (`~/Library/Logs`, else `~/.local/state/ember/logs`), also in
the hook/statusline redirects written to `settings.json` and in the plugin
shim. `EMBER_SERVER_URL` empty or `auto` turns on discovery
(`internal/producer/serverurl.go`): `discovery.BrowseEmber` sends an RFC 6762
legacy-unicast PTR query for `_ember._tcp` from an ephemeral port next to the
dnssd multicast browse (a CLI on macOS gets no multicast replies without Local
Network multicast access, but the server's dnssd responder answers legacy
queries by unicast; replies must carry QR and one of our query IDs). Answers
are kept per (instance name, URL), so a host spoofing the name surfaces as a
second server; `PickServer` dedupes by URL and needs exactly one, or an
`EMBER_SERVER_INSTANCE` matching instance name, host or IP. The pick is cached
in `$XDG_STATE_HOME/ember/server.json` with the preference it was made under; `loadConfig` resolves auto to the
cached URL without browsing (hooks stay fast), and daemons hold an
`AutoServer` the client consults per request: it waits for a first server
with backoff and re-browses after 3 consecutive transport errors (context
errors excluded), at most once per minute, in the background so a POST made under a
marker lock never waits on it (#258). Discovery never runs in `configure` (Ember.app calls it per agent), only in
`discover`, `doctor`, a headless `install` and the daemons. Ember.app's
`validateServerURL` keeps `auto` and builds no client from it. Linux liveness
reads `/proc/<pid>/stat` (BusyBox has no `ps -p`). Headless mode (no Ember.app,
or `--headless`) only changes service ownership and hints; see RUNBOOK
"Headless / Linux producers".
**Shared marker directory contract:** producers write session markers into the
same `~/.local/state/ember/sessions/` directory, but each daemon only owns
markers whose `tool` field matches its own (e.g. the Claude daemon skips a
marker with `tool: "codex"`); a marker with a missing/empty `tool` (a legacy
marker written before the field existed) is treated as Claude's so old
markers still get reaped.

- **Claude Code producer — `cmd/ember-claude-producer`.** Hook-based: Claude
  fires hooks per invocation; the producer maps 8 events to states
  (SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, Notification,
  Stop→done (kept `running` while `background_tasks` is non-empty),
  StopFailure→error, SessionEnd→DELETE). Three **tool-outcome hooks** (#76, `posttool.go`,
  registered `async: true` so Claude never waits on them) add no states:
  PostToolUse / PostToolUseFailure / PermissionDenied end a `waiting` only
  when they belong to the call its PermissionRequest recorded (a hashed
  tool name + `tool_input` fingerprint kept in the marker as
  `pending_permission`, plus the preceding PreToolUse's `tool_use_id` when
  its fingerprint matches, so neither a parallel call nor an identical
  earlier call can end the wait) with one POST back to `running`. That
  dialog's `permission_prompt` Notification arriving within 15 s after the
  wait ended is dropped rather than re-sticking `waiting`. Otherwise a failure or auto-mode denial just marks that
  call's trail item (`Bash: npm test (exit 1)`, `(failed)`, `(aborted)`,
  `(denied)`) in the marker, and the next POST (next hook or the ≤10 s
  heartbeat) carries it, so they add no requests. A failed tool keeps the
  session `running` (red ERR stays for StopFailure) and a denial doesn't
  blink. Hook stdin is stream-decoded (up to 64 MiB) and `tool_response` is
  skipped token by token, never stored; the failure's `error` text is read
  only for its `Exit code N` first line. configure/deconfigure delete the
  old spike log (`~/.local/state/ember/spike-hooks.jsonl`, which held full
  error output). Per-session flock + atomic
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
- **Claude Code mod producer — `producers/claude-code/mod`** (#253 phases 1-2,
  experimental, opt-in plugin `ember-mod@ember`). A hooks module that runs
  inside Claude Code (2.1.288+) and POSTs/DELETEs `/v1/status` with the Go
  producer's wire shapes, under its own source (`EMBER_MOD_SOURCE`, default
  `<EMBER_SOURCE>-mod`) so both can run side by side. `hooks/producer.ts` is
  pure (env parsing, config, the session tracker, wire bodies, the outbox);
  `hooks/register.ts` adapts it onto `$`. Same mapping as `hook.go`
  (`classic.*` events), with two differences: `tool.check` resolving to `ask`
  (interactive, mode not `auto`/`dontAsk`/`bypassPermissions`) starts
  `waiting` for that `tool_use_id`, and the awaited `tool.call` for that id
  ends it, which replaces the fingerprint matching for *which* call; *when*
  is the approved tool finishing (like PostToolUse), so an agents cross-check
  (#669, `watchPass` in `register.ts`, `Tracker.agentsCorrect`) ends it
  sooner. The 2.1.304 API has no "dialog answered" or "mode changed" event, so
  the mod ports the Go watcher's core. While a session is `waiting` on a
  permission dialog, and for 15 s after any resume (a watcher flip or the
  approved tool finishing), a 1 s `$.clock.every` timer (started and cancelled
  from `persist`, never from a hook; restarted on restore) fingerprints `<config dir>/sessions/*.json` with `$.fs.list` (name,
  mtime, size) and runs `claude agents --json` (5 s timeout; `claude
  --version` first, gated on 2.1.288 with cached verdicts, binary found at
  `~/.local/bin`, `PATH`, Homebrew, `/usr/local/bin`) only on a fingerprint
  change, a due confirmation or a 60 s fallback, as `step` does. It flips the
  row to `running` after two `busy` sightings of the same wait at least 1.5 s
  apart, and back to `waiting` (message `waitingFor`) on two `waiting`
  sightings within 15 s of a resume with no event since (`wantFor`'s
  `case "waiting"`). Go's reverse rule has no time limit while the marker is
  `running`; the mod's is bounded by that 15 s window (`RESUME_GRACE_MS`). A
  row fetched before a newer event is ignored. A failed
  or unparseable call clears the sightings, cancels the timer and re-arms it
  after 5 min. `parseAgents` and the wait rule share `test/parity.json` cases
  with `agents.go` (`agents_parse`, `agents_wait`). It does not start waits
  from `running` or turn `idle` into `done` (Go does). A Shift+Tab mode
  change has no source (agents rows carry no mode), so the mode is still read
  from classic events; the cross-check bounds a stale one (a
  classifier-answered ask in `auto` shows `waiting` for seconds). `classic.PreToolUse`,
  `tool.check` and `tool.call` carry no `session_id`, so the id comes from
  `$.session.id()`, hashed with `$.session.cwd()` when invalid, as classic
  hooks hash theirs. The tracker lives in module memory and is mirrored to
  `$.state` (`ember-mod.snapshot`, declared in `types/index.d.ts`; plain JSON
  the host keeps for the session, written after each event and tick that
  changed it), so a hot reload (`/reload-plugins`) rebuilds it from there on first use instead of forgetting
  in-flight sessions. The snapshot holds the sessions, the stray-POST start
  times (kept only while younger than 60 s: the settle callbacks that clear
  them died with the old code, so they just age out), `permission_mode` and the
  T3 verdict. Not kept: the outbox (closures; the next heartbeat re-POSTs the
  restored state), the config, `id -u` and the host id (re-read within a tick).
  `$.state` is the session's, not durable: a restart of Claude Code starts empty.
  T3 sessions (#216): `hooks/t3.ts` ports `spawnedByT3`. An SDK session
  (`CLAUDE_CODE_ENTRYPOINT=sdk-ts` or `CLAUDE_AGENT_SDK_VERSION` set, read with
  `$.env.get`) walks the process tree (`sh -c 'echo $PPID'` for the process
  that spawned it, then, where `/proc` exists, `/proc/<pid>/stat` and
  `cmdline` read with `$.fs`; `ps -o ppid=,comm=` and `ps -ww` where it
  doesn't, and per pid when a `/proc` read fails (`hidepid`), never a `/proc`
  attempt through a shell) and, when the launcher is a T3 server, the mod posts
  nothing. The verdict is taken once per process (Go pays per hook), under a
  250 ms budget; events that arrive while it runs await the same lookup, so none can POST
  after the DELETE of a late match. A failed lookup fails open and is retried
  at most every 30 s, and a later match DELETEs the rows already posted. A
  `no` verdict sticks for the process. `test/parity.json` `t3` cases run
  against both `spawnedByT3` and `detectT3`. Differences: darwin has no
  `kern.procargs2` from a shell, so argv is rebuilt from `ps comm=` and
  `args=` (a spaced executable stays whole, a spaced path in a later `node`
  argument splits), and the walk starts at whatever process the engine
  spawns `sh` from, assumed to be the `claude` process. `$.clock.every(10 s)`
  re-POSTs like `heartbeatPass` (done/error for `EMBER_DONE_TTL_SECONDS`, the
  30 min notification-wait backstop, the `EMBER_HEARTBEAT_TTL_HOURS` reap) and
  is skipped while the previous beat is still reloading. A reload builds the
  config and swaps it in one step; a reload that fails keeps the old config
  and the tick still runs on it. Sends go through a per-session outbox: one
  in flight, only the newest pending one kept, so a dead server can't pile
  them up. Each send is bounded inside its slot (`$.clock.sleep` race:
  `EMBER_HOOK_TIMEOUT_MS` for hook sends, 5 s like `daemonHTTPTimeout` for
  tick sends), so a hung fetch releases the session; SessionEnd waits at most
  800 ms in all (`sessionEndHTTPBudget`). `$.http.fetch` can't be cancelled,
  so a POST that timed out keeps running and can land after the next send.
  Each such stray is counted per session; heartbeat POSTs for the session
  are skipped while a stray is younger than 60 s (so heartbeats add at most
  one stray a minute; hook POSTs and DELETEs are never skipped). When a stray
  settles, whatever it returned (a lost response may still have been
  applied), the session's current wire state is sent once more: its DELETE
  if it has ended or been parked, else its latest POST, so a late `running`
  can't outlive a `done` past the done TTL (Go's `postReconciled` does the
  same from the marker). That repair send is not watched again. The timer dies with the process, so a killed
  Claude Code is reaped by the server's `stale_seconds`, not by a `ps`
  check. Every hook only observes: it returns `next(e)` (or the
  verdict/result untouched) and its `.catch` answers `next(e)`. Same guards
  as the Go path: `producer.env` must be a regular, non-symlink, 0600 file
  owned by the user (`stat`, and `id -u` once, from `/usr/bin`, `/bin`, then
  `PATH`, since `$.fs` has no modes; a refused file disables the mod rather
  than being ignored, while a check that could not run keeps the previous
  config),
  and the `claude-hooks.disabled` kill switch is checked before every event
  and before anything is spawned. Gauges (#668, phase 2): `session.measure`
  (after each main-thread turn and when a rate window moves a whole point;
  its input always carries every figure) sets the current session's
  `context_pct` from `context.percent` and `rate_window_pct`,
  `rate_reset_at` and the host-local `rate_reset_label` from the
  `five_hour` entry of `rateLimits`, with Go's `Pct` rounding. The hook
  returns `next(e)` at once and does this in the background, and nothing is
  sent: the next hook POST or heartbeat carries the figures, as the
  statusline marker does in Go, and they stick until a reading has newer
  ones. A reading for a session the tracker doesn't hold is dropped (as
  `enrichMarker` drops a render with no marker). A `five_hour` entry with
  no percent counts as 0 %, as Go's zero value does, and its reset is read
  on its own. `$.session.usage()` is read once in the background at
  `session.start` (for a session restored from a snapshot written before
  phase 2) and on the first hook of a tracked session with no gauges
  (`Tracker.wantsGauges`, once per tracked session: after `/compact` or a
  resume the session is forgotten and re-tracked). Each reading takes a
  sequence number when it starts, and one that finishes after a newer
  reading was applied is dropped, so a slow `usage()` can't overwrite a
  `session.measure`. The engine refuses at load time any module that hooks
  an event it doesn't know, so `session.measure` can't be gated per
  version; it was live-probed on 2.1.288, the mod's floor. The figures ride
  the `$.state` snapshot (`contextPct`, `ratePct` -1 and `rateResetAt` 0 mean
  unknown). `seven_day`, `spend_limit`
  and `cost` have no `/v1/status` field and are not sent. The mod never
  posts `/v1/usage`: it has no `account_key` (`$.session.identity()`
  answers only an org-managed mod), so it would add a keyless second
  "Claude" account, and a post replaces the account's whole report, so one
  without `models` would wipe the OAuth poll's per-model windows. The Go
  daemon keeps `/v1/usage`; with no fresh report the server's 5h fallback
  reads the newest session with `rate_window_pct`, so the mod's rows count.
  `test/parity.json` (including `gauges`: one reading as statusline JSON
  and as `SessionUsage`) and `test/transitions.json` (event sequences, a
  `statusline` reading included, to exact wire bodies) are shared with
  `mod_parity_test.go`, which drives `dispatchHookFrom`, `dispatchTick` and
  `enrichFromStatusline` and compares the `/v1/status` calls (the tick's
  `/v1/usage` relay is Go-only and skipped; any other route fails the
  test); the node side drives the `Tracker`. CI's
  mod job also runs `claude plugin validate --strict` and `claude plugin
  test` (`test/mod.test.ts`, the `register.ts` wiring) on a pinned Claude
  Code.
- **Codex CLI producer — `cmd/ember-codex-producer`.** Codex has **no hook
  system**, so this is a long-lived **daemon that tails rollout JSONL**
  (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`). Single-goroutine poll loop
  (2 s), live-session map keyed by rollout UUID, byte-offset tailing, keepalive
  re-POST (15 s) to stay under the server staleness reap. Day dirs are named in
  **local** time (scanned yesterday/today/tomorrow); a 30 s walk of the whole
  tree also picks up resumed sessions, which Codex appends to their original,
  older day dir. A rollout already idle past the activity window when found
  is cached by (mtime, size) and not opened until it changes; only sessions
  this producer posted get a DELETE. Shows `session_meta.source` kinds from
  `EMBER_CODEX_SOURCES` (default `cli,vscode`; `exec`/`mcp` opt-in;
  object-valued sources such as `subagent` decode to their key) and skips
  originator `Claude Code` (the Claude Code Codex plugin, already shown as
  the Claude session) unless `EMBER_CODEX_INCLUDE_CLAUDE=1`, which marks them
  `via Claude`. Codex ≥ 0.153 writes **paginated** rollouts (`history_mode`)
  that persist only `task_started`/`task_complete`/`turn_aborted`/`token_count`
  and **`item_completed`** TurnItems: an item → running (but not after
  `task_complete` until the next `task_started`; `SubAgentActivity` never
  changes state), `AgentMessage` → message, `CommandExecution`/`FileChange`/
  `McpToolCall`/`Extension` `web.search` (`web:`) and `clock.sleep`/
  `CollabAgentToolCall`/`ContextCompaction` → trail. Approvals and errors are never persisted there,
  so `waiting` cannot come from a paginated rollout (#254). `turn_aborted`
  `interrupted`/`replaced` → done (only `budget_limited` → error);
  `stream_error` is a retry → running. Rate limits only from `limit_id`
  `codex` (or none); a null window keeps its last value until its
  `resets_at` passes. `POST /v1/usage` carries the newest snapshot across
  sessions (by rollout line timestamp), on change or every 15 s. It also writes
  `~/.local/state/ember/sessions/<uuid>.json` markers so Codex shows
  in the menu app. Codex gets a distinct **chevron+underscore** 8×8 icon vs
  Claude's robot-face (the `_` cursor overlaid in the state colour). It
  also reads `rate_limits.primary` (5h) **and `secondary`** (weekly) from the
  rollout `token_count` events and posts both to `POST /v1/usage` alongside each
  status post (host-local reset labels formatted producer-side).
  **App-server source** (#263, #272; `cmd/ember-codex-producer/appserver*.go`,
  `EMBER_CODEX_APPSERVER`, default on). Codex ≥ 0.160 runs TUI sessions inside
  a shared app-server daemon, which the TUI starts itself. VS Code and the
  desktop app run their own stdio app-servers, so the rollout watcher still
  covers those. A goroutine stats
  `$CODEX_HOME/app-server-control/app-server-control.sock` every 2 s and, when
  it exists, connects (WebSocket over the Unix socket, `GET /rpc`, one
  JSON-RPC message per text frame, stdlib) with 1–30 s backoff and reconnects
  after a daemon restart or update. Frame writes have a 10 s deadline. After
  a minute with no inbound message, a `thread/loaded/list {limit:1}` must be
  answered within 10 s, so a hung daemon that keeps the socket open still
  triggers a reconnect. The handshake is bounded too: `initialize` and each
  bootstrap `thread/loaded/list` page get 10 s, and the whole handshake gets
  30 s, after which the connection is closed (that also cuts a blocked
  write), so a daemon that hangs before the worker starts is dropped within
  30 s. A `thread/read`, `thread/resume` or `thread/unsubscribe` that times
  out (30 s) is followed by the liveness probe. If that also fails, the
  session reconnects, so a daemon that hangs mid-session is dropped within
  about 40 s. If it answers, only that thread is quarantined (an unsubscribe
  timeout too): no read, resume or unsubscribe for it for 1 min, doubling on
  each further timeout up to 10 min and starting again at 1 min after 10
  quiet minutes past the last one. A read or resume that succeeds resets the
  backoff to 1 min; the thread closing (even mid-call) or a reconnect drops
  the quarantine. A thread's first timeout, and the first after 10 quiet
  minutes, log a warning; the rest log at debug. Other threads are read and
  subscribed as usual meanwhile, and a quarantined thread whose read is
  queued again stays with the rollout watcher instead of being held back. So
  one reliably slow thread on a healthy daemon neither forces a reconnect
  every 31 s (churning the device rows) nor stalls the worker. A daemon that
  answers probes but times out every thread call costs 30 s per thread once,
  then each retry is spread out by its quarantine. A timed-out resume or
  unsubscribe, or a rejected unsubscribe, leaves the daemon's subscription
  unknown, so once that thread is idle it is unsubscribed again until an
  unsubscribe succeeds (#523). A rejection does not quarantine the thread: it
  delays only the next unsubscribe (1 min, doubling up to 10 min, reset by a
  success) and logs a warning at most every 10 min. A timed-out call's late
  reply still counts: a late resume marks the thread subscribed (or, if the
  thread was closed and is read again, unknown), so it is unsubscribed once
  more; a late unsubscribe settles it. Hooks, and stashed late resumes for a
  thread not read again, are dropped after 10 min: a reply later than that is
  ignored, and if a later call already settled the thread its subscription
  stays stray. A call that fails because the connection is gone ends the
  session at once, so it reconnects; any other failed unsubscribe waits at
  least 1 s before the retry. A previous session's released threads survive
  failed handshakes, so the first full bootstrap still DELETEs the gone ones.
  A message over 16 MiB is drained and skipped and the connection stays up.
  It **never starts the daemon**. It
  initializes as `codex_app_server_daemon`: the first client name outside
  the server's non-originating list becomes the daemon-wide originator, which
  Codex records in every TUI rollout; the name is on that list
  (`NON_ORIGINATING_CLIENT_NAMES`, codex-rs `initialize_processor.rs`), so
  sessions stay labelled `codex-tui` (spike #263). It opts out of the delta notifications.
  Bootstrap: `thread/loaded/list` + `thread/read`. Ephemeral per-turn helper
  threads are skipped, and `source`/`originator` go through the same
  `EMBER_CODEX_SOURCES` / `EMBER_CODEX_INCLUDE_CLAUDE` filter (TUI-via-daemon
  threads report `source: "vscode"`). State comes from the broadcast
  `thread/status/changed`: `active` → running, `waitingOnApproval` /
  `waitingOnUserInput` → waiting, `idle` → done (error after a failed turn or
  a `systemError`, until the next `active`), `systemError` → error, `thread/closed` / `notLoaded` → DELETE. A thread
  subscribes (`thread/resume {excludeTurns}`) **only while active** and
  unsubscribes on idle, because a subscriber keeps a thread loaded forever.
  Unsubscribed, the daemon unloads an exited TUI's thread after 60 s and
  sends `thread/closed`. A resume before the first turn persists fails with
  "no rollout found" and is retried every second while the thread is active.
  The subscription supplies the trail (`item/started`: commandExecution with
  the shell wrapper removed, fileChange, mcpToolCall, webSearch …), the
  message (`agentMessage`), context % (`thread/tokenUsage/updated`
  `last.inputTokens / modelContextWindow`) and the turn result
  (`turn/completed`: interrupted → done, failed → error).
  `account/rateLimits/updated` (`limitId` `codex` or none; a null window
  keeps its value) is a candidate for the newest `/v1/usage` snapshot. A
  done/error thread posts for one activity window after its last change. A
  running or waiting thread posts for as long as it runs. **Dedupe** by
  thread id (= rollout `session_meta.id`): while connected, the app-server
  owns every loaded thread it has read, and recently closed ones for one
  activity window more. The watcher still folds those rollouts but posts or
  DELETEs nothing for them. A loaded thread whose `thread/read` is still
  pending is only held back: the watcher stops posting it but keeps its post,
  so the takeover runs once the read lands, and a failed read leaves the
  session with the watcher, with no DELETE. A session the watcher had
  posted is DELETEd at takeover unless the app-server posts it. A status change or close that arrives while a
  `thread/read` is in flight overrides the read's snapshot. On disconnect,
  sessions it posted go back to the watcher, or get a DELETE when the
  watcher has no live rollout for them. After the reconnect, released ids
  the new daemon no longer loads (the restart killed their TUI) get a
  DELETE. `CODEX_HOME` (in `producer.env`, else the process env, else
  `~/.codex`) moves both the sessions dir and the socket. **Hard
  invariant**: the client never answers a server request. On the shared
  daemon, approvals fan out to every subscriber and are replayed to late
  joiners, and any response counts as the user's decision. The only
  outbound type (`outbound`) has no result/error field, every id comes from
  the client's own counter, and the read loop drops server requests. Tested
  against a fake daemon that sends every approval/input request kind.

- **T3 Code producer — `cmd/ember-t3-producer`** (#210). T3 Code is a GUI
  over Codex / Claude / Cursor / OpenCode; its threads never reach the other
  producers (Codex runs as `codex app-server`, filtered out by `source`).
  A LaunchAgent (`com.ember.t3`) **polls T3's SQLite state read-only** every
  2 s (`mode=ro` + `query_only`, so the live WAL is still read) and keeps
  sessions alive with the Codex-style 15 s keepalive. Session id = T3 thread
  id, activity = thread title, tool `t3` ("T3" glyph, the 3 in the state
  colour). Mapping, aligned with T3's own `agentAwareness`: a pending runtime
  request other than `auth_refresh` → `waiting` (even on a settled thread:
  Codex `user_input` requests outlive the turn), except that from migration 63
  (T3 ≥ 0.1.1) a non-blocking `user_input` request (payload
  `responseCapability.type = 'message'`) on a preparing/starting/running run
  maps from the run (`running`), as in T3's `agentAwareness`. With no pending
  runtime request of any kind (from migration 63 also: only a non-blocking
  one, judged on the newest pending request), the newest `waiting`
  `secret_request` turn item of a preparing/starting/running/waiting run is a
  pending `user_input` → `waiting` "needs input", stamped
  `startedAt ?? updated_at`, unless that
  newest item has no node; run status preparing / starting / running /
  **waiting** → `running` (a run "waiting" is post-turn drain such as
  checkpoint capture, not the user). Like T3's `activityRunStatus ?? status`,
  the newest preparing/starting/running/waiting run of any ordinal wins over
  the presented run, and a presented `queued` run is not activity (→ DELETE).
  From migration 63 (T3 ≥ 0.1.1) a `done` or `error` thread the user dismissed
  (thread payload `settledOverride = 'settled'`, a field v0.1.0 also stores but
  never used to hide) is not reportable, as in T3's `agentAwareness`; running,
  waiting and completion-holding threads stay visible (#710).
  A `completed` run stays `running` while background work that wakes the agent
  is open (`backgroundWorkHoldsCompletion`: roster task of the active provider
  thread with kind other than `command`, or an active subagent /
  non-persistent dynamic_tool turn item outside a rolled-back run, or (T3 ≥
  0.1.0) a thread `pullRequests` link with a `watch` and a source other than
  `stack-dismissed`; dev-server commands do not hold). That hold is a
  single-query SQL equivalent of T3's shell read plus in-memory decode, not a
  copy of its code; the only known difference is that it does not dedupe
  roster vs item by task id. The turn-item lookup repeats the type/status
  lists of T3's partial `turn_items_recovery_idx` verbatim so SQLite uses it
  (a scan of every item costs ~0.5 s per poll at 600k items). The secret
  lookup runs once per poll, not per thread, and repeats the status list of
  T3's partial `runs_recovery_idx` (then drops `queued`) for the same reason.
  When a hold ends after the activity window, the watcher times `done` from
  the moment it saw running → done, so it still shows. Deliberate difference:
  the `auth_refresh` filter runs in the request query, so such a request never
  hides an older approval. A pending `auth_refresh` still suppresses the
  secret fallback, as in T3: with only an `auth_refresh` and a waiting secret
  the thread maps from its run status (`running`), not `waiting`.
  failed → `error`
  with the failed root error item's message, else the newest bound provider
  session's `lastError`; completed → `done`; idle / interrupted / cancelled /
  rolled_back / archived / deleted / unknown → DELETE. Subagent child threads
  (`lineage.relationshipToParent = 'subagent'`) are skipped, as T3's sidebar
  does. done and error stay for the activity window (5 min), timed from the
  run's completion (v2) or session update (v1), never from the thread's
  `updated_at`, which auto-settle, rename and archive bump days later. Two
  schemas: `userdata/statev2.sqlite` (`orchestration_v2_projection_*`, T3 ≥
  0.0.46) and `userdata/state.sqlite` (`projection_threads` +
  `projection_thread_sessions`, T3 ≤ 0.0.45, session status idle/starting/
  running/ready/interrupted/stopped/error, `ready` = done); when both exist
  the one whose file or `-wal` was written last wins, so a downgrade does not
  pin a stale v2 file. T3 liveness is `userdata/server-runtime.json` plus a
  `kill(pid, 0)` probe and, on darwin, the process start time
  (`sysctl kern.proc.pid`) against the recorded `startedAt` + 1 s, so a crash
  followed by pid reuse is not taken for a live T3: without liveness a thread
  caught `running` when T3 quit would stay on the clock, since nothing
  updates the database again.

  *Transport decision: SQLite poll, not the WebSocket RPC.* T3's server does
  expose `orchestration.subscribeShell` (snapshot, then thread upserts) over
  Effect RPC (`RpcSerialization.layerJson` over a WebSocket). Using it would
  need (a) a WebSocket client — Go's stdlib has none, so a new dependency or a
  hand-rolled RFC 6455 client; (b) T3's pairing flow: a pairing credential
  exchanged for a bearer token, then a short-lived WS ticket per connect, with
  the bearer stored on disk as a second secret next to `EMBER_TOKEN`; (c)
  tracking Effect RPC's internal framing (Request / Chunk / Ack / Exit / Ping)
  and two incompatible shell contracts (v1 `OrchestrationThreadShell` vs
  `OrchestrationV2ThreadShell`, both on the same method name). The SQLite read
  reuses `modernc.org/sqlite` (already the server's only dependency), needs no
  credentials, works whether T3 runs as the desktop app or `t3 serve`, and is
  testable with synthesised fixtures. Its cost is coupling to internal
  projection tables that churn (67 migrations by v0.1.1): the queries are reduced copies
  of T3's own shell queries, the newest verified migration per schema is
  pinned (`pinnedMigrations`: v1 54, v2 67) and a newer one is logged once and
  still read, and a missing table or column is a soft failure (log once a
  minute, back off to 60 s, never post or delete on a bad read).

  *Double sessions (#216).* T3's Claude provider loads the user's Claude
  settings, so the Ember Claude hooks fire for T3 Claude threads too. The
  Claude hook skips them, so each T3 thread shows once, as `t3` (`t3.go`,
  checked in `dispatchHookFrom` right after decoding: no POST, no new
  marker). T3 sets no env var of its own on the Claude child (v0.1.0 81493ef,
  nightly 0.1.1 e58a485: the SDK `env` is the server's `process.env` plus an
  optional `CLAUDE_CONFIG_DIR`, `makeClaudeEnvironment` in
  `provider/Drivers/ClaudeHome.ts`; no `CLAUDE_AGENT_SDK_CLIENT_APP`). The
  Agent SDK sets `CLAUDE_CODE_ENTRYPOINT=sdk-ts` and
  `CLAUDE_AGENT_SDK_VERSION` when unset (SDK 0.3.276 `sdk.mjs`); either one is
  the gate (the version too, since a T3 started from a Claude shell inherits
  `cli`), so interactive sessions pay nothing. For an SDK session the hook
  walks past shells to its owner (`claude`), then past shells from the
  owner's parent to the process that launched it (the server spawns the CLI
  directly; on Linux `AgentScope` `exec`s through `sh`/`systemd-run`), and
  reads that launcher's real argv (`/proc/<pid>/cmdline`; darwin
  `sysctl kern.procargs2`). It is a T3 server when:
  - `argv[0]` is named `t3`: the `t3@0.1.0` npm launcher `bin/t3.js`
    `spawnSync`s `@t3code/t3-<platform>/t3`, and the boot service runs
    `<T3 home>/runtime/versions/<v>/t3 serve` (`serviceLauncher.ts`);
  - an argument ends `/app.asar/apps/server/dist/bin.mjs` and is followed by
    `--bootstrap-fd`: the desktop app runs `<app> --require …
    app.asar/apps/server/dist/bin.mjs --bootstrap-fd 3`
    (`DesktopBackendConfiguration.ts`);
  - under `node`/`bun`, the first non-option argument is named `t3` or ends
    `/node_modules/t3/dist/bin.mjs` (the 0.0.x npm package).
  Whole arguments are compared, so an unrelated `apps/server/dist/bin.mjs`,
  a `bin.mjs.backup` or an option value does not match. All lookups share a
  250 ms context (`ps` via `exec.CommandContext`, `WaitDelay` 100 ms, so a
  stalled `ps` can hold the hook ~350 ms); a timeout or failed lookup
  reports the session. When the probe says T3 but a marker exists
  (an upgrade mid-session, or an earlier hook whose lookup failed), the hook
  removes it and DELETEs the `claude` row, so no frozen row is re-posted by
  the heartbeat. Cost: 3 `ps` calls plus one sysctl on darwin, SDK sessions
  only. Rejected signals: `T3CODE_CLI_PATH` exists only since v0.1.0
  desktop and leaks into T3's integrated terminal; `ELECTRON_RUN_AS_NODE` is
  set by any Electron host; a cwd under `~/.t3/worktrees` misses local-mode
  threads; the pid in `server-runtime.json` means reading T3's home.
  Checking the direct launcher, not any ancestor, keeps an SDK script started
  from a T3 terminal reported. Not recognised, so still doubled: a dev or
  checkout server (`node --watch src/bin.ts`, `node dist/bin.mjs`) and an
  unpackaged desktop build. Wrongly hidden: an interactive `claude` in T3's
  integrated terminal when T3's own env carries `CLAUDE_AGENT_SDK_VERSION`
  (T3 started from a T3 Claude thread's Bash tool). The gate passes on the
  inherited var and the launcher walk skips the terminal shell to the T3
  server (pinned by `TestSpawnedByT3_InheritedSDKEnvHidesAT3TerminalClaude`).

Claude producer constraints:
- **Hook timeout.** The hook path uses the short `HookTimeoutMs` (default
  500 ms) because the hook blocks the `claude` CLI. The daemon uses a separate,
  longer `daemonHTTPTimeout` (5 s) so a slow link doesn't flap heartbeat
  re-POSTs and reap DELETEs.
  A settings.json hook without `timeout` gets Claude Code's 600 s default
  (async hooks get none enforced), so installed blocking hooks always carry
  one.
- **Stop upserts `done`, never deletes**, because deleting on every Stop
  dropped the display to the idle robot between turns. The marker keeps
  `done` (the reply's first line as message) until the next prompt,
  SessionEnd, owner-liveness reap or the marker TTL, but the server only
  shows it for its `done_ttl_seconds` linger: the marker stamps
  `state_changed_at`, and the heartbeat re-posts `done`/`error` only until
  `EMBER_DONE_TTL_SECONDS` (default 30, keep it equal to the server's) after
  the state changed (that covers a lost hook POST), then lets the server
  reap it, so the idle screen returns ~30-40 s after a turn. `running` and
  `waiting` are always re-posted. A Stop whose `background_tasks` include a
  `subagent`, `workflow`, `teammate` or `cloud session` changes nothing (that
  work wakes the session with a new turn); a background `shell` or `monitor`
  (a dev server, `tail -f`) doesn't hold the session in `running`.
  `session_crons` are ignored: a scheduled prompt fires UserPromptSubmit.
- **Notification types:**

  | `notification_type` | State |
  | --- | --- |
  | `permission_prompt`, `agent_needs_input`, `elicitation_dialog`, `elicitation_url_dialog`, `quota_auto_resume_stale` | waiting |
  | `elicitation_complete`, `elicitation_response` | running, only out of a non-permission `waiting` |
  | `quota_auto_resume_fired` | running |
  | `quota_auto_resume_disabled` | error |
  | `agent_completed` | done |
  | `idle_prompt` | done, unless already `done`/`error` (keeps the reply line and any error) |

  StopFailure shows a label for its `error` enum plus `error_details`.
- **No network under the session lock.** Hooks and the heartbeat hold the
  flock only for marker file I/O and send after releasing it. Every POST is
  then reconciled: the marker is re-read and, if it changed meanwhile, re-sent
  once (a change racing that re-send heals at the next heartbeat); if it
  vanished (SessionEnd), the session is DELETEd again, also when the POST
  errored, since a timed-out POST may still have landed. So a POST can't leave
  a stale or ghost session. Hooks wait for the lock at most
  `HookTimeoutMs`+100 ms and the status line 250 ms; running out means a
  wedged holder, and the whole update (marker write included) is dropped
  until the next hook. SessionEnd (1.5 s shared budget, which plugin hook
  timeouts don't raise) and non-startup SessionStart wait at most 200 ms /
  the hook wait and remove the marker regardless; SessionEnd caps its DELETE
  at 800 ms. settings.json hooks carry `timeout` 5 s (SessionEnd 2 s), same
  as the plugin.
- **Hook commands self-heal.** They are wrapped as `[ -x BIN ] && BIN … || true`
  so they exit 0 when the bundled binary is gone, and Claude Code never
  reports a hook error after the app is moved or deleted. Tool-outcome hooks run
  `async` because PostToolUse fires on every call and their order doesn't
  matter.
- **Two hook registrations, one list.** `producerHookSpecs` (`install.go`)
  is what `configure` merges into `~/.claude/settings.json`; the `ember`
  plugin (`producers/claude-code/plugin/hooks/hooks.json`, listed by the repo's
  `.claude-plugin/marketplace.json`) registers the same set through a
  `/bin/sh scripts/ember-hook <event>` shim that finds the binary and always
  exits 0. `plugin_test.go` fails when they drift. Claude Code doesn't dedupe a
  plugin's hook against a settings one, so `configure` skips (and strips) the
  settings.json hooks when `enabledPlugins["ember@ember"]` is true, and
  `doctor` flags both at once. Config stays in `producer.env`; the plugin has no
  `userConfig`. Since deconfigure can't unregister plugin hooks, it writes the
  kill switch `~/.config/ember/claude-hooks.disabled`, which `runHook` checks
  before `loadConfig`; configure removes it. Ember.app reads the same state
  itself (`ClaudeHookRegistration`, EmberKit) rather than running `doctor`:
  doctor rewrites `producer.env`'s `EMBER_SOURCE` and probes the server, too
  much for a pane refresh. The two parsers are held together by shared
  fixtures in `cmd/ember-claude-producer/testdata/hook-registration`, which
  both test suites read. Known divergence: with a duplicate key Go keeps the
  last value and `JSONSerialization` the first; a UTF-8 BOM is rejected on both
  sides (Swift checks for it, since `JSONSerialization` accepts it).
- **Statusline.** The `statusline` subcommand never calls `loadConfig` and
  makes no network call, so it needs no token. Its stdout is the status bar
  Claude renders and must not be redirected; only stderr goes to the producer
  log.
- **Usage relay.** The statusline-driven `/v1/usage` POST has no per-model
  figures, so the daemon forwards the last OAuth-poller per-model snapshot
  (`usageModels`); without it the server's last-write-wins `UsageStore.Put`
  would blank the per-model breakdown on the next 10 s heartbeat. The
  statusline relay (the session whose figures changed most recently wins, by
  the marker's `statusline_changed_ms`, ties broken by mtime) is the primary
  weekly/5h source; the OAuth endpoint is the flaky fallback. The statusline
  rewrites (and fsyncs) the marker only when a figure changed, or once a
  minute by mtime so the TTL reap still sees it; hooks carry
  `statusline_changed_ms` over. A wrapped status line command gets
  `EMBER_STATUSLINE_TIMEOUT_MS` (default 10 s), then its process group is
  killed and the session's last good output
  (`~/.local/state/ember/statusline/<session>.out`, pruned after a day) is
  shown instead. A command that exits 0 but leaves a background child holding
  stdout still has its output shown. A producer stopped by SIGTERM, SIGINT
  or SIGHUP kills that process group before it exits with 128 + the
  signal number (143, 130, 129), a normal exit rather than death by
  signal; a SIGHUP or SIGINT ignored at start (nohup, `cmd &`) stays
  ignored (the Go runtime always takes SIGTERM, so an inherited ignore of
  it never held). A SIGKILL can't be caught, so a command started then
  can outlive it until its own end.
- **`claude agents --json` cross-check** (#266, `agents.go`,
  `EMBER_CLAUDE_AGENTS_POLL`, default on). Hooks miss three transitions: an
  approved permission dialog stays `waiting` until the tool finishes (no hook
  fires on approval), a dialog dismissed with Esc and an Esc-interrupted turn
  fire nothing at all (no Stop), so the marker stays `waiting`/`running` until
  the next prompt. The daemon reads the documented `claude agents --json`
  (`status busy|waiting|idle`, `waitingFor`, `sessionId`) and corrects a
  marker: busy ends a wait, waiting starts one (flagged `agents_wait` so the
  watcher may end it; any hook write clears the flag), idle → `done`
  "interrupted". It only ends waits it understands: a permission dialog
  (`pending_permission`) or its own; Notification-only waits
  (`quota_auto_resume_stale`, elicitation) stay with the hooks. An
  `agent_needs_input` wait (#485) comes from a background session but marks
  the parent waiting, so it is flagged `agent_input_wait` and the marker
  keeps the hook's `cwd`. Real `claude agents --json` background rows carry
  `kind:"background"`, `state` and `cwd` but no `waitingFor`; one needs
  input while `state` is `blocked`. An agent belongs to a parent when its
  cwd equals or is under the parent's cwd, or shares its git common dir (a
  worktree, found by reading `.git` files, no `git` process). The wait ends
  once no blocked background agent belongs to the marker; a blocked agent
  no live marker owns, unparseable output, or an old marker without a cwd
  holds every such wait: back to `running` "working" if the parent is busy,
  `idle` if not (never `done`, which would fire the done reward), and a
  leftover `pending_permission` can't clear it while its agent is blocked.
  Before #485 it stuck until the 6 h heartbeat TTL. Backstop for waits the
  poller can't confirm (`agent_input_wait`, elicitation and other
  Notification-only waits): no hook, state change or poller confirmation
  for 30 min (`waitBackstop`, `tick.go`; heartbeats don't count) parks the
  marker (`wait_expired`, state `idle`, no heartbeats) and deletes the
  server session; the next hook or poller correction re-adds it. A wait the
  poller sees (a permission prompt, the row's `waitingFor`, or a blocked
  same-cwd agent) writes `wait_confirmed_at` at most every 5 min and never
  expires. Expiry re-checks after the network: a POST that lands after the
  DELETE from a now-parked marker is followed by another DELETE, and a hook
  that rewrote the marker during the DELETE is posted again.
  Stop skipped for waking background work sets `bg_wake`, which
  blocks idle → done until the next prompt or done/error.
  Each call costs ~0.1 s CPU and a ~75 MB transient process (no disk writes),
  so it runs only while some marker is running/waiting, and then only when a
  file in `~/.claude/sessions` changes (Claude rewrites `<pid>.json` in place
  on status flips; the layout is internal, used only as a trigger) or 60 s
  have passed. The fallback is skipped while every active session was
  unlisted with a live owner. A correction needs two snapshots ≥1.5 s apart
  that agree on an unchanged marker, re-checked under the lock, so a hook that
  is merely late (Stop lands ~0.5 s before the status goes idle) wins. A
  session missing from the list is reaped only if its owner pid is gone
  (nested `CLAUDE_CODE_CHILD_SESSION` and SDK sessions aren't listed); a
  killed process can't rewrite its sessions file, so the heartbeat's owner
  check usually reaps it first. The CLI runs in its own process group with a
  5 s timeout that kills the group and a 1 s `WaitDelay`, so a helper holding
  stdout can't wedge the watcher. The binary is `~/.local/bin/claude`, then
  PATH, then Homebrew paths, gated once per path+mtime on `claude --version` ≥
  2.1.288 (an older CLI would take `agents` as a prompt, so the gate is a
  safety check, not a feature probe); a failed call backs off 5 min. `doctor` prints the state.
  **Hookless sessions** (#285): a session started before the ember plugin was
  installed never loads it, and when `configure` strips the old
  `settings.json` hooks a running session hot-reloads settings and loses them
  too, so it reports through no hook until restarted or `/reload-plugins`
  (verified: a plugin update alone keeps a running session's hooks; a plugin
  installed mid-session plus removed settings hooks fires nothing; the
  statusline, a settings command, keeps running). Its marker stays `done`
  while the session works. A `done`/`idle` marker is promoted only on proof
  that a turn began after it went dormant: its owner's `sessions/<pid>.json`
  says `busy` with `statusUpdatedAt` past the second after `state_changed_at`
  (no `statusUpdatedAt`: a statusline change ≥5 s after done). A slow Stop hook
  of another plugin keeps the old turn busy and fails the proof; so does idle
  with a background shell, which the file calls `shell` while `claude agents`
  says busy (measured on 2.1.289). The CLI is asked only when that file or the
  statusline changed and the proof holds, so quiet sessions and healthy turn
  ends cost no call; a session without the file (nested, SDK) is never
  promoted. Busy confirmed by two snapshots promotes it to `running`
  "working" (flag `agents_run`, cleared by any hook write); idle, or `shell`,
  then ends it as `done` "done". Marker comparisons ignore statusline-owned
  fields, which change on every assistant message. Hooks stamp `hook_at`, the statusline
  `statusline_at`; `doctor` warns when a busy session's statusline is fresh but
  no hook wrote for 10 min (markers without `hook_at` are skipped).
- **Session lock file.** The per-session lock file is never deleted: removing
  it would break the POSIX flock-on-inode guarantee between concurrent holders.

### Menu-bar app — `macos/` (native SwiftUI)

macOS menu-bar companion, a **pure HTTP client** of the server (it reads
`GET /state`, `GET /v1/preview`, and drives the Pomodoro endpoints — it does
**not** read the producers' local markers). The Connection tab edits
`producer.env` (shared with the Go producers) and rebuilds the live client
without relaunch. Hybrid layout:

- **`EmberKit` (`macos/Sources/`, SwiftPM)** — all testable logic, no scene
  code: Codable models mirroring the wire shapes (`Models/`), `APIClient`,
  `ServerConnection` (producer.env's URL and token as the one client every
  request derives from; `reload()` says whether the server or token changed),
  typed wrappers only where a route group has real logic (`DeviceService`,
  `PreviewService`, `RemindersService`; every other path string lives in its
  one caller: `LiveModel`'s feed switch, `SettingsModels`, `ActionRunner`), `pickWinning`,
  `EnvFile` + validation, the settings types, and the app foundations (#119):
  `Live/` (`LiveModel`, `RefreshCoordinator`, `ActionRunner`), `Config/`
  (`ConfigModel` as `ServerConfigModel`/`EnvConfigModel`, `SettingsModels`,
  `PreviewModel`: a Settings pane's pixel preview, debounced 300 ms, and only
  the latest request's outcome is applied, so a slow older response can't
  replace a newer preview)
  and `Presentation/` (display names, formatters, and `MenuRows`, the menu's
  row rules), plus `Device/` (`DeviceSettingsModel`: the clock's settings
  saved as a patch of the keys that changed, overlay, sensors, apps, buttons,
  audio), `Settings/` (`SettingsRoute`, the selection stored as a path
  such as `device/clock/app/weather` with every older pane name mapped;
  `SettingsTree`, the sidebar built from the devices and `AppCatalog`, the
  per-kind hardware pages and apps; melody choices, the Connection probe) and `Reminders/`
  (`ReminderScheduler` behind a `ReminderSource` seam, plus the pure fire
  rules). Headless `swift test`.
- **`Ember` (`macos/Ember/`, thin Xcode app)** — an `LSUIElement` agent
  app: a `MenuBarExtra` (`.menu` style: session header and activity, other
  sessions, 5h usage per tool, next meeting or reminder, Pomodoro status and
  controls, today vs the goal, the last failed action, and a Clock submenu
  with next/previous app, dismiss, display power and Show on Clock; rows a
  server lacks are hidden, e.g. usage falls back to `/state` and display
  power needs 0.28+), the animated bot or tool glyph as its icon, a
  fixed-width sidebar `Settings` window (height resizable only, like System
  Settings; three groups: **App** (General, Connection, Permissions,
  Sounds & Alerts), **Sources** (Agents, Day Clock, Focus, Weather, Calendar,
  Music: where data comes from, set once; Day Clock moved there from App in
  #741, and a stored `app/day-clock` selection opens it) and **Devices** (Clock
  and Knob, each with its hardware pages and an Apps subtree holding that
  device's presentation of each source; the knob's Day Clock app shows only
  when its caps list the `day` page (#739); a source pane and its device apps bind the same config model,
  so a pending edit shows in both; the selection and expanded nodes persist
  in `UserDefaults`, and a route under a device still loading is held, not
  replaced by the fallback); the title follows the pane, the
  subtitle is the one save status of every config model, controls stay
  disabled until their model has loaded), a resizable **Dashboard** window ("Ember", ⌘0), and a Dock
  menu while a window is open. `Ember/Shared/` holds the views all three
  surfaces use (`FeedStateView`, `StatTile`, `StaleChip`,
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
| C, only while held | `screen` 1 s, `clockHealth` 15 s, `dayState` 15 s, `weather`/`activity`/`heatmap`/`dayHistory`/`dayWeeks` 5 min | — |

A view holds feeds for its lifetime with `.task { await env.live.track(…) }`
(refcounted). A feed has at most one request in flight; a second caller joins
it. 429s back off through `RateLimitBackoff`; a 404 or 405 (an older server
without the route) is `.featureOff`, which views show as "off", never as stale
data. An unchanged poll doesn't republish its value. System sleep pauses every
loop and wake refetches at once. A `/state` failure keeps the snapshot live for
3 polls (`degraded`), then marks it stale and the connection offline; the bot
only follows a live snapshot. The one read that isn't a feed is
`LiveModel.serverVersion` (the Dashboard subtitle's "· v0.29.0"): `/version`
only changes when the server restarts, so it's read once each time the
connection comes up (first answer, a new server, back from offline, where an
upgrade shows), off the `/state` loop; a lost read retries on the next good
`/state`. The same `/version` read carries `clock_disabled` (`EMBER_CLOCK=off`,
fixed for the server's lifetime), so `LiveModel.isClockDisabled` is known
before the menu first opens, is re-read after a reconnect, and costs no clock
I/O (a `/v1/clock/health` read can probe the TC001). The two sources are
ordered by when each request was sent (one sequence number taken at send
time), not by when it answered: a `/v1/clock/health` read sent after the last
`/version` read wins, even when its value equals the cached one, so a redeploy
faster than the offline detection (3 failed polls) can't leave a stale flag,
and a reply slowed down (a health read waiting on the clock probe, a lagging
`/version`) can't overrule a newer one. `MenuRows.clockMenu` takes the model's
verdict and doesn't re-check the health. While it's set the Clock submenu shows
only "Clock disabled on this server": no Next/Previous/Dismiss, display power or
Show on Clock. The menu adds no polling: opening it catches up
`stats`/`meetings`/`usage` (`LiveModel.menuFeeds`) only if they're over 60 s
old, every fetch pushes the next poll back, so stats stay at one request a
minute. No explicit refresh fetches a feed inside a 429's Retry-After window.
Actions (Pomodoro, clock, app visibility) go through `ActionRunner`, from every
surface (Settings restarts the clock and switches the display through it too),
which keeps the last failure for 10 s and refreshes the feeds the action touched
(stats follow a Pomodoro phase change). Display power is one value,
`LiveModel.displayPower`: the newest of clock health's `matrixPower` (dated at
probe time, so the server's 30 s probe cache can't undo a write; it must be over
2 s newer to beat a report), a successful power write or reboot, and Settings'
overlay read (dated when issued). Results from a previous server are dropped.
While a write is in flight the switches show its target
(`ActionRunner.pendingDisplayPower`). With no window open the app makes about
2,640 requests an hour: 1,200 each to `/state` and `/v1/pomodoro/state`, 60 each
to stats, usage, meetings and apps (the old poller made about 4,800, 1,200 of
them stats). `APIClient`'s sessions and the direct screen mirror skip the
on-disk `URLCache`, which, with no cache headers from the server, only rewrote
`Cache.db` every poll.

**Model and networking constraints.**
- **Config models.** Panes bind controls to `ConfigModel.draft` and call
  `scheduleSave()`; the model writes 600 ms after the last edit and never writes
  before a successful load, so views must disable their controls until
  `isLoaded` or an early edit is silently discarded. A failed save is retried by
  the next `load()`; a `.featureOff` save is reverted to the server's value, not
  retried. A pending edit blocks a reload from overwriting what the user just
  typed. Models decode absent fields to the server's own defaults so re-saving
  an old config never turns a feature off.
- **Env toggles.** `envTrue` (default true) and `envOn` (default false) parse
  like Go's `producer.Bool`: `true/1/yes/on` and `false/0/no/off` in any case,
  anything else keeps the default. The Swift parsers also trim whitespace,
  which Go's `Bool` doesn't; both file readers already trim values, so the
  result is the same for `producer.env`.
- **Info buttons (#290).** `SettingsInfo` text states only what the code or
  docs behind it say, so re-check it when behaviour changes. Sources: the
  server's defaults and validation (`cmd/ember`); cinder `docs/features.md`
  ("View long-poll" #27, "QSPI 80 MHz panel link" #23) for the knob; this file
  ("GET /v1/display/brightness", "Hold precedence", "5h limit-reset alarm",
  "App-server source", "claude agents --json cross-check"); RUNBOOK's `EMBER_*`
  toggle reference and `cmd/ember-codex-producer/config.go`; awtrix-ng's
  `guides/brightness` and `reference/settings`; `cmd/ember/weather.go`
  (`wmoCondition`, `metSymbolCondition`) for severe weather.
- **Env file.** Every in-app writer of `producer.env` (settings panes, the
  Connection pane's token save) goes through one `EnvFileStore` per path; without
  that serial queue two concurrent saves read the same old file and the second
  drops the first's change. `EnvFile` serialisation drops the empty element a
  trailing newline produces so it round-trips without growing blank lines;
  writes are atomic (replace when the file exists, plain move on first write,
  because `replaceItemAt` fails on a missing destination), directory 0700 and
  file 0600, matching `envfile.go`; duplicate keys are last-write-wins like the
  producer's parser. The producer-install command runner drains stderr on a
  separate thread: reading two pipes in turn deadlocks once the child fills the
  unread one.
- **Connection saves.** Empty required fields are tolerated on first run, so URL,
  token and source can be filled in any order, but each non-empty field is
  validated and clearing a required field that has a committed value is
  rejected: skipping that write would leave the UI blank while `producer.env`
  keeps the old value, which reappears on relaunch. The pane commits Source and
  Server URL on Return or focus loss (and on teardown), since saving a
  half-typed URL would repoint every model at the wrong server; the token is
  saved only by Save Token or Return, so a partly typed secret never reaches
  `producer.env`.
- **Device settings.** `DeviceSettingsModel` loads its clock reads
  sequentially: fanning them out empties the server's per-IP token bucket and
  causes 429s. A save diffs against `applied` (what the model last accepted),
  not the last response, because a load that lands during a pending edit is
  discarded and diffing against it would send the Pomodoro takeover's values
  back as stale edits. `DisplayOverlay` always encodes `overlay: null`
  explicitly (NG's way to clear it); `power` is read-only on
  `/v1/device/display`, and the matrix is blanked only through
  `DeviceService.setDisplayPower`.
- **Latest wins.** Every `LiveModel` feed carries a request counter (bumped by
  `configure` and each request) and only the newest request's answer lands, so
  a slow poll cannot overwrite a newer `refreshNow` and a response from the
  previous server is dropped after a Connection change. A value is published
  only when it differs from what is shown, and the last-fetched timestamps are
  not observed, so an unchanged 3 s poll invalidates no view. `ActionRunner`
  takes its server token before the request: a write that returns after a
  reconnect says nothing about the new server's clock. `PreviewModel` uses a
  generation counter bumped by every `request`/`cancel` instead of task
  cancellation: a cancelled fetch can still return, and its outcome is dropped
  unless its generation is current.
- **Refresh coordinator.** A fetch task does its own bookkeeping (recording the
  result, clearing "running") before it completes, so starter and joiners all
  resume to a recorded, finished fetch; leaving it to the starter let a joiner
  that resumed first re-join the finished task without suspending and spin the
  main actor. After a server switch `forgetInFlight()`/`restart()` drop
  in-flight fetches, and their late answers must not stamp the new server's
  timing or backoff.
- **429 pacing.** `RateLimitBackoff` doubles from the previous backoff, not the
  base, so a sustained squeeze converges instead of re-probing every
  `retryAfter`, and applies the server's `Retry-After` floor after the doubling
  cap (waiting less than asked only earns another 429). A missing, zero,
  negative, HTTP-date or junk `Retry-After` lands on the fallback, never
  "retry immediately". A 429 says nothing about server health, so it leaves the
  failure ladder untouched. A poll loop that keeps its cadence through a 429
  keeps getting 429s because the bucket cannot refill while drained.
- **Mirror.** The proxy-versus-direct decision for the clock mirror lives in
  `MirrorPoller`, not a SwiftUI `.task` loop: it must not treat a 429 as "the
  proxy route is missing", and it keeps the direct clock read going while
  throttled (that read never touches the server, so it costs no rate-limit
  budget; skipping it left the panel black).
- **Errors.** `APIClient` runs on dedicated `URLSession`s (one per
  `RequestBudget`) with no URL cache. `APIError` conforms to `LocalizedError`;
  without it settings footers render the NSError bridge text instead of the
  server's `{"error":"…"}`. Local Network refusal is detected from the failed
  path CFNetwork attaches (`_NSURLErrorNWPathKey`, private but stable) on the
  error or its underlying error. A 405 on a read route means a server that has
  only the POST of that route (`/v1/usage` before 0.28), so it is `.featureOff`
  like a 404. Dashboard fields an old server sends as Go's zero time
  (`0001-01-01T00:00:00Z`) are read as "no value" (anything before 1971).
- **Discovery.** `ServerDiscovery` puts IPv6 hosts in brackets and
  percent-encodes a link-local zone id's `%` as `%25` (RFC 6874:
  `fe80::1%en0` becomes `[fe80::1%25en0]`). Browse callbacks hop to the main
  actor, so one queued just before `stop()` can land after it; only the current
  browser's callbacks are applied. `ClockDiscovery` probes each address once
  per scan (`NWBrowser` replays the whole result set on every change; an
  unclaimed address is retried on the next set) and leaves a resolve stuck in
  `.waiting` alone, since `NWConnection` retries by itself and the scan window
  bounds it, whereas `ServerDiscovery` fails a `.waiting` resolution fast and
  reclaims it. The Settings "clock unreachable" prompt needs a failed proxied
  settings read (502), not merely a failed health probe: the clock's Wi-Fi drops
  requests and the server caches a probe for 30 s; an unreachable server or a
  rejected token is not something clock discovery can fix.
- **Permissions.** Permission refreshes are coalesced: the pane's `.task` and
  `didBecomeActive` both fire when Settings opens, so a non-forced refresh joins
  the running check and skips if one started recently, while an explicit one
  (Check Again, after Repair) waits out the running check, which may predate the
  fix, and runs fresh. During a re-check the last Local Network verdict stays up
  so the row doesn't flicker.
- **Apple Music pusher (#226).** Settings › Sources › Music toggles it
  (per Mac, `UserDefaults` `musicNowPlaying.enabled`, off by default).
  `MusicNowPlayingWatcher` observes the `com.apple.Music.playerInfo`
  distributed notification only while it is on (a knob skip re-reads
  briefly, see "Now playing" › Music). Music posts no `playerInfo` for a
  seek or a restart of the same track (cinder#230), so while the last
  report is playing, something watches (the commands long-poll's
  `watched`, #575, see "Now playing" › Music, or this app's own knob
  now-playing preview) and Automation is granted (checked once
  per watch), the pusher reads `player position` every 2 s (`AppleMusicPusher.seekPoll`,
  a `core/getd` of `pPos` sent to Music's PID with a 1 s timeout, so it
  can't relaunch a quitting Music). When the read is more than 3 s off the
  report's position extrapolated to now (the server's seek tolerance), it
  pushes a fresh snapshot, unless a newer notification arrived meanwhile.
  The preview needs no round trip: the Knob settings' now-playing feed
  task (`KnobPreviewFeeds`, overview thumbnail or the page) calls
  `AppleMusicPusher.holdPreview()` while it runs and `releasePreview()`
  when SwiftUI cancels it; holds are counted, so the overview and the
  page together keep one watch. The pusher watches while `watched` or a
  hold is on. Both ending stops the watch; either starting it from off
  starts it with one
  read at once (miss count reset; a hold while already watching changes
  nothing, backoff included), so a seek made while nothing looked
  shows within a round trip. That read waits for an in-flight report or
  artwork upload to finish rather than being dropped. The pusher starts out watched and treats an
  answer without the key (an older server) as watched.
  The anchor moves on every report attempt, so a failed POST isn't retried
  each poll. A paused or stopped report (delivered or not), a read that
  finds Music gone (reported as stopped) or turning the toggle off ends
  the watch, so a paused or idle Music costs no wake-ups; a scrub
  while paused shows on resume, whose notification re-reads the position.
  Three failed reads in a row while Music runs (it hangs, or Automation
  was revoked mid-track; `AppleMusicPusher.seekMissLimit`, counted afresh
  on every playing report) back the watch off instead of ending it: ticks
  in between only check `NSRunningApplication` (no Apple event, no shared
  queue), so a force-quit of a hung Music, which posts no `playerInfo`,
  still reports stopped within 2 s, and the position read is retried after
  4, 8, 16 s, then every 30 s (`seekMaxSkips`), so seeks show again once
  Music recovers without waiting for a notification. A good read restores
  the 2 s pace. `AppleScriptMusicBridge` logs a failing event or script
  code once until a success or a different code (an ignored -1728 counts
  as different; `AppleEventFailureLog`), so a hung Music (-1712) or an undecided
  Automation (-1744) doesn't log on every poll or notification. Every
  event stays `.neverInteract`: a background notification never raises
  the Automation prompt; Settings › Permissions asks for it.
  EmberKit's `AppleMusicPusher` coalesces bursts to the latest state, POSTs
  `/v1/nowplaying` (`source:"music"`, `player` = the Connection pane's
  source name, else the computer name, `track_id` = Music's persistent ID
  in AppleScript's 16-hex form), and PUTs the album artwork when the answer
  says the server lacks it (`ArtworkShrinker` re-encodes art over 512 KB or
  1000 px as a 1000 px JPEG). `AppleScriptMusicBridge` reads `player
  position`, `{persistent ID, raw data of artwork 1}` of the current track
  in one script (the PUT is skipped when the ID isn't the reported track's,
  so a skip can't put B's cover on A) and, when the toggle
  turns on mid-track, a one-shot snapshot; every script first checks
  `NSRunningApplication` for `com.apple.Music`, because a `tell` would
  launch Music (`NSAppleScript` isn't thread-safe, so scripts share one serial
  queue). While the toggle is on, `MusicCommandListener` long-polls
  `/v1/nowplaying/commands` for this player and runs the knob's commands
  (#280; see "Now playing"). Hardened runtime needs
  `com.apple.security.automation.apple-events` (`Ember/Ember.entitlements`)
  and `NSAppleEventsUsageDescription`; Settings › Permissions has an
  "Automation: Music" row read with `AEDeterminePermissionToAutomateTarget`
  for the concrete event `core/getd`, which the scripts send, since wildcard
  event codes are reported not to prompt (no prompt; "Couldn't check" while
  Music isn't running). MediaRemote was
  not used: private, entitlement-gated since macOS 15.4.
- **Presentation.** The menu-bar label is driven by a small value (icon plus
  VoiceOver text) instead of the winning `Session` or `ConnectionHealth`, so it
  re-renders only when what it shows changes. The clock-health reading is dated
  on this Mac's clock (arrival time minus the probe's age,
  `generatedAt − checkedAt`, both from the server's clock) so the two clocks
  never mix; dating is good to about a second, so a health reading must be
  clearly newer than a power-write report to replace it. Session activity text
  is sanitised before display: a whole tagged element is machine content and is
  dropped; a tag cut off by the producer's truncation (`<task-notifica…`) is
  markup from its `<` on, but `a < b` is not a tag; only control characters
  (Cc) are stripped, since format characters (Cf) include the joiner inside
  emoji. Dashboard day and ISO-week keys use `Calendar` arithmetic, never
  "+ 86 400 s", so a 23- or 25-hour DST day lands on the right key. When
  opening a clock's web page, only plain http(s) URLs may reach `NSWorkspace`
  (`baseURL` arrives over the network; never `file:` or
  `x-apple.systempreferences:`).

This replaced the retired Go menu (`fyne.io/systray` + DarwinKit). The Agents pane's
preview is **pixel-accurate** because it renders the server's `/v1/preview` grids
— produced by the same `internal/render` core the device uses (see below).
The Agents, Focus, Weather and Calendar panes all fetch their previews the same
way: a `PreviewModel` per preview, driven by the `previews(_:into:fetch:)`
modifier (`PanelPreview.swift`), which requests on appear, on a draft change and
on window reactivation, and cancels on disappear.

The Agents pane's **Reporting** section manages the bundled helpers through
`ProducerInstallService` (`SMAppService` + the helper's `configure` /
`deconfigure`). One row per agent: Claude Code, Codex and T3 Code, each with its
state (on / off / needs approval / not running with Repair) and its own switch.
A row shows when its tool is detected (`~/.claude`, `~/.codex`, `~/.t3` or
`T3CODE_HOME` / `EMBER_T3_HOME`) or its agent is registered; T3 Code's row
always shows (`listedWhenUndetected`), so it can be turned on before T3's
first run. A per-agent off is remembered (`~/.config/ember/producers.json`, migrated once from the `producers.optOut` default), so the master
switch neither counts that agent as "partial" nor turns it back on; with every
detected agent opted out, master on clears the opt-outs. On launch an agent
case new since the last launch (`producers.knownAgents`) starts opted out when
some agent is already registered. An agent whose CLI LaunchAgent plist (same
label) is in `~/Library/LaunchAgents` shows as installed from the CLI and is
never registered alongside it; Move to Ember runs the helper's `uninstall`
first. The master switch turns off every registered agent. The Claude row also says how its hooks are
registered and whether the kill switch pauses them.

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
server — falls back to the defaults). While a snapshot exists, a Settings ›
Clock edit of either key is not written to the device (it would resume
rotation or free the buttons mid-focus): `applyMenuSettings` stores it in the
snapshot (and its persisted copy), so the restore applies it when the block
ends; the edit's other keys go to the clock as usual. `GET
/v1/device/settings` meanwhile reports the snapshot's values for the two keys,
so the toggles show the user's choice rather than the takeover's; both calls
name the keys answered that way in an `X-Ember-Deferred-Keys` header.
`priorMu` (a leaf lock) serialises those edits with the snapshot read and the
restore, so a start or stop edge can wait out one menu call (8 s) or one
restore (5 s); an edit's own waits give up at its 25 s write budget. Edits
outside focus write unlocked; one that overlaps a takeover edge is folded
into the new snapshot afterwards (`priorGen`), unless a later edit already
set the key (per-key edit sequence numbers). A lost restore backs off
`restoreBackoffTicks` (5) publishes so an offline clock doesn't stall the
coordinator every tick. NG persists settings across reboots,
so a takeover left behind by a dead server would stick: the snapshot is therefore also persisted to the
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
`work_start`/`work_end` as `null` on a day with no work). The stats handlers
read these knobs per request: `day_start_hour` (0-23, default 4: earlier
activity counts to the previous day), `streak_grace_days` (missed days a
streak tolerates, default 1, `0` = strict), `work_hours_gap_minutes` (default
15), `work_hours_include_activity`;
**unauthenticated**
`POST /hooks/awtrix/button` (the device can't send a token) mapping
middle=pause/resume/start, right=skip, left=stop — all on press (the AWTRIX3-era
left+right chord is removed).

**Pomodoro state** (`cmd/ember/pomodoro_state.go`, #338). The readers of the
timer go through one projection, `App.pomodoroState(now)` → `pomodoroState`:
`On` (an engine exists and `pomodoro.enabled`), the engine `Status`, and
`Counting`/`EndsAt` (running and not paused) from one `Engine.Snapshot`. The
engine stays the only authority; the state knows nothing about devices.
`active()` (on and not idle) gates the clock's takeover view (`pomoView`, which
adds the colours), `busy()` (on and running or paused) holds back a knob OTA
offer, and the knob's `pomo` block (`knobPomo`) is `null` when off and sends
`ends_at` while counting, `remaining_sec` otherwise. The action replies and
`GET /v1/pomodoro/state` stay the engine `Status`, behind the same on check.

### Day clock — `internal/workday`, `cmd/ember/day_*.go` (#729)

A workday the user starts and stops on the knob, with a stop time set at
clock-in (spec: vault `Personal/Ember/Specs/2026-10-27-work-tracking-design.md`).
Agents are never stopped; off the clock Ember is quiet, and replies sent off
the clock are counted, not blocked. No prompt text is involved anywhere.

- **Events, not state.** The store (`internal/pomodoro/daylog.go`, same
  sqlite file) keeps `day_events(at, kind, value)` with kinds `start`
  (value: the stop time), `break`, `resume`, `stop`, `auto_stop`, `stop_at`
  (value: the new stop time), and `day_replies(at, source, tool,
  session_key, repo, exact)` (unique on `source, tool, session_key, at`;
  `repo` and `exact` added by #730, older rows are estimated); both are pruned at 400 days by the day tick. `workday.Replay`
  folds events, ordered by `(at, id)`, into periods (start, end, stop time,
  breaks, auto); events outside a period are ignored. Every new event's `at`
  is at least the previous one's, so replay order matches write order. On
  start (`ensureStore` → `loadDay`) the server replays the events from the
  last `start` on (`DayEventsFromLastStart`, so an open period is always
  whole): the open period (if any), the last stop time used, the last
  clock-out, and the replies since it. A restart therefore keeps the day state; it drops
  only the start offer and the reaped-session memory below.
- **Times.** Server local time; days by `pomodoro.day_start_hour` (4), the
  boundary built as a local wall time (`Calendar.DayStart`), so it stays at
  04:00 across DST changes.
  `stop_at` is `HH:MM` resolved inside the start's logical day
  (`(day start, day start + 24 h]`), so `00:00` after an 11:00 start is the
  coming midnight. `start {at}` resolves `HH:MM` to the latest past instant
  and is refused before the last day event.
- **Actions** (`/v1/day/*`, control or device token, like Pomodoro). `start`
  while on, `break` on a break, `resume` while on, `stop` while off and
  `offer/dismiss` without an offer are no-ops that answer the state; `break`
  and `resume` while off are 409. The reply is always the `day` block shape.
- **Reply detection** (`noteDayReply`, called from `App.Upsert`). A session
  whose prior state was `done` or `waiting` and that posts `running` is one
  reply, at most one row per session per minute. New sessions never count,
  which keeps `codex exec`, SDK reviews and `claude -p` out. A done session
  is reaped 30 s after its last post (`done_ttl_seconds`), and the Codex and
  T3 producers `DELETE` a done session once their 5-min window ends, both
  long before most replies. So the server remembers reaped or deleted
  `done`/`waiting` keys for 24 h from their last update (`onSessionReaped`
  and `App.Delete` → `noteDaySessionEnded`, in memory) and treats a later
  `running` on that key as a reply. Accepted
  false positives: a T3 thread resumed by a delegated child or a schedule, a
  Claude session woken by a background task after it posted `done`. Misses:
  a reply more than 24 h after the session went quiet, or after a restart
  that dropped the memory.
- **Exact replies** (#730, `POST /v1/day/replies`, `recordExactReplies`).
  The producers post the user's own prompts, never their text: the Claude
  hook on `UserPromptSubmit` (entrypoint `cli` without an Agent SDK version,
  or `claude-desktop`) and `ember-claude-producer backfill` over the
  transcripts; the T3 producer from `orchestration_v2_projection_messages`
  (`role = 'user'`, `createdBy = 'user'`, not streaming, not in a subagent
  thread) behind a cursor in its state dir (`t3-replies.cursor`, first run
  60 days back); the Codex producer from `codex-tui` rollouts
  (`response_item` user messages not starting with `#` or `<`). All skip
  text starting with `<task-notification>`, `<task>`, `<local-command`,
  `<system-reminder>`, `<bash-stdout>` or `<bash-stderr>`
  (`producer.AutomatedPrompt`). `repo` is the main checkout's folder
  (`producer.RepoFromCwd`: `git rev-parse --git-common-dir`, else the T3,
  `.claude/worktrees`, `.worktrees` and `-wt` path rules), empty with
  `send_project` off. An exact reply within 2 s of another exact one for the
  same session key is the same prompt (`DayExactSameSpan`: the hook stamps
  its own clock, backfill the transcript's), so a replay or a rerun backfill
  adds nothing; one that hits an estimated row's key turns that row exact. A newly inserted reply within 2 min of now
  takes the estimate's live path (`liveReply`: per-session throttle,
  off-clock count, offer); older ones only go into history, with no offer,
  no quiet change and no knob wake.
- **Exact beats the estimate.** `dayTracker.exactAt` holds each `(source,
  tool)` pair's newest exact `at` (loaded from the store on start, dropped
  after 24 h). While a pair has one in the last 24 h, `noteDayReply` records
  no estimate for it; a pair that never posts exact replies (an older
  producer, another computer) keeps the estimate. History and the
  off-clock count on start (`DayRepliesBetween`, `CountDayReplies`) drop an
  estimated row when its pair has an exact one within 24 h either side, so
  a backfill replaces the estimates it overlaps.
- **Offer start.** Off, and no clock-in yet in this logical day: the first
  reply sets `offer_start` for 60 min, until a start or a dismiss; once
  raised, no new offer that day. Confirming is `start {at: offer_start}`, so
  the period (and the replies in it) is back-dated. In memory only.
- **Auto clock-out** (`dayTick`, every 15 s from `StartCoordinator`, and once
  at start-up). On or on a break, past the stop time, with no reply for
  `day.auto_stop_minutes` (30): `auto_stop` at max(stop time, last reply,
  last event). Also at the next day start (04:00) whatever the replies.
  No summary for an auto stop.
- **Quiet when off.** `App.quietAt(t) = Config.quietAt(t) || dayQuiet()`,
  where `dayQuiet` is `day.enabled`, a clock-in on record (so
  a server that never saw one, such as one updated before the knob page
  ships, keeps its sounds) and no open period. The coordinator reads it
  through its `quiet` hook (notice sound, chime, quiet LED), the reminder
  handler and loop guard call it, and the knob view's `quiet` uses it. Each
  day change calls `dayChanged`: `topicDay` wakes knob long polls and a
  coordinator tick refreshes the LED, like a quiet-hours edge.
- **Knob `day` block** (`dayBlock`, only for a knob whose caps list the
  `day` page with the page on; never for a legacy knob): see
  DEVICE-PROTOCOL "Day clock". `on_min`, `break_min` and `over_min` move each
  minute while on, so the view's ETag does too; the 5 s long-poll recheck
  delivers it. `breaks` lists the open period's finished breaks (the latest
  16, `dayKnobBreaks`) so the knob can draw them as gaps. `utc_offset` is
  the day zone's offset at `now`, so the knob reads the `HH:MM` values in
  Ember's zone rather than the weather location's. The block is the last one `fit` drops before `mood`.
- **History** (`GET /v1/day/history`, `/weeks`) is recomputed from events and
  replies per request (`workday.Calendar.Days`, `Weeks`): a day's `start` is
  its first clock-in, `stop`/`stop_target`/`auto`/`in_range` come from its
  last period, `in_range` is `stop <= stop target + day.grace_minutes`.
  A reply inside any period (bounds included) is on the clock, otherwise off;
  replies count toward their own logical day.

### Weather — `cmd/ember/weather.go`, `weather_providers.go`

A standalone widget that shows current conditions. The server fetches them
**itself** (not via a producer) from a free, key-less provider —
**Open-Meteo** by default, **MET Norway** selectable — on a `refresh_minutes`
cadence. Provider codes (WMO for Open-Meteo, `symbol_code` for MET) map to six
render buckets (`clear/clouds/fog/rain/snow/storm`) + a `severe` flag
(`internal/render` `weather.go`). The fetch also pulls the next ~24 **hourly
temperatures** and (Open-Meteo only) the location's **UTC offset**
(`&timezone=auto` → `utc_offset_seconds`). The latest observation lives in an
in-memory `weatherStore`, the single source: one poller fills it and nothing
fetches per device. Each fetch that stores or confirms an observation also
saves it in the settings store (`weather_snapshot`, `weather_snapshot.go`,
#497: the source tag, MET's `Last-Modified` and the observation; no
credential), and the first poll after boot restores it when its source
matches the configured one and it is still fresh (`weatherStaleAfter` past
`FetchedAt`/`ValidUntil`), with `lastFetch` = its `FetchedAt`. So a restart
under a keyed provider's 5-min floor still serves weather, and doesn't
refetch before `refresh_minutes`. Units aren't part of the tag: the
observation is in °C either way. The store is tagged with the source it serves, provider,
Open-Meteo model and location (`weatherSource`; #409 for the provider, #350
added the location, #427 the model): `state()` hides an observation tagged
with another source, so after a provider, model or location change every view
shows weather as absent until the new source's first fetch. The model is part
of the tag only under Open-Meteo, so changing it while MET Norway serves
changes nothing. On every poll, `pollWeather` retags the store through
`switchSource` when the configured source differs, clearing the observation,
`lastFetch`, the MET validators and the popup's previous condition.
`StartWeather` also wakes on change notifications and polls at once on a
mismatch; that poll fetches only when weather is enabled. A fetch that
finishes after the source changed is dropped (it doesn't set `lastFetch`
either), so a slow old-source reply can't land. While the store has no
observation for its source (after a switch or at start), a failed fetch is
retried after 2 min (`weatherRetryBase`) instead of a full `refresh_minutes`,
and each further consecutive failure doubles the delay (2, 4, 8 min …) up to
`refresh_minutes` (`weatherRetryDelay`), so a permanent error (a MET 403, an
outage) settles at the normal cadence rather than ~720 calls a day. A success
or a source switch resets the count. A provider's `Retry-After` (seconds or
HTTP date, carried on `weatherHTTPError`) is a floor on the next attempt
whether or not an observation is shown, capped at `refresh_minutes`
(`markWeatherFailedAfter`): a year, a 2099 date or a second count past
`time.Duration` would otherwise freeze weather for days. `parseRetryAfter`
clamps the seconds to 24 h (`weatherRetryAfterMax`) before converting, and a
past date, a negative or a non-number gives no floor. With an observation on
screen a failure keeps the normal cadence. The air-quality call runs on every
minute poll, before the weather `due` check, so turning the air tile on
fetches within a minute. It is skipped while the stored reading is younger
than `refresh_minutes`, so weather retries don't refetch a reading that just
succeeded. While it fails it backs off like weather: `airFailures` counts
consecutive failures and `airDue` waits `weatherRetryDelay(airFailures,
refresh)` after the last attempt (2, 4, 8 min … up to `refresh_minutes`, so
~8 calls in an hour of failures, and every minute at `refresh_minutes: 1`),
with the error's `Retry-After` as a floor capped at `refresh_minutes`
(`markAirFailed`). A success resets the count and floor; a location switch
clears the reading, the count, the floor and the last-attempt time.

**Providers and models** (#427, `weather_providers.go`). `provider` is one
of `weatherProviders`; under Open-Meteo, `open_meteo_model` picks the
forecast model from the fixed `openMeteoModels` list (default `best_match`,
which sends no `models=`; any other adds `&models=<id>`). With one model
Open-Meteo answers under the plain variable names, so parsing is the same.
An unknown model (a comma list included) is a 400 on `PUT
/v1/weather/config`. In `config.json`, at startup and on `/admin/reload`,
`sanitizeConfigBaseline` logs `weather.open_meteo_model invalid` and falls
back to `best_match`, like an invalid icon id (`validateConfig` doesn't run
`validateWeather`). In a stored `weather_json` blob only that key is
dropped (logged `persisted weather model ignored`), so a model removed in a
later release doesn't throw away the rest of the weather settings. The
merge then keeps the current model (the `config.json` one, `best_match`
by default) and the reapply stores the blob with it, so the warning shows
once.
The app keeps the user-facing descriptions
(`EmberKit/Settings/WeatherSources.swift`, shown under the pickers); this
table mirrors them, and `TestAppWeatherSourcesMatchTheServerLists` keeps the
app's ids equal to the server's. A new provider is one id in `weatherProviders`, a fetch
branch, and an entry in `WeatherSources.providers` (with `keyEnv` when it
needs a server-side key: `providers_available` in `GET/PUT
/v1/weather/config` then decides whether the app offers it, and a PUT
switching to it without the key is a 400 (`settingSpec.check`, run on
PUTs only, never on a reapply). The stored provider is never rewritten at
startup: a choice whose key went away stays chosen, Ember makes no fetch
for it, `/v1/weather/state` answers `error` with the reason
(`weatherUnavailableReason`), the startup log warns, and the app says so
under the picker; the user can pick another provider, and fetches resume
on the first start with the key). Values checked on
2026-10-11 at Belgrade and New York (#427):

| Provider / model | Coverage and resolution | Limits in Ember |
|---|---|---|
| Open-Meteo | Worldwide, free, no key | Non-commercial use, 10,000 calls a day; 15-min rain; also serves air quality for every provider |
| └ `best_match` (default) | Best local model blended with a global one; central Europe is mostly DWD ICON | Can miss showers other models see (#427: rain in Belgrade, 0 mm here); 15-min rain split from hourly outside central Europe and the US |
| └ `ecmwf_ifs` | ECMWF IFS HRES 9 km, worldwide, 4 runs a day | No UV; 15-min rain is the hourly amount split evenly |
| └ `ecmwf_ifs025` | ECMWF IFS open data 0.25° (~25 km), 4 runs a day | No UV; coarse, smooths local showers; 15-min rain split from hourly |
| └ `icon_seamless` | DWD ICON-D2 2 km (Germany, Alps), ICON-EU 7 km, global elsewhere | No UV; 15-min rain split from hourly outside the ICON-D2 area |
| └ `gfs_seamless` | NOAA HRRR 3 km (contiguous US, hourly runs), GFS 13–25 km elsewhere | 15-min rain split from hourly outside the US |
| └ `ukmo_seamless` | UK Met Office UKV 2 km (UK, Ireland), global 10 km | UV only near the UK and for the first 2 days; 15-min rain split from hourly |
| MET Norway | Worldwide, free, no key; MEPS 2.5 km in the Nordics, ECMWF elsewhere | Hourly rain steps; chance of rain only in the Nordic area; needs an identifying User-Agent; no air quality |
| Tomorrow.io | Worldwide, own blend; needs `EMBER_TOMORROW_API_KEY` (free plan) | Free plan: 500 calls a day, 25 an hour, so at most one fetch per 5 min; minutely rain for the next hour where the plan provides it (the docs call it premium), otherwise hourly, split into 15-min steps; days run 06:00–06:00 local; codes translated to WMO; no air quality. Choosable only while the key is set (`providers_available`) |
| WeatherAPI.com | Worldwide, own blend; needs `EMBER_WEATHERAPI_KEY` (free plan) | Free plan: 100,000 calls a month and 3 forecast days (today + 2), at most one fetch per 5 min (~8,600 a month); no minutely data, so rain is hourly `precip_mm` split into 15-min steps; codes translated to WMO; no air quality. Choosable only while the key is set |

The Tomorrow.io and WeatherAPI.com rows come from their documentation, not
a probe (no key; #432, #437). Left out: `meteofrance_seamless` returns no chance of rain (hourly or daily)
and only 4 days. Every listed model returns `current`, `minutely_15`
precipitation and the daily `precipitation_probability_max`; a value a model
lacks (UV for most) is nil and left out of the knob view like any missing value.

**Face data** (#350, `weather_providers.go`). Each fetch also fills the
observation's knob-face fields, nothing persisted: `Now` (cloud %, UV,
wind and gust km/h, wind direction; each nil when the provider lacks it),
`Precip` (the next 12 h of precipitation steps, mm/h plus a snow flag, every
`PrecipStep`), `Days` (today plus the next 3 local days: condition, min/max
°C, precipitation mm, probability, UV max, gust max; nil fields unknown).
A day is `[Start, End)` between two true local midnights (`dayClock`,
`weather_days.go`; #434, #440): the zone is the provider's (Open-Meteo
`timezone`, WeatherAPI.com `tz_id`) or, for MET Norway and Tomorrow.io,
the air reading's `timezone` passed in the fetch hint, loaded from the
embedded tzdata (`loadZone` accepts only plain zone paths, never
`Local`). With no zone, days fall back to midnights under the single
offset and `End` = `Start` + 24 h. A DST day is 23 h or 25 h, MET's
periods are split at the true midnight (the splitter always moves
forward), and `faceDays` picks today by `[Start, End)`, so a cached
observation selects the right day across a change. Where DST starts at
midnight (America/Santiago 2026-09-06, America/Havana 2027-03-14) the
date has no 00:00: `midnight` takes the transition, the date's first
instant (01:00), so the day before ends there and the gap day lasts 23 h;
`TestDayClockOverEveryZoneForTwoYears` checks every embedded zone for two
years (no empty day, days abut, MET's splitter returns). The knob gets
`t` = `Start` and `end` = `End`.

- **Open-Meteo**, one call of 17 variables: `current=` the seven current
  values, `minutely_15=precipitation,snowfall&forecast_minutely_15=48` (mm
  per 15 min, ×4 to mm/h; snow when `snowfall` > 0; a `null` step is left
  out; Open-Meteo stamps a step with its end, the sum covers the preceding
  15 min, so the step's `At` is the stamp − 15 min, MET's convention, where
  `next_1_hours` at `t` covers `[t, t+1h)`), `hourly=temperature_2m`, and
  `daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,uv_index_max,wind_gusts_10m_max&forecast_days=4`
  (day `time` is the date's midnight under the single request-time
  `utc_offset_seconds`, not its true midnight: in Belgrade 10-26 arrives
  as 10-25 23:00+01. Ember takes the date from `time` +
  `utc_offset_seconds` and builds `[Start, End)` from that date in the
  reply's `timezone`).
  About 3 KB.
- **MET Norway** reads `locationforecast/2.0/complete` (not `compact`): UV
  (`ultraviolet_index_clear_sky`, short range, clear sky), gusts and
  probabilities (Nordic only) and the 6-h min/max exist only there. ~90 KB
  raw, ~7 KB gzipped (the client asks for gzip). The current values
  (condition, code, temperature, severe, overlay, `Now` in m/s × 3.6 and
  `ObservedAt`) come from the timeseries entry at or before now: the
  observation keeps the first 24 entries as `Steps`, and `newWeatherState`
  projects the latest one not after `now` (`weatherObservation.current`), so
  they move on each hour while MET is skipped until `Expires` or answers
  304. Open-Meteo has no `Steps`; its `current` block is refetched every
  poll. `Precip` is the leading run of 1-h entries'
  `next_1_hours.precipitation_amount` (snow for a snow or sleet symbol, at
  most 14). `Days` are aggregated per local day: an entry's instant values
  go to the day of its time; its period is `next_1_hours` when the next
  entry is 1 h later, else `next_6_hours` over the gap (at most 6 h). A
  period that crosses local midnight (a 6-h period at UTC+2, a 1-h one at a
  half-hour offset) counts for every day it overlaps: its precipitation is
  split by the overlap (amount × overlap / period length, so a 2-h gap isn't
  counted 6 h), and its probability and its symbol go to each such day. Its
  6-h min/max go only to the day that holds most of the period (the earlier
  one on a 3-h + 3-h split; the last day the gap reaches when the next entry
  comes before the period's midpoint), so at UTC+2 the 20:00–02:00 evening
  max isn't also the next day's. The condition is the worst symbol of the day's
  periods (storm > snow > rain > fog > clouds > clear) and `Code` that
  symbol: `next_1_hours` in the hourly range, where a 6-h window would reach
  hours outside the day, and `next_6_hours` beyond it. Probability and UV
  are maxima of what the entries carry.
- **MET caching.** The store keeps `Last-Modified` and `Expires`
  (`lastModified`, `notBefore`; cleared with the observation on a provider
  or location switch, so a new location neither waits for the old `Expires`
  nor sends the old `Last-Modified`, which MET would answer with a 304 for
  the wrong city). `Expires` is read relative to the response's `Date`: the
  stored instant is the poll time plus `Expires` − `Date` − `Age`, kept
  between zero and `max(refresh, 1h)` (`maxProviderFreshness`). `Age` is the
  seconds a cache in between held the response, capped at 2^31 as RFC 9111
  says, also when it overflows int64. So a MET or Ember clock that is off
  neither skips MET for hours nor refetches on every poll, a cached copy
  isn't fresh for longer than MET meant, and a replayed old `Date` can't
  park polling or keep stale data fresh
  (`TestMetNoCapsFreshnessFromAReplayedDate`). Without a `Date` the
  absolute `Expires` is kept. A due poll before `Expires` skips the MET call
  but still moves `lastFetch`, so the next due poll checks again; after
  `Expires` the request carries `If-Modified-Since`. A **304** keeps the
  observation, sets its `FetchedAt` to now and its `ValidUntil` and
  `notBefore` to the new `Expires`, and notifies (the `stale` flag may
  move). With the 10-min default that is about one call per 40 min instead
  of every 10.
- **Freshness** is measured from the later of `FetchedAt` and the
  observation's `ValidUntil` (MET's `Expires`), so skipping MET until
  `Expires` never marks the data stale early.
- **Local day for MET.** MET sends no UTC offset. The air call (fetched
  just before) carries `utc_offset_seconds`, and the MET observation takes
  it as its `TZOffsetSeconds`/`TZKnown` (and its `timezone` for the day
  boundaries), so day buckets, sun times and labels follow DST, but only while air is on (tile or popup threshold)
  and its reading is fresh (younger than `weatherStaleAfter`). A location
  change clears the air reading with the observation, and a late air reply
  for the old location is dropped, so the offset is always this
  location's. Otherwise (air off, failing for 30 min, or not yet fetched
  here) the longitude fallback is used.
- **Tomorrow.io** (#432, `weather_tomorrow.go`) reads
  `/v4/weather/forecast?location=lat,lon&units=metric&apikey=…` with no
  `timesteps`, so the plan's default timelines come back (minutely, hourly
  120 h, daily; ~170 KB raw by the documented schema, gzipped in transit). Units are metric, so wind
  and gusts are m/s × 3.6. Weather codes are translated to WMO
  (`tomorrowWMOCodes`: 1000→0, 1100→1, 1101→2, 1102/1001→3, 2000/2100→45,
  4000→53, 4200→61, 4001→63, 4201→65, 5001/5100→71, 5000→73, 5101→75,
  6000→56, 6200/6001→66, 6201→67, 7xxx→77, 8000→95; 0 and unknown codes
  leave `Code` empty and the condition clouds), so condition, severe,
  overlay and the knob's faces reuse the Open-Meteo mapping and cinder
  needs no change. `Steps` are the first minutely entry (the current
  values, its `time` the observation time) then the hourly entries after
  it, 24 in all, projected like MET's. `Precip` is 15-min steps, at most
  48: minutely entries averaged per quarter hour (intensity = rain + freezing
  rain + snow + sleet mm/h; snow when snow + sleet outweigh rain in most of
  the quarter's minutes), then each hourly entry split into four quarters
  over `[t, t+1h)` from where the minutely ones end; a step with no
  intensity at all is left out. `Days` come from the daily timeline:
  Tomorrow.io's day runs 06:00–06:00 local (`time` is 06:00 local in UTC),
  so each entry goes to the local date of its `time` (zone and offset from
  the air call like MET, else the longitude fallback), a day that ended before the
  current step is skipped (the first entry is often yesterday's period).
  Min/max, `precipitationProbabilityMax`, `uvIndexMax`, `windGustMax`;
  precipitation is `rainAccumulationSum` plus the snow, sleet and ice
  liquid-water sums. The day code (`tomorrowDayCode`) covers the same
  06:00–06:00 window as these aggregates: each hourly `weatherCode` in the
  window counts its hour, the daily `weatherCodeMax` counts the hours the
  hourly timeline doesn't cover from the current hour on (today's past
  hours count for nothing), and `pickDayCode` decides (see "Keyed
  providers"). A day past the hourly range takes `weatherCodeMax`, which is
  the numerically highest code, not the worst weather (4200 light rain
  beats 4001 rain). Sun and moon
  times in the reply are ignored (Ember's astro). Rate limits, errors
  and the key: "Keyed providers".
- **WeatherAPI.com** (#437, `weather_weatherapi.go`) reads
  `/v1/forecast.json?key=…&q=lat,lon&days=3&aqi=no&alerts=no` (the free
  plan's 3 days; ~50 KB raw by the documented schema). `location.tz_id`
  gives the real UTC offset at the observation (loaded from the embedded
  tzdata; `Local`, a name that isn't a plain zone path or one that doesn't
  load falls back to the air call's offset, then longitude). `current` is
  the observation (`last_updated_epoch`; a `current` without it is ignored
  and the hour holding now stands in), then the hourly entries after it,
  24 `Steps` in all. A reply that yields no step at all (no timed
  `current`, no hour from now on) is `weatherapi: invalid response`, so it
  never replaces the cached observation.
  Units: `wind_kph`/`gust_kph` are km/h already, `wind_degree` is where the
  wind comes from. Condition codes are translated to WMO
  (`weatherAPIWMOCodes`: 1000→0, 1003→2, 1006/1009→3; mist, haze, smoke
  and dust 1012–1048 and fog 1135→45, freezing fog 1147→48; patchy rain
  possible 1063 and light showers 1240→80; drizzle 1150/1153→51;
  freezing drizzle 1072/1168→56, 1171→57; rain 1180/1183→61,
  1186/1189→63, 1192/1195→65; freezing rain 1198→66, 1201→67; showers
  1243→81, 1246→82; snow and sleet 1204/1210/1213→71,
  1114/1207/1216/1219→73, 1117/1222/1225→75; ice pellets
  1237/1261/1264→77; snow and sleet showers 1066/1069/1249/1252/1255→85,
  1258→86; thunder 1087/1273–1282→95, so "thundery outbreaks possible"
  counts as a storm and can raise the severe alert like Open-Meteo's 95).
  There is no minutely data: `Precip` is each hourly `precip_mm` (mm in
  the hour = mm/h) over `[t, t+1h)` (the docs don't say which hour the
  amount covers; this is MET's convention), split into four 15-min steps
  from the hour holding now, at most 48 (12 h); snow when `snow_cm` > 0 or
  the code is a snow code. `Days` are the dates of `forecastday`, between
  true local midnights in the `tz_id` zone. `mintemp_c`/`maxtemp_c`,
  `totalprecip_mm`, pop = max(`daily_chance_of_rain`,
  `daily_chance_of_snow`), UV max = max of `day.uv` and the hours' `uv`,
  gust max = max of the hours' `gust_kph` (there is no daily gust). The
  day code counts the day's hours from the hour holding now on
  (`pickDayCode`); a day with none of them left takes `day.condition`.
- **Keyed providers** (`weather_keyed.go`; Tomorrow.io, WeatherAPI.com). A
  provider in `keyedProviders` names its env var and minimum interval.
  `providers_available` lists it only while its key is set, a PUT
  switching to it without the key is a 400 (`settingSpec.check`, PUTs
  only), and a keyless choice stays chosen with no fetch and a
  `/v1/weather/state` `error` (`weatherUnavailableReason`).
  - **Day code** (`pickDayCode`): hours are counted per WMO code, and the
    code with the highest severity × hours wins (ties to the more severe).
    Severity is the index in `wmoSeverity` plus one: clear 1, clouds, fog,
    drizzle and freezing drizzle, then rain by strength, each strength's
    showers just above its plain rain (light freezing rain 66 sits after
    light showers and below moderate rain; heavy freezing rain 67 is the
    top rain code, above heavy showers), snow, storm last. So 10 h of heavy rain beat 1 h of
    flurries, 23 h of clear beat 1 h of fog, and 1 h of thunder still
    outweighs a few cloudy hours. Providers' codes are pooled after the
    WMO translation, so codes sharing a WMO code count together
    (Tomorrow.io 1102 + 1001 as overcast 3, 2000 + 2100 as fog 45, all ice
    pellets as 77, ranked between light snow showers and moderate snow).
    One scale for every provider was chosen over Tomorrow.io's own order:
    the knob only ever sees the WMO code, so the ranking should be its.
  - **Quota** (one `providerQuota` per provider, since each key has its
    own): refresh and failure retries never run faster than every 5 min
    (`minFetchInterval`). A 429, 401 or 403 waits for `Retry-After` or the
    next top of the hour (a bad key or a used-up monthly quota isn't
    retried every 5 min), not capped at the refresh interval
    (`retryAfterCap`). Only a 429's deadline is persisted
    (`savedRetryAt`); a 401/403 hold lives in memory, because the key only
    changes with a restart, which tries the fixed key within 5 min (the
    persisted last attempt still holds the floor). The state survives `switchSource`, so editing the
    location or switching providers and back neither fetches sooner than
    5 min after the last attempt nor skips a running back-off, and a
    failure whose source changed mid-flight still sets it. `reserveQuota`
    checks the floor and the back-off and records the attempt under one
    lock, so two polls can't both pass; a later failure never shortens a
    longer deadline. The attempt time is taken right before the request
    (after the poll's sun and air work), and a back-off counts from the
    response (`Retry-After` plus the request's duration), the next-hour
    fallback likewise. Polls run on a 1-min ticker, so the floor allows
    30 s for the tick's jitter (`quotaTickSlack`): at least 4.5 min between
    calls. Ticks alone keep a 5-min cadence (12 an hour, 288 a day), but a
    source change polls at once, so flipping providers can land a call on
    the 4.5-min floor: at most 14 in a rolling hour and 320 in a UTC day
    (only with a source change every 4.5 min all day), inside
    Tomorrow.io's 25 and 500. An unreadable row warns once per failing
    streak (and logs its recovery), not every poll. Both times persist per provider in the
    settings store (`weather_quota_tomorrow_io`, `weather_quota_weatherapi`:
    `attempt_at`, `retry_until`, no credential) and are loaded before the
    provider's first fetch, so a restart can't bypass them. A loaded
    `attempt_at` later than now is dropped and `retry_until` is capped at
    now + 24 h, so a row written while the clock was ahead can't block for
    longer. If the row can't be read the load fails closed: the floor
    counts from that first poll and the read is retried on the next one.
    A failed write is only logged; the in-memory reservation still holds.
  - **Errors** (`keyedFetchError`) are fixed text: `weather http <code>`
    (numeric status, no reason phrase, no body), `<provider>: request
    failed` (transport, with the context error when cancelled) or
    `<provider>: invalid response` (decoding, or a reply with nothing in
    it). A reply's text never reaches a log, so a key echoed in a body, a
    reason phrase or a field (a `time.ParseError` quotes its input) can't
    leak, whole or cut. The key goes only in the query string.

**Weather state** (`cmd/ember/weather_state.go`, #337). Every reader of the
stored weather goes through one projection, `weatherStore.state(cfg, now)` →
`weatherState` (`newWeatherState` for drafts and tests): the observation and
air reading, `Fresh`/`AirFresh` (younger than `weatherStaleAfter`, 30 min;
false when missing), `enabled`, the location's `LocalDay`, `Night` and the
local day's sun crossings (`Sun`, a `sunCrossings` with a flag per
crossing, none without coordinates). It
knows nothing about devices; the freshness rule and the sun math live only
here. Its readers: the clock tiles (`live` = enabled and fresh; views and the
moon read the state), the sun popups (location and label offset), the knob view
(`knobWeather`), the dashboard's `/v1/weather/state` and the weather preview
(which reads the store under the saved source, so a draft location keeps the
live observation, and swaps in sample observations when the store has none;
with a draft location it therefore shows the saved city's conditions and
forecast with the draft city's sun times, night and local day, computed with
the saved city's UTC offset). Air is served only for the store's location, so
between a location save and the poller's retag no view shows the old
location's air.
The condition, interval, severe and AQI popups are not readers: they fire on a
due poll and take the series' entry for now (`weatherObservation.current`),
whether the poll brought a full fetch, was skipped until MET's `Expires` or
got a 304; a 200 whose series starts before now can't rewind the condition
and replay a popup or chime, so they trail the hourly-advancing display by
at most `refresh_minutes`. Presentation stays with each reader (`roundedSun`
gives the 5-min rounding the dashboard and knob share, and only when both
crossings exist: their wire sun stays both-null or both-set, #423, while
the sun popups and the moon use a lone crossing). The state shares the
store's `Hourly`/`HourlyAQI` slices: readers treat them as read-only, and the
poller only ever replaces whole observations. `faces()` (`weather_faces.go`,
#350) derives the knob-face values from the state, on demand because the moon
scan is the costly part: the offset in use, the rain spells of the next 12 h
relative to now (wet ≥ 0.1 mm/h, ≤ 30-min gaps merged, peak intensity, at most
4, ended ones dropped), the current step's intensity and the end of the spell
containing now (none when that end is only where the steps run out), today
(the day containing now, none once the observation's days are over; its
min/max widened to the current temperature) and the next 3 days, the local
twilight and the moon's phase, lit fraction, up flag and up window (no
location: phase and lit fraction only).

The coordinator reconciles three rotating tiles with the same
change-and-staleness dedupe as the usage card:

- **`ember-weather`** — 8×8 condition icon + the current temperature **centred**
  in the free area (rows 1–5), its digits in the strip's `TempColor` gradient
  colour (degree sign white; coloured by °C in either unit) + a per-hour
  **forecast strip** on the bottom bar
  (row 7, cols 8–31; each hour takes `24/N` columns from col 8, see
  `hourSlot`),
  coloured by a cold→warm temperature gradient (`render.TempColor`). On a
  **clear night** the icon becomes the current **moon phase** (`moon_phase`;
  phase computed locally in `cmd/ember/astro_moon.go`, no API).
- **`ember-forecast`** (`forecast_tile`, default on) — **full-width hourly
  temperature bars** (no icon/temp — those live on the conditions tile, so the
  two tiles read differently at a glance); the bars sit on the same hour grid as
  the strips (cols 8–31, `24/N` columns each), so hour *i* lines up across the
  tiles and bar widths never alternate (`forecast_hours`, 1..24; bar height +
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
tile), so the **server provisions icons**: on startup, on every weather or
Pomodoro config apply, after a config reload and whenever the clock moves to a
new URL (rediscovery or a `PUT /v1/device/config`), it lists the clock's `/ICONS` folder (`GET
/api/v1/files?dir=/ICONS`), downloads any missing configured icon ID from the
LaMetric gallery (`.gif`→`.jpg` fallback, then the extensionless URL as a
last resort — some IDs, e.g. the Pomodoro tomato `29802`, exist only as a
PNG there, which is decoded and re-encoded as GIF locally since awtrix-ng's
upload only accepts GIF/JPEG magic bytes, answering PNG with 415; `pngToGIF`
refuses anything that isn't a PNG of at most 16×16, such as an HTML error page),
and uploads it (`multipart
POST /api/v1/files?dir=/ICONS`, `Publisher.ListIcons`/`PutIcon`). List failures abort the run;
per-icon failures log and retry on the next apply/restart. The startup run
comes from `initDeviceDiscovery`, once the boot rediscover has judged the URL
(`provisionClockInBackground`, which also starts the boot-ping install; both
run on `App.clockJobs`, a `clockJobGroup` that hands each job its context).
Shutdown closes the group first: it refuses new jobs from then on (so a handler
still running past the `server.Shutdown` deadline can't add to a WaitGroup
already being waited on) and cancels the context, so a job stalled on the lossy
link aborts instead of eating the deadline. A cancelled job logs at Debug, not
Warn, and the icon loop stops at the first cancel. Shutdown then stops the
clock migration before it waits for the workers and the clock jobs, so neither
a hung worker nor a hung job keeps `clockMigrate.stopAndWait()` from running
before the store closes. `reapplySettings` holds icon
provisioning (`iconHold`, released by `defer`) while it restores stored slices,
because the weather and Pomodoro `after` hooks fire before the clock URL
override (`clock` is registered last) and before rediscovery has judged that
URL, so a provisioning job started there listed `/ICONS` on a stale address
(#382); `/admin/reload` calls `provisionClockInBackground` after its reapply. Covers both the
weather condition icons and the Pomodoro tomato/coffee icons (`29802`/`6396`)
whenever their owning feature is enabled.

**Weather preview** — `GET /v1/weather/preview` (open, read-only, mirrors
`/v1/preview`): renders the tiles under draft query params (`rotate_in_apps`,
`forecast_tile`, `air_tile`, `forecast_hours`, `units`, `moon_phase`,
`lat`/`lon`) into the same
`{frames}` grids, using the live observations when present, else canned
samples (21 °C clouds, sinusoidal 24 h arc; AQI 42 easing off overnight) so it
never renders blank. The frames come from the pushed tiles' own `view`
(`previewTiles`, see "Rotating tiles" above), with the draft params laid over
the live config: `forecast_hours` follows the device's rule (<=0 or >24 means
24, 1..5 kept), and a clear night shows the moon phase exactly as the clock
does. Every absent param defaults from the saved weather config (as do a
non-integer `forecast_hours`, a `units` other than `metric`/`imperial`, and an
unparsable / out-of-range `lat`/`lon` pair), so a caller sending no params
(curl, a CLI) sees exactly the tiles the clock is sent. The macOS app sends
every param because the Settings pane previews an edit before its autosave
lands. Native-icon mode previews with the drawn sprite (the canvas can't
animate gallery icons), and the overlay is not drawn. Feeds the menu's
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
lat/lon (`astro.go` `sunTimes`, polar-safe), each crossing fired once within
a 2-min window (`sunPopupLog`); the label uses the location's real UTC offset
(longitude fallback for MET). Popups use a drawn icon by default; `use_native_icons` swaps in a
native AWTRIX/LaMetric animated weather icon by ID — per-condition IDs default to
widely-used gallery icons and are overridable from the menu (`icon_ids`) so the user
can curate from developer.lametric.com/icons. awtrix-ng honors a notification's own
`sound`/`soundRtttl` even when it also carries a `draw`/`icon` (the AWTRIX3-era
gotcha that forced chimes out-of-band via `/api/rtttl`/`/api/sound` is gone), so the
severe-alert chime rides directly on the popup payload. Config is
fully runtime-editable (`GET/PUT /v1/weather/config`, persisted to store key
`weather_json`), including `enabled` — so the menu can turn the whole widget on/off.

Weather constraints:
- **Icon ids are validated.** `WeatherConfig.IconIDs` values flow into a device
  file path (`/ICONS/<id>.<ext>`) and the LaMetric fetch URL, so they must be
  1-10 ASCII digits (`weatherIconIDPattern`); this is enforced in
  `validateWeather` and when the `config.json` baseline loads
  (`sanitizeConfigBaseline`). Optional toggles are pointers so absent (default
  on) differs from an explicit false/0; `fillAbsent` resolves nil at every entry
  point so marshalled config never contains nulls.
- **Poller timing.** The attempt time is recorded before fetching, so a provider
  failing at startup backs off a full refresh interval (api.met.no throttles
  aggressive clients). Failed fetches keep the last observation; the tile clears
  through the stale TTL. The interval-popup clock is seeded on the first
  observation so startup doesn't fire an interval popup at once. `sunPopupGrace`
  (2 min) with the 1-minute poll catches a sunrise/sunset without firing for one
  that passed before startup. The AQI threshold popup is edge-triggered and also
  fires on the very first reading, so a restart mid-episode still alerts.
- **Astro.** Moon phase and sun times are computed locally (no API or key).
  The sun is the low-precision sunrise equation, but with the ecliptic
  longitude of date (the perihelion longitude advances 1.71946° per century;
  without it the declination ran ~0.17° high in 2026). `sunHeight` is the
  sun's height at any instant with the declination of that instant and the
  hour angle measured between the transits either side of it (so the
  equation of time moves smoothly and the height has no jump at local
  midnight); `solarElevation` and `sunAt` both use it. `sunAt` (#423)
  solves for each crossing on its own, between the day's three extremes:
  the lowest point between the previous transit and this one, the highest
  around this transit, the lowest between this transit and the next. A span
  between two neighbouring extremes holds a crossing if the height changes
  side of the altitude across it; the direction is the sign at the span's
  end (rising if above), not which half of the day it is in. Each crossing
  is refined with Illinois regula falsi to half a second. An extreme sits at
  the transit or at the midpoint between two transits unless the height
  there is within 1e-3 of the altitude's sine; then a golden-section search
  over the quarter day either side finds the real one (the declination drift
  moves it a minute or two at Longyearbyen), so a dip under the altitude
  that lasts two minutes either side of local midnight still gives both
  crossings (Longyearbyen −6°, 2026-09-07 22:55 and 22:57 UTC). The low
  point between two transits is computed from the same inputs by both days,
  so day d's setting span ends exactly where day d+1's rising span starts
  and no crossing is lost or counted twice at the seam. Gating each crossing
  on its own span, not on the transit declination, keeps real crossings the
  old gate dropped: Tromsø's last sunrise before the midnight sun
  (2026-05-17 22:51 UTC) and first sunset after (2026-07-25 22:38 UTC), 1–2
  a year per site and altitude at Longyearbyen, McMurdo and Utqiaġvik.
  **The poles.** Within ~0.1° of a pole the daily swing of the height
  (∝ cos lat) is smaller than the declination's drift around the
  equinoxes, so the extreme moves hours from the transit (~2.5 h at 89.9°,
  far outside the old ±0.05 d search), and above ~89.97° the 1e-3 gate
  itself can skip a search that was needed. Where cos lat < 0.002
  (|lat| ≳ 89.89°) every extreme is searched; at the pole itself the height
  only drifts, the search lands on a window edge, and the single seasonal
  crossing falls in whichever span holds it, with the direction its sign
  gives (89.99°N's sunset on 2026-09-25 03:03 UTC sat in the half the old
  code only searched for a sunrise, so it was dropped).
  `sunAt` returns `sunCrossings` with a flag per crossing: the day before
  the midnight sun (Longyearbyen, 2026-04-18) has `HaveRise` and no
  `HaveSet`. `TestSunAtFindsEverySignChangeOfTheModelElevationAllYear`
  checks root-finding completeness, not astronomy: its oracle is the same
  model's `solarElevation` scanned minute by minute through 2026 (Tromsø,
  Longyearbyen, McMurdo, Utqiaġvik, 89.9°N, 89.95°N, 89.99°N, both poles
  and Amundsen–Scott at −89.99°; at −6°, −4°, −0.833° and +6°), and every
  sign change must be reported within a minute in its direction, and
  nothing else. A one-off run over 2024–2030 with 24 sites (equator, the
  date line, the polar circles, Reykjavík, Alert, Mawson, 89.5°–90° both
  hemispheres) at −18°, −12°, −6°, −4°, −0.833° and +6° found no miss.
  Accuracy is the NOAA test's job. Every crossing sits within 0.08° of its
  altitude (`TestSunCrossingsSitAtTheirAltitudeAllYear`) and within 10 s of
  where the elevation crosses it
  (`TestSunCrossingsConvergeOnTheElevationCrossing`). NOAA iterates once,
  so matching NOAA doesn't show convergence. Against the NOAA Solar
  Calculator (`TestTwilightMatchesNOAA`: London, Sydney, Quito, Berlin,
  Oslo, Tromsø at −6°, −4°, −0.833° and +6°) every crossing is within
  20 s; before the per-crossing declination, Oslo's evening crossings near
  the equinox were ~2.5 min late and Tromsø's first sunset after the
  midnight sun 23 min. Root-finding costs a few µs per `sunAt` call, more
  near the poles where every extreme is searched. Twilight (#350) generalises `sunTimes` to any altitude
  (`sunAt`): `localTwilight` gives the local day's crossings of −6° (civil
  dawn, dusk), −4° (blue hour ends, starts) and +6° (golden hour ends,
  starts); one the sun never makes is zero. The local day is the solar day:
  the crossings around the transit of the local date's solar noon (see "Sun
  times are the location's local day"), not those between local midnights,
  so a dusk after local midnight (Reykjavík, late July) is the date
  before's, like a post-midnight sunset
  (`TestTwilightIsTheSolarDayOfTheLocalDate`). The moon (`astro_moon.go`) is
  a truncated Meeus ch. 47 position (the 32 largest longitude terms with
  their distance terms, 16 latitude terms plus the additive ones; against
  Meeus example 47.a at 0h TD, evaluated at 0h − ΔT UT, longitude within
  0.0001°, latitude 0.004°, distance 19 km), taken at TT = UT + ΔT with ΔT a
  fixed 69 s (IERS, flat since 2017; a few seconds of drift moves a rise or
  set by well under a second). Without ΔT a rise or set moved 3-6 s up to
  60° latitude, 40 s at Tromsø and 84 s for Longyearbyen's grazing events:
  `moonPhaseAt` gives the cycle position (Moon minus Sun ecliptic longitude,
  0 new, 0.5 full) and the lit fraction from the elongation, and replaced
  the mean-synodic-month phase, which was up to ~15 h off, so the clock's
  moon icon uses it too. `moonEvents` scans the geocentric altitude on the
  whole-hour grid and bisects each crossing of the rise/set altitude 0.7275π
  − 0.5667° (Meeus ch. 15: parallax π at that instant's distance, less
  refraction and semi-diameter; +0.09° at apogee to +0.18° at perigee, where
  a fixed mean +0.125° was up to ~0.05° off) to the second, rounded to the
  minute. An hour whose ends are on the same side can still hold two
  crossings when the moon turns near the horizon (high latitudes: up
  11:59–12:34 UT at Longyearbyen on 2026-09-13). Near the horizon (an end
  within 1° of it; a turn inside one hour moves the altitude at most ~0.5°
  from the closer end) a golden-section search finds the hour's altitude
  extremum, and a crossing extremum is bisected on both sides. Against a
  1-minute scan of 2026 for Berlin, Oslo, Tromsø and Longyearbyen every
  event matches; the check costs ~15 % (`moonUpWindow` ~75 → ~88 µs, M4 Pro,
  mixed latitudes), the per-instant distance another ~30 % (~88 → ~115 µs).
  The hour grid makes the result independent of `now`, so a view's ETag
  doesn't move with the clock. `moonUpWindow` is the current interval above
  the horizon (rise in the past) or the next one, searched ±36 h; an end not
  found is zero (polar). It also returns `up`, whether the moon is above the
  horizon now: the ends alone can't tell "up for days" from "down for days"
  (both have neither). Checked against Open-Meteo's `daily=moonrise,moonset`
  (recorded once, test reference only) for four latitudes: within 70 s
  (`TestMoonRiseSetMatchOpenMeteoReference`); and against MET Norway's
  Sunrise 3.0 `moon` (minute precision, recorded once) for Oslo through
  September 2026: both sides are rounded to the minute, so each event is on
  MET's minute or one off and the test only rules out two or more
  (`TestMoonRiseSetMatchMETNorwayToTheRoundedMinute`; the fixed horizon was
  up to 2 min off). The "local" label time uses the UTC offset Open-Meteo
  supplies (`TZKnown`/`TZOffsetSeconds`; for MET a live air reading's); only
  without it (MET with air off or stale) does it fall back to longitude (15°
  per hour, no tz database), which can differ from civil time at DST/zone
  boundaries. At polar day/night there are no sun times; `Night` then comes
  from the sun's elevation (see below), and the clock's moon icon, which
  also needs a sun crossing that day, stays off (the sun icon). A boundary
  day with one crossing keeps it (#423): the moon shows when that day's
  night comes, and the popup fires for the lone crossing; the knob and
  dashboard still get both times null.
- **Sun times are the location's local day.** Every display caller
  (dashboard `/v1/weather/state`, the knob view, the clock's moon icon)
  reads them from the weather state, which calls
  `localSunTimes`: the
  sunrise/sunset of the local date containing now, with the same offset rule as
  the label (provider offset, else longitude). `sunTimes` itself picks the UTC
  date of its argument, so passing raw `now` gave yesterday's times after local
  midnight east of UTC and tomorrow's in a western evening (#344). The date
  handed to `sunTimes` is local noon shifted by `lon/15` hours (≈ 12:00 UTC on
  the right solar date); neither the UTC instant of local noon nor the local
  calendar date works once the offset and longitude disagree by ~12h (NZDT
  +13h, Kiritimati +14h at −157°, the lon ≤ −172.5° fallback). A sunset after
  local midnight (high latitudes in summer) belongs to yesterday's solar day.
- **Sun popups follow the crossings, not the local day.** The poll takes the
  crossings of the UTC dates either side of now (`sunEvents`, the set `Night`
  uses), so a crossing after local midnight that belongs to the previous
  solar day pops too (Tromsø's first sunset after the midnight sun,
  2026-07-26 00:37 local; every June sunset near midnight in Reykjavík).
  `sunPopupLog` keeps the last popped time per direction and drops a
  crossing at exactly that instant (`sunEvents` is deterministic, so a
  crossing has the same time on every poll), so each pops once; two sunsets
  can share a local day when the sunset moves back before midnight.
  `TestSunPopupsFireOnceForEveryModelCrossingAllYear` runs the window and
  the log minute by minute through 2026 at those five sites with their
  real zones and requires one popup per −0.833° crossing of the model
  elevation and nothing else.
- **Night is the sun schedule's call.** `Night` is `sunNight` over the sun
  events of the neighbouring UTC dates (`sunEventsAround`, the same bracket
  brightness `sunLevel` uses). With an event on both sides, night is "the
  last event was a sunset". When either side is empty (the days next to
  polar day or night, and polar day or night itself) it is the sun's actual
  elevation at `now` (`solarElevation`, the same `sunHeight` as
  `sunTimes`) below the same −0.833° horizon. Neither side alone tells the
  answer: next to polar night a lone sunset means night (McMurdo after its
  last sunset), next to polar day it doesn't (Tromsø's last sunset before
  midnight sun, then 40° sun the next day). A noon-altitude guess was wrong
  for days at McMurdo. `TestSunNightMatchesElevationAllYearAtPolarSites`
  pins every polar site to the elevation all year (more than 1° from the
  horizon); at normal latitudes the bracket is always two-sided, so nothing
  changed there. The knob's `night`, the clock's moon and the brightness
  night agree, including at the sunset second and a post-midnight sunset,
  which stays day until it happens.

### Reminders — Apple Reminders + `POST /v1/reminders/fire`

Reminders are sourced from the user's **Apple Reminders** (macOS), not an
internal list. The **menu app** polls incomplete reminders that have a due
*time* and, when one comes due (within a 90 s grace window, honoring an
optional lead time), POSTs **`POST /v1/reminders/fire`**
`{text, sound, duration, native_icon_id, hold, repeat_sound}` to the server,
which renders the
bell-icon popup (`render.ReminderPopupPayload`) and pushes it to the device. A
reminder chimes once. With the opt-in `repeat_sound` (menu: "Repeat sound
until dismissed", default off) a `hold` alarm with sound loops its chime
(`soundLoop`, a melody with a trailing rest) until dismissed. The server
caps that loop, since an alarm nobody is there to dismiss would ring for
hours: `StartReminderLoopGuard` checks every 15 s and dismisses the alarm by
name when its 15-min hold window runs out, and at quiet-hours start dismisses
it and re-pushes it held but silent (the notice adapter strips sound only at
push time, so the re-push is the stored notice with its sound cleared). A
button acknowledgement just forgets the loop; a failed dismiss is retried on
the next check. An unheld reminder carries `repeat:1`, so a
long text scrolls through fully before it leaves (the meeting and weather
popups do the same). The server keeps that in-memory loop state and the
settings (enable/sound/hold/repeat/lead/duration/icon) as the `reminders`
overlay key (`reminders_config.go`, `GET/PUT /v1/reminders/config`, #726), but
no list and no schedule.
Consequence: reminders fire only while a Mac is awake and Ember is running (the
Linux server can't read Apple Reminders). Every Mac with Reminders access may
fire; each POST carries an `Idempotency-Key` header, `<id>|<due unix s>` where
`<id>` is `calendarItemExternalIdentifier` (the iCloud id, equal on every Mac)
or, when EventKit has none, the per-device `calendarItemIdentifier`. The due
time stays in the key because a recurring reminder keeps one external id. The
server remembers keys for 10 min and answers a repeat with 200 without pushing
again (a failed push releases the key), so the reminder rings once whichever
Macs fire it.
The app waits up to 35s for the answer (`RequestBudget.clockLong`; the server
holds the request while it pushes to the clock, up to 10s) and retries on the
next poll, inside the grace
window, only when the failure proves nothing was sent: connection refused/no
route, 429, or another 4xx. A timeout or 5xx (e.g. 502 after a lost clock ack)
may have rung the clock, so it is not retried; a catch-up fire past the grace
window is final on failure, as it is already outside the window. The server
caps reminder text on a rune boundary, since slicing bytes can split a
multi-byte rune and AWTRIX 0.98 rejects the invalid UTF-8. The loop guard bumps
a loop id per arm so a stale stop cannot clear a newer loop, and a retried
`fire` is checked against its idempotency key before the hold window is armed,
so a duplicate changes nothing. While a held reminder is armed
(`reminderHeldUntil`), a device button press counts as acknowledging it instead
of a Pomodoro action (the firmware dismisses on the middle button), and the
middle press disarms it. The scheduler logs through
`os.Logger` (subsystem `com.ember.Ember`, category `reminders`) with reminder
titles marked `.private`.

Code split: EmberKit's `ReminderScheduler` (`Reminders/`) owns the poll loop
(every 30 s, or sooner to wake 0.25 s past the next fire time), fire
selection, the send and its retry outcome, `lastFireError` and the `upcoming`
list (next five not yet due) that the menu, Dashboard and Calendar pane show.
It reads a `ReminderSource` (`hasAccess`, `dueTimedReminders()`) and takes
injected clocks, so `ReminderSchedulerTests` drive it with a fake source and
`ManualClock`. The app keeps only the platform side in `ReminderWatcher`: the
EventKit adapter (`EventKitReminderSource`, deliberately not MainActor so the
EventKit callback queue doesn't trap), authorization, and the App Nap
assertion. Its prefs come from the server through EmberKit's
`ReminderPrefsSync`: it loads `SettingsModels.reminders` at launch and every
5 min (30 s until loaded), feeds the model's `applied` value to the scheduler,
which holds `nil` and rings nothing until the first load, and keeps the last
loaded value through an outage. The same sync runs the one-time move of the old
UserDefaults `reminderPrefs` blob to the server with
`PUT /v1/reminders/config?if_default=1`: the server checks for the defaults
inside the settings update (`setting.putIf`), so concurrent Macs can't
overwrite each other, and answers 409 with the current values otherwise. Either
answer sets the `reminderPrefsMigratedToServer` marker; a failed request leaves
it off for the next load. That assertion (`.userInitiatedAllowingIdleSystemSleep`)
is held only while the scheduler is armed (`onArmedChange`): a fire in
progress, a `.notDelivered` fire awaiting its retry, or the next fire time
within 5 minutes. The rest of the time Ember may nap, and App Nap can stretch
a 30 s sleep past the 90 s grace. So each poll compares wall-clock and uptime
progress since the last one: uptime pauses during system sleep but not during
App Nap, so if both advanced together (within 5 s) the Mac stayed awake and
the poll fires anything whose fire time fell since the last poll, even past
grace. After a system sleep it doesn't, which keeps "rings only while the Mac
is awake". A poll from a stopped loop can't arm the scheduler (generation
counter).

> **Shared store.** Runtime settings + hidden-apps + Pomodoro stats all live in
> the one SQLite store, opened once at boot by `initPomodoro` (`ensureStore`,
> path `pomodoro.db_path`) whether or not Pomodoro is enabled.

### Now playing — `internal/nowplaying`, `cmd/ember/nowplaying*.go` (#226)

Music state and pictures for the knob's now-playing page (cinder #14).
Design note: Obsidian `Personal/Ember/Specs/2026-10-05-now-playing-design.md`.

- **Model** (`internal/nowplaying.Registry`, pure): one entry per
  `(source, player)`; a report is `{source, player, state
  playing|paused|stopped, title, artist, album, track_id, duration_ms,
  position_ms}` (`source` `^[a-z][a-z0-9_-]{0,31}$`, text ≤200 characters).
  `stopped` deletes the entry, and so does a playing or paused report with
  no title and no track id (#497; it names no track, and the knob would draw
  an empty face). Shown: the most recently *started* playing
  entry, else the most recently paused one. Paused lives 10 min from the
  pause (re-reports don't extend it); playing lives until its extrapolated
  end + 5 min (a Mac that slept never says "stopped"; Music posts no notification on a seek, so a backward seek must not hide a playing track), or 30 min after its
  last report without a duration. An expired entry stays (hidden) until
  its track or state changes, so Plex re-reporting a long-paused session
  doesn't bring it back; entries silent for 1 h are forgotten, and at most
  32 players are kept.
- **App state** (#338): the registry already is now playing's app state.
  `Registry.Current(now)` holds the one expiry rule, and every reader goes
  through it with its own `now`: the knob view's `nowplaying` block,
  `GET /v1/nowplaying/state`, the art route and control's "still shown"
  check. None of them re-derives freshness, so there is no separate state
  type (pinned by `TestNowPlayingConsumersShareTheRegistryExpiry`).
- **Position anchor:** the entry stores `position_ms` at `position_at`. A
  report for the same track and state moves the anchor only when it is more
  than 3 s off the extrapolation (a seek). Plex re-reports `viewOffset`
  every poll; without the anchor the knob view's ETag would change every
  2 s.
- **Public state** (`GET /v1/nowplaying/state`, no token) leaves out
  `player` (a Mac's name); the knob block leaves it out too. `PUT
  /v1/nowplaying/art` requires `track_id`.
- **Pictures:** each entry holds an album and an artist source image (bytes
  + content hash); a new track drops both. `art_version` hashes the two
  hashes, so it moves exactly when a picture does. `backdrop` is the artist
  picture, else the album. Rendering (`nowplaying.Render`): centre-crop
  square, area-average resample (no `x/image` dep), backdrop = 3-pass box
  blur (radius size/40) dimmed to 35 %, then **baseline** JPEG q80 (Go's
  encoder writes SOF0 only, which TJpgDec/`esp_jpeg` on the knob needs; no
  alpha — the knob applies its own circle mask). Sources must be JPEG/PNG,
  ≤2 MB, ≤2048 px a side, checked with `DecodeConfig` and then fully
  decoded once at ingest (`nowplaying.Prepare`), so a corrupt body is a
  400, not a failure on every render. That same decode computes the
  picture's `accent` (#412: a 24-bin hue histogram of a 32×32 downsample
  inside the disc, lightened to 3:1 on black) and keeps it on the image,
  next to its hash. `grid` folds the source into the 32×32 grid one row
  at a time, so ingest is unserialised but costs little beyond the decode:
  ~57 ms and ~6.5 MB from a 2048 px JPEG (the decode alone ~6.3 MB),
  ~26 ms and ~3.2 MB from 1400 px.
  `cover` (#412) is the album picture, else the artist, cropped into a
  466 px circle (r 208) on black with a rim vignette and a bottom scrim
  baked in (`internal/nowplaying/cover.go`), so the knob decodes and blits
  one image. The recipe version (`nowplaying.CoverRecipe`) is in the
  view's `cover_version` (`<art_version>-<recipe>`), the only `v` that
  makes a cover `immutable`, so a recipe bump gives the knob a new URL;
  the ETag and cache key carry it too. ~29 ms from a 1400 px JPEG, decode
  included. Renders are serialised (`renderMu`): a 2048 px source is
  ~50 MB of transient decode buffers, which concurrent cache misses would
  multiply (a 1400 px source costs ~44 ms and ~22 MB); a miss costs
  ~40 ms, so art routes are rate-limited. Served sizes are a fixed set per
  kind (`nowplaying.Sizes`: album 240/120, artist 64/120, backdrop 466,
  cover 466; first = default), so a client can't force a fresh render per
  request. 466 isn't a multiple of the 16 px MCU; TJpgDec handles the
  partial MCU, but the knob's output buffer and stride must allow for it
  (cinder#14). Renders are cached in a RAM LRU of 32 entries / 8 MB keyed
  by `(hash, kind, size)`, plus the recipe for `cover`. Nothing touches
  disk (Deezer's terms forbid storing its images).
- **Plex** (`nowplaying_plex.go`): with `EMBER_PLEX_URL` + `EMBER_PLEX_TOKEN`
  a goroutine polls `/status/sessions` (`Accept: application/json`; 2 s
  while a track plays, else 10 s; `POST /hooks/plex?key=` wakes it).
  Only `type:"track"` sessions, filtered by `EMBER_PLEX_USER` /
  `EMBER_PLEX_PLAYER`; first playing match, else first paused. Artist =
  `originalTitle` (compilations) else `grandparentTitle`. Album art =
  `parentThumb` (else `thumb`), artist = `grandparentThumb`, both via
  `/photo/:/transcode?width=480&height=480&minSize=1&upscale=1&url=…`,
  fetched only when the path changes. The token rides in the
  `X-Plex-Token` header only (`plexConfig` has a redacting `LogValue`);
  a failing poll logs once per distinct error. Redirects are refused (Go
  would carry the custom token header to another host). A failed art
  fetch is retried each poll, logged once per path. A `viewOffset` equal
  to the previous poll's for the same track is stale (Plex updates it only
  on a client timeline report), so the poller reports the extrapolated
  position instead and the anchor holds.
- **Artist pictures** (`nowplaying_deezer.go`): an entry with an artist and
  no artist picture queues a lookup (channel of 4; full = dropped, the next
  report re-queues). Deezer `search/artist?q=` (no key): exact
  case-insensitive name only (no match = no picture, never a stranger's
  photo); `picture_xl` rewritten to 500 px and fetched only from
  `https://*.dzcdn.net`, redirects refused (the picture is served
  publicly, so a spoofed answer must not make the server fetch a LAN URL).
  Hits and misses are remembered by name in RAM (64 names and 4 MB, misses
  1 h, errors 1 min). `SetArtistArt` attaches only while the player still
  plays that artist. **Off by default:** `EMBER_ARTIST_LOOKUP=1` turns it
  on (artist names leave the LAN; logged once at startup).
- **Ember.app pusher** (`macos/Ember/Services/MusicNowPlayingWatcher.swift`):
  see "Menu-bar app".
- **Volume** (#280): a report may carry `volume` 0-100 (the player's own
  level: Music's `sound volume`, never the Mac's output volume). A report
  without it keeps the player's last known level. `GET /v1/nowplaying/state`
  answers it (`null` unknown); the knob block has it only when known.
- **Playback control** (#280, `nowplaying_control.go`,
  `nowplaying_plex_control.go`): `POST /v1/nowplaying/control`
  `{"action":"play|pause|play_pause|next|previous|volume","delta":±1..100,"source","player","track_id"}`
  (delta for volume only; the knob sends `play`/`pause` for the state it
  wants, not the toggle). Master **or** knob device token; a device only
  while its pages have `nowplaying` on (403). It acts on the **shown**
  entry's source: 409 nothing playing, or when the optional
  `source`/`player`/`track_id` name another entry (the knob sends the
  `source` and `track_id` of its view block, so a press never acts on a
  player that started since it drew); 503 no controller for that source
  (Plex not configured, or no Ember.app polling for that Mac); 502 the
  player refused. An optional `Idempotency-Key` (≤128 B) answers a repeat
  with `200 {"status":"duplicate"}` and sends nothing, so a retry can't
  skip twice. Keys are kept 2 min, at most 128 per caller (a device or the
  owner) and 32 callers, so one busy client can't evict another's; a
  long turn may age a caller's own oldest keys out sooner. The key is
  checked first (a duplicate spends no limit); a 409/503/429 releases it
  (nothing was sent), a 502 keeps it: the player may have acted, so the
  retry answers `duplicate` (200) rather than the error. Each caller has
  its own limit (burst 20, 10/s, 429). **Per-IP limiting** on this route
  and on `GET /v1/devices/self/view` charges failed tokens only
  (`rateLimitAuthFailures`: an IP whose bucket is spent on failures gets
  429 before any token check); a valid token is not charged, so a knob
  turning for minutes (a control POST and a view re-arm per step) is
  never limited. The view has a per-device cap instead (burst 30, 10/s).
  - **Plex** runs from the server: `GET /player/playback/{play|pause|skipNext|skipPrevious|setParameters?volume=}`
    `?type=music&commandID=N` with `X-Plex-Target-Client-Identifier` = the
    session's `Player.machineIdentifier` (the PMS relays it to the player).
    A `play_pause` toggle starts from the state Ember commanded in the last
    5 s, else the entry's (the poller may not have seen a pause yet).
    Sessions carry no volume: a step adds to a level read or set in the
    last 5 s (a continuous turn), else reads the player's timeline again
    (`/player/timeline/poll?wait=0`, `Timeline type="music" volume=`), since
    the level may have changed on the player; no known level = 502, never
    a guessed one. The poller re-reads the timeline every 30 s while a
    track plays, so a change on the player shows. A command wakes the
    poller. **Not verified against a real Plexamp** (RUNBOOK "Now playing").
  - **Music** runs on the Mac that reported it: commands wait in a
    per-player queue (`commandQueue`, at most 8, coalescing consecutive
    volume steps, dropped after 5 s so a late "next" never fires) that
    Ember.app long-polls with `GET /v1/nowplaying/commands?player=&wait=≤25`
    (request budget `.clockLong`, 35 s to the first byte, above that wait; master token only, every client token 403; delivery at most once; each command carries `age_ms`,
    its wait here, and the app drops one older than 5 s in all). Every
    answer carries `watched` (always present, #575): true while a knob
    whose current caps list `shown` reported `nowplaying` in the last 50 s
    (DEVICE-PROTOCOL "Shown page"), and true while a knob without `shown`
    made a view request in the last 50 s, since an old knob can't say what
    it shows and the seek watch kept running for it before; false
    otherwise. A dead or spare old knob stops counting once it goes quiet
    (view-request times live in memory, so after a restart an old knob
    counts from its next poll, ≤25 s). No knob paired means false: nothing
    else in the server or the app reads the Music position (the clock
    draws no now playing; the app's knob preview needs a paired knob and
    holds the seek watch itself, see "Apple Music pusher"), so
    only the unauthenticated `GET /v1/nowplaying/state` would see a seek
    late. The app sends
    the value it knows as `?watched=`; a waiting poll then also answers
    once the value differs. The wait subscribes to the `shown` store's
    wake channel before reading (no lost wake-up); a change of reported
    page, a report after a stale one, an old knob's view request after
    50 s of silence and every device-registry change (caps, delete, mint)
    close it, and a stopped-on-wake timer set 1 ms past the latest 50 s
    mark among whatever keeps it true covers staleness, re-armed when a
    heartbeat moved that mark. A wake
    only re-reads, so a spurious one costs a registry scan. Without
    `?watched=` the poll waits for commands only. A Mac
    counts as listening while a poll waits and 30 s after one; otherwise
    the control answers 503. The app sends Apple Events to the running
    Music's **PID** (an event to a process that just quit fails; nothing
    can launch Music), only while Automation is already granted (checked
    without prompting; the prompt is the Settings button's), then re-reads
    and re-reports (Music posts no notification for a volume change). After
    a `next`/`previous` it re-reads every 200 ms, at most 10 times (#526):
    the first read comes before Music has switched, and Music posts no
    `playerInfo` for a skip while paused, so a single read would report the
    old track. It reads the persistent ID before each skip and stops once
    it has seen as many track changes as skips that ran (a batch of two
    `next` waits for the second); a failed read and an empty ID (Music
    briefly stopped mid-switch) don't count, so an unknown starting track
    waits for a change. It then reports the last good read, or nothing if
    Music quits or the listener stops meanwhile. While polling it holds the
    command loop, so commands queued behind it wait up to 2 s; that full
    wait happens only when fewer track changes show than skips ran: none
    show (`next` + `previous` that Music applies in one go, a `previous`
    that restarts the track, a skip in a stopped or one-track queue), or
    Music coalesces two `next` into one change. The registry lives in server
    memory and the Music pusher reports on events (the seek watch only
    re-reports a position jump, and never retries), so the long-poll doubles as
    the reconnect signal: its first successful round after a failure (a
    server restart) re-pushes the Music snapshot
    (`AppleMusicPusher.pushSnapshot`, #497). It reports the outcome of the
    latest report the pusher sent, which can be a newer Music update merged
    into the snapshot. Later rounds retry it only after a transport error or
    a 5xx, and after a 429 not before its `Retry-After`, which a long-poll
    failure doesn't cut short; any other failure (400, 413, an encoding
    error) waits for the next long-poll failure. There is no retry timer. Commands
    age from when the poll returned, so a slow snapshot push can't make a
    queued command look younger than it is. The `playerInfo` userInfo keys
    are undocumented by Apple; the ones read have been stable since iTunes.
- **Not built yet:** iTunes album fallback, Plex websocket.

### Knob controls — `cmd/ember/controls*.go`, `macos/Sources/EmberKit/Controls`, `macos/Ember/Services/*Controls*` (#515)

The knob as a Mac controller (cinder#144, option C: Ember relay). Phase C1
is the Mac side; the knob page (`controls`, C2) is not built in firmware yet.
The server already knows the page id (#747): off by default, and a knob
token is refused with 403 unless its last checkin lists `controls` in
`caps.pages` and its stored settings have the page on. The gate runs before
the dedupe claim and the rate limiter; owner and control tokens skip it.
Ember.app lists the Controls app and Pages row only for a knob whose caps
list the page.

- **Trust boundary.** The server and the wire carry only gestures. A knob
  posts `POST /v1/controls/action` `{"gesture","steps"}`; the server queues
  `{id, gesture, steps, age_ms}` for the **target Mac**; Ember.app on that
  Mac maps the gesture to an action from its own local settings
  (`UserDefaults` `knobControls.mapping`) and runs it. AppleScript text and
  Shortcut names never leave the Mac, so a compromised server, a leaked
  token or a forged queue item can at most fire an action the user set up
  on that Mac.
- **Target.** The settings slice `controls_json` holds only `target`, the
  Mac's name: the now-playing `player` name (`EMBER_SOURCE`, else the
  computer name, ≤64 characters, `ThisMac.name`). PUT is master token only.
  `GET /v1/controls/config` adds `macs`, the Macs seen polling (memory only,
  at most 64; `listening` = a poll waiting or one in the last 30 s).
- **Queue** (`controlQueue`, modelled on the now-playing `commandQueue`):
  per Mac, at most 8, consecutive `turn`s merged (steps summed, clamped to
  ±100; a merge that reaches 0 drops the turn, since the app rejects a
  turn without steps and would lose the whole batch), turns dropped after
  2 s and presses after 5 s so a late action never fires, at-most-once
  delivery. At most 64 Macs are remembered; evicting the oldest idle one
  drops its queue with it. A push for a Mac that is not
  listening is 503 and releases the `Idempotency-Key` (same store rules as
  now-playing control: 2 min, 128 per caller); per-caller limit burst 20,
  10/s. Routes: `/v1/controls/commands` and `PUT /v1/controls/config` are
  master token only (a knob token is 401, any client token 403, `admin`
  included: a client could otherwise drain the target's queue, hold the 8
  long-poll slots or pose as a listening Mac); `GET /v1/controls/config`
  also takes an `admin` client. The action route takes the master or a
  knob device token, never a client token; it keeps the master token like
  `/v1/nowplaying/control`, since a master holder can mint a knob token
  anyway.
- **App, fail closed.** `KnobControlsService` reads the target every 30 s
  (and when the Settings picker saves) and runs the `ControlsListener`
  long-poll only while the target is this Mac and a token is set; no
  target, a failed read, another Mac or no token stops it. The listener
  re-checks cancellation and the target before each action. A target
  picked from another Mac's Settings takes effect when the target Mac next
  refreshes, up to 30 s; until then the action route answers 503. `ControlCommand` decoding is
  strict: an unknown gesture, a missing `id`, bad `steps` or any field
  outside `id, gesture, steps, age_ms` fails the whole answer, which runs
  nothing and is logged. `ControlDispatcher` maps gesture + local mapping
  onto the `ControlsExecutor` protocol (tests use a recording fake); a
  slot set to nothing, or a kind that doesn't fit the gesture (volume on a
  press), runs nothing.
- **Executor** (`MacControlsExecutor`): volume and mute through CoreAudio
  on the default output (`VirtualMainVolume`, `kAudioDevicePropertyMute`;
  a volume step up unmutes; no TCC). Media keys are `NSEvent` systemDefined
  subtype 8 with `NX_KEYTYPE_PLAY/NEXT/PREVIOUS` (16/17/18), key down then
  up, posted as CGEvents. Scroll is `CGEvent(scrollWheelEvent2Source:)` in
  lines; a clockwise turn (positive steps) scrolls down or right, so the
  wheel value is negated. Shortcuts run as `/usr/bin/shortcuts run <name>`
  and AppleScript as `/usr/bin/osascript -` with the script on stdin, both
  launched without waiting so a slow script never holds the queue, through
  `ScriptProcessRunner`: one script process at a time (a press while one
  runs is dropped and logged as a drop, outcome `busy`, not a failure).
  After 10 s the runner sends SIGTERM to the script's process group
  (`kill(-pid, …)`; Foundation's `Process` starts each child as its own
  group leader, which a test pins), so grandchildren left by `do shell
  script` die too. It then probes the group every 50 ms (`kill(-pid, 0)`)
  and sends SIGKILL 1 s later only if the group still exists. The SIGKILL
  follows the last probe immediately, so the residual risk is the
  microseconds between them: the group could empty and a new group leader
  take the same number only after a full pid wrap in that window, which is
  accepted. The 10 s timeout covers only the leader's lifetime. The runner
  tracks each group apart from its leader: when the leader exits but a
  descendant is still in the group, it probes the group every 1 s and
  forgets it only once it is empty, so a pending timeout SIGKILL and the
  quit path still reach it; such a group is never timed out, only quit or
  the group emptying ends it. On a normal quit
  (`applicationWillTerminate` → `KnobControlsService.shutdown()`: the Quit
  item, logout) the listener stops and `ScriptProcessRunner.shutdown()`
  first waits for a launch still inside `Process.run()`, cancels the
  timeout tasks, probes every tracked group and sends SIGTERM right after
  to each one still there (so a lingering group's stale 1 s probe never
  decides it), probes them every 10 ms and, after 500 ms, probes once more
  and sends SIGKILL right away to any group still there. Both signals
  follow a probe within microseconds, the same pid-reuse window as the
  timeout path. Quit blocks for at most about that long, and only
  while a group exists. The runner then refuses new scripts (outcome
  `failed`). In the app `run` and `shutdown` are both on the main actor, so
  they never overlap; the launch wait and the refusal guard the public API
  for callers on other threads. A force quit, a crash or a SIGKILL of
  Ember skips all of this and leaves the group running until it ends.
  Killing `shortcuts run` only stops the CLI: the shortcut itself most
  likely keeps running in the
  Shortcuts engine (not verified on a device), and the runner is free again,
  so a shortcut that runs past 10 s can overlap the next one (at most one
  new shortcut per ~11 s). That is accepted: the `shortcuts` CLI has no stop
  command, and holding the runner longer would block every script action
  behind one slow shortcut. The stdin pipe is set to `F_SETNOSIGPIPE` and
  written with the throwing `write(contentsOf:)` off the main thread, so a
  script that exits before reading can't raise SIGPIPE in Ember.
  The executor's effects are synchronous and `@MainActor`, like the
  listener, so `ControlDispatcher` checks the target and then runs the
  effect with no suspension in between. The only suspension is the
  Accessibility check (`canPostEvents`), after which the dispatcher checks
  again for cancellation, the target and the slot's mapping (a slot edited
  meanwhile is dropped as stale). CoreAudio volume calls and `Process.run`
  therefore run on the main thread; a script is spawned, never awaited.
  A CoreAudio call can stall for hundreds of ms while coreaudiod restarts or
  the output switches to Bluetooth or AirPlay, which would freeze the menu
  bar; not seen so far (#525). If it shows, keep the target and mapping
  checks on main and move only the CoreAudio calls to a private serial
  queue.
  Posting events (media keys, scroll) needs Accessibility: the executor only
  checks `CGPreflightPostEventAccess` and skips (logged once); the prompt
  (`CGRequestPostEventAccess`) is the Settings › Knob › Pages › Controls
  button, shown only when this Mac is the target and a mapped action needs
  it.
- **Latency** (C1, loopback scratch server, n = 120 each): POST →
  executor call p50 0.5 ms / p95 0.8 ms from a keep-alive client, p50
  8.6 ms / p95 11.5 ms with a fresh `curl` per action (process start
  included). The LAN hop to the Unraid server is not measured.

### Runtime settings overlay (`settings_overlay.go`)

Every menu-editable config slice — pomodoro (`settings_json`), weather
(`weather_json`), meetings (`meetings_json`), usage (`usage_json`), display
(`display_json`), quiet hours (`quiet_json`) — is one registration with the
settings overlay: a `settingSpec[D]` giving its store key, `view` (effective
`Config` → wire DTO), `apply` (DTO → config copy, validation included) and an
optional `after` hook (engine update, re-render nudge, icon provisioning).
The overlay owns the rest, identically for all of them:

- **Merge.** A PUT body (or stored blob) must be a JSON object; its top-level
  keys are laid over the JSON of the current effective value and decoded into
  a fresh DTO. Omitted key = unchanged; a present key replaces the whole field
  (objects/maps included).
- **Validate + swap + persist atomically** under `cfgMu` (`tryUpdateConfig`):
  an invalid result is a 400 and changes nothing; the persisted blob is the
  normalised view, never the raw body, and persisting inside the lock stops a
  racing PUT from overwriting the store with a stale merge. A bare
  load-copy-store of the config loses updates when two appliers race, which is
  why all writers use `updateConfig`/`tryUpdateConfig`. A corrupt or invalid
  stored blob is logged and ignored, leaving the file baseline.
- **Re-apply** (`settings.reapply()`): at startup right after the store opens,
  and after `/admin/reload` swaps in the new file baseline, every stored
  override is laid over the baseline through the same merge — so a blob written
  before a field existed keeps that field's current value.

**The clock URL** (`clock_url.go`, #175) is a registration too
(`device_base_url`, DTO `{"base_url"}`). Its `encode`/`decode` pair keeps the
raw-URL string it was stored as before the overlay, so old stores need no
migration. It is the one setting with a tier the overlay doesn't own. The URL
lives in three tiers, all in the one `Config` value:

- `awtrix.http_base_url`: the file baseline, never overwritten at runtime.
- The menu override: this registration.
- The discovery swap (`clockDiscovered`): written only by `rediscoverClock`,
  in memory, and only if the effective URL hasn't changed since it probed.

`Config.clockURL()` is the only place that turns the tiers into the
effective URL and its `source`, in the order discovered > store > config >
none. In words: effective = menu override, else `config.json` baseline; if that fails its probes, an in-memory mDNS swap replaces it (the pin included) until a PUT naming `base_url` or a reload that changes the file URL (the
store row is never touched). A swap only follows failed probes, so the pin
is not unbeatable: it wins only while it answers.
Everything that dials or reports the clock asks it: clock access, doctor, the
`/v1/device/config` and `/discover` bodies, clock health, and the boot-ping and
button callback hosts. `TestHTTPBaseURLOnlyReadAsBaseline` stops anything else
from reading the baseline field.

A PUT naming `base_url` clears the swap in the same critical section
(`putWith`), so the menu's pick always re-pins. Reapply never does this. On
`/admin/reload`, `carryClockURL` keeps the override, and keeps a swap only
while the file URL is unchanged. A changed file URL is the operator pinning a
clock, but a store override still beats it.

The tiers sit in one `Config` value so a reader gets URL and source from one
load. `HTTPBaseURL` is written only by config load and `/admin/reload`;
`clockOverride` is stored in the settings KV under `deviceBaseURLKey` as a raw
URL string; `clockDiscovered` is in-memory, never persisted and never written
over the override. The unexported runtime tiers are invisible to `config.json`
and `diffConfig`. `swapDiscoveredClock` swaps only if the effective URL is still
the one that failed, so a menu PUT or reload that landed while discovery probed
wins; if discovery finds the pinned clock again it is reported as the store's
tier, not a swap. `sameDeviceURL` normalises because discovery builds
`http://<ip>:<port>` while `config.json` usually omits the default port. A swap
to a different clock clears the cached capabilities (they described the previous
clock and the audio gate would refuse on their word), and a PUT naming
`base_url` clears a swap even when the URL equals the override discovery swapped
away from. A swap found by the watch, and a PUT that changes the effective URL,
both go through `clockMoved`. The PUT compares the URL before and after its
write with `sameDeviceURL` inside the same `cfgMu` hold as the write, so a swap
that lands while the PUT runs cannot hide the move (#390). `clockMoved` is a
republish (`clock_rediscovered` / `clock_url_changed`) plus the per-clock
one-shots that otherwise run only at boot or reload: icon provisioning and the
boot-ping script install (`ensureBootPingScript`), so a clock found or pinned
after boot gets them too. The PUT handler decides this, not a settings `after`
hook, because `reapplySettings` replays the stored override at boot before
rediscovery has judged it (#382).
They start in the background after `rediscoverClock` returns, so neither
`deviceRediscoverMu` nor the `clock_rediscovered` republish waits on them.
`rediscoverClock` is single-flighted by `deviceRediscoverMu` so the
boot check and the periodic probe never browse mDNS concurrently.

Hidden apps (`display_hidden_apps`) are a set toggle, not a config overlay.

**Clock presentation keys (#232).** Four slices also carry keys that are the
TC001's presentation, not source settings: `weather_json` (tile toggles,
popups, native icons, icon ids), `meetings_json` (`tile_lead_minutes`,
`popup_lead_minutes`), `usage_json` (`usage_widget`, `usage_per_model`) and
`settings_json` (`focus_color`, `break_color`). Their `settingSpec` lists them
in `presentation`. Once the clock config is migrated ("Clock record and config
façade" below), those keys belong to the clock config row
(`clock_config_json`): the slice's stored blob drops them, a stored blob that
still has them (written by an older server) has them ignored on reapply, and a
PUT that changes them stores the clock config row and the slice in one
`PutSettings` transaction (the slice only if its source part changed). A
failed store there answers 500 with memory unchanged, like the façade PUT, so
an identical retry stores it (a slice's own write, before the migration or
with no presentation change, still logs and answers 200 as before). A PUT
or GET of the old endpoint still takes and returns the merged legacy shape.
The clock config row is registered first, so reapply loads it before the
slices.

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
documented limitation. A `RECURRENCE-ID` override is the event's latest truth: it
is applied even when its original instant was EXDATE'd or lies outside the poll
window. ICS text unescaping maps `\n` to a single space (one-line clock display)
and processes escapes left to right in one pass, so `\\n` yields a backslash
and the letter n. The binary imports `time/tzdata` so the distroless image
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
- **Fetch ordering and popups.** The popup check runs every tick, after the
  fetch on due ticks, so a cancelled or moved meeting in the just-arriving ICS
  cannot fire from the previous snapshot. Popups are marked before firing under
  the store lock to avoid doubles. The check scans up to 10 future occurrences,
  not just the next, so back-to-back meetings or overlapping lead windows are
  each notified. An empty successful fetch replaces `upcoming` wholesale, so a
  genuinely empty calendar clears the store.
- **Credentials in logs.** ICS URLs are logged by index only, and never log a
  `*url.Error` (it embeds the URL). A literal comma in a URL is unsupported in
  `EMBER_MEETINGS_ICS_URLS`.

**Coordinator (`meetTile`).** One entry in the tile module (see "Rotating
tiles" under the server), which owns the clear/dedupe/re-push state machine
for every rotating tile. `ember-meet` joins the rotation when the next
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
via the settings overlay (see "Runtime settings overlay").
API: `GET/PUT /v1/meetings/config` (bearer auth).

**Read endpoints (no auth).** `GET /v1/meetings/preview` renders the `ember-meet`
tile (`previewTiles`, the pushed tile's own view) into a 32×8 frame grid, using the live next occurrence when present and a
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

**Firmware capabilities** (`GET /api/v1/capabilities`) are fetched at startup,
after a rediscovery swap, after a detected clock reboot (a firmware update
reboots the clock at the same URL), on a device-watch tick that finds the
clock reachable with no capabilities cached (cold start, a failed fetch, a
clock-config PUT, which empties the cache), and once when the clock answers
422 to a notice with a sound (whatever the `field`). A fetch that keeps
failing (a clock that answers `/api/v1/device` but errors on capabilities is
retried every watch tick) logs `Warn` once per outage and URL, then `Debug`
until a fetch is cached or the cache is invalidated (a URL swap, a
clock-config PUT) or the clock is disabled. A late answer that a newer fetch
or a clock change has superseded (two refreshes of the same clock racing, or a
switch to another clock), success or failure, changes neither the cache nor
the outage. Capabilities are cached in-process, tagged with the clock URL and
a generation so a late answer from a clock the menu has switched away from is
dropped, and served at `GET /v1/device/capabilities` (falling back to a live
proxy fetch when the cache is cold) — the firmware's supported
effect/transition/overlay/palette name lists plus its
`audio{buzzer,track,mp3,radio}` outputs (NG 1.1.0 replaced the old top-level
`radio` flag; NG 1.2 renamed `buzzer` to `rtttl`), relayed as the device's own
document so no key is dropped. Settings › Clock and Sounds use them to
populate its transition picker and to gate the buzzer-volume row, instead of
guessing at a static enum. `/admin/doctor` reports the cached counts (and
whether a buzzer is present) as its `capabilities` check.

**App ordering** (`GET/PUT /v1/device/apps`) proxies `GET /api/v1/apps` /
`PUT /api/v1/apps/order` — ordering plus enable/disable of the device's own
apps, replacing the AWTRIX3 settings keys `TIM`/`DAT`/`TEMP`/`HUM`/`BAT` that
NG has no equivalent for. `disabled` is the complete list of switched-off
apps: NG switches on every app it doesn't name, so a body naming only the app
just turned off (or `[]` with a reorder) turns every other off app back on
(#413). NG also refuses a missing or `null` `disabled`, so Ember answers 400
for one itself (and for a missing or `null` `order`), on this route and on the façade's `rotation`, rather than
forwarding `null` or guessing `[]`, which would switch every off app on
(#449). After a
successful order write the PUT reads the list back (2 s budget) to update the
clock record's version, and both routes run one at a time with the façade;
the same holds for `PUT /v1/device/apps/{name}/enabled`, which switches one
app and leaves the rest as they are (#447). The name must be NG's
`[A-Za-z0-9_-]{1,32}`, not `active`/`next`/`previous`/`order`, and not a
script module (checked against the cached list, and the fresh one on the
fallback): 400 otherwise. NG has no capability flag for its per-app
`PUT /api/v1/apps/{name}/enabled` (body `true` or `false`, added in NG
1.2), so the server probes: it tries the per-app call first, and a 404 or
405 means this clock lacks it. The server then remembers "no per-app
route" for that clock identity (URL, uid and firmware as last seen by the
capability refresh or the `/v1/clock/health` probe, the uid only from an
awtrix-ng board), so a URL swap or a firmware update seen by either tries
the route again. So does a reboot (uptime lower than last seen) or an hour
since the mark, so a one-off 404 (another host briefly on the address, a
web server not up yet) doesn't keep the clock on the fallback. Without it,
the server reads `GET /api/v1/apps` and writes the full order with only that
app changed, using the rules of the app's `NativeAppsPlan` (modules left
out, a missing `enabled` read as on, an unlisted name switched off by
naming it in `disabled`). The read is fresh, under the app-order lock, so
it is never older than the write. A 507 (`applied, not saved yet`) is
relayed as 507, but the change is live, so the list is read back as after
a success. A clock 404 on the fallback becomes 502, so a 404 from this
route only ever means a server without it. Other refusals (400
`invalidName`, 422) are relayed and not retried;
see "One operation at a time" and "Rotation cache" under the clock config
façade.
The ambient weather overlay is a separate concern, `PATCH /api/v1/display`
via `GET/PUT /v1/device/display` — not part of app ordering. Display power is
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
some networks) for when multicast doesn't make it through. In a UDP reply the
packet's source IP is the device address; the hostname it reports may not
resolve (NG lets the device be renamed without its mDNS record following), so it
is kept only as the display host. A resolved host is
fingerprinted via `GET /api/v1/device`: it counts as the clock only when it
reports both a non-empty `uid` **and** `boardType == "awtrixng"` — the AWTRIX3
`/api/stats` fingerprint doesn't exist on NG. The effective clock URL is the
**menu override, else `config.json` baseline; if that fails its probes, an in-memory mDNS swap replaces it (the pin included) until a PUT naming `base_url` or a reload that changes the file URL** (never written
to the store or `config.json`). The pin is
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
during a long gap); `rebootUptimeSlack` absorbs whole-second truncation on the
device plus up to one probe timeout of latency on each of two readings, and a
zero previous probe is never a reboot (nothing has been pushed yet). A missed probe on its own is **not** a reboot — the
server→clock link drops a large share of requests, and the old "unreachable,
then answering" rule republished (and re-switched the screen) every few ticks.
For the same reason the reachability check retries once before it falls back
to an mDNS browse, and a browse that finds the clock at the URL already in use
is not a swap. A real swap to a new URL does republish. `RepublishAll`
coalesces calls less than 10s apart into one immediate plus one deferred
republish (the gate has a leading and a trailing edge: deferred, not dropped, so
a clock that reboots twice in quick succession gets its second boot state; 10 s
is shorter than any real reboot cycle, as the boot ping lands about 12 s after
reboot), and both `/hooks/awtrix/*` routes sit behind the per-IP rate
limiter (the button hook also caps its body at 1 KB), so an unauthenticated
flood can't turn into a republish storm. Swaps are **in-memory
only** — `config.json` and the writable store are never rewritten, so a
config/store edit still takes effect the next time its source URL goes
unreachable. The whole probe loop is gated by `awtrix.auto_rediscover` (config,
default on; `/admin/doctor`'s `clock` check reports the source, reachability,
and last re-discovery time/result; `"disabled"` under `EMBER_CLOCK=off`, which runs the server with no clock I/O at all, see RUNBOOK). The server also advertises
itself as `_ember._tcp` so the menu app can discover it (gated by
`EMBER_MDNS_ADVERTISE`); the app browses (`ServerDiscovery`) only while
Settings › Connection is open. Both directions require host/macvlan networking.

The app can find the clock itself (#57), for a server that can't see
multicast. Settings › Clock's Discover sheet runs the server's
`/v1/device/discover` (503 `clock_disabled` under `EMBER_CLOCK=off`) and an app-side browse (`ClockDiscovery`, EmberKit) side
by side and lists both, one row per `uid`, labelled by who found it (the
server's address wins a tie: it has shown it can reach it). The app-side rules
mirror `internal/discovery`: browse `_awtrixng._tcp` (listed in
`NSBonjourServices`), resolve pinned to IPv4, base URL `http://<ipv4>:<port>`
(0 → 80), fingerprint `GET /api/v1/device` with a 1.5 s timeout requiring 2xx,
a non-empty `uid` and `boardType == "awtrixng"`. Two deliberate differences.
There is no `FIND_AWTRIXNG` broadcast fallback: the server's exists for a host
where multicast doesn't get through, while the Mac shares the clock's LAN with
working mDNS, and the fixed reply port (4211) would clash with a server on the
same Mac. And there is no IPv6 fallback. A scan is bounded: a 5 s browse (the
server's is 3 s; resolving the clock over its lossy Wi-Fi can take retries,
so a waiting resolve is kept until the window ends) plus the server's 2 s
probe grace. It is owned by the sheet's `.task`: dismissing the sheet or
leaving the pane cancels it, and Mac sleep stops it (#61). Every dropped
resolve or probe is logged (`com.ember.Ember`, category `discovery`). The
"Find Clock from This Mac" prompt needs a fresh health read with no clock, or
a failed probe *and* a failed last push, so one lost request doesn't raise it. Picking a clock is the usual
`PUT /v1/device/config`, so the app still never writes to the device.

Settings › Clock (and the clock half of Sounds & Alerts) manages the clock's
*own* firmware settings — but **the server stays the only writer to the
device**: the app sends only the keys that changed to `/v1/device/settings`
(bearer auth), and the server whitelists + range-validates each NG settings key
(`device_settings.go`'s `deviceSettingRules`) before forwarding to the clock's
unauthenticated `PATCH /api/v1/settings`. `autoTransition`/`blockNavigation`
(NG's replacements for AWTRIX3's `ATRANS`/`BLOCKN`) are overridden by the
Pomodoro takeover during a focus block; the menu then reads and writes the
user's saved values, which the restore applies (see Pomodoro above). Time/date are
discrete typed fields on NG (`timeMode`, `dateOrder`, `dateSeparator`, …) with
no format strings to validate, unlike AWTRIX3's `TFORMAT`/`DFORMAT` strftime
strings. `buttonCallback` is set separately via `PUT /v1/device/buttons`
(below) because it lives on `/api/v1/system`, not `/api/v1/settings`.
The sound keys follow the clock's audio API: NG 1.0/1.1 takes `soundEnabled`
(device mute) and `buzzerVolume` (0–100), which replaced the pre-1.1.0
`volume` (0–30); NG 1.2 takes the mixer, `volume`, `appVolume` and
`alertVolume` (0–100), and refuses the old pair. GET passes through whichever
of these keys the clock returns, with no capabilities lookup. PUT picks the
whitelist from the sound keys the clock reported on the last GET for the same
URL (`appVolume`/`alertVolume` mean 1.2; a bare `volume` decides nothing),
then from cached capabilities, and answers 400 naming the replacement keys
when the menu sends the other set; with neither known it accepts both and the
clock decides. `radioVolume` is not whitelisted (the TC001 has no radio).
`smoothScroll` (an AWTRIX3 key NG never had; `scroll.mode` replaces it) is
gone — NG rejects unknown keys with 422, so one stale key fails the whole
PATCH. The per-app colours (`timeColor`, `dateColor`, `temperatureColor`,
`humidityColor`, `batteryColor`) accept `null`, which returns the app to
inheriting `textColor` (Settings' "Same as text"). A 0.27.x server filters its
GET to its older whitelist, so the app treats missing `soundEnabled`/
`buzzerVolume` (and missing mixer keys) as "no NG 1.1 support" and hides
mute, volume and "Same as text" there. The app picks its Sounds controls from
the keys GET returns: `appVolume` or `alertVolume` shows Master/Apps/Alerts
sliders (hidden on a clock with no output), the old pair shows the Sound
toggle and buzzer volume, and a bare `volume` shows neither; a 404 on the audio routes hides the test chime and melody list.

When the clock refuses a proxied request, the `/v1/device/*` handlers
(sensors and buttons included, since #146) relay its NG error envelope
through `writeClockError` instead of a bare 502: the menu gets
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
so it plays during quiet hours; the clock's own mute (`soundEnabled`, or
`volume`/`alertVolume` 0 on NG 1.2) still applies. On NG 1.2 a stored melody
plays as `{"file":"<name>"}` instead of `{"melody":"<name>"}`. These handlers (and display power) go through `internal/awtrix`
client methods rather than the raw proxy; a client `*APIError` is relayed by
the same envelope mapping.

The settings whitelist (`deviceSettingRules`) rejects unknown keys so the proxy
cannot poke arbitrary firmware settings. `transitionEffect` is bounded to an
identifier shape only (the device 422s unknown names); `appDurationMs` is
milliseconds on NG (bounded 1 s-1 h); `scroll.speed` is a percentage of the
base rate; `weekdayBar` whitelists only the subkeys the Device tab needs; the
TC001 volume rules cover only the piezo. Button status is best-effort: an
unreachable clock still reports press tracking, just without
`configured`/`configured_callback`. `handleAwtrixButton` records receipt before
any early return, since a POST arriving at all proves the clock's
`buttonCallback` is configured and reaching the server (surfaced in
doctor/health output). While a `hold:true` reminder is on the clock, a button
edge acknowledges it rather than acting on the Pomodoro: NG's callback doesn't
consume the press, so the firmware already dismisses the popup and the server's
`DELETE` usually 404s, which is not a failure. The `DELETE` stays as
belt-and-braces (a stuck hold alarm is the worse failure) and dismisses by name
so a foreign popup is never the one cleared. The capabilities refresh empties
the cache on failure and the endpoint then falls back to a live fetch, which
also warms the cache.

**Boot-ping hook and script.** `POST /hooks/awtrix/boot` is unauthenticated for
the reason the button hook is: a script on the clock has nowhere to keep a
bearer token that the device's own `GET /api/v1/apps/script/{name}` wouldn't
hand to any LAN client. The blast radius is a republish of state the server
already owns, bounded twice (per-IP rate limiter; `RepublishAll` coalescing
under `republishMinGap`); the body is never read and the reply is 204 at once,
the republish running on the coordinator goroutine. `ensureBootPingScript` is
best-effort (an unreachable clock at startup is normal; the feature only speeds
up the watch loop), idempotent (re-PUTting restarts the app on the device and
would re-ping, so a current script is left alone) and serialised by
`bootPingMu` so a PUT cannot race a DELETE. A Berry script that fails to
compile still installs: the device answers 200 with the compiler message in the
reply's `error` and shows `ERR:<name>` on the panel, so the body, not the
status, says whether install worked. Deleting an already-absent script is
success. `buildBootCallbackURL` mirrors `buildCallbackURL` deliberately (two
hooks, two paths); both use `outboundIP` (a UDP dial that sends nothing, just
resolves the route to the clock host, falling back to a public address). The
script itself is fire-and-forget: a 404 from an Ember that predates the hook
still proves the server heard it, so only a transport failure (status 0: no
Wi-Fi yet, server down) retries; it waits about 5 s for Wi-Fi/DHCP before the
first POST and about 20 s between retries. Keep it under 8 KB: NG ≥1.1.1 dropped
its fixed 8 KB cap, but compile still draws on the shared ~96 KB Berry heap.
The `url` baked into the script header must be a bare http(s) URL (no quote or
newline), since it is interpolated into a double-quoted header field.

Sensor calibration (`GET/PUT /v1/device/sensors`) targets `tempOffset`/
`humOffset` on `/api/v1/system` — NG has no dedicated settings-API key for
them, and the old AWTRIX3 `dev.json`-on-LittleFS contract is gone entirely. The
PUT read-merges the existing `/api/v1/system` object (preserving unrelated
keys — notably `buttonCallback`, which Pomodoro buttons depend on, and the
Wi-Fi credentials the device needs to boot) and writes it back with a plain
`PUT`; NG applies system changes **live, no reboot**, unlike `dev.json`, which
only took effect at boot. The read-merge-PUT is `clockAccess.updateSystem`,
which serialises it with the buttons PUT so neither loses the other's write.
The Ulanzi firmware default is `tempOffset:-9`
(self-heating compensation); an explicit `null` in a sensors PUT resets to that
default (or `0` for humidity), so the menu treats −9/0 — not 0/0 — as the
baseline.

### Dashboard read API — `cmd/ember/dashboard_http.go`, `clock_health_http.go` (#110)

Open (no token) reads for the native macOS dashboard, alongside the existing
`GET /v1/pomodoro/{stats,heatmap,workhours}`:

- **`GET /v1/usage`** — the latest `UsageStore` snapshot per tool (5h/7d windows,
  `models` keyed by model name, `stale` past `usageStaleTTL`; since #535 the
  tool's representative account, "Usage per account"). Before this the
  snapshot was write-only; `/state` leaked just the 5h percent. In-memory and
  filled only by the producers' daemons (`ember-claude-producer run` relays the
  statusline windows every heartbeat and polls the OAuth endpoint), so it is
  empty after a server restart until one posts; the app's Usage card and menu
  then fall back to the sessions' `rate_window_pct` (`MenuRows.sessionFiveHour`:
  known reset, dropped once it passes).
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
  stored while it is on, but already-stored rows are still summarised when it is off. Rows are throttled to one per session per 2 min,
  **except a transition into waiting**, which is written once at least 10 s
  have passed since the session's last row, so a short prompt isn't lost.
- **`GET /v1/weather/state`** — the poller's cached observation (condition,
  the provider's raw `condition_code`, `temp_c`, hourly points stamped with the
  provider's own series start), air quality, the user's `location_name` label
  and the location's local-day sunrise/sunset **rounded to 5 min** (to the second they'd pin the
  coordinates). The rounding covers only the times sent: `sun.night` here, and
  `night` on `/v1/display/brightness`, flip at the exact crossings, so a LAN
  client polling them learns the exact sunrise and sunset; with `source: sun`
  its `level` ramps over `twilight_minutes`, so one read in the ramp gives the
  time since sunset or until sunrise. `faces` (#610) is
  `newKnobWeatherDetail` re-shaped for the dashboard (RFC 3339 times, `null`
  for absent): the knob's face inputs, so the app previews the Rain, Rain
  forecast, Wind, Next days, Sun and Air faces from the numbers the knob
  draws. Its twilight carries the knob view's 5-min rounding; the moon's
  rise and set (unrounded on the knob view) are left out, and `moon.up` is
  evaluated at now rounded to that 5-min grid (the knob view's is exact),
  so polling it can't time a moonrise or moonset closer than that. No
  provider call;
  the coordinates are never echoed.
- **`GET /v1/clock/health`** (per-IP rate-limited; under `EMBER_CLOCK=off` it adds `"disabled": true` (omitted otherwise), `device` is `null` and no publishes are counted) — publish counts for the last
  24 h (hourly buckets fed by `recordPublish`) and since start, the last publish,
  plus the clock's `currentApp` (not shown by the app: 30 s behind a rotation
  that changes every few seconds), `wifiRssi`, heap, uptime, `wifi.connects`,
  `matrixPower`, battery and sensors from `GET /api/v1/device`, **cached 30 s**
  and probed detached from the caller's cancellation, so polling can't add
  traffic on the clock's lossy Wi-Fi and a disconnecting viewer can't cache
  "unreachable". `latest_firmware`/`update_available` come from GitHub's
  awtrix-ng latest-release API: the server's only call to the internet for this.
  A background goroutine does the lookup (single in-flight), so the endpoint
  serves the cached answer and never waits. It runs at most every 6 h, 30 min
  after a failure (logged at Warn), fails soft to `null`, and is off with
  `EMBER_FIRMWARE_CHECK=0`.
  The clock's IP, SSID host, UID, hostname and button presses are not served:
  the wire struct decodes only a subset of `GET /api/v1/device`. The device
  probe uses a short timeout (lossy Wi-Fi; dashboards prefer "unreachable" to
  hanging); a failed firmware lookup keeps the previous answer.

These reads are unauthenticated, so none may carry a secret: no clock or ICS
URL, Wi-Fi SSID/IP, device UID, button presses or coordinates. Internal errors
answer a generic 500 body (cause logged only). `?days` is capped (1..90) because
each request walks every heartbeat in the window on the store's single
connection. Active-span reconstruction: heartbeats of one session no more than
`SpanGapSec` apart form a span, a span ends at its last heartbeat and a lone
heartbeat is zero-width, so short bursts under-count by up to one recording
interval (2 min); spans are cut at the day start, and `recording` is false while
`work_hours_include_activity` is off, which stops new rows only: stored rows stay
readable and still count, so windows read zero only once they hold no stored rows. Source colours
are remembered in memory per source (`sourceColorMemo`) so charts can colour
sources with no live session. Row writes use `activityThrottle` (2 min) with the
10 s `activityWaitFloor` for a transition into waiting, as above; the stats
cache is invalidated on phase rollover and otherwise expires by `statsCacheTTL`
(1 min). Sun times are rounded to 5 min because to the second they'd pin the
coordinates to a few hundred metres; the location is the user-typed label only.
That covers only the times sent: `sun.night` on `/v1/weather/state` and
`night` on `/v1/display/brightness` flip at the exact crossings, and with
`source: sun` its `level` ramps through twilight, so polling them still reveals
the exact sunrise and sunset on the LAN.

- **`GET /v1/display/brightness`** (per-IP rate-limited) —
  `{"level":0-255,"source":"lux|sun|default","night":bool}`: one brightness for
  displays with no light sensor (the cinder knob), so they hold no Home
  Assistant token. `lux`: the clock's `lightLevel` from the same 30 s device
  probe `/v1/clock/health` uses (no extra poller), log-mapped between
  `lux_dark` and `lux_bright` onto `floor..ceiling`, smoothed by an EMA and held
  while the target stays within `hysteresis` of it (a change of `hysteresis` or
  more moves it; floor and ceiling always snap; the held level is clamped to the
  current floor and ceiling). The newest sample is kept across failed probes and
  fed to the EMA once, only if newer than the last; a gap over `stale_seconds`
  reseeds it. `stale_seconds` must be at least two probe intervals (60) so one
  lost probe never flips the source. Once the sample is older than
  `stale_seconds`, or the clock never reported `lightLevel`, the answer is
  `sun`: `day_level` by day, ramping to `night_level` over `twilight_minutes`
  after sunset and back up ending at sunrise (`sunTimes` for the weather
  lat/lon, neighbouring UTC dates included; when a side has no sunrise or
  sunset it follows the sun's elevation, so polar winter reads night). That
  fallback resets the filter. No weather location either: `default` (`day_level`). `night` is
  the schedule's own call (after sunset, before sunrise) whenever a location is
  set, in every source; the weather state's `Night` uses the same bracket
  (`sunNight`), so they agree.
  `lightLevel` reads 0 in a dark room (observed overnight, `ldrRaw` 0); the
  defaults assume lux and want a daytime check. Policy is pure
  (`decideBrightness` in `brightness.go`); the clock's own brightness is
  untouched. The filter advances only on a server tick (`StartBrightness`, at
  boot and every 60 s, through the same probe cache); a GET reads it
  (`brightnessAt`, re-held against the current config) and never probes the
  clock or moves the EMA, so the answer does not depend on how many clients
  poll. A read allows the sample `stale_seconds` + one tick + one probe
  timeout of age (the tick's sample may come from a cache up to 30 s old), so
  `stale_seconds` near its 60 s minimum does not flap to `sun` between ticks. The probe cache's mutex is never held across the clock request: one
  caller probes, others get the previous result (or wait for the first one). Knobs (defaults): `floor` 10, `ceiling` 255, `night_level` 20,
  `day_level` 255, `lux_dark` 1, `lux_bright` 200, `ema_alpha` 0.3, `hysteresis`
  8, `stale_seconds` 120, `twilight_minutes` 45; config.json `brightness`,
  editable via `GET/PUT /v1/brightness/config` (merge semantics, 400 on an
  invalid merge, e.g. `night_level` below `floor`; 0 is not a valid value for
  any knob).

Wire conventions (for Swift's `JSONDecoder` `.iso8601` and Swift Charts):
RFC 3339 timestamps with **whole seconds** (`.iso8601` rejects fractions),
`null` instead of zero sentinels (work hours' empty days emit
`work_start`/`work_end: null`, not `0001-01-01`), series as arrays of points,
and the unit in every key (`_sec`, `_percent`, `_c`, `_dbm`, `_bytes`,
`_ugm3`). Storage errors are logged, not returned. Handlers wrap `build*`
methods that take `now`; `TestDashboardGolden` renders them at a fixed instant
into `cmd/ember/testdata/dashboard/*.json` (`go test ./cmd/ember -run
TestDashboardGolden -update` to regenerate), and EmberKit's decode tests read
those same files. EmberKit's models are in `Sources/EmberKit/Models/`; each
feed's route is in `LiveModel`'s fetch switch.

**The Dashboard window (#111)** is a card grid (`macos/Ember/Dashboard/`):
Clock (live mirror + next/previous/dismiss/power), Focus, Usage, Upcoming,
Day (Today, This week, the 28-day Each day strip, Stop time, Weeks; from
`/v1/day/{state,history,weeks}`, hidden on a server without them, and
`day.enabled` off leaves only Today with a link to Settings › Day Clock),
Agents, Last 7 days, 12 weeks, When you focus (weekday × hour heatmap +
12-week strip), Agent time, Weather. The strip's axis is 06:00–04:00 (hours
6…28, wall-clock, DST-safe); days and weeks, never streaks. Debug-only
`EMBER_DASHBOARD_SNAPSHOTS=<dir>` renders the Day cards from fixtures through
an off-screen window (build with a scratch bundle id). Device hardware health is not
here: it lives in Settings › Devices › {Clock, Knob} › Hardware (see
"Hardware pages", #246). 3/2/1 columns
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

Dashboard rendering constraints:
- **Feed states.** `FeedStateView` applies one rule to every card. Never loaded
  shows the redacted placeholder (else a spinner); loaded shows content or the
  empty message. A 404/405 (feature off or old server) shows the off message
  with a Settings button even if an old value exists. Offline, server error or
  429 with a value shows the content plus a stale chip (none for 429). With no
  value: offline reads "Server unreachable", a timeout "Server not responding",
  Local Network denied "Local Network access is off", a server error "Server
  error" with its message, 429 a spinner (retry scheduled), and 401 "Needs
  token" with a Connection button.
- **Clock card.** It has no "Showing <app>" label (the Clock › Hardware page's
  "Current app" fact is a probe snapshot, up to 30 s old): the only source is clock health's `current_app` (cached up to 30 s on
  the server, polled every 15 s) while the clock rotates apps every few seconds
  and the mirror updates each second, so it would be wrong most of the time.
  NG's screen endpoint carries no app name, and a per-second extra request is
  too much for the lossy link.
- **Visibility.** SwiftUI's `scenePhase` stays `.active` for a minimised or fully
  covered window on macOS, so the Dashboard tracks `NSWindow.occlusionState`; its
  extra feeds (notably the 1 s clock mirror) are held only while the window is
  visible.
- **Charts.** The agent-time chart uses day keys as categories (a date axis
  centred labels between ticks and drew the last day's label off-centre); the
  focus bar chart and heatmap use numeric band/unit-square rows so bars and gaps
  have real heights at any card size; a day-based x domain extends one day past
  the last so centred labels aren't clipped.
- **LED mirror.** The glow is a radial gradient confined to each pixel's own cell
  (a blurred layer bled into neighbours), and the panel's global brightness is
  not simulated because multiplying pixel opacity flattens per-pixel contrast.

### Device registry — `cmd/ember/devices*.go`, `clients.go`, `knob_settings.go` (#221)

Per-device tokens so the master `EMBER_TOKEN` never leaves the Mac/server.
The registry holds devices only: kind `cinder-knob` (the ESP32-S3 knob
in the cinder repo) and, since #230, the clock as `awtrix-ng` (no token;
"Clock record and config façade" below). Scoped API tokens for
sources and controllers (`client`, below) live in their own store
(`clients.go`, #340), so nothing that ranges over devices (list, view,
checkin, OTA, stats, doctor, epoch) can meet one. Knob-only lookups
(`findKnob`, re-provisioning by `hw_id`) match `kind == cinder-knob`, so a
record of another kind is 404 on knob routes and never re-provisioned as a
knob. The menu app shows one knob (it filters the list by kind), but the
registry keys by `hw_id` so re-provisioning the same board finds its record.

- **Record** (`deviceRecord`): `id` (`knob-` + last 6 hex of `hw_id`; the full
  `hw_id` on a collision), `kind`, `hw_id` (12 hex, `:`/`-` stripped,
  lower-cased), `name` (≤64 chars, default `Knob 61FC8C`), `token_sha256`,
  `pending_token_sha256` + `rotated_at` during a rotation, `config`
  (`knobSettings`), `config_version` (starts at 1), `created_at`, and
  `last_checkin` (`seen_at`, `fw`, `ip`, `rssi`, `heap_internal_free`,
  `heap_internal_largest`, `uptime_s`, `applied_version`), and `caps`
  (#341: the capabilities the last checkin reported, absent for a legacy
  knob; see DEVICE-PROTOCOL "Capabilities").
- **Persistence:** the whole registry plus the epoch is one JSON blob in the
  SQLite settings KV (key `devices_json`), the same store as the overlay
  settings. Every mutation clones the state, persists, then swaps under
  `deviceRegistry.mu`, so a failed write (500) changes nothing. Two hot paths
  skip that (#233): device auth is a locked scan with no clone or write (only
  a rotation promotion writes), and a checkin updates `last_checkin` in place
  and writes the blob only when the last write is `deviceCheckinPersistInterval`
  (10 min) old; a checkin that mints a pending rotation token, or whose `caps`
  differ from the stored ones (new, changed or gone), writes at once.
  If that periodic write fails, the checkin still answers 200 (logged
  `device checkin not persisted`) and the next checkin or the flush retries.
  Any other registry write carries the in-memory checkins along, and graceful
  shutdown flushes them, so a restart shows a recent `last_checkin` (at most
  10 min old after a crash). No store
  (tests, unwritable volume) = in-memory only. If the stored blob fails to
  read or decode at boot, the registry stays empty and refuses every write
  and device auth with 500 until restart, so the blob is never overwritten and
  can be repaired by hand; doctor's `devices` check fails with the error.
  Plaintext tokens are never stored or logged; only the mint response, a
  checkin carrying `new_token` and an approved claim poll contain one
  (`Cache-Control: no-store`).
- **Tokens:** `ekd_` + 32 random bytes, base64url (47 chars). Lookup hashes
  the bearer and compares against every record's hashes with
  `subtle.ConstantTimeCompare` (no early exit). `POST /v1/devices` with a known
  `hw_id` re-provisions: new token, old one and any rotation revoked, config
  and version kept, 200 instead of 201.
- **Client tokens (#269, own store since #340):** `clientRegistry`
  (`clients.go`) with its own lock, its own load error and its own blob
  (settings key `clients_json`, `{"clients":[{id, name, token_sha256, scopes,
  sources?, created_at}]}`). Id `client-` + 8 random hex,
  token `ekc_` + 32 random bytes, `scopes` ⊆ `{ingest, control, read,
  admin}` (sorted, deduplicated, at least one), a required `name`, no
  `hw_id`; at most 64 clients (every mint rewrites the client blob).
  `POST /v1/devices {"kind":"client","name","scopes"}` mints one
  (201, token once); `GET /v1/clients` lists them (owner or `admin`), and
  `PATCH`/`DELETE /v1/devices/{id}` and `/rotate` route any `client-` id to
  the client store by prefix. A client store that failed to load answers
  500 there and on `GET /v1/clients`, never a 404 from the device side.
  Those routes and every client response keep the pre-#340 wire shape (`clientView` mirrors `deviceView`: `kind: client`,
  `hw_id: ""`, `config_version: 0`, `last_checkin: null`, plus `scopes` and
  `sources`). Device ids must never start with `client-`. `requireAuth` admits a client on an owner route when it
  holds `requiredScope(pattern)` (`client_tokens.go`; any route not listed
  needs `admin`, and `admin` satisfies every scope); `requireControl` admits
  `control` on the Pomodoro actions. Wrong scope is 403, an unknown `ekc_`
  token 401. Client lookups (`clientRegistry.authenticate`) are the same
  constant-time scan over the client store; device auth scans devices only,
  so an `ekc_` token never reaches `/v1/devices/self/*`, now-playing control
  or `/admin/*`. Rotating a client mints and returns the new token at once (200)
  and revokes the old one: there is no checkin to deliver it on. Config,
  stats and checkin lookups only see devices, so a client id is 404 there.
  Mint, rename, rotate and delete leave the devices epoch alone (no knob needs to
  re-check in for a client change) and take the master
  token (a client caller, admin included, is 403: `requireAuth` marks
  client callers in the request context), and mint stops at 64 clients
  (400). An optional `sources` list (#299, 1-16 trimmed names ≤64 chars,
  needs `ingest`, 400 with `admin`, which passes `/v1/clear`) is stored on the record (`omitempty`, so older
  records load unbound) and kept across rotation; `requireAuth` puts the
  caller's id and sources in the request context and `allowSource` makes
  `POST`/`DELETE /v1/status` and `POST /v1/usage` 403 for any other
  `source` after body validation. Usage is stored per tool and account
  (#535) and owned per account by a writer (the client token's id; the
  master token is its own writer), never by its self-declared `source`:
  `UsageStore.PutIfOwned` applies `usageOwnerAfter` and writes under the
  same lock (rules in "Usage per account"). Session keys
  join `source/tool/session` with `/`, so `allowSessionKey` 400s a `/` in a
  bound caller's tool or session and mint rejects `/` in a source; otherwise
  `ci` + `lab` + `claude/1` would address `ci/lab`'s session. `boundSources` fails closed: an `ekc_` bearer with no caller in the
  context (a route not behind `requireAuth`) is 403, never treated as
  unbound. `docs/openapi.yaml`
  records each operation's credentials; `openapi_test.go` checks every
  registered route against it (route × credential table).
- **LAN re-pair (#658):** `claimRegistry` (`devices_claims.go`), in memory
  only with its own lock, so a restart drops every claim and the knob's next
  poll is 404. A knob with an Ember URL but no valid token posts `POST
  /v1/devices/claim {hw_id, fw, claim_key}` (unauthenticated,
  `rateLimit`); the claim (`c_` + 8 random hex) gets a 6-digit code from
  `crypto/rand` (rejection-sampled, unique among live claims) and lives 10
  min. The server keeps the SHA-256 of `claim_key` and compares it and the
  code with `subtle.ConstantTimeCompare`. Caps: 2 live claims per source
  and 16 in all; past that 429, never an eviction, so a flood can't push
  out the real knob's claim. The source is the socket address (never a
  forwarded header), unmapped and without its zone, and an IPv6 address
  counts by its /64, because one host can take any number of addresses in
  its prefix. Expired claims are pruned under the lock before the count,
  and the count and insert share that lock. Claim and poll bodies stop at
  4 KiB (413). What stays open: whoever holds 8 sources (8 IPv4 addresses
  or 8 /64s) can keep all 16 slots full, by design of "no eviction", so
  the knob falls back to USB; the shared per-IP rate limiter
  (`IPLimiter`) still keys by exact address, as on every route. In Docker
  bridge mode (`-p`) every client arrives from the gateway address, so the
  per-source cap of 2 is the whole server's cap: one client holding 2
  claims (or a knob that rebooted twice) blocks every other claim until
  they expire, and the 16 never comes into play. Production runs
  `--network host` (the Unraid template's `Network: host`), where the
  sources are real. Link-local IPv6 is one `fe80::/64` for the whole LAN,
  the same effect for IPv6-only clients; cinder uses IPv4. 409 `already_paired` while that `hw_id`'s
  record checked in within 5 min (a live knob never claims). The owner
  lists pending claims (`GET /v1/devices/claims`) and approves one with the
  code from the knob's screen (`POST …/claims/{id}/approve`, master only:
  approval mints a knob token like `POST /v1/devices`). Both sides find
  the routes through the server feature `lan_pair` in the open `/version`
  `features`: an older server answers the claim routes 401 (the `/v1/`
  owner catch-all) and the list 405, never 404. Approve runs, all under
  the claim lock: an approved claim first (the right code is 200 with the
  same device, a wrong one 400 that costs no try, so typos can't drop a
  claim whose token is already minted); then the code (five wrong ones
  drop a pending claim); then the live check again, so an old claim can't
  revoke a knob that paired another way in between (409 `already_paired`,
  the claim dropped); then `provision()`: a new record, or a re-provision
  (new token, old one and any rotation revoked, config and version kept,
  epoch bumped; a checkin in flight between the live check and the mint
  can still be revoked, since only one token is ever valid and the knob
  re-pairs). Every other claim for that `hw_id` is then dropped as
  superseded, because the mint just revoked whatever they could deliver.
  The plaintext token stays on the claim, and every keyed poll (`POST
  /v1/devices/claim/poll`) returns it until the first request
  authenticated with it, which deletes the claim (`deviceAuthWith` calls
  `claims.collect`), as with rotation delivery. A poll first checks that
  the token is still the record's (`tokenCurrent`), so a device deleted or
  re-provisioned after the approval makes the claim 404 and frees its
  slot. Approve and decline run the same check on an approved claim, so
  one whose device was deleted or re-provisioned is dropped and answers 404
  instead of the stale view or 409. An approved claim lasts 10 min from the
  approval. Decline drops a pending claim; an approved one is 409
  `claim_approved` (the token exists; undo by deleting the device). Approve and decline take the
  same lock, so a race ends as exactly one of "approved, decline 409" or
  "declined, approve 404". A re-post with the same `hw_id` and
  `claim_key` returns the same claim (200) without taking a slot, so a
  knob that lost the reply doesn't burn its source's cap. 409 and 429
  carry `Retry-After`: the end of the 5-min live window, or the expiry of
  the claim that blocks the cap. `onExpire` runs under the claim lock and
  must not block or call back into the registry. Every poll 404 (unknown,
  expired, declined, dropped, superseded, revoked, wrong key) has one body. Logs: `device claim created|refused|code rejected|approved|declined|expired|collected`
  with `claim_id`, `hw_id`, IP; never the code, key or token
  (`TestClaimSecretsNeverLogged`). Threat model: the code on the physical
  knob's screen is the proof of presence, and a rogue LAN claim's code
  exists only in the rogue's reply; brute force needs `EMBER_TOKEN`. The
  claim's 409 is an accepted oracle: it costs no slot and says only that
  a MAC checked in within 5 min, which ARP and the `knob-xxxxxx` name
  already show on the LAN, and 48 bits leave only targeted probes. Token
  delivery is plain HTTP on the LAN, the same exposure as checkin
  `new_token`. Decline is a `POST …/decline`, not `DELETE
  /v1/devices/claims/{id}`: Go's mux rejects that pattern next to `DELETE
  /v1/devices/{id}/demo` (both match `/v1/devices/claims/demo`). Device ids
  are `knob-`, `clock-` or `client-` prefixed, so none is `claim` or
  `claims`.
- **Rotation:** `POST /v1/devices/{id}/rotate` (202) only marks the record.
  The first checkin made with the old token mints the pending token and returns
  it as `new_token`; later old-token checkins get the **same** token again
  (a lost response costs nothing, and a second caller cannot replace the
  knob's token). Its plaintext is held in memory only, so after a restart the
  next delivery mints a replacement. The first request made with the pending
  token promotes it, retires the old hash and bumps the epoch. The old token
  also stops working 24 h (`deviceRotationGrace`) after the rotate, not after
  delivery, so rotating a knob that stays offline that long is a revoke; the
  pending one stays valid. **Rotation is hygiene, not leak response:** whoever
  holds the old token can collect the new one. For a suspected leak,
  `DELETE /v1/devices/{id}` or re-POST the `hw_id` (USB), which revoke at once.
- **Config** (`knobSettings`, schema v1): `brightness{follow_ember, level
  0-255, floor 1-255 ≤ level, startup 0-255}`, `pages[{id,on}]` (defaults `bot`,
  `pomodoro`, `weather` on and `nowplaying` off; a stored or PUT list missing a known
  id gets it appended off (at most 8 pages, the firmware's KS_MAX_PAGES), #284; any id matching `^[a-z][a-z0-9_-]{0,15}$` is kept, so
  firmware can add pages without a server release; order = page order; no
  duplicates), `home` (must name a page that is on), `swipe_pages` (#315,
  cinder#57, firmware 0.9.15: a vertical swipe changes the page; absent or
  null = true like the bot flags; see DEVICE-PROTOCOL "Swipe pages"),
  `poll_ms` 1000-10000,
  `bot{sleepy_after_s 0-86400 (0 = never), demo_hold_s 1-600, source_label,
  working_ring}` (the two booleans, cinder#42: the curved host label and the
  glint orbiting the outline while working; absent or null = true, so a
  record stored before #282 keeps both on with no config push;
  `weather_moments` and `weather_every_min 0-240` (0 = no periodic moment),
  #443, cinder#90: the bot page's weather moments, absent or null = on and
  30; see DEVICE-PROTOCOL "Bot weather moments"),
  `display{fast_link, rotation}` (`fast_link`, cinder#23: the knob's panel
  QSPI link at 80 MHz, absent or null = true like the bot flags; the knob
  reboots to apply a change and falls back to 40 MHz by itself after a
  failed link check. `rotation` `0|90|180|270`, #386, cinder#45: the
  display's rotation in degrees, default 0, a stored record without it
  reads 0; a knob that reported `caps` can only change it to a value in
  `caps.rotations`; see DEVICE-PROTOCOL "Display rotation"),
  `quiet{calm, dim_level 1-255}` (#343, cinder#28: what the knob does while
  the view says `quiet`; absent or null = `calm` true and `dim_level` 20,
  Ember's default night level; see DEVICE-PROTOCOL "Quiet hours"),
  `idle{dim_after_s, dim_level, off_after_s}` and
  `apps{pomodoro{focus_color, break_color}, weather{faces[]}, nowplaying{dim_after_s}}`
  (#656, cinder#255-257: omitempty pointers the server never fills, so an
  old record keeps its bytes and `config_version`; absent = the firmware's
  default; unknown keys and page ids are a 400; see DEVICE-PROTOCOL "Idle
  and per-app options"),
  `diagnostics` `off|basic|full` (default `off`; a record stored before #239
  loads as `off`; see "Knob diagnostics" below), `stats_interval_s`
  `30|60|120|300` and `live_interval_s` `2|5|10` (defaults 60 and 5, chosen
  by measurement in #249; a record stored before #249 loads with the
  defaults, and the firmware's own defaults match, so no config push is
  needed). Unlike the
  overlay's top-level `mergeSetting`, the owner PUT decodes the body onto a copy
  of the current config with unknown fields rejected: nested objects merge
  field by field (`{"brightness":{"level":100}}` keeps the other brightness
  fields), arrays (`pages`) replace whole. `config_version` and the epoch move
  only when the merged config differs, so `{}` is a no-op. GET/PUT answer the
  version in `X-Ember-Config-Version`.
- **Epoch:** `/state` carries `X-Ember-Devices-Epoch`, an opaque counter
  (compare for inequality) persisted with the registry and bumped on mint,
  config change, rotate, rotation promotion and delete, and on a clock record's
  creation or `config_version` change; checkins and probes don't move it. The knob checks in
  when it changes, else every 60 s, so a settings edit lands in about one
  `/state` poll without putting per-device data in a public response.
- **Client migration (#340):** before either store loads,
  `App.loadRegistries` runs `migrateClientRecords`: it moves every `kind:
  client` record from `devices_json` into `clients_json` (same id, name,
  scopes, sources, hash and `created_at`; the other device records are kept
  byte for byte, and the epoch stays). It writes `clients_json` first (a
  same-id record from `devices_json` replaces the stored one, since only an
  older server could have changed it there) and then `devices_json` without
  the clients, so a crash between the two writes leaves both copies and the
  next boot finishes the move. A blob with no client records is not
  rewritten, so reruns are free. If the move fails (a decode or write
  error), both stores fail closed like a corrupt blob: device and client
  auth and writes answer 500 until a restart succeeds. The device registry
  also refuses to load a `client` record, so an unmigrated blob can't put a
  client back into device iteration.
- **Doctor:** `devices` check lists each knob's last-checkin age and the
  clock's last good probe (`last_seen`); warns when a knob never checked in, a
  clock was never probed, or either is silent for more than 5 min. Under
  `EMBER_CLOCK=off` the clock shows as `clock disabled`, no warning. It sees
  devices only; `client_tokens` reports `count=N`, or fails when the client
  store didn't load.
- **App (Settings › Knob, #222/#223):** EmberKit `Knob/` holds the protocol
  and models, no UI. `ImprovCodec` (Improv Serial frames + checksum; the host
  appends `\n` after each frame) and `CinderLineCodec` (`CINDER1 {json}` lines,
  sorted keys) are pinned by the shared vectors in
  `macos/Tests/EmberKitTests/testdata/knob/` that cinder's firmware tests can
  reuse (keep them in sync with cinder's `firmware/test/vectors`).
  `KnobStreamDemuxer` splits the port's bytes into frames, `CINDER1`
  lines and log lines. `KnobLink`/`KnobLinkOpener` is the transport seam (USB
  serial now, BLE later); `SerialPortLink` opens `/dev/cu.*` raw
  (`O_NONBLOCK`, `cfmakeraw`, `HUPCL` cleared first) and **never touches
  DTR/RTS**: toggling RTS resets the ESP32-S3. It takes the port exclusively
  (`TIOCEXCL` + `flock`, like pyserial's `exclusive=True`) and releases it with
  `TIOCNXCL` before closing, because the tty's exclusive flag outlives the fd
  while anything else holds the port open; a busy port shows as "couldn't open". `KnobSerialPorts` watches IOKit for `303a:1001` and keys a
  port by its USB serial number (the MAC = `hw_id`) **without opening it**: the
  port is shared with `idf.py monitor` and esptool (ROM download mode is
  `303a:1001` too), so Ember opens it only while Settings › Knob is on screen
  (one probe on appear and per new board, 3 device-info tries, a `CINDER1`
  boot event also counts) or when the user starts a setup or a USB action. No
  plug-in notification for that reason.
  The firmware contract: the Ember URL is `http://host[:port]` only (the
  knob has no TLS; scheme and host sent lower-case, host IPv4 or `[a-z0-9.-]`,
  no path), checked before the mint; the knob name is the server record's,
  at most 32 UTF-8 bytes; a knob whose URL changes restarts itself after
  `set_ember`, so the next step reconnects (and resends Wi-Fi if it comes back
  `ready`); Improv `invalid RPC` or `{"ev":"wifi","state":"invalid"}` means the
  Wi-Fi settings were rejected; `ember: connecting` is still in progress.
  The knob's lwIP can't resolve `.local` (no mdns component) and `localhost`
  would be the knob itself, so `KnobEmberURL` handles those hosts: a `.local`
  name is resolved on this Mac first and replaced by its IPv4, falling back to
  this Mac's LAN IPv4 only if that fails; `localhost`, `127.*`, `::1` and
  `0.0.0.0` become this Mac's LAN IPv4. The sheet says when the host was
  replaced. The cleanup that deletes a record minted by a failed setup runs as
  an unstructured task, so a cancelled setup still sends the DELETE.
  `KnobProvisioner` runs Improv device info (no answer in 2 s = not cinder),
  the knob's own scan, `POST /v1/devices` (token), `set_ember`, the Wi-Fi RPC,
  follows the reboot (reopens by serial number, handing the new session to the
  caller so a failed step can still re-mint on it) and waits for `ember: ok` or
  a server checkin strictly newer than the record's checkin at mint time (the
  server's clock, never the Mac's). If a step after the mint fails and the
  record never checked in, it deletes the record. `KnobModel` shows the newest `cinder-knob` record only (one
  knob; a setup on another board replaces it after a confirm and deletes the
  old record) and autosaves `ConfigModel<KnobSettings>` as a merge PUT of the
  changed fields only. Tests use a fake link and a pty pair; nothing opens a
  real serial port. Feature gates (the now-playing page, the stats interval
  pickers, the Rotation picker) read the record's `effective_caps`
  (`KnobDevice.supports(feature:)`, `supports(page:)`,
  `rotationChoices(current:)`: the Rotation picker shows when `rotations`
  holds more than 0 or the stored value isn't 0, and marks a stored value
  outside `rotations` as unsupported); a server without `effective_caps`
  falls back to the old fw semver floors (`KnobCaps.legacy*Floors`), kept
  for two releases.
- **Pairing requests (#659):** `KnobPairingModel` (owned by `KnobModel.pairing`)
  reads `/version` and polls `GET /v1/devices/claims` every 3 s while
  `KnobDetail` is on screen only when `features` lists `lan_pair`; it stops for
  good on that gate or a 404/405 from the list (older server: the UI stays
  hidden). The
  Pairing request section (top of Status, above the empty state when no knob is
  registered) takes the 6-digit code (spaces allowed) and calls
  `POST /v1/devices/claims/{id}/approve`; a 400 `wrong_code` carries
  `attempts_left`, a 404 means the claim expired, a 409 `already_paired` drops
  the claim ("this knob already paired"). A 409 `claim_approved` on decline
  shows "already approved". Decline is
  `POST .../decline`. Approving a claim whose `hw_id` differs from the
  registered knob shows the one-knob replace confirm, then `didSetUp` forgets
  the old record. The Forget dialog adds the pair-again sentence only when the
  knob's caps list `lan_pair`.
- **Knob page previews (#240):** Settings › Knob › Apps draws each page's
  466 px round face in SwiftUI (EmberKit `KnobFace/`), the way `PanelPreview`
  shows the clock's frames, but drawn locally: the knob renders on-device and
  its view endpoint takes a device token only. The app composes the same
  inputs from what it already polls: `/state` sessions (mood and host label,
  `KnobMood`, the firmware's render priority and #213 host rule; on a
  waiting or done bot the newest session in that state leads, as the
  server's `mood.lead*` does, and the label is its project like cinder
  0.9.81's `ember_host_project_label` (≤10 slots or 9 + "...", " +N" other
  sessions in that state, ≤12 slots, the host label without a letter or
  digit) with a Montserrat 18 host line at r 147 when several hosts report,
  the tool icon raised to r 128 and the label clipped to the 340×116 canvas
  at y 312; `KnobLabelTests` holds cinder's table cases; the last
  good mood stays while `/state` fails, as on the knob),
  `/v1/pomodoro/state`, `/v1/weather/state` (tracked while a preview is up)
  and `/v1/display/brightness` (read at once when "Follow Ember brightness"
  turns on, then every 60 s), plus the draft knob settings, so toggling a
  page or a bot timing shows at once. The renderers port cinder's firmware
  (`bot_shape.c`, `bot_view.c` Head style with its plain 9 px outline and
  squash variants picked by sy/sx so the pop only pulses the eyes, the eye
  lid as a clip line from the top (`bot_lid_line_y`),
  `pomo_view.c`, the weather page's Temperature face (`temp_face.c`,
  `wx_temp.c`, `wx_paint.c`, #430: colour-scale gauge ring with its
  marker, thermometer and mercury, value, today's low and high, sky
  sub-line, face dots; today's range is `current.today_min_c`/`today_max_c`
  of `/v1/weather/state`; the sky, clouds, hero and drift draw only in
  the animated preview (below), the burn-in shift not at all; the
  sub-line follows `wx_temp.c`
  (#578): `current.cloud_pct` picks the `sky_word` band, held near an edge
  by `WX_BAND_HYST` while the preview stays up, the condition's word
  without it, and by day `current.uv` ≥ 3 replaces it with
  `wx_uv_advice` in `wx_uv_rgb`; night is the fetch's `sun.night`, the
  knob's own, until a sent sun time plus 2.5 min passes, then the rounded
  times until the next poll), the six other weather faces (#610:
  `KnobRainFace`, `KnobCastFace`, `KnobWindFace`, `KnobDaysFace`,
  `KnobSunFace`, `KnobAirFace` from `wx_rain.c`, `wx_cast.c`, `wx_wind.c`,
  `wx_days.c`, `wx_sun.c`, `wx_air.c`, fed by `/v1/weather/state`'s
  `faces`: values, units, sub-lines with LVGL's `#RRGGBB word#` recolour,
  the gauges' colour ramps, the cast dial's hour cells, end labels, the
  lit face dot, and the heroes (umbrella and drops, cast clouds, windsock,
  day rows, rising sun, flower) as static vectors; the rain, cast and
  wind heroes draw a synthetic rest pose (#615): every loop at its phase
  for k = 0 and every arrival at its end values, full opacity. The knob
  itself never shows exactly this frame, since k is past 0 by the time
  the cast gate or the wind's full fade opens. The rain sheets sit at
  `wx_rain_frame`'s strip offsets (45, 94 and 0 px down; snow falls at
  half speed, so 23, 47 and 0), wrapped through the two-tile strip and
  faded over `WX_RAMP` of each box; no drip, splash or bolt is on at k = 0
  (`SPLASH_DROP`, `wx_canopy_y`, `wx_splash_hit`, `DRIP` and `FLASH` are
  pinned, and the test derives that from them). The cast sheets draw both
  tiles at `wx_cast_sheet_off`, faded over `WX_CAST_RAMP`, and each
  side's clouds move by `wx_cast_breathe` (−4, +3 or 0 px) and fade to
  the rim from their `fade_from`, drawn from the same traced outline as
  the temperature sky (`KnobCastFace.Cloud.sky` converts each to a
  `KnobTempSky.Cloud`; no Path booleans); the gloom pulse is at rest. The
  windsock is in pose A with the streak rows and leaves
  `wx_wind_frame(level, 0)` shows; with no wind data it hangs still in
  pose B with neither, as on the knob. `KnobWeatherHeroes.swift` holds the
  tables
  (`KnobRainFace.sheets`, `KnobCastFace.sheets`, `clouds`, `tones`,
  `breathe`, `KnobWindFace.geo`) and the frame-0 maths;
  `KnobWeatherHeroParityTests` parses cinder's C initialisers and
  `#define`s strictly (a value it cannot read, such as an expression or
  an element count other than `N(array)`, fails the test rather than
  being half-read) and pins the tables, the constants and the frame
  formulas, so a cinder change fails `swift test`. Each strip tries
  wraps −2 to +1 against its box (at k = 0 today's tables only reach −1
  and 0, the knob's other phases reach −2, and +1 only a box taller
  than the strip period), and `rainSheetsDrawEveryWrapTheirGuardLetsThrough`
  and its cast twin pin the full range. The Knob › Weather
  preview animates the heroes as the knob does (#625, `KnobWeatherLive`,
  `KnobWeatherMotion.swift`): `KnobFaceView(face, t:)` takes the seconds
  since the face showed and each renderer draws cinder's frame at that t
  (`k = floor(t * 15)`; arrivals on `ARRIVE_FPS` 30 or `FADE_FPS` 15
  steps): `wx_rain_frame` (strip offsets, the two drips, the three
  canopy splashes, the storm bolt) under the hero fade `wx_arrive(tf)`;
  the cast strips, `wx_cast_breathe`, `wx_cast_gloom` and the rain gate
  and label fade; `wx_wind_frame` under the fade; `wx_air_petal` on the
  wilted flower; `wx_sun_frame` (rise and rays) and `wx_days_frame` (row
  fade, slide and count-up). `temp_face.c` `paint()`'s own arrival is
  drawn on every face too (#644): the value counts up
  (`wx_count_value`/`wx_count_step` on `ta`; wind, air and Temperature
  colour by the counted value, the wind sock with them; the forecast
  steps `wx_cast_step_text` to "12+"), the gauge marker eases to
  `frac * wx_arrive(ta)` (the forecast marker stays at now), the
  Temperature mercury rises to `wx_merc_top` on the same ease in half-px
  steps, the forecast's teardrop (`KnobFaceRender.dropPath`, prebuilt
  from `wx_paint_drop`'s bezier and 7 px head) slides from now to the
  spell along `wx_cast_ease` and fades out by `WX_CAST_DROP_OUT_S`, the
  sub-line fades in at 85, 170, 255 from 1.32 s (`SUB_FADE_S`), and the
  sock fades in without wind data. The half-px mercury step applies
  only while the ease runs, so the settled tube is the rest pose's
  unrounded one; the ticks always light from the half-px top
  (`top <= TICKS[t]` on `set_mercury`'s `q / 2`), so −2 °C keeps its
  160 px tick lit at rest as the knob does. The drop and gauge marker
  maths run in `Float` like cinder's (`wx_cast_drop_deg`, `wx_gauge_xy`
  on a `float` frac), since a double-precision angle can round the
  drop to the next quarter degree or the marker to the next pixel (the
  marker's pi is `(float)M_PI`, `Float(Double.pi)`, not the rounded-down
  `Float.pi`). With no t (the Pages overview, Reduce Motion) the faces
  draw the rest pose above; under Reduce Motion the Temperature face
  instead draws its settled frame (`KnobWeatherLive.rest`: t at 1.8 s,
  every arrival and the sub-line fade done, drift time 0), so its sky
  shows at rest. `KnobWeatherMemo.shownAt`
  is the show time, like cinder's `s_t`: it resets on page open (a fresh
  memo) and when a reported face changes, not when the preview's own AUTO
  pick steps to another face or the data refreshes, and a clock set back
  re-stamps it. A knob that reports its face reports AUTO's resolved face,
  so the preview can't tell an AUTO step from a turn: it replays the
  arrival on both, while the knob replays only on a cut (`wx_job_cut`).
  `KnobWeatherSchedule` ticks at 15 fps, mid-frame on the show's frame
  grid, only while the view is on screen in a visible, active window
  (the feeds' `WindowVisibilityReader` signal) and the face moves: loops
  (rain, cast, wind with data, wilted air) tick on, arrivals stop on the
  first frame that equals the rest pose: 4/3 s for the counts and fades,
  1.8 s (`WX_ARRIVE_S + SUB_FADE_S`, where the sub-line fade and
  cinder's 30 fps paint throttle end) when a sub-line shows, 22/15 s
  for the sun; a face with neither a value nor a sub-line nor a hero is
  still. The Temperature sky (#648, `KnobTempSky.swift`,
  `KnobTempSkyRender.swift`) draws only with a t (the Pages overview
  and other static callers stay without it), under the thermometer's
  mercury and labels: `wx_temp.c`'s hero rules (by day the sun up to
  band 1; by night a moon with `moon.up` true, phase 0-99 and
  illumination 0-100 rounded to `WX_ILLUM_STEP`, small behind band 3,
  none at 4; stars at night up to band 1), `wx_paint_rays` as a raster
  built once from the per-pixel formula in `Float` (keep-out boxes and
  rim fade; alpha bit-equal to cinder's), the stars, the sun and the
  phase-cut moon as vectors and gradients, and `wx_clouds_for`'s bands as
  outlines traced through `wx_cloud_shape`'s points (each puff's arc to
  the next point, then the base), with no path booleans, since macOS 26
  and 27 disagree on their result. Each cloud is one sprite with its own rim fade, composited
  before the fade so its outline doesn't show through the fill; hero and
  clouds fade in on `wx_arrive(tf)`. Colours stay 8-bit where the panel
  is RGB565. The drift replays `wx_drift_step` at `WX_DRIFT_FPS` from
  `WX_ARRIVE_S` on a fixed seed (`KnobTempDrift.seed`, the bot's
  0x454D4252, start count 1) in place of `esp_random()`. It never
  settles, so a cloudy Temperature face ticks like a loop while visible
  and a clear one stops after its arrival. The replay is bounded: past
  60 s it starts at a frame within the last minute where the clouds'
  state is certain (`KnobTempDrift.anchor`: the bound on how far the
  clouds can lag, 12 px a cloud at the window's start, has drained to 0
  and exactly one cloud's target moves), else at the show. The knob
  restarts the drift on a new seed at every full re-bake (a band, night
  or sun change); the preview keeps its one path. What stays static: the
  burn-in shift, the face cross-fade `fo`, and the weather moments,
  and `nowplaying_view.c`
  for the now-playing page, #284: backdrop disk, ring, album, artist avatar
  riding the ring, three text lines, folded to ASCII like `np_text_fold`;
  `KnobNowPlayingFeed` polls `/v1/nowplaying/state` and fetches the three
  pictures from `/v1/nowplaying/art` once per `art_version`, only while a
  preview is up; burn-in dimming and drift are not drawn; text folds with the firmware's own tables, `KnobTextFold`, tested against np.c; failed picture fetches retry with a 5-60 s back-off; Knob > Now Playing is its own app page from firmware 0.9.0; `GET /v1/nowplaying/state` sends `X-Ember-Now` for clock skew).
  The weather preview draws the face the knob shows (#611):
  `KnobShownFeed` polls `GET /v1/devices` every 2 s while a preview with
  the weather page is visible (the page's own pane, or the Pages overview
  with weather on; `WindowVisibilityReader` and `controlActiveState`, so
  not while the window is minimised or covered or the app is hidden or
  inactive), resumes at once when it shows again, backs off 4, 8, 16,
  30 s on failed polls and resets on a good one. It publishes only a
  change, so an unchanged poll redraws nothing, and hands each list to
  `KnobModel.adopt(devices:)`, so `KnobDetail`'s 15 s reload skips only
  its device-list fetch while it runs and still refreshes settings, OTA
  and coredumps. It starts from `env.knob.devices`, so the first frame
  already shows the reported face. The seed is not a good list: if the
  first poll after open fails, the face drops to AUTO at once until a
  poll succeeds. `KnobShownFollow` follows the knob
  whose `page`/`face` changed last (the `at` of the poll that brought
  the change; every poll moves `at`), keeping the current knob on a tie;
  on `weather` its face is drawn, another page keeps the last reported
  weather face, and with no fresh report (old firmware, a stale knob) or
  no good list for over 50 s `KnobWeatherAuto` mirrors
  `wx_temp_from_shown`: Rain now while wet or stormy, else Rain forecast
  when a spell starts within `WX_CAST_SOON_S` (1 h; `WX_CAST_KEEP_S`,
  1.5 h, while it is on screen), else Wind from `WX_WIND_AUTO_KMH`
  (39 km/h; down to 34 while shown), else Air above `WX_AIR_POOR` (60,
  minus the 5 hysteresis while shown), else Temperature.
  `KnobWeatherMemo` (a value) holds what was drawn: AUTO's keep, each
  face's own hysteresis while it stays drawn (rain intensity, cast,
  windsock level, wilted flower), and the Temperature cloud band, which,
  as in `wx_temp_from_shown`, moves with every observation whatever face
  is drawn. The section computes the face once with `peek` (no state
  change in `body`), so face and caption come from the same pick, and
  commits it whenever it differs (`draw`, from the first render on and
  after a reset); it redraws every 60 s and on new data.
  The memo starts empty each time the weather preview opens, as the knob
  does on page open. The lit dot is the drawn face's index in
  `wx_face_id_t`. Every face, Temperature included, goes grey like
  `wx_look_from_obs`: weather off, `current.stale`, or `generated_at`
  more than 1800 s old (strictly, `WX_MAX_AGE_S`). `BotBehavior.Tuning`
  carries the knob's slower, flatter hop, its swap-blink hide rule and its
  error shake, huff and gaze split (`bot.error`). `knob-theme.json`
  (`macos/Sources/EmberKit/Resources/`) holds the numbers the renderers
  share with the firmware: geometry, palette, label layout and LVGL font
  metrics, eye geometry, the Temperature face's scale, ring, thermometer
  and label layout, and the other weather faces' colours, thresholds and
  layout (`weather.rain` … `weather.air`); their words stay in code and
  are pinned by the same test. Shape formulas, the weather heroes' shape
  tables and the hop squash curve stay in code on both sides. `KnobThemeTests` pins
  every key; with a cinder checkout next to this repo (or `CINDER_DIR`) it
  also parses the firmware source and checks each key against the constant
  it mirrors. tarakanof/cinder#34 tracks generating the C side from a copy.
  Labels use the bundled Montserrat Medium (SIL OFL 1.1,
  `Montserrat-OFL.txt` next to it), the font LVGL's built-in sizes are made
  from, placed by LVGL's line box (top, line height, baseline). The bot
  redraws only when its pose changes: 20 fps while `BotBehavior.isAnimating`,
  otherwise the next frame is scheduled at `nextEventAt`. Weather redraws
  once a minute (staleness) plus the hero frames above, the Pomodoro ticks once a second, and only while
  the pane is on screen; Reduce Motion keeps blinks only. The Pages overview has no timers: it redraws when the polled data
  changes. `KNOB_SNAPSHOT_DIR=… swift test --filter knobFaces` writes PNGs
  of every face.

### Clock record and config façade — `cmd/ember/clock_device.go`, `clock_config.go`, `clock_presentation.go` (#230, #232)

The TC001 is a registry record of kind `awtrix-ng`, so both displays are
addressed as `/v1/devices/{id}`. Phases 2 and 4 of the per-device apps spec
(`Personal/Ember/Specs/2026-10-04-per-device-apps-design.md` in the vault): the
record carries identity, liveness and `config_version`; its presentation
config is stored as the clock's own config row (#232), with the old
endpoints as aliases. Before the migration (no clock record yet, or a failed
migration) the same config is a façade over the overlay slices, as in #230.

- **Lifecycle.** A good probe of the effective clock URL (`GET
  /api/v1/device` answering `boardType: awtrixng` and a `uid`, through the
  clock-health probe the device watch and the sampler share; the body is
  read through the clock client's 1 MiB reply cap) creates the record. A
  probe result counts only if the URL it probed is still the effective one
  when it lands, so a probe of the old clock that finishes after a
  rediscovery swap is dropped. A discovery swap registers only the
  candidate it just made effective, never the others a scan touched. There
  is at most one `awtrix-ng` record (one clock in phase 2). A different uid
  at the effective URL replaces the record's identity only when the same
  new uid is seen twice in a row (two probes, or the swap plus a probe):
  the record is rewritten in place (new id and `hw_id`, `last_seen` reset,
  `created_at` and a user-set name kept, one epoch bump). A host answering
  with a fresh or alternating uid therefore never flips the record or moves
  the epoch, and `devices_json` cannot grow from the probe. Id `clock-` +
  last 6 characters of the uid (lower-cased, `:`/`-` stripped,
  `[a-z0-9_]{1,32}`, else ignored), `hw_id` = uid, name `Clock <SUFFIX>`,
  no `token_sha256`, no `config` stored (`omitzero`), creation bumps the
  epoch. Each later good probe sets `last_seen` (`seen_at`, `fw`, `ip`,
  `rssi`, `uptime_s`) in memory, never moves the epoch, and writes the blob
  on the checkin rule (every 10 min, or with any other registry write, and
  on shutdown). The URL stays in `clock_url.go`: the record does not own
  it. Multi-clock needs the URL on the record first.
- **Kind gates.** Device auth skips non-knob records, so the clock never
  authenticates even if a hash were planted on it. `rotate` and the registry
  `checkin` answer 400 (`… applies to kind "cinder-knob" only`). Stats,
  coredumps and OTA routes look up knobs only (`findKnob`), so a clock id
  is 404 there, and the OTA loops (`otaTargets`, `holdUnlessKept`, `otaRetire`)
  skip other kinds. `POST /v1/devices` still mints only knobs and clients.
  `PATCH` renames (a probe never resets the name). `DELETE` removes the
  record (204); the next good probe re-creates it with the default name.
- **Storage (#232).** The clock's presentation lives in the settings row
  `clock_config_json`: `{"schema":1,"apps":{…},"migrated_from_overlay":"<server
  version>"}`. `apps` holds an entry per app that has a store override:
  `agents` (`usage_cards`, `usage_per_model`), `focus` (both colours),
  `weather` (the whole weather app below, `on` included) and `calendar`
  (`tile_lead_minutes`, `popup_lead_minutes`). An app with no entry follows
  `config.json` (the baseline tier, same rule as the overlay: a stored entry
  pins all of that app's fields, an absent one leaves them to the file).
  Not stored there: `apps.calendar.on` stays meetings `enabled` (it gates
  the ICS poll, a source switch), `hidden_tools` stays `display_hidden_apps`
  (already a clock-only row), and `rotation` stays on the clock (NG keeps
  its own order; a second copy would drift whenever the order changes on the
  clock's web UI or the old route). The row sits beside `devices_json`, not
  inside the record: every registry write rewrites `devices_json`, and an
  older server would drop an unknown record field on its next write
  (a knob checkin persists within 10 min), losing the config; a separate row
  also lets one `PutSettings` cover a slice and the clock config without
  taking the registry lock under `cfgMu`, and survives a `DELETE` of the
  record. The row keeps fields and apps this server doesn't know: they are
  read with it and written back on every rewrite (reapply, alias PUT,
  façade PUT; `icon_ids` replaces whole), so rolling back from a newer
  server that added some loses nothing. In memory the row is `Config.clockPresentation` (unexported, so
  invisible to `config.json` and `diffConfig`), and its apps are applied onto
  the `Config` presentation fields the coordinator reads. Those fields have
  one writer after the migration, the clock config: the slices no longer set
  them, so the coordinator reads the clock's presentation and nothing else,
  with no code change on its side (goldens unchanged).
- **Migration (#232).** `migrateClockConfig` runs at boot right after
  `reapplySettings` when a clock record exists, else in the background after
  the first good probe creates the record (a server with `EMBER_CLOCK=off` and no record stays
  on the façade). It does nothing if the clock config is loaded. If
  `clock_config_json` exists but did not load (bad JSON, bad schema, invalid
  value), it refuses to overwrite it and stays on the façade. Otherwise,
  under `cfgMu`, every slice with a stored row that has presentation keys
  gives its app's current effective value to `apps` (reapply rewrites every
  stored row with its full view, so in practice every stored slice pins its
  app, as it already did; after reapply the value is exactly what the façade
  composed, so the clock shows the same thing),
  and one `PutSettings` writes `clock_config_json` and every stored slice
  rewritten without its presentation keys. A failed transaction changes
  nothing, keeps the façade, logs `clock config not migrated` once and makes
  the doctor `devices` check warn; the next good probe (or boot) retries in
  the background, and a success clears the warning.
  A crash before the commit is a rerun; after it, the next boot loads the row
  and finds nothing left to move. The migration holds `App.rotationOp`
  (waiting with no deadline, like reload), the lock boot and `/admin/reload`
  hold from the config swap through their reapply, so a record created
  mid-reload can't migrate from half-reapplied memory and store the
  `config.json` values over the slices'. It also can't interleave with a
  façade PUT. Boot releases the lock after its reapply and then migrates.
  A probe only starts it in a goroutine (one at a time), so the probe's
  single-flight and `deviceRediscoverMu` are never held while it waits.
  Shutdown waits for it, within its deadline, before it flushes the
  registry and closes the store. It refuses new runs before it waits, and
  does both before waiting on the workers, so a late probe can't join the
  wait and a hung worker can't skip it. A run
  that finds the row already loaded (for example an unreadable row fixed
  and loaded by `/admin/reload`) clears the recorded error.
  Lock order `rotationOp` → `cfgMu`, as for the façade. Doctor `devices`
  reports `clock config migrated (from <version>)`.
- **Façade.** `GET /v1/devices/{clock}/config` composes; `PUT` splits back.
  Every `awtrix-ng` record serves the same config (one clock). After the
  migration the presentation fields come from the clock config (which the
  `Config` mirrors), so the table below names each field's old endpoint, the
  alias that still reads and writes it.

  | Façade field | Source | Old endpoint |
  |---|---|---|
  | `apps.agents.usage_cards` | `usage_widget` | `/v1/usage/config` (`usage_json`) |
  | `apps.agents.usage_per_model` | `usage_per_model` | `/v1/usage/config` |
  | `apps.agents.hidden_tools` | hidden tools, sorted | `/v1/apps` (`display_hidden_apps`) |
  | `apps.focus.focus_color`, `break_color` | Pomodoro colours | `/v1/pomodoro/config` (`settings_json`) |
  | `apps.weather.on` | `rotate_in_apps` (current-conditions tile) | `/v1/weather/config` (`weather_json`) |
  | `apps.weather.native_icon` | `tile_native_icons` | same |
  | `apps.weather.forecast`, `forecast_hours` | `forecast_tile`, `forecast_hours` | same |
  | `apps.weather.air`, `moon`, `overlay` | `air_tile`, `moon_phase`, `overlay` | same |
  | `apps.weather.popups.on_change`, `sun`, `severe` | `popup_on_change`, `sun_popups`, `severe_alert` | same |
  | `apps.weather.popups.native_icons` | `use_native_icons` | same |
  | `apps.weather.popups.interval_minutes`, `duration_seconds` | `popup_interval_minutes` (0 = off), `popup_duration_seconds` | same |
  | `apps.weather.icon_ids` | `icon_ids` (overrides only) | same |
  | `apps.calendar.on` | meetings `enabled` | `/v1/meetings/config` (`meetings_json`) |
  | `apps.calendar.tile_lead_minutes`, `popup_lead_minutes` | same names (0 = popup off) | same |
  | `rotation.order`, `rotation.disabled` | the clock's own apps. Read back: the enabled ones in NG order / the disabled ones, Ember's pushed tiles (`origin: pushed`) left out. Written (by the app): every app in order, switched-off ones included so they keep their slot / every app that is off | `/v1/device/apps` (on the clock) |

  The field list is what the coordinator and the weather/meetings popups
  read for the clock today, so the coordinator is unchanged. Left out on
  purpose: source settings (weather provider/location/units/refresh, the
  air popup threshold, `weather.enabled`, `pomodoro.enabled`, Pomodoro
  durations, usage threshold and limit alarm), every sound (`severe_sound`,
  meetings `chime`, Pomodoro sound, attention chime: Sounds are their own
  move), the agent-card spine flags (they stay per Mac in `producer.env`),
  and the rotation dwell (a `config.json`-only field). Focus and agents have
  no `on`: no clock-only toggle exists for them. The knob reads none of
  this: its Pomodoro colours and pages are its own (`knobSettings`), and the
  clock's hidden tools do not apply to it.
- **Merge.** As the knob: the body is decoded onto a copy of the composed
  config with unknown fields rejected, so objects merge field by field
  (`{"apps":{"weather":{"popups":{"sun":false}}}}` keeps the other popup
  fields), arrays replace whole, `icon_ids` replaces whole too (a map, so an
  override can be dropped), `schema` must stay 1, an unknown app or key is a
  400, `{}` writes nothing. `hidden_tools` takes what `/v1/apps` takes (any
  non-empty name, no count cap), so a GET body can always be PUT back. Each
  touched slice is staged through its `settingSpec` (`setting.stage`: the
  same `apply` and validation as the old endpoint). A dry run on a copy
  comes first, so any 400 lands before any write. Then, inside one
  `tryUpdateConfig` (under `cfgMu`), the patch is merged again onto the
  config and hidden tools as they are at that moment and staged, so a
  write that landed meanwhile (an old endpoint, another façade PUT, a
  `/v1/apps` toggle, which also takes `cfgMu`) is kept: a PUT changes only
  the fields it names. Every changed slice and the hidden tools are stored
  in one SQLite transaction (`Store.PutSettings`); after the migration that
  transaction carries `clock_config_json` when a presentation field
  changed, and a slice only when its source part changed. Only after it
  commits do the new config and hidden tools go live, so a failed store
  answers 500 and leaves memory and the store unchanged. A slice whose view didn't
  change isn't rewritten; the specs' `after` hooks run after.
- **Rotation.** `rotation` keeps NG's meaning in both directions: `order`
  names the enabled apps in order, `disabled` the ones switched off, so a
  GET's `rotation` can be sent back. A PUT carrying `rotation` first reads
  the clock's list; only when that read succeeds and matches the patch is
  the clock write skipped. "Matches" is deliberately narrow (#449), since
  NG replaces its saved order on every write and rebuilds the apps it
  isn't given from its own fallback order, which Ember doesn't replay:
  `order` must be the clock's whole list, every app in the same sequence
  (pushed tiles, off apps and absent apps holding a reserved slot
  included), every listed app must already have a `slot`, no name may
  repeat, `disabled` may name only listed apps, and every app must stay on
  or off as it is. Anything else, when in doubt, writes. NG lists modules
  with no `slot` (and no `enabled`), so a clock with a module always
  writes. The app's save names every non-module app that is
  present or holds a slot, so on an unchanged clock without modules or
  slotless apps it skips; a module on the clock, a partial `order` or a
  GET's `rotation` sent back (it leaves out pushed tiles) writes.
  A failed read, or any difference (a
  reorder made on the old route or the clock's web UI), writes: the
  patch's `rotation`
  is forwarded as is (the `PUT /v1/device/apps` body) to the clock after
  validation and before the slices; a clock error is returned (502 when
  unreachable) and nothing else changes. After a successful write the list
  is read back; if that read fails or doesn't parse, the reply's `rotation`
  is `null` (unknown) rather than the old list. If the clock write went
  through and the slices then fail (a 500 store error, or a 400 from a
  concurrent change), the error body and the log say the app order was
  already written.
- **One operation at a time.** Every request that reads or writes the
  clock's app order or the cached list (façade GET and PUT, `GET` and
  `PUT /v1/device/apps`), the settings reapply at boot and
  `/admin/reload`, and the clock config migration (#232), holds one lock (`App.rotationOp`) for the whole
  operation: the pre-read, the write, the read-back, the cache update, the
  settings commit, the record's version sync and the response snapshot. So
  a read can't land after a newer write, two writes can't interleave their
  read-backs, and a GET can't see a half-applied PUT or reload. Each such
  request has a deadline from arrival (11 s, `rotationOp.budget`), started
  before its body is read, that covers the body, the wait for the lock and
  every clock call, under the app's 12 s `.clock` timeout.
  - A request that doesn't get the lock in time, or gets it already
    expired, answers 503 and changes nothing. If the client went away
    while it waited, the server logs that and answers nothing.
  - One that gets it late has only the rest of its deadline for its clock
    calls, so a write may be cut short: 502, as for any lost write, and the
    cached list is cleared because the clock may have applied it.
  - A façade PUT checks its deadline again inside the commit, under
    `cfgMu` and before anything is stored; if it ran out, it answers 503
    with no settings changed (and says when the clock's order was already
    written).
  - `/admin/reload` takes the lock before it publishes the reloaded
    config and holds it through the reapply and the version sync; boot
    does the same around its reapply. The reload waits for the lock with
    no deadline of its own, so it can stall for as long as the current
    holder runs (at most its 11 s budget plus the local commit).
  - Writes that don't touch the clock (`/v1/apps`, the settings endpoints)
    don't take the lock.
- **Version.** `config_version` is a counter on the record (#232; it was a
  hash of the composed config in #230). The record also stores
  `config_digest`, the SHA-256 of the composed config's JSON, composed from
  one snapshot (config and hidden tools read under `cfgMu`). A resync that
  finds a different digest stores it and adds 1 to `config_version`, so the
  version moves on every change, even one back to an earlier config.
  A record from #230 has no digest and holds the old hash: if that hash
  still matches the config, the digest is adopted with no bump; otherwise
  the counter continues from the hash + 1, so the move to a counter never
  goes backwards. The registry keeps a high-water mark
  (`clock_version_mark` in `devices_json`, raised on every bump and when a
  clock record is deleted), and a new record starts at mark + 1, so deleting
  the clock record and letting the probe re-create it never repeats a
  version a client has seen; clients compare for inequality. An older
  server drops the mark on its next registry write, so a delete and
  re-create done while one runs can still restart the count (the next
  delete on this server raises the mark from the surviving record again).
  Compute, store and the pause check all run under
  `clockSyncGate.mu`, and pausing or resuming takes it too, so a stale
  digest never overwrites a newer one and a resync can't start before a
  pause and finish inside it. It is resynced after every `tryUpdateConfig`,
  hidden-tool change, record creation and app-list read; a change bumps
  the epoch once, whichever endpoint caused it. The façade GET and PUT
  reply from one snapshot: under `clockSyncGate.mu` they compose the body,
  resync the record to that body's digest and read the version, so a
  settings PUT on an old endpoint (which doesn't take `rotationOp`) can't
  land between the two and one version never carries two bodies. A façade PUT bumps it once: its pre-read and read-back
  stay local while it talks to the clock; then, with the resync paused, it
  stores the final list (the read-back, or the pre-read when it skipped the
  write) and commits the clock config or the slices, and resyncs once.
- **Rotation cache.** `rotation` is the last app list the server read from
  the clock, kept in memory, `null` until one succeeds. Pushed Ember tiles
  are not part of it, so tiles coming and going never move the version. The
  façade GET, the façade pre-read and the read-back after a write on
  either route each have a 2 s budget (`clockRotationReadBudget`);
  `GET /v1/device/apps` uses the menu call timeout (8 s), cut to what is
  left of its deadline. A failed or
  unparseable GET or pre-read keeps the cached list. Every successful order
  write is read back, so a reorder moves the version once, at the write,
  and a write that leaves the order as it was doesn't move it. If the
  read-back fails or doesn't parse, the cache becomes `null` and the
  version moves; the next successful read moves it again. On the lossy
  clock link that is common (with ~44% of calls lost, about 4 reorders in
  10 bump twice); harmless, since nothing consumes the clock version yet.
  The server doesn't cache the written body instead: `rotation` stays what
  the clock reported, and NG may order the apps the body didn't name
  differently. A reorder on the clock's own web UI shows at the next read.
  Boot and `/admin/reload` reapply the overlay with the resync paused and
  resync once after, so a restart moves the epoch at most once (it does
  once if the stored version included an app list the new process hasn't
  read yet).
- **App.** (`ClockConfig.swift`, `ClockConfigModel.swift` in EmberKit, #359.)
  Ember.app uses the façade when `GET /v1/devices` lists an `awtrix-ng`
  record; that record exists only on a server that has the façade, so no
  `/version` feature is needed. The Settings clock node is then keyed by the
  record id (`clock-…`); without one it stays `clock`, and stored routes and
  expanded sidebar groups move between the two by kind. Settings › Clock › Apps › Agents, Focus, Weather
  and Calendar read and write their façade fields through `ClockAppLens`: the
  pane edits the old config type (`WeatherConfig`, …), the lens overlays the
  façade slice on the old model's draft for reading and writes only the
  slice back, so source-only fields (`weather.enabled`, Pomodoro rounds) still
  come from the old endpoint, read-only. A save is a merge `PUT` of the
  changed leaves (arrays, `icon_ids` and `rotation` whole). The Sources and
  Sounds panes still `PUT` whole bodies to the old endpoints, so after a
  façade save the fields it changed are written into the old models in
  place (`SettingsModels.adopt`) instead of reloading them. Only the changed
  fields are written, so an old-endpoint edit of a shared field such as
  `meetings.enabled` survives, and a load already in flight is dropped. The
  values go into the draft, so a pending edit carries them, and into the
  baseline only if no old-endpoint save started or ran during the façade
  `PUT` (a save epoch marks that). Otherwise the old
  model stays dirty and saves again with the new values, so a whole-body
  `PUT` that landed after the façade one is corrected. In the other direction,
  an old-endpoint save of usage, Pomodoro, weather or meetings reloads the
  façade. On a server whose `/version` `features` lists
  `device_app_enabled` (only a yes is kept; a no or a failed read is
  asked again on the next toggle, so a server upgraded at the same URL is
  picked up), an app toggle sends only
  that app, `PUT /v1/device/apps/{name}/enabled {"enabled":bool}`, so
  apps switched on or off meanwhile in NG's web UI or by Ember on another
  Mac stay as they are (#447). A 404 from that route (an older server)
  falls back to the full write below and drops the kept yes, unless the
  server or app list generation changed while it was in flight, in which
  case nothing more is sent. Reorders, and
  toggles on an older server, send
  `{"rotation":{"order","disabled"}}` (the same body as `PUT
  /v1/device/apps`) through the façade, built by `NativeAppsPlan` from the
  whole list after the change: `order` names every app, switched-off ones
  included so they keep their place (NG accepts a name in both lists), and
  `disabled` every app that is off, not only the one toggled (#413). The
  list itself, which needs `origin`/`present`, is still read from
  `/v1/device/apps`. App-order writes run one at a time, each sending the
  latest list; a reload that overlaps a pending or finishing write keeps the
  list on screen. A failed write undoes only its own toggle, and not even
  that once the same app was toggled again after it; once the queue is
  empty the app list is reread. A 503 (lock
  timeout) or 502 (cut-short clock write) shows its own message and
  refetches: the façade `GET` replaces the baseline, and edits made while the
  `PUT` was in flight are rebased onto it and sent again. A 404 marks the
  record missing: a settings edit,
  including any made while the `PUT` was in flight, is replayed into the old
  model and saved through the old endpoint, an app-order write is retried once on `PUT /v1/device/apps`, and the panes use
  the old endpoints. A missing façade is probed again (only a fresh `GET`
  turns it back on) when a pane reloads, or
  at a device-list refresh at most every 5 min. Loads carry a sequence
  number and model changes a generation, so a stale reply (an older load, or
  one for a previous record or server) is dropped; a save also invalidates
  loads already in flight. A pending façade edit is still sent when the
  record briefly drops out of the device list. The clock's hidden tools stay
  on the per-tool `/v1/apps` toggle: the façade's `hidden_tools` replaces the
  whole list, so a toggle from the menu bar landing in between would be lost.

### Knob diagnostics — `cmd/ember/devices_stats.go` (#239)

Hardware stats for Settings › Devices › Knob › Hardware. The knob's
`config.diagnostics` decides what it sends; the server keeps the samples in
memory only (a restart loses them; a checkin never writes the store for
them, see #233); the owner reads them per range and can ask for faster
reports ("live mode"). Firmware side: tarakanof/cinder#31.

**Checkin `stats` object (firmware → server).** Optional on
`POST /v1/devices/self/checkin`, next to the existing top-level fields, which
still carry `rssi` (dBm), `heap_internal_free` and `heap_internal_largest`
(bytes) and `uptime_s`; the server takes those into the sample, so `stats`
does not repeat them. A top-level `rssi` of 0, or a heap or uptime of 0, reads
as "not measured" (null in the stats), so send the real value or leave it
out. With `diagnostics` `off` the knob sends no `stats`. Every field is
optional; omit what the board can't measure (PSRAM fields on a board without
PSRAM) rather than sending 0. Unknown keys in `stats` are ignored, so firmware
can add fields before the server knows them. The level is the server's
call: with `off` it discards any `stats` it gets, and at `basic` it drops the
full-only fields, so a knob that hasn't applied a level change yet can't
show data the owner turned off.

| Field | Type, unit | Level | Meaning |
|---|---|---|---|
| `period_ms` | int, ms, 1..3 600 000 | basic | Window the averages and counts below cover: time since the previous report; for the first report after boot (or after diagnostics were turned on), time since measuring started. Missing = 60 000. |
| `cpu_pct` | array of number, %, 0..100, ≤ 8 entries | basic | Load per core over the window (index = core), e.g. idle-task runtime share. |
| `heap_internal_min` | int, bytes | basic | Lowest internal free heap since boot (`heap_caps_get_minimum_free_size(MALLOC_CAP_INTERNAL)`). |
| `psram_free` | int, bytes | basic | Free PSRAM now. |
| `psram_min` | int, bytes | basic | Lowest free PSRAM since boot. |
| `psram_largest` | int, bytes | basic | Largest free PSRAM block now. |
| `temp_c` | number, °C, -40..150 | basic | Chip temperature sensor. |
| `reset_reason` | string `^[a-z][a-z0-9_]{0,15}$` | basic | Why the chip last restarted: `poweron`, `ext`, `sw`, `panic`, `int_wdt`, `task_wdt`, `wdt`, `deepsleep`, `brownout`, `sdio`, `usb`, `jtag`, `efuse`, `pwr_glitch`, `cpu_lockup`, `unknown` (ESP-IDF `esp_reset_reason`, lower-cased without the `ESP_RST_` prefix), or `lvgl_stall` (cinder's LVGL stall watchdog restarted it). |
| `req_ok` | int, count | full | HTTP requests to Ember that got an answer (any 2xx/304) during the window. |
| `req_fail` | int, count | full | Requests that failed (transport error, timeout, non-2xx/304) during the window. |
| `req_ms_avg` | number, ms | full | Mean request duration over the window (merged per request, by `req_ok + req_fail`). |
| `req_ms_max` | int, ms | full | Slowest request in the window. |
| `fps` | number, frames/s | full | Rendered frames per second over the window. |
| `frame_ms_avg` | number, ms | full | Mean time to render a frame (merged per frame, by `fps·period_ms`). |
| `frame_ms_max` | int, ms | full | Slowest frame in the window. |
| `inv_areas` | int, count | full | Areas LVGL drew over the window, summed over every refresh that ended in it (cinder `refr_stats`, cinder#118): read at `LV_EVENT_RENDER_START`, after `lv_obj_update_layout` and `lv_refr_join_area`, so a moved object's new position counts and joined areas count once, as their bounding box. Past 32 areas (`LV_INV_BUF_SIZE`) LVGL collapses the list to one full-screen area (472×466 px), so at the busiest frames areas fall while px peak. |
| `inv_px` | int, px | full | Pixels in those areas over the window, the same basis. Areas that overlap without being joined each count their own pixels, so the overlap counts twice. |

`inv_areas` and `inv_px` must cover exactly the `period_ms` window: totals
since the previous checkin report, kept in an accumulator of their own, never
the 30 s serial stats log's take (sharing it would leave a 60 s report
holding at most the last 30 s, and the rate half the truth). cinder keeps
such an accumulator since 0.9.72 (the `refr_stats` window, reset with each
report, cinder#133).

Counts and averages are **per window, reset after each report** (not
since boot). Example (full):
`"stats":{"period_ms":60000,"cpu_pct":[12,40],"heap_internal_min":30000,"psram_free":7000000,"psram_min":6500000,"psram_largest":6000000,"temp_c":41.5,"reset_reason":"poweron","req_ok":28,"req_fail":2,"req_ms_avg":35.5,"req_ms_max":120,"fps":29.5,"frame_ms_avg":12.25,"frame_ms_max":40,"inv_areas":2700,"inv_px":1728000}`.
A `stats` value that isn't an object or has a field out of range (negative
bytes or counts, CPU outside 0..100, more than 8 cores, temperature outside
-40..150, a malformed `reset_reason`) is dropped whole and logged
(`device stats dropped`); the checkin itself still answers 200, so a
firmware bug never costs config or token delivery.

**Cadence and live mode.** Normally the knob sends `stats` once per
`stats_interval_s` (default 60 s). It checks in every 60 s, or, while
diagnostics are on, every stats interval when that is shorter (30 s), so a 120 or 300 s interval still keeps
the 150 s `online` window and carries `stats` only on every 2nd or 5th
checkin; the first report after boot or after diagnostics are turned on goes
out at once. A checkin brought forward by an epoch change carries `stats`
only when the window is within half a checkin period of the interval. Both
the checkin answer and `GET /v1/devices/self/view` carry
`"diag_live_until":<server Unix seconds>` while live mode is on and
diagnostics are not `off`; the field is absent otherwise (so the view body
and ETag are unchanged without it; in the view it is the last field). The
checkin answer also has the `X-Ember-Now` header, like the view. While the
server's now (`X-Ember-Now`, offset applied) is before `diag_live_until` the
knob checks in **every `live_interval_s`** (default 5 s) with `stats`
(`period_ms` ≈ the interval); after it, or when the field disappears, it goes
back to its normal cadence. The knob learns of live mode within one view poll
(`poll_ms`) and of an interval change with the config (the view's
`config_version`). A live checkin is an ordinary checkin: it reports
`config_version` and the usual fields, and the knob applies a `config` or
`new_token` in its answer as usual. The app asks for live mode only while
the 15-minute range is shown (the only range that draws live samples) and
sends `seconds:0` when the user picks another range.

**Measured (#249, knob on the live server, 5-10 min per value).** The
knob's cost does not move with either interval: CPU 20.7-21.7 % / 0.5-0.6 %
per core, render 6.5-6.7 ms avg, internal free 105.5 KB in every run; only
traffic changes: 30.4-31.2 req/min and 10-11 KB/min at 30-300 s stats,
35.5 / 39.2 / 54.4 req/min and 14 / 18 / 31 KB/min live at 10 / 5 / 2 s.
The knob checks in from its view-poll loop, so a live interval lands on the
next `poll_ms` tick (2 s polls: 5 s → ~6 s). The server spends ~11 µs per
checkin with stats and ~6 µs per 304 view (in-process); its memory per
device follows the intervals (below). The app's 15-minute poll (every 5 s)
carries the whole live window: 30 / 57 / 138 KB at 10 / 5 / 2 s. Defaults
60 s / 5 s: 30 s doubles the checkins for no knob-side gain and 120/300 s
coarsen the 1-hour charts while saving only server memory; 2 s live costs
+39 % knob requests, 2.4× the app payload and 2.3× the live ring for detail
the 2 s view poll already limits. Table in the #249 PR.

**Storage.** `knobStatsStore`: per device a live ring (at most 300 samples =
10 min at 2 s; samples older than 10 min leave it) holding each sample whole,
and a minute ring (at most 1440 buckets; buckets older than 24 h leave it) where
samples of one wall-clock minute fold into one bucket: gauges take the
newest value, `*_min` the lowest, `*_max` the highest, averages and rates the
mean weighted by `period_ms`; a bucket's `t` is its newest report. Each
average and rate weighs only the reports that carried it (each field keeps
its own measured duration, #473/#475/#482: `cpu_percent`, the request
rates, `render_fps` and the `invalidated_*` rates), so a
report without it, such as one sent at basic diagnostics, doesn't dilute
the mean; a field sent as `0` counts. `request_latency_avg_ms` is the mean
per request: it weighs each report by its `req_ok + req_fail`, so a report
with no counted requests adds nothing. `frame_avg_ms` is the mean per frame
the same way, weighed by `fps·period_ms` (no weight without `fps`). Counts become
rates on arrival (`(req_ok+req_fail)·60000/period_ms` per minute,
`inv_areas·1000/period_ms` per second, like `fps`). Both rings grow as
samples arrive (never past their cap) and give their buffer back when it
falls under a quarter full, so a device costs what it reports: about 500 KB
for a day at 30-60 s, 300 KB at 120 s, 150 KB at 300 s, plus 25/47/110 KB at
10/5/2 s while a live session is in the window; the live share is released
as it ages out. These are estimates: the measured 380/245/125 and 20/37/87 KB
plus 88 B a sample (48 B for the two `invalidated_*` rates since cinder#118,
32 B of struct and 16 B of values, and 40 B for the five weights of
#475/#482). Deleting the device drops its series.

**`GET /v1/devices/{id}/stats?range=15m|1h|24h`** (owner token; default
`1h`, anything else 400, unknown id 404). Answer (dashboard wire
conventions: whole-second RFC 3339, `null` for unknown, units in keys):
`device_id`, `diagnostics`, `stats_interval_s` and `live_interval_s` (the
knob's settings, so a client can poll and break lines at the cadence samples
arrive), `range`, `online` (checked in within 150 s),
`last_seen` (last checkin, with or without stats), `live_until` (null when
not live or diagnostics off), `reset_reason` (of the newest sample),
`latest` (newest sample, null before the first) and `points` (ascending,
`[]` when empty). `15m` = minute buckets before the oldest live sample, then
the live samples (5 s while live); `1h` = minute buckets; `24h` = minute
buckets folded into 5-minute buckets (≤ 288 points). A point:
`t`, `uptime_sec`, `rssi_dbm`, `cpu_percent` (array), `heap_internal_free_bytes`,
`heap_internal_min_bytes`, `heap_internal_largest_bytes`, `psram_free_bytes`,
`psram_min_bytes`, `psram_largest_bytes`, `temp_c`, `requests_per_min`,
`request_failures_per_min`, `request_latency_avg_ms`, `request_latency_max_ms`,
`render_fps`, `frame_avg_ms`, `frame_max_ms`, `invalidated_areas_per_sec`,
`invalidated_px_per_sec` (null from firmware that doesn't send `inv_areas`/`inv_px`),
`brightness_level` (Ember's
0–255 brightness at the checkin). Goldens: `knob_stats.json`,
`knob_stats_empty.json` in `cmd/ember/testdata/dashboard` (EmberKit decodes
them too).

**`POST /v1/devices/{id}/stats/live`** (owner token): body optional
`{"seconds":N}`, N 0..600, default 180; 0 stops live mode. Sets the deadline
to now + N (not extended: each call replaces it) and answers
`{"live_until":RFC3339|null}`. 409 while the device's diagnostics are
`off`. In memory only.

**App.** Settings › Devices › Knob › Behavior has the Diagnostics picker
(Off/Basic/Full) and, when the server has them, "Send stats every"
(30 s-5 min) and "Live stats every" (2-10 s) pickers, disabled while
diagnostics are off, with a footnote on the trade-off (#249); Status ›
Diagnostics has "Show Hardware". See "Hardware pages" below. The knob page's lines break
after three missed reports at the range's spacing or the knob's
`stats_interval_s`, whichever is longer. The gap uses the current interval
for every point, so after a change from 300 s to 30 s the older 5-minute
points draw as dots until they leave the range.

**Knob pane layout (#318).** The knob's Settings pages are collapsible
groups: Status (Connection, Address, Last check-in, Settings, USB, Set Up /
Forget), Firmware & updates (the firmware row, update mode and the stored
images inline; collapsed, its header shows the running version and "Update
to X available", progress or the failure, and it expands when an update
starts or fails), Diagnostics (Wi-Fi channel and access point,
Reconnects, Last restart, Boots, Display link) and Crash dumps; Display;
Behavior and Advanced. Each fact has one owner: Wi-Fi signal, memory,
uptime and CPU live on Hardware (charts and the Now card); firmware,
address and restart history live on Status, which links to Hardware.
With diagnostics off, Hardware still shows a Now card from the last
check-in (signal, free memory and largest block, uptime, the lowest free
memory and weakest signal since start; `KnobNowReadout`), because cinder
sends those in every check-in. A
collapsed group is stored in `settings.collapsed` (UserDefaults, comma-
separated `SettingsGroup` ids, the same shape as `settings.expanded`).

### Knob demo mode — `cmd/ember/demo*.go` (#457)

A developer tool for checking the knob's pages and animations on the real
knob without waiting for real activity. Ember.app's Devices › Demo page
(behind Settings › General › "Show developer tools") picks scenes; the
server builds each one and relays it through the view the knob already
polls. Contract: DEVICE-PROTOCOL.md "Demo mode".

- **State** lives in memory only (`demoRegistry` on `App`), one run per
  knob: the scene queue, the current scene's id, `hold`, `pending` or
  `playing`. Newest POST wins. Nothing touches the store.
- **Scenes** are a fixed catalog (`demoCatalog`): name, group, title, the
  knob page, an optional act, `offline`, and a builder for a whole fake view
  from presets. Times are relative to the moment the scene became available
  (whole seconds), not to each request, so the demo view keeps one ETag
  while it plays and a long poll doesn't spin.
- **Relay.** `knobView` adds `demo` as the last key for a knob whose caps
  list `demo`; the fake view copies the real `v`, `epoch`, `config_version`
  and `brightness`. Setting or stopping a demo and a report that changes
  state notify `topicDemo`, which wakes waiting view polls.
- **Reports** ride the view request (`demo_id`, `demo_state`), read before
  the long poll. `done` for the current id advances the queue under a new
  id, so the reply already carries the next scene. Expiry is lazy, applied
  on each read: `pending` 10 s → `unanswered` (kept for the status 60 s,
  then gone), `playing` with no report for 60 s → gone (a knob that
  rebooted mid-scene), a held scene 60 s after its first `playing` → gone
  (the knob caps it too; a knob that keeps re-reporting `playing` would
  otherwise hold it forever). A late `playing` or `done` for a scene the view
  already served (`deliverKnobView`: a 200 or 304 reply carried it, not
  just a view build such as the long poll's pre-check; it is marked before
  the write, so a report racing the reply still counts, and unmarked if the
  body write returns an error; best effort, since `net/http` and the OS
  send buffer absorb the reply, so a gone client may fail only at the
  flush after the handler returns, or not at all), up to 5 s past the
  10 s cutoff, still counts (`playing`
  revives it, `done` advances the queue): the knob took it at 9.9 s and its
  report rode the next request.
- **Reboot.** A checkin that reads as a reboot (`knobRebooted`: a higher
  `diag.boots` when both checkins carry it; otherwise, including a lower
  or one-sided `boots`, a lower `uptime_s` that fits the time since the
  previous checkin, both known) or a view request with no report while the scene is `playing` and
  the last report is 3 s old (`demoReportGap`) turns the run into
  `restarted` (queue cut to the dropped scene, nothing served, reports
  ignored, gone 60 s later) and notifies `topicDemo`. Without it a crash in
  a scene looped: the rebooted knob forgot the id and played the scene
  again (#460). The view rule is what catches the first post-boot poll,
  which cinder sends before its first checkin; the 3 s gap keeps a
  report-free request sent before playback and delivered late from ending
  a healthy run; it only narrows that race, and the checkin that follows
  the poll is the backstop for a `playing` run that reboots in under 3 s.
  The checkin path drops a run only if it started before the boot (arrival
  minus `uptime_s`; for a `pending` run 2 s earlier, `bootEstimateSlack`,
  since the estimate runs late by the truncated uptime and the transit and
  the booted knob could have taken that run; a `playing` run was reported
  before the reboot, so it gets no slack; no drop when `uptime_s` is
  unknown) and is the run whose generation the
  handler read before the checkin, so a demo set after the knob came back,
  or set while the checkin was in flight, survives. An `unanswered` run
  stays `unanswered`. A checkin that `knobCheckinStale` reads as an older
  one arriving late (within `staleCheckinWindow`, 1 min; an uptime lower
  than the stored one on the same or unknown boot, or, when either side
  lacks `diag.boots`, grown more than the time since plus 10 s) is
  answered but not stored: the next comparison and the device list keep the newer record and caps, its crash isn't reported
  again, and it adds no stats sample and no OTA step.
- **App.** `KnobDemoModel.follow` reads the status every second while the
  run is `pending` or `playing`, keeps the 1 s pace for the first five
  `unanswered` reads (the server can still revive the run for 5 s past the
  cutoff), and then reads every 5 s while an `unanswered` or `restarted`
  warning shows, until the server reports `idle` (60 s later),
  so the line fades with it. Stop stays enabled for those warnings and
  dismisses them (`DELETE …/demo`).
- **Cover.** The now-playing scenes use a built-in 480 px orange-to-violet
  gradient, prepared once like uploaded art (`nowplaying.Prepare`). The art
  route serves it whenever `v` equals its `art_version` or `cover_version`,
  so the knob fetches it exactly like real art. Its `Hash` is a fixed
  string, not the hash of the PNG: Go releases encode the same pixels to
  different PNG bytes, which moved the goldens' `art_version` between the
  local toolchain and CI.

### Knob firmware updates — `cmd/ember/firmware*.go`, `devices_ota*.go` (#225)

Over-the-air updates for the cinder knob (firmware side: tarakanof/cinder#10;
design: `Personal/cinder/Specs/2026-10-06-knob-ota-design.md`). Ember
hosts the images, decides when to offer one, serves it, and tracks the
knob's progress; the knob downloads, verifies the pinned SHA-256, installs
when no Pomodoro runs, and confirms or rolls back by itself. Ember never
contacts GitHub: images arrive only through `POST /v1/firmware` (cinder's
`publish.sh`/`release.sh` or Ember.app), so no token lives on the server.

**Store (`firmware.go`).** `<data dir>/firmware/cinder-knob/<version>/`
holds `cinder.bin`, an optional `cinder.elf` and `meta.json` (the
`image` fields plus `elf_sha256`). Each file is written to a temp file,
fsynced and renamed, `meta.json` last, so its presence means stored; at
boot the index is rebuilt from the `meta.json` files, and version dirs
without one (or without `cinder.bin`), `.*.tmp-*` leftovers and an ELF
that `meta.json` does not record are removed. Upload checks, all from
the bytes: 64 KiB-4 MiB, byte 0 `0xE9`, `chip_id` (offset 12, u16 LE)
9 = ESP32-S3, `esp_app_desc_t` magic `0xABCD5432` at 32, `version` (48,
strict semver, no build metadata), `project_name` (80) `cinder`,
`idf_ver` (144), `app_elf_sha256` (176; `build` = its first 4 bytes as
hex, the knob's `fw_build`). A new version is written into a
`.tmp-<version>-*` directory and renamed into place; a replace moves the
old directory aside to `.old-<version>-*` first and deletes it after the
rename, so a failed write keeps the old version, and a crash between the
two renames is undone at boot (invalid version dirs are swept first,
then an `.old-` directory whose version is missing is moved back and
loaded; while an invalid `<version>/` or a file of that name is in the
way, the aside is kept and boot reports it). If the new directory cannot
be moved in, the old one is moved back; if that fails too, the version
leaves the index until boot restores it. Once the new directory is in
place the replace is done (index, SHA and unblock follow it, also on a
retry after such a hidden version) even if deleting the `.old-` copy
fails: the upload answers 201, and the leftover is retried like the
`.tmp-` ones below. A DELETE, an eviction and an upload of a version the
index does not hold retire the version's copies by renaming each to a
`.tmp-<version>-*` name, `.old-` copies first and `<version>/` last, and
stop at the first rename that fails. A failed rename has touched
nothing: an indexed version stays indexed, whole and blocked, and the
call can be retried. Once `<version>/` is renamed away the version is
gone (index entry dropped, block cleared) and the retired dirs are
deleted best effort; a leftover (also a stuck `.old-` copy, a failed
upload's temp dir and one boot could not remove) is logged once, retried
each time the store next deletes a dir, and swept at boot, which never
restores a `.tmp-` dir. Until it is gone the doctor's `bytes` counts it,
since it still takes disk. So the index never holds a half-removed
version, and a failure never leaves the pre-replace bytes as the only
copy for boot to restore without their block. A dev-seed
build (cinder's local default with `sdkconfig.secrets`) has the Wi-Fi
password and the master token compiled in, so an image or ELF
containing the server's own `EMBER_TOKEN` or the marker
`CINDER-DEV-SEED-BUILD` is refused (`dev_seed_build`); the ELF is
streamed to a temp file through SHA-256 and the same scanner (a needle
split across reads is still found). The ELF is stored only when its
SHA-256 equals `app_elf_sha256` (`elf_mismatch`). Same version, same bytes:
200 and nothing changes; other bytes: 409 unless `?replace=1` and no knob
targets it. A target parked after its version failed or rolled back
(`version` = target, phase `failed`/`rolled_back`) does not count as in
use for a replace or a DELETE (#330): otherwise the failed build could
not be removed until the user retried or retargeted. Pruning after each upload keeps the 5 newest versions (semver
order), every version that is a knob's target (parked ones too) or active offer or a knob's
running `fw`, and the image just written. The store lock is taken before
the registry lock (the in-use checks, which bump `fwGen`, and `otaRetire`,
which prunes `blocked` and clears removed versions), never the other
way round.

**Record (`devices_ota.go`).** `deviceRecord.ota` in the registry blob:
`mode` (`manual` = "Ask first", the default, or `auto`), `target`, a
one-shot `retry`, `blocked` (versions that failed or rolled back, at most
16; auto mode and `available` skip them; a version is dropped from every
knob's list when it is replaced with `?replace=1`, evicted by retention or
deleted (204, or 404 for a stale entry, whose leftover `<version>` or
`.old-<version>-*` directories are removed first; 409 for a knob's
target either way, unless that target is parked after the version failed
or rolled back), only once `<version>/` is renamed away, and under the
store lock so no upload or block can slip in between), `phase`, `error`,
`from`, and the offered `version`/`build`/`size`/`auto`, the `attempt`
id and `started_at`/`finished_at`. It is
written only when it changes (never per progress tick): the checkin
computes the step on a copy under the registry lock and persists only a
difference. Progress (`bytes`) and `waiting_for` live in memory.
`last_checkin` keeps the knob's `fw_build` and `ota` report as sent.
A DELETE or a retention eviction (not a replace, whose version still
exists) also retires the version from every knob outside an active
attempt: a `target` naming it is cleared with `retry` (bumping the epoch),
and a record whose `version` it is, unless `done`, is reset (`version`,
`phase`, `error` and the attempt's details cleared; `mode`, `blocked`, the
`attempt` counter and any other target stay). Without it a parked target
or a dismissed attempt would point at a missing image: no offer, auto
mode stuck behind it, a Retry waiting for a checkin forever.

**Firmware generation (#330).** The registry keeps an in-memory
`fwGen`. Every removal bumps it under the registry lock in the same step
as its in-use check (`otaTargets` for a DELETE or replace,
`holdUnlessKept` for a guarded DELETE and for each version retention
evicts), while the store lock is still held, so the check, the bump, the
purge and the retire are one step to anyone who reads the store.
`holdUnlessKept` also keeps the registry locked until the version's
copies are renamed out of their live paths, so every registry call
(checkins, OTA reads, clock config) waits for those renames, and a
checkin reporting the version can't land between the check and the
retire. The `RemoveAll` of the retired copies runs after the hold is
released, still under the store lock. The store releases the hold with
`defer`, before `unblock` runs, so a panicking purge cannot wedge later
checkins. A checkin reads `fwGen` and the epoch before it reads the
store, and its registry update offers or clears only if `fwGen`, the
target, the mode and the attempt are unchanged; otherwise it records the
knob's result and waits for the next checkin. Its dangling-target clear
(a target whose image is gone, outside an active attempt) also needs an
unchanged epoch, so a target re-selected after a re-upload survives. Every
`PUT …/ota`, including a mode-only change or `target:null`, commits only
if `fwGen` is unchanged since it read the record, retrying up to 3 times
(then 409 `firmware_changed`). One with a target or a retry first checks
the image for the version it would set (the explicit target, else the
current target, else the last attempt's version) against the store, and
also needs that version unchanged at commit. So a Retry can't revive a version a DELETE or an
eviction has claimed: a Retry before the claim makes the version held
(DELETE 409), and one after it finds the image gone (400). Both orders
are pinned by tests through `otaReadHook` and the store's `rename` seam, as
is an auto checkin that commits while a plain DELETE is purging its
candidate, or that waits out an eviction's hold (no offer either way).

**Checkin.** The knob adds `fw_build` (8 hex; ignored when malformed) and
`ota` `{"image":"valid|pending_verify|new|undefined","last":{"attempt","error","result":"ok|failed|rolled_back","version"},"phase":"idle|waiting|rebooting","rollback":bool,"slot":0|1}`
(dropped and logged like `wifi` when invalid). `diag.crash` gains `elf`
(8 hex, the crashed app's ELF prefix), copied into the core dump sidecar
so `GET /v1/firmware/by-build/{elf}/elf` finds the ELF that decodes it.
Each checkin first applies the knob's result to an active offer:
`fw_build` = offered build with `pending_verify` → `verifying`, with any
other image state → `done` (target cleared); `ota.phase` `waiting` →
`installing`, `rebooting` → `restarting`; `last` with the current
`attempt` and the offered version and `failed`/`rolled_back` → that
phase, its `error`, and the version added to `blocked`, in any active
phase, `offered` included (a failure before any byte was served, or the
knob's own guards refusing the offer). `error` is the knob's short code
(`^[a-z0-9_]{1,24}$`, stored as sent), among them `reset_waiting` (the
knob restarted with a verified image still waiting to install) and
`health_display`, `health_render`, `health_heap`, `health_stack` (rolled
back: the new image failed that health check before it was marked valid,
cinder#1). The app turns known codes into a sentence
(`KnobOTAError.label`) and shows others as sent. Every new offer (another
version, or a Retry) takes the next `attempt` id, a per-device counter
kept in the record, always one past the larger of the record's counter
and the `ota.last.attempt` in the checkin, so a re-paired knob or a
registry restored from an older backup never gets an id the knob still
holds; repeats of the same offer keep it. The knob stores the id with
its attempt and echoes it in `ota.last.attempt`, so a `last` it keeps in
NVS from an earlier attempt (it does until a new attempt starts) never
fails or finishes the current one. When the knob declines an offer for a
lasting reason (its bad-image guard, the per-boot attempt cap) it reports
`last` `{"attempt","result":"failed","error":"refused","version"}`, which
Ember applies like any failure. An offer that the knob has been sent for
30 minutes of checkins (manual) or 24 hours (automatic: it waits for 10 min
without input, which can take hours) without starting it becomes `failed`
with `error` `not_started`; an automatic one is also blocked. The clock is
in memory, keyed by attempt, and restarts with a new attempt and whenever
a checkin withholds the offer (Pomodoro, core dump, knob not ready).
Then it decides the offer. The candidate is `target` (manual: any stored
version, downgrades included; none after its own failure until a Retry or
a new target) or, with no target in auto mode, the newest `release` image
above the running `fw` that is not blocked. The answer carries
`"ota":{"attempt","auto","build","retry","sha256","size","url":"/v1/devices/self/firmware/<v>","version"}`
(keys sorted) only when the knob reported `rollback` true (the bootloader
can roll back; without it the first OTA would have no safety net),
`image` `valid`, `phase` idle, a `fw_build` other than the candidate's,
the Pomodoro engine is neither running nor paused (idle or parked is
fine), and the same answer has no `coredump_wanted` (a dump uploaded after
the update would carry the new `fw`). Pomodoro and core dump holds show as
`waiting_for`; an auto offer the knob has not started shows `idle_input`
(it waits for 10 min without input). Setting a target, a mode or a retry
bumps the devices epoch, so the knob checks in within one view poll;
checkins never move it.

**Download.** `GET /v1/devices/self/firmware/{version}` (device token,
per-device limiter: burst 6, one per 10 s) answers 404 for an unknown
version and 409 unless the version is this device's offer in phase
`offered` or `downloading`, so a device token reads one image and only
during its own update. The first request moves the phase to
`downloading` and resets the progress (a Retry of the same version starts
from zero). `http.ServeContent` serves the file with `ETag:
"<sha256>"` and `Cache-Control: no-store`, so `Range` and `If-Range`
resume work (a stale `If-Range` gets the whole file, a `Range` past the end 416). The write deadline
is raised to 10 min through `http.NewResponseController` (the server's
`WriteTimeout` is 30 s and a slow knob stalls the writer through TCP
back-pressure). A counting `ResponseWriter` (unwrappable, so the
controller reaches the connection) records range start + bytes written
as the progress. Once every byte has gone out and the knob has not
checked in since, the owner view shows `restarting`: the knob verifies,
switches the boot slot and restarts right after the stream ends.

**Owner view.** `GET /v1/devices/{id}/ota` answers
`{"mode","target","version","phase","progress_pct","bytes","size","from","error","started_at","finished_at","blocked","running":{"fw","build","slot","image","rollback"},"available","waiting_for"}`
(dashboard style: whole-second RFC 3339, null when unknown; `available` =
the newest stored version above the running one, any channel, not in
`blocked`; `version` =
the current or last attempt's version, also for automatic ones). `PUT`
merges `mode`, `target` (`null` clears it and cancels an offer that has
not started downloading; after a `failed` or `rolled_back` attempt it also
dismisses the failure: phase `idle`, `error` cleared, `blocked` unchanged
(a manual `not_started` timeout never blocked its version), with or
without a target; clearing a target is 409 `ota_in_progress` while
`downloading`, since the knob would carry on with the download anyway) and `retry` (re-offers the last version once with
`retry:true`, which clears the knob's own bad-image guard, and unblocks
it). Setting the target to the version that just failed or rolled back,
or to any version in `blocked`, is a retry too (unblocked, `retry:true`):
without it the knob would ignore its own bad image forever. A successful
install (`done`) also removes the version from `blocked`. A target or retry is 409 `no_rollback_bootloader` while the last
checkin has no `ota.rollback`; a new target, clearing the target or a
retry is 409 `ota_in_progress` while the phase is `installing`,
`restarting` or `verifying` (the knob holds the staged or unconfirmed
image, and its result would be lost); an unknown version is 400, and so
is a retry whose version Ember no longer stores. Goldens:
`knob_ota_idle.json`, `knob_ota_downloading.json`,
`knob_ota_rolled_back.json`, `firmware_list.json` in
`cmd/ember/testdata/dashboard` (EmberKit decodes them).

**Doctor.** `firmware`: the dir is writable, image count, bytes on disk
(OK with "no data dir" when storage is off). `devices` warns when a knob
reports `ota.image:"new"`: it booted an OTA image through a bootloader
without rollback, so the one-time USB flash was skipped.

**App.** Settings › Devices › Knob › Status, group "Firmware & updates":
"Firmware" shows `fw (build)`,
"Update to X" when `available` is set (disabled without a rollback
bootloader, with the USB note; hidden after a failure when it would name
the failed version, where Retry does the same), then a progress bar and
the phase line (with Cancel Update, `PUT …/ota {"target":null}`, while a
target waits in `idle` or `offered`), or the failure line (naming the attempt's `version`),
Retry and Dismiss (`PUT …/ota {"target":null}`: it clears a parked
target and the failure, so the line and the collapsed "Update to X failed"
go away; `blocked` is unchanged). It polls `GET …/ota` every second only while the phase is
`downloading`, `installing` or `restarting` and the pane is open; the
pane's 15 s reload covers the rest. Deleting the version a knob runs is
allowed (the knob keeps both images in flash), but the confirmation says
that its ELF, needed to decode that build's crash dumps, goes too. The same
group has Ask first / Automatic, then the images stored on Ember inline
(no sheet): each row is the version with its badges (filled, tinted
capsules: On the knob green, Latest blue on the highest stored version,
Installing orange on the target while it downloads, installs, restarts or
is verified (nothing while it waits or is offered), Failed red on the
`attemptVersion` of a failed or rolled-back update, No ELF yellow), size,
date and ESP-IDF version, then Install (any stored build except the one the knob
runs, matched by `build`, downgrades included; `PUT …/ota {"target"}`;
disabled with the USB note without a rollback bootloader; a refused
install reloads the status and names the reason), a Test/Release channel
menu that `PATCH`es either way (Test to Release asks first and says what
Automatic would do with it), Download ELF and Delete…, which asks in a
dialog naming the version (a 404 counts as
already deleted; the list reloads either way; a refusal shows under that
row, a 409 as "a knob is updating to this version or waiting to", with the
way out). Upload… sits under the list (upload
`cinder.bin` plus a `cinder.elf` or `<name>.elf` next to it, channel Test
by default, upload progress from the `URLSession` upload task).
Delete Old Builds… under it deletes, one `DELETE …?keep=protected` at a
time, every image `KnobFirmwareImage.oldBuilds` picks: all but the selected
knob's build and its `running.fw` version, the `fw` of every knob in
`/v1/devices`, the newest Release, the newest Test if it is newer than that
Release (semver order, the server's `compareSemver`), `target`, a
downloading or installing update's `version` and the offered update.
Without a `running` report it picks nothing. The server enforces the same
rule (`firmwareNewestKept` plus `holdUnlessKept`) under the store lock and
answers 409 `firmware_kept_in_use` or `firmware_kept_newest` (the
registry stays locked from the keep check until the retire renames end,
so a check-in reporting that version waits), which the row shows as a grey "Kept" note,
not an error; an update target's 409 counts as kept too. So the client's
rule is only a pre-filter, and races with polls, channel changes or other
knobs cost at most a "Kept" answer. On confirm,
`KnobOTAModel.deleteOldBuilds` reads `/version` and refuses to run unless
`features` lists `firmware_delete_keep` (an older server would ignore the
parameter), then rereads the OTA status, the image list and `/v1/devices`
(any failure aborts with nothing deleted), and re-applies the rule before
each `DELETE`. `configure` bumps a generation; every load and the batch
check it before writing, and each loader drops an answer older than the
latest one it started, so a switch of server or knob never lets old answers
land. Each failure shows on its row; the list reloads once. A row's own
Delete… stays unguarded and can remove the running build after its warning.
Uploads and ELF downloads use the `transfer` request budget (60 s per
request, 10 min per resource). The Crash rows get "Download ELF…" when
Ember holds the ELF for the crash's build.

### Clock stats — `cmd/ember/clock_stats.go` (#246)

The clock's history for Settings › Devices › Clock › Hardware. Every fresh
probe of the clock (`probeClockHealth`, the 30 s-cached `GET /api/v1/device`
behind `/v1/clock/health` and the brightness tick) becomes a sample. The
device watch reads the clock through the same probe (`probeDevice`, cache
up to 15 s old; reboot detection uses its `uptime_sec` and `checked_at`), so
the clock sees one `GET /api/v1/device` per 30 s; with
`awtrix.auto_rediscover` off, `StartClockSampler` takes the watch's place.
Samples and the IP belong to one clock URL: when the effective URL changes
the store starts over, so two devices never share a chart. Memory only, nothing written to
the database; a restart loses them. Storage is the knob's: `sampleSeries`
(`stats_series.go`), a live ring of 20 samples (10 min at 30 s) and a
minute ring of 1440 buckets. A bucket keeps the newest reading of each
gauge, the lowest `min_free_heap_bytes`, the summed publish counts, and is
`reachable` if any probe in it was.

**`GET /v1/clock/stats?range=15m|1h|24h`** (owner token; default `1h`,
anything else 400; `Cache-Control: no-store`): `range`, `configured` (a clock
address is set), `reachable` (newest probe; null before the first),
`checked_at`, `ip_address` (the clock's own report; deliberately not in the
open `/v1/clock/health`), `sample_interval_sec` (30), `latest` (newest sample
that reached the clock, so an offline page still shows the last readings)
and `points` (ascending; `15m` = minute buckets before the oldest live
sample, then 30 s samples; `1h` minute buckets; `24h` 5-minute buckets). A
point: `t`, `reachable`, `rssi_dbm`, `free_heap_bytes`, `min_free_heap_bytes`,
`temperature_c`, `humidity_percent`, `light_lux`, `battery_percent`, `publish_ok`,
`publish_fail` (publishes since the previous sample). An unreachable probe is
a point with `reachable:false` and null readings. Goldens: `clock_stats.json`,
`clock_stats_empty.json`.

### Hardware pages — `macos/Ember/Hardware/` (#246)

Device health lives in Settings, not the Dashboard: Settings › Devices ›
{Clock, Knob} › Hardware (`HardwarePage.health`, stored
`device/<id>/hardware/health`, between Status and Display). Both pages share one system: a header with the device's status
(Online / Live / Offline, last report …) and a segmented 15 Minutes / 1 Hour
/ 24 Hours picker (`HardwareRange`); a wide "Now" card (`HardwareNowCard`) of
270° ring gauges over a grid of facts; `HardwareChartCard`s in two columns.
Clock: gauges Wi-Fi, memory, temperature, humidity, light, battery (sensors
the clock never reported are left out); facts uptime, firmware (+ update),
current app, IP address, last restart, delivered over the selected range
(never rounded up to 100 % while any failed: "99.6 %"); charts Wi-Fi, memory,
temperature, humidity, light level, and publishing as stacked bars per
minute (per hour over 24 h) headed by the failure count. Only the clock's own
readings: the clock doesn't report its display brightness. Knob: as in #241
(CPU per core on a fixed 0–100 % axis, memory, PSRAM, temperature, Wi-Fi,
requests, latency, rendering), plus IP address and Ember brightness (the
`/v1/display/brightness` level at each checkin, `brightness_level` in its
stats, which the knob shows while it follows Ember);
a button opens Behavior, where the diagnostics picker stays. States: loading
(redacted fake), server too old, no clock / diagnostics off, waiting for the
first reading, offline (last data, Now card muted and captioned "As of
<time>"), stale chip.

HIG rules (from the #241 review): a gauge's arc is one colour picked by the
reading's thresholds (green / yellow / orange; light has none and stays
neutral), a fuller arc means more of the quantity; warnings add a triangle
icon, never colour alone. Charts use a categorical palette with no good/bad
meaning (blue, orange, purple, grey), dashed lines for low-water marks,
slowest and failures, a legend that draws each mark; `.linear` lines
(`.stepEnd` for integer dBm and brightness below 24 h), broken at gaps of 3
missed points; an area fill only on charts with a zero baseline; bytes in
decimal units fixed per chart; hover callout; `AXChartDescriptor` audio
graphs on every chart. Polling runs only while the page is on screen and its
window visible (occlusion): `KnobStatsModel` every 5 s / 15 s / 60 s with
live mode at 15 min (180 s, renewed every 60 s, `seconds:0` on leaving the
range or the page), `ClockStatsModel` every 15 s / 30 s / 60 s.
`KnobStatsFake` and `ClockStatsFake` drive previews and the snapshot render
(Debug build: `EMBER_HARDWARE_SNAPSHOTS=<dir> Ember.app/Contents/MacOS/Ember`
writes PNGs of both pages and of the Knob Status, Display and Behavior
settings pages (fixture JSON through an in-process `URLProtocol`), light
and dark, and quits; it skips server,
producers and USB; build it with another `PRODUCT_BUNDLE_IDENTIFIER`).

### Config load and `/admin/reload`

Resolve order: the `-config` flag, `CONFIG_PATH`, `./config.json` if present,
else defaults only. `-config` works before or after the `doctor` subcommand
(`ember -config X doctor`); given twice, the later one wins. Any other flag
before a subcommand, and any stray argument (`ember -- doctor`), exits 2
rather than being dropped; the message names the fix per case (`-print-config`
is a server flag and runs alone, `-h` points at both helps, doctor flags go
after `doctor`, an unknown one is named as such, `version` and `healthcheck`
take none). `-v`/`--version` prints the version unless another subcommand
follows (`ember -v`, `ember -v version`, `ember -offline -v`); before one
(`ember -v doctor`) it exits 2 and says to run the two separately. A valued doctor flag before `doctor` takes
the next token as its value, as Go's `flag` does (`ember -server-url X doctor`
exits 2 with `ember doctor -server-url X`; `-server-url doctor` reads `doctor`
as the URL and falls through to the server's flag error).
A bool flag's value must be attached
(`-print-config=false`): `-print-config false` reads `false` as a stray argument
and exits 2. Failures wrap the sentinels `ErrConfigRead`,
`ErrConfigParse` and `ErrConfigValidate`. Unknown JSON fields are a parse error
(`DisallowUnknownFields`), so deprecated keys (`pulse_style`,
`heartbeat_seconds`, `refresh_seconds`, `notify_on_waiting`) stay decodable and
`warnDeprecatedConfig` logs them. `sanitizeConfigBaseline` drops or replaces
values that fail the SSRF and path validators (`validDeviceURL`, the weather
icon-id pattern, e.g. a hand-edited path-traversal id) rather than crashing
startup, and reload runs the same repair so a hand-edited file cannot bypass the
guard by arriving through reload; `validateConfig` stays as defence in depth. The
required-field check is reload-specific: it runs on the raw parsed config, because
`applyDefaults` would fill an explicit empty `awtrix.http_base_url` with the
fallback URL and hide the misconfiguration, so reload answers 422. At startup
`loadConfig` fills that default first, so an empty value starts on the fallback.

Reload answers: 412 when the server started from defaults (no file), 500 on a
read error, 400 on parse, 422 on validation, 409 when a non-reloadable leaf
changed (the HTTP listener is bound once and admin auth tokens are wired into
long-lived structures; the operator must restart), else 200
`{reloaded, changed_fields}`. `diffConfig` walks `Config` by reflection using
json tags, so a new field is diffed automatically; struct fields recurse and
others compare with `reflect.DeepEqual`, which distinguishes nil from non-nil
pointers (the "unset vs explicit false" case). The effective `auth.status_token`
is the file's value when set, else the env var named by `auth.status_token_env`
(default `EMBER_TOKEN`); repo policy keeps it env-only and out of committed JSON.
Reload keeps the running token, so it is copied from the running config before
diffing (else every reload 409s), and `formatLeafValue` redacts it in the 409
message anyway. Loading the old config and storing the new one is one `cfgMu`
critical section, so a concurrent settings PUT is not lost. Reload re-syncs the
Pomodoro engine and re-applies persisted overlay settings (the clock config
is carried over in the same critical section as the config swap, like the
clock URL tiers, so a PUT landing before the reapply still sees it migrated),
and starts
`ensureBootPingScript` off the request path because it does device HTTP and the
reply must not wait on an unreachable clock. `adminRequireAuth` is stricter than
`requireAuth`: an empty `EMBER_TOKEN` closes the admin endpoints (they expose
mutation and runtime detail) rather than opening them.

Defaults that encode a decision: toggles that are `*bool` (usage widget,
per-model usage, limit alarm, `AutoRediscover`, meetings and weather flags) mean
nil = default on, so old config blobs keep working. `awtrix.indicators` is
opt-in because the LEDs are shared panel real estate; `awtrix.boot_ping` is off
by default because it puts a script on a device the operator owns. The 300 s
session timeout default tolerates producer heartbeat lapses so an active session
isn't reaped to the idle robot mid-work (it matches the Codex producer's
activity window). `IdleRestoreSeconds` is integer-divided by 60 for the display
DTO (a 90 s file baseline reads as 1 minute; the DTO only allows whole minutes),
and the coordinator reads display knobs live so a change applies on the next
tick. After a reload the coordinator also retunes its dwell ticker to
`display.rotation_dwell_seconds`, because `publish()` reads the live dwell for
its dedupe window and a ticker left on the startup period would drift from it; a
non-positive dwell falls back to 3 s.

TLS: `EMBER_TLS_CERT_FILE` and `EMBER_TLS_KEY_FILE` must both be set (HTTPS) or
neither (HTTP); exactly one is a startup error. The pair is parsed eagerly, so a
malformed or mismatched pair fails startup with a clear error. Expiry, SAN
coverage and trust chain are not validated and are the operator's job.

### Doctor and container healthcheck

`DoctorResult.OK` is false for any Fail or Skipped check; Warn does not flip it,
so a stale meetings feed or the startup window before the first ICS poll cannot
make `/admin/doctor` 503 or `ember doctor` exit 1. Skipped is non-OK because
offline mode is partial by design: automation must treat `OK == false` as failed
or partial and inspect the mode and per-check status. The `clock` check only
warns on a transient miss (the watch loop recovers), and a missing
`capabilities` entry is a Warn (clock dark at startup; the endpoint still
live-fetches). Under `EMBER_CLOCK=off` the `clock`, `awtrix_reachable` and `capabilities` checks are OK with a "disabled" detail (not Skipped, which is non-OK), so a scratch server's doctor still gates on `OK`. The meetings check never prints feed URLs. Offline, failures of
static checks are real failures. The standalone doctor builds a bare stderr
logger so baseline-repair warnings aren't dropped.

`ember healthcheck` defaults to the container loopback URL and flips to https
when `EMBER_TLS_CERT_FILE` is set. Its 2 s client timeout sits 1 s under the
Dockerfile's `HEALTHCHECK --timeout=3s`, so the binary can print a diagnostic
before the daemon kills it. For https targets `EMBER_HEALTHCHECK_CA_FILE` adds a
PEM bundle and `EMBER_HEALTHCHECK_INSECURE=1|true` skips verification (fine on a
trusted LAN).

### Observability and rate limiting

`/metrics` request counters are keyed by the matched mux pattern; an unmatched
route collapses to one `<unmatched>` series so a 404 spammer cannot blow up label
cardinality, and scrapes are not self-counted. Requests rejected by
`requireAuth`/`adminRequireAuth` keep the outer prefix (`/v1/` or `/admin/`), so
per-route 401 counts are a non-feature. Label values escape only backslash,
double quote and newline per the exposition spec (not Go's `%q`, which
Prometheus parsers reject). `ember_publish_retries_total` exists because a push
that succeeds on its second attempt still counts as one ok publish, hiding link
degradation until both attempts fail. The increment helpers are nil-safe so bare
`&App{}` test literals cannot mask failures with a panic.

`IPLimiter` lives on `App` (sweeper lifetime, construction in tests without
goroutine leaks) and re-reads `RateLimitConfig` on every `Allow`, so
`/admin/reload` changes apply coherently. `Retry-After` is
`ceil((1 − tokens)/refill)`, at least 1 s; the sweeper evicts idle buckets every
`IdleEvictSeconds/5`, clamped to 5-60 s. The limiter sits outside auth on both
`/v1/` and `/admin/`.

### Misc server invariants

- **Hidden apps** filter only the device display (rotation and attention lock);
  `/state` is unaffected. `baselineApps` always appear in the menu toggle list so
  a tool can be hidden before it first reports. A persistence failure of the
  hidden set is logged and non-fatal.
- **Activity heartbeats** persist at most one row per session per
  `activityThrottle` window, since producers post every 2-10 s, finer than
  work-hours sessionization needs; a transition into waiting bypasses the
  throttle (subject to a floor) so short prompts still count.
- **Credentials.** `meetingsURLs` (`EMBER_MEETINGS_ICS_URLS`) are never
  serialised, logged or stored; only the count is exposed.
- **Previews** never publish or store state. Boolean query params are explicit
  truthy strings (the menu always sends resolved values); `source_card` and
  `session_bar` default on when absent (`queryBoolDefault`).
- **Env toggles.** `envEnabled` default-on toggles accept `0/false/no/off` (any
  case) to disable.
- **Shutdown.** `main` waits for all workers, including the coordinator's exit
  restore PATCH, bounded by `exitRestoreBudget`, which stays under Docker's
  default 10 s stop grace so the container is never SIGKILLed mid-restore.
  Returning from `main` kills goroutines at once, so without the wait the
  restore never reaches the clock and a last `pomoTick` can write to a closed DB.

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
- **Auth:** bearer token on write endpoints. The effective token is
  `auth.status_token` from the config file when set, else the env var named by
  `auth.status_token_env` (default `EMBER_TOKEN`, which therefore loses to a file
  token); policy is env only, never in committed JSON, argv, URL or logs. `slog.LogValuer` redaction throughout. **Fails closed:**
  an unset `EMBER_TOKEN` rejects every `/v1` write with 401 (same policy as the
  `/admin` surface); the token is compared in constant time, and the per-IP
  rate limiter sits *outside* auth so rejected 401s still consume budget (a
  wrong-token flood is throttled to 429). The unauthenticated device hooks
  (`/hooks/awtrix/{button,boot}`) share the same per-IP limiter; they are
  unauthenticated because the device's callbacks cannot send a bearer token, and
  they only map presses to timer actions or trigger a republish, so the LAN
  blast radius is minimal.
- **Auth surfaces.** Three credentials, each route accepts exactly the ones
  listed (`server_test.go` `TestDeviceTokenScope` is the table):

  | Route | Credential |
  |---|---|
  | `GET /state`, `/healthz`, previews, dashboard reads, `GET /v1/nowplaying/{state,art}` | none |
  | `POST /hooks/plex?key=` | `EMBER_PLEX_WEBHOOK_KEY` in the query (404 when unset) |
  | `POST /hooks/awtrix/{button,boot}` | none (clock callbacks; rate-limited) |
  | `POST /v1/pomodoro/{start,pause,resume,stop,skip}` | `EMBER_TOKEN` **or** a device token |
  | `POST /v1/devices/self/checkin`, `GET /v1/devices/self/{config,view}`, `PUT /v1/devices/self/coredump`, `GET /v1/devices/self/firmware/{version}` | device token only |
  | every other `/v1/*` (incl. `/v1/devices` admin and `/v1/devices/{id}/stats`) | `EMBER_TOKEN` only |
  | `/admin/*` | `EMBER_TOKEN` only |

  Device tokens fail closed with the rest: an unset `EMBER_TOKEN` rejects them
  too. All three authed groups sit behind the same per-IP limiter.
- **Text limits are in characters.** `activity` length is validated in
  characters, not bytes: producer truncation is rune-based, so a multibyte
  activity (Cyrillic, emoji) can exceed 80 bytes while being at most 80
  characters. `compactText` caps labels at 80 runes and backs the cut off so it
  never strands a combining mark, variation selector or skin-tone modifier
  (no U+FFFD in `/state`). Session `Upsert` reads the prior state and writes
  under one lock, so concurrent POSTs for one session never misclassify the
  transition.
- **`/state` `render` names the winner (#208):** `render.source` and
  `render.tool` carry the priority-winning session's host and tool (empty
  strings when no session is active; `source` is blank when several winning-state
  sessions come from different hosts, `tool` when their tools differ, matching the
  aggregate `text`), so thin clients (cinder knob, ESP32)
  need not re-run PickWinning over `sessions[]`.
- **Knob checkin (device token).** `POST /v1/devices/self/checkin` decodes
  non-strict like `/v1/status`:
  `{"fw":"0.5.0","ip":"192.168.0.39","rssi":-58,"heap_internal_free":47104,"heap_internal_largest":31744,"uptime_s":812,"config_version":6}`
  (since cinder 0.9.2 also `link_mhz` 40/80 and `link_fallback`, 0..1000,
  stored on the record's `last_checkin` and shown as "Display link"; since
  cinder 0.9.8 a `wifi` object,
  `{"bssid":"78:45:58:4b:c2:cd","channel":6,"disconnects":3,"last_reason":203,"rssi_min":-83}`,
  every key but `disconnects` left out when the knob does not know it,
  `last_reason` an ESP-IDF `wifi_err_reason_t`, `rssi_min` the lowest RSSI since
  the previous checkin; stored as `last_checkin.wifi` in the same shape and shown
  as the "Wi-Fi" and "Reconnects" rows. An invalid object (channel outside
  0..14, `rssi_min` outside -127..0, `last_reason` outside 0..255, negative
  `disconnects`, a `bssid` not lower-case `aa:bb:cc:dd:ee:ff`, a wrong type) is
  dropped (logged at Info when the device's drop reason changes, Debug when it
  repeats), the rest of the checkin kept; a checkin without one clears it;
  since cinder 0.9.13 (cinder#22, Ember#307) a `diag` object,
  `{"boots":42,"crash":{"pc":"0x4201a2b3","reason":"panic","task":"ember"},"heap_internal_min":71234,"heap_largest_min":30720,"reset_reason":"poweron","stack_free":{"ember":1880,"eye":900,"lvgl":2304}}`,
  every key optional (`devices_diag.go`): `crash` repeats on every checkin
  while a core dump sits in flash (`reason` panic, int_wdt, task_wdt, wdt,
  lvgl_stall, unknown), `reset_reason` is ESP-IDF's reset reason as a short name
  (or `lvgl_stall`),
  `stack_free` maps a task to its free stack in bytes. Stored as
  `last_checkin.diag` plus two server fields carried from the previous
  checkin's diag: `reboots`, the boots increases Ember has seen (a drop in
  `boots`, an erased counter, counts one); `prev_reset_reason`, the reset
  reason before the latest reboot, set only when `boots` rose by exactly one
  (or dropped) since the previous checkin; and `reboots_since_seen`, the jump
  when `boots` rose by more than one, since the reasons of the reboots in
  between are unknown (`prev_reset_reason` is then left out). All three restart
  from zero after a checkin without a valid diag (a single dropped object
  loses them; carrying them across would need a store of its own), and keys of
  those names sent by the knob are ignored. A
  crash logs once at Warn (`knob crash reported`) when it first appears or its
  pc, task, reason or id changes against the previous checkin. Since Ember#311
  `crash` also carries `id` and `size` (`"id":"1a2b3c4d","size":65536`), both
  or neither: the dump's IDF checksum (its last 4 bytes as a little-endian
  u32, which is the CRC-32 IEEE of the bytes before them) as 8 lower-case hex,
  and its length. Dropped like
  `wifi`, with the same Info/Debug logging, when invalid: a negative count, a
  reason not 1..24 of `a-z0-9_`, a `pc` not lower-case `0x` plus 1..8 hex
  digits, a task name not 1..16 printable ASCII characters, an `id` not 8
  lower-case hex, a `size` outside 1..131072, only one of the two, more than 32
  `stack_free` tasks, a wrong type. Since cinder#273 (Ember#722) `diag` may
  carry `"idle":{"state":"dim","input_age_s":125,"wake":"touch"}`: `state`
  `awake`, `dim` or `off`, `input_age_s` the seconds since the knob's idle
  clock last restarted, `wake` what last woke the screen (`touch`, `encoder`,
  `mood`, `demo`, `ota_face`, `ota_trial`, or `""`). Stored as
  `last_checkin.diag.idle`, latest checkin only, always with all three keys (a
  key the knob left out reads as `""` or 0); older firmware omits it and the
  app shows nothing. Strings the server doesn't know are stored and shown as
  sent. `idle` is decoded as a part of its own: a negative `input_age_s`, a
  `state` or `wake` over 32 bytes or a wrong type drops only `idle` (logged
  like `wifi`, `diag.idle dropped`), so crash, boots and the reboot history
  of the rest of `diag` survive. The app shows Crash (only while present),
  Last restart, Boots, Lowest free memory and, with `idle` while the knob is
  online, Screen (hidden when `state` is empty), Last activity (relative to
  the checkin's `seen_at` minus `input_age_s`, so it keeps ticking between
  checkins) and Last wake (only when `wake` is set) rows;
  since Ember#757 a `room` object of sensor readings, every key optional,
  stored as `last_checkin.room` with its `received_at`. Unlike `wifi`, a
  checkin without one keeps the stored block, and a bad value drops only
  that value (DEVICE-PROTOCOL "Room readings"))
  (every field optional; `ip` must parse when present, else the remote address
  is recorded; `fw` ≤32 chars). Answer: `{"config_version":7}` when the
  reported version is current, plus `"config":{…}` when it isn't, plus
  `"new_token":"ekd_…"` while a rotation is open, plus `"diag_live_until"`
  in live mode, plus `"coredump_wanted":"<id>"` or `"coredump_ack":"<id>"`
  when the checkin's `diag.crash` has an `id` (see "Knob core dumps"); an optional `stats` object carries diagnostics (both in
  "Knob diagnostics"). An optional `caps` object (#341,
  `devices_caps.go`) is stored on the record and answered with
  `"caps_ack":true`; invalid caps are dropped (no ack, the knob is treated
  as legacy, the reason kept as `caps_error` and logged at Warn once per
  change), and a checkin without `caps` clears the stored ones, so a
  downgraded knob falls back to the legacy table. The owner's
  device list shows `effective_caps` for every knob. Rules, table and
  matrix: DEVICE-PROTOCOL "Capabilities". `GET
  /v1/devices/self/config` answers `{"config_version":7,"config":{…}}`.
- **Knob core dumps (device token up, owner down, #311).** Upload → server
  stores → checkin acks → knob erases; the knob never erases without an ack
  for the same id. A checkin with `diag.crash.id` gets `coredump_wanted` while
  that id is not stored for this device and `coredump_ack` once it is
  (neither without an id, or when the server has no data dir).
  `PUT /v1/devices/self/coredump?id=<id>` (`knob_coredumps_http.go`) takes the
  whole image as `application/octet-stream`: `http.MaxBytesReader` caps it at
  131072 bytes (413); it is stored only when at least 8 bytes long, the CRC-32
  IEEE of all but the last 4 bytes equals `id` and those 4 bytes read
  little-endian equal `id` (else 400). A CRC over the whole image is useless
  as an id: with IDF's trailing CRC it is always the residue `0x2144df1c`. An
  already stored id answers 204 before the body is read; one upload per device
  runs at a time (409 with `Retry-After: 60`, in-memory flag). Storage
  (`knob_coredumps.go`): `<data dir>/coredumps/<device>/<id>.bin` plus a
  `<id>.json` sidecar (`id`, `size`, `fw` from the last checkin, `received_at`,
  and `reason`/`task`/`pc` when the last checkin's crash had this id), each
  written to a temp file and renamed, the sidecar last (its presence means
  stored). The sidecar also holds `seq`, a per-device insert counter (highest
  stored plus one): the list and pruning order by it, not by `received_at`, so
  a clock stepping back or several uploads in one second can't misorder them.
  The newest 3 are kept, older ones pruned on insert, and the dump just
  written is never pruned. Before each insert, and for every device at
  startup, leftovers are removed: `.*.tmp-*` files from a killed write, a
  `.bin` without its sidecar and a sidecar without its `.bin`. The data dir is the directory of the Pomodoro DB (`ensureStore`).
  The device-exists check and the write run under the store lock, so a knob
  deleted mid-upload leaves no files, and `DELETE /v1/devices/{id}` removes
  the device's directory. Owner or admin client: `GET
  /v1/devices/{id}/coredumps` lists
  `[{"id","size","fw","received_at","reason","task","pc"}]` newest first
  (`[]` when none), `GET /v1/devices/{id}/coredumps/{dump}` serves the bytes
  with `Content-Disposition: attachment;
  filename="<device>-<fw>-<dump>.bin"` (`fw` reduced to `A-Za-z0-9._-`,
  `unknown` when empty), `DELETE` the same path answers 204. A device token
  gets 401 on these, so a knob cannot read any dump, its own included.
  Ember.app lists them in the Knob pane's "Crash dumps" group (the crash the
  last checkin reports, then the stored dumps; each row is the crash, a line
  "Firmware X, uploaded by Y · size · date", where X is the image whose
  `build` matches the dump's `elf` and Y the `fw` that uploaded it, then its
  buttons: "Download Crash Dump…" through `NSSavePanel`, "Download ELF…",
  and "Delete…", confirmed in the row). A delete answering 404 counts as
  success, and the list reloads after every delete (#313, #314). Decoding: RUNBOOK "Decoding a
  knob core dump".
- **Knob firmware updates (#225).** See "Knob firmware updates" below: the
  checkin takes `fw_build` and an `ota` object and may answer with an `ota`
  offer; the knob fetches it from `GET /v1/devices/self/firmware/{version}`.
- **Knob view (device token, #234).** `GET /v1/devices/self/view` is the
  knob's one poll (`devices_view.go`): everything it shows, about 400 B with
  weather, versus four endpoints and ~4 KB before. Typical body (409 B, field
  order fixed):
  `{"v":1,"epoch":1,"config_version":1,"mood":{"waiting":1,"errors":0,"running":1,"done":0,"source":"M4"},"pomo":{"phase":"focus","running":true,"paused":false,"ends_at":1782044100,"planned_sec":1500,"round":0},"weather":{"provider":"open-meteo","cond":"rain","code":"61","temp_c":12.5,"stale":false,"severe":false,"night":false,"sunrise":1782013200,"sunset":1782072900},"brightness":{"level":255,"night":false}}`.
  `mood` is `/state`'s `render` counters and `source` (the winning host),
  then who leads the winning state (#282, `moodLead` in `mood_state.go`):
  `lead` (the source with the most sessions in that state, ties to the
  smaller name, so it does not flip as sessions heartbeat; left out when it
  equals `source`), `hosts`
  (distinct sources in that state; left out at 0 or 1), `lead_color` (first
  valid `source_color` of the lead's sessions, uppercased) and `tool` (the
  lead's tool when its sessions agree), all omitted when empty, so an idle
  view's bytes do not change. When the winning state is `waiting` (#485)
  or `done` (#493) the lead is the newest session in that state instead
  (latest `state_since`, the rule the app and the clock use): `lead` is its
  host, `lead_color` its colour, `tool` its tool, and `lead_project` its
  `project` (omitted when the producer sent none). "+N" on the knob is
  `waiting` − 1 (or `done` − 1), other sessions, not hosts. `Upsert` also
  notifies when this summary moves without the render (a new source colour);
  `pomo` is null with the Pomodoro off, and carries `ends_at` (server Unix
  seconds) while counting down, `remaining_sec` otherwise (paused, parked,
  idle), never both; `weather` is null when disabled or never fetched,
  `night` is the weather state's `Night` (`sunNight`, neighbouring UTC dates
  merged, so a western evening after UTC midnight is still day until its
  sunset; false without a location) and is what the knob should use;
  `sunrise`/`sunset` are informational Unix seconds rounded to 5 min for the
  location's own date (the observation's UTC offset, else the longitude's
  hour; null without a location or in polar day/night), `stale` as in
  `/v1/weather/state`, and `observed_at` (#355) the provider's time for the
  current values (Open-Meteo `current.time`, MET Norway's first timeseries
  `time`) as Unix seconds, left out when the provider sent none (see
  DEVICE-PROTOCOL.md "Weather observation time");
  `brightness` is the read-only `/v1/display/brightness` level and `night`;
  `"quiet":true` follows `App.quietAt` (quiet hours, #343, or off the day
  clock, #729; left out otherwise, so a view outside the window keeps its
  bytes); `day` is the day clock ("Day clock" above), only with the `day`
  page in caps and on.
  `epoch` and `config_version` replace the `/state` epoch header: the knob
  checks in when either moves. Every answer carries `X-Ember-Now` (server
  Unix seconds, also on 304), the anchor for `ends_at`, so
  nothing in the body moves with the clock alone and an unchanged view keeps
  its ETag. `ETag` is a strong FNV-64a of the body; `If-None-Match` (weak
  comparison, lists and `*` accepted) answers **304** with no body. The view
  reads memory only: no store write, no clock probe, no checkin recorded, no
  session marshal. `/state` gets no ETag: its `now` changes every response.
  A long-poll `?wait=` (#235) builds on the same `knobView` body and ETag.
  **Now playing (#226):** only when the device's `pages` has
  `{"id":"nowplaying","on":true}`, the body gains (after `brightness`)
  `"nowplaying":{"state":"playing","source":"plex","title":…,"artist":…,"album":…,"duration_ms":330000,"position_ms":61000,"position_at":<Unix ms>,"art_version":"fb43cf74b67a","album_art":true,"artist_art":true}`
  (`{"state":"none"}` when nothing plays; zero/empty fields omitted). A
  knob without that page gets byte-identical bodies, so `v` stays 1.
  `position_ms` is anchored at `position_at`: extrapolate with
  `X-Ember-Now`; the block moves only on a real change. On an
  `art_version` change fetch `/v1/nowplaying/art?kind=…&v=<art_version>`
  (cacheable with `v`).
  In live mode the body ends with `"diag_live_until":<Unix s>` (see "Knob
  diagnostics").
  **Caps (#341):** for a knob whose checkin reported `caps`, `v` is
  `min(1, caps.view[1])`, and `mood`, `pomo`, `weather` and `nowplaying`
  (the blocks of the `bot`, `pomodoro`, `weather` and `nowplaying` pages)
  are left out unless the page is in `caps.pages` and on in the knob's
  `pages`; a kept `pomo` or `weather` is still `null` when off. A body over
  `caps.limits.view_bytes` (floor 4096) drops the weather detail, then
  `nowplaying`, then `usage`, then `weather`, then `pomo` until it fits, and logs once
  per device if it still doesn't. A knob without `caps` gets the body
  above unchanged, bytes and ETag (`knobView` skips the filter for it).
  **Weather detail (#350):** a knob whose caps list `wx_faces` gets
  `utc_offset`, `now`, `today`, `rain`, `days`, `twilight`, `moon` and
  `aqi` appended to its `weather` block (`devices_view_weather.go`, an
  embedded `*knobWeatherDetail` left nil otherwise, so every other view
  keeps its bytes). It projects `weatherState.faces()` to integers; the
  shapes and rules are in DEVICE-PROTOCOL.md "Weather detail". Building it
  costs ~90 µs (the moon scan), paid only for such knobs and only when the
  `weather` page is in their caps and on (`knobView` decides before
  building, not after `applyCaps` hides the block).
  **Prepared cover (#412):** a knob whose caps list `np_cover` gets
  `"accent":"#RRGGBB"` and `cover_version` after every other key of
  `nowplaying` when the entry has a picture, and fetches `kind=cover` at
  `v=<cover_version>`; DEVICE-PROTOCOL.md "Prepared cover" has the recipe
  and rules. `"cover_light":true` follows when the cover's text band
  (y 280–391, x 63–402) has mean relative luminance ≥ 0.22, measured per
  prepared pixel at prepare time (`nowplaying.Image.CoverLight`,
  cinder#168; the threshold's derivation is in DEVICE-PROTOCOL). Every other view keeps its bytes.
  **Usage (#535):** a knob whose caps list `usage` gets a `usage` block
  after `nowplaying` (`devices_view_usage.go`): the accounts of "Usage per
  account" in display order, hidden ones and ones with no snapshot
  left out (after a restart an account has its last stored snapshot,
  stale), at most 8, the block left out when none
  is left. Shape and rules in DEVICE-PROTOCOL.md "Usage". Fresh accounts
  carry no time that moves on a repost, so the ETag moves only when a
  value does; going stale is caught by the 5 s recheck.
- **Knob shown page (#609).** A knob with the `shown` caps feature adds
  `?shown=<page>[:<face>]` to its view polls; `devices_shown.go` keeps the
  last good value per device in memory (`knobShownStore`, not persisted)
  and `GET /v1/devices` shows it as `shown` for 50 s after the last poll
  that carried it. A malformed value is ignored and logged once; the view
  bytes don't depend on it. Shape, freshness and the knob's 300 ms
  debounce in DEVICE-PROTOCOL.md "Shown page".
- **Knob view long-poll (#235).** `?wait=N` (whole seconds, capped at 25,
  `devices_view_wait.go`) with `If-None-Match` holds the request while the
  view still matches the tag: 200 with the new body as soon as it changes,
  else 304 with the same ETag when the wait ends. Without `wait`, without a
  tag, with `If-None-Match: *` (matches any view: always 304), or when the
  tag is already stale it answers at once as above, before any waiter slot
  is taken (so a stale tag gets its 200 even at the cap); a malformed
  `wait` is 400. A reverse proxy in front needs a read timeout ≥ 35 s
  (RUNBOOK). Every view answer carries `X-Ember-View-Wait: 25`
  so a client long-polls only a server that advertises it (an older one
  ignores the parameter and would answer at once, so a client that sent it
  blindly would spin). `X-Ember-Now` is the time the answer leaves. The 25 s
  cap stays under the server's 30 s Read/WriteTimeout and 120 s
  IdleTimeout; the handler still moves both conn deadlines to wait + 5 s
  (`http.ResponseController`), else a slow write or net/http's background
  read would cut it off. Waiters: 2 per device (one plus a reconnect the
  server has not yet seen close), 32 in total; over a cap → 429 +
  `Retry-After: 2`. A closed client cancels the request context and frees
  its slot at once (no goroutine left); `shutdown` closes the broadcaster
  first so `server.Shutdown` does not wait out open polls (they answer 304).
  A device deleted mid-wait gets 401, as the auth check would give.
  Metrics: `ember_knob_view_waiters` (gauge), `ember_knob_view_longpoll_total{result=change|timeout|shutdown|gone|busy}`.
- **Change broadcaster (`changes.go`).** `changeBroadcaster` is a
  `chan struct{}` closed and swapped on every `notify(topic)`. A waiter
  subscribes *before* it reads the state and then blocks on that channel,
  so a change between the read and the block is never lost. Sources:
  sessions (upsert only when the `/state` render moves, so heartbeats and
  statusline ticks stay silent; delete, clear, reap), Pomodoro (`pomoChanged`: every
  action, button, phase end, settings), weather (new observation),
  brightness (the served level/source/night moved), now playing
  (`nowplaying.Registry.OnChange`: track, state, seek, pictures, stop/remove;
  not a heartbeat that repeats the report), devices (`deviceRegistry.onChange` after
  each committed mutation: epoch, config, rotation; live mode), config
  (`tryUpdateConfig`: every settings PUT; `/admin/reload` stores directly and
  notifies config + Pomodoro after its engine resync), usage (every accepted
  `POST /v1/usage`; the waiter rebuilds and keeps waiting when the bytes
  did not move), day (`dayChanged`: every day action, auto clock-out,
  off-clock reply, day settings). `notify` takes only
  its own lock and never blocks, so callers may hold theirs. Fields that move
  with the clock alone (sun-schedule brightness, weather going stale, live
  mode ending) are caught by a 5 s recheck inside the wait. Quiet hours are
  exact: each wait also arms a timer for the next instant `quietAt` flips
  (`Config.nextQuietEdge`), so a waiting knob gets `quiet` at the minute it
  flips. Across DST the flip is not always a configured start or end: a
  start in the spring-forward gap flips at the transition, and a fall-back
  can leave or re-enter the window at the transition or hit an end twice.
  So the candidates are the start/end wall times on the surrounding days,
  each also shifted by the offset change of nearby zone transitions, plus
  the transitions themselves (`ZoneBounds`); the earliest candidate where
  `quietAt` differs from now wins (`TestNextQuietEdgeAcrossDST`, and a
  minute-scan cross-check over DST weekends in two zones). Topics are bits
  with a per-topic sequence (`since(seq)`), so a later SSE `/v1/events`
  (#269 phase 2) can name what changed on the same broadcaster without a
  queue per subscriber.
- **Liveness fields stay local:** process-liveness data (`owner_pid`,
  `owner_start`) lives only in the local marker, embedded so the wire decoder
  ignores it — never in the `StatusRequest` body.

### Data reality (what flows from where)

| Signal | Claude | Codex | Notes |
|---|---|---|---|
| state / tool / session | hooks | rollout JSONL | T3 Code: SQLite poll of `~/.t3/userdata` (no context, rate or usage) |
| `context_pct` | statusline `context_window.used_percentage` (mod: `session.measure` `context.percent`) | rollout token_count | transcript heuristic was removed (over-read) |
| `rate_window_pct` (5h) | statusline `five_hour.used_percentage` (mod: `rateLimits[five_hour].percentUsed`) | rollout `rate_limits.primary.used_percent` | |
| `rate_reset_at` | statusline `…five_hour.resets_at` (mod: `…resetsAt`, ISO to epoch) | rollout `…primary.resets_at` | epoch secs; countdown computed at render time (TZ-independent) |
| `activity` / trail | hooks (`Tool: detail`, outcome suffix from PostToolUseFailure / PermissionDenied) | rollout (`exec:`/`edit:`/`web:`/`mcp:`) | shared `PrependTrail` ring buffer; `AnnotateTrail` marks an item's outcome |
| `source_card` | producer.env `EMBER_SOURCE_CARD` | producer.env `EMBER_SOURCE_CARD` | `*bool`; absent = on; hides source-name card when false |
| `session_bar` | producer.env `EMBER_SESSION_BAR` | producer.env `EMBER_SESSION_BAR` | `*bool`; absent = on; hides session-pixel bar when false |
| `tokens_today`, cost, model, PR | — | — | wire field exists for tokens_today; **no producer fills it yet** |
| usage 5h / weekly / per-model | `api/oauth/usage` (Keychain, always-on) | rollout `rate_limits.primary`+`secondary` (session-only) | drives the usage card inside the main `ember` app (threshold-gated) and the knob's `usage` block |
| usage account / plan | `~/.claude.json` `oauthAccount` (hashed) | `~/.codex/auth.json` + id_token claims (hashed) | `account_key`, `plan` on `POST /v1/usage` (#535) |

### AI usage card (threshold-gated, single app)

Account-global subscription usage renders inside the main `ember` app as a
**usage card** in the number-slot rotation — no standalone apps. The flow:
producers `POST /v1/usage` → in-memory `UsageStore` (per tool and account,
"Usage per account" below; **not persisted** — every entry refreshes ≤5 min
so a restart self-heals) → the coordinator
builds `UsageView` structs from the usage state each tick and includes a
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

**Usage state** (`cmd/ember/usage_state.go`, #338). Every reader of the
stored usage goes through one projection, `UsageStore.state(sessions, now)` →
`usageState` (`newUsageState` for tests). `Accounts` lists every account
report of the last 7 days with its `Fresh`. Per tool it holds one report,
the tool's **representative account**: the fresh one with the highest 5h
percent (no 5h window counts lowest; ties to the newest, then the smaller
key), else, with none fresh, the newest. So the clock card, the limit
alarm and `GET /v1/usage` see one account per tool, and with a single
account nothing changed (#535). Per tool it holds the report,
`Fresh` (`now - updated_at <= usageStaleTTL`, 10 min, inclusive; a report
from the future counts as fresh) and, for `claude` and `codex`, the effective
5h window: `FiveHourPct`/`ResetAt` from a fresh report's 5h window, otherwise
from the newest session carrying `rate_window_pct` and a reset; `ResetLabel`
is the fresh report's 5h label (empty when that report has no 5h window,
even if the numbers fall back to a session), and only with no fresh report
the newest labelled session's `rate_reset_label`. It knows nothing about devices. Readers: the clock usage
cards (`usageViews`; 7d and per-model only when `Fresh`), the limit alarm
(`checkLimitAlarms`), and `GET /v1/usage` (`stale` = `!Fresh`; it passes no
sessions and reads only the reports). The clock passes its hidden-filtered
sessions, so a hidden tool has no fallback on the clock.

**Usage per account** (#535; `usage.go`, `usage_accounts.go`). Producers
add `account_key` and `plan` to `POST /v1/usage`:

- `account_key` is the first 8 bytes, as 16 hex characters, of sha256 over
  the tool and the account's ids joined by NUL
  (`producer.AccountKey`), computed on the Mac. The server accepts only
  `^[0-9a-f]{16}$`, so neither a raw id nor an email can be stored. Claude:
  `oauthAccount.accountUuid` and `organizationUuid` from the global config
  (`$CLAUDE_CONFIG_DIR/.claude.json`, else `~/.claude.json`; a legacy
  `.config.json` beside it wins, as in Claude Code), re-read when its
  mtime or size moves. The account travels with the snapshot: the
  statusline hook runs in the session's own environment, so it computes
  the key and plan from that session's `CLAUDE_CONFIG_DIR` and writes them
  into the session marker (`usage_account_key`, `usage_plan`, plus the
  config file's path, mtime and size so an unchanged file isn't re-parsed;
  hooks carry them over). It starts the wrapped status line command (if
  any) in the background and meanwhile writes the marker twice (#543).
  The first write stores the figures with the carried-over account; under
  the marker lock it also stats the config, and if the mtime or size
  moved, or a token is already set, it sets a new pending token
  (`usage_account_pending_token`: the write's `UnixNano`, kept above any
  token it replaces). Only then is the config parsed, outside the lock,
  and a second write, under the lock again, stores the new account and
  clears the token only if the marker still holds this render's token. It
  stores nothing else: `statusline_at` and `statusline_changed_ms` keep the
  first write's times, so a render whose parse stalls past the 10-min max
  age doesn't make its figures look fresh. If the marker no longer holds
  this render's token (a newer render set its own, or already settled it),
  the second write skips the marker entirely: that render parsed, or is
  parsing, a config at least as new. It prints once both are done: a
  render takes the longer of the two, a multi-MB `~/.claude.json` never
  holds the lock the daemon and hooks wait on, and neither a slow parse
  nor a slow wrapped command that Claude Code cancels on the next update
  can keep the figures from being written. The account can lag behind
  the figures: while a token is set the daemon doesn't relay that
  marker's snapshot. Only the render holding the current token clears it,
  and that render's write 1 came after every earlier render's. So a
  snapshot is relayed only under an account parsed from the config as it
  stood at its figures' write 1 or later: once `/login` has moved the
  config, the new login's figures don't go out under the old key.
  The token has no time bound: it stays set
  until a render's second write lands (every later render re-parses while
  it is set), so a session whose renders are all cancelled relays no
  statusline snapshot. The 5-min endpoint poll still covers the daemon's
  account meanwhile, and status POSTs still carry `rate_window_pct`. The
  agents watcher's view ignores the token, so re-minting it on each render
  doesn't restart its debounce. A marker from before the token
  (`usage_account_pending: true`) counts as pending, and the next render
  converts it to a token. An old daemon still running after an upgrade
  doesn't know the token and drops it when it rewrites a marker, so
  restart the producers after upgrading (the app does, and so does
  `install` on Linux). The daemon's tick never reads the config; only the
  5-min endpoint poll does, through a cache keyed by path, mtime and size
  (#539). An unchanged render rewrites the marker once a minute, judged by
  `statusline_at` (hooks carry it over and bump the file's mtime, so the
  mtime can't tell). The daemon relays the freshest snapshot per account,
  each under its marker's key; a marker without one is posted keyless,
  never under the daemon's account. A marker written before the upgrade
  (no account fields at all) is taken to be from the daemon's config dir
  and left out once a keyed marker from that dir exists, so an upgrade
  doesn't show an extra fresh "Claude" account. A session whose config
  file is missing records its path with `|-|-` for mtime and size, so it
  isn't taken for a pre-upgrade marker and stays posted keyless. A marker
  whose last render (`statusline_at`, else its mtime) is over 10 min old
  isn't relayed, so a dead session on an old login can't pass its old
  numbers off as fresh. The 5-min endpoint poll reads the daemon's account
  before the fetch and drops the response if it changed by the time the
  fetch returns; its per-model windows are cached per account and joined
  only to that account's statusline posts. Codex:
  `$CODEX_HOME/auth.json` `tokens.account_id` (else the id_token's
  `chatgpt_account_id`), plus `chatgpt_user_id` for the workspace plans
  `team`, `business`, `enterprise` and `edu`, where the account id is the
  workspace. The id_token's payload is base64-decoded locally, never
  verified against the network and never logged; only the three claims
  are read. No `oauthAccount` or no `tokens` (an API key): no key. A
  Codex rate-limit snapshot is bound, when it is captured, to the account
  key it was captured under, and only snapshots bound to the current key are
  posted (#542). The daemon reads the account once per poll cycle and keeps
  the last 8 account eras, each a hashed key and the time it took over, in
  `$XDG_STATE_HOME/ember/codex-accounts.json` (default `~/.local/state`;
  0600, written atomically on a change, every 10 min and on shutdown, with
  the last check time). On the first run there is no file: one era covers
  all time, so existing sessions keep posting. A first observation with no
  key (no `auth.json`, or a half-written one) isn't saved, and the first
  real key takes over that era. A later start with the same key keeps the
  stored eras, so a token refresh while the daemon was down changes nothing.
  A different key opens an era at the later of the stored last check and
  `auth.json`'s write time. That write time is the latest of the file's
  mtime, its ctime (macOS and Linux only; other platforms use mtime and the
  link) and the symlink's own mtime, so a switcher that renames or links a
  saved file in still dates the switch. A live switch opens an era at that
  write time, or at the previous check if the write time is older, clamped
  to now. A timestamp equal to an era's start belongs to that era. A token
  refresh rewrites `auth.json` but keeps the key, so it starts no era. An
  empty key after a non-empty one (Codex truncates and rewrites `auth.json`
  in place, so a read can catch it half-written; or a logout) is not a
  switch, and the last key stays current meanwhile. A rollout snapshot is
  bound only when its line timestamp and its session's start fall in eras of
  the same key, because a Codex process keeps the login it started with. The
  start is the `session_meta` timestamp, moved to a later line in two cases.
  `codex resume` appends to the old rollout and writes no new `session_meta`
  or other marker, so: the first line after no rollout line for 30 min or
  more; and the first turn start after two consecutive lines whose gap
  holds an era start, with no `token_count`, `task_complete` or
  `turn_aborted` in between. A turn start is `task_started`. Codex before
  0.100 doesn't persist `task_started`, so `user_message` also counts, but
  only on a rollout with no `task_started`: newer Codex also writes
  `user_message` for a mid-turn steer. The turn-start rule
  covers a resume right after a switch: a resumed process writes none of
  those before its first turn, while a process still running on the old
  login does, so it stays unbound. A rate line or `session_meta` with no
  timestamp is bound to nothing (the latter logged at debug). An app-server
  snapshot is bound by its arrival time. On a key change, before the usage
  is picked, the app server's merged windows that arrived before the new era
  are cleared, so the new login's first snapshot stays and a null window
  can't carry the old login's value. Limits: an old-login process that sat
  idle across the switch (its last turn ended before it, its next one starts
  after it) or wrote no line for 30 min posts under the new key from its
  next turn, since that looks like a resume; after a switch made while the
  daemon was down, the era starts at the latest `auth.json` write, so if the
  new login's own refresh or any ctime change came later, its sessions
  started before that wait for one of those segment starts; on a pre-0.100
  rollout, a steer in an old-login turn that spans the switch, before its
  next `token_count`, opens a segment; the shared app-server daemon outlives
  its sessions and its wire carries no account id, so if it kept the old
  login after a `codex login` from another process, its rate limits are
  posted under the new key.
- A window may carry `window_minutes`, its length when the producer knows
  it (Codex: rollout `window_minutes`, app-server `windowDurationMins`);
  the knob's `five_hour.starts_at` is `resets_at` minus it, else minus 5 h.
  Producers drop it for an hour on an old server's `unknown field`, like
  the account fields.
- `models` takes at most 4 names matching `^[a-z0-9_-]{1,24}$` and every
  `reset_label` at most 32 bytes; anything else is a 400.
- `plan` (`^[a-z0-9_]{1,24}$`): Claude from `organizationType` /
  `organizationRateLimitTier` (`max`, `pro`, `team`, `enterprise`, `free`),
  Codex from the id_token's `chatgpt_plan_type`, else the rate-limit
  snapshot's plan (rollout `rate_limits.plan_type`, app-server `planType`).
- Producers drop either field for an hour when an older server answers
  400 `unknown field` (`fieldDropper` in `internal/producer/compat.go`, as
  for the status `project`), so a new producer keeps posting to an old
  server; an old producer's keyless post works unchanged.

The store is keyed by (tool, account_key). The newest `updated_at` wins
(the server stamps every post with its receive time and ignores any time a
client sends, so ranking, staleness and expiry can't be forged; an older
one never overwrites), so several Macs on one account share one entry.
`source` is trimmed once on arrival, before `allowSource`, and only the
trimmed value is stored and compared. Here `source` is a path label
(`endpoint`, `statusline`, `codex_stream`), not a host, so the caps don't
use it: they count per writer, the authenticated principal (each client
token by its id; the master token is one writer of its own). A writer
keeps at most 8 accounts per tool and 16 across tools, a new one replacing
that writer's own oldest, never the master's or another token's entry, so
no single client token reaches the 64-account limit; past it a client
token's new account is refused (507, logged `usage post rejected
reason=account_limit`), while the master token's never is. Every Mac on the
master token shares its 8 per tool. An entry belongs to the writer that
created it, and only that writer's caps count or evict it. Merging several
Macs and boxes on one account is the point, so another writer may update
it once it is stale (no post for 10 min, the view's staleness) or its
owner's client token no longer exists (deleted; a re-minted token adopts
its old accounts this way), and then takes it over. While it is fresh and
someone else's, a client token's post is 403 ("the master token" or
"another client token posted it in the last 10 min", logged `usage post
rejected reason=owned owner=master|client`, no ids), checked against the
registry too after a restart, as far as its hourly-throttled `last`
reaches: an account that was fresh just before a restart can look stale
right after it, so another client may take it over until the owner's next
post (a master post takes it back); the master token is never
refused and always takes the entry over. So a Mac on the master token and a
box on a client token logged into one account both feed it: the box's
posts are refused while the Mac's are fresh and take over once the Mac
sleeps. **Stale takeover is by design** (newest wins; several writers merge
on one account, as the spec says), so its edges are fenced: a takeover
moves only that entry's owner and never its first sighting, sequence or
settings, so order and names don't move; an entry the master token ever
wrote (`MasterHeld` in the store, `master` in the registry, carried from
the registry into the store after a restart) counts toward no client
token's cap and no client cap evicts it; and a bound token still passes
`allowSource` for its labels before any of this. The registry follows the same per-writer caps and skips a
new account at 64 instead of refusing; the store keeps such an account's
first post time, so its first sighting and place don't move with each
post (until a restart, which forgets it). An account's
id is `<tool>:<account_key>`, so one key on two tools stays two accounts; a
keyless post is one account per tool, whose id is the bare tool name, which
no keyed id can equal; once its tool has a keyed
account, the keyless one is left out of the account list as soon as it is
stale, so an upgraded fleet doesn't show a ghost. An entry is dropped 7
days after its last post (`usageAccountTTL`; pruned on each post and
skipped by `state`); stale still means 10 min.

**Registry** (`usageAccountRegistry`, store key `usage_accounts_seen`):
per account id its tool, writer, plan and first and last sighting, persisted so the
default order and the 7-day expiry survive a restart. Memory follows every
post; the store is written only for a new account, a plan change, when
the account's last write is an hour old, or on graceful shutdown if any
newer sighting is still unwritten (at most one write per account per
hour, plus one per plan or owner change, so a master takeover and the
master-held mark reach the store at once; owners change at most once per
10-min staleness, and the post's `source` plays no part, so one
account alternating between `statusline` and `endpoint` doesn't rewrite
it). A post older than the account's last sighting changes nothing, so two
racing posts can't move it back in time; entries past 7 days are pruned.
Each entry also keeps the account's last snapshot, trimmed to what the
knob can show: the 5h and 7d windows as `[pct, resets_at, window_minutes]`
and the percents of up to 4 models with a name the view accepts, pct
rounded to 0.1, no labels, no source. Its times are whole seconds in UTC,
and `POST /v1/usage` clamps any `resets_at` into now − 1 day … now + 30
days, so 64 worst-case accounts (each master-held and then taken over by
a client token, the largest an entry gets, with 19-digit sequence
numbers) persist in about 29 KB, capped
at 30 KiB by `TestUsageRegistryPersistsATrimmedSnapshotOfBoundedSize`.
The snapshot is written with the entry, so
after a restart an account with no post yet shows that snapshot as stale
with its old `updated_at` (the last post after a graceful shutdown, up to
an hour behind it after a crash, the write throttle) instead of vanishing
from the knob; the first post replaces it.

**Settings** (`usage_accounts_json`, one `settingSpec`; `Config.UsageAccounts`):
per account id `name`, `hidden` and `order`. `GET /v1/usage/accounts`
(owner or `admin`) lists the accounts in display order with
`name`, `name_set` (true when the name is the account's own, even
one equal to `default_name`, so a client can keep it), `default_name`,
`plan`, `hidden`, `order`, `first_seen`, `updated_at` (null with no
snapshot, live or stored) and `stale`;
`PUT` (master token only) merges by id: an id's object replaces its
settings, `null` resets them, an id the server doesn't list is a 400. An
account's settings go with it: the next accepted post resets the settings
of every id that neither the store nor the registry holds (gone 7 days
after its last post, or replaced under its writer's cap, in whichever
order the two drop it), so churn can't fill the 64-entry settings cap.
Nothing is pruned while the registry failed to load. A registry whose
stored value can't be read or parsed fails closed, like the device
registry: it runs from memory until restart, never writes over the stored
value (which keeps every first sighting and snapshot for a fix), and
`ember doctor` reports `usage_accounts` FAIL.
Order: accounts with an `order` first, ascending; then Claude, Codex, other
tools by name, each by first seen. Names: the set one, else the plan's
label ("Max", "Pro", "Team", "Plus"; no plan: "Claude", "Codex"). Two or
more default-named accounts of one tool on one plan are numbered in display
order ("Max 1", "Max 2"); an account with its own name or hidden leaves
the numbering of the others, so the knob never shows a lone "Max 2" (a
hidden one is numbered among the shown ones as if it were shown), and an
account's `default_name` is what it would get back.
`hidden` only affects the knob: the clock keeps the representative
account and its per-tool hide in `/v1/apps`.
Ember.app edits them in Settings › Sources › Agents › Accounts (#536;
`UsageAccounts` in EmberKit, `UsageAccountsSection`): one row per account
with the knob's tool mark, the plan label, the name (an empty field means
`default_name`), a show-on-knob switch and a drag handle (or Control-click)
to reorder. No id or email is shown. Names are trimmed, stripped of control
characters and cut to 24 code points (whole characters), as the server
counts them. A typed name is kept as typed, even when it equals the
default; a row's own name comes from `name_set` (an older server without
it: a name different from `default_name`). The plan labels are checked
against the server's table through the golden
`cmd/ember/testdata/usage/plan_labels.json`. A reorder writes `order`
0…n-1 on every row; a save PUTs only the rows whose settings changed and
that the last server list holds, each with its whole object (`null` when
it is back to defaults), and takes the reply as the new list, so
renumbered `default_name`s show. A 400 or 403 reverts the list to the
server's (re-read) and keeps the error under the section; nothing is
resent. On a 400, an edit made while the PUT and the re-read were in
flight is re-applied onto the server's list and saved next (a 403 drops
it, since every edit would fail): a name or switch per row, while an
order change takes the draft's order of the ids the server still lists,
appends any others and renumbers them all, so the list shows the order
the user last saw. One 400 exception:
when the re-read list lost an account the body named (expired, evicted,
or the stale keyless one dropped), the PUT is retried once without it,
and if the retry fails its own error is shown. A focused name field that
still shows the rejected name takes the row's name back on the error.

**Idle usage frame.** When all sessions expire and a tool is over threshold,
the coordinator publishes a **dimmed usage frame** (the same usage card content
at ~40% brightness) during the `DIMMED` phase instead of going dark
immediately. Under threshold the app leaves the device rotation normally.

**5h limit-reset alarm.** A small per-tool state machine, app state in
`limitAlarmState` (`usage_alarm_state.go`, #351), checked each tick by the
coordinator (`usage_alarm.go` `checkLimitAlarms`). `due(usageState, now)`
updates the armed state and returns the tools to fire; the coordinator
renders each as a notice and calls `fired` only when the push succeeded.
When the effective 5h window — fresh endpoint usage, else the live-session
statusline fallback — reads **≥ 99.5 % with a known future reset**, it arms for that `resets_at`; once the reset
passes (+60 s grace, never early) it fires **one** auto-dismiss notification
(`CLAUDE 5H RESET` / `CODEX 5H RESET`, drawn tool icon) plus an RTTTL chime.
Drifted reset estimates re-arm instead of firing; an unreachable device retries
next tick (armed state preserved); fired alarms dedupe per `(tool, resets_at)`.
State is in-memory by design — a restart mid-window re-arms from the next
snapshot. The endpoint percent is rounded, hence the 99.5 % threshold, and the
alarm fires a minute after the estimated reset because reset estimates drift.
Disabling the alarm drops armed state so re-enabling hours later cannot fire a
stale "reset" popup; re-enabling recreates both maps, so fired entries are
dropped too. A past `resets_at` still cannot re-fire: arming requires a reset
still in the future, while a fire only happens after the reset plus the 60 s
grace. `UsageStore` is not persisted: entries refresh at most every 5 min and
a restart self-heals within one interval. Gated only by `limit_alarm` (usage
config, default on); deliberately independent of the usage card threshold (the
alarm is about resuming work, not tiles).

**Quiet hours.** A global night mute (`quiet_hours` config: `enabled`,
`start`/`end` `"HH:MM"`, default off / 22:00–08:00; runtime override via
`GET/PUT /v1/quiet/config`, store key `quiet_json`). Enforced in the clock
adapter's notice code (`coordinator_notices.go`) — during the window
(server-local wall clock; overnight wrap supported; `start == end` = never)
a notice is pushed without its sound (no `sound`/`soundRtttl`/`soundLoop`,
NG 1.0/1.1's three notification sound fields, folded into the one `sound`
object on 1.2; AWTRIX3 spelled the latter two
`rtttl`/`loopSound`) and `playChime` no-ops, so every sound source is covered
at one choke point. Visual output is untouched — an attention hold still
takes the screen at night, just silently — and sounds resume on the first
event after the window. `clockPublisher` is ungated; only `NewApp` builds it
and only the notice code may call its `Notify`/`PlayRTTTL`
(`clock_access_guard_test.go` enforces both), so a new sound source needs no
per-feature check. The menu's explicit `/v1/device/audio/test` bypasses the
gate on purpose. The rule itself is app state: `App.quietAt(t)`, which is
`Config.quietAt(t)` (`quiet_config.go`) or off the day clock (#729, "Day
clock"), read by the clock adapter through `coordinator.quietAt` (its
`quiet` hook) and by the knob view (`"quiet":true`, #343), so both devices
agree on the window. Every caller passes server wall-clock local time (the coordinator
clock, the view's `now`), because `quietActive` reads `Hour()`/`Minute()`
with no zone conversion.

**Notices.** App code never builds an AWTRIX notification. It emits a
`notice{app, kind, priority, sound, payload}`: `payload` is the visual popup
body from `render.*PopupPayload`, `sound` names a device melody or an inline
RTTTL (optionally looping), `priority` is interrupt or queue, and `kind`
selects the popup. The clock adapter (`coordinator.showNotice`) turns it into
one `POST /api/v1/notifications`: `name` from the kind (`ember-weather-popup`,
`ember-reminder`, …, `notify_names.go`), `stack` from the priority (queue =
`stack:true`), sound keys unless quiet hours are on. `playChime` plays an
out-of-band chime (the attention lock) and is quiet-gated too.
`dismissNotice` retracts a kind by name and is **never** quiet-gated: the
reminder loop guard dismisses a looping alarm at quiet-hours start, so a gated
dismiss would leave it ringing all night (`TestDismissNoticeIgnoresQuietHours`).
Every quiet decision (notice sound, the chime, the reminder loop's arm and
stop, the corner-LED quiet indicator) goes through `coordinator.quietAt`, read
on the coordinator clock, which delegates to `App.quietAt`. The reminder
handler samples it once, before the push, and hands that value to
`showNoticeQuiet`, so the alarm's sound and its loop guard always agree: quiet
hours starting during a slow push still leave a loud loop guarded, and quiet
hours ending between two samples can't send an unguarded loop. Firing
decisions (meeting per-occurrence dedupe, sun once-per-crossing, weather edge triggers,
the usage alarm arm/fire state, reminder idempotency keys) stay with the app
that owns the state. A notice is plain data, so a later knob subscription can
read the same stream. `notices_test.go` pins the exact clock calls of every
notice source, quiet hours off and on, as JSONL goldens (`testdata/notices`,
regenerate with `-update`).

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
- **Waiting with a project (#485)** — while the lock holds, the payload
  names the session: `text` is the display name plus " +N" for the other
  waiting sessions (`learn-language +2`), in amber, with
  `scroll:{mode:"loop",whenFits:"static"}` (NG's documented `loop`: the text
  moves without end, a copy following `gap` px behind; a short name rests).
  The tool logo blinks amber every 500 ms as a native `icon`
  (`ember_wait_<tool>_<crc32 of the GIF>`, a two-frame 8×8 GIF, 50 cs per
  frame, generated by `render.WaitIcons`; the hash in the name makes a
  changed GIF upload again, and provisioning deletes any other
  `ember_wait_*` icon once the current set is on the clock; the delete
  lists the icons again and deletes them in one `Publisher.DeleteIcons`
  call, so it only acts on the listing of the clock it deletes from, even
  if the clock moved after the first listing). `ensureNativeIcons` uploads them with the other
  icons, so the name does not blink and `textOffsetX` is left out: NG starts
  text at col 9 after an 8 px icon with the default `iconGap`. Until the
  icons are on the clock (`App.waitIcons`, cleared on a clock move,
  re-checked by one icon listing after a detected reboot or boot ping; a
  provisioning job that started before a move doesn't arm them or start
  the delete, since the move bumps a clock generation it checks; a move
  during the delete needs no check, as the set is fixed and a stale name
  is stale on any clock) the
  drawn icon stays and the name blinks instead. After the lock, the
  session's first rotation card (`cardProject`) shows the name once
  (`repeat:1`, no hold), and the coordinator keeps that card until the
  scroll is done (`render.CardDwell`: 1 s NG hold plus width + 23 px at
  21 px/s) instead of moving on at the 3 s dwell. The lead is the newest
  waiting session.
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
- **`scroll.whenFits` is a string enum, not a bool** (a bool is rejected with
  422 on `scroll.whenFits`; verified on 1.0.13). `"static"` leaves text at rest
  when it fits and scrolls it when it overflows. Set it explicitly, because every
  scroll field inherits individually from the device's global scroll setting.
- **`durationMs`/`lifetimeMs` are milliseconds on NG** (AWTRIX3 used seconds).
  Forgetting the ×1000 is a silent 1000× shortening, not an error, so every
  conversion goes through `msOf`.
- **The source-name card cannot scroll.** The bitmap ops around the native-text
  box would clip a scroll, so longer names are cut to what fits in cols 9-23 by
  NG's small-font glyph widths ("STUD" is 15 px; "MWMW" would be 23 px and
  becomes "MW"). Runes outside printable ASCII count as wide because their
  widths are unknown.
- **Berry.** Open-Meteo emits `"current_weather_units":{…,"temperature":"°C",…}`
  before the real reading, so a `find` on `"temperature":` lands on the units
  block; anchor on the enclosing object instead (one 128-byte window then holds
  both values). The API reference's worked example has this bug. `matchall`'s
  first hit is the "2" of `temperature_2m` in the needle itself.
- **Verify on-device** by reading `GET /api/v1/display/screen`, which wraps
  the framebuffer as `{"width":32,"height":8,"pixels":[256 ints]}` (AWTRIX3
  returned the bare 256-int array — consumers must unwrap the new shape) — see
  RUNBOOK for the ANSI-render + crafted-session technique.
- **The gamma/brightness hue-shift analysis that used to live here was derived
  from AWTRIX3 source and has not been re-derived for awtrix-ng** — deleted
  rather than carried forward unverified. If NG exhibits the same brightness-
  dependent hue shift, re-derive and re-document it against NG's own gamma
  code before relying on it.
- **NG 1.2 changed the audio API; Ember keys off `capabilities.audio.rtttl`
  (#577).** 1.2 renamed `audio.buzzer` to `audio.rtttl` and answers 422 to
  `soundRtttl`, `soundLoop`, a numeric `sound`, `{"melody":…}` on
  `/api/v1/audio/play` and the `soundEnabled`/`buzzerVolume` settings. The
  presence of `audio.rtttl` in the cached capabilities selects the 1.2 shapes
  (`AudioCaps.SoundObjectAPI`): notices send `"sound":"<name>"`,
  `{"file":…,"loop":true}` or `{"rtttl":…[,"loop":true]}`, the menu's melody
  preview sends `{"file":…}`, and the settings whitelist swaps to
  `volume`/`appVolume`/`alertVolume`. "Has a buzzer" is `buzzer || rtttl`
  everywhere it gates (audio routes' 503, Settings › Sounds, the doctor
  check). With no capabilities cached Ember sends the 1.0/1.1 payloads, byte
  for byte. A notice or melody preview the clock refuses with 422 refreshes
  the capabilities once and, if the format changed, retries once in the new
  one; a notice still refused for a sound field after that (`field` `sound`,
  `sound.*`, `soundRtttl`, `soundLoop`) is sent once more without its sound,
  so the popup shows silently. A refusal of another field (an icon, a
  colour) is not resent: dropping the sound can't fix it. That is at most
  three attempts, never a loop. The refresh triggers (reboot, a watch tick
  with an empty cache) keep that path rare. A custom Pomodoro melody
  containing `:` is RTTTL, as for severe weather (`soundSetting`), and the
  Pomodoro and weather settings PUTs refuse a changed melody NG 1.2 can't
  play: a name outside `[A-Za-z0-9_-]{1,32}`, or RTTTL that is not
  `name:defaults:notes` with a 1–24 character name, or is over 512
  characters (`validSoundSetting`; NG 1.2.2 `sounds.md` "Writing RTTTL").
  Notes are not parsed, so a bad note still saves and plays silently. The
  rule is NG 1.2's whatever firmware is attached, so a 1.1 clock can't be
  given a value that breaks after an OTA; the 400 says so. The melody
  preview takes NG 1.2's 1–32 playback name rule (only saving a melody is
  limited to 24) when the cached capabilities show 1.2 or none are cached,
  and 1–24 on a 1.0/1.1 clock, whose `{"melody":…}` plays only stored
  melodies. Values already stored are kept. The old volume values are not
  migrated: NG 1.2 resets `volume` to 60 on update, a muted clock included,
  and Settings › Sounds says so under the mixer.

### DarwinKit / AppKit (retired Go menu)
The retired Go menu was replaced by the native SwiftUI app (`macos/`), so its
hard-won DarwinKit/AppKit retention crashes (weak `NSWindow.delegate`, libffi
`NSTimer`-block frees, `NSBitmapImageRep planes`, bundle-less activation policy,
uncommitted `NSTextField` edits) are no longer live constraints.

### macOS menu app (SwiftUI)
- **Opening a window from an `LSUIElement` app on macOS 26+ takes four steps.**
  Promote to `.regular` first, because an accessory app cannot own the key window
  of the frontmost app and the Dock icon must exist before activation is
  requested. Defer the raise to a later runloop turn: from a `MenuBarExtra(.menu)`
  item the action runs inside the menu's tracking loop, and activating there is
  undone when the menu closes (window in front but inactive, grey traffic
  lights). Use cooperative `NSApp.activate()` plus
  `makeKeyAndOrderFront`/`orderFrontRegardless`, retried while SwiftUI builds the
  window. If the system still refuses, fall back to the deprecated
  `activate(ignoringOtherApps:)`, called through a protocol to avoid the
  deprecation warning. SwiftUI names a `Window` scene's `NSWindow` identifier
  `"<id>-AppWindow-<n>"`, and the raise helper finds the window by that prefix.
- **`.task` and timers don't run in a `.menu`-style `MenuBarExtra`**, so the menu
  never polls; it catches up overdue glance feeds in `onAppear`, throttled to
  once a minute so repeated opens can't push stats past one request a minute.
- **Location.** For a menu-bar (accessory) app macOS often never presents the
  "Allow location" prompt, so a pending authorization times out as
  `.authorizationUnavailable` and the UI points at System Settings;
  that error means the user must enable Location for the app there.
  `requestLocation()` is deferred until the user answers the prompt, because
  issuing it while `.notDetermined` doesn't reliably deliver a callback.
- **Hardware snapshot tool.** `ImageRenderer` leaves AppKit-backed controls
  (the segmented picker) blank, so the Debug-only `EMBER_HARDWARE_SNAPSHOTS=<dir>`
  renderer draws through an off-screen key window (key, so the default
  button draws prominent).
- **Ring gauges are hand-drawn.** The system accessory gauge style is fixed at
  watch-complication size, so `HardwareNowCard` draws its own 270° ring.
- **A disabled row can't be re-enabled by a child.** A child's
  `.disabled(false)` can't undo a parent's `.disabled(true)`; the info button
  stays usable in a disabled row by overriding the environment value
  (`staysEnabled`, #290).
- **Launch at login** uses `SMAppService.mainApp` (System Settings › General ›
  Login Items). It needs the app signed and in `/Applications` to register fully,
  and a first registration may report `.requiresApproval`.

### Producer / deploy

- **Linux producer plumbing.** `/proc/<pid>/stat` `comm` is cut to 15 bytes
  (`ember-claude-pr`), so owner checks match on the cut name. Process start
  time is read as clock ticks after boot with `USER_HZ` fixed at 100 (sysconf
  needs cgo). Log redirection uses `unix.Dup2`, not `syscall.Dup2`:
  linux/arm64 has only `dup3`.
- **Unix socket paths in tests.** `t.TempDir()` paths can exceed `sun_path`
  (104 bytes on macOS); tests that listen on a Unix socket use a short path.
- **Process-liveness, not file-existence, detects session close.** A heartbeat
  that re-posts any young marker keeps dead sessions alive for hours and defeats
  every server staleness window. `SessionEnd` is unreliable (skipped on
  window-close / Cmd-Q / SIGHUP / crash). Walk the hook's process ancestry past
  the `sh` wrapper to the owning `claude`/`node` PID, record PID + `ps lstart`
  (guards PID reuse), reap when it dies (~10 s). A failed start-time lookup is
  not death: only a pid that signal 0 reports gone is. Audit any code that round-trips
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
- **A rebuilt ad-hoc helper leaves its job loaded but unspawnable.**
  Background Items pins an agent's launch constraint (LWCR) to the helper's
  cdhash when there's no Team ID, and re-registering keeps the existing item,
  so after a helper change the first spawn is a *Launch Constraint Violation*.
  launchd's repair then makes Background Items replace the item (new UUID,
  fresh constraint) but reports failure (`Unable to invalidate item`, error
  -67068 on macOS 27.0), and the loaded job keeps the dead UUID: `launchctl
  print` shows `job state = spawn failed`, `last exit code = 78: EX_CONFIG`,
  `needs LWCR update`, and every respawn logs `Could not find and/or execute
  program … 3: No such process`. Unregister + register on top of it doesn't
  help; `launchctl bootout` of the job, then register, does (verified on
  26A428). The app treats that state as **Not running** (Repair boots out,
  then registers) and checks again 30 s after an update reconcile (the new helper's first spawn,
  launchd's 10 s respawn throttle and the failed constraint repair take about
  11 s in all, on macOS 27). The
  helpers are signed with a fixed identifier (`com.ember.claude-producer`,
  `com.ember.codex-producer`, `com.ember.t3-producer`): the ad-hoc default `<name>-<LC_UUID>` changes
  every build, and Local Network privacy keys on it, so a rebuilt helper got
  `connect: no route to host` until the user allowed it again.
- **A Local Network refusal looks like an outage.** An ad-hoc app's grant is
  keyed to its cdhash, so every local rebuild loses it (even right after the
  user toggled it on): `NWBrowser` fails with DNS-SD `NoAuth` (-65555) and
  URLSession fails with -1009 over `ENETDOWN` ("Network is down"), path
  "unsatisfied (Local network prohibited)". EmberKit maps that to
  `APIError`/`FeedError.localNetworkDenied` (`LocalNetworkDenial`) so the UI
  says "Local Network access is off" rather than "Server unreachable", and
  Settings › Permissions probes it. Wi-Fi off gives the same -1009/`ENETDOWN`
  for a LAN host, so without the failed path's reason that guess only counts
  while `NetworkPathSnapshot` (one `NWPathMonitor`) says the Mac's path is
  satisfied. Sign local builds with
  `scripts/local-signing-identity.sh` to keep the grant across rebuilds.
- **`BundleProgram` and argv[0].** With `BundleProgram` set in a LaunchAgent
  plist, launchd uses `ProgramArguments[0]` as argv[0]. It must be the program's
  basename, not the subcommand; otherwise the daemon starts with `argv=["run"]`,
  prints usage and exits 2. `verify-bundle.sh` guards against this.
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
- **The Docker build pre-creates the Pomodoro data dir** (`/var/lib/ember`)
  because the distroless runtime image has no shell to `mkdir`; it ships a
  writable, nonroot-owned location for the SQLite stats DB. The
  `-X main.version` ldflags path is pinned by `version_ldflags_test.go`; change
  both together.
- **Building inside a git *worktree* hides the VCS revision** from Docker
  `buildvcs` (the worktree `.git` is a file) → `version: unknown`. Build from a
  normal checkout / CI.
- **`.dockerignore` must never exclude a tracked file.** The build stage runs
  `go build -buildvcs=true` against the copied `.git`; a tracked file missing
  from the context shows as deleted, and every image is stamped `+dirty`
  (v0.46.4 and earlier: `.claude` was excluded whole). `scripts/check-image-vcs.sh`
  enforces this in CI (`image-vcs`) and before publish.

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
