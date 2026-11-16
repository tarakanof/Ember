#!/bin/bash
# Usage: release-notes.sh <release-page-url> < PR titles, one per line
set -euo pipefail

[ "$#" -eq 1 ] || { sed -n '2p' "$0" >&2; exit 2; }
PAGE="$1"
[[ "$PAGE" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/tag/v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  { echo "error: '$PAGE' is not a GitHub release page URL" >&2; exit 1; }

titles="$(cat)"
if grep -q ']]>' <<<"$titles"; then
  echo "error: a title contains ']]>', which would end the appcast's CDATA section" >&2
  exit 1
fi

cat <<'HTML'
<!doctype html>
<html><head><meta charset="utf-8"><style>
:root { color-scheme: light dark; }
body { font: 13px -apple-system, BlinkMacSystemFont, sans-serif; margin: 12px 16px; line-height: 1.45; }
h2 { font-size: 13px; margin: 12px 0 4px; }
ul { margin: 0; padding-left: 18px; }
li { margin: 2px 0; }
p.more { margin-top: 14px; }
</style></head><body>
HTML

awk '
function esc(s) { gsub(/&/, "\\&amp;", s); gsub(/</, "\\&lt;", s); gsub(/>/, "\\&gt;", s); gsub(/"/, "\\&quot;", s); return s }
{
  if (!match($0, /^(feat|fix|perf)(\([^)]*\))?!?: /)) next
  head = substr($0, 1, RLENGTH); text = substr($0, RLENGTH + 1)
  if (head ~ /^[a-z]+\((release|scripts|ci|build|deps|test)\)/) next
  if (text == "" || seen[text]++) next
  line = "<li>" esc(toupper(substr(text, 1, 1)) substr(text, 2)) "</li>"
  if (head ~ /^feat/) new[++n] = line; else fixed[++f] = line
}
END {
  if (n) { print "<h2>New</h2>"; print "<ul>"; for (i = 1; i <= n; i++) print new[i]; print "</ul>" }
  if (f) { print "<h2>Fixed</h2>"; print "<ul>"; for (i = 1; i <= f; i++) print fixed[i]; print "</ul>" }
  if (!n && !f) print "<p>Maintenance and internal improvements.</p>"
}' <<<"$titles"

printf '<p class="more"><a href="%s">Full release notes</a></p>\n</body></html>\n' "$PAGE"
