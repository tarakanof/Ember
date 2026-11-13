#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Cut a release: bump the macOS app version, build, notarize and staple
# Ember.app, then tag and publish a GitHub Release carrying Ember-X.Y.Z.zip.
#
# Usage:
#   scripts/release.sh <X.Y.Z> [release title] [flags]
#   scripts/release.sh 0.9.0 "Foldable settings + weather-tile fix"
#
# Flags:
#   -y, --yes    skip the confirmation prompt (non-interactive)
#   --no-app     release without the app (no build, no zip), not marked latest
#   --app-only   build, notarize and staple Ember-X.Y.Z.zip, then stop:
#                no commit, tag or release; works from any branch
#
# Preconditions: a clean tree; `main` in sync with origin (except --app-only);
# `gh` authenticated; the tag must not exist. An app release also needs the
# Developer ID Application identity of DEVELOPMENT_TEAM in the keychain and the
# `ember-notary` notarytool profile (EMBER_NOTARY_PROFILE overrides the name).

usage() { sed -n '5,22p' "$0" >&2; exit 2; }

ASSUME_YES=0
APP=1
APP_ONLY=0
ARGS=()
for a in "$@"; do
  case "$a" in
    -y|--yes) ASSUME_YES=1 ;;
    --no-app) APP=0 ;;
    --app-only) APP_ONLY=1 ;;
    -h|--help) usage ;;
    -*) echo "unknown flag: $a" >&2; usage ;;
    *) ARGS+=("$a") ;;
  esac
done
set -- "${ARGS[@]:-}"
if [ "$APP" -eq 0 ] && [ "$APP_ONLY" -eq 1 ]; then
  echo "error: --no-app and --app-only exclude each other" >&2
  exit 2
fi

VERSION="${1:-}"
TITLE="${2:-}"
[ -n "$VERSION" ] || usage

VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: '$VERSION' is not strict semver X.Y.Z" >&2
  exit 1
fi
TAG="v$VERSION"
[ -n "$TITLE" ] || TITLE="$TAG"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
PROJECT_YML="macos/project.yml"
NOTARY_PROFILE="${EMBER_NOTARY_PROFILE:-ember-notary}"
ZIP_NAME="Ember-$VERSION.zip"

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: $1 not found${2:+ ($2)}" >&2; exit 1; }; }

if [ "$APP_ONLY" -eq 0 ]; then
  need gh "brew install gh"
  gh auth status >/dev/null 2>&1 || { echo "error: gh not authenticated (run: gh auth login)" >&2; exit 1; }
  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [ "$branch" != "main" ]; then
    echo "error: on branch '$branch'; releases are cut from main" >&2
    exit 1
  fi
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "error: working tree is dirty; commit or stash first" >&2
  exit 1
fi
if [ "$APP_ONLY" -eq 0 ]; then
  git fetch --quiet origin main
  if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
    echo "error: local main is not in sync with origin/main; pull/push first" >&2
    exit 1
  fi
  if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || \
     git ls-remote --exit-code --tags origin "$TAG" >/dev/null 2>&1; then
    echo "error: tag $TAG already exists" >&2
    exit 1
  fi
fi

current="$(sed -n 's/^[[:space:]]*MARKETING_VERSION:[[:space:]]*"\([^"]*\)".*/\1/p' "$PROJECT_YML" | head -1)"
[ -n "$current" ] || { echo "error: couldn't read MARKETING_VERSION from $PROJECT_YML" >&2; exit 1; }
build="$(sed -n 's/^[[:space:]]*CURRENT_PROJECT_VERSION:[[:space:]]*"\([0-9]*\)".*/\1/p' "$PROJECT_YML" | head -1)"
[ -n "$build" ] || { echo "error: couldn't read a numeric CURRENT_PROJECT_VERSION from $PROJECT_YML" >&2; exit 1; }
next_build=$((build + 1))
TEAM="$(sed -n 's/^[[:space:]]*DEVELOPMENT_TEAM:[[:space:]]*"\{0,1\}\([A-Z0-9]*\)"\{0,1\}[[:space:]]*$/\1/p' "$PROJECT_YML" | head -1)"

notary_setup() {
  cat >&2 <<EOF
One-time setup (see issue #835):
  1. https://account.apple.com › Sign-In and Security › App-Specific Passwords › +,
     name it $NOTARY_PROFILE, and copy the password.
  2. xcrun notarytool store-credentials $NOTARY_PROFILE --apple-id <your Apple ID> --team-id $TEAM
  3. Paste the app-specific password when asked.
Or cut a server-only release with --no-app.
EOF
}

if [ "$APP" -eq 1 ]; then
  [ -n "$TEAM" ] || { echo "error: couldn't read DEVELOPMENT_TEAM from $PROJECT_YML" >&2; exit 1; }
  need xcodegen "brew install xcodegen"
  need xcodebuild "install Xcode"
  need xcrun "install Xcode"
  need jq "brew install jq"
  if ! security find-identity -v -p codesigning 2>/dev/null | grep "\"Developer ID Application: .* ($TEAM)\"" >/dev/null; then
    echo "error: no valid 'Developer ID Application: … ($TEAM)' identity in the keychain" >&2
    echo "Install the Developer ID Application certificate of team $TEAM (Xcode › Settings › Accounts › Manage Certificates), or cut a server-only release with --no-app." >&2
    exit 1
  fi
  if ! xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
    echo "error: notarytool profile '$NOTARY_PROFILE' is missing or its credentials are rejected" >&2
    notary_setup
    exit 1
  fi
fi

echo "Release plan:"
echo "  app version : $current -> $VERSION  ($PROJECT_YML)"
echo "  app build   : $build -> $next_build"
if [ "$APP" -eq 1 ]; then
  echo "  app         : $ZIP_NAME, Developer ID ($TEAM), notarized with profile $NOTARY_PROFILE"
else
  echo "  app         : none (--no-app)"
fi
if [ "$APP_ONLY" -eq 1 ]; then
  echo "  stops after : the stapled zip (--app-only: no commit, tag or release)"
else
  echo "  tag         : $TAG  (on $(git rev-parse --short HEAD))"
  echo "  title       : $TITLE"
  echo "  publishes   : GitHub Release -> docker-publish.yml -> Docker Hub (:$VERSION, :latest)"
fi
if [ "$ASSUME_YES" -ne 1 ]; then
  printf 'Proceed? [y/N] '
  read -r reply
  case "$reply" in y|Y|yes|YES) ;; *) echo "aborted."; exit 1 ;; esac
fi

WORK=""
RESTORE=0
finish() {
  local rc=$?
  if [ "$RESTORE" -eq 1 ]; then
    git checkout -- "$PROJECT_YML"
    [ "$rc" -eq 0 ] || echo "restored $PROJECT_YML; nothing was committed, tagged or pushed" >&2
  fi
  if [ -n "$WORK" ] && [ "$rc" -ne 0 ]; then
    echo "build folder kept: $WORK" >&2
  fi
  return "$rc"
}
trap finish EXIT

sed -i.bak "s/^\([[:space:]]*MARKETING_VERSION:[[:space:]]*\"\)[^\"]*\(\".*\)/\1$VERSION\2/" "$PROJECT_YML"
sed -i.bak "s/^\([[:space:]]*CURRENT_PROJECT_VERSION:[[:space:]]*\"\)[^\"]*\(\".*\)/\1$next_build\2/" "$PROJECT_YML"
rm -f "$PROJECT_YML.bak"
RESTORE=1

signed_by_developer_id() {
  local info
  info="$(codesign -dvv "$1" 2>&1)" || { echo "FAIL not signed: $1" >&2; return 1; }
  if ! printf '%s\n' "$info" | grep -q "^Authority=Developer ID Application: .* ($TEAM)$" ||
     ! printf '%s\n' "$info" | grep -qx "TeamIdentifier=$TEAM"; then
    echo "FAIL not signed with Developer ID ($TEAM): $1" >&2
    printf '%s\n' "$info" | grep -E '^(Authority|TeamIdentifier|Signature)=' >&2 || true
    return 1
  fi
  echo "OK   Developer ID ($TEAM): $(basename "$1")"
}

ZIP=""
if [ "$APP" -eq 1 ]; then
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/ember-release-$VERSION.XXXXXX")"
  ( cd macos && xcodegen generate >/dev/null )
  echo "building Ember.app $VERSION ($next_build) into $WORK"
  xcodebuild -project macos/Ember.xcodeproj -scheme Ember -configuration Release \
    -derivedDataPath "$WORK/dd" -quiet OTHER_CODE_SIGN_FLAGS=--timestamp \
    CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO build
  BUILT="$WORK/dd/Build/Products/Release/Ember.app"
  scripts/verify-bundle.sh "$BUILT"
  signed_by_developer_id "$BUILT"
  if codesign -d --entitlements - --xml "$BUILT" 2>/dev/null | grep "com.apple.security.get-task-allow" >/dev/null; then
    echo "error: Ember.app carries the get-task-allow entitlement; notarization rejects it" >&2
    exit 1
  fi
  for helper in "$BUILT"/Contents/MacOS/ember-*; do
    signed_by_developer_id "$helper"
  done

  ditto -c -k --keepParent "$BUILT" "$WORK/notarize.zip"
  echo "submitting to the notary service (profile $NOTARY_PROFILE); this takes a few minutes"
  result="$(xcrun notarytool submit "$WORK/notarize.zip" --keychain-profile "$NOTARY_PROFILE" --wait --output-format json)" || true
  status="$(jq -r '.status // empty' <<<"$result" 2>/dev/null || true)"
  submission="$(jq -r '.id // empty' <<<"$result" 2>/dev/null || true)"
  if [ "$status" != "Accepted" ]; then
    echo "error: notarization ${status:-failed} (submission ${submission:-none})" >&2
    printf '%s\n' "$result" >&2
    if [ -n "$submission" ]; then
      xcrun notarytool log "$submission" --keychain-profile "$NOTARY_PROFILE" >&2 || true
    fi
    exit 1
  fi
  echo "notarized: submission $submission"

  xcrun stapler staple "$BUILT"
  xcrun stapler validate "$BUILT"
  assessment="$(spctl -a -vv -t exec "$BUILT" 2>&1)" || { printf '%s\n' "$assessment" >&2; echo "error: Gatekeeper rejects the stapled app" >&2; exit 1; }
  printf '%s\n' "$assessment"
  printf '%s\n' "$assessment" | grep -q "source=Notarized Developer ID" ||
    { echo "error: Gatekeeper does not see a notarized Developer ID app" >&2; exit 1; }

  ZIP="$WORK/$ZIP_NAME"
  ditto -c -k --keepParent "$BUILT" "$ZIP"
  echo "app: $ZIP"
fi

if [ "$APP_ONLY" -eq 1 ]; then
  echo "--app-only: $ZIP is notarized and stapled; $PROJECT_YML is restored"
  exit 0
fi

git add "$PROJECT_YML"
git commit -m "chore(release): $TAG" >/dev/null
RESTORE=0
git tag -a "$TAG" -m "$TITLE"

git push origin main
git push origin "$TAG"

if [ "$APP" -eq 1 ]; then
  NOTES="**Install the app:** download \`$ZIP_NAME\` below, double-click it, drag **Ember** to **Applications** and open it."
  gh release create "$TAG" --verify-tag --draft --title "$TITLE" --notes "$NOTES" --generate-notes
  if ! gh release upload "$TAG" "$ZIP" || ! gh release edit "$TAG" --draft=false; then
    echo "error: $TAG is tagged and pushed, but its release is still a draft. Finish it with:" >&2
    echo "  gh release upload $TAG '$ZIP' --clobber && gh release edit $TAG --draft=false" >&2
    exit 1
  fi
  rm -rf "$WORK"
  WORK=""
else
  gh release create "$TAG" --verify-tag --latest=false --title "$TITLE" --generate-notes
fi

echo
echo "Released $TAG. Docker build is running:"
gh run list --workflow=docker-publish.yml --limit 1 2>/dev/null || true
echo "Then update the container on the Unraid host to pull the new image."
