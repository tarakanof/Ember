# Claude Code Producer

macOS-side bridge that reports `claude` CLI session activity to the
`ember` server (sub-project A in this repo).

## Install

Requires the Go 1.22+ toolchain (used to build the producer binary
locally) and `claude` CLI.

```sh
./producers/claude-code/install.sh
```

The script `go install`s `ember-claude-producer` to `$GOBIN`
(usually `~/go/bin`), installs a LaunchAgent at
`~/Library/LaunchAgents/com.ember.heartbeat.plist`,
creates `~/.config/ember/producer.env` from the example,
and merges hook entries into `~/.claude/settings.json`.

Then edit the env file:

```sh
$EDITOR ~/.config/ember/producer.env
```

Set `EMBER_SOURCE`, `EMBER_SERVER_URL`, and `EMBER_TOKEN`. Restart
`claude` (exit and re-run) to pick up the new hooks.

## Hooks as a Claude Code plugin (alternative to settings.json hooks)

The repo is also a Claude Code plugin marketplace (`.claude-plugin/marketplace.json`)
listing one plugin, `ember` (`producers/claude-code/plugin/`). It registers the
same hooks the installer writes, with the same events, matchers and `async`
flags. A Go test keeps the two in sync. The plugin carries only the hooks: you
still need the `ember-claude-producer` binary (`go install` or Ember.app's
bundled copy) and `~/.config/ember/producer.env`.

```sh
claude plugin marketplace add tarakanof/Ember   # or /plugin marketplace add tarakanof/Ember
claude plugin install ember@ember
ember-claude-producer install                   # or configure: LaunchAgent + statusLine; hooks are skipped
ember-claude-producer doctor                    # "claude hooks: plugin ember@ember ..."
```

- **Config.** `producer.env` stays the single source of truth for the URL,
  token and toggles. The plugin has no `userConfig`: those values would reach
  only the hooks, while the heartbeat daemon, the statusline and Ember.app read
  `producer.env`.
- **Binary lookup.** `scripts/ember-hook` uses `$EMBER_CLAUDE_PRODUCER` if set
  (and only that), else `ember-claude-producer` on `PATH`, else
  `~/go/bin/ember-claude-producer`, else
  `/Applications/Ember.app/Contents/MacOS/ember-claude-producer` (or
  `~/Applications/...`). No binary: the hook exits 0 silently.
- **Never in the way.** Every hook exits 0 and prints nothing. Blocking hooks
  have a 5 s timeout (the producer's own HTTP timeout is 500 ms); the three
  tool-outcome hooks run `async`, as in the installer.
- **statusLine.** Plugins can't set `statusLine`, so `install`/`configure`
  still writes it to `~/.claude/settings.json`. When `enabledPlugins` there
  has `ember@ember: true`, they skip the hooks and remove any producer hooks
  already in settings.json, so nothing runs twice.
- **Updates.** The plugin has no `version`, so an update follows the repo's
  latest commit: `claude plugin marketplace update ember && claude plugin update
  ember@ember`, or turn on
  auto-update for the marketplace in `/plugin`.
- **Uninstall.** `claude plugin uninstall ember@ember` (and
  `claude plugin marketplace remove ember`). Run `ember-claude-producer configure`
  afterwards if you want the settings.json hooks back.

**Migrating from settings.json hooks:** install the plugin, then run
`ember-claude-producer configure`. It sees the enabled plugin and drops the
settings.json hooks. Until you do, every event is handled twice (Claude Code
dedupes identical hooks across settings files, but not a plugin's copy), and
`doctor` reports `registered TWICE`. Restart `claude` afterwards.

## Verify

```sh
ember-claude-producer doctor
```

Should print your config, the LaunchAgent's status, and any active
markers in the state directory.

```sh
curl http://<server>/state | jq
```

Should reflect a `running` session within ~1s of starting a `claude` prompt.

## Uninstall

```sh
./producers/claude-code/uninstall.sh
```

Removes the binary, the LaunchAgent, and the producer's `~/.claude/settings.json`
hook entries. Leaves `~/.config/ember/producer.env` and
`~/.local/state/ember/` in place; remove them by hand if desired.

## Threat model

The bearer token is sent in cleartext over plain HTTP — this is a LAN-only
deployment assumption. Anyone on the same LAN with packet capture access
can intercept it. HTTPS is future work (sub-project E).

## Troubleshooting

- **No marker shows up:** `ember-claude-producer doctor` to check config; check `~/Library/Logs/ember-claude-producer.log` for hook errors.
- **`launchctl bootstrap` fails on install:** an existing LaunchAgent may be loaded under a different name. `launchctl list | grep awtrix` to inspect, then bootout the conflicting one.
- **`claude` reports "hook command failed"**: hooks always exit 0 by design. If you see this, the binary may not be at the path the install captured. Re-run `install.sh`.

## Spec

This producer is sub-project B of the ember decomposition. The
wire protocol is sub-project A; see [`docs/STYLE.md`](../../docs/STYLE.md)
for repo-wide style.
