#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

GOBIN="$(go env GOBIN 2>/dev/null || true)"
[ -z "$GOBIN" ] && GOBIN="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"
BIN="$GOBIN/awtrix-menu"

if [ -x "$BIN" ]; then
  "$BIN" uninstall
else
  launchctl bootout "gui/$UID/com.awtrix-ai-status.menu" 2>/dev/null || true
  rm -f "$HOME/Library/LaunchAgents/com.awtrix-ai-status.menu.plist"
fi
echo "Uninstall complete."
