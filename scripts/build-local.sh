#!/usr/bin/env bash
set -euo pipefail

# Local Release build of Ember.app on a Mac without a Developer ID.
#
# Builds ad-hoc (the only signing Xcode accepts without a trusted identity)
# without the get-task-allow entitlement Xcode injects into such builds (a
# debugger-attachable Release app is no install candidate),
# then re-signs the bundle inside-out with a stable identity so its designated
# requirement, and with it the Local Network grant, survives rebuilds. The
# identity (see local-signing-identity.sh): EMBER_SIGNING_IDENTITY or
# ~/.config/ember/signing-identity (a SHA-1, e.g. your Apple Development
# certificate), else the self-signed "Ember Local Signing". Without either the
# app stays ad-hoc and macOS asks for Local Network access again after every
# rebuild.
#
# Each build gets a unique CFBundleVersion (EMBER_BUILD_NUMBER, default a
# yyyymmddHHMM stamp): with a fixed build number macOS keeps serving cached
# bundle info, e.g. an Info.plist whose NSBonjourServices predates a new
# service type, so its browse fails with NoAuth (-65555).
#
# It does not install: copy the printed app into /Applications yourself
# (quit Ember first, then `ditto <app> /Applications/Ember.app`).
#
# Usage: build-local.sh [derived-data-dir]   (default: /tmp/ember-local-build)

REPO="$(cd "$(dirname "$0")/.." && pwd)"
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
DD="${1:-/tmp/ember-local-build}"

xcodegen generate --spec "$REPO/macos/project.yml" --project "$REPO/macos" >/dev/null
xcodebuild -project "$REPO/macos/Ember.xcodeproj" -scheme Ember -configuration Release \
  -derivedDataPath "$DD" -quiet \
  CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= \
  CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO \
  CURRENT_PROJECT_VERSION="${EMBER_BUILD_NUMBER:-$(date +%Y%m%d%H%M)}" build
APP="$DD/Build/Products/Release/Ember.app"

rc=0
"$REPO/scripts/local-signing-identity.sh" --hash >/dev/null || rc=$?
case "$rc" in
  0) "$REPO/scripts/local-signing-identity.sh" --sign "$APP" ;;
  1) echo "no local signing identity: leaving the app ad-hoc signed"
     echo "(set EMBER_SIGNING_IDENTITY / ~/.config/ember/signing-identity to your Apple Development"
     echo " SHA-1, or run scripts/local-signing-identity.sh once, to keep Local Network access across rebuilds)" ;;
  *) exit 1 ;; # the override is set but names no single identity; the error is printed above
esac

"$REPO/scripts/verify-bundle.sh" "$APP"
# Building registered this copy with LaunchServices. macOS resolves
# com.ember.Ember through it for Local Network and NSBonjourServices checks, so
# a stray copy can shadow the installed app: unregister it.
"$LSREGISTER" -u "$APP" 2>/dev/null || true
echo "built: $APP"
