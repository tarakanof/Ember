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

FAKE="$WORK/fakebin"
mkdir -p "$FAKE"
stub() { printf '#!/bin/bash\nset -euo pipefail\necho "%s $*" >>"$CALLS"\n%s\n' "$1" "$2" >"$FAKE/$1"; chmod +x "$FAKE/$1"; }

stub gh '
case "$1 ${2:-}" in
  "auth status") exit 0 ;;
  "release upload")
    unzip -l "$4" >"$T_DIR/uploaded.txt"
    [ "${FAKE_UPLOAD:-ok}" = ok ] ;;
  "run list") exit 0 ;;
esac'
stub xcodegen ':'
stub xcodebuild '
dd=""; prev=""
for a in "$@"; do [ "$prev" = -derivedDataPath ] && dd="$a"; prev="$a"; done
app="$dd/Build/Products/Release/Ember.app"
mkdir -p "$app/Contents/MacOS"
for b in Ember ember-claude-producer ember-codex-producer ember-t3-producer ember-server; do echo bin >"$app/Contents/MacOS/$b"; done'
stub security '
case "${FAKE_DEVID:-yes}" in
  yes) echo "  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"Developer ID Application: Test Person ('"$TEAM"')\"" ;;
  *) echo "  1) 0123456789ABCDEF0123456789ABCDEF01234567 \"Apple Development: Test Person (ZZZZZ99999)\"" ;;
esac
echo "     1 valid identities found"'
stub codesign '
case "${FAKE_SIGNER:-devid}" in
  devid) printf "Authority=Developer ID Application: Test Person ('"$TEAM"')\nAuthority=Developer ID Certification Authority\nTeamIdentifier='"$TEAM"'\n" >&2 ;;
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

new_repo() {
  rm -rf "$WORK/origin.git" "$WORK/repo" "$WORK/uploaded.txt" "$WORK/tmp"
  mkdir -p "$WORK/tmp"
  : >"$WORK/calls.log"
  git init -q --bare "$WORK/origin.git"
  git init -q -b main "$WORK/repo"
  mkdir -p "$WORK/repo/scripts" "$WORK/repo/macos"
  cp "$SRC" "$WORK/repo/scripts/release.sh"
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
  git -C "$WORK/repo" remote add origin "$WORK/origin.git"
  git -C "$WORK/repo" push -q origin main
}

run_release() {
  CALLS="$WORK/calls.log" T_DIR="$WORK" PATH="$FAKE:$PATH" TMPDIR="$WORK/tmp/" \
    "$WORK/repo/scripts/release.sh" "$@" >"$WORK/out" 2>&1
}
mkdir -p "$WORK/tmp"

line_of() { grep -n -m1 -- "$1" "$WORK/calls.log" | cut -d: -f1; }
untouched() {
  [ -z "$(git -C "$WORK/repo" status --porcelain)" ] || fail "$1: tree left dirty: $(git -C "$WORK/repo" status --porcelain)"
  grep -q 'MARKETING_VERSION: "1.2.2"' "$WORK/repo/macos/project.yml" || fail "$1: version bump not restored"
  [ "$(git -C "$WORK/repo" rev-list --count HEAD)" = 1 ] || fail "$1: a commit was made"
  [ -z "$(git -C "$WORK/repo" tag)" ] || fail "$1: a tag was made"
  [ -z "$(git -C "$WORK/origin.git" tag)" ] || fail "$1: a tag was pushed"
  ! grep -q '^gh release' "$WORK/calls.log" || fail "$1: a release was created"
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
grep -q '^gh release upload v1.2.3 .*/Ember-1.2.3.zip$' "$WORK/calls.log" || fail "happy path: wrong zip uploaded"
grep -q '^gh release edit v1.2.3 --draft=false' "$WORK/calls.log" || fail "happy path: draft not published"
grep -q 'Ember.app/Contents/stapled-ticket' "$WORK/uploaded.txt" || fail "happy path: the uploaded zip is not the stapled app"
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
FAKE_PROFILE=missing FAKE_DEVID=no run_release 1.2.3 --no-app --yes || fail "--no-app failed"
for tool in xcodebuild xcrun security codesign spctl verify-bundle.sh 'gh release upload' 'gh release edit'; do
  ! grep -q "^$tool" "$WORK/calls.log" || fail "--no-app ran $tool"
done
grep -q '^gh release create v1.2.3 --verify-tag --title' "$WORK/calls.log" || fail "--no-app: release not published directly"
[ "$(git -C "$WORK/origin.git" tag)" = v1.2.3 ] || fail "--no-app: tag not pushed"
echo "ok: --no-app releases without building or notarizing"

new_repo
git -C "$WORK/repo" checkout -qb feature
run_release 1.2.3 --app-only --yes || fail "--app-only failed"
grep -q '^xcrun stapler staple' "$WORK/calls.log" || fail "--app-only: not stapled"
[ -n "$(find "$WORK/tmp" -name Ember-1.2.3.zip)" ] || fail "--app-only: zip not kept"
git -C "$WORK/repo" checkout -q main
untouched "--app-only"
echo "ok: --app-only notarizes from any branch and changes nothing in git"

run_release 1.2.3 --no-app --app-only --yes && fail "--no-app with --app-only accepted"
echo "ok: --no-app and --app-only exclude each other"
