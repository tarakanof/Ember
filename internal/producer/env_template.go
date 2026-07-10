package producer

// EnvExample returns the canonical body seeded into ~/.config/ember/producer.env
// on first install. Both the Claude and Codex producers share this file, so this
// is the single source of truth for its contents — carries the required keys
// (EMBER_SOURCE, EMBER_SERVER_URL, EMBER_TOKEN) plus every documented optional
// key from either producer.
func EnvExample() string {
	return `# ember producer configuration (shared by Claude + Codex producers)
# Required:
EMBER_SOURCE=set-me-to-this-laptop-id
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
`
}
