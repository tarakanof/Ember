# Runbook

How to build, deploy, install, and verify `ember`. For the system
design see [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Build & test

Local Homebrew Go toolchain; do **not** set `GOROOT` manually (let `go env
GOROOT` resolve to Homebrew's `libexec`).

```sh
go test ./... -race
gofmt -w <files>
go vet ./...
go run ./cmd/ember -config config.example.json
```

If a sandboxed shell can't write the default build cache, use a repo-local one:
`GOCACHE="$PWD/.gocache" go test ./...`. In a shell with a stale
`GOROOT=~/.go`, prefix with `unset GOROOT &&`.

## Local server deployment (dev Mac, OrbStack)

Docker is available via OrbStack; `docker buildx`, `scripts/image-smoke.sh`, and
ad-hoc `docker run` all work locally (no remote Docker host needed). The live
deployment is an ad-hoc `docker run` (no compose file) — its exact flags are
captured in `docker inspect ember`.

```sh
# build (from a normal checkout, NOT a worktree — see ARCHITECTURE gotchas)
docker buildx build -t ember:local .

# run: token via env, config bind-mounted, Pomodoro DB on a named volume
docker run -d --name ember --restart unless-stopped -p 3627:3627 \
  -e EMBER_TOKEN="$(cat ~/.config/ember/token)" \
  -v ~/.config/ember/config.json:/etc/ember/config.json \
  -v ember-pomodoro:/var/lib/ember \
  ember:local
```

- `config.json` carries device URL, `app_name` (`ember`), publish timeout.
  - **The server pushes its main status display to the device under
    `awtrix.app_name`.** If the clock's rotation shows an unexpected app name
    (e.g. a pre-rebrand `ai_status`), that's almost certainly a stale `app_name`
    in `config.json` — not a stray external publisher. Fix the name and restart;
    don't go hunting MQTT/other hosts. (`config.json` is a single-file bind
    mount — edit then `docker restart ember` to apply.)
- Token: `openssl rand -hex 32 > ~/.config/ember/token`. **`EMBER_TOKEN` is
  mandatory:** auth fails closed, so with it unset every `/v1` write (and every
  `/admin` call) returns 401 — read endpoints (`/state`, `/healthz`, previews)
  stay open. If writes suddenly 401 after a deploy, check the env var first.
- **mDNS discovery needs host networking.** The `-p 3627:3627` form above is fine
  for everything except clock/server discovery (multicast doesn't cross the
  bridge). For the discovery features, run with `--network host` and drop `-p`
  (the production/Unraid path; see "Discovery & mDNS" and "Docker Hub release").
- **When recreating the container, `docker inspect` it first** to replicate
  exact mount destinations / env names rather than reconstructing from memory.
- **After any render-side change, rebuild + redeploy from current `main`** —
  producer↔server version skew shows nothing rather than erroring (see
  ARCHITECTURE).
- Verify in-container: `docker exec ember /ember doctor`
  (expect all `[OK]`).

## Producer install / uninstall

**Claude** (per-Mac, hooks merged into the GLOBAL `~/.claude/settings.json` +
the heartbeat/statusline LaunchAgents):

```sh
go install ./cmd/ember-claude-producer   # rebuild the binary in ~/go/bin
ember-claude-producer install            # registers hooks + LaunchAgent
ember-claude-producer uninstall
```

`producer.env` (`~/.config/ember/producer.env`) holds: `source`,
`EMBER_SERVER_URL`, token, `EMBER_SOURCE_COLOR`, and the `EMBER_*` toggles
(below). The producer re-reads it each tick — no restart needed.

> **Gotcha:** the auto-mode classifier blocks a chained `go install + install`
> Bash call (it writes the global settings.json). Run them as **separate**
> commands, or the build silently doesn't happen and `install` uses a stale
> binary. `go install` alone is allowed; since the LaunchAgent/hook paths point
> at `~/go/bin`, rebuilding in place is usually enough — no re-`install` needed.

**Codex** daemon: installed as LaunchAgent `com.ember.codex`
(`KeepAlive=true`). Restart with `launchctl kickstart -k gui/$UID/com.ember.codex`.

**Menu app** (native SwiftUI, `macos/` — replaces the retired Go menu): generate the Xcode project and run it.

```sh
brew install xcodegen                                   # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/Ember.xcodeproj                          # pick your Dev team, then ⌘R
swift test --package-path macos                          # headless EmberKit tests
```

The generated `Ember.xcodeproj` is gitignored — regenerate it after pulling.
Launch-at-login is in-app (App tab → `SMAppService`), not a LaunchAgent. The app
reads `producer.env` for connection config and needs a server on a build that
includes `GET /v1/preview` (added 2026-05; older servers 401 that route).

The app bundle now builds + signs the two producer helpers (`ember-claude-producer`,
`ember-codex-producer`) and their LaunchAgent plists into `Contents/MacOS` and
`Contents/Library/LaunchAgents` via a `postCompileScripts` phase
(`scripts/build-producers.sh`) that runs before Xcode's own app-level codesign, so
signing happens inside-out. `CODE_SIGN_IDENTITY` picks Developer ID for release
builds vs ad-hoc (`-`) for local dev; notarizing the `.app` covers the nested
helpers, no separate step needed. `scripts/verify-bundle.sh <Ember.app>` is the
gate for this contract (universal binaries, valid plists, codesign) — run it
after any build that touches the producers or the bundling phase.

The menu dropdown also runs the Pomodoro (Start/Pause/Resume/Skip/Stop) and has
per-app clock toggles (`PUT /v1/apps`). Settings → Pomodoro exposes the focus
duration (to 8h), the auto-stop cap ("Auto-stop after: N h", `0` = off), and
colours; Settings → App picks the Dock/app icon + the menu-bar tray glyphs.

Settings is a **sidebar window** (Connection / **Device** / **Agent** /
Pomodoro / Weather / Reminders / App), opened with ⌘, or the menu's "Settings…"
item, and it **auto-applies** changes — there are no Save buttons. The **Agent** tab
(formerly "Code agent"/"Display") groups by scope: per-Mac card/bar toggles write
`producer.env` immediately (each with a pixel-art pictogram of what it adds to
the 32×8 matrix, plus a three-way Bottom bar picker), while the behavior group
(hide-when-idle, attention hold, attention chime) and the usage group
(usage card, threshold, per-model, limit reset alarm) debounce server `PUT`s; Connection commits each
text field on **Return or focus loss** (an invalid field shows a red caption and
isn't written, leaving the others intact) and "Test Connection" lives in the
toolbar; Pomodoro/Weather/Reminders each debounce a single `PUT` ~600 ms after
the last edit; colours use the native macOS colour picker (bridged to `#RRGGBB`).
The source-colour toggle/picker stay disabled until Source + Server URL are set
(the tint write validates all fields together).

The **Connection** tab also lists **Discovered servers** (Ember servers found via
Bonjour/`_ember._tcp`); tapping one fills the Server URL. When the list is empty it
offers a **Grant Local Network Access…** button (macOS gates Bonjour browsing
behind the Local Network privacy permission) + **Rescan**. The **Device** tab
speaks the awtrix-ng schema directly (General / Native Apps / Time & Date /
Actions), proxied through the server (`/v1/device/*`) — brightness, volume,
app time, transitions (the picker is fed by `GET /v1/device/capabilities`
instead of a static list), native-app toggles, calendar colours, sensor
calibration (temp/hum offsets, written via a read-merge-PUT of
`/api/v1/system`; **applies live, no reboot** — unlike the old AWTRIX3
`dev.json` contract), Reboot / Dismiss, buttons (enabled with a one-click
`PUT /v1/device/buttons`) — plus a **Discover clocks** picker (mDNS) to choose
which awtrix-ng clock the server drives. Time and date are discrete typed
fields (no format strings to fill in, unlike AWTRIX3's `TFORMAT`/`DFORMAT`),
and sensor edits apply immediately. The **App** tab's *About* shows the app
version + the connected server's `/version`.

The **Weather** tab edits `weather` config (provider Open-Meteo/MET Norway,
lat/lon, units, the rotating-tile + popup behaviour, severe-alert sound); the
**Reminders** tab edits the alarm list (timezone, per-item time/text/weekdays/
sound). Both persist server-side (`/v1/{weather,reminders}/config` → the SQLite
store) and survive `/admin/reload`. Weather/reminders run regardless of whether
Pomodoro is enabled — but they still write to the Pomodoro `db_path` store, so a
deploy that wants them persisted needs that writable volume mounted.

## Device button → Pomodoro control

The clock's three physical buttons drive the timer via NG's `buttonCallback`
(an HTTP POST per press; no MQTT broker needed). The easiest path is the menu
app's Device tab (one-click `PUT /v1/device/buttons`, `{"enabled":true}`),
which computes the server's own reachable URL automatically. To set it by
hand instead:

```sh
curl -X PUT http://<hostname>.local/api/v1/system \
  -H "Content-Type: application/json" \
  -d '{"buttonCallback": "http://<mac-lan-ip>:3627/hooks/awtrix/button"}'
```

- Use the **Mac's LAN IP** (where the container publishes `:3627`) — *not*
  `localhost`; it's DHCP, so a reservation keeps it stable. Always
  read-merge — a naive partial PUT to `/api/v1/system` risks dropping the
  device's Wi-Fi credentials (this is exactly what `handleDeviceButtonsPut`
  does under the hood).
- `/api/v1/system` writes apply **live, no reboot** (unlike AWTRIX3's
  `dev.json`, which only took effect at boot).
- Ember's `pomodoro.button_callback` config bool must be `true` (default).
- The Pomodoro view uses **on-device NG animated icons** (`/ICONS/29802.gif`
  tomato = focus, `/ICONS/6396.gif` coffee = break). Ember provisions them
  itself (`ensureNativeIcons`) if missing — no manual upload needed.
- Mapping (press-down only): **middle/select** = pause / resume /
  start-from-idle, **right** = skip, **left** = stop. The AWTRIX3-era
  left+right chord is removed (#81). While a timer runs Ember sets
  `blockNavigation:true` + `autoTransition:false` (`PATCH /api/v1/settings`)
  so the buttons drive the timer instead of switching apps, restoring both on
  stop. NG documents — and Ember has verified on firmware 1.0.13 — that a
  configured `buttonCallback` does **not** consume the press (the buttons keep
  their normal firmware job), and that select/middle self-dismisses a showing
  notification even under `blockNavigation:true` — the AWTRIX3-era "won't
  self-dismiss while a callback is configured" gotcha does not hold on NG.
- **Reboot recovery:** a running timer takes over the display
  (`blockNavigation:true`/`autoTransition:false` + a forced
  `PUT /api/v1/apps/active`). Pushed apps and both settings are RAM-only on
  NG, so a reboot silently drops them; recovery now rides the shared
  device-watch loop (30s probe, detects the reboot via falling
  `uptimeSeconds`, triggers a full republish) rather than a Pomodoro-specific
  re-assert timer. The Berry boot-ping hook
  (`POST /hooks/awtrix/boot`, config toggle `awtrix.boot_ping`, default off)
  makes this near-instant — ~12s from reboot to republish, measured — with
  the 30s watch as fallback.
- Smoke-test the server half without the device:
  `curl -X POST -d "button=select&state=1" http://localhost:3627/hooks/awtrix/button`
  (should start a focus).

## `EMBER_*` toggle reference (the "spine" flags)

Each is a render-opt-in boolean (default off unless noted). They gate the wire
field the producer sets, not the data capture. See ARCHITECTURE → "spine".

| Env var | Effect |
|---|---|
| `EMBER_CONTEXT_PCT_ENABLED` (default true) | context glass fill |
| `EMBER_CONTEXT_NUMBER_ENABLED` | **no-op since 2026-06** (single-app display rework); safe to delete from `producer.env` |
| `EMBER_RATE_PCT_ENABLED` (default true) | **no-op since 2026-06** (single-app display rework); safe to delete from `producer.env` |
| `EMBER_RATE_BOTTOM_BAR` | 5h rate as the row-7 bottom bar |
| `EMBER_RATE_RESET` | **no-op since 2026-06** (single-app display rework); safe to delete from `producer.env` |
| `EMBER_ACTIVITY_DETAIL_ENABLED` | scrolling `Tool: detail` + `cardTool` |
| `EMBER_ACTIVITY_TRAIL_ENABLED` | last-N actions ticker (extends detail) |
| `EMBER_SOURCE_COLOR` | hex body colour for the 8×8 icon + source-name card digits |
| `EMBER_SOURCE_CARD` (default true) | source-name card (uppercased, 4 glyphs) in the number slot; set false to hide |
| `EMBER_SESSION_BAR` (default true) | session-pixel bar on row 7 (1 px per non-idle session); set false to hide |

## Meetings calendar widget

`EMBER_MEETINGS_ICS_URLS` — comma-separated ICS feed URLs (the meetings widget is
inert without this var). `webcal://` and `webcals://` are accepted and rewritten
to `https://`. These are **credentials** (possession = calendar read access):
they are never stored in the JSON config, the SQLite store, any log, or any API
response. The feed count (never the URLs) is visible at `/admin/doctor` and the
`GET /v1/meetings/config` response (`ics_urls_configured`).

**On-device verification.** Set `EMBER_MEETINGS_ICS_URLS`, restart the container,
enable the widget in Settings → Meetings. Within `tile_lead_minutes` of the next
meeting the `ember-meet` tile appears in the rotation. A stale feed (last
successful fetch ≥ 60 min ago) shows as a `[WARN]` in `/admin/doctor` — non-fatal,
does not affect other checks.

## AI usage card (threshold-gated, inside the main app)

Claude + Codex subscription usage renders as a **usage card** inside the main
`ember` app — no standalone `ember-usage-*` apps. The card appears in the
number-slot rotation **only when the tool's 5h window ≥ `usage_threshold_pct`**
(default 60; 0 = always show). Startup clears any legacy `ember-usage-*` apps
left on the device.

Unlike the spine flags above, its toggles are **server config (JSON), not
`EMBER_*` env vars** — both default **on**:

| Config field (`/v1/usage/config`) | Effect |
|---|---|
| `usage_widget` (default true) | master enable for the usage card |
| `usage_per_model` (default true) | Claude Opus/Sonnet weekly faces (`OP`/`SO`) |
| `usage_threshold_pct` (default 60) | 5h % floor to show the usage card; `0` = always |
| `limit_alarm` (default true) | auto-dismiss popup + chime when a 5h window resets (see below) |

Runtime overrides for all fields: `GET/PUT /v1/usage/config` (bearer auth,
store key `usage_json`). Partial PUT bodies are safe — missing fields keep their
current values (pre-seeded from the live config before decode).

Per-tool show/hide reuses the existing per-app visibility (`PUT /v1/apps`) — hide
`claude` or `codex` to drop its usage card faces too.

**5h limit-reset alarm.** When a tool's 5h window reaches ≥99.5% with a known
future reset time, the coordinator arms an alarm. Once the reset (+60 s grace)
passes it fires one auto-dismiss popup ("CLAUDE 5H RESET" / "CODEX 5H RESET",
drawn tool icon, 10 s) and an RTTTL chime. If fresh data shows the reset
estimate drifted to a later time, the alarm re-arms rather than fires; if the
device is unreachable it retries next tick. State is in-memory: a restart
mid-window simply re-arms from the next snapshot. Deduped per (tool, resetAt).
Toggle with `limit_alarm` above.

## Display behavior config

Runtime display knobs: `GET/PUT /v1/display/config` (bearer auth, store key
`display_json`). `config.json` is the baseline; a PUT overrides and persists to
the SQLite store, surviving restarts and `POST /admin/reload`.

| Field | Range | Default (config.json) | Notes |
|---|---|---|---|
| `idle_hide_minutes` | 0–60 | 2 (was 20 in older configs) | minutes until the dimmed-idle frame stops publishing; **0 = hide immediately** (no dim phase) |
| `attention_hold_seconds` | 5–300 | 30 | how long the coordinator holds the attention lock before auto-releasing; read live, so a PUT applies to the current lock |
| `attention_chime` | bool | false | plays a short RTTTL note on every fresh waiting/error lock acquisition |

`config.json` validation enforces `idle_restore_seconds` in [60, 3600] (whole
seconds, no zero-hide); the runtime DTO exposes the same knob as whole minutes
(0–60) with 0 meaning immediate hide. Existing explicit `config.json` values are
unaffected by the default change — only bare/default installs change.

## Discovery & mDNS

The server finds the awtrix-ng clock on the LAN by mDNS (browse the
awtrix-ng-specific `_awtrixng._tcp` service, not a generic `_http._tcp` sweep)
with a `FIND_AWTRIXNG` UDP broadcast fallback (broadcast `:4210`, replies
collected on a fixed `:4211`) for networks where multicast doesn't make it
through; a resolved host is fingerprinted via `GET /api/v1/device`, requiring
both a non-empty `uid` and `boardType == "awtrixng"` (the AWTRIX3 `/api/stats`
fingerprint doesn't exist on NG). The server advertises itself as
`_ember._tcp` so the macOS app can auto-fill the server URL (Connection tab →
"Discovered servers"). The Device tab proxies the clock's own NG API through
`/v1/device/*`.

- **Host networking is required** for either direction — multicast doesn't cross
  the default Docker bridge. Run the container with `--network host` (or macvlan).
- Effective clock URL precedence: writable-store override (menu's Device tab) >
  reachable `awtrix.http_base_url` from `config.json` > mDNS auto-pick (in-memory;
  never written back to the read-only config or the store).
- **Self-healing:** the server reachability-tests the effective URL (store
  override included) at boot and every 30s via a background probe
  (`awtrix.auto_rediscover`, default on); an unreachable URL falls through to a
  fresh mDNS auto-pick without touching `config.json` or the store, so a
  clock's IP changing (DHCP renumbering) recovers on its own within ~30s. The
  same tick reads the clock's `uptimeSeconds` to detect a reboot and triggers a
  full republish of every pushed app — pushed apps are RAM-only on NG, so a
  reboot silently drops them all. The Berry boot-ping hook (#73)
  (`POST /hooks/awtrix/boot`, unauthenticated device-side hook, config toggle
  `awtrix.boot_ping`) republishes instantly on boot instead of waiting on the
  next 30s tick — the 30s watch remains the fallback path.
  `/admin/doctor`'s `clock` check reports `base_url`/`source`/`reachable` plus
  `last_rediscover_at`/`last_rediscover_result`.
- `EMBER_MDNS_ADVERTISE` (default on; `0`/`false` disables) gates only the
  advertising side; clock discovery and the Device tab still work with a
  configured URL.
- **Troubleshooting — clock dark after its IP changed:** the server self-heals
  within ~30s (mDNS). To apply the new IP now, restart the container (re-runs boot discovery) or
  `PUT /v1/device/config {"base_url": …}`; `/admin/doctor` shows the clock's
  reachability + last re-discovery. A DHCP reservation avoids the whole
  problem.

**Data sources & dependencies:**
- **Claude (always-on):** the claude producer daemon polls
  `api.anthropic.com/api/oauth/usage` every ~5 min using the OAuth token in the
  **macOS login Keychain** (item `Claude Code-credentials`). Requires the user to
  be logged into Claude Code on that Mac; the token is **read-only, never
  refreshed**. On 401 (or any other non-200/transient error) the poller just
  skips that tick and keeps polling every ~5 min — it never stops the loop —
  until the user re-auths in Claude Code.
- **Codex (session-only):** posted from the rollout stream while a Codex session
  is active; usage card faces clear ~10 min after the last session.
- **Claude 5h fallback:** when the endpoint usage is stale/absent (daemon idle or
  401) but a Claude session is live, the 5h face is synthesised from the
  statusline `rate_window_pct` + host-local `rate_reset_label` (the statusline
  posts the label so the UTC server renders it verbatim). 7d + per-model have no
  fallback — they appear only with fresh endpoint data.
- Entries are in-memory; stale tools (no post within ~10 min) are cleared from
  the usage card automatically.

**Verify it's flowing:** `GET /state` does not include usage (it's a separate
store), but posting a crafted usage payload:
`curl -s -XPOST localhost:3627/v1/usage -H "Authorization: Bearer $EMBER_TOKEN" \
  -d '{"tool":"claude","source":"endpoint","five_hour":{"used_percent":75,"reset_label":"14:25"}}'`
then watching the clock's `ember` app show the usage card face in rotation confirms
the push path (threshold default is 60 — post ≥60% to trigger it). Toggle a tool
off via `PUT /v1/apps` and confirm its usage faces disappear.

## On-device verification (no waiting for real data)

1. **Read the live matrix:** `GET http://<hostname>.local/api/v1/display/screen`
   returns `{"width":32,"height":8,"pixels":[256 ints]}` (awtrix-ng wraps the
   framebuffer; AWTRIX3 returned the bare 256-int array). Ember proxies this
   raw at `GET /v1/device/screen` and the macOS app unwraps it to mirror the
   display. Render the pixels as ANSI 24-bit colour blocks in the terminal to
   confirm pixels paint.
2. **Drive a widget to a chosen value:** `POST /v1/status` (token from
   `producer.env`) a crafted session (e.g. rate 75% → amber ⅔ bar, ctx 88% →
   near-full glass). Keep it alive with a re-POST loop faster than the
   running-staleness reap (`stale_seconds`, default 300); `DELETE` when done.
3. **Hook-free observation:** a background shell loop polling `/state` doesn't
   fire Claude hooks, so it's a clean observer of session age. Age sawtoothing
   ~5–10 s = heartbeat healthy; monotonic climb to the stale window (default
   300 s) then ABSENT = nothing refreshing it.
4. Before assuming a rebuild is needed, check `GET /version` (server revision)
   and the installed producer binary mtime — sometimes flipping `producer.env`
   toggles is the only step.
5. **Display hold / re-push behavior (firmware 1.0.13, measured):** a forced
   `PUT /api/v1/apps/active` plus `durationMs == lifetimeMs` sustains a hold;
   `apps/active` 404s if the named app hasn't been pushed yet, so the switch
   must strictly follow a successful push. Re-pushing the same app is
   idempotent — blink phase and dwell are both unaffected — so the
   coordinator's dedupe is a network/device-load nicety, not something
   correctness depends on. At `lifetimeMs` the app is deleted outright
   (`lifetimeExpiry` default `"remove"`), which is what returns the display to
   native rotation crash-safely. The device's own app rotation needs at least
   two enabled apps to actually rotate.

## awtrix-ng flashing, backup, and first-boot

Covers moving the TC001 from stock AWTRIX3 to [awtrix-ng](https://github.com/Blueforcer/awtrix-ng).

1. **Backup the flash BEFORE any firmware change.** This is the only way back to
   AWTRIX3 besides re-flashing via its own web flasher
   (<https://blueforcer.github.io/awtrix3/>) — take it.

   ```sh
   esptool.py --port /dev/cu.usbserial-* read_flash 0x0 0x400000 tc001-backup-$(date +%Y%m%d).bin
   ```

   Store the `.bin` off-device (not just on the flashing Mac). Restore with:

   ```sh
   esptool.py --port /dev/cu.usbserial-* write_flash 0x0 tc001-backup-YYYYMMDD.bin
   ```

2. **Flash awtrix-ng.** USB web flasher at
   <https://blueforcer.github.io/awtrix-ng/> (Chrome or Edge — WebSerial only).
   For the first install, choose **full-erase**: AWTRIX3 settings are **not**
   preserved across the switch.

3. **First boot / Wi-Fi join.** The device opens an **open** (unsecured) access
   point named `awtrixng-<mac6>`. Join it, then either let the captive portal
   pop up or browse to `http://192.168.4.1`. System tab → **Scan**, pick the
   target SSID, set the password and a **deliberate hostname** (used for
   `.local` discovery below). Save, then reboot via the blue banner. The
   assigned IP scrolls across the matrix on boot; once joined the device is
   reachable at `http://<hostname>.local`.

4. **Baseline config after joining the network.** All via
   `PUT /api/v1/system`, `Content-Type: application/json`:

   ```sh
   curl -X PUT http://<hostname>.local/api/v1/system \
     -H "Content-Type: application/json" \
     -d '{"tzName": "Europe/Belgrade"}'
   ```

   Set at minimum:
   - `tzName` — factory default is already `Europe/Berlin`; only change if the
     clock lives elsewhere.
   - `buttonCallback` → `http://<ember-server>:3627/hooks/awtrix/button`.

   > **Warning:** `tempOffset` factory default is already **−9.0** for the
   > TC001. Do not re-apply a manual offset on top of it — that double-corrects
   > and the on-device temp reads low.

   System writes via `/api/v1/system` apply **live** — no reboot needed
   (unlike AWTRIX3's `dev.json`, which only applies at boot).

5. **Later firmware updates.** NG has dual-slot OTA — settings, icons, and
   scripts survive it. Via Web UI: System → Maintenance. Via curl:

   ```sh
   curl -F "firmware=@firmware-awtrix-ng.bin" http://<hostname>.local/update
   ```

   Use the **plain** (non-`s3`) binary for the TC001.

6. **Quick verify:**

   ```sh
   curl http://<hostname>.local/api/v1/device
   ```

   Expect `"boardType": "awtrixng"` and your chosen hostname in the response.

## Docker Hub release

Releases are published on strict-semver `vX.Y.Z` tags (latest: `v0.2.0`),
public + multi-arch. **A redeploy is release-driven: there is no `latest`-from-`main`
auto-build** — you cut a Release, the workflow builds + pushes the image, then you
repoint the container. The release workflow
(`.github/workflows/docker-publish.yml`) fires when a strict-semver `vX.Y.Z`
GitHub Release is published (or a `workflow_dispatch` on a `vX.Y.Z` tag ref) →
multi-arch Docker Hub push (SBOM + provenance).
Requires repo var `DOCKERHUB_USERNAME` + secret `DOCKERHUB_TOKEN` — **set the
token newline-safe** (`printf '%s' val | gh secret set DOCKERHUB_TOKEN`); a
trailing newline causes a `malformed HTTP Authorization header` login failure.

To cut a release + repoint the live container:

```sh
gh release create vX.Y.Z --target main --title vX.Y.Z --notes "…"   # triggers the workflow
gh run watch <run-id> --exit-status                                  # multi-arch build ~2 min
docker pull dtarakanov/ember:X.Y.Z
# recreate with HOST networking (required for mDNS discovery — see "Discovery
# & mDNS"); drop any -p 3627:3627 mapping and make sure :3627 is free on the host.
docker rm -f ember && docker run -d --name ember --restart unless-stopped \
  --network host \
  -e EMBER_TOKEN="$(cat ~/.config/ember/token)" \
  -v ~/.config/ember/config.json:/etc/ember/config.json \
  -v ember-pomodoro:/var/lib/ember \
  dtarakanov/ember:X.Y.Z   # else replicate exact flags from `docker inspect ember`
```

> On **Unraid** the bundled template (`deploy/unraid/ember.xml`) already sets
> `Network: host`; after the new release, just bump the container's tag (or
> "Force update" on `:latest`) and apply. Switching an existing bridge container
> to host networking requires removing + re-adding it (network mode can't change
> in place).

Verify: `GET /version` reports `dirty:false`; `docker exec ember /ember doctor`
all `[OK]`. For Unraid, see `README.md` → "Unraid install" + the
`deploy/unraid/ember.xml` template. Spec/plan history lives in the Obsidian vault
(`Superpowers Specs/ember/`).

## Upgrade notes

**`display.idle_restore_seconds` default changed 1200 → 120 (2 min).** Only
bare/default installs are affected — any deployment with an explicit
`idle_restore_seconds` in `config.json` keeps its value unchanged. If an
existing deployment has a runtime store override (set via `PUT
/v1/display/config`), that persisted value also wins over the new default.

## Home Assistant / Node-RED

Node-RED is intentionally **disabled** (add-on stopped, manual boot, watchdog
off) — its old Pomodoro/Weather flows were brittle. Do not rebuild around
Node-RED unless the user explicitly reverses that decision.
