#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

GOBIN="$(go env GOBIN 2>/dev/null || true)"
if [ -z "$GOBIN" ]; then
  GOBIN="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"
fi
BIN="$GOBIN/ember-claude-producer"

if [ -x "$BIN" ]; then
  "$BIN" uninstall
else
  # Fallback: directly invoke launchctl + advise manual settings.json edit
  launchctl bootout "gui/$UID/com.ember.heartbeat" 2>/dev/null || true
  rm -f "$HOME/Library/LaunchAgents/com.ember.heartbeat.plist"
  echo "(could not run binary uninstall; manually edit ~/.claude/settings.json to remove awtrix entries)" >&2
fi

if [ -x "$BIN" ]; then
  rm -f "$BIN"
fi

echo "Uninstall complete."
