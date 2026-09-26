#!/usr/bin/env bash
set -euo pipefail

# Local Release build of Ember.app on a Mac without a Developer ID.
#
# Builds ad-hoc (the only signing Xcode accepts without a trusted identity),
# then, when the "Ember Local Signing" identity exists (see
# local-signing-identity.sh), re-signs the bundle inside-out with it so its
# designated requirement, and with it the Local Network grant, survives
# rebuilds. Without the identity the app stays ad-hoc and macOS asks for
# Local Network access again after every rebuild.
#
# It does not install: copy the printed app into /Applications yourself
# (quit Ember first, then `ditto <app> /Applications/Ember.app`).
#
# Usage: build-local.sh [derived-data-dir]   (default: /tmp/ember-local-build)

REPO="$(cd "$(dirname "$0")/.." && pwd)"
DD="${1:-/tmp/ember-local-build}"

xcodegen generate --spec "$REPO/macos/project.yml" --project "$REPO/macos" >/dev/null
xcodebuild -project "$REPO/macos/Ember.xcodeproj" -scheme Ember -configuration Release \
  -derivedDataPath "$DD" -quiet \
  CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= build
APP="$DD/Build/Products/Release/Ember.app"

if "$REPO/scripts/local-signing-identity.sh" --hash >/dev/null; then
  "$REPO/scripts/local-signing-identity.sh" --sign "$APP"
else
  echo "no 'Ember Local Signing' identity: leaving the app ad-hoc signed"
  echo "(run scripts/local-signing-identity.sh once to keep Local Network access across rebuilds)"
fi

"$REPO/scripts/verify-bundle.sh" "$APP"
echo "built: $APP"
