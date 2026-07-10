#!/usr/bin/env bash
set -euo pipefail

# Smoke test for build-producers.sh: builds into a throwaway App.app bundle
# with ad-hoc signing and asserts the resulting binaries are universal and
# signed.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_SCRIPT="$SCRIPT_DIR/build-producers.sh"

WORKDIR="$(mktemp -d)"
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

APP="$WORKDIR/App.app"
mkdir -p "$APP/Contents/MacOS"

CODE_SIGN_IDENTITY=- "$BUILD_SCRIPT" "$APP"

fail() { echo "FAIL: $1" >&2; exit 1; }

for bin in ember-claude-producer ember-codex-producer; do
  path="$APP/Contents/MacOS/$bin"
  [ -f "$path" ] || fail "$bin missing at $path"

  info="$(lipo -info "$path")"
  echo "$info" | grep -q "arm64" || fail "$bin: lipo -info missing arm64 ($info)"
  echo "$info" | grep -q "x86_64" || fail "$bin: lipo -info missing x86_64 ($info)"

  sig="$(codesign -dv "$path" 2>&1)"
  echo "$sig" | grep -q "Signature=adhoc" || fail "$bin: codesign -dv missing adhoc signature ($sig)"

  echo "ok: $bin ($info)"
done

for label in com.ember.heartbeat com.ember.codex; do
  plist="$APP/Contents/Library/LaunchAgents/$label.plist"
  [ -f "$plist" ] || fail "$label.plist missing at $plist"

  plutil -lint "$plist" >/dev/null || fail "$label.plist failed plutil -lint"

  got_label="$(plutil -extract Label raw -o - "$plist")"
  [ "$got_label" = "$label" ] || fail "$label.plist: Label '$got_label' != filename '$label'"

  bundle_program="$(plutil -extract BundleProgram raw -o - "$plist")"
  [ -f "$APP/$bundle_program" ] || fail "$label.plist: BundleProgram '$bundle_program' does not exist in bundle"

  echo "ok: $label.plist"
done

echo "build-producers_test.sh: PASS"
