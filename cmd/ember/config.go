package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config-load sentinel errors. Wrap with fmt.Errorf("...: %w", ErrConfig*).
var (
	ErrConfigRead     = errors.New("config read")
	ErrConfigParse    = errors.New("config parse")
	ErrConfigValidate = errors.New("config validate")
)

// resolveConfigPath picks the config path the same way the running server does.
// Precedence: -config flag value → CONFIG_PATH env → ./config.json (if it exists) → defaults-only ("").
// The returned source string ∈ {"flag","env","cwd","defaults"} describes which arm matched.
func resolveConfigPath(flagValue string) (path, source string) {
	if flagValue != "" {
		return flagValue, "flag"
	}
	if env := os.Getenv("CONFIG_PATH"); env != "" {
		return env, "env"
	}
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json", "cwd"
	}
	return "", "defaults"
}

// parseConfigFile reads + decodes a config file. Returns:
//   - ErrConfigRead-wrapped error if the file can't be read.
//   - ErrConfigParse-wrapped error if the JSON is malformed (including unknown fields).
//   - the parsed Config (without applyDefaults; the caller chooses when to apply).
func parseConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %s: %v", ErrConfigRead, path, err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %s: %v", ErrConfigParse, path, err)
	}
	return cfg, nil
}

// warnDeprecatedConfig logs one Warn per config.json key that still parses but
// no longer does anything, so the operator can drop it.
func warnDeprecatedConfig(cfg Config, logger *slog.Logger) {
	d := cfg.Display
	if d.PulseStyle != "" {
		logger.Warn("display.pulse_style is deprecated and ignored — AWTRIX firmware animates attention via blinkText", "value", d.PulseStyle)
	}
	for _, k := range []struct {
		key string
		set bool
	}{
		{"display.heartbeat_seconds", d.HeartbeatSeconds != nil},
		{"display.refresh_seconds", d.RefreshSeconds != nil},
		{"display.notify_on_waiting", d.NotifyOnWaiting != nil},
	} {
		if k.set {
			logger.Warn("config key is ignored and can be removed", "key", k.key)
		}
	}
}

// sanitizeConfigBaseline repairs config.json values that fail the SSRF-guard
// validators (validDeviceURL, weatherIconIDPattern) but that we don't want to
// treat as fatal load errors — a hand-edited config.json shouldn't crash the
// server at startup. Invalid entries are logged to logger and replaced/dropped
// in place; validateConfig runs afterward as a defense-in-depth check that
// should now always pass for these two fields.
func sanitizeConfigBaseline(cfg *Config, logger *slog.Logger) {
	if err := validDeviceURL(cfg.AWTRIX.HTTPBaseURL); err != nil {
		logger.Warn("config.json awtrix.http_base_url invalid, falling back to default",
			"value", cfg.AWTRIX.HTTPBaseURL, "err", err, "default", defaultDeviceBaseURL)
		cfg.AWTRIX.HTTPBaseURL = defaultDeviceBaseURL
	}
	for k, v := range cfg.Weather.IconIDs {
		if !weatherIconIDPattern.MatchString(v) {
			logger.Warn("config.json weather.icon_ids entry invalid, dropping", "key", k, "value", v)
			delete(cfg.Weather.IconIDs, k)
		}
	}
}

// validateConfig enforces required fields and well-formedness. Returns
// ErrConfigValidate-wrapped on failure. Run AFTER applyDefaults so empty
// optional fields don't trigger.
func validateConfig(cfg Config) error {
	if cfg.AWTRIX.HTTPBaseURL == "" {
		return fmt.Errorf("%w: awtrix.http_base_url is required", ErrConfigValidate)
	}
	if err := validDeviceURL(cfg.AWTRIX.HTTPBaseURL); err != nil {
		return fmt.Errorf("%w: awtrix.http_base_url: %v", ErrConfigValidate, err)
	}
	if cfg.AWTRIX.TimeoutSeconds <= 0 {
		return fmt.Errorf("%w: awtrix.timeout_seconds must be > 0", ErrConfigValidate)
	}
	if cfg.RateLimit.Burst < 0 {
		return fmt.Errorf("%w: rate_limit.burst must be >= 0, got %d", ErrConfigValidate, cfg.RateLimit.Burst)
	}
	if cfg.RateLimit.RefillPerSec < 0 {
		return fmt.Errorf("%w: rate_limit.refill_per_sec must be >= 0, got %v", ErrConfigValidate, cfg.RateLimit.RefillPerSec)
	}
	if cfg.RateLimit.IdleEvictSeconds < 0 {
		return fmt.Errorf("%w: rate_limit.idle_evict_seconds must be >= 0, got %d", ErrConfigValidate, cfg.RateLimit.IdleEvictSeconds)
	}
	if cfg.Display.FrameLifetimeSeconds < 10 || cfg.Display.FrameLifetimeSeconds > 120 {
		return fmt.Errorf("%w: display.frame_lifetime_seconds %d out of range [10, 120]", ErrConfigValidate, cfg.Display.FrameLifetimeSeconds)
	}
	if cfg.Display.IdleRestoreSeconds < 60 || cfg.Display.IdleRestoreSeconds > 3600 {
		return fmt.Errorf("%w: display.idle_restore_seconds %d out of range [60, 3600]", ErrConfigValidate, cfg.Display.IdleRestoreSeconds)
	}
	if err := validatePomodoro(cfg.Pomodoro); err != nil {
		return err
	}
	if err := validateQuietHours(cfg.QuietHours); err != nil {
		return err
	}
	return nil
}

// validatePomodoro enforces sane Pomodoro durations and well-formed colours.
func validatePomodoro(p PomodoroConfig) error {
	if p.FocusMinutes < 1 || p.FocusMinutes > 480 {
		return fmt.Errorf("%w: pomodoro.focus_minutes %d out of range [1, 480]", ErrConfigValidate, p.FocusMinutes)
	}
	if p.ShortBreakMinutes < 1 || p.ShortBreakMinutes > 60 {
		return fmt.Errorf("%w: pomodoro.short_break_minutes %d out of range [1, 60]", ErrConfigValidate, p.ShortBreakMinutes)
	}
	if p.LongBreakMinutes < 1 || p.LongBreakMinutes > 180 {
		return fmt.Errorf("%w: pomodoro.long_break_minutes %d out of range [1, 180]", ErrConfigValidate, p.LongBreakMinutes)
	}
	if p.RoundsBeforeLongBreak < 1 || p.RoundsBeforeLongBreak > 12 {
		return fmt.Errorf("%w: pomodoro.rounds_before_long_break %d out of range [1, 12]", ErrConfigValidate, p.RoundsBeforeLongBreak)
	}
	if !isHexColor(p.FocusColor) {
		return fmt.Errorf("%w: pomodoro.focus_color %q must be #RRGGBB", ErrConfigValidate, p.FocusColor)
	}
	if !isHexColor(p.BreakColor) {
		return fmt.Errorf("%w: pomodoro.break_color %q must be #RRGGBB", ErrConfigValidate, p.BreakColor)
	}
	if p.Enabled && p.DBPath == "" {
		return fmt.Errorf("%w: pomodoro.db_path is required when pomodoro.enabled", ErrConfigValidate)
	}
	if p.MaxSessionMinutes < 0 || p.MaxSessionMinutes > 1440 {
		return fmt.Errorf("%w: pomodoro.max_session_minutes %d out of range [0, 1440]", ErrConfigValidate, p.MaxSessionMinutes)
	}
	if p.WorkHoursGapMinutes < 0 || p.WorkHoursGapMinutes > 180 {
		return fmt.Errorf("%w: pomodoro.work_hours_gap_minutes %d out of range [0, 180]", ErrConfigValidate, p.WorkHoursGapMinutes)
	}
	if p.DayStartHour < 0 || p.DayStartHour > 23 {
		return fmt.Errorf("%w: pomodoro.day_start_hour %d out of range [0, 23]", ErrConfigValidate, p.DayStartHour)
	}
	if p.StreakGraceDays < 0 || p.StreakGraceDays > 7 {
		return fmt.Errorf("%w: pomodoro.streak_grace_days %d out of range [0, 7]", ErrConfigValidate, p.StreakGraceDays)
	}
	if p.DailyGoalSessions < 0 || p.DailyGoalSessions > 50 {
		return fmt.Errorf("%w: pomodoro.daily_goal_sessions %d out of range [0, 50]", ErrConfigValidate, p.DailyGoalSessions)
	}
	if p.WeeklyGoalDays < 0 || p.WeeklyGoalDays > 7 {
		return fmt.Errorf("%w: pomodoro.weekly_goal_days %d out of range [0, 7]", ErrConfigValidate, p.WeeklyGoalDays)
	}
	return nil
}

type AuthConfig struct {
	StatusToken    string `json:"status_token"`
	StatusTokenEnv string `json:"status_token_env"`
}

func (a AuthConfig) LogValue() slog.Value {
	tokenStatus := "unset"
	if a.StatusToken != "" {
		tokenStatus = "set"
	}
	return slog.GroupValue(
		slog.String("status_token_env", a.StatusTokenEnv),
		slog.String("status_token", tokenStatus),
	)
}

type Config struct {
	HTTP      HTTPConfig      `json:"http"`
	AWTRIX    AWTRIXConfig    `json:"awtrix"`
	Auth      AuthConfig      `json:"auth"`
	Display   DisplayConfig   `json:"display"`
	RateLimit RateLimitConfig `json:"rate_limit"`
	Pomodoro  PomodoroConfig  `json:"pomodoro"`
	Weather   WeatherConfig   `json:"weather"`
	Meetings  MeetingsConfig  `json:"meetings"`
	// Usage-widget toggles. Pointers so the file can distinguish "unset"
	// (nil → default on) from an explicit false; resolved via the helpers below.
	UsageWidget   *bool `json:"usage_widget,omitempty"`
	UsagePerModel *bool `json:"usage_per_model,omitempty"`
	LimitAlarm    *bool `json:"limit_alarm,omitempty"`
	// UsageThresholdPct gates the in-app usage card (and the idle usage
	// frame): the card shows only when a tool's 5h window is >= this percent.
	// nil → default 60; 0 = always show.
	UsageThresholdPct *int `json:"usage_threshold_pct,omitempty"`
	// QuietHours mutes all device sounds during the window (server-local time).
	QuietHours QuietHoursConfig `json:"quiet_hours"`
}

// usageWidgetEnabled reports whether the in-app usage card and the idle usage
// frame are enabled. Default on (nil pointer).
func (c Config) usageWidgetEnabled() bool { return c.UsageWidget == nil || *c.UsageWidget }

// usagePerModelEnabled reports whether the Claude per-model (Opus/Sonnet) usage
// frames should be pushed. Default on (nil pointer).
func (c Config) usagePerModelEnabled() bool { return c.UsagePerModel == nil || *c.UsagePerModel }

// limitAlarmEnabled reports whether the 5h-limit reset popup+chime is armed.
// Default on (nil pointer).
func (c Config) limitAlarmEnabled() bool { return c.LimitAlarm == nil || *c.LimitAlarm }

// usageThresholdPct returns the 5h-percent gate for the usage card, clamped
// to 0..100. Default 60 (nil pointer); 0 means "always show".
func (c Config) usageThresholdPct() int {
	if c.UsageThresholdPct == nil {
		return 60
	}
	v := *c.UsageThresholdPct
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// PomodoroConfig holds the Pomodoro feature's static defaults. Runtime-editable
// settings (durations, colours, toggles) are persisted in the stats store and
// edited from the menu app; these values seed the engine at startup and provide
// the fallbacks the store is initialised from.
type PomodoroConfig struct {
	Enabled               bool   `json:"enabled"`
	FocusMinutes          int    `json:"focus_minutes"`
	ShortBreakMinutes     int    `json:"short_break_minutes"`
	LongBreakMinutes      int    `json:"long_break_minutes"`
	RoundsBeforeLongBreak int    `json:"rounds_before_long_break"`
	AutoStartNext         bool   `json:"auto_start_next"`
	Sound                 bool   `json:"sound"`
	SoundMelody           string `json:"sound_melody,omitempty"`
	FocusColor            string `json:"focus_color"`
	BreakColor            string `json:"break_color"`
	DBPath                string `json:"db_path"`
	// ButtonCallback enables mapping device button presses (delivered to
	// /hooks/awtrix/button) to timer actions.
	ButtonCallback    bool `json:"button_callback"`
	MaxSessionMinutes int  `json:"max_session_minutes"` // 0 = no cap; whole cycle auto-stops after this many minutes

	// Stats/dashboard knobs (read at request time by the stats handlers; not part
	// of the runtime DTO). Zero values fall back to sensible defaults at use.
	WorkHoursGapMinutes int `json:"work_hours_gap_minutes"` // gap (min) that splits one work session from the next (default 15)
	DayStartHour        int `json:"day_start_hour"`         // logical day boundary 0-23; pre-this-hour activity counts to the previous day (default 4)
	StreakGraceDays     int `json:"streak_grace_days"`      // missed days tolerated within the current streak (default 1; 0 = strict)
	DailyGoalSessions   int `json:"daily_goal_sessions"`    // completed-focus target per day (default 8; 0 = disabled)
	WeeklyGoalDays      int `json:"weekly_goal_days"`       // active-day target per week (default 5; 0 = disabled)
	// WorkHoursIncludeActivity overlays AI-coding-session activity (from
	// /v1/status) onto the work-hours view and enables persisting that activity
	// timeline. When false, work-hours uses Pomodoro focus blocks only.
	WorkHoursIncludeActivity bool `json:"work_hours_include_activity"`
}

// Effective stats knobs, coercing zero/missing values (e.g. from an older config
// file) to defaults. DayStartHour and the goals legitimately allow 0, so only
// the gap is coerced.
func (p PomodoroConfig) workHoursGap() time.Duration {
	g := p.WorkHoursGapMinutes
	if g <= 0 {
		g = 15
	}
	return time.Duration(g) * time.Minute
}

type RateLimitConfig struct {
	Disabled         bool    `json:"disabled"`
	Burst            int     `json:"burst"`
	RefillPerSec     float64 `json:"refill_per_sec"`
	IdleEvictSeconds int     `json:"idle_evict_seconds"`
}

type HTTPConfig struct {
	Addr string `json:"addr"`
}

type AWTRIXConfig struct {
	HTTPBaseURL    string `json:"http_base_url"`
	AppName        string `json:"app_name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	// AutoRediscover gates the periodic StartDeviceWatch probe loop (see
	// device.go). nil/absent defaults to enabled, matching the *bool toggle
	// pattern used elsewhere (usage_widget, meetings.enabled, weather.*).
	AutoRediscover *bool `json:"auto_rediscover,omitempty"`
	// BootPing installs the ember-boot-ping Berry script on the clock (see
	// boot_ping.go): on reboot it POSTs /hooks/awtrix/boot so Ember re-pushes
	// its tiles in seconds instead of waiting for StartDeviceWatch to notice.
	// Off by default — it puts a script on a device the operator owns.
	BootPing bool `json:"boot_ping"`
}

// AutoRediscoverEnabled reports whether the periodic clock re-discovery probe
// (StartDeviceWatch) should run. nil (field absent from config.json/store) ⇒
// enabled, so old config blobs without the field keep self-healing on.
func (c AWTRIXConfig) AutoRediscoverEnabled() bool {
	return c.AutoRediscover == nil || *c.AutoRediscover
}

type DisplayConfig struct {
	IdleText             string `json:"idle_text"`
	StaleSeconds         int    `json:"stale_seconds"`
	DoneTTLSeconds       int    `json:"done_ttl_seconds"`
	RotationDwellSeconds int    `json:"rotation_dwell_seconds"`
	AckTimeoutSeconds    int    `json:"ack_timeout_seconds"`
	// G.2:
	FrameLifetimeSeconds int  `json:"frame_lifetime_seconds"`
	IdleRestoreSeconds   int  `json:"idle_restore_seconds"`
	AttentionChime       bool `json:"attention_chime"`
	// Indicators turns on the three corner-LED ambient status lights (see
	// coordinator_indicators.go). Opt-in: they are shared real estate on the
	// panel, so a plain Ember install leaves them alone.
	Indicators bool `json:"indicators"`
	// PulseStyle is parsed but ignored. Kept so configs from G.1b that
	// still carry "pulse_style": "breathe" continue to parse under
	// DisallowUnknownFields. AWTRIX firmware has no multi-frame draw
	// mode; attention is animated via blinkText instead.
	PulseStyle string `json:"pulse_style,omitempty"`
	// HeartbeatSeconds, RefreshSeconds and NotifyOnWaiting were parsed and
	// defaulted but never read by anything. They stay decodable so existing
	// config files still load under DisallowUnknownFields; nil means absent,
	// and warnDeprecatedConfig flags any that are set.
	HeartbeatSeconds *int  `json:"heartbeat_seconds,omitempty"`
	RefreshSeconds   *int  `json:"refresh_seconds,omitempty"`
	NotifyOnWaiting  *bool `json:"notify_on_waiting,omitempty"`
}

func defaultConfig() Config {
	return Config{
		HTTP: HTTPConfig{
			Addr: ":3627",
		},
		AWTRIX: AWTRIXConfig{
			HTTPBaseURL:    "http://192.168.0.14",
			AppName:        "ember",
			TimeoutSeconds: 10,
		},
		Auth: AuthConfig{
			StatusTokenEnv: "EMBER_TOKEN",
		},
		Display: DisplayConfig{
			IdleText:             "AI idle",
			StaleSeconds:         300,
			DoneTTLSeconds:       30,
			RotationDwellSeconds: 3,
			AckTimeoutSeconds:    30,
			FrameLifetimeSeconds: 30,
			IdleRestoreSeconds:   120,
		},
		RateLimit: RateLimitConfig{
			Disabled:         false,
			Burst:            60,
			RefillPerSec:     5.0,
			IdleEvictSeconds: 300,
		},
		Pomodoro: PomodoroConfig{
			Enabled:                  false,
			FocusMinutes:             25,
			ShortBreakMinutes:        5,
			LongBreakMinutes:         15,
			RoundsBeforeLongBreak:    4,
			AutoStartNext:            true,
			Sound:                    true,
			FocusColor:               "#FF0000",
			BreakColor:               "#00FF00",
			DBPath:                   "/var/lib/ember/pomodoro.db",
			ButtonCallback:           true,
			MaxSessionMinutes:        480,
			WorkHoursGapMinutes:      15,
			DayStartHour:             4,
			StreakGraceDays:          1,
			DailyGoalSessions:        8,
			WeeklyGoalDays:           5,
			WorkHoursIncludeActivity: true,
		},
	}
}

// loadConfig resolves and loads the server's config, logging any
// SSRF/icon-id baseline repairs (see sanitizeConfigBaseline) through logger
// rather than the unconfigured slog default handler.
func loadConfig(path string, logger *slog.Logger) (Config, error) {
	resolved, _ := resolveConfigPath(path)
	if resolved == "" {
		cfg := defaultConfig()
		cfg.applyDefaults()
		return cfg, nil
	}
	cfg, err := parseConfigFile(resolved)
	if err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	sanitizeConfigBaseline(&cfg, logger)
	warnDeprecatedConfig(cfg, logger)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":3627"
	}
	if c.AWTRIX.HTTPBaseURL == "" {
		c.AWTRIX.HTTPBaseURL = defaultDeviceBaseURL
	}
	if c.AWTRIX.AppName == "" {
		c.AWTRIX.AppName = "ember"
	}
	if c.AWTRIX.TimeoutSeconds <= 0 {
		c.AWTRIX.TimeoutSeconds = 10
	}
	if c.Display.IdleText == "" {
		c.Display.IdleText = "AI idle"
	}
	if c.Display.StaleSeconds <= 0 {
		// 300s tolerates a lapse in the producer heartbeat (and bridges quiet
		// stretches within a session) so an active session isn't reaped to the
		// idle robot mid-work. Matches the codex producer's activity window.
		c.Display.StaleSeconds = 300
	}
	if c.Display.DoneTTLSeconds <= 0 {
		c.Display.DoneTTLSeconds = 30
	}
	if c.Display.RotationDwellSeconds <= 0 {
		c.Display.RotationDwellSeconds = 3
	}
	if c.Display.AckTimeoutSeconds <= 0 {
		c.Display.AckTimeoutSeconds = 30
	}
	if c.Display.FrameLifetimeSeconds <= 0 {
		c.Display.FrameLifetimeSeconds = 30
	}
	if c.Display.IdleRestoreSeconds <= 0 {
		c.Display.IdleRestoreSeconds = 120
	}
	if c.Auth.StatusTokenEnv == "" {
		c.Auth.StatusTokenEnv = "EMBER_TOKEN"
	}
	if c.Auth.StatusToken == "" {
		c.Auth.StatusToken = os.Getenv(c.Auth.StatusTokenEnv)
	}
	if c.RateLimit.Burst == 0 {
		c.RateLimit.Burst = 60
	}
	if c.RateLimit.RefillPerSec == 0 {
		c.RateLimit.RefillPerSec = 5.0
	}
	if c.RateLimit.IdleEvictSeconds == 0 {
		c.RateLimit.IdleEvictSeconds = 300
	}
	// Disabled is a bool — zero value is false, the right default.

	if c.Pomodoro.FocusMinutes <= 0 {
		c.Pomodoro.FocusMinutes = 25
	}
	if c.Pomodoro.ShortBreakMinutes <= 0 {
		c.Pomodoro.ShortBreakMinutes = 5
	}
	if c.Pomodoro.LongBreakMinutes <= 0 {
		c.Pomodoro.LongBreakMinutes = 15
	}
	if c.Pomodoro.RoundsBeforeLongBreak <= 0 {
		c.Pomodoro.RoundsBeforeLongBreak = 4
	}
	if c.Pomodoro.FocusColor == "" {
		c.Pomodoro.FocusColor = "#FF0000"
	}
	if c.Pomodoro.BreakColor == "" {
		c.Pomodoro.BreakColor = "#00FF00"
	}
	if c.Pomodoro.DBPath == "" {
		c.Pomodoro.DBPath = "/var/lib/ember/pomodoro.db"
	}
	// Sound and ButtonCallback are bools: their no-config defaults come from
	// defaultConfig(); a config file controls them explicitly.

	c.Weather.applyDefaults()
	c.Meetings.applyDefaults()
}
