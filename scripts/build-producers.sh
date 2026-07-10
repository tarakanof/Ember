#!/usr/bin/env bash
set -euo pipefail

# Resolve the app bundle: Xcode sets CODESIGNING_FOLDER_PATH to the .app during
# a build; a --dest arg overrides for manual runs.
APP="${1:-${CODESIGNING_FOLDER_PATH:-}}"
[ -n "$APP" ] || { echo "usage: build-producers.sh <Ember.app> (or run from Xcode)"; exit 2; }
REPO="$(cd "$(dirname "$0")/.." && pwd)"
IDENTITY="${CODE_SIGN_IDENTITY:--}"    # Developer ID in release; ad-hoc for dev
MACOS_DIR="$APP/Contents/MacOS"
mkdir -p "$MACOS_DIR"

build_universal() {
  local pkg="$1" out="$2" tmp
  tmp="$(mktemp -d)"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64  go build -C "$REPO" -o "$tmp/arm64" "./cmd/$pkg"
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64  go build -C "$REPO" -o "$tmp/amd64" "./cmd/$pkg"
  lipo -create "$tmp/arm64" "$tmp/amd64" -output "$MACOS_DIR/$out"
  rm -rf "$tmp"
  # Inside-out sign: hardened runtime + timestamp, same identity as the app.
  codesign --force --sign "$IDENTITY" --options runtime --timestamp "$MACOS_DIR/$out"
  echo "signed $out ($(lipo -info "$MACOS_DIR/$out" | sed 's/.*: //'))"
}

build_universal ember-claude-producer ember-claude-producer
build_universal ember-codex-producer  ember-codex-producer

# Bundle the SMAppService LaunchAgent plists alongside the signed binaries.
LA_DIR="$APP/Contents/Library/LaunchAgents"
mkdir -p "$LA_DIR"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.heartbeat.plist" "$LA_DIR/"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.codex.plist"     "$LA_DIR/"
for p in "$LA_DIR"/com.ember.*.plist; do plutil -lint "$p" >/dev/null; done
