#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Cut a release: bump the macOS app version, build, notarize and staple
# Ember.app, then tag and publish a GitHub Release carrying Ember-X.Y.Z.zip
# and the Sparkle appcast.xml that signs it.
#
# Usage:
#   scripts/release.sh <X.Y.Z> [release title] [flags]
#   scripts/release.sh 0.9.0 "Foldable settings + weather-tile fix"
#
# Flags:
#   -y, --yes    skip the confirmation prompt (non-interactive)
#   --no-app     release without the app (no build, no zip), not marked latest
#   --app-only   build, notarize and staple Ember-X.Y.Z.zip and write its
#                appcast.xml, then stop: no commit, tag or release; works
#                from any branch
#
# Preconditions: a clean tree; `main` in sync with origin (except --app-only);
# `gh` authenticated; the tag must not exist. An app release also needs the
# Developer ID Application identity of DEVELOPMENT_TEAM in the keychain, the
# `ember-notary` notarytool profile (EMBER_NOTARY_PROFILE overrides the name) and
# the Sparkle EdDSA private key in the login keychain, matching SUPublicEDKey.

usage() { sed -n '5,25p' "$0" >&2; exit 2; }

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
INFO_PLIST="macos/Ember/Info.plist"
SPARKLE_SERVICE="https://sparkle-project.org"
SPARKLE_ACCOUNT=ember
plist_get() { /usr/libexec/PlistBuddy -c "Print :$1" "$2" 2>/dev/null; }

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

sparkle_setup() {
  cat >&2 <<EOF
One-time setup (see issue #861), on the Mac that releases:
  1. Save the Sparkle private key from 1Password to ~/Desktop/ember-sparkle.key.
  2. Run: <bin>/generate_keys --account $SPARKLE_ACCOUNT -f ~/Desktop/ember-sparkle.key
     (<bin> is ~/Library/Developer/Xcode/DerivedData/Ember-*/SourcePackages/artifacts/sparkle/Sparkle/bin
     after any Xcode build of Ember).
  3. Delete ~/Desktop/ember-sparkle.key.
Or cut a server-only release with --no-app.
EOF
}

if [ "$APP" -eq 1 ]; then
  [ -n "$TEAM" ] || { echo "error: couldn't read DEVELOPMENT_TEAM from $PROJECT_YML" >&2; exit 1; }
  need xcodegen "brew install xcodegen"
  need xcodebuild "install Xcode"
  need xcrun "install Xcode"
  need jq "brew install jq"
  identities="$(security find-identity -v -p codesigning 2>/dev/null || true)"
  DEVID_HASH="$(sed -n "s/^ *[0-9]*) \([0-9A-F]\{40\}\) \"Developer ID Application: .* ($TEAM)\"\$/\1/p" <<<"$identities" | head -1)"
  if [ -z "$DEVID_HASH" ]; then
    echo "error: no valid 'Developer ID Application: … ($TEAM)' identity in the keychain" >&2
    echo "Install the Developer ID Application certificate of team $TEAM (Xcode › Settings › Accounts › Manage Certificates), or cut a server-only release with --no-app." >&2
    exit 1
  fi
  if ! xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
    echo "error: notarytool profile '$NOTARY_PROFILE' is missing or its credentials are rejected" >&2
    notary_setup
    exit 1
  fi
  source_key="$(plist_get SUPublicEDKey "$INFO_PLIST" || true)"
  case "$source_key" in
    "") echo "error: $INFO_PLIST has no SUPublicEDKey" >&2; exit 1 ;;
    PLACEHOLDER*)
      echo "error: $INFO_PLIST SUPublicEDKey is still the placeholder; set it to the public half of the Sparkle key (generate_keys -p)" >&2
      exit 1 ;;
  esac
  if ! security find-generic-password -s "$SPARKLE_SERVICE" -a "$SPARKLE_ACCOUNT" >/dev/null 2>&1; then
    echo "error: no Sparkle signing key for account '$SPARKLE_ACCOUNT' in the login keychain" >&2
    sparkle_setup
    exit 1
  fi
fi

echo "Release plan:"
echo "  app version : $current -> $VERSION  ($PROJECT_YML)"
echo "  app build   : $build -> $next_build"
if [ "$APP" -eq 1 ]; then
  echo "  app         : $ZIP_NAME, Developer ID ($TEAM), notarized with profile $NOTARY_PROFILE"
  echo "  update feed : appcast.xml signed with the Sparkle key in the login keychain"
else
  echo "  app         : none (--no-app)"
fi
if [ "$APP_ONLY" -eq 1 ]; then
  echo "  stops after : the stapled zip and its appcast.xml (--app-only: no commit, tag or release)"
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
  if ! grep -q "^Authority=Developer ID Application: .* ($TEAM)$" <<<"$info" ||
     ! grep -qx "TeamIdentifier=$TEAM" <<<"$info"; then
    echo "FAIL not signed with Developer ID ($TEAM): $1" >&2
    printf '%s\n' "$info" | grep -E '^(Authority|TeamIdentifier|Signature)=' >&2 || true
    return 1
  fi
  if ! grep -q '^Timestamp=' <<<"$info"; then
    echo "FAIL no secure timestamp in the signature: $1" >&2
    return 1
  fi
  echo "OK   Developer ID ($TEAM): $(basename "$1")"
}

framework_code() {
  local fw dir own n
  for fw in "$1"/Contents/Frameworks/*.framework; do
    [ -d "$fw" ] || continue
    dir="$fw/Versions/Current"
    own="$(basename "$fw" .framework)"
    for n in "$dir"/XPCServices/*.xpc "$dir"/*.app; do
      [ -e "$n" ] || continue
      echo "$n"
    done
    for n in "$dir"/*; do
      [ -f "$n" ] && [ -x "$n" ] && [ "$(basename "$n")" != "$own" ] || continue
      echo "$n"
    done
    echo "$fw"
  done
}

resign_with_developer_id() {
  local app="$1" n
  local cs=(codesign --force --sign "$DEVID_HASH" --options runtime --timestamp)
  while IFS= read -r n; do
    "${cs[@]}" --preserve-metadata=identifier,entitlements "$n"
  done < <(framework_code "$app")
  "${cs[@]}" --preserve-metadata=identifier,entitlements,requirements "$app"
}

previous_app_release() {
  local tag
  tag="$(gh release view --json tagName -q .tagName 2>/dev/null || true)"
  if [ -z "$tag" ] || ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  fi
  echo "$tag"
}

collect_titles() {
  local range="${1:+$1..}HEAD" prs direct n title
  prs="$(git log --first-parent --merges --format=%s "$range" |
    sed -n 's/^Merge pull request #\([0-9][0-9]*\) from .*/\1/p')"
  direct="$(git log --first-parent --no-merges --format=%s "$range")"
  for n in $prs; do
    if ! title="$(gh pr view "$n" --json title -q .title)" || [ -z "$title" ]; then
      echo "error: couldn't read the title of PR #$n (gh pr view); the release notes would be incomplete" >&2
      return 1
    fi
    printf '%s\n' "$title" >>"$WORK/titles.txt"
  done
  [ -z "$direct" ] || printf '%s\n' "$direct" >>"$WORK/titles.txt"
  echo "release notes: $(printf '%s' "$prs" | grep -c .) PR(s) and $(printf '%s' "$direct" | grep -c .) direct commit(s) since ${1:-the first commit}"
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
  resign_with_developer_id "$BUILT"
  scripts/verify-bundle.sh "$BUILT"
  signed_by_developer_id "$BUILT"
  while IFS= read -r nested; do
    signed_by_developer_id "$nested"
  done < <(framework_code "$BUILT")
  if codesign -d --entitlements - --xml "$BUILT" 2>/dev/null | grep "com.apple.security.get-task-allow" >/dev/null; then
    echo "error: Ember.app carries the get-task-allow entitlement; notarization rejects it" >&2
    exit 1
  fi
  for helper in "$BUILT"/Contents/MacOS/ember-*; do
    signed_by_developer_id "$helper"
  done
  SPARKLE_BIN="$WORK/dd/SourcePackages/artifacts/sparkle/Sparkle/bin"
  built_key="$(plist_get SUPublicEDKey "$BUILT/Contents/Info.plist" || true)"
  keychain_key="$("$SPARKLE_BIN/generate_keys" --account "$SPARKLE_ACCOUNT" -p | tr -d '[:space:]')"
  if [ -z "$built_key" ] || [ "$built_key" != "$keychain_key" ]; then
    echo "error: Ember.app's SUPublicEDKey does not match the Sparkle key in the keychain; no update signed with it could install" >&2
    echo "  app     : ${built_key:-none}" >&2
    echo "  keychain: ${keychain_key:-none}" >&2
    exit 1
  fi

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
  grep -q "source=Notarized Developer ID" <<<"$assessment" ||
    { echo "error: Gatekeeper does not see a notarized Developer ID app" >&2; exit 1; }

  ZIP="$WORK/$ZIP_NAME"
  ditto -c -k --keepParent "$BUILT" "$ZIP"
  echo "app: $ZIP"

  signed="$("$SPARKLE_BIN/sign_update" --account "$SPARKLE_ACCOUNT" "$ZIP")"
  ed_sig="$(sed -n 's/.*sparkle:edSignature="\([^"]*\)".*/\1/p' <<<"$signed")"
  ed_len="$(sed -n 's/.*length="\([0-9]*\)".*/\1/p' <<<"$signed")"
  if [ -z "$ed_sig" ] || [ "$ed_len" != "$(stat -f%z "$ZIP")" ]; then
    echo "error: sign_update gave no signature for $ZIP_NAME, or the wrong length" >&2
    exit 1
  fi
  FEED_URL="$(plist_get SUFeedURL "$BUILT/Contents/Info.plist")"
  : >"$WORK/titles.txt"
  collect_titles "$(previous_app_release)"
  scripts/release-notes.sh "${FEED_URL%/releases/latest/download/appcast.xml}/releases/tag/$TAG" \
    <"$WORK/titles.txt" >"$WORK/notes.html"
  APPCAST="$WORK/appcast.xml"
  scripts/appcast.sh "$FEED_URL" "$VERSION" "$next_build" \
    "$(plist_get LSMinimumSystemVersion "$BUILT/Contents/Info.plist")" "$ed_sig" "$ed_len" "$WORK/notes.html" >"$APPCAST"
  echo "appcast: $APPCAST"
fi

if [ "$APP_ONLY" -eq 1 ]; then
  echo "--app-only: $ZIP is notarized and stapled, $APPCAST signs it; $PROJECT_YML is restored"
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
  if ! gh release upload "$TAG" "$ZIP" "$APPCAST" || ! gh release edit "$TAG" --draft=false; then
    echo "error: $TAG is tagged and pushed, but its release is still a draft. Finish it with:" >&2
    echo "  gh release upload $TAG '$ZIP' '$APPCAST' --clobber && gh release edit $TAG --draft=false" >&2
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
