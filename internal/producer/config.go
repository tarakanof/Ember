package producer

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func Bool(v string, def bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}

func EnvFilePath(home string) string {
	return filepath.Join(home, ".config", "ember", "producer.env")
}

type Common struct {
	Source           string
	ServerURL        string
	ServerConfigured string
	ServerAuto       bool
	ServerInstance   string
	Token            string
	SourceColor      string

	ActivityTrailEnabled bool
	SourceCardEnabled    bool
	SessionBarEnabled    bool
}

func DefaultCommon() Common {
	return Common{ActivityTrailEnabled: true, SourceCardEnabled: true, SessionBarEnabled: true}
}

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

func (c *Common) Resolve(home string) {
	c.Source = ResolveSource(c.Source)
	c.ServerConfigured = c.ServerURL
	c.ServerURL, c.ServerAuto = ResolveServerURL(home, c.ServerURL, c.ServerInstance)
	if c.Token == "" {
		c.Token = os.Getenv("EMBER_TOKEN")
	}
}

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

type Gauges struct {
	ContextPctEnabled    bool
	ContextNumberEnabled bool
	RateBottomBarEnabled bool
	RateResetEnabled     bool
}

func DefaultGauges() Gauges {
	return Gauges{ContextPctEnabled: true}
}

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

func (g Gauges) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.Bool("context_pct_enabled", g.ContextPctEnabled),
		slog.Bool("context_number_enabled", g.ContextNumberEnabled),
		slog.Bool("rate_bottom_bar_enabled", g.RateBottomBarEnabled),
		slog.Bool("rate_reset_enabled", g.RateResetEnabled),
	}
}

func (g Gauges) Apply(req *StatusRequest) {
	req.ContextNumber = g.ContextNumberEnabled
	req.RateBottomBar = g.RateBottomBarEnabled
	req.RateReset = g.RateResetEnabled
}
