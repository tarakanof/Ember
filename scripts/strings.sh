#!/usr/bin/env bash
set -euo pipefail

# Keeps macos/Ember/Localizable.xcstrings in step with the code.
#
#   scripts/strings.sh sync  [DerivedData]   write every extracted key into the catalog
#   scripts/strings.sh check [DerivedData]   fail if a key is missing or a format
#                                            string has no translator comment
#
# The app target's keys come from the .stringsdata the compiler emits during an
# Xcode build (SWIFT_EMIT_LOC_STRINGS, set in project.yml). Pass the build's
# -derivedDataPath; without one the script builds the app unsigned into a temp
# dir first. EmberKit's keys come from compiling the package with the same
# flag: Xcode doesn't extract a package's strings into the app's catalog, so
# they stay "manual" entries and this script adds any that are missing.
#
# Sync is what Xcode does when it builds with the catalog open, plus the
# EmberKit keys, run from the command line so it works without the IDE.

MODE="${1:-}"
case "$MODE" in sync|check) ;; *) echo "usage: strings.sh sync|check [DerivedData]" >&2; exit 2 ;; esac
REPO="$(cd "$(dirname "$0")/.." && pwd)"
CATALOG="$REPO/macos/Ember/Localizable.xcstrings"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

DD="${2:-}"
if [ -z "$DD" ]; then
  DD="$WORK/dd"
  echo "strings.sh: building the app to extract its strings…" >&2
  (cd "$REPO/macos" && xcodegen generate >/dev/null)
  xcodebuild -project "$REPO/macos/Ember.xcodeproj" -scheme Ember -configuration Debug \
    -derivedDataPath "$DD" CODE_SIGNING_ALLOWED=NO build >"$WORK/build.log" 2>&1 \
    || { tail -40 "$WORK/build.log" >&2; exit 1; }
fi
APP_DATA="$(find "$DD/Build/Intermediates.noindex" -path '*/Ember.build/*' -name '*.stringsdata' 2>/dev/null | sort)"
[ -n "$APP_DATA" ] || { echo "strings.sh: no Ember .stringsdata under $DD (was it an Xcode build of the app?)" >&2; exit 1; }

echo "strings.sh: extracting EmberKit's strings…" >&2
mkdir -p "$WORK/kit"
swift build --package-path "$REPO/macos" --target EmberKit --scratch-path "$WORK/kitbuild" \
  -Xswiftc -emit-localized-strings -Xswiftc -emit-localized-strings-path -Xswiftc "$WORK/kit" \
  >"$WORK/kit.log" 2>&1 || { tail -40 "$WORK/kit.log" >&2; exit 1; }

printf '%s\n' "$APP_DATA" >"$WORK/app.list"
if [ "$MODE" = sync ]; then
  args=()
  while IFS= read -r f; do args+=(--stringsdata "$f"); done <"$WORK/app.list"
  xcrun xcstringstool sync "$CATALOG" "${args[@]}"
fi
python3 "$REPO/scripts/strings_catalog.py" "$MODE" "$CATALOG" "$WORK/app.list" "$WORK/kit"
