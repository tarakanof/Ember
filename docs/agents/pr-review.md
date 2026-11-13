# PR review profile

The repo's half of the pr-review skill: read by the reviewer every round and by the fixer
at self-check and census time. Rules stated elsewhere are pointers: `AGENTS.md` (hard
rules), `docs/STYLE.md`, `docs/WORKFLOW.md`, `docs/DEVICE-PROTOCOL.md`.

## Product

Ember shows Claude Code / Codex / T3 Code activity, a Pomodoro, a day clock, prints, music
and weather on one owner's desk displays: an Ulanzi TC001 clock (pushed frames) and a
round knob (`tarakanof/cinder`, pulls views). Producers on the owner's Macs report agent
state; a Go server (Docker on Unraid, or bundled in Ember.app) aggregates it; Ember.app
configures everything. The user is the owner at their desk. The wrong action on their
behalf: a display that lies (`waiting` after the answer, `done` while running, a replayed
"finished"), a token sent to the wrong listener, producers left on the old server, a
broken install or update, or the owner's devices, Macs or names leaking into the repo.

## Severity

- **blocker**: an `AGENTS.md` hard rule broken: a secret (`EMBER_TOKEN`, device tokens,
  Wi-Fi passwords) printed, logged, committed or sent to an unverified listener; a test,
  script or doc step that touches live state (`~/.config/ember`, `~/.claude`, `~/.codex`,
  `~/Library/LaunchAgents`, `/Applications/Ember.app`, the paired knob, port 3627, the
  real clock); a code comment outside STYLE §9's exceptions; a wire change the knob or
  older firmware can't take (DEVICE-PROTOCOL compatibility table, the 1024 B
  `config_bytes` floor) without regenerated goldens; owner-specific strings (device ids,
  hostnames, names) outside tests and fixtures; a release or install path that leaves
  users without a working app (README download link, `releases/latest`, a launch crash).
- **should-fix**: a display state that disagrees with the source of truth for one
  ordinary sequence; the PR body or docs claiming behaviour the code doesn't have; a
  state lost on relaunch or toggle while its throttle stamp survives; a missing test for
  a behaviour the PR adds; a doc step naming UI that doesn't exist.
- **nit**: a translator comment that misdescribes a shared key; a docs list missing one
  new member (identifiers, readers); log noise on a quiet path.

## Do not flag

- gofmt, `go vet`, `go test -race`, govulncheck, redocly, `claude plugin validate`: CI runs them.
- Missing or thin code comments: the repo forbids them (STYLE §9). Localization `comment:`
  arguments are translator notes; `scripts/strings.sh check` enforces them on format strings.
- `knob-61fc8c` / `61FC8C` in `macos/Ember/Hardware/Preview`, `KnobStatsFake.swift`, test
  data and the translator comments derived from them: fixtures mirroring the goldens.
- An acceptance point that only the owner can run (notarization, a real release, live
  hardware, an install on their Mac) when the PR body lists it as the owner's step with
  numbered instructions: a merge gate, not a defect.
- A deliberate placeholder or deferred scope the issue's owner notes name (for example
  #861's `SUPublicEDKey` placeholder until the key exists).
- Doc prose style, line wrapping, and wording that is correct.
- Regenerated goldens (`cmd/ember/testdata/**`) whose diff matches the PR's stated shape change.

## Defect classes

- **producer.env writers** (`macos/Sources/EmberKit/BuiltInServer*.swift`,
  `ConnectionSettings`, `ProducerInstallService`, `internal/producer/envfile.go`): the URL and
  token change together, only after the listener is proven ours (port pre-check, two
  healthy polls); every change reloads `ServerConnection` and kickstarts every *loaded*
  producer job (app-registered or CLI); a stale draft never writes an old field back
  (save only fields that changed). The app writes through `SettingsKeys` and
  `EnvFileStore`, never the literal names. Search:
  `rg -n 'producer\.env|EMBER_SERVER_URL|EMBER_TOKEN|SettingsKeys\.(serverURL|token)|EnvFileStore|producerEnv'`.
- **producer parity** (`cmd/ember-claude-producer` vs `producers/claude-code/mod`): the Go
  hook and the mod must reach the same state for the same event sequence, including the
  skips (`correctableWait`, elicitation, worker permission). A change to one names the
  other's matching change and test.
- **upstream mirrors** (`cmd/ember-t3-producer/query_v2.go`, the Codex item labels): a query
  that mirrors an upstream rule (T3's `derivePendingBackgroundWork`, `subagentAwaitsApproval`)
  filters before it ranks, and dedupes the same way upstream does; the test feeds the
  ordering that would differ.
- **restart and replay** (`internal/printer`, `internal/pomodoro`, `internal/workday`, push
  trackers): after a server or device restart mid-state, nothing replays an event already
  shown (`finished`, a popup) and nothing is lost; persisted stamps and the state they
  throttle are restored together.
- **releases/latest readers** (`scripts/release.sh`, README, `install-producers.sh`, the
  app's update check: `UpdateCheck`, then Sparkle's `appcast.xml` from #861): every
  release kind leaves `releases/latest` on a release with the app zip and its feed. Search: `rg -n 'releases/latest'`.
- **signed bundle** (`macos/project.yml`, `scripts/build-producers.sh`,
  `scripts/local-signing-identity.sh`, `scripts/verify-bundle.sh`): every nested binary is
  signed inside-out with the runtime flag and a fixed identifier where Local Network keys
  on it; a teamless (ad-hoc, self-signed) build still launches (library validation);
  release builds carry no `get-task-allow`.
- **loopback test stubs** (Go `httptest`, producer recorders): T3 Code sends `GET /` to new
  loopback listeners; a counting stub ignores it, or counts only its own route.
- **shell under pipefail** (`scripts/*.sh`): no `cmd | grep -q` whose writer may still be
  writing (#864); Bash 3.2 only; a failure before the commit restores what it changed.
- **wire shape** (`cmd/ember/*_http.go`, `docs/openapi.yaml`, device views and configs): a
  shape change regenerates the goldens and updates API.md / DEVICE-PROTOCOL in the same PR.

## Self-check

```sh
u="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"; test -z "$u" || { echo "$u"; false; }
go vet ./... && go test ./... -race
swift test --package-path macos
scripts/strings.sh check          # runs xcodegen and an unsigned Debug build itself
for t in scripts/*_test.sh; do "$t" || exit 1; done
node --test producers/claude-code/mod/test/producer.test.mts
claude plugin test producers/claude-code/mod   # when the claude CLI is installed
```

Run the Go lines when Go changed, the app lines when `macos/` changed, the shell tests
when `scripts/` changed, the mod lines when `producers/claude-code/mod/` changed (and
for any producer-parity fix, whichever side it touched).

## Checks before a re-check

The targeted tests for the fixed code (`go test ./<pkg> -race -run <Test>`,
`swift test --package-path macos --filter <Suite>`, the touched `scripts/*_test.sh`, the
mod's `node --test` for a mod change), plus
`scripts/strings.sh check` when a string changed. The full suite when the fix touches a
defect class above.

## Settings

- rounds: 4
- reviewer_model: opus
- reviewer_effort: medium
- lite_model: sonnet
- lite_effort: low
- lite_max_lines: 40
- sensitive_paths: *auth*, *login*, *secret*, *credential*, *keychain*, *crypto*, *token*, *migration*, *.sql, .github/workflows/*, scripts/release*.sh, scripts/*sign*.sh, scripts/build-local.sh, scripts/install-producers.sh, macos/project.yml, macos/Ember/Info.plist, *.entitlements, macos/Ember/LaunchAgents/*, macos/Sources/EmberKit/BuiltInServer*, macos/Sources/EmberKit/ProducerInstallService.swift, macos/Sources/EmberKit/ConnectionSettings.swift, macos/Sources/EmberKit/Config/EnvFileStore.swift, macos/Sources/EmberKit/Config/ConfigModel.swift, macos/Ember/AppEnvironment.swift, internal/producer/env*, */store.go, cmd/ember/testdata/devices/*, docs/DEVICE-PROTOCOL.md
- codex: auto
