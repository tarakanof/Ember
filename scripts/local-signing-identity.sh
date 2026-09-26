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
# A certificate Apple issued you (an "Apple Development" identity from Xcode)
# is the better choice when you have one: its requirement is
#   identifier "com.ember.Ember" and anchor apple generic and
#   certificate leaf[subject.CN] = "Apple Development: <name> (<id>)" and
#   certificate 1[field.1.2.840.113635.100.6.2.1] /* exists */
# which names the certificate's subject, not its hash, so it also holds for a
# renewed certificate with the same name. Select it with
# EMBER_SIGNING_IDENTITY, or once for good in ~/.config/ember/signing-identity
# ($XDG_CONFIG_HOME/ember/signing-identity when XDG_CONFIG_HOME is set; the
# first non-blank line after stripping # comments and whitespace). Either holds
# a SHA-1 or an exact identity name of a valid identity (find-identity -v); a
# name that matches more than one is an error, so prefer the SHA-1.
# EMBER_SIGNING_IDENTITY=- forces ad-hoc for one build, ignoring the file.
#
# Which identity signs (--hash, --sign, --check): that override, else the
# self-signed "Ember Local Signing" identity, else none (the caller leaves the
# build ad-hoc). An override that names no valid identity, or a config file
# that can't be read, is an error, not a silent fallback.
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

# identity_hash prints the SHA-1 of the first code-signing identity called
# $NAME, valid or not (untrusted self-signed identities are listed only
# without -v, flagged CSSMERR_TP_NOT_TRUSTED).
identity_hash() {
  security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null |
    awk -v n="\"$NAME\"" 'index($0, n) { print $2; exit }'
}

# override prints the configured identity (SHA-1 or name):
# EMBER_SIGNING_IDENTITY, else the first non-blank line of $IDENTITY_FILE
# (# starts a comment), else nothing.
override() {
  if [ -n "${EMBER_SIGNING_IDENTITY:-}" ]; then
    echo "$EMBER_SIGNING_IDENTITY"
  elif [ -e "$IDENTITY_FILE" ]; then
    [ -r "$IDENTITY_FILE" ] || return 1
    sed -e 's/#.*//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$IDENTITY_FILE" | awk 'NF { print; exit }'
  fi
}

# matching prints the SHA-1 of every code-signing identity in $KEYCHAIN whose
# SHA-1 ($1 = sha) or exact name ($1 = name) is $2, deduped. With $3 = -v, only
# valid ones (not expired, revoked or untrusted); otherwise any, so an invalid
# match can be told apart from no match. Invalid identities carry a trailing
# "(CSSMERR_...)" after the quoted name.
matching() {
  local by="$1" want="$2" valid="${3:-}"
  security find-identity ${valid:+"$valid"} -p codesigning "$KEYCHAIN" 2>/dev/null |
    awk -v by="$by" -v w="$want" '
      /^ *[0-9]+\) [0-9A-F]+ "/ {
        name = $0; sub(/^[^"]*"/, "", name); sub(/"( \(CSSMERR_[A-Z_]+\))?$/, "", name)
        if ((by == "sha" && $2 == w) || (by == "name" && name == w)) print $2
      }' | sort -u
}

# override_hash resolves the override (a SHA-1 or an exact name) to the SHA-1
# of exactly one valid identity in $KEYCHAIN, or fails with a message on
# stderr: no such identity, only invalid ones, or several valid ones.
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

# resolve sets HASH and LABEL to the identity local builds sign with: the
# override, else "Ember Local Signing". Returns 0 when found, 1 when there is
# neither or the override is "-" (ad-hoc), 2 when the override is set but
# unusable or the config file can't be read (message on stderr). Callers run
# it under || where errexit is off, so every failure is checked explicitly.
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

# print_hash exits 1 when there is no identity, 2 when the override is unusable.
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

# sign_app re-signs a built Ember.app with the identity, inside-out: nested
# frameworks, then the helper executables under Contents/MacOS (keeping the
# com.ember.* identifiers build-producers.sh gave them), then the app.
# Hardened runtime and the entitlements are kept, except get-task-allow,
# which Xcode injects into ad-hoc builds and a Release install must not have.
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
