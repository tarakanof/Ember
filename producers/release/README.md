# Ember producers @VERSION@

Headless status reporters for an [Ember](https://github.com/tarakanof/Ember)
server, for machines without Ember.app (Linux boxes, or a Mac where you want
the CLI to own the background service):

| Binary | Reports | Background service |
| --- | --- | --- |
| `ember-claude-producer` | Claude Code (hooks + statusline + heartbeat) | `ember-claude-producer` systemd user unit / `com.ember.heartbeat` LaunchAgent |
| `ember-codex-producer` | Codex CLI (rollout files) | `ember-codex-producer` / `com.ember.codex` |
| `ember-t3-producer` | T3 Code (local SQLite) | `ember-t3-producer` / `com.ember.t3` |

## Install

Verify and unpack (from the release page, next to `SHA256SUMS`):

```sh
sha256sum -c --ignore-missing SHA256SUMS   # macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
tar -xzf @ARCHIVE@
install -m 0755 ember-producers_*/ember-*-producer ~/.local/bin/
```

Or let the script do all of it (detects OS/arch, verifies the checksum,
runs `install`):

```sh
curl -fsSL https://raw.githubusercontent.com/tarakanof/Ember/main/scripts/install-producers.sh | sh -s -- --producers "claude codex"
```

Then, per producer you use:

```sh
ember-claude-producer install --headless   # hooks + statusline + service
ember-codex-producer install --headless
$EDITOR ~/.config/ember/producer.env       # EMBER_TOKEN=<the server's bearer token>
ember-claude-producer doctor               # server found? token set? service running?
```

`EMBER_SERVER_URL` empty (the default) or `auto` finds the server over mDNS
(`_ember._tcp`); set `http://host:3627` when several servers answer or
multicast doesn't reach it. On Linux the service is a systemd **user** unit;
on a box you don't stay logged into, run `sudo loginctl enable-linger $USER`
so it keeps running. Logs: `~/.local/state/ember/logs/` (Linux),
`~/Library/Logs/` (macOS).

The macOS binaries are universal (arm64 + x86_64) and ad-hoc signed, not
notarized: a copy downloaded with a browser needs
`xattr -d com.apple.quarantine ember-*-producer` (curl downloads don't).

Full guide: docs/RUNBOOK.md, "Headless / Linux producers".
