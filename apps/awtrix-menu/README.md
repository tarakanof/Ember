# awtrix-menu — macOS menu-bar status app

Sub-project D in this repo. Reads the producer's marker files (sub-project B)
and shows current Claude state in the menu bar. Includes a "Preferences…" menu
item that opens a hardened localhost form for editing `producer.env`.

## Install

Requires Go 1.26+ and macOS (matches the repo's `go.mod`). Sub-project B
(the producer) should already be installed and configured.

```sh
./apps/awtrix-menu/install.sh
```

The script `go install`s the binary, clears Gatekeeper quarantine on the
locally-built binary, and bootstraps a LaunchAgent that auto-starts at login.

If first run is blocked by Gatekeeper, in Finder right-click the binary
(under `~/go/bin/awtrix-menu`) → Open. macOS records the authorization;
subsequent runs are unblocked.

## Verify

After install, the icon should appear within ~5 seconds. Click it for the
status menu. "Doctor" runs `awtrix-claude-producer doctor` in Terminal.

## Uninstall

```sh
./apps/awtrix-menu/uninstall.sh
```

## Threat model

The Preferences form runs an HTTP server on `127.0.0.1:0` (random port) only
while the menu app is running. Defenses against cross-origin browser-CSRF:
per-click single-use nonce in URL, `Origin`/`Host` validation, env-file
optimistic concurrency via `mtime`. Same-user processes can read the env
file directly; that's the trust model for a developer laptop.

## Spec / plan

`docs/superpowers/specs/2026-05-08-mac-menu-bar-app-design.md` (gitignored).
