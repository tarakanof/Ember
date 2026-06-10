#!/bin/sh
# Activate the repo's version-controlled git hooks (.githooks/) by pointing
# core.hooksPath at them. Run once per clone:
#
#   ./scripts/install-hooks.sh
#
# Applies repo-wide (all worktrees share the config). Undo with:
#   git config --unset core.hooksPath
set -e
cd "$(git rev-parse --show-toplevel)"
chmod +x .githooks/* 2>/dev/null || true
git config core.hooksPath .githooks
echo "Installed: core.hooksPath -> .githooks (pre-push runs gofmt/vet/go test -race)."
echo "Bypass a single push with: git push --no-verify"
