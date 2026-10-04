#!/usr/bin/env bash
set -euo pipefail

# Tests install-producers.sh against a fake release served from file://:
# checksum verification, binary placement, `install --headless` per selected
# producer, and refusal of a tampered archive. Fake producers only record
# their arguments, so nothing touches the real ~/.config or services.

SCRIPT="$(cd "$(dirname "$0")" && pwd)/install-producers.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux; case "$(uname -m)" in aarch64|arm64) arch=arm64 ;; *) arch=amd64 ;; esac ;;
  Darwin) os=darwin; arch=universal ;;
  *) echo "skip: unsupported OS"; exit 0 ;;
esac
name="ember-producers_${os}_${arch}"
rel="$WORK/release"
mkdir -p "$rel" "$WORK/stage/$name"
for p in claude codex t3; do
  cat >"$WORK/stage/$name/ember-$p-producer" <<SH
#!/bin/sh
echo "\$0 \$*" >>"$WORK/calls"
SH
  chmod 0755 "$WORK/stage/$name/ember-$p-producer"
done
echo "9.9.9" >"$WORK/stage/$name/VERSION"
tar -C "$WORK/stage" -czf "$rel/$name.tar.gz" "$name"
if command -v sha256sum >/dev/null 2>&1; then sum=$(sha256sum "$rel/$name.tar.gz" | cut -d' ' -f1); else sum=$(shasum -a 256 "$rel/$name.tar.gz" | cut -d' ' -f1); fi
echo "$sum  $name.tar.gz" >"$rel/SHA256SUMS"

bin="$WORK/bin"
EMBER_RELEASE_BASE_URL="file://$rel" HOME="$WORK/home" sh "$SCRIPT" --force --bin-dir "$bin" --producers "claude codex" >"$WORK/out" 2>&1 ||
  { cat "$WORK/out"; fail "install run failed"; }
for p in claude codex t3; do
  [ -x "$bin/ember-$p-producer" ] || fail "ember-$p-producer not installed"
done
grep -q "ember-claude-producer install --headless" "$WORK/calls" || fail "claude install not run: $(cat "$WORK/calls")"
grep -q "ember-codex-producer install --headless" "$WORK/calls" || fail "codex install not run"
! grep -q "ember-t3-producer" "$WORK/calls" || fail "t3 install run although not selected"
grep -q "Checksum OK" "$WORK/out" || fail "no checksum confirmation"
echo "ok: install + checksum"

: >"$WORK/calls"
echo "tampered" >>"$rel/$name.tar.gz"
if EMBER_RELEASE_BASE_URL="file://$rel" HOME="$WORK/home" sh "$SCRIPT" --force --bin-dir "$WORK/bin2" >"$WORK/out" 2>&1; then
  fail "tampered archive accepted"
fi
grep -q "checksum mismatch" "$WORK/out" || fail "no checksum mismatch message: $(cat "$WORK/out")"
[ ! -e "$WORK/bin2/ember-claude-producer" ] || fail "binaries copied from a tampered archive"
[ ! -s "$WORK/calls" ] || fail "install ran after a checksum mismatch"
echo "ok: tampered archive refused"

if sh "$SCRIPT" --producers "claude bogus" --no-install >"$WORK/out" 2>&1; then
  fail "unknown producer accepted"
fi
echo "ok: unknown producer refused"

[ "$(tail -n 1 "$SCRIPT")" = 'main "$@"' ] || fail "script must end by calling main (curl | sh truncation guard)"
half=$(( $(wc -l <"$SCRIPT") / 2 ))
if ! head -n "$half" "$SCRIPT" | EMBER_RELEASE_BASE_URL="file://$rel" HOME="$WORK/home" sh -s -- --force --bin-dir "$WORK/bin3" >"$WORK/out" 2>&1; then
  : # a syntax error from the cut is fine; running a fragment is not
fi
[ ! -e "$WORK/bin3" ] || fail "a truncated script did work"
echo "ok: truncated download is inert"

echo "install-producers_test.sh: PASS"
