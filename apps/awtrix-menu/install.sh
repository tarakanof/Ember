#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found; install Go 1.22+ first" >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"
go install ./cmd/awtrix-menu/

GOBIN="$(go env GOBIN)"
[ -z "$GOBIN" ] && GOBIN="$(go env GOPATH)/bin"
BIN="$GOBIN/awtrix-menu"
[ -x "$BIN" ] || { echo "build succeeded but binary not found at $BIN" >&2; exit 1; }

# Clear Gatekeeper quarantine on the locally-built binary.
# Safe ONLY because this script just built the binary from the local checkout.
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true

exec "$BIN" install
