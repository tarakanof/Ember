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
  Also in `-p` bridge mode every client arrives from the Docker gateway IP, so
  the clock's `/hooks/awtrix/*` calls share one rate-limit bucket with the Macs'
  `/v1` traffic (60 burst, 5/s).
- **TLS (optional).** `EMBER_TLS_CERT_FILE` and `EMBER_TLS_KEY_FILE` must both
  be set (HTTPS) or neither (HTTP); exactly one is a startup error. The pair is
  parsed eagerly, so a malformed PEM, unreadable file or key/cert mismatch fails
  startup with a clear error. Expiry, SAN coverage and the trust chain are not
  validated; that is the operator's responsibility. The container healthcheck
  switches to https when the cert is set (`EMBER_HEALTHCHECK_CA_FILE` adds a
  trust bundle, `EMBER_HEALTHCHECK_INSECURE=1` skips verification).
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

**Claude hooks via the plugin** (alternative to the settings.json hooks):
`claude plugin marketplace add tarakanof/Ember`, `claude plugin install ember@ember`,
then `ember-claude-producer configure` (or `install`). With `ember@ember`
enabled in `~/.claude/settings.json`, configure/install write only the
statusLine (plugins can't set one) and strip any producer hooks already there;
`doctor`'s `claude hooks:` line warns `registered TWICE` if both are present.
Update: `claude plugin marketplace update ember && claude plugin update
ember@ember`. Uninstalling or disabling the plugin leaves no hooks (`doctor`:
`NONE`); run `configure` to put the settings.json hooks back. `deconfigure`/
`uninstall` write the kill switch `~/.config/ember/claude-hooks.disabled`, so
plugin hooks go silent (`doctor`: `DISABLED`). `configure` removes it. Hook
timeouts: blocking hooks 5 s (settings.json SessionEnd 2 s); SessionEnd shares
Claude Code's 1.5 s budget and caps its DELETE at 800 ms whatever
`EMBER_HOOK_TIMEOUT_MS` (default 500) says. `EMBER_DONE_TTL_SECONDS` (default
30, match the server's `done_ttl_seconds`) is how long the heartbeat keeps
re-posting a finished (`done`/`error`) session.
`configure`/`deconfigure` back up `~/.claude/settings.json` to
`settings.json.ember-bak` (one file, overwritten) before changing it, and skip
the write when nothing changes; a symlinked settings.json is written through
(a link into a read-only store is an error naming it, a dangling link is
replaced). Older producers wrote `settings.json.bak.<pid>` on every
run; they are never deleted automatically (`doctor` prints how many there
are), remove them by hand. `EMBER_STATUSLINE_TIMEOUT_MS` (default 10000, env
var or `producer.env`) bounds a wrapped status line command; on timeout the
last good output is shown.
`EMBER_CLAUDE_AGENTS_POLL` (default on; `0`/`off` disables, the env var
overrides `producer.env`) lets the `run` daemon cross-check running/waiting
sessions with `claude agents --json`: it ends a wait the hooks can't see end
(approved dialog, Esc on a dialog), marks an Esc-interrupted turn `done`
("interrupted"). It needs Claude Code ≥ 2.1.288 at `~/.local/bin/claude`
(native installer), on the daemon's PATH, or in `/opt/homebrew/bin` /
`/usr/local/bin`; nvm/npm-global installs aren't found and the check stays
off. `doctor` prints `claude agents cross-check: on (claude X at PATH)` or why
it is inactive. The daemon log (`ember-tick.log`) records each correction.
Settings › Agents shows the same thing under the Claude Code row ("Hooks: the
ember@ember plugin", "~/.claude/settings.json", both, or none, plus "Paused"
when the kill switch exists), reading the same files as `doctor`. With Claude
reporting on, registered twice, none (a plugin enabled only for a project
isn't checked, as in `doctor`), or paused gets an orange warning and
**Fix Hooks**, which runs the bundled `ember-claude-producer configure`; a
settings.json that isn't valid JSON gets a warning to fix it by hand and no
button.
Details: [`producers/claude-code/README.md`](../producers/claude-code/README.md).

`producer.env` (`~/.config/ember/producer.env`) holds: `source`,
`EMBER_SERVER_URL` (empty or `auto` = mDNS discovery, see "Headless / Linux
producers"), token, `EMBER_SOURCE_COLOR`, and the `EMBER_*` toggles
(below). The producer re-reads it each tick — no restart needed.

> **Gotcha:** the auto-mode classifier blocks a chained `go install + install`
> Bash call (it writes the global settings.json). Run them as **separate**
> commands, or the build silently doesn't happen and `install` uses a stale
> binary. `go install` alone is allowed; since the LaunchAgent/hook paths point
> at `~/go/bin`, rebuilding in place is usually enough — no re-`install` needed.

**Codex** daemon: installed as LaunchAgent `com.ember.codex`
(`KeepAlive=true`). Restart with `launchctl kickstart -k gui/$UID/com.ember.codex`.

**T3 Code** daemon (`cmd/ember-t3-producer`, tool `t3`): polls T3 Code's local
SQLite state read-only and reports each thread (session = T3 thread id,
activity = thread title). Verified against T3 Code **v0.0.45** (`state.sqlite`,
migration 54) and **v0.0.46-preview.20261002.2598** (`statev2.sqlite`,
migration 56). No pairing or token for T3 is needed: it reads files the T3
server writes under `~/.t3/userdata/`.

```sh
go install ./cmd/ember-t3-producer
ember-t3-producer install     # LaunchAgent com.ember.t3 (KeepAlive)
ember-t3-producer doctor      # T3 server running? schema/migration? thread count?
ember-t3-producer uninstall
```

Ember.app bundles it like Codex: Settings › Agents lists **T3 Code** with its
own switch (the master switch includes it once T3 is detected: `~/.t3` or
producer.env's `EMBER_T3_HOME`, the same place the helper reads). The row shows
even before T3 is installed, marked "Not found on this Mac", and can be turned
on anyway; the first time it runs, macOS asks once for the helper's Local
Network access. An agent turned off with its own switch stays off when the
master switch is turned on (the choice is kept in `producers.optOut`); on the
first launch of this version, T3 starts turned off if reporting was already on.
If `~/Library/LaunchAgents/<label>.plist` exists (installed with the CLI,
same label as the app's copy) the row says **Installed from the CLI** and the
app never registers its own; **Move to Ember** runs the bundled helper's
`uninstall` (it boots out and removes only the CLI's agent), then turns the
app's copy on. The CLI `install` stays for dev builds. Restart with `launchctl kickstart -k gui/$UID/com.ember.t3`; log at
`~/Library/Logs/ember-t3-producer.log`. `producer.env` keys (all optional):
`EMBER_T3_HOME` (default `~/.t3`, a leading `~/` is expanded; set it if you run T3 with `T3CODE_HOME` /
`--base-dir`), `EMBER_T3_POLL_INTERVAL_MS` (default 2000, floor 250),
`EMBER_T3_ACTIVITY_WINDOW_SECONDS` (default 300: how long a done/error thread
stays). `EMBER_ACTIVITY_TRAIL_ENABLED=false` hides thread titles. A T3 dev
server (`--dev-url`) keeps its state under `dev/`, not `userdata/`, and is not
read.

*Double sessions with Claude.* T3's Claude provider runs the Agent SDK with
the user's settings (`settingSources` includes `"user"` in v0.0.45; the SDK
default loads them in v0.0.46), so the Ember Claude hooks in
`~/.claude/settings.json` fire too: one T3 Claude thread shows as a `t3` and a
`claude` session. To avoid it, point T3's Claude provider at its own config dir
(the Claude provider instance's home path in T3's provider settings, which
T3 exports as `CLAUDE_CONFIG_DIR`; it needs its own `claude` login)
without Ember's hooks, or untick one of the two tools in the menu's Show on Clock list (`t3` is
listed there even before a T3 session exists).
Codex threads should not be doubled: T3 drives `codex app-server`, and the
Codex producer only follows rollouts with `session_meta.source == "cli"`
(confirm in the live check below).

*Live check once T3 is installed* (not yet run on the dev Mac):
1. Start T3, open a project, `ember-t3-producer doctor` → "T3 server:
   running", schema v1 or v2, the thread count.
2. `ember-t3-producer install`, start a Codex thread in T3 → `GET /state` shows
   `tool: "t3"`, `state: "running"`, `activity` = the thread title.
3. Ask for something that needs approval (or a Codex `request_user_input`) →
   `waiting`; answer → `running`; finish → `done` (no WAIT flash while T3
   captures the checkpoint), gone after 5 min. A subagent fan-out must not
   add extra `t3` sessions.
4. Archive the thread → DELETE at the next poll; quit T3 → every `t3` session
   is deleted within one poll.
5. With a Codex thread running in T3, `GET /state` must show no extra
   `codex` session for it; with a Claude thread, expect the `claude` double
   described above unless T3's Claude home path is separate.
6. Check the log for `T3 Code schema is newer than the verified one` after T3
   updates; bump `pinnedMigrations` once the mapping is re-verified.

**Menu app** (native SwiftUI, `macos/` — replaces the retired Go menu): generate the Xcode project and run it.

```sh
brew install xcodegen                                   # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/Ember.xcodeproj                          # pick your Dev team, then ⌘R
swift test --package-path macos                          # headless EmberKit tests
```

The generated `Ember.xcodeproj` is gitignored — regenerate it after pulling.

**Local install without a Developer ID.** macOS Local Network privacy (and
Little Snitch) key their grants to the app's designated requirement. An
ad-hoc signed app's requirement is its cdhash, so every rebuild is a new
program to macOS: Bonjour browsing fails with `NoAuth (-65555)` and LAN
connections fail with "Network is down", even right after you turn Ember on
in System Settings › Privacy & Security › Local Network. Sign local builds
with one stable identity instead, and the requirement stays the same on
every rebuild.

*Recommended: your Apple Development certificate.* If Xcode has signed you in
to an Apple account, the login keychain holds an "Apple Development: <name>
(<id>)" identity. List them with `security find-identity -v -p codesigning`
and pick yours by SHA-1: several identities can share a name (one per team),
and a name matching more than one is refused. Save the SHA-1 once:

```sh
mkdir -p ~/.config/ember
echo <SHA-1> > ~/.config/ember/signing-identity     # once; EMBER_SIGNING_IDENTITY overrides it
scripts/local-signing-identity.sh --check           # shows which identity local builds sign with
scripts/build-local.sh                              # Release build into /tmp/ember-local-build, signed with it
osascript -e 'quit app "Ember"'
ditto /tmp/ember-local-build/Build/Products/Release/Ember.app /Applications/Ember.app
open /Applications/Ember.app                        # approve Local Network one last time
```

The requirement becomes `identifier "com.ember.Ember" and anchor apple generic
and certificate leaf[subject.CN] = "Apple Development: <name> (<id>)" and
certificate 1[field.1.2.840.113635.100.6.2.1] /* exists */`: it names the
certificate's subject, not its hash, so it also holds after the certificate is
renewed under the same name, and `verify-bundle.sh` shows your team ID.

*Fallback: a self-signed certificate.* Without an Apple account, run
`scripts/local-signing-identity.sh` once to create "Ember Local Signing" in the
login keychain, then build the same way. The requirement is
`identifier "com.ember.Ember" and certificate leaf = H"<sha1>"`, stable until
you recreate the certificate. It is untrusted on purpose: no admin password
and no trust-settings change; `codesign` signs with it anyway and a locally
built app never meets Gatekeeper. The script generates it with
`/usr/bin/openssl` (LibreSSL) on purpose: its PKCS#12 defaults (3DES/SHA-1) are
what `security import` reads on every macOS, while OpenSSL 3's AES defaults are
not. The key is imported non-exportable (`-x`) with `-T codesign`, so `codesign`
can use it without a keychain prompt.

Which identity signs: `EMBER_SIGNING_IDENTITY` (a SHA-1 or an exact identity
name), else the first non-blank, non-`#` line of
`~/.config/ember/signing-identity` (`$XDG_CONFIG_HOME/ember/signing-identity`
when `XDG_CONFIG_HOME` is set; an Xcode GUI build doesn't inherit your shell's
XDG, so keep the file under `~/.config` or leave XDG unset), else
"Ember Local Signing", else none and the app stays ad-hoc.
`EMBER_SIGNING_IDENTITY=-` forces ad-hoc for one build even with the file set.
The override must name exactly one valid identity (as listed by
`security find-identity -v`): one that doesn't exist, is expired, revoked or
untrusted, matches more than one, or a config file that can't be read, fails
the build instead of falling back. The self-signed identity is untrusted, so
it's used by leaving the override unset, not by naming it.
`build-local.sh` builds ad-hoc (Xcode only accepts trusted identities with a
matching profile) and then re-signs the bundle inside-out with the identity,
keeping hardened runtime and the helpers' `com.ember.*` identifiers, dropping
`get-task-allow` and using no secure timestamp, so it works offline.
`scripts/verify-bundle.sh` prints which identity signed the app, its team and
its designated requirement (ad-hoc means re-approval after each rebuild). An
Xcode build whose configured Developer ID is missing signs the producer
helpers with the same identity when there is one, ad-hoc otherwise.
`--check` reports the identity, `--remove` deletes "Ember Local Signing";
`EMBER_SIGNING_KEYCHAIN` points the script at another keychain (used to test
it in a scratch keychain). The first signing with a new identity may show a
keychain prompt for `codesign`: choose **Always Allow**. Switching an existing
install to another identity (ad-hoc to either, self-signed to Apple
Development, or a recreated self-signed one) needs one more Local Network
approval for the app and each helper, and Little Snitch sees a new signer.

**Local Network denied although the toggle is on.** macOS resolves
`com.ember.Ember` through LaunchServices for both checks: nehelper caches the
allowed executable UUIDs from that bundle, and mDNSResponder checks the browse
type against its `NSBonjourServices`. Each scratch or DerivedData build
registers another Ember.app, and a stale copy can shadow the installed one:
connections log `unsatisfied (Local network prohibited)` and a browse fails
with `NoAuth(-65555)` (mDNSResponder logs `App Info.plist(NSBonjourServices)
does not allow …`). `build-local.sh` stamps a unique `CFBundleVersion` per
build (`2.<timestamp>`, below the next release's number) (`EMBER_BUILD_NUMBER` overrides it) and unregisters its own output;
`scripts/lsregister-clean.sh` (`--dry-run` to list) unregisters every other
copy and re-registers /Applications/Ember.app. If a browse is still refused,
`sudo killall mDNSResponder` drops its cached decision. Read the logs with
`/usr/bin/log show --predicate 'process == "mDNSResponder" OR process == "nehelper"'`
(in zsh, bare `log` is a builtin).

**Strings**: every user-facing string lives in `macos/Ember/Localizable.xcstrings`.
After adding or changing UI text, run `scripts/strings.sh sync` (it builds the
app into a fresh temp DerivedData; if you pass one as the second argument, make
it a clean build, since a reused one keeps `.stringsdata` from deleted files and
sync would re-add their keys), then give
any new format string (`%@`, `%lld`, `^[…](inflect: true)`) a translator comment
in the catalog. EmberKit's strings aren't extracted by Xcode, so the script
adds them as manual entries. `scripts/strings.sh check` is what CI runs; it also
warns about keys no code uses any more (delete those by hand).

**CI**: [`ci.yml`](../.github/workflows/ci.yml) runs the Go job on every push/PR,
plus a `macos` job (path-filtered to `macos/**`, `scripts/**`, `cmd/ember/testdata/**`,
`cmd/ember-claude-producer/testdata/**` and
the workflow file itself) that installs `xcodegen`, runs `swift test
--package-path macos`, regenerates the Xcode project, and does an unsigned
`xcodebuild ... CODE_SIGNING_ALLOWED=NO build` — there's no Developer ID on the
runner, so `build-producers.sh`'s sign phase skips itself under
`GITHUB_ACTIONS` (see that script) — then `scripts/strings.sh check` on that
build, which fails when a string is missing from the catalog.
Launch-at-login is in-app (App tab → `SMAppService`), not a LaunchAgent. The app
reads `producer.env` for connection config and needs a server on a build that
includes `GET /v1/preview` (added 2026-05; older servers 401 that route).

The app bundle now builds + signs the three producer helpers (`ember-claude-producer`,
`ember-codex-producer`, `ember-t3-producer`) and their LaunchAgent plists
(`com.ember.heartbeat`, `com.ember.codex`, `com.ember.t3`) into `Contents/MacOS` and
`Contents/Library/LaunchAgents` via a `postCompileScripts` phase
(`scripts/build-producers.sh`) that runs before Xcode's own app-level codesign, so
signing happens inside-out. `CODE_SIGN_IDENTITY` picks Developer ID for release
builds vs ad-hoc (`-`) for local dev; notarizing the `.app` covers the nested
helpers, no separate step needed. `scripts/verify-bundle.sh <Ember.app>` is the
gate for this contract (universal binaries, valid plists, codesign) — run it
after any build that touches the producers or the bundling phase.

On launch the app re-registers the enabled producer agents when the bundle
changed (version, build, or a digest of the bundled helpers and plists, stored
as `producers.lastReconciledVersion`), and re-registers any enabled agent that
`launchctl print gui/$UID/<label>` can't find, or shows stuck (`job state =
spawn failed` plus `needs LWCR update` or `last exit code = 78`: a rebuilt
ad-hoc helper, see ARCHITECTURE's gotchas); a stuck job is booted out first
(a failed bootout is logged and reported as a Repair failure). After an
update it checks again 30 s later; that recheck, Repair and the toggle run
one at a time. Settings › Agents shows such an agent as **Not running** with
a **Repair** button. By hand: `launchctl bootout gui/$UID/com.ember.heartbeat`
(and `com.ember.codex`), then quit and reopen Ember. Each helper's LaunchAgent
records whether it last reached the server in
`~/.config/ember/{claude,codex,t3}-producer.link.json`; when that says
`"no_route": true` (macOS denied it Local Network access, `connect: no route
to host` in `~/Library/Logs/ember-tick.log`), Settings › Agents shows a hint
with **Review Permissions…**.

**Settings › Permissions** lists every OS permission Ember uses with its live
status (text + symbol) and a fix button, re-checked when the pane appears and
whenever Ember becomes active (one check at a time, at most every 5 s;
**Check Again** skips the wait): Local Network (probed: a 3 s `_ember._tcp`
browse, where DNS-SD `NoAuth`/`PolicyDenied` means off, plus `GET /healthz`
on the server when it's a LAN host), the helpers' Local Network (the
`no_route` files above), Background Items (the producer agents, with Repair),
Reminders (required while reminder alarms are on) and Location (Weather's
Detect only, never required). Settings › General shows a one-line warning
when a required one is off. Wherever the app reports server reachability
(Connection's status row, the menu header, the Dashboard subtitle and empty
state, Discover Clocks' errors), a request macOS refused for Local Network
reasons says so instead of "Server unreachable": URLSession reports it as
`NSURLErrorNotConnectedToInternet` over `ENETDOWN` ("Network is down") with
the path "unsatisfied (Local network prohibited)" (`LocalNetworkDenial`).
Read those in the unified log with `/usr/bin/log show --predicate 'process ==
"Ember"' --last 1h | grep -i "local network prohibited"` (plain `log` is a zsh
builtin). The same places say "not responding" / "The server didn't answer
in time" when a request timed out instead of failing to connect (5 s for
plain server calls such as `/state`, 12 s or 35 s for calls that reach the
clock; see `RequestBudget`). For the header, the Dashboard and Test
Connection that means the host is down, the route black-holed or the
process hung: a powered-off or off-subnet server usually times out rather
than refusing, so a dead box now reads "not responding", not "unreachable".
Only a clock-settings call that times out points at a slow clock.

**Release note for the first release with fixed helper identifiers**
(`com.ember.claude-producer`, `com.ember.codex-producer`): existing installs
get one Local Network prompt per helper after updating (allow it, or the
helper can't reach the server), and possibly one launch-constraint failure
per agent on the first launch, which the app heals by itself within ~30 s.
The CLI `install`/`uninstall`
subcommands only unload a job loaded from their own
`~/Library/LaunchAgents/<label>.plist`, and `install` refuses (before touching
any config) when the label is the app's or unidentifiable, or when launchd
holds the app's "enabled" override (`launchctl print-disabled gui/$UID`) for a
job it dropped. Booting out the app's job makes launchd drop it while
Background Items still says it's enabled; `doctor` then suggests Repair.

The menu dropdown also runs the Pomodoro (Start/Pause/Resume/Skip/Stop) and has
per-app clock toggles (`PUT /v1/apps`). Settings → Pomodoro exposes the focus
duration (to 8h), the auto-stop cap ("Auto-stop after: N h", `0` = off), and
colours; Settings → App picks the Dock/app icon + the menu-bar icon: the
**animated bot** (default — drawn in code, blinks and glances on human-like
timing, morphs per session state, tinted by state colour; honours Reduce motion;
see [`MENU-BOT.md`](MENU-BOT.md))
or the per-tool tray glyphs.

Settings is a **sidebar window** (Connection / **Device** / **Agent** /
Pomodoro / Weather / Reminders / App), opened with ⌘, or the menu's "Settings…"
item, and it **auto-applies** changes — the Connection tab's **Token** is the one
field with a Save button, because a write-only field can't be auto-applied
per keystroke (it also commits on Return, on focus loss, and when the view
goes away). The **Agent** tab
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
offers a **Review Permissions…** button (macOS gates Bonjour browsing
behind the Local Network privacy permission; Settings › Permissions has the
fix) + **Rescan**. The **Device** tab
speaks the awtrix-ng schema directly (General / Native Apps / Time & Date /
Actions), proxied through the server (`/v1/device/*`) — brightness, sound (mute + buzzer volume),
app time, transitions (the picker is fed by `GET /v1/device/capabilities`
instead of a static list), native-app toggles, calendar colours, sensor
calibration (temp/hum offsets, written via a read-merge-PUT of
`/api/v1/system`; **applies live, no reboot** — unlike the old AWTRIX3
`dev.json` contract), Reboot / Dismiss, buttons (enabled with a one-click
`PUT /v1/device/buttons`) — plus **Open clock web UI** (opens the firmware's own
page at the clock's address, for everything Ember doesn't proxy: files, Berry
scripts, OTA) and a **Discover clocks** picker (mDNS) to choose
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

## Headless / Linux producers

The three Go producers run without Ember.app: on a Linux box running Claude
Code, Codex or T3 Code (amd64/arm64), or on a Mac where the CLI should own the
background services (#255).

**Headless mode** means the CLI owns the service and the producer finds the
server itself. It is automatic on Linux, and on macOS when no `Ember.app` is in
`/Applications` or `~/Applications`; `install --headless` forces it on a Mac
with the app (the Ember.app ownership check still refuses a label the app has
registered). It changes only who manages the service and the hints printed;
config, hooks, statusline and the wire protocol are the same everywhere.

**Install** from a release (archives `ember-producers_{linux_amd64,linux_arm64,
darwin_universal}.tar.gz` + `SHA256SUMS`, see "Docker Hub release"):

```sh
curl -fsSL https://raw.githubusercontent.com/tarakanof/Ember/main/scripts/install-producers.sh \
  | sh -s -- --producers "claude codex"     # --version vX.Y.Z, --bin-dir DIR, --no-install
$EDITOR ~/.config/ember/producer.env        # EMBER_TOKEN=<server bearer token>
ember-claude-producer doctor
```

The script detects OS/arch, verifies the archive against `SHA256SUMS`, copies
the binaries to `~/.local/bin` (put it on `PATH`) and runs
`<producer> install --headless` for each selected producer. It refuses to run
as root (per-user services) and, on a Mac with Ember.app, unless `--force`.
The script runs from `main()` on its last line, so a truncated download does
nothing. `ember-*-producer discover` finds and caches the server on demand. From source instead:
`go install ./cmd/ember-claude-producer` then `ember-claude-producer install
--headless` (run them as separate commands, see the gotcha above).

**Service.** On Linux `install` writes a systemd **user** unit
`~/.config/systemd/user/ember-{claude,codex,t3}-producer.service`
(`ExecStart=<binary> run`, `Restart=always`, `Nice=10`), then
`systemctl --user daemon-reload`, `enable`, `restart` (so a rebuilt binary takes
over). `uninstall` runs `disable --now`, removes the unit and reloads. `doctor`
shows `is-active`/`is-enabled` and the linger state. User units stop when your
last session ends: on a box you SSH into, run `sudo loginctl enable-linger
$USER` once so they start at boot and keep running. No user bus (a container,
`su` without a login session) makes `install` fail with that hint; run
`<producer> run` under your own supervisor then. macOS keeps the LaunchAgents
(`com.ember.heartbeat`, `com.ember.codex`, `com.ember.t3`).

**Server discovery.** `EMBER_SERVER_URL` empty (the new template default) or
`auto` browses `_ember._tcp` for ~3 s: an RFC 6762 legacy unicast query from
an ephemeral port (answered straight to it, so it works for a plain CLI on
macOS without multicast access; only replies with the QR bit and our query ID
count) alongside the regular multicast browse. One server answers → it is
used and cached in `$XDG_STATE_HOME/ember/server.json` (default
`~/.local/state`), together with the `EMBER_SERVER_INSTANCE` it was picked
under (a cache from another preference is ignored). Answers are kept per
(instance name, URL), so a second host answering as `Ember`, or one server
with two IPv4 addresses, shows up as several servers: set `EMBER_SERVER_URL`,
or `EMBER_SERVER_INSTANCE` to one's instance name (`Ember`, `Ember (2)`), host
name (`unraid`) or IP; `doctor`/`discover` list what was found and say which
to set. Discovery runs only in `discover`, `doctor`, a headless `install`
and the daemons; `configure` (what Ember.app runs) stays offline. Hooks and
the statusline never browse: they use the cache (no cache yet → they stay
silent until one of those fills it). Daemons wait for a first server with
backoff (5 s → 5 min) and re-browse in the background after 3 transport
failures in a row (at most once a minute), so a server that moved to a new IP
is picked up. The server must advertise (`EMBER_MDNS_ADVERTISE` on) and its
container must use host networking (see "Discovery & mDNS"); across VLANs or
where multicast is filtered, set the URL.

> **Trust:** `auto` trusts the LAN. Any host on it can answer `_ember._tcp`,
> and the producers send `EMBER_TOKEN` to the picked server over plain HTTP.
> A conflicting answer is reported as ambiguous rather than followed, but a
> spoofer that is the only answer wins. On shared, guest or otherwise
> untrusted networks set an explicit `EMBER_SERVER_URL` (ideally https behind
> a reverse proxy).

Ember.app accepts `auto` in Settings › Connection (it keeps the value and,
having no URL to call, shows the server as not configured); on a Mac with the
app, prefer a real URL there.

**Source**: empty `EMBER_SOURCE` defaults to the short host name from
`os.Hostname` on Linux (`build-1.example.com` → `build-1`,
`ip-10-0-1-5.ec2.internal` → `ip-10-0-1-5`), max 24 chars.

**Claude Code on Linux**: hooks come from the `ember@ember` plugin
(`claude plugin marketplace add tarakanof/Ember`, `claude plugin install
ember@ember`, then `ember-claude-producer configure`) or from `settings.json`
as `install` writes them; the statusline is the same. The plugin shim finds
the binary on `PATH`, `~/go/bin` or `~/.local/bin`. Usage polling reads the
OAuth token from `~/.claude/.credentials.json` (no Keychain on Linux).

**Paths**: config `~/.config/ember/producer.env` (0600); session markers
`~/.local/state/ember/sessions/` (fixed: shared with Ember.app); the server
cache and Linux logs follow an absolute `$XDG_STATE_HOME`
(`$XDG_STATE_HOME/ember/server.json`, `…/ember/logs/<producer>.log`, default
`~/.local/state`; macOS logs stay in `~/Library/Logs`). `install` copies
`XDG_STATE_HOME` into the systemd unit so the daemon and the hooks agree.
`uninstall` leaves `producer.env`, the cache and the logs in place.

**Liveness on Linux** reads `/proc/<pid>/stat` instead of `ps` (BusyBox `ps`
on Alpine has no `-p`/`lstart`): the Claude session owner walk and its start
check, and T3's server start time.

## Device button → Pomodoro control

The clock's three physical buttons drive the timer via NG's `buttonCallback`
(an HTTP POST per press; no MQTT broker needed). The easiest path is the menu
app's Settings › Devices › Clock › Buttons (one-click `PUT /v1/device/buttons`, `{"enabled":true}`),
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
  `curl -X POST -H 'Content-Type: application/json' -d '{"button":"middle","state":true}' http://localhost:3627/hooks/awtrix/button`
  (should start a focus).

## Knob device registry (`/v1/devices`)

The cinder knob authenticates with its own **device token** (`ekd_…`), never
`EMBER_TOKEN`. Ember.app's knob setup mints it; to do it by hand (the token
is printed once; only its SHA-256 is stored):

```sh
H="Authorization: Bearer $EMBER_TOKEN"
curl -s -XPOST localhost:3627/v1/devices -H "$H" \
  -d '{"kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob"}'
curl -s localhost:3627/v1/devices -H "$H"                 # status, no secrets
curl -s -XPUT localhost:3627/v1/devices/knob-61fc8c/config -H "$H" -d '{"poll_ms":3000}'
curl -s -XPOST localhost:3627/v1/devices/knob-61fc8c/rotate -H "$H"
curl -s -XDELETE localhost:3627/v1/devices/knob-61fc8c -H "$H"   # revoke
```

- **Lost token / reflashed knob:** POST the same `hw_id` again. It answers
  200 with a new token, revokes the old one and keeps the knob's config.
- **Rotation:** the next checkin carries `new_token` (repeated unchanged until
  used); the old token keeps working until the knob first uses the new one, or
  24 h after the rotate. A knob offline for longer than that needs
  re-provisioning over USB.
- **Suspected token leak: don't rotate.** Rotation is routine hygiene only:
  anyone holding the old token can collect the new one. `DELETE` the knob or
  re-POST its `hw_id` (USB setup) instead; both revoke immediately.
- **`/admin/doctor` `devices` fails with "registry load failed":** the stored
  `devices_json` row didn't decode. Device writes and knob auth answer 500
  until restart so the row isn't overwritten; fix or delete the row in
  `pomodoro.db`, then restart.
- **Knob gets 401:** it was deleted, re-provisioned elsewhere, or missed a
  rotation; `/admin/doctor` → `devices` lists each knob's last checkin
  (warns when one never checked in or is silent > 5 min).
- The registry lives in the SQLite store (key `devices_json`, next to
  `pomodoro.db`), so the same writable volume is needed; without it, minted
  tokens are lost on restart.

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
| `EMBER_SOURCE` | machine label; empty or the old `set-me-to-this-laptop-id` placeholder defaults to a short id from the macOS `LocalHostName` (`Dmitrys-MacBook-Pro` -> `mbp`, `-Air` -> `mba`, `Mac-mini` -> `mini`; the clock card fits ~4 glyphs); install/configure/doctor write it into producer.env once and print it |
| `EMBER_SOURCE_COLOR` | hex body colour for the 8×8 icon + source-name card digits |
| `EMBER_SOURCE_CARD` (default true) | source-name card (uppercased, 4 glyphs) in the number slot; set false to hide |
| `EMBER_SESSION_BAR` (default true) | session-pixel bar on row 7 (1 px per non-idle session); set false to hide |
| `EMBER_CODEX_SOURCES` (default `cli,vscode`) | Codex producer only: comma-separated `session_meta.source` kinds shown; add `exec` / `mcp` for headless or agent-driven Codex runs |
| `EMBER_CODEX_INCLUDE_CLAUDE` | Codex producer only: also show sessions Claude Code's Codex plugin starts (originator `Claude Code`), messages prefixed `via Claude`; off because the parent Claude session already shows that work |
| `EMBER_CODEX_APPSERVER` (default true) | Codex producer only: observe TUI sessions through the Codex app-server daemon socket (`$CODEX_HOME/app-server-control/app-server-control.sock`) when it exists; a no-op without the daemon. Ember never starts the daemon. `doctor` shows the socket, a probe connection (daemon user agent, loaded threads) and the daemon's updater setting |
| `CODEX_HOME` | Codex producer only: Codex's home when it is not `~/.codex`; set it in `producer.env` (a LaunchAgent does not see shell exports). Moves the sessions dir (unless `EMBER_CODEX_SESSIONS_DIR`) and the app-server socket |

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

## Now playing (Plex, Apple Music) — #226

What the knob's now-playing page (cinder #14) shows. Sources: the Plex poller
on the server and Ember.app's Apple Music pusher (Settings › Sources › Music,
per Mac, off by default). Reads: `GET /v1/nowplaying/state`,
`GET /v1/nowplaying/art?kind=album|artist|backdrop&size=` (fixed sizes: album 240/120, artist 64/120, backdrop 466; ARCHITECTURE
"Now playing").

| Env | Meaning |
|---|---|
| `EMBER_PLEX_URL` | Plex Media Server base URL, `http(s)://host:32400`. With the token, enables the poller |
| `EMBER_PLEX_TOKEN` | **Secret.** Sent only as the `X-Plex-Token` header; never logged, stored or answered |
| `EMBER_PLEX_USER` / `EMBER_PLEX_PLAYER` | Optional filters: Plex user title; player title or machine id |
| `EMBER_PLEX_WEBHOOK_KEY` | Optional. Enables `POST /hooks/plex?key=<this>` (Plex Pass webhook → poll now). Unset = 404 |
| `EMBER_ARTIST_LOOKUP` | **Off by default.** `1` turns on artist-picture lookups on Deezer: every artist **name** played (Plex and Apple Music) leaves your network, tied to your public IP; pictures stay in RAM only. Logged once at startup when on |

**Verify without a knob.** `curl -s $EMBER/v1/nowplaying/state` while
Plexamp plays (or after the app posts), then
`curl -s "$EMBER/v1/nowplaying/art?kind=backdrop" -o /tmp/b.jpg` — a 466 px
square, dimmed baseline JPEG. A knob gets the compact block in
`/v1/devices/self/view` only after `nowplaying` is added to its pages
(`PUT /v1/devices/{id}/config {"pages":[…,{"id":"nowplaying","on":true}]}`).

**Privacy.** `GET /v1/nowplaying/state` needs no token: anyone on the LAN
can read the track, artist and album (not the Mac's name).

**Apple Music permission (manual test).** On a Mac, reset the grant with
`tccutil reset AppleEvents com.ember.Ember` (only on a test build's bundle
id, never the installed app's), start Music, turn on Settings › Sources ›
Music: macOS should show "Ember wants access to control Music". Or in
Settings › Permissions, with the row at "Not asked yet", click Allow
Access…: it calls `AEDeterminePermissionToAutomateTarget` with
`core`/`getd` (wildcard event codes are reported not to prompt). Deny →
the row turns Off with "Open Automation Settings…".

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
current values (like every settings PUT). `usage_threshold_pct` outside 0–100
is a 400.

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
the SQLite store, surviving restarts and `POST /admin/reload`. A partial body
changes only the fields it names.

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
"Discovered servers"). Settings › Devices › Clock proxies the clock's own NG API through
`/v1/device/*`.

- **Host networking is required** for either direction — multicast doesn't cross
  the default Docker bridge. Run the container with `--network host` (or macvlan).
  When the server can't see the clock anyway, Settings › Devices › Clock › Status › Discover Clocks
  (or **Find Clock from This Mac…**, shown when the server reports the clock
  unreachable) also browses from the Mac and pins the pick on the server. The
  app needs Local Network access for that.
- Effective clock URL: the writable-store override (Settings › Devices › Clock › Status › Discover Clocks),
  else `awtrix.http_base_url` from `config.json`. If that fails its probes, an
  in-memory mDNS swap replaces it (the override included) until a PUT naming
  `base_url` or a reload that changes the file URL; the swap is never written
  back to the config or the store.
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
  next 30s tick — the 30s watch remains the fallback path. A missed probe
  alone is never a reboot (only falling or lagging `uptimeSeconds` is), and
  republishes less than 10s apart coalesce, so a `clock reboot detected` log
  line on a lossy link now means a real reboot.
  `/admin/doctor`'s `clock` check reports `base_url`/`source`/`reachable` plus
  `last_rediscover_at`/`last_rediscover_result`.
- `EMBER_CLOCK=off` (also `0`/`false`/`no`/`disabled`; unset = clock on) runs the
  server with **no clock I/O at all**: no AWTRIX HTTP (publishes, probes, the
  `/v1/device/*` proxy), no mDNS browse or UDP find, no auto-rediscover or sampler,
  no boot-ping install, no icon provisioning, no firmware lookup. Publishes are dropped
  (not counted in publish health), the
  `/v1/device/*` proxy answers 502 `clock disabled`, `GET /v1/device/discover` 503
  `clock_disabled`. `GET /v1/clock/health` carries `"disabled": true` with
  `device: null`; `/admin/doctor`'s `clock`, `awtrix_reachable` and
  `capabilities` checks are `ok` with a "disabled" detail (doctor stays OK). Use it for tests and scratch servers: safe by construction.
- `EMBER_MDNS_ADVERTISE` (default on; `0`/`false`/`no`/`off` disables) gates only the
  advertising side; clock discovery and Settings › Devices › Clock still work with a
  configured URL.
- `EMBER_FIRMWARE_CHECK` (default on; `0`/`false`/`no`/`off` disables) gates the
  server's only call to the internet for the dashboard: a background lookup of the latest
  awtrix-ng release on GitHub (at most every 6h, 30 min after a failure, logged
  at Warn) behind `latest_firmware`/`update_available` in `GET /v1/clock/health`.
  Disabled, those fields are `null` and no request leaves the server.
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

**Verify it's flowing:** `GET /v1/usage` (no token) returns every tool's latest
snapshot with `updated_at` and `stale`; `GET /state` carries only the 5h
percent. Posting a crafted usage payload:
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
multi-arch Docker Hub push (SBOM + provenance). The same release event runs
`.github/workflows/release-producers.yml`: `scripts/package-producers.sh` on a
macOS runner builds the producers for linux/amd64, linux/arm64 and darwin
(universal, ad-hoc signed, not notarized) and uploads one `tar.gz` per OS/arch
plus `SHA256SUMS` to the release (`workflow_dispatch` with `tag` re-attaches
them; pre-releases are skipped). Archives are reproducible (entries stamped
with `SOURCE_DATE_EPOCH` or the commit time, sorted, root-owned, `gzip -n`).
Try it locally with `scripts/package-producers.sh 0.0.0 /tmp/out` (the out dir
must be empty or hold only earlier archives).
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
