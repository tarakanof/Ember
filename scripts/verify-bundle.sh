#!/usr/bin/env bash
set -euo pipefail

# Asserts the producer-bundling contract for a built Ember.app: producers
# present and universal, LaunchAgent plists valid, codesign verified inside-out.

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

  local ident
  ident="$(codesign -dv "$bin" 2>&1 | sed -n 's/^Identifier=//p')"
  if [ "$ident" = "com.ember.${name#ember-}" ]; then
    echo "OK   signing identifier: $name ($ident)"
  else
    echo "FAIL signing identifier: $name (got: $ident, want: com.ember.${name#ember-})"
    exit 1
  fi
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

  local argv0 argv1
  argv0="$(plutil -extract ProgramArguments.0 raw "$plist" 2>/dev/null)"
  argv1="$(plutil -extract ProgramArguments.1 raw "$plist" 2>/dev/null)"
  if [ "$argv0" = "$(basename "$program")" ] && [ "$argv1" = "run" ]; then
    echo "OK   ProgramArguments argv0/argv1: $name -> [$argv0, $argv1]"
  else
    echo "FAIL ProgramArguments must be [$(basename "$program"), run]: $name -> [$argv0, $argv1]"
    exit 1
  fi
}

check_producer ember-claude-producer
check_producer ember-codex-producer
check_producer ember-t3-producer

check_plist "$LA_DIR/com.ember.heartbeat.plist"
check_plist "$LA_DIR/com.ember.codex.plist"
check_plist "$LA_DIR/com.ember.t3.plist"

check "codesign --verify --deep --strict: $(basename "$APP")" codesign --verify --deep --strict "$APP"

signer="$(codesign -dvv "$APP" 2>&1 | sed -n 's/^Authority=//p' | head -1)"
if codesign -dv "$APP" 2>&1 | grep -q '^Signature=adhoc'; then
  signer="ad-hoc (Local Network access must be re-approved after every rebuild)"
fi
echo "INFO signed by: ${signer:-unknown}"
team="$(codesign -dv "$APP" 2>&1 | sed -n 's/^TeamIdentifier=//p')"
echo "INFO team: ${team:-none}"
echo "INFO $(codesign -dr - "$APP" 2>&1 | grep '^designated' || echo 'designated => (none)')"

echo "ALL CHECKS PASSED: $APP"
