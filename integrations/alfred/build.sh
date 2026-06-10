#!/bin/sh
# Package the Alfred workflow into an importable .alfredworkflow bundle.
#
# Usage: ./build.sh   →  produces ember-pomodoro.alfredworkflow in this dir.
# Double-click the result to import it into Alfred (requires the Powerpack).
set -eu
cd "$(dirname "$0")"

out=ember-pomodoro.alfredworkflow
rm -f "$out"

# An .alfredworkflow is just a zip of info.plist + the bundled scripts/icon.
files="info.plist pomo-filter.sh"
[ -f icon.png ] && files="$files icon.png"

# shellcheck disable=SC2086
zip -q "$out" $files
echo "Built $out"
echo "Next: double-click it, then set EMBER_POMO / EMBER_URL / EMBER_TOKEN in the"
echo "workflow's [𝓍] variables pane. See README.md."
