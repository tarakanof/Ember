#!/usr/bin/env bash
# shellcheck disable=SC2016
set -euo pipefail

[ "$(uname -s)" = Darwin ] || { echo "skip: needs macOS (ditto)"; exit 0; }

SRC="$(cd "$(dirname "$0")" && pwd)/release.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $1" >&2; [ -f "$WORK/out" ] && cat "$WORK/out" >&2; exit 1; }

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid
TEAM=ABCDE12345
PUBKEY='cHVibGljLWtleS1mb3ItdGVzdHMtMDEyMzQ1Njc4OWFi'
SIG='c2lnbmF0dXJlLWZvci10ZXN0cw=='
FEED=https://github.com/tarakanof/ember/releases/latest/download/appcast.xml

FAKE="$WORK/fakebin"
mkdir -p "$FAKE"
stub() { printf '#!/bin/bash\nset -euo pipefail\necho "%s $*" >>"$CALLS"\n%s\n' "$1" "$2" >"$FAKE/$1"; chmod +x "$FAKE/$1"; }

stub gh '
case "$1 ${2:-}" in
  "auth status") exit 0 ;;
  "release upload")
    unzip -l "$4" >"$T_DIR/uploaded.txt"
    if [ -n "${5:-}" ]; then cp "$5" "$T_DIR/uploaded-appcast.xml"; fi
    [ "${FAKE_UPLOAD:-ok}" = ok ] ;;
  "run list") exit 0 ;;
  "release view") [ "${FAKE_PREV:-v1.2.2}" != none ] && echo "${FAKE_PREV:-v1.2.2}" ;;
  "pr view")
    case "$3" in
      7) echo "feat(app): show a <new> thing & more" ;;
      8) echo "docs(agents): reviewer profile" ;;
      *) exit 1 ;;
    esac ;;
esac'
stub xcodegen ':'
stub xcodebuild '
dd=""; prev=""
for a in "$@"; do [ "$prev" = -derivedDataPath ] && dd="$a"; prev="$a"; done
app="$dd/Build/Products/Release/Ember.app"
mkdir -p "$app/Contents/MacOS"
for b in Ember ember-claude-producer ember-codex-producer ember-t3-producer ember-server; do echo bin >"$app/Contents/MacOS/$b"; done
plutil -create xml1 "$app/Contents/Info.plist"
plutil -insert SUFeedURL -string "'"$FEED"'" "$app/Contents/Info.plist"
plutil -insert SUPublicEDKey -string "${FAKE_BUILT_KEY:-'"$PUBKEY"'}" "$app/Contents/Info.plist"
plutil -insert LSMinimumSystemVersion -string 26.0 "$app/Contents/Info.plist"
fw="$app/Contents/Frameworks/Sparkle.framework"
mkdir -p "$fw/Versions/B/XPCServices/Installer.xpc" "$fw/Versions/B/XPCServices/Downloader.xpc" "$fw/Versions/B/Updater.app"
echo bin >"$fw/Versions/B/Sparkle"; echo bin >"$fw/Versions/B/Autoupdate"
chmod +x "$fw/Versions/B/Sparkle" "$fw/Versions/B/Autoupdate"
ln -s B "$fw/Versions/Current"
bin="$dd/SourcePackages/artifacts/sparkle/Sparkle/bin"
mkdir -p "$bin"
cp "$FAKE_SPARKLE/generate_keys" "$FAKE_SPARKLE/sign_update" "$bin/"'
stub security '
if [ "$1" = find-generic-password ]; then
  [ "$*" = "find-generic-password -s https://sparkle-project.org -a ember" ] || exit 44
  [ "${FAKE_EDKEY:-yes}" = yes ] || { echo "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain." >&2; exit 44; }
  exit 0
fi
case "${FAKE_DEVID:-yes}" in
  yes) echo "  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"Developer ID Application: Test Person ('"$TEAM"')\"" ;;
  *) echo "  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"Apple Development: Test Person (ZZZZZ99999)\"" ;;
esac
sleep 0.2
echo "     1 valid identities found"'
stub codesign '
if [ "$1" = -d ] && [ "${2:-}" = --entitlements ]; then
  [ "${FAKE_TASK_ALLOW:-no}" = yes ] && echo "<key>com.apple.security.get-task-allow</key><true/>"
  sleep 0.2
  echo "</dict></plist>"
  exit 0
fi
case "${FAKE_SIGNER:-devid}" in
  devid) printf "Authority=Developer ID Application: Test Person ('"$TEAM"')\nAuthority=Developer ID Certification Authority\nTimestamp=Nov 16, 2026 at 18:00:00\nTeamIdentifier='"$TEAM"'\n" >&2 ;;
  notimestamp) printf "Authority=Developer ID Application: Test Person ('"$TEAM"')\nAuthority=Developer ID Certification Authority\nTeamIdentifier='"$TEAM"'\n" >&2 ;;
  local) printf "Authority=Ember Local Signing\nTeamIdentifier=not set\n" >&2 ;;
esac'
stub xcrun '
case "$1 $2" in
  "notarytool history") [ "${FAKE_PROFILE:-yes}" = yes ] ;;
  "notarytool submit") printf "{\"id\":\"sub-1\",\"status\":\"%s\"}\n" "${FAKE_NOTARY:-Accepted}" ;;
  "notarytool log") echo "{\"issues\":[]}" ;;
  "stapler staple") touch "$3/Contents/stapled-ticket" ;;
  "stapler validate") [ -e "$3/Contents/stapled-ticket" ] ;;
esac'
stub spctl 'echo "$4: accepted" >&2; echo "source=Notarized Developer ID" >&2'

FAKE_SPARKLE="$WORK/fakesparkle"
mkdir -p "$FAKE_SPARKLE"
sparkle_stub() { printf '#!/bin/bash\nset -euo pipefail\necho "%s $*" >>"$CALLS"\n%s\n' "$1" "$2" >"$FAKE_SPARKLE/$1"; chmod +x "$FAKE_SPARKLE/$1"; }
sparkle_stub generate_keys '[ "$*" = "--account ember -p" ] && echo "'"$PUBKEY"'"'
sparkle_stub sign_update '[ "$1 $2" = "--account ember" ] || exit 1
len="$(stat -f%z "$3")"
case "${FAKE_SIGN:-ok}" in
  ok) echo "sparkle:edSignature=\"'"$SIG"'\" length=\"$len\"" ;;
  nosig) echo "length=\"$len\"" ;;
  badlen) echo "sparkle:edSignature=\"'"$SIG"'\" length=\"$((len + 1))\"" ;;
esac'

new_repo() {
  rm -rf "$WORK/origin.git" "$WORK/repo" "$WORK/uploaded.txt" "$WORK/tmp"
  mkdir -p "$WORK/tmp"
  : >"$WORK/calls.log"
  git init -q --bare "$WORK/origin.git"
  git init -q -b main "$WORK/repo"
  mkdir -p "$WORK/repo/scripts" "$WORK/repo/macos"
  cp "$SRC" "$WORK/repo/scripts/release.sh"
  cp "$(dirname "$SRC")/appcast.sh" "$(dirname "$SRC")/release-notes.sh" "$WORK/repo/scripts/"
  mkdir -p "$WORK/repo/macos/Ember"
  plutil -create xml1 "$WORK/repo/macos/Ember/Info.plist"
  plutil -insert SUPublicEDKey -string "${SOURCE_KEY:-$PUBKEY}" "$WORK/repo/macos/Ember/Info.plist"
  printf '#!/bin/bash\necho "verify-bundle.sh $*" >>"$CALLS"\n' >"$WORK/repo/scripts/verify-bundle.sh"
  chmod +x "$WORK/repo/scripts/"*.sh
  cat >"$WORK/repo/macos/project.yml" <<EOF
targets:
  Ember:
    settings:
      base:
        MARKETING_VERSION: "1.2.2"
        CURRENT_PROJECT_VERSION: "41"
        DEVELOPMENT_TEAM: $TEAM
EOF
  echo "*.xcodeproj" >"$WORK/repo/macos/.gitignore"
  git -C "$WORK/repo" add -A
  git -C "$WORK/repo" commit -qm init
  git -C "$WORK/repo" tag v1.2.2
  for pr in 7:feat-thing 8:docs-profile; do
    git -C "$WORK/repo" checkout -qb "${pr#*:}"
    git -C "$WORK/repo" commit -q --allow-empty -m "work for #${pr%%:*}"
    git -C "$WORK/repo" checkout -q main
    git -C "$WORK/repo" merge -q --no-ff -m "Merge pull request #${pr%%:*} from t/${pr#*:}" "${pr#*:}"
  done
  git -C "$WORK/repo" commit -q --allow-empty -m "fix(server): a direct fix on main"
  git -C "$WORK/repo" remote add origin "$WORK/origin.git"
  git -C "$WORK/repo" push -q origin main
  BASE_COUNT="$(git -C "$WORK/repo" rev-list --count HEAD)"
}

run_release() {
  CALLS="$WORK/calls.log" T_DIR="$WORK" FAKE_SPARKLE="$FAKE_SPARKLE" PATH="$FAKE:$PATH" TMPDIR="$WORK/tmp/" \
    "$WORK/repo/scripts/release.sh" "$@" >"$WORK/out" 2>&1
}
mkdir -p "$WORK/tmp"

line_of() { grep -n -m1 -- "$1" "$WORK/calls.log" | cut -d: -f1; }
untouched() {
  [ -z "$(git -C "$WORK/repo" status --porcelain)" ] || fail "$1: tree left dirty: $(git -C "$WORK/repo" status --porcelain)"
  grep -q 'MARKETING_VERSION: "1.2.2"' "$WORK/repo/macos/project.yml" || fail "$1: version bump not restored"
  [ "$(git -C "$WORK/repo" rev-list --count HEAD)" = "$BASE_COUNT" ] || fail "$1: a commit was made"
  [ "$(git -C "$WORK/repo" tag)" = v1.2.2 ] || fail "$1: a tag was made"
  [ -z "$(git -C "$WORK/origin.git" tag)" ] || fail "$1: a tag was pushed"
  ! grep -q -E '^gh release (create|upload|edit|delete)' "$WORK/calls.log" || fail "$1: a release was created"
}

new_repo
FAKE_PROFILE=missing run_release 1.2.3 --yes && fail "no notary profile: release went ahead"
grep -q "store-credentials ember-notary" "$WORK/out" || fail "no notary profile: setup steps not printed"
! grep -q '^xcodebuild' "$WORK/calls.log" || fail "no notary profile: built anyway"
untouched "no notary profile"
echo "ok: a missing notary profile stops before anything changes"

new_repo
FAKE_DEVID=no run_release 1.2.3 --yes && fail "no Developer ID: release went ahead"
grep -q "Developer ID Application: … ($TEAM)" "$WORK/out" || fail "no Developer ID: no explanation"
! grep -q '^xcrun' "$WORK/calls.log" || fail "no Developer ID: went on to the notary check"
untouched "no Developer ID"
echo "ok: a missing Developer ID identity stops before anything changes"

new_repo
FAKE_SIGNER=local run_release 1.2.3 --yes && fail "local signer: release went ahead"
grep -q "not signed with Developer ID" "$WORK/out" || fail "local signer: no explanation"
! grep -q '^xcrun notarytool submit' "$WORK/calls.log" || fail "local signer: submitted anyway"
untouched "local signer"
echo "ok: an app signed with the local fallback identity is refused and the bump restored"

new_repo
FAKE_TASK_ALLOW=yes run_release 1.2.3 --yes && fail "get-task-allow: release went ahead"
grep -q "get-task-allow entitlement" "$WORK/out" || fail "get-task-allow: no explanation"
! grep -q '^xcrun notarytool submit' "$WORK/calls.log" || fail "get-task-allow: submitted anyway"
grep -q '^xcodebuild .*CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO' "$WORK/calls.log" || fail "xcodebuild may inject get-task-allow"
untouched "get-task-allow"
echo "ok: a debuggable (get-task-allow) app is refused before notarization"

new_repo
FAKE_EDKEY=no run_release 1.2.3 --yes && fail "no Sparkle key: release went ahead"
grep -q "generate_keys --account ember -f" "$WORK/out" || fail "no Sparkle key: import steps not printed"
! grep -q '^xcodebuild' "$WORK/calls.log" || fail "no Sparkle key: built anyway"
untouched "no Sparkle key"
echo "ok: a missing Sparkle signing key stops before anything changes"

SOURCE_KEY='PLACEHOLDER+REPLACE+WITH+THE+OWNERS+EDKEY+A=' new_repo
run_release 1.2.3 --yes && fail "placeholder key: release went ahead"
grep -q "SUPublicEDKey is still the placeholder" "$WORK/out" || fail "placeholder key: no explanation"
! grep -q '^xcodebuild' "$WORK/calls.log" || fail "placeholder key: built anyway"
untouched "placeholder key"
echo "ok: a placeholder SUPublicEDKey stops before anything changes"

new_repo
FAKE_BUILT_KEY='b3RoZXIta2V5LW5vdC10aGUta2V5Y2hhaW4tb25lISE=' run_release 1.2.3 --yes && fail "key mismatch: release went ahead"
grep -q "does not match the Sparkle key in the keychain" "$WORK/out" || fail "key mismatch: no explanation"
! grep -q '^xcrun notarytool submit' "$WORK/calls.log" || fail "key mismatch: submitted anyway"
untouched "key mismatch"
echo "ok: an app whose SUPublicEDKey differs from the keychain key is refused before notarization"

new_repo
FAKE_SIGNER=notimestamp run_release 1.2.3 --yes && fail "no timestamp: release went ahead"
grep -q "no secure timestamp" "$WORK/out" || fail "no timestamp: no explanation"
! grep -q '^xcrun notarytool submit' "$WORK/calls.log" || fail "no timestamp: submitted anyway"
untouched "no timestamp"
echo "ok: code signed without a secure timestamp is refused before notarization"

for mode in nosig badlen; do
  new_repo
  FAKE_SIGN=$mode run_release 1.2.3 --yes && fail "sign_update $mode: release went ahead"
  grep -q "sign_update gave no signature for Ember-1.2.3.zip, or the wrong length" "$WORK/out" || fail "sign_update $mode: no explanation"
  grep -q '^sign_update ' "$WORK/calls.log" || fail "sign_update $mode: sign_update never ran"
  [ ! -e "$WORK/uploaded.txt" ] || fail "sign_update $mode: uploaded anyway"
  untouched "sign_update $mode"
done
echo "ok: a sign_update result with no signature or the wrong length stops before the commit"

new_repo
FAKE_NOTARY=Invalid run_release 1.2.3 --yes && fail "notarization rejected: release went ahead"
grep -q "notarization Invalid (submission sub-1)" "$WORK/out" || fail "notarization rejected: no submission id"
grep -q '^xcrun notarytool log sub-1' "$WORK/calls.log" || fail "notarization rejected: log not fetched"
! grep -q '^xcrun stapler' "$WORK/calls.log" || fail "notarization rejected: stapled anyway"
untouched "notarization rejected"
echo "ok: a rejected notarization prints its log and leaves no commit, tag or release"

new_repo
run_release 1.2.3 "Test release" --yes || fail "happy path failed"
for step in '^xcodebuild' '^verify-bundle.sh' '^xcrun notarytool submit' '^xcrun stapler staple' '^spctl' '^gh release create' '^gh release upload' '^gh release edit'; do
  [ -n "$(line_of "$step")" ] || fail "happy path: missing step $step"
done
[ "$(line_of '^xcrun stapler staple')" -lt "$(line_of '^gh release create')" ] || fail "happy path: released before stapling"
[ "$(line_of '^gh release upload')" -lt "$(line_of '^gh release edit')" ] || fail "happy path: published before the upload"
grep -q '^gh release create v1.2.3 --verify-tag --draft' "$WORK/calls.log" || fail "happy path: release not created as a draft"
! grep -q -- '--latest=false' "$WORK/calls.log" || fail "happy path: app release not marked latest"
grep -q '^gh release upload v1.2.3 [^ ]*/Ember-1.2.3.zip ' "$WORK/calls.log" || fail "happy path: wrong zip uploaded"
grep -q '^gh release edit v1.2.3 --draft=false' "$WORK/calls.log" || fail "happy path: draft not published"
grep -q 'Ember.app/Contents/stapled-ticket' "$WORK/uploaded.txt" || fail "happy path: the uploaded zip is not the stapled app"
[ "$(line_of '^xcrun stapler staple')" -lt "$(line_of '^sign_update')" ] || fail "happy path: signed before stapling"
fwv='Ember.app/Contents/Frameworks/Sparkle.framework/Versions/Current'
devsign="codesign --force --sign 0123456789ABCDEF0123456789ABCDEF01234567 --options runtime --timestamp"
for n in "$fwv/XPCServices/Installer.xpc" "$fwv/XPCServices/Downloader.xpc" "$fwv/Updater.app" "$fwv/Autoupdate" 'Ember.app/Contents/Frameworks/Sparkle.framework' 'Ember.app'; do
  [ -n "$(line_of "^$devsign --preserve-metadata=[a-z,]* [^ ]*/$n$")" ] || fail "happy path: $n not re-signed with Developer ID and a timestamp"
done
! grep -q "^$devsign .*/$fwv/Sparkle$" "$WORK/calls.log" || fail "happy path: framework binary signed apart from its bundle"
[ "$(line_of "/$fwv/Autoupdate$")" -lt "$(line_of '/Ember.app/Contents/Frameworks/Sparkle.framework$')" ] || fail "happy path: framework signed before its helpers"
[ "$(line_of '/Ember.app/Contents/Frameworks/Sparkle.framework$')" -lt "$(line_of "^$devsign --preserve-metadata=[a-z,]* [^ ]*/Ember.app$")" ] || fail "happy path: app signed before its framework"
[ "$(line_of "^$devsign --preserve-metadata=[a-z,]* [^ ]*/Ember.app$")" -lt "$(line_of '^verify-bundle.sh')" ] || fail "happy path: verified before re-signing"
grep -q "^codesign -dvv .*/Updater.app$" "$WORK/calls.log" || fail "happy path: nested Sparkle code not checked for Developer ID"
grep -q '^sign_update --account ember .*/Ember-1.2.3.zip$' "$WORK/calls.log" || fail "happy path: sign_update not run on the release zip with Ember's key"
grep -q '^gh release upload v1.2.3 .*/Ember-1.2.3.zip .*/appcast.xml$' "$WORK/calls.log" || fail "happy path: appcast not uploaded with the zip"
grep -q '<sparkle:version>42</sparkle:version>' "$WORK/uploaded-appcast.xml" || fail "happy path: appcast build number"
grep -q 'url="https://github.com/tarakanof/ember/releases/download/v1.2.3/Ember-1.2.3.zip"' "$WORK/uploaded-appcast.xml" || fail "happy path: appcast enclosure URL"
grep -q "sparkle:edSignature=\"$SIG\"" "$WORK/uploaded-appcast.xml" || fail "happy path: appcast signature"
grep -q 'length="[1-9][0-9]*"' "$WORK/uploaded-appcast.xml" || fail "happy path: appcast length"
! grep -q 'releaseNotesLink' "$WORK/uploaded-appcast.xml" || fail "happy path: appcast still links the GitHub page as notes"
grep -q '<li>Show a &lt;new&gt; thing &amp; more</li>' "$WORK/uploaded-appcast.xml" || fail "happy path: merged feat PR title missing from the notes"
grep -q '<li>A direct fix on main</li>' "$WORK/uploaded-appcast.xml" || fail "happy path: direct fix commit missing from the notes"
! grep -q -i 'reviewer profile\|init\|work for' "$WORK/uploaded-appcast.xml" || fail "happy path: non-user-facing change in the notes"
grep -q '^gh release view --json tagName' "$WORK/calls.log" || fail "happy path: previous app release not looked up"
grep -q 'MARKETING_VERSION: "1.2.3"' "$WORK/repo/macos/project.yml" || fail "happy path: version not bumped"
grep -q 'CURRENT_PROJECT_VERSION: "42"' "$WORK/repo/macos/project.yml" || fail "happy path: build not bumped"
[ "$(git -C "$WORK/origin.git" tag)" = v1.2.3 ] || fail "happy path: tag not pushed"
[ "$(git -C "$WORK/origin.git" log -1 --format=%s main)" = "chore(release): v1.2.3" ] || fail "happy path: release commit not pushed"
[ -z "$(find "$WORK/tmp" -mindepth 1 -maxdepth 1)" ] || fail "happy path: build folder left behind"
echo "ok: build, notarize and staple come before the tag; the stapled zip is uploaded to a draft that is then published"

new_repo
FAKE_UPLOAD=fail run_release 1.2.3 --yes && fail "upload failure: reported success"
grep -q "still a draft" "$WORK/out" || fail "upload failure: no recovery command"
! grep -q '^gh release edit' "$WORK/calls.log" || fail "upload failure: draft published without the app"
[ -n "$(find "$WORK/tmp" -name Ember-1.2.3.zip)" ] || fail "upload failure: zip deleted"
echo "ok: a failed upload leaves the release a draft and keeps the zip"

new_repo
FAKE_PROFILE=missing FAKE_DEVID=no FAKE_EDKEY=no run_release 1.2.3 --no-app --yes || fail "--no-app failed"
for tool in xcodebuild xcrun security codesign spctl verify-bundle.sh generate_keys sign_update 'gh release upload' 'gh release edit'; do
  ! grep -q "^$tool" "$WORK/calls.log" || fail "--no-app ran $tool"
done
grep -q '^gh release create v1.2.3 --verify-tag --latest=false --title' "$WORK/calls.log" || fail "--no-app: release not published directly, or marked latest"
[ "$(git -C "$WORK/origin.git" tag)" = v1.2.3 ] || fail "--no-app: tag not pushed"
echo "ok: --no-app releases without building or notarizing, and leaves latest on the last app release"

new_repo
git -C "$WORK/repo" checkout -qb feature
run_release 1.2.3 --app-only --yes || fail "--app-only failed"
grep -q '^xcrun stapler staple' "$WORK/calls.log" || fail "--app-only: not stapled"
[ -n "$(find "$WORK/tmp" -name Ember-1.2.3.zip)" ] || fail "--app-only: zip not kept"
[ -n "$(find "$WORK/tmp" -name appcast.xml)" ] || fail "--app-only: appcast not written"
git -C "$WORK/repo" checkout -q main
untouched "--app-only"
echo "ok: --app-only notarizes from any branch and changes nothing in git"

run_release 1.2.3 --no-app --app-only --yes && fail "--no-app with --app-only accepted"
echo "ok: --no-app and --app-only exclude each other"
