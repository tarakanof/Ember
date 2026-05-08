#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found; install Go 1.22+ first" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

echo "Building awtrix-claude-producer..."
cd "$REPO_ROOT"
go install ./cmd/awtrix-claude-producer/

GOBIN="$(go env GOBIN)"
if [ -z "$GOBIN" ]; then
  GOBIN="$(go env GOPATH)/bin"
fi
BIN="$GOBIN/awtrix-claude-producer"

if [ ! -x "$BIN" ]; then
  echo "Build succeeded but binary not found at $BIN" >&2
  exit 1
fi

echo "Running install subcommand..."
exec "$BIN" install
