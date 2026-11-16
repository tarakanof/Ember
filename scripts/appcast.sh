#!/bin/bash
# Usage: appcast.sh <feed-url> <X.Y.Z> <build> <min-macos> <ed-signature> <length> <notes.html>
set -euo pipefail

[ "$#" -eq 7 ] || { sed -n '2p' "$0" >&2; exit 2; }
FEED="$1" VERSION="$2" BUILD="$3" MIN_OS="$4" SIG="$5" LENGTH="$6" NOTES="$7"

die() { echo "error: $1" >&2; exit 1; }
suffix=/releases/latest/download/appcast.xml
[[ "$FEED" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/latest/download/appcast\.xml$ ]] ||
  die "feed URL '$FEED' is not a GitHub releases/latest appcast.xml URL"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version '$VERSION' is not X.Y.Z"
[[ "$BUILD" =~ ^[0-9]+$ ]] || die "build '$BUILD' is not a number"
[[ "$MIN_OS" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "minimum macOS '$MIN_OS' is not N.N"
[[ "$SIG" =~ ^[A-Za-z0-9+/]+=*$ ]] || die "signature is not base64"
[[ "$LENGTH" =~ ^[1-9][0-9]*$ ]] || die "length '$LENGTH' is not a positive number"
[ -s "$NOTES" ] || die "release notes file '$NOTES' is missing or empty"
! grep -q ']]>' "$NOTES" || die "release notes contain ']]>', which would end the CDATA section"

REPO="${FEED%"$suffix"}"
TAG="v$VERSION"

cat <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>Ember</title>
    <link>$FEED</link>
    <item>
      <title>Ember $VERSION</title>
      <sparkle:version>$BUILD</sparkle:version>
      <sparkle:shortVersionString>$VERSION</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>$MIN_OS</sparkle:minimumSystemVersion>
      <description><![CDATA[$(cat "$NOTES")
]]></description>
      <enclosure url="$REPO/releases/download/$TAG/Ember-$VERSION.zip" length="$LENGTH" type="application/octet-stream" sparkle:edSignature="$SIG"/>
    </item>
  </channel>
</rss>
XML
