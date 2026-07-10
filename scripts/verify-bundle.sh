#!/usr/bin/env bash
set -euo pipefail

# Gate asserting the whole producer-bundling contract for a built Ember.app:
# both producers present + universal, both LaunchAgent plists valid and
# resolvable, and codesign verification inside-out (each producer, then the
# whole app). Reusable in CI/release, and for a quick local sanity check
# after `build-producers.sh`.
#
# NOT run here (ad-hoc dev limitation — need a Developer ID):
#   spctl -a -vv "$APP"   (Gatekeeper assessment)
#   notarization

APP="${1:-}"
[ -n "$APP" ] || { echo "usage: verify-bundle.sh <Ember.app>"; exit 2; }
[ -d "$APP" ] || { echo "FAIL: no such app bundle: $APP"; exit 1; }

MACOS_DIR="$APP/Contents/MacOS"
LA_DIR="$APP/Contents/Library/LaunchAgents"

check() {
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "OK   $desc"
  else
    echo "FAIL $desc"
    exit 1
  fi
}

check_producer() {
  local name="$1" bin="$MACOS_DIR/$1"
  [ -f "$bin" ] || { echo "FAIL producer present: $name"; exit 1; }
  echo "OK   producer present: $name"

  local archs
  archs="$(lipo -info "$bin" 2>/dev/null | sed 's/.*: //')"
  case "$archs" in
    *arm64*x86_64* | *x86_64*arm64*) echo "OK   universal (arm64 + x86_64): $name" ;;
    *) echo "FAIL universal (arm64 + x86_64): $name (got: $archs)"; exit 1 ;;
  esac

  check "codesign --verify --strict: $name" codesign --verify --strict "$bin"
}

check_plist() {
  local plist="$1" name
  name="$(basename "$plist")"
  [ -f "$plist" ] || { echo "FAIL plist present: $name"; exit 1; }
  echo "OK   plist present: $name"

  check "plutil -lint clean: $name" plutil -lint "$plist"

  local label
  label="$(plutil -extract Label raw "$plist")"
  local expected="${name%.plist}"
  if [ "$label" = "$expected" ]; then
    echo "OK   Label == filename: $name"
  else
    echo "FAIL Label == filename: $name (Label=$label, want=$expected)"
    exit 1
  fi

  local program
  program="$(plutil -extract BundleProgram raw "$plist")"
  if [ -f "$APP/$program" ]; then
    echo "OK   BundleProgram resolves: $name -> $program"
  else
    echo "FAIL BundleProgram resolves: $name -> $program (not found)"
    exit 1
  fi
}

check_producer ember-claude-producer
check_producer ember-codex-producer

check_plist "$LA_DIR/com.ember.heartbeat.plist"
check_plist "$LA_DIR/com.ember.codex.plist"

check "codesign --verify --deep --strict: $(basename "$APP")" codesign --verify --deep --strict "$APP"

echo "ALL CHECKS PASSED: $APP"
