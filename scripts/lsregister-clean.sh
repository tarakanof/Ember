#!/usr/bin/env bash
set -euo pipefail

# Unregisters every com.ember.* bundle LaunchServices knows about except the
# installed /Applications/Ember.app, then re-registers that one.
#
# Why: macOS resolves com.ember.Ember through LaunchServices when it checks
# Local Network access (nehelper caches the allowed executable UUIDs from that
# bundle) and NSBonjourServices (mDNSResponder). Every scratch or DerivedData
# build registers another copy; one of them can shadow the installed app, and
# its browse then fails with NoAuth (-65555) or its connections with "Local
# network prohibited" even though the toggle in System Settings is on.
#
# Usage: lsregister-clean.sh [--dry-run]

LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
INSTALLED=/Applications/Ember.app
DRY=0
[[ "${1:-}" == "--dry-run" ]] && DRY=1

list=$(mktemp)
trap 'rm -f "$list"' EXIT
"$LSREGISTER" -dump 2>/dev/null |
  awk '/^path:/{p=$0} /^identifier:.*com\.ember\./{print p}' |
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
[[ -d "$INSTALLED" ]] && "$LSREGISTER" -f -R "$INSTALLED"
echo "unregistered $n stale copy(ies); re-registered $INSTALLED"
echo "if a browse still fails with NoAuth, restart mDNSResponder: sudo killall mDNSResponder"
