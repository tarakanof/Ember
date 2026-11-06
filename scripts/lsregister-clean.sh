#!/usr/bin/env bash
set -euo pipefail

# Unregisters every com.ember.* bundle LaunchServices knows about except the
# installed /Applications/Ember.app, then re-registers that one.
# Usage: lsregister-clean.sh [--dry-run]

LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
INSTALLED=/Applications/Ember.app
DRY=0
[[ "${1:-}" == "--dry-run" ]] && DRY=1

list=$(mktemp)
trap 'rm -f "$list"' EXIT
"$LSREGISTER" -dump 2>/dev/null |
  awk '/^path:/{p=$0} /^identifier: *com\.ember\.[A-Za-z0-9.-]+$/{print p}' |
  sed -E 's/^path: *//; s/ \(0x[0-9a-f]+\)$//' | sort -u |
  grep -vx "$INSTALLED" > "$list" || true

n=0
while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  n=$((n + 1))
  if [[ $DRY -eq 1 ]]; then
    echo "would unregister: $path"
  else
    "$LSREGISTER" -u "$path" 2>/dev/null || true
  fi
done < "$list"

if [[ $DRY -eq 1 ]]; then
  echo "$n stale registration(s)"
  exit 0
fi
echo "tried to unregister $n stale copy(ies)"
if [[ -d "$INSTALLED" ]]; then
  "$LSREGISTER" -f -R "$INSTALLED"
  echo "re-registered $INSTALLED"
else
  echo "no $INSTALLED to re-register"
fi
echo "if a browse still fails with NoAuth, restart mDNSResponder: sudo killall mDNSResponder"
