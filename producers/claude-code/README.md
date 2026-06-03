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
