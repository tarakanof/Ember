#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Cut a release: bump the macOS app version, tag, and publish a GitHub Release —
# which triggers .github/workflows/docker-publish.yml to build + push the server
# image to Docker Hub. Keeps the app's MARKETING_VERSION and the release tag in
# lockstep so they can't drift (the only version that isn't already tag-derived).
#
# Usage:
#   scripts/release.sh <X.Y.Z> [release title]
#   scripts/release.sh 0.9.0
#   scripts/release.sh 0.9.0 "Foldable settings + weather-tile fix"
#
# Flags:
#   -y, --yes   skip the confirmation prompt (non-interactive)
#
# Preconditions: run from a clean `main` in sync with origin, with the `gh` CLI
# authenticated. The tag must not already exist.

usage() { sed -n '6,22p' "$0" >&2; exit 2; }

ASSUME_YES=0
ARGS=()
for a in "$@"; do
  case "$a" in
    -y|--yes) ASSUME_YES=1 ;;
    -h|--help) usage ;;
    -*) echo "unknown flag: $a" >&2; usage ;;
    *) ARGS+=("$a") ;;
  esac
done
set -- "${ARGS[@]:-}"

VERSION="${1:-}"
TITLE="${2:-}"
[ -n "$VERSION" ] || usage

# Strict semver vX.Y.Z, matching the gate in docker-publish.yml. Accept an
# optional leading "v" for convenience but normalise it away.
VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: '$VERSION' is not strict semver X.Y.Z" >&2
  exit 1
fi
TAG="v$VERSION"
[ -n "$TITLE" ] || TITLE="$TAG"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
PROJECT_YML="macos/project.yml"

# --- Preconditions -----------------------------------------------------------

command -v gh >/dev/null 2>&1 || { echo "error: gh CLI not installed" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "error: gh not authenticated (run: gh auth login)" >&2; exit 1; }

branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$branch" != "main" ]; then
  echo "error: on branch '$branch'; releases are cut from main" >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "error: working tree is dirty; commit or stash first" >&2
  exit 1
fi
git fetch --quiet origin main
if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
  echo "error: local main is not in sync with origin/main; pull/push first" >&2
  exit 1
fi
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || \
   git ls-remote --exit-code --tags origin "$TAG" >/dev/null 2>&1; then
  echo "error: tag $TAG already exists" >&2
  exit 1
fi

current="$(sed -n 's/^[[:space:]]*MARKETING_VERSION:[[:space:]]*"\([^"]*\)".*/\1/p' "$PROJECT_YML" | head -1)"
[ -n "$current" ] || { echo "error: couldn't read MARKETING_VERSION from $PROJECT_YML" >&2; exit 1; }

echo "Release plan:"
echo "  app version : $current -> $VERSION  ($PROJECT_YML)"
echo "  tag         : $TAG  (on $(git rev-parse --short HEAD))"
echo "  title       : $TITLE"
echo "  publishes   : GitHub Release -> docker-publish.yml -> Docker Hub (:$VERSION, :latest)"
if [ "$ASSUME_YES" -ne 1 ]; then
  printf 'Proceed? [y/N] '
  read -r reply
  case "$reply" in y|Y|yes|YES) ;; *) echo "aborted."; exit 1 ;; esac
fi

# --- Bump, tag, publish ------------------------------------------------------

# -i.bak is portable across BSD (macOS) and GNU sed; drop the backup after.
sed -i.bak "s/^\([[:space:]]*MARKETING_VERSION:[[:space:]]*\"\)[^\"]*\(\".*\)/\1$VERSION\2/" "$PROJECT_YML"
rm -f "$PROJECT_YML.bak"

# Keep the generated Xcode project in sync for local builds (it's gitignored, so
# this never affects the commit — purely a convenience). Best-effort.
if command -v xcodegen >/dev/null 2>&1; then
  ( cd macos && xcodegen generate >/dev/null ) || echo "warn: xcodegen generate failed; regenerate manually" >&2
fi

git add "$PROJECT_YML"
git commit -m "chore(release): $TAG" >/dev/null
git tag -a "$TAG" -m "$TITLE"

git push origin main
git push origin "$TAG"

# --verify-tag: refuse to publish if the pushed tag somehow doesn't exist.
# --generate-notes: auto-build the body from merged PRs/commits since the last
# tag; edit the release afterwards if you want curated notes.
gh release create "$TAG" --verify-tag --title "$TITLE" --generate-notes

echo
echo "Released $TAG. Docker build is running:"
gh run list --workflow=docker-publish.yml --limit 1 2>/dev/null || true
echo "Then update the container on the Unraid host to pull the new image."
