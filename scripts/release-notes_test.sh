#!/usr/bin/env bash
set -euo pipefail

GEN="$(cd "$(dirname "$0")" && pwd)/release-notes.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }
PAGE=https://github.com/tarakanof/ember/releases/tag/v1.2.3

head_html() {
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
}

cat >"$WORK/titles" <<'EOF2'
feat(app): install updates with Sparkle 2
fix(discovery): end dnssd lookups with Canceled so readers exit
docs(agents): a pr-review profile for Ember
fix(scripts): let grep read all input in pipefail checks
feat(release): sign app updates with Ember's Sparkle key
perf: draw <fewer> frames & "less" often
chore(deps): bump x
fix(server)!: log the no-clock warning once
feat(app): install updates with Sparkle 2
refactor: tidy
EOF2
{
  head_html
  cat <<'HTML'
<h2>New</h2>
<ul>
<li>Install updates with Sparkle 2</li>
</ul>
<h2>Fixed</h2>
<ul>
<li>End dnssd lookups with Canceled so readers exit</li>
<li>Draw &lt;fewer&gt; frames &amp; &quot;less&quot; often</li>
<li>Log the no-clock warning once</li>
</ul>
<p class="more"><a href="https://github.com/tarakanof/ember/releases/tag/v1.2.3">Full release notes</a></p>
</body></html>
HTML
} >"$WORK/want.html"
"$GEN" "$PAGE" <"$WORK/titles" >"$WORK/got.html" || fail "generation failed"
diff -u "$WORK/want.html" "$WORK/got.html" || fail "notes differ from the expected HTML"
echo "ok: feat/fix/perf titles become New and Fixed lists; other types, internal scopes and duplicates are left out; text is escaped"

{
  head_html
  cat <<'HTML'
<p>Maintenance and internal improvements.</p>
<p class="more"><a href="https://github.com/tarakanof/ember/releases/tag/v1.2.3">Full release notes</a></p>
</body></html>
HTML
} >"$WORK/want-empty.html"
printf 'docs: x\nchore(release): v1.2.3\n' | "$GEN" "$PAGE" >"$WORK/got-empty.html" || fail "empty generation failed"
diff -u "$WORK/want-empty.html" "$WORK/got-empty.html" || fail "notes without user-facing changes differ"
echo "ok: a release with no user-facing change says so"

if "$GEN" 'https://example.com/x' </dev/null >/dev/null 2>&1; then fail "accepted a non-GitHub release page"; fi
if "$GEN" </dev/null >/dev/null 2>&1; then fail "accepted no arguments"; fi
if printf 'feat: a ]]> b\n' | "$GEN" "$PAGE" >/dev/null 2>&1; then fail "accepted a title that would close the CDATA section"; fi
echo "ok: a bad release page URL, no arguments or a CDATA terminator is refused"
