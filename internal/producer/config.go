package producer

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Bool parses a producer.env toggle: true/1/yes/on and false/0/no/off (any
// case); an empty or unrecognised value keeps def.
func Bool(v string, def bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}

// EnvFilePath is ~/.config/ember/producer.env, shared by every producer.
func EnvFilePath(home string) string {
	return filepath.Join(home, ".config", "ember", "producer.env")
}

// Common is the producer.env configuration every producer reads: identity,
// server, token and the clock-card toggles.
type Common struct {
	Source           string
	ServerURL        string // effective URL: explicit, else the cached discovery
	ServerConfigured string // EMBER_SERVER_URL as written
	ServerAuto       bool   // EMBER_SERVER_URL empty or "auto": discover over mDNS
	ServerInstance   string // EMBER_SERVER_INSTANCE: which server when several answer
	Token            string
	SourceColor      string

	ActivityTrailEnabled bool
	SourceCardEnabled    bool
	SessionBarEnabled    bool
}

// DefaultCommon is Common before producer.env is applied.
func DefaultCommon() Common {
	return Common{ActivityTrailEnabled: true, SourceCardEnabled: true, SessionBarEnabled: true}
}

// Set applies one producer.env key and reports whether it was a Common key.
func (c *Common) Set(k, v string) bool {
	switch k {
	case "EMBER_SOURCE":
		c.Source = v
	case "EMBER_SERVER_URL":
		c.ServerURL = v
	case "EMBER_SERVER_INSTANCE":
		c.ServerInstance = v
	case "EMBER_TOKEN":
		c.Token = v
	case "EMBER_SOURCE_COLOR":
		c.SourceColor = v
	case "EMBER_ACTIVITY_TRAIL_ENABLED":
		c.ActivityTrailEnabled = Bool(v, true)
	case "EMBER_SOURCE_CARD":
		c.SourceCardEnabled = Bool(v, true)
	case "EMBER_SESSION_BAR":
		c.SessionBarEnabled = Bool(v, true)
	default:
		return false
	}
	return true
}

// Resolve fills the derived fields once producer.env is applied: the
// default source, the effective server URL, and EMBER_TOKEN from the process
// environment when producer.env has none.
func (c *Common) Resolve(home string) {
	c.Source = ResolveSource(c.Source)
	c.ServerConfigured = c.ServerURL
	c.ServerURL, c.ServerAuto = ResolveServerURL(home, c.ServerURL, c.ServerInstance)
	if c.Token == "" {
		c.Token = os.Getenv("EMBER_TOKEN")
	}
}

// LogAttrs are Common's log fields, with the token redacted to set/unset.
func (c Common) LogAttrs() []slog.Attr {
	tok := "unset"
	if c.Token != "" {
		tok = "set"
	}
	return []slog.Attr{
		slog.String("source", c.Source),
		slog.String("server_url", c.ServerURL),
		slog.Bool("server_auto", c.ServerAuto),
		slog.String("token", tok),
		slog.String("source_color", c.SourceColor),
		slog.Bool("activity_trail_enabled", c.ActivityTrailEnabled),
		slog.Bool("source_card_enabled", c.SourceCardEnabled),
		slog.Bool("session_bar_enabled", c.SessionBarEnabled),
	}
}

// StatusRequest starts a POST /v1/status body for a session with the
// source identity and card toggles filled in.
func (c Common) StatusRequest(tool, session, state string) StatusRequest {
	req := StatusRequest{Source: c.Source, Tool: tool, Session: session, State: state}
	if c.SourceColor != "" {
		sc := c.SourceColor
		req.SourceColor = &sc
	}
	sc, sb := c.SourceCardEnabled, c.SessionBarEnabled
	req.SourceCard, req.SessionBar = &sc, &sb
	return req
}

// Gauges are the context/rate display toggles of the producers that report
// context and rate-limit figures (Claude Code, Codex).
type Gauges struct {
	ContextPctEnabled    bool
	ContextNumberEnabled bool
	RateBottomBarEnabled bool
	RateResetEnabled     bool
}

// DefaultGauges is Gauges before producer.env is applied.
func DefaultGauges() Gauges {
	return Gauges{ContextPctEnabled: true}
}

// Set applies one producer.env key and reports whether it was a Gauges key.
func (g *Gauges) Set(k, v string) bool {
	switch k {
	case "EMBER_CONTEXT_PCT_ENABLED":
		g.ContextPctEnabled = Bool(v, true)
	case "EMBER_CONTEXT_NUMBER_ENABLED":
		g.ContextNumberEnabled = Bool(v, false)
	case "EMBER_RATE_BOTTOM_BAR":
		g.RateBottomBarEnabled = Bool(v, false)
	case "EMBER_RATE_RESET":
		g.RateResetEnabled = Bool(v, false)
	default:
		return false
	}
	return true
}

// LogAttrs are Gauges' log fields.
func (g Gauges) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.Bool("context_pct_enabled", g.ContextPctEnabled),
		slog.Bool("context_number_enabled", g.ContextNumberEnabled),
		slog.Bool("rate_bottom_bar_enabled", g.RateBottomBarEnabled),
		slog.Bool("rate_reset_enabled", g.RateResetEnabled),
	}
}

// Apply sets req's per-request display flags.
func (g Gauges) Apply(req *StatusRequest) {
	req.ContextNumber = g.ContextNumberEnabled
	req.RateBottomBar = g.RateBottomBarEnabled
	req.RateReset = g.RateResetEnabled
}
