#!/usr/bin/env bash
set -euo pipefail

GEN="$(cd "$(dirname "$0")" && pwd)/appcast.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

SIG='aGVsbG8gd29ybGQgc2lnbmF0dXJlIGJ5dGVzIGZvciB0ZXN0aW5nIG9ubHkhIQ+/=='
REPO=https://github.com/tarakanof/ember
printf '<p>New &amp; fixed</p>\n<ul><li>One</li></ul>\n' >"$WORK/notes.html"

cat >"$WORK/want.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>Ember</title>
    <link>$REPO/releases/latest/download/appcast.xml</link>
    <item>
      <title>Ember 1.2.3</title>
      <sparkle:version>42</sparkle:version>
      <sparkle:shortVersionString>1.2.3</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>26.0</sparkle:minimumSystemVersion>
      <description><![CDATA[<p>New &amp; fixed</p>
<ul><li>One</li></ul>
]]></description>
      <enclosure url="$REPO/releases/download/v1.2.3/Ember-1.2.3.zip" length="12345678" type="application/octet-stream" sparkle:edSignature="$SIG"/>
    </item>
  </channel>
</rss>
XML

"$GEN" "$REPO/releases/latest/download/appcast.xml" 1.2.3 42 26.0 "$SIG" 12345678 "$WORK/notes.html" >"$WORK/got.xml" || fail "generation failed"
diff -u "$WORK/want.xml" "$WORK/got.xml" || fail "appcast differs from the expected XML"
echo "ok: fixed inputs give the exact appcast"

if command -v xmllint >/dev/null 2>&1; then
  xmllint --noout "$WORK/got.xml" || fail "appcast is not well-formed XML"
  echo "ok: the appcast is well-formed XML"
fi

bad() {
  if "$GEN" "$@" >/dev/null 2>"$WORK/err"; then fail "accepted: $*"; fi
}
FEED="$REPO/releases/latest/download/appcast.xml"
bad "$FEED" 1.2 42 26.0 "$SIG" 1 "$WORK/notes.html"
bad "$FEED" 1.2.3 4x 26.0 "$SIG" 1 "$WORK/notes.html"
bad "$FEED" 1.2.3 42 26 "$SIG" 1 "$WORK/notes.html"
bad "$FEED" 1.2.3 42 26.0 'not<base64>' 1 "$WORK/notes.html"
bad "$FEED" 1.2.3 42 26.0 "$SIG" 0 "$WORK/notes.html"
bad "$FEED" 1.2.3 42 26.0 "$SIG" -5 "$WORK/notes.html"
bad "https://example.com/feed.xml" 1.2.3 42 26.0 "$SIG" 1 "$WORK/notes.html"
bad "$REPO/releases/latest/download/appcast.xml?a=1&b=2" 1.2.3 42 26.0 "$SIG" 1 "$WORK/notes.html"
bad "$FEED" 1.2.3 42 26.0 "$SIG" 1
bad "$FEED" 1.2.3 42 26.0 "$SIG" 1 "$WORK/missing.html"
printf 'x ]]> y\n' >"$WORK/cdata.html"
bad "$FEED" 1.2.3 42 26.0 "$SIG" 1 "$WORK/cdata.html"
echo "ok: malformed version, build, macOS version, signature, length, feed URL or notes file is refused"
