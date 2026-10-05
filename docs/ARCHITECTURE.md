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
  restarts and `/admin/reload`) — one of the settings-overlay registrations
  (see "Runtime settings overlay" below).
- **Coordinator ownership and locking.** The coordinator goroutine owns every
  write to the rotation: the session app, tiles, corner LEDs and the display
  hold. One-shot notifications (`/v1/notify`, reminders, weather and meeting
  popups, the Pomodoro phase-end alert) call the `Publisher` directly, and the
  menu's `/v1/device` proxy uses `clockAccess`.
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
    `RLock`. The idle usage-face cursor shares `cardCursor`.
  - **Attention lock release.** A lock ends on ack timeout (`ackTimeoutDur`,
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
  so the device doesn't evict it. `usageViews` prefers endpoint usage and
  falls back to the statusline with the same precedence as the limit alarm;
  hidden and below-threshold tools are absent. Weather tiles clear after
  `weatherTileStaleTTL` (30 min) so a wedged poller leaves no stale
  temperature. The meeting countdown is the ceiling of the remaining time (at
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
  `NewApp(cfg, nil, …)` and wrapped in `quietPublisher` at once. Tests pass a
  fake instead. Sound policy and the coordinator's retries sit above that
  seam, so a fake sees exactly what the clock would. `a.clock` has no
  `Notify`/`PlayRTTTL`, so nothing can sound the clock past the quiet gate.
  `clock_access_guard_test.go` checks these rules on the type-checked
  package.

  The `Publisher` is the seam for every server-initiated clock write.
  `clockPublisher` builds a fresh client per call, so a rediscovery URL swap
  applies to the next write. Pushed apps are RAM-only on NG; on startup
  `ListApps` adopts Ember-managed apps left from a prior run so they can be
  reconciled or cleared although the in-memory push trackers start empty.
  `PlayRTTTL` is only for chimes with no notification of their own (the
  attention lock); popups carry their melody in `soundRtttl`. Every
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
  (retried). Notifications run with quiet hours off, then on at 23:00. The
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
  triggers a reconnect. A message over 16 MiB is drained and skipped and the
  connection stays up. It **never starts the daemon**. It
  initializes as `codex_app_server_daemon`: the first client name outside
  the server's non-originating list becomes the daemon-wide originator, which
  Codex records in every TUI rollout. It opts out of the delta notifications.
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
  owns every loaded thread, and recently closed ones for one activity window
  more. The watcher still folds those rollouts but posts or DELETEs nothing
  for them. A session the watcher had posted is DELETEd at takeover unless
  the app-server posts it. A status change or close that arrives while a
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
  Codex `user_input` requests outlive the turn); run status preparing /
  starting / running / **waiting** → `running` (a run "waiting" is
  post-turn drain such as checkpoint capture, not the user). Like T3's
  `activityRunStatus ?? status`, the newest preparing/starting/running/
  waiting run of any ordinal wins over the presented run, and a presented
  `queued` run is not activity (→ DELETE). A `completed` run stays `running`
  while background work that wakes the agent is open
  (`backgroundWorkHoldsCompletion`: roster task of the active provider
  thread with kind other than `command`, or an active subagent /
  non-persistent dynamic_tool turn item outside a rolled-back run; dev-server
  commands do not hold). That hold is a single-query SQL equivalent of T3's shell read plus
  in-memory decode, not a copy of its code; the only known difference is that
  it does not dedupe roster vs item by task id. The turn-item lookup repeats
  the type/status lists of T3's partial `turn_items_recovery_idx` verbatim so
  SQLite uses it. When a hold ends after the activity window, the watcher
  times `done` from the moment it saw running → done, so it still shows. Deliberate difference: the `auth_refresh` filter
  runs in the request query, so such a request never hides an older approval.
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
  projection tables that churn (56 migrations): the queries are reduced copies
  of T3's own shell queries, the newest verified migration per schema is
  pinned (`pinnedMigrations`: v1 54, v2 56) and a newer one is logged once and
  still read, and a missing table or column is a soft failure (log once a
  minute, back off to 60 s, never post or delete on a bad read).

  *Double sessions.* T3's Claude provider loads the user's Claude settings, so
  the Ember Claude hooks fire for T3 Claude threads too and the same work shows
  as a `t3` and a `claude` session. The hook payload carries no T3 marker
  (T3 sets neither `CLAUDE_AGENT_SDK_CLIENT_APP` nor any T3 env var), so the
  producer cannot tell them apart cheaply; the RUNBOOK documents the
  separate-`CLAUDE_CONFIG_DIR` workaround. A dedupe in the Claude hook (walk
  the ancestry to the pid in T3's `server-runtime.json`) is possible but adds
  `ps` calls to the 500 ms hook budget; left for after a live check.

Claude producer constraints:
- **Hook timeout.** The hook path uses the short `HookTimeoutMs` (default
  500 ms) because the hook blocks the `claude` CLI. The daemon uses a separate,
  longer `daemonHTTPTimeout` (5 s) so a slow link doesn't flap heartbeat
  re-POSTs and reap DELETEs.
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
  stdout still has its output shown.
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
  (`quota_auto_resume_stale`, `agent_needs_input`, elicitation) stay with the
  hooks. A Stop skipped for waking background work sets `bg_wake`, which
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
  2.1.288; a failed call backs off 5 min. `doctor` prints the state.
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
  Sounds & Alerts), **Sources** (Agents, Focus, Weather, Calendar, Music: where
  data comes from, set once) and **Devices** (Clock and Knob, each with its
  hardware pages and an Apps subtree holding that device's presentation of
  each source; a source pane and its device apps bind the same config model,
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
| C, only while held | `screen` 1 s, `clockHealth` 15 s, `weather`/`activity`/`workhours`/`heatmap` 5 min | — |

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
`/state`. The menu adds no polling: opening it catches up
`stats`/`meetings`/`usage` only if they're over 60 s old, and every fetch pushes
the next poll back, so stats stay at one request a minute. Actions (Pomodoro,
clock, app visibility) go through `ActionRunner`, from every surface (Settings
restarts the clock and switches the display through it too), which keeps the
last failure for 10 s and refreshes the feeds the action touched (stats follow
a Pomodoro phase change). Display power is one value, `LiveModel.displayPower`:
the newest of clock health's `matrixPower` (dated at probe time, so the
server's 30 s probe cache can't undo a write; it must be over 2 s newer to
beat a report), a successful power write or reboot, and Settings' overlay read
(dated when issued). Results from a previous server are dropped. While a write
is in flight the switches show its target (`ActionRunner.pendingDisplayPower`). With no window open the app makes about 2,640 requests an hour:
1,200 each to `/state` and `/v1/pomodoro/state`, 60 each to stats, usage,
meetings and apps (the old poller made about 4,800, 1,200 of them stats).
`APIClient`'s sessions and the direct screen mirror skip the on-disk `URLCache`,
which, with no cache headers from the server, only rewrote `Cache.db` every poll.

**Model and networking constraints.**
- **Config models.** Panes bind controls to `ConfigModel.draft` and call
  `scheduleSave()`; the model writes 600 ms after the last edit and never writes
  before a successful load, so views must disable their controls until
  `isLoaded` or an early edit is silently discarded. A failed save is retried by
  the next `load()`; a `.featureOff` save is reverted to the server's value, not
  retried. A pending edit blocks a reload from overwriting what the user just
  typed. Models decode absent fields to the server's own defaults so re-saving
  an old config never turns a feature off.
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
  distributed notification only while it is on: no timers, no polling.
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
  launch Music. While the toggle is on, `MusicCommandListener` long-polls
  `/v1/nowplaying/commands` for this player and runs the knob's commands
  (#280; see "Now playing"). Hardened runtime needs
  `com.apple.security.automation.apple-events` (`Ember/Ember.entitlements`)
  and `NSAppleEventsUsageDescription`; Settings › Permissions has an
  "Automation: Music" row read with `AEDeterminePermissionToAutomateTarget`
  (no prompt; "Couldn't check" while Music isn't running). MediaRemote was
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
first run. A per-agent off is remembered (`producers.optOut`), so the master
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
tile), so the **server provisions icons**: on startup and on every weather or
Pomodoro config apply it lists the clock's `/ICONS` folder (`GET
/list?dir=/ICONS`), downloads any missing configured icon ID from the
LaMetric gallery (`.gif`→`.jpg` fallback, then the extensionless URL as a
last resort — some IDs, e.g. the Pomodoro tomato `29802`, exist only as a
PNG there, which is decoded and re-encoded as GIF locally since awtrix-ng's
upload only accepts GIF/JPEG magic bytes, answering PNG with 415; `pngToGIF`
refuses anything that isn't a PNG of at most 16×16, such as an HTML error page),
and uploads it (`multipart
POST /edit`, `Publisher.ListIcons`/`PutIcon`). List failures abort the run;
per-icon failures log and retry on the next apply/restart. Covers both the
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
- **Astro.** Moon phase and sun times are computed locally (no API or key) at low
  precision (a minute or two). The "local" label time uses the UTC offset Open-Meteo
  supplies (`TZKnown`/`TZOffsetSeconds`); only without it (MET) does it fall back
  to longitude (15° per hour, no tz database), which can differ from civil time at
  DST/zone boundaries. At polar day/night `isNight` defaults to day (the sun
  icon), since declination versus latitude isn't cheaply distinguished.

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
EventKit callback queue doesn't trap), authorization, prefs in UserDefaults,
and the App Nap assertion. That assertion (`.userInitiatedAllowingIdleSystemSleep`)
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
Design note: Obsidian `Superpowers Specs/ember/2026-10-05-now-playing-design.md`.

- **Model** (`internal/nowplaying.Registry`, pure): one entry per
  `(source, player)`; a report is `{source, player, state
  playing|paused|stopped, title, artist, album, track_id, duration_ms,
  position_ms}` (`source` `^[a-z][a-z0-9_-]{0,31}$`, text ≤200 characters).
  `stopped` deletes the entry. Shown: the most recently *started* playing
  entry, else the most recently paused one. Paused lives 10 min from the
  pause (re-reports don't extend it); playing lives until its extrapolated
  end + 5 min (a Mac that slept never says "stopped"; Music posts no notification on a seek, so a backward seek must not hide a playing track), or 30 min after its
  last report without a duration. An expired entry stays (hidden) until
  its track or state changes, so Plex re-reporting a long-paused session
  doesn't bring it back; entries silent for 1 h are forgotten, and at most
  32 players are kept.
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
  decoded once at ingest (`nowplaying.Validate`), so a corrupt body is a
  400, not a failure on every render. Served sizes are a fixed set per kind
  (`nowplaying.Sizes`: album 240/120, artist 64/120, backdrop 466; first =
  default), so a client can't force a fresh render per request. 466 isn't
  a multiple of the 16 px MCU; TJpgDec handles the partial MCU, but the
  knob's output buffer and stride must allow for it (cinder#14).
  Renders are serialised (a 1400 px source costs ~44 ms and ~22 MB
  transient) and cached in a RAM LRU of 32 entries / 8 MB keyed by
  `(hash, kind, size)`. Nothing touches disk (Deezer's terms forbid
  storing its images).
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
    (master token; delivery at most once; each command carries `age_ms`,
    its wait here, and the app drops one older than 5 s in all). A Mac
    counts as listening while a poll waits and 30 s after one; otherwise
    the control answers 503. The app sends Apple Events to the running
    Music's **PID** (an event to a process that just quit fails; nothing
    can launch Music), only while Automation is already granted (checked
    without prompting; the prompt is the Settings button's), then re-reads
    and re-reports (Music posts no notification for a volume change).
- **Not built yet:** iTunes album fallback, Plex websocket.

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
away from. `rediscoverClock` is single-flighted by `deviceRediscoverMu` so the
boot check and the periodic probe never browse mDNS concurrently.

Hidden apps (`display_hidden_apps`) are a set toggle, not a config overlay.

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
so it plays during quiet hours; the clock's own `soundEnabled` mute still
applies. These handlers (and display power) go through `internal/awtrix`
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
  `models` keyed by model name, `stale` past `usageStaleTTL`). Before this the
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
  and today's sunrise/sunset **rounded to 5 min** (to the second they'd pin the
  coordinates). No provider call; the coordinates are never echoed.
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
  lat/lon, neighbouring UTC dates included; with no sunrise or sunset it
  follows noon sun altitude, so polar winter reads night). That fallback resets
  the filter. No weather location either: `default` (`day_level`). `night` is
  the schedule's own call (after sunset, before sunrise) whenever a location is
  set, in every source, and can differ from `isNight` across a UTC date change.
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
Agents, Last 7 days, 12 weeks, Work hours, When you focus (weekday × hour
heatmap + 12-week strip), Agent time, Weather. Device hardware health is not
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

### Device registry — `cmd/ember/devices*.go`, `knob_settings.go` (#221)

Per-device tokens so the master `EMBER_TOKEN` never leaves the Mac/server.
Today the only kind is `cinder-knob` (the ESP32-S3 knob in the cinder repo);
the menu app shows one knob, but the registry keys by `hw_id` so re-provisioning
the same board finds its record.

- **Record** (`deviceRecord`): `id` (`knob-` + last 6 hex of `hw_id`; the full
  `hw_id` on a collision), `kind`, `hw_id` (12 hex, `:`/`-` stripped,
  lower-cased), `name` (≤64 chars, default `Knob 61FC8C`), `token_sha256`,
  `pending_token_sha256` + `rotated_at` during a rotation, `config`
  (`knobSettings`), `config_version` (starts at 1), `created_at`, and
  `last_checkin` (`seen_at`, `fw`, `ip`, `rssi`, `heap_internal_free`,
  `heap_internal_largest`, `uptime_s`, `applied_version`).
- **Persistence:** the whole registry plus the epoch is one JSON blob in the
  SQLite settings KV (key `devices_json`), the same store as the overlay
  settings. Every mutation clones the state, persists, then swaps under
  `deviceRegistry.mu`, so a failed write (500) changes nothing. Two hot paths
  skip that (#233): device auth is a locked scan with no clone or write (only
  a rotation promotion writes), and a checkin updates `last_checkin` in place
  and writes the blob only when the last write is `deviceCheckinPersistInterval`
  (10 min) old; a checkin that mints a pending rotation token writes at once.
  If that periodic write fails, the checkin still answers 200 (logged
  `device checkin not persisted`) and the next checkin or the flush retries.
  Any other registry write carries the in-memory checkins along, and graceful
  shutdown flushes them, so a restart shows a recent `last_checkin` (at most
  10 min old after a crash). No store
  (tests, unwritable volume) = in-memory only. If the stored blob fails to
  read or decode at boot, the registry stays empty and refuses every write
  and device auth with 500 until restart, so the blob is never overwritten and
  can be repaired by hand; doctor's `devices` check fails with the error.
  Plaintext tokens are never stored or logged; only the mint response and a
  checkin carrying `new_token` contain one (`Cache-Control: no-store`).
- **Tokens:** `ekd_` + 32 random bytes, base64url (47 chars). Lookup hashes
  the bearer and compares against every record's hashes with
  `subtle.ConstantTimeCompare` (no early exit). `POST /v1/devices` with a known
  `hw_id` re-provisions: new token, old one and any rotation revoked, config
  and version kept, 200 instead of 201.
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
  duplicates), `home` (must name a page that is on), `poll_ms` 1000-10000,
  `bot{sleepy_after_s 0-86400 (0 = never), demo_hold_s 1-600, source_label,
  working_ring}` (the two booleans, cinder#42: the curved host label and the
  glint orbiting the outline while working; absent or null = true, so a
  record stored before #282 keeps both on with no config push),
  `display{fast_link}` (cinder#23: the knob's panel QSPI link at 80 MHz,
  absent or null = true like the bot flags; the knob reboots to apply a
  change and falls back to 40 MHz by itself after a failed link check),
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
  config change, rotate, rotation promotion and delete; checkins don't move it. The knob checks in
  when it changes, else every 60 s, so a settings edit lands in about one
  `/state` poll without putting per-device data in a public response.
- **Doctor:** `devices` check lists each record's last-checkin age; warns
  when one never checked in or is silent for more than 5 min.
- **App (Settings › Knob, #222/#223):** EmberKit `Knob/` holds the protocol
  and models, no UI. `ImprovCodec` (Improv Serial frames + checksum; the host
  appends `\n` after each frame) and `CinderLineCodec` (`CINDER1 {json}` lines,
  sorted keys) are pinned by the shared vectors in
  `macos/Tests/EmberKitTests/testdata/knob/` that cinder's firmware tests can
  reuse. `KnobStreamDemuxer` splits the port's bytes into frames, `CINDER1`
  lines and log lines. `KnobLink`/`KnobLinkOpener` is the transport seam (USB
  serial now, BLE later); `SerialPortLink` opens `/dev/cu.*` raw
  (`O_NONBLOCK`, `cfmakeraw`, `HUPCL` cleared first) and **never touches
  DTR/RTS**: toggling RTS resets the ESP32-S3. It takes the port exclusively
  (`TIOCEXCL` + `flock`, like pyserial's `exclusive=True`); a busy port shows as
  "couldn't open". `KnobSerialPorts` watches IOKit for `303a:1001` and keys a
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
  real serial port.
- **Knob page previews (#240):** Settings › Knob › Apps draws each page's
  466 px round face in SwiftUI (EmberKit `KnobFace/`), the way `PanelPreview`
  shows the clock's frames, but drawn locally: the knob renders on-device and
  its view endpoint takes a device token only. The app composes the same
  inputs from what it already polls: `/state` sessions (mood and host label,
  `KnobMood`, the firmware's render priority and #213 host rule; the last
  good mood stays while `/state` fails, as on the knob),
  `/v1/pomodoro/state`, `/v1/weather/state` (tracked while a preview is up)
  and `/v1/display/brightness` (read at once when "Follow Ember brightness"
  turns on, then every 60 s), plus the draft knob settings, so toggling a
  page or a bot timing shows at once. The renderers port cinder's firmware
  (`bot_shape.c`, `bot_view.c` Head style with its plain 6 px outline and
  squash variants picked by sy/sx so the pop only pulses the eyes,
  `pomo_view.c`, `weather_face.c`, `weather_scene.c`, and `nowplaying_view.c`
  for the now-playing page, #284: backdrop disk, ring, album, artist avatar
  riding the ring, three text lines, folded to ASCII like `np_text_fold`;
  `KnobNowPlayingFeed` polls `/v1/nowplaying/state` and fetches the three
  pictures from `/v1/nowplaying/art` once per `art_version`, only while a
  preview is up; burn-in dimming and drift are not drawn; text folds with the firmware's own tables, `KnobTextFold`, tested against np.c; failed picture fetches retry with a 5-60 s back-off; Knob > Now Playing is its own app page from firmware 0.9.0; `GET /v1/nowplaying/state` sends `X-Ember-Now` for clock skew); `BotBehavior.Tuning`
  carries the knob's slower, flatter hop. `knob-theme.json`
  (`macos/Sources/EmberKit/Resources/`) holds the numbers the renderers
  share with the firmware: geometry, palette, label layout and LVGL font
  metrics, eye geometry, and the weather scene's sprite sizes, layout,
  particles and timing. The sprite outlines themselves (cloud, moon, rays…)
  and the hop squash curve stay in code on both sides. `KnobThemeTests` pins
  every key; with a cinder checkout next to this repo (or `CINDER_DIR`) it
  also parses the firmware source and checks each key against the constant
  it mirrors. tarakanof/cinder#34 tracks generating the C side from a copy.
  Labels use the bundled Montserrat Medium (SIL OFL 1.1,
  `Montserrat-OFL.txt` next to it), the font LVGL's built-in sizes are made
  from, placed by LVGL's line box (top, line height, baseline). The bot
  redraws only when its pose changes: 20 fps while `BotBehavior.isAnimating`,
  otherwise the next frame is scheduled at `nextEventAt`. Weather runs at the
  firmware's frame rate, the Pomodoro ticks once a second, and only while
  the pane is on screen; Reduce Motion keeps blinks only and holds the sky
  still. The Pages overview has no timers: it redraws when the polled data
  changes. `KNOB_SNAPSHOT_DIR=… swift test --filter knobFaces` writes PNGs
  of every face.

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
| `reset_reason` | string `^[a-z][a-z0-9_]{0,15}$` | basic | Why the chip last restarted: `poweron`, `ext`, `sw`, `panic`, `int_wdt`, `task_wdt`, `wdt`, `deepsleep`, `brownout`, `sdio`, `usb`, `jtag`, `efuse`, `pwr_glitch`, `cpu_lockup`, `unknown` (ESP-IDF `esp_reset_reason`, lower-cased without the `ESP_RST_` prefix). |
| `req_ok` | int, count | full | HTTP requests to Ember that got an answer (any 2xx/304) during the window. |
| `req_fail` | int, count | full | Requests that failed (transport error, timeout, non-2xx/304) during the window. |
| `req_ms_avg` | number, ms | full | Mean request duration over the window. |
| `req_ms_max` | int, ms | full | Slowest request in the window. |
| `fps` | number, frames/s | full | Rendered frames per second over the window. |
| `frame_ms_avg` | number, ms | full | Mean time to render a frame. |
| `frame_ms_max` | int, ms | full | Slowest frame in the window. |

Counts and averages are **per window, reset after each report** (not
since boot). Example (full):
`"stats":{"period_ms":60000,"cpu_pct":[12,40],"heap_internal_min":30000,"psram_free":7000000,"psram_min":6500000,"psram_largest":6000000,"temp_c":41.5,"reset_reason":"poweron","req_ok":28,"req_fail":2,"req_ms_avg":35.5,"req_ms_max":120,"fps":29.5,"frame_ms_avg":12.25,"frame_ms_max":40}`.
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
`period_ms`-weighted mean; a bucket's `t` is its newest report. Counts become
rates on arrival (`(req_ok+req_fail)·60000/period_ms`). Both rings grow as
samples arrive (never past their cap) and give their buffer back when it
falls under a quarter full, so a device costs what it reports: about 380 KB
for a day at 30-60 s, 245 KB at 120 s, 125 KB at 300 s, plus 20/37/87 KB at
10/5/2 s while a live session is in the window (released as it ages out). Deleting the device drops its series.

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
`render_fps`, `frame_avg_ms`, `frame_max_ms`, `brightness_level` (Ember's
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
diagnostics are off, with a footnote on the trade-off (#249); Status has
"Show Hardware". See "Hardware pages" below. The knob page's lines break
after three missed reports at the range's spacing or the knob's
`stats_interval_s`, whichever is longer. The gap uses the current interval
for every point, so after a change from 300 s to 30 s the older 5-minute
points draw as dots until they leave the range.

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
writes PNGs of both pages, light and dark, and quits; it skips server,
producers and USB; build it with another `PRODUCT_BUNDLE_IDENTIFIER`).

### Config load and `/admin/reload`

Resolve order: the `-config` flag, `CONFIG_PATH`, `./config.json` if present,
else defaults only. Failures wrap the sentinels `ErrConfigRead`, `ErrConfigParse`
and `ErrConfigValidate`. Unknown JSON fields are a parse error
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
Pomodoro engine and re-applies persisted overlay settings, and starts
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
  | `POST /v1/devices/self/checkin`, `GET /v1/devices/self/{config,view}` | device token only |
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
  stored on the record's `last_checkin` and shown as "Display link")
  (every field optional; `ip` must parse when present, else the remote address
  is recorded; `fw` ≤32 chars). Answer: `{"config_version":7}` when the
  reported version is current, plus `"config":{…}` when it isn't, plus
  `"new_token":"ekd_…"` while a rotation is open, plus `"diag_live_until"`
  in live mode; an optional `stats` object carries diagnostics (both in
  "Knob diagnostics"). `GET
  /v1/devices/self/config` answers `{"config_version":7,"config":{…}}`.
- **Knob view (device token, #234).** `GET /v1/devices/self/view` is the
  knob's one poll (`devices_view.go`): everything it shows, about 400 B with
  weather, versus four endpoints and ~4 KB before. Typical body (409 B, field
  order fixed):
  `{"v":1,"epoch":1,"config_version":1,"mood":{"waiting":1,"errors":0,"running":1,"done":0,"source":"M4"},"pomo":{"phase":"focus","running":true,"paused":false,"ends_at":1782044100,"planned_sec":1500,"round":0},"weather":{"provider":"open-meteo","cond":"rain","code":"61","temp_c":12.5,"stale":false,"severe":false,"night":false,"sunrise":1782013200,"sunset":1782072900},"brightness":{"level":255,"night":false}}`.
  `mood` is `/state`'s `render` counters and `source` (the winning host),
  then who leads the winning state (#282, `knob_lead.go`): `lead` (the source
  with the most sessions in that state, ties to the smaller name, so it does
  not flip as sessions heartbeat; left out when it equals `source`), `hosts`
  (distinct sources in that state; left out at 0 or 1), `lead_color` (first
  valid `source_color` of the lead's sessions, uppercased) and `tool` (the
  lead's tool when its sessions agree), all omitted when empty, so an idle
  view's bytes do not change. `Upsert` also notifies when this summary moves
  without the render (a new source colour);
  `pomo` is null with the Pomodoro off, and carries `ends_at` (server Unix
  seconds) while counting down, `remaining_sec` otherwise (paused, parked,
  idle), never both; `weather` is null when disabled or never fetched,
  `night` is the sun schedule's call (`sunLevel`, neighbouring UTC dates
  merged, so a western evening after UTC midnight is still day until its
  sunset; false without a location) and is what the knob should use;
  `sunrise`/`sunset` are informational Unix seconds rounded to 5 min for the
  location's own date (the observation's UTC offset, else the longitude's
  hour; null without a location or in polar day/night), `stale` as in
  `/v1/weather/state`;
  `brightness` is the read-only `/v1/display/brightness` level and `night`.
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
  notifies config + Pomodoro after its engine resync). `notify` takes only
  its own lock and never blocks, so callers may hold theirs. Fields that move
  with the clock alone (sun-schedule brightness, weather going stale, live
  mode ending) are caught by a 5 s recheck inside the wait. Topics are bits
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
| `context_pct` | statusline `context_window.used_percentage` | rollout token_count | transcript heuristic was removed (over-read) |
| `rate_window_pct` (5h) | statusline `five_hour.used_percentage` | rollout `rate_limits.primary.used_percent` | |
| `rate_reset_at` | statusline `…five_hour.resets_at` | rollout `…primary.resets_at` | epoch secs; countdown computed at render time (TZ-independent) |
| `activity` / trail | hooks (`Tool: detail`, outcome suffix from PostToolUseFailure / PermissionDenied) | rollout (`exec:`/`edit:`/`web:`/`mcp:`) | shared `PrependTrail` ring buffer; `AnnotateTrail` marks an item's outcome |
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
snapshot. The endpoint percent is rounded, hence the 99.5 % threshold, and the
alarm fires a minute after the estimated reset because reset estimates drift.
Disabling the alarm drops armed state so re-enabling hours later cannot fire a
stale "reset" popup, while fired entries are kept so a past `resets_at` cannot
re-fire. `UsageStore` is not persisted: entries refresh at most every 5 min and
a restart self-heals within one interval. Gated only by `limit_alarm` (usage config, default on); deliberately
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
`clockPublisher` is ungated and only `NewApp` builds it, wrapped at once
(`clock_access_guard_test.go` enforces this), so a new sound source needs no
per-feature check. The menu's explicit `/v1/device/audio/test` bypasses the gate
on purpose. The gate's `now` func must return wall-clock local time, because
`quietActive` reads `Hour()`/`Minute()` with no zone conversion.

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
- **Launch at login** uses `SMAppService.mainApp` (System Settings › General ›
  Login Items). It needs the app signed and in `/Applications` to register fully,
  and a first registration may report `.requiresApproval`.

### Producer / deploy
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
  then registers) and checks again 30 s after an update reconcile. The
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
