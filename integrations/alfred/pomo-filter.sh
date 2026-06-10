#!/bin/sh
# Alfred Script Filter for Ember Pomodoro control.
#
# Emits Alfred JSON: a menu of actions whose subtitle shows the live timer.
# Typing after the keyword (`{query}`) filters the actions. The selected item's
# `arg` is the ember-pomo subcommand, which the connected Run Script executes.
#
# Workflow env vars (set in Alfred → workflow → [x] variables):
#   EMBER_POMO   absolute path to the ember-pomo CLI
#                (default: looks on PATH, then the repo's integrations/cli)
#   EMBER_URL    server base URL   (passed through to ember-pomo)
#   EMBER_TOKEN  bearer token      (passed through to ember-pomo)

set -eu
query=${1:-}

# Locate the CLI.
cli=${EMBER_POMO:-}
if [ -z "$cli" ]; then
	if command -v ember-pomo >/dev/null 2>&1; then
		cli=$(command -v ember-pomo)
	else
		cli="$(cd "$(dirname "$0")/../cli" 2>/dev/null && pwd)/ember-pomo"
	fi
fi

# Live status line for subtitles (never fails the filter if the server is down).
status=$("$cli" remaining 2>/dev/null || echo "offline")

# JSON-escape helper (handles the few chars that matter for our static strings).
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

# title | arg | subtitle | icon-hint
items='
Start focus|start focus|Begin a 25-min focus block
Pause / resume|toggle|Play–pause the current phase
Stop|stop|End the phase, back to idle
Skip|skip|Jump to the next phase
Short break|start short|Begin a short break
Long break|start long|Begin a long break
Statistics|stats|Print usage stats'

printf '{"items":['
first=1
echo "$items" | while IFS='|' read -r title arg subtitle; do
	[ -n "$title" ] || continue
	# filter by query (case-insensitive substring on the title)
	if [ -n "$query" ]; then
		lc_title=$(printf '%s' "$title" | tr '[:upper:]' '[:lower:]')
		lc_query=$(printf '%s' "$query" | tr '[:upper:]' '[:lower:]')
		case "$lc_title" in *"$lc_query"*) ;; *) continue ;; esac
	fi
	[ "$first" = 1 ] || printf ','
	first=0
	printf '{"title":"%s","subtitle":"%s — now: %s","arg":"%s","valid":true}' \
		"$(esc "$title")" "$(esc "$subtitle")" "$(esc "$status")" "$(esc "$arg")"
done
printf ']}'
