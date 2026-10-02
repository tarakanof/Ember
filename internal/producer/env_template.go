package producer

import "strings"

// EnvExample returns the canonical body seeded into ~/.config/ember/producer.env on first install.
func EnvExample() string {
	return strings.Replace(`# ember producer configuration (shared by Claude + Codex producers)
# Required (EMBER_SOURCE defaults to this Mac's short host name when empty):
EMBER_SOURCE=@SOURCE@
EMBER_SERVER_URL=http://192.168.0.36:3627
EMBER_TOKEN=set-me-to-the-server-bearer-token

# Optional (defaults shown):
# EMBER_HEARTBEAT_TTL_HOURS=6
# EMBER_HOOK_TIMEOUT_MS=500
# EMBER_SOURCE_COLOR=#aa66ff
# EMBER_CONTEXT_PCT_ENABLED=true
# EMBER_CODEX_POLL_INTERVAL_MS=2000
# EMBER_CODEX_ACTIVITY_WINDOW_SECONDS=300
# EMBER_CODEX_SESSIONS_DIR=~/.codex/sessions
`, "@SOURCE@", DefaultSource(), 1)
}
