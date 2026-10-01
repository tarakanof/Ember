#!/usr/bin/env bash
set -euo pipefail

APP="${1:-${CODESIGNING_FOLDER_PATH:-}}"
[ -n "$APP" ] || { echo "usage: build-producers.sh <Ember.app> (or run from Xcode)"; exit 2; }
REPO="$(cd "$(dirname "$0")/.." && pwd)"
IDENTITY="${CODE_SIGN_IDENTITY:--}"
TIMESTAMP=--timestamp
KEYCHAIN_ARGS=()

if [ "$IDENTITY" != "-" ]; then
  if [ "${CODE_SIGNING_ALLOWED:-YES}" = "NO" ]; then
    echo "build-producers.sh: signing not allowed — ad-hoc"
    IDENTITY="-"
  elif ! security find-identity -v -p codesigning 2>/dev/null | grep -qF "$IDENTITY"; then
    rc=0
    LOCAL="$("$REPO/scripts/local-signing-identity.sh" --hash)" || rc=$?
    case "$rc" in
      0)
        echo "build-producers.sh: no usable '$IDENTITY' identity — using the local signing identity ($LOCAL)"
        IDENTITY="$LOCAL"
        TIMESTAMP=--timestamp=none
        if [ -n "${EMBER_SIGNING_KEYCHAIN:-}" ]; then
          KEYCHAIN_ARGS=(--keychain "$EMBER_SIGNING_KEYCHAIN")
        fi
        ;;
      1)
        echo "build-producers.sh: no usable '$IDENTITY' identity — falling back to ad-hoc"
        IDENTITY="-"
        ;;
      *) exit 1 ;;
    esac
  fi
fi
MACOS_DIR="$APP/Contents/MacOS"
mkdir -p "$MACOS_DIR"

build_universal() {
  local pkg="$1" out="$2" ident="$3" tmp
  tmp="$(mktemp -d)"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64  go build -C "$REPO" -o "$tmp/arm64" "./cmd/$pkg"
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64  go build -C "$REPO" -o "$tmp/amd64" "./cmd/$pkg"
  lipo -create "$tmp/arm64" "$tmp/amd64" -output "$MACOS_DIR/$out"
  rm -rf "$tmp"
  codesign --force --sign "$IDENTITY" ${KEYCHAIN_ARGS[@]+"${KEYCHAIN_ARGS[@]}"} --identifier "$ident" --options runtime "$TIMESTAMP" "$MACOS_DIR/$out"
  echo "signed $out as $ident ($(lipo -info "$MACOS_DIR/$out" | sed 's/.*: //'))"
}

build_universal ember-claude-producer ember-claude-producer com.ember.claude-producer
build_universal ember-codex-producer  ember-codex-producer  com.ember.codex-producer

LA_DIR="$APP/Contents/Library/LaunchAgents"
mkdir -p "$LA_DIR"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.heartbeat.plist" "$LA_DIR/"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.codex.plist"     "$LA_DIR/"
for p in "$LA_DIR"/com.ember.*.plist; do plutil -lint "$p" >/dev/null; done
