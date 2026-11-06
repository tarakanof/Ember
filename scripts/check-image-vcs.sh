#!/usr/bin/env bash
set -euo pipefail

# usage: scripts/check-image-vcs.sh <image> <commit-sha>

IMG="${1:-}"
SHA="${2:-}"
[ -n "$IMG" ] && [ -n "$SHA" ] || { echo "usage: check-image-vcs.sh <image> <commit-sha>" >&2; exit 2; }

out="$(docker run --rm "$IMG" version)"
echo "image version: $out"

case "$out" in
  *"+dirty"*)
    echo "::error::$IMG reports a dirty build of a clean commit; the Docker build context differs from the checkout (check .dockerignore against tracked files)" >&2
    exit 1 ;;
esac
case "$out" in
  *"($SHA,"*) echo "vcs: clean build of $SHA" ;;
  *)
    echo "::error::$IMG revision does not match $SHA (no .git in the build context?)" >&2
    exit 1 ;;
esac
