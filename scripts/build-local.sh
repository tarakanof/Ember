#!/usr/bin/env bash
set -euo pipefail

# Local Release build of Ember.app on a Mac without a Developer ID.
# Usage: build-local.sh [derived-data-dir]   (default: /tmp/ember-local-build)

REPO="$(cd "$(dirname "$0")/.." && pwd)"
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
DD="${1:-/tmp/ember-local-build}"

xcodegen generate --spec "$REPO/macos/project.yml" --project "$REPO/macos" >/dev/null
xcodebuild -project "$REPO/macos/Ember.xcodeproj" -scheme Ember -configuration Release \
  -derivedDataPath "$DD" -quiet \
  CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= \
  CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO \
  CURRENT_PROJECT_VERSION="${EMBER_BUILD_NUMBER:-2.$(date +%Y%m%d%H%M%S)}" build
APP="$DD/Build/Products/Release/Ember.app"

rc=0
"$REPO/scripts/local-signing-identity.sh" --hash >/dev/null || rc=$?
case "$rc" in
  0) "$REPO/scripts/local-signing-identity.sh" --sign "$APP" ;;
  1) echo "no local signing identity: leaving the app ad-hoc signed"
     echo "(set EMBER_SIGNING_IDENTITY / ~/.config/ember/signing-identity to your Apple Development"
     echo " SHA-1, or run scripts/local-signing-identity.sh once, to keep Local Network access across rebuilds)" ;;
  *) exit 1 ;;
esac

"$REPO/scripts/verify-bundle.sh" "$APP"
"$LSREGISTER" -u "$APP" 2>/dev/null || true
echo "built: $APP"
