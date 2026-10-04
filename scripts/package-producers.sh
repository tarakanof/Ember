#!/usr/bin/env bash
set -euo pipefail

# Builds the three producers for linux/amd64, linux/arm64 and darwin
# (universal) and packages one tar.gz per OS/arch plus SHA256SUMS, ready to
# attach to a GitHub release (.github/workflows/release-producers.yml).
#
# usage: scripts/package-producers.sh <version> [out-dir]
#   version: X.Y.Z or vX.Y.Z (stamped into `<producer> version`)
#   out-dir: default dist/producers
# The darwin archive needs lipo (macOS) or llvm-lipo.

VERSION="${1:-}"
[ -n "$VERSION" ] || { echo "usage: package-producers.sh <version> [out-dir]" >&2; exit 2; }
VERSION="${VERSION#v}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${2:-$REPO/dist/producers}"
PRODUCERS=(ember-claude-producer ember-codex-producer ember-t3-producer)

LIPO="$(command -v lipo || command -v llvm-lipo || true)"
[ -n "$LIPO" ] || { echo "package-producers.sh: lipo/llvm-lipo not found (needed for the darwin universal archive)" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  SHA=(sha256sum)
else
  SHA=(shasum -a 256)
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
rm -rf "$OUT"
mkdir -p "$OUT"

build() { # goos goarch outdir
  local goos="$1" goarch="$2" dir="$3" p
  mkdir -p "$dir"
  for p in "${PRODUCERS[@]}"; do
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -C "$REPO" -trimpath \
      -ldflags "-s -w -X main.version=$VERSION" -o "$dir/$p" "./cmd/$p"
  done
}

stage() { # name -> prints the staging dir with docs added
  local name="$1" dir="$WORK/stage/$1"
  mkdir -p "$dir"
  cp "$REPO/LICENSE" "$dir/LICENSE"
  printf '%s\n' "$VERSION" >"$dir/VERSION"
  sed "s/@VERSION@/$VERSION/g; s/@ARCHIVE@/$name.tar.gz/g" "$REPO/producers/release/README.md" >"$dir/README.md"
  echo "$dir"
}

# Archives list root:root and carry no macOS AppleDouble/xattr entries.
if tar --version 2>/dev/null | grep -q bsdtar; then
  TAR_OWNER=(--uid 0 --gid 0 --uname root --gname root --no-xattrs)
else
  TAR_OWNER=(--owner 0 --group 0 --numeric-owner)
fi

pack() { # name
  local name="$1"
  COPYFILE_DISABLE=1 tar "${TAR_OWNER[@]}" -C "$WORK/stage" -czf "$OUT/$name.tar.gz" "$name"
  echo "packaged $name.tar.gz"
}

for arch in amd64 arm64; do
  name="ember-producers_linux_$arch"
  dir="$(stage "$name")"
  build linux "$arch" "$dir"
  pack "$name"
done

build darwin arm64 "$WORK/darwin_arm64"
build darwin amd64 "$WORK/darwin_amd64"
name="ember-producers_darwin_universal"
dir="$(stage "$name")"
for p in "${PRODUCERS[@]}"; do
  "$LIPO" -create "$WORK/darwin_arm64/$p" "$WORK/darwin_amd64/$p" -output "$dir/$p"
  chmod 0755 "$dir/$p"
done
pack "$name"

(cd "$OUT" && "${SHA[@]}" ./*.tar.gz | sed 's# \./# #' >SHA256SUMS)
echo "wrote $OUT/SHA256SUMS"
cat "$OUT/SHA256SUMS"
