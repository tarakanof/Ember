#!/usr/bin/env bash
set -euo pipefail

# A stable code-signing identity for local (non-Developer-ID) builds.
#
# Why: macOS Local Network privacy (and Little Snitch) key their grants to the
# code's designated requirement. For ad-hoc code that is the cdhash, so every
# rebuild is a new program: NWBrowser fails with NoAuth (-65555) and LAN
# connections fail with "Network is down" until the user re-approves it. Code
# signed with one self-signed certificate gets the requirement
#   identifier "com.ember.Ember" and certificate leaf = H"<sha1>"
# which stays the same across rebuilds, so the grant survives.
#
# The certificate is self-signed and NOT trusted: no admin rights and no trust
# settings change. codesign signs with an untrusted identity fine; only
# Gatekeeper would object, and a locally built app never goes through it.
#
# Usage:
#   local-signing-identity.sh            create it if missing (idempotent), print its SHA-1
#   local-signing-identity.sh --check    report whether it exists (exit 1 if not)
#   local-signing-identity.sh --hash     print only the SHA-1 (exit 1 if missing), for scripts
#   local-signing-identity.sh --sign APP re-sign a built Ember.app inside-out with it
#   local-signing-identity.sh --remove   delete it from the keychain
#
# EMBER_SIGNING_KEYCHAIN overrides the keychain (default: the login keychain),
# e.g. a scratch keychain for testing.

NAME="Ember Local Signing"
KEYCHAIN="${EMBER_SIGNING_KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}"

# identity_hash prints the SHA-1 of the first code-signing identity called
# $NAME, valid or not (untrusted self-signed identities are listed only
# without -v, flagged CSSMERR_TP_NOT_TRUSTED).
identity_hash() {
  security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null |
    awk -v n="\"$NAME\"" 'index($0, n) { print $2; exit }'
}

create() {
  local hash pass
  hash="$(identity_hash)"
  if [ -n "$hash" ]; then
    echo "$NAME already exists: $hash"
    return 0
  fi
  tmp="$(mktemp -d)" # global: the EXIT trap runs after create() returns
  trap 'rm -rf "$tmp"' EXIT
  chmod 700 "$tmp"
  cat >"$tmp/cert.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
EOF
  # /usr/bin/openssl (LibreSSL) on purpose: its PKCS#12 defaults (3DES/SHA-1)
  # are what `security import` reads on every macOS; OpenSSL 3's AES
  # defaults are not.
  /usr/bin/openssl req -new -x509 -newkey rsa:2048 -nodes -days 3650 \
    -config "$tmp/cert.cnf" -keyout "$tmp/key.pem" -out "$tmp/cert.pem" 2>/dev/null
  pass="$(/usr/bin/openssl rand -hex 16)"
  /usr/bin/openssl pkcs12 -export -name "$NAME" -inkey "$tmp/key.pem" -in "$tmp/cert.pem" \
    -out "$tmp/id.p12" -passout "pass:$pass"
  # -x: the private key can't be exported again. -T: codesign may use it
  # without a keychain prompt.
  security import "$tmp/id.p12" -k "$KEYCHAIN" -f pkcs12 -P "$pass" -x -T /usr/bin/codesign >/dev/null
  hash="$(identity_hash)"
  [ -n "$hash" ] || { echo "error: imported, but no '$NAME' code-signing identity in $KEYCHAIN" >&2; exit 1; }
  echo "created $NAME: $hash"
}

check() {
  local hash
  hash="$(identity_hash)"
  if [ -z "$hash" ]; then
    echo "$NAME: not found in $KEYCHAIN (run scripts/local-signing-identity.sh to create it)"
    exit 1
  fi
  echo "$NAME: $hash (in $KEYCHAIN)"
}

print_hash() {
  local hash
  hash="$(identity_hash)"
  [ -n "$hash" ] || exit 1
  echo "$hash"
}

remove() {
  local hash n=0
  while hash="$(identity_hash)"; [ -n "$hash" ]; do
    security delete-identity -Z "$hash" "$KEYCHAIN" >/dev/null
    n=$((n + 1))
    [ "$n" -lt 10 ] || { echo "error: $NAME still present after $n deletions" >&2; exit 1; }
  done
  echo "removed $n '$NAME' identit$([ "$n" = 1 ] && echo y || echo ies) from $KEYCHAIN"
}

# sign_app re-signs a built Ember.app with the identity, inside-out: nested
# frameworks, then the helper executables under Contents/MacOS (keeping the
# com.ember.* identifiers build-producers.sh gave them), then the app.
# Hardened runtime and the entitlements are kept, except get-task-allow,
# which Xcode injects into ad-hoc builds and a Release install must not have.
sign_app() {
  local app="$1" hash main f name ents
  [ -d "$app" ] || { echo "error: no such app bundle: $app" >&2; exit 2; }
  hash="$(identity_hash)"
  [ -n "$hash" ] || { echo "error: no '$NAME' identity; run scripts/local-signing-identity.sh first" >&2; exit 1; }
  local cs=(codesign --force --sign "$hash" --keychain "$KEYCHAIN" --options runtime --timestamp=none)

  if [ -d "$app/Contents/Frameworks" ]; then
    for f in "$app"/Contents/Frameworks/*; do
      [ -e "$f" ] || continue
      "${cs[@]}" --preserve-metadata=identifier,entitlements "$f"
    done
  fi
  main="$(plutil -extract CFBundleExecutable raw "$app/Contents/Info.plist")"
  for f in "$app"/Contents/MacOS/*; do
    name="$(basename "$f")"
    [ "$name" != "$main" ] || continue
    "${cs[@]}" --preserve-metadata=identifier "$f"
  done
  ents="$(mktemp)"
  codesign -d --entitlements - --xml "$app" >"$ents" 2>/dev/null || true
  if [ -s "$ents" ]; then
    /usr/libexec/PlistBuddy -c 'Delete :com.apple.security.get-task-allow' "$ents" >/dev/null 2>&1 || true
  fi
  if [ -s "$ents" ] && [ "$(plutil -convert json -o - "$ents" 2>/dev/null)" != "{}" ]; then
    "${cs[@]}" --entitlements "$ents" "$app"
  else
    "${cs[@]}" "$app"
  fi
  rm -f "$ents"
  echo "signed $(basename "$app") with $NAME ($hash)"
  codesign -dr - "$app" 2>&1 | sed -n 's/^designated => /designated requirement: /p'
}

case "${1:-}" in
  "" | --create) create ;;
  --check) check ;;
  --hash) print_hash ;;
  --sign) [ -n "${2:-}" ] || { echo "usage: $0 --sign <Ember.app>" >&2; exit 2; }; sign_app "$2" ;;
  --remove) remove ;;
  -h | --help) sed -n '4,27p' "$0" ;;
  *) echo "usage: $0 [--create|--check|--hash|--sign APP|--remove]" >&2; exit 2 ;;
esac
