#!/usr/bin/env bash
set -euo pipefail

# Resolve the app bundle: Xcode sets CODESIGNING_FOLDER_PATH to the .app during
# a build; a --dest arg overrides for manual runs.
APP="${1:-${CODESIGNING_FOLDER_PATH:-}}"
[ -n "$APP" ] || { echo "usage: build-producers.sh <Ember.app> (or run from Xcode)"; exit 2; }
REPO="$(cd "$(dirname "$0")/.." && pwd)"
IDENTITY="${CODE_SIGN_IDENTITY:--}"    # Developer ID in release; ad-hoc for dev
TIMESTAMP=--timestamp

# CODE_SIGNING_ALLOWED=NO (unsigned CI builds): ad-hoc, so go build/lipo/plist
# copy/plutil lint still run and only the signing step changes. A configured
# identity that isn't in the keychain (no Developer ID cert installed): the
# stable self-signed "Ember Local Signing" identity when it exists (see
# local-signing-identity.sh; it keeps Local Network grants across rebuilds),
# else ad-hoc. An explicit "-" stays ad-hoc.
if [ "$IDENTITY" != "-" ]; then
  if [ "${CODE_SIGNING_ALLOWED:-YES}" = "NO" ]; then
    echo "build-producers.sh: signing not allowed — ad-hoc"
    IDENTITY="-"
  elif ! security find-identity -v -p codesigning 2>/dev/null | grep -qF "$IDENTITY"; then
    if LOCAL="$("$REPO/scripts/local-signing-identity.sh" --hash)"; then
      echo "build-producers.sh: no usable '$IDENTITY' identity — using Ember Local Signing ($LOCAL)"
      IDENTITY="$LOCAL"
      TIMESTAMP=--timestamp=none
    else
      echo "build-producers.sh: no usable '$IDENTITY' identity — falling back to ad-hoc"
      IDENTITY="-"
    fi
  fi
fi
KEYCHAIN_ARGS=()
if [ "${EMBER_SIGNING_KEYCHAIN:-}" != "" ] && [ "$IDENTITY" != "-" ]; then
  KEYCHAIN_ARGS=(--keychain "$EMBER_SIGNING_KEYCHAIN")
fi
MACOS_DIR="$APP/Contents/MacOS"
mkdir -p "$MACOS_DIR"

# build_universal <cmd package> <output name> <signing identifier>
#
# The identifier is pinned: without -i, an ad-hoc signed Mach-O with no
# Info.plist gets "<name>-<LC_UUID hex>", so every rebuild is a new program to
# macOS. Local Network privacy keys its allow on that identifier (a rebuilt
# helper is blocked with EHOSTUNREACH until the user allows it again).
build_universal() {
  local pkg="$1" out="$2" ident="$3" tmp
  tmp="$(mktemp -d)"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64  go build -C "$REPO" -o "$tmp/arm64" "./cmd/$pkg"
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64  go build -C "$REPO" -o "$tmp/amd64" "./cmd/$pkg"
  lipo -create "$tmp/arm64" "$tmp/amd64" -output "$MACOS_DIR/$out"
  rm -rf "$tmp"
  # Inside-out sign: hardened runtime + timestamp, same identity as the app.
  codesign --force --sign "$IDENTITY" ${KEYCHAIN_ARGS[@]+"${KEYCHAIN_ARGS[@]}"} --identifier "$ident" --options runtime "$TIMESTAMP" "$MACOS_DIR/$out"
  echo "signed $out as $ident ($(lipo -info "$MACOS_DIR/$out" | sed 's/.*: //'))"
}

build_universal ember-claude-producer ember-claude-producer com.ember.claude-producer
build_universal ember-codex-producer  ember-codex-producer  com.ember.codex-producer

# Bundle the SMAppService LaunchAgent plists alongside the signed binaries.
LA_DIR="$APP/Contents/Library/LaunchAgents"
mkdir -p "$LA_DIR"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.heartbeat.plist" "$LA_DIR/"
cp "$REPO/macos/Ember/LaunchAgents/com.ember.codex.plist"     "$LA_DIR/"
for p in "$LA_DIR"/com.ember.*.plist; do plutil -lint "$p" >/dev/null; done
