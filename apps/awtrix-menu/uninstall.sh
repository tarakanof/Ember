#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

GOBIN="$(go env GOBIN 2>/dev/null || true)"
[ -z "$GOBIN" ] && GOBIN="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"
BIN="$GOBIN/awtrix-menu"

PLIST="$HOME/Library/LaunchAgents/com.awtrix-ai-status.menu.plist"

if [ -x "$BIN" ]; then
  "$BIN" uninstall
else
  launchctl bootout "gui/$UID/com.awtrix-ai-status.menu" 2>/dev/null || true
  # Recover the installed binary path from the plist BEFORE removing the plist
  # (spec ordering: read ProgramArguments[0], then unlink the plist, then the binary).
  PLIST_BIN=""
  if [ -f "$PLIST" ]; then
    PLIST_BIN="$(plutil -extract 'ProgramArguments.0' raw -o - "$PLIST" 2>/dev/null || true)"
  fi
  rm -f "$PLIST"
  if [ -n "$PLIST_BIN" ] && [ -e "$PLIST_BIN" ]; then
    rm -f "$PLIST_BIN"
  fi
fi
echo "Uninstall complete."
