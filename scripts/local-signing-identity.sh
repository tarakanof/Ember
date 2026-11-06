#!/usr/bin/env bash
set -euo pipefail

# A stable code-signing identity for local (non-Developer-ID) builds.
#
# Usage:
#   local-signing-identity.sh            create "Ember Local Signing" if missing (idempotent), print its SHA-1
#   local-signing-identity.sh --check    report the identity local builds sign with (exit 1 if none)
#   local-signing-identity.sh --hash     print only its SHA-1 (exit 1 if none), for scripts
#   local-signing-identity.sh --sign APP re-sign a built Ember.app inside-out with it
#   local-signing-identity.sh --remove   delete "Ember Local Signing" from the keychain
#
# EMBER_SIGNING_KEYCHAIN overrides the keychain (default: the login keychain),
# e.g. a scratch keychain for testing. EMBER_SIGNING_IDENTITY_FILE overrides
# the config file path.

NAME="Ember Local Signing"
KEYCHAIN="${EMBER_SIGNING_KEYCHAIN:-$HOME/Library/Keychains/login.keychain-db}"
IDENTITY_FILE="${EMBER_SIGNING_IDENTITY_FILE:-${XDG_CONFIG_HOME:-$HOME/.config}/ember/signing-identity}"

identity_hash() {
  security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null |
    awk -v n="\"$NAME\"" 'index($0, n) { print $2; exit }'
}

override() {
  if [ -n "${EMBER_SIGNING_IDENTITY:-}" ]; then
    echo "$EMBER_SIGNING_IDENTITY"
  elif [ -e "$IDENTITY_FILE" ]; then
    [ -r "$IDENTITY_FILE" ] || return 1
    sed -e 's/#.*//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$IDENTITY_FILE" | awk 'NF { print; exit }'
  fi
}

matching() {
  local by="$1" want="$2" valid="${3:-}"
  security find-identity ${valid:+"$valid"} -p codesigning "$KEYCHAIN" 2>/dev/null |
    awk -v by="$by" -v w="$want" '
      /^ *[0-9]+\) [0-9A-F]+ "/ {
        name = $0; sub(/^[^"]*"/, "", name); sub(/"( \(CSSMERR_[A-Z_]+\))?$/, "", name)
        if ((by == "sha" && $2 == w) || (by == "name" && name == w)) print $2
      }' | sort -u
}

override_hash() {
  local want="$1" by=name hashes n what
  what="\"$want\""
  if printf '%s' "$want" | grep -Eq '^[0-9A-Fa-f]{40}$'; then
    by=sha
    want="$(printf '%s' "$want" | tr '[:lower:]' '[:upper:]')"
    what="$want"
  fi
  hashes="$(matching "$by" "$want" -v)"
  n="$(printf '%s' "$hashes" | grep -c . || true)"
  if [ "$n" = 0 ] && [ -n "$(matching "$by" "$want")" ]; then
    echo "error: signing identity override $what exists in $KEYCHAIN but is not valid (expired, revoked or untrusted)" >&2
    return 1
  fi
  case "$n" in
    1) echo "$hashes" ;;
    0) echo "error: signing identity override $what: no such code-signing identity in $KEYCHAIN" >&2; return 1 ;;
    *) echo "error: signing identity override \"$want\" matches $n identities; set it to one SHA-1:" >&2
       printf '%s\n' "$hashes" | sed 's/^/  /' >&2
       return 1 ;;
  esac
}

resolve() {
  local want
  HASH="" LABEL=""
  want="$(override)" || { echo "error: cannot read signing identity file $IDENTITY_FILE" >&2; return 2; }
  if [ "$want" = "-" ]; then
    LABEL="ad-hoc (signing identity override is \"-\")"
    return 1
  fi
  if [ -n "$want" ]; then
    HASH="$(override_hash "$want")" || return 2
    LABEL="$(security find-identity -v -p codesigning "$KEYCHAIN" 2>/dev/null |
      awk -v h="$HASH" '$2 == h { sub(/^[^"]*"/, ""); sub(/"[^"]*$/, ""); print; exit }')"
    return 0
  fi
  HASH="$(identity_hash)"
  LABEL="$NAME"
  [ -n "$HASH" ] || return 1
}

create() {
  local hash pass
  hash="$(identity_hash)"
  if [ -n "$hash" ]; then
    echo "$NAME already exists: $hash"
    return 0
  fi
  tmp="$(mktemp -d)"
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
  /usr/bin/openssl req -new -x509 -newkey rsa:2048 -nodes -days 3650 \
    -config "$tmp/cert.cnf" -keyout "$tmp/key.pem" -out "$tmp/cert.pem" 2>/dev/null
  pass="$(/usr/bin/openssl rand -hex 16)"
  /usr/bin/openssl pkcs12 -export -name "$NAME" -inkey "$tmp/key.pem" -in "$tmp/cert.pem" \
    -out "$tmp/id.p12" -passout "pass:$pass"
  security import "$tmp/id.p12" -k "$KEYCHAIN" -f pkcs12 -P "$pass" -x -T /usr/bin/codesign >/dev/null
  hash="$(identity_hash)"
  [ -n "$hash" ] || { echo "error: imported, but no '$NAME' code-signing identity in $KEYCHAIN" >&2; exit 1; }
  echo "created $NAME: $hash"
}

check() {
  local rc=0
  resolve || rc=$?
  case "$rc" in
    0) echo "$LABEL: $HASH (in $KEYCHAIN)" ;;
    2) exit 1 ;;
    *) if [ -n "$LABEL" ]; then
         echo "$LABEL"
       else
         echo "no local signing identity: no override (EMBER_SIGNING_IDENTITY or $IDENTITY_FILE)"
         echo "and no '$NAME' in $KEYCHAIN (run scripts/local-signing-identity.sh to create it)"
       fi
       exit 1 ;;
  esac
}

print_hash() {
  local rc=0
  resolve || rc=$?
  [ "$rc" = 0 ] || exit "$rc"
  echo "$HASH"
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

sign_app() {
  local app="$1" hash main f name ents rc=0
  [ -d "$app" ] || { echo "error: no such app bundle: $app" >&2; exit 2; }
  resolve || rc=$?
  [ "$rc" != 2 ] || exit 1
  [ "$rc" = 0 ] || { echo "error: no signing identity; set EMBER_SIGNING_IDENTITY or run scripts/local-signing-identity.sh first" >&2; exit 1; }
  hash="$HASH"
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
  echo "signed $(basename "$app") with $LABEL ($hash)"
  codesign -dr - "$app" 2>&1 | sed -n 's/^designated => /designated requirement: /p'
}

case "${1:-}" in
  "" | --create) create ;;
  --check) check ;;
  --hash) print_hash ;;
  --sign) [ -n "${2:-}" ] || { echo "usage: $0 --sign <Ember.app>" >&2; exit 2; }; sign_app "$2" ;;
  --remove) remove ;;
  -h | --help) sed -n '4,46p' "$0" ;;
  *) echo "usage: $0 [--create|--check|--hash|--sign APP|--remove]" >&2; exit 2 ;;
esac
