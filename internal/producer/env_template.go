package producer

import "strings"

func EnvExample() string {
	return strings.Replace(`# ember producer configuration (shared by Claude + Codex producers)
# EMBER_SOURCE: machine label on the clock card (~4 glyphs); empty = short host id.
# EMBER_SERVER_URL: empty or "auto" = find the server over mDNS (_ember._tcp);
# set http://host:3627 when several servers answer or multicast can't reach it.
# Discovery trusts the LAN (the token goes over plain HTTP): on untrusted
# networks set an explicit URL.
EMBER_SOURCE=@SOURCE@
EMBER_SERVER_URL=
# Required: the server's bearer token.
EMBER_TOKEN=set-me-to-the-server-bearer-token

# Optional (defaults shown):
# EMBER_HEARTBEAT_TTL_HOURS=6
# EMBER_HOOK_TIMEOUT_MS=500
# EMBER_DONE_TTL_SECONDS=30
# EMBER_SERVER_INSTANCE=   # with several servers: instance or host name to pick
# EMBER_SOURCE_COLOR=#aa66ff
# EMBER_CONTEXT_PCT_ENABLED=true
# EMBER_CODEX_POLL_INTERVAL_MS=2000
# EMBER_CODEX_ACTIVITY_WINDOW_SECONDS=300
# EMBER_CODEX_SESSIONS_DIR=~/.codex/sessions
# EMBER_CODEX_SOURCES=cli,vscode
# EMBER_CODEX_INCLUDE_CLAUDE=false
`, "@SOURCE@", DefaultSource(), 1)
}
