package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	// Embedded tz database: TZID resolution must work in the distroless container
	// image that ships no zoneinfo files; the meetings ICS parser uses LoadLocation.
	_ "time/tzdata"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/discovery"
	"github.com/tarakanof/ember/internal/pomodoro"
)

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
	HeartbeatSeconds     int    `json:"heartbeat_seconds"`
	RefreshSeconds       int    `json:"refresh_seconds"`
	NotifyOnWaiting      bool   `json:"notify_on_waiting"`
	RotationDwellSeconds int    `json:"rotation_dwell_seconds"`
	AckTimeoutSeconds    int    `json:"ack_timeout_seconds"`
	// G.2:
	FrameLifetimeSeconds int  `json:"frame_lifetime_seconds"`
	IdleRestoreSeconds   int  `json:"idle_restore_seconds"`
	AttentionChime       bool `json:"attention_chime"`
	// PulseStyle is parsed but ignored. Kept so configs from G.1b that
	// still carry "pulse_style": "breathe" continue to parse under
	// DisallowUnknownFields. AWTRIX firmware has no multi-frame draw
	// mode; attention is animated via blinkText instead.
	PulseStyle string `json:"pulse_style,omitempty"`
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
			HeartbeatSeconds:     10,
			RefreshSeconds:       5,
			NotifyOnWaiting:      false,
			RotationDwellSeconds: 3,
			AckTimeoutSeconds:    30,
			FrameLifetimeSeconds: 30,
			IdleRestoreSeconds:   120,
		},
		RateLimit: RateLimitConfig{
			Disabled:         false,
			Burst:            10,
			RefillPerSec:     2.0,
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
	if c.Display.HeartbeatSeconds <= 0 {
		c.Display.HeartbeatSeconds = 10
	}
	if c.Display.RefreshSeconds <= 0 {
		c.Display.RefreshSeconds = 5
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
		c.RateLimit.Burst = 10
	}
	if c.RateLimit.RefillPerSec == 0 {
		c.RateLimit.RefillPerSec = 2.0
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

type StatusRequest struct {
	Source         string  `json:"source"`
	Tool           string  `json:"tool"`
	Session        string  `json:"session"`
	State          string  `json:"state"`
	Message        string  `json:"message"`
	TokensToday    int64   `json:"tokens_today"`
	ContextPct     *int    `json:"context_pct,omitempty"`
	SourceColor    *string `json:"source_color,omitempty"`
	RateWindowPct  *int    `json:"rate_window_pct,omitempty"`
	Activity       string  `json:"activity,omitempty"`
	ContextNumber  bool    `json:"context_number,omitempty"`
	RateBottomBar  bool    `json:"rate_bottom_bar,omitempty"`
	RateResetAt    int64   `json:"rate_reset_at,omitempty"`
	RateReset      bool    `json:"rate_reset,omitempty"`
	RateResetLabel string  `json:"rate_reset_label,omitempty"`
	SourceCard     *bool   `json:"source_card,omitempty"`
	SessionBar     *bool   `json:"session_bar,omitempty"`
}

func (r StatusRequest) normalized() Session {
	source := strings.TrimSpace(r.Source)
	if source == "" {
		source = "unknown"
	}
	tool := strings.ToLower(strings.TrimSpace(r.Tool))
	if tool == "" {
		tool = "ai"
	}
	session := strings.TrimSpace(r.Session)
	if session == "" {
		session = "default"
	}

	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		state = "idle"
	}
	if !validState(state) {
		state = "idle"
	}

	return Session{
		Source:         source,
		Tool:           tool,
		Session:        session,
		State:          state,
		Message:        strings.TrimSpace(r.Message),
		TokensToday:    r.TokensToday,
		ContextPct:     r.ContextPct,
		SourceColor:    r.SourceColor,
		RateWindowPct:  r.RateWindowPct,
		Activity:       strings.TrimSpace(r.Activity),
		ContextNumber:  r.ContextNumber,
		RateBottomBar:  r.RateBottomBar,
		RateResetAt:    r.RateResetAt,
		RateReset:      r.RateReset,
		RateResetLabel: r.RateResetLabel,
		SourceCard:     r.SourceCard,
		SessionBar:     r.SessionBar,
		UpdatedAt:      time.Now(),
	}
}

func (r StatusRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		return errors.New("state is required")
	}
	if !validState(state) {
		return fmt.Errorf("invalid state %q (must be one of idle, running, waiting, done, error)", state)
	}
	if r.ContextPct != nil {
		if *r.ContextPct < 0 || *r.ContextPct > 100 {
			return fmt.Errorf("context_pct out of range %d (must be 0..100)", *r.ContextPct)
		}
	}
	if r.SourceColor != nil {
		if !isHexColor(*r.SourceColor) {
			return fmt.Errorf("source_color %q must match #RRGGBB hex", *r.SourceColor)
		}
	}
	if r.RateWindowPct != nil {
		if *r.RateWindowPct < 0 || *r.RateWindowPct > 100 {
			return fmt.Errorf("rate_window_pct out of range %d (must be 0..100)", *r.RateWindowPct)
		}
	}
	// Count runes, not bytes: producers truncate activity to 80 runes
	// (internal/producer.Truncate), so a multibyte activity (Cyrillic, emoji)
	// can exceed 80 bytes while still being ≤80 characters. A byte check here
	// 400s the whole status POST for such activity.
	if n := utf8.RuneCountInString(strings.TrimSpace(r.Activity)); n > 80 {
		return fmt.Errorf("activity too long (%d chars, max 80)", n)
	}
	if r.RateResetAt < 0 {
		return fmt.Errorf("rate_reset_at must be a non-negative unix timestamp, got %d", r.RateResetAt)
	}
	return nil
}

func validState(state string) bool {
	switch state {
	case "idle", "running", "waiting", "done", "error":
		return true
	default:
		return false
	}
}

// isHexColor reports whether s is a 7-char string of the form "#RRGGBB"
// with lowercase or uppercase hex digits.
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

type App struct {
	cfg          atomic.Pointer[Config] // hot-swappable; read with cfg.Load() per request
	cfgMu        sync.Mutex             // serializes cfg's read-copy-write; see updateConfig
	configPath   string                 // resolved at startup; "" when running on defaults
	configSource string                 // "flag" | "env" | "cwd" | "defaults"
	publisher    Publisher
	logger       *slog.Logger
	listener     net.Listener // bound HTTP listener; captured at startup for doctor introspection
	versionInfo  versionInfo  // computed once at startup; served by /version
	startedAt    time.Time    // set in NewApp; used by doctor uptime check
	limiter      *IPLimiter   // populated in NewApp; sweeper started by main()

	mu            sync.Mutex // protects sessions, lastPublished, lastPublish*
	sessions      map[string]Session
	lastPublished Render

	// Last-publish telemetry, all guarded by App.mu.
	lastPublishAt  time.Time
	lastPublishOK  bool
	lastPublishErr string

	metrics *metrics // populated by NewApp; never nil at runtime
	coord   *coordinator

	// engine + store are non-nil only when the Pomodoro feature is enabled
	// (wired via EnablePomodoro). The engine is safe for concurrent use; the
	// store is single-writer (driven from the coordinator/HTTP path).
	engine *pomodoro.Engine
	store  *pomodoro.Store

	// reminderHeldUntil is the unix-nano deadline during which a hold:true reminder
	// alarm is assumed to be on the clock. While armed, a device button press is
	// treated as acknowledging the alarm (the firmware dismisses on the middle
	// button) rather than a Pomodoro action — the middle press disarms it. 0 = none.
	reminderHeldUntil atomic.Int64

	// btnMu guards the left/right press-edge state used to synthesise a
	// simultaneous left+right "chord" (the firmware has no native chord). A
	// chord toggles Pomodoro start/stop; the individual left/right actions fire
	// on release so a chord can pre-empt them.
	btnMu        sync.Mutex
	btnLeftDown  time.Time // zero when up
	btnRightDown time.Time // zero when up
	btnChord     bool      // a chord fired and is still being released

	// activityLast throttles activity-heartbeat persistence to at most one row
	// per session per activityThrottle window (producers post every 2-10s, far
	// finer than the work-hours sessionization needs). Guarded by activityMu.
	activityMu   sync.Mutex
	activityLast map[string]time.Time

	appsMu     sync.Mutex      // guards hiddenApps
	hiddenApps map[string]bool // tool names hidden from the device display

	// usage holds the latest per-tool subscription-usage snapshots posted to
	// POST /v1/usage. In-memory only; refreshed on a <=5-min cadence.
	usage *UsageStore

	// weather holds the latest fetched observation + popup bookkeeping; the
	// poller (StartWeather) writes it and the coordinator reads it for the tile.
	// weatherFetcher performs the provider HTTP calls. Both non-nil from NewApp.
	weather        *weatherStore
	weatherFetcher *weatherFetcher

	// meetingsURLs holds the ICS calendar feed URLs parsed from
	// EMBER_MEETINGS_ICS_URLS at startup. These are credentials (possession =
	// calendar read access) and are never serialised to JSON, logged as strings,
	// or stored; only the count is exposed via the config GET endpoint.
	meetingsURLs []string

	// meetings holds upcoming occurrences + popup bookkeeping; the poller
	// (StartMeetings) writes it and the coordinator reads it for the tile.
	// meetingsFetcher performs the ICS HTTP calls. Both non-nil from NewApp.
	meetings        *meetingsStore
	meetingsFetcher *icsFetcher

	// iconFetch downloads a LaMetric gallery icon by ID for the weather icon
	// provisioner (ensureWeatherIcons); injectable in tests. iconMu serialises
	// provisioner runs.
	iconFetch func(ctx context.Context, id string) (data []byte, ext string, err error)
	iconMu    sync.Mutex

	// deviceBaseline is the clock URL from config.json captured at boot, before
	// any store override or auto-discovery. deviceSource() uses it to tell
	// "config" from "discovered". browseFn is the mDNS browse, overridable in tests.
	deviceBaseline   string
	deviceAutoPicked bool // set once at boot when discovery chose the clock URL
	browseFn         func(context.Context, time.Duration) ([]discovery.Candidate, error)

	// deviceRediscoverMu single-flights rediscoverClock so the boot check and
	// the periodic probe can't browse mDNS concurrently. lastRediscoverAt /
	// lastRediscoverResult record the most recent attempt for /admin/doctor.
	deviceRediscoverMu   sync.Mutex
	lastRediscoverAt     atomic.Int64 // unix secs, 0 = never
	lastRediscoverResult atomic.Value // string: "reachable" | "swapped" | "no-device"

	// lastButtonAt is the unix-seconds time of the most recent device button
	// POST to /hooks/awtrix/button (0 = never). Proves the clock's button_callback
	// reaches us; surfaced via GET /v1/device/buttons.
	lastButtonAt atomic.Int64
}

func NewApp(cfg Config, publisher Publisher, logger *slog.Logger) *App {
	a := &App{
		publisher:      publisher,
		logger:         logger,
		sessions:       make(map[string]Session),
		versionInfo:    computeVersionInfo(),
		startedAt:      time.Now(),
		usage:          newUsageStore(),
		weather:        newWeatherStore(),
		activityLast:   make(map[string]time.Time),
		deviceBaseline: cfg.AWTRIX.HTTPBaseURL,
		browseFn:       discovery.BrowseAWTRIX,
	}
	a.weatherFetcher = newWeatherFetcher()
	a.meetings = newMeetingsStore()
	a.meetingsFetcher = newICSFetcher()
	a.iconFetch = fetchLaMetricIcon
	a.cfg.Store(&cfg)
	a.metrics = newMetrics()
	a.limiter = NewIPLimiter(a)
	if hp, ok := publisher.(*HTTPPublisher); ok {
		hp.app = a
	}
	// All device traffic flows through the quiet-hours gate; the raw publisher
	// is never handed out past this point.
	quiet := &quietPublisher{next: publisher, cfg: a.cfg.Load, now: time.Now}
	a.publisher = quiet
	a.coord = newCoordinator(cfg, a.cfg.Load, quiet, realClock{}, logger, a.metrics)
	a.coord.snapshot = a.Snapshot
	a.coord.onPublishResult = a.recordPublish
	a.hiddenApps = map[string]bool{}
	a.coord.hiddenApps = a.hiddenAppsSet
	a.coord.usage = a.usage
	a.coord.weather = a.weather
	a.coord.meetings = a.meetings
	return a
}

// updateConfig serializes a config read-copy-write: it locks cfgMu, loads
// the current config, lets mutate apply changes to a copy, and stores the
// result. This closes the lost-update window that a bare
// `cur := *a.cfg.Load(); cur.X = ...; a.cfg.Store(&cur)` leaves open when two
// settings appliers race — the loser's stale copy would silently revert the
// winner's change. Readers stay lock-free via cfg.Load() (unchanged).
func (a *App) updateConfig(mutate func(*Config)) {
	a.cfgMu.Lock()
	cur := *a.cfg.Load()
	mutate(&cur)
	a.cfg.Store(&cur)
	a.cfgMu.Unlock()
}

// recordPublish updates the last-publish telemetry + lastPublished
// metadata exposed to the admin endpoints. Called by the coordinator
// after every publish attempt; guarded by App.mu.
//
// The snap argument carries the legacy Render struct (renderLocked's
// text/color/counter output) so admin tooling can show what was last
// pushed even though the actual pixels are now produced by
// RenderForCoord and not stored anywhere.
func (a *App) recordPublish(snap Snapshot, err error) {
	a.mu.Lock()
	a.lastPublishAt = time.Now().UTC()
	a.lastPublishOK = err == nil
	if err != nil {
		a.lastPublishErr = err.Error()
	} else {
		a.lastPublishErr = ""
		a.lastPublished = snap.Render
	}
	a.mu.Unlock()
}

// Upsert writes req into the session map and returns the resulting
// Render plus the state the session held BEFORE this upsert ("" if
// new). priorState is read and updated under a single App.mu acquisition
// so concurrent POSTs for the same session never misclassify the
// transition (a separate priorState+Upsert pair has a TOCTOU window
// that would let request B's Upsert land between request A's priorState
// read and its own Upsert).
func (a *App) Upsert(req StatusRequest) (Render, string) {
	session := req.normalized()
	key := session.Key()
	a.mu.Lock()
	prior := ""
	if existing, ok := a.sessions[key]; ok {
		prior = existing.State
	}
	a.sessions[key] = session
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render, prior
}

func (a *App) Clear() Render {
	a.mu.Lock()
	clear(a.sessions)
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Delete(key string) Render {
	a.mu.Lock()
	delete(a.sessions, key)
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Snapshot() Snapshot {
	now := time.Now()
	a.mu.Lock()
	render := a.renderLocked(now)
	sessions := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return Snapshot{
		Now:      now,
		Sessions: sessions,
		Render:   render,
	}
}

func (a *App) renderLocked(now time.Time) Render {
	cfg := a.cfg.Load()
	staleAfter := time.Duration(cfg.Display.StaleSeconds) * time.Second
	doneTTL := time.Duration(cfg.Display.DoneTTLSeconds) * time.Second
	for key, session := range a.sessions {
		age := now.Sub(session.UpdatedAt)
		var reaped bool
		switch session.State {
		case "done", "error":
			reaped = age > doneTTL
		default:
			reaped = age > staleAfter
		}
		if reaped {
			a.metrics.incSessionEvicted()
			a.logger.Warn("session reaped",
				"source", session.Source,
				"tool", session.Tool,
				"session", session.Session,
				"state", session.State,
				"age_seconds", int(age.Seconds()),
			)
			delete(a.sessions, key)
		}
	}

	var waiting, running, errored, done []Session
	for _, session := range a.sessions {
		switch session.State {
		case "waiting":
			waiting = append(waiting, session)
		case "running":
			running = append(running, session)
		case "error":
			errored = append(errored, session)
		case "done":
			done = append(done, session)
		}
		// idle sessions are intentionally not bucketed — they never win a render slot
	}

	sortSessions(waiting)
	sortSessions(running)
	sortSessions(errored)
	sortSessions(done)

	activeTotal := len(waiting) + len(running) + len(errored)

	// Pick winning group by priority.
	var winningGroup []Session
	var color string
	switch {
	case len(waiting) > 0:
		winningGroup = waiting
		color = "#FF3300"
	case len(errored) > 0:
		winningGroup = errored
		color = "#FF3300"
	case len(running) > 0:
		winningGroup = running
		color = "#00A3FF"
	case len(done) > 0:
		winningGroup = done
		color = "#707070"
	default:
		return Render{
			Text:        cfg.Display.IdleText,
			Color:       "#707070",
			ActiveTotal: activeTotal,
		}
	}

	text := perSessionLabel(winningGroup[0])
	if len(winningGroup) >= 2 {
		text = aggregateLabel(len(waiting), len(running), len(errored), len(done))
	}

	return Render{
		Text:        compactText(text),
		Color:       color,
		Waiting:     len(waiting),
		Running:     len(running),
		Errors:      len(errored),
		Done:        len(done),
		ActiveTotal: activeTotal,
		Message:     winningGroup[0].Message,
	}
}

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

func firstMessage(session Session, fallback string) string {
	if session.Message != "" {
		return session.Message
	}
	return fallback
}

func labelFor(session Session) string {
	switch strings.ToLower(session.Tool) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	default:
		if session.Tool == "" {
			return "AI"
		}
		return strings.ToUpper(session.Tool[:1]) + session.Tool[1:]
	}
}

func compactText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 80 {
		return text
	}
	return text[:77] + "..."
}

func perSessionLabel(s Session) string {
	switch s.State {
	case "waiting":
		return "WAIT " + firstMessage(s, "approval")
	case "error":
		return "ERR " + labelFor(s) + " " + firstMessage(s, "error")
	case "running":
		return labelFor(s) + " run"
	case "done":
		msg := firstMessage(s, "")
		if msg != "" {
			return labelFor(s) + " done " + msg
		}
		return labelFor(s) + " done"
	default:
		return labelFor(s)
	}
}

func aggregateLabel(waiting, running, errored, done int) string {
	parts := []string{"AI"}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("W%d", waiting))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("E%d", errored))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("R%d", running))
	}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("D%d", done))
	}
	return strings.Join(parts, " ")
}

// ClearIndicators turns off all three right-side indicator LEDs. Called
// once at server startup as part of the G.1a retirement of the old
// per-frame indicator semantics. Failures are not fatal (the device may
// be temporarily unreachable); the caller logs and continues.
// Subsequent Publish calls do not touch the indicators.
func (a *App) ClearIndicators(ctx context.Context) error {
	for i := 1; i <= 3; i++ {
		if err := a.publisher.ClearIndicator(ctx, i); err != nil {
			return fmt.Errorf("clear indicator %d: %w", i, err)
		}
	}
	return nil
}

// StartCoordinator runs the display coordinator goroutine + a dwell
// ticker that sends cmdTick on each interval. Blocks until ctx is done.
func (a *App) StartCoordinator(ctx context.Context) {
	cfg := a.cfg.Load()
	dwell := time.Duration(cfg.Display.RotationDwellSeconds) * time.Second
	if dwell <= 0 {
		dwell = 3 * time.Second
	}

	go a.coord.Run(ctx)

	ticker := time.NewTicker(dwell)
	defer ticker.Stop()

	// While the Pomodoro feature is enabled, a 1 s ticker advances the engine
	// and refreshes the countdown. pomoTick is a cheap no-op when the engine
	// is idle, so the ticker runs unconditionally when the feature is wired.
	var pomoC <-chan time.Time
	if a.engine != nil {
		pt := time.NewTicker(time.Second)
		defer pt.Stop()
		pomoC = pt.C
	}

	// Emit an initial tick so the first frame appears right after startup.
	a.coord.Send(coordCmd{kind: cmdTick})
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.coord.Send(coordCmd{kind: cmdTick})
		case <-pomoC:
			a.pomoTick()
		}
	}
}

func handleMetrics(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		app.metrics.render(w, app)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Snapshot())
	})
	mux.Handle("GET /version", handleVersion(a.versionInfo))
	mux.Handle("GET /metrics", handleMetrics(a))

	// Pomodoro reads are open like /state. The button hook is unauthenticated
	// because the AWTRIX device's button_callback cannot send a bearer token;
	// it only maps presses to timer actions, so LAN blast radius is minimal.
	mux.HandleFunc("GET /v1/pomodoro/state", a.handlePomodoroState)
	mux.HandleFunc("GET /v1/pomodoro/stats", a.handlePomodoroStats)
	mux.HandleFunc("GET /v1/pomodoro/heatmap", a.handlePomodoroHeatmap)
	mux.HandleFunc("GET /v1/pomodoro/workhours", a.handlePomodoroWorkHours)
	mux.HandleFunc("GET /v1/pomodoro/dashboard", a.handlePomodoroDashboard)
	// Open, read-only render preview for the menu app's Display tab. The
	// specific GET pattern wins over the "/v1/" requireAuth catch-all below.
	mux.HandleFunc("GET /v1/preview", a.handlePreview)
	mux.HandleFunc("GET /v1/weather/preview", a.handleWeatherPreview)
	mux.HandleFunc("GET /v1/pomodoro/preview", a.handlePomodoroPreview)
	mux.HandleFunc("GET /v1/reminders/preview", a.handleReminderPreview)
	mux.HandleFunc("GET /v1/meetings/preview", a.handleMeetingsPreview)
	mux.HandleFunc("GET /v1/meetings/state", a.handleMeetingsState)
	mux.HandleFunc("POST /hooks/awtrix/button", a.handleAwtrixButton)

	writeMux := http.NewServeMux()
	writeMux.Handle("POST /v1/status", http.HandlerFunc(a.handleStatus))
	writeMux.Handle("DELETE /v1/status", http.HandlerFunc(a.handleDeleteStatus))
	writeMux.Handle("POST /v1/clear", http.HandlerFunc(a.handleClear))
	writeMux.Handle("POST /v1/notify", http.HandlerFunc(a.handleNotify))
	writeMux.Handle("POST /v1/pomodoro/start", http.HandlerFunc(a.handlePomodoroStart))
	writeMux.Handle("POST /v1/pomodoro/pause", http.HandlerFunc(a.handlePomodoroPause))
	writeMux.Handle("POST /v1/pomodoro/resume", http.HandlerFunc(a.handlePomodoroResume))
	writeMux.Handle("POST /v1/pomodoro/stop", http.HandlerFunc(a.handlePomodoroStop))
	writeMux.Handle("POST /v1/pomodoro/skip", http.HandlerFunc(a.handlePomodoroSkip))
	writeMux.Handle("GET /v1/pomodoro/config", http.HandlerFunc(a.handlePomodoroConfigGet))
	writeMux.Handle("PUT /v1/pomodoro/config", http.HandlerFunc(a.handlePomodoroConfigPut))
	writeMux.Handle("GET /v1/apps", http.HandlerFunc(a.handleAppsGet))
	writeMux.Handle("PUT /v1/apps", http.HandlerFunc(a.handleAppsPut))
	writeMux.Handle("POST /v1/usage", http.HandlerFunc(a.handleUsage))
	writeMux.Handle("GET /v1/usage/config", http.HandlerFunc(a.handleUsageConfigGet))
	writeMux.Handle("PUT /v1/usage/config", http.HandlerFunc(a.handleUsageConfigPut))
	writeMux.Handle("GET /v1/display/config", http.HandlerFunc(a.handleDisplayConfigGet))
	writeMux.Handle("PUT /v1/display/config", http.HandlerFunc(a.handleDisplayConfigPut))
	writeMux.Handle("GET /v1/quiet/config", http.HandlerFunc(a.handleQuietConfigGet))
	writeMux.Handle("PUT /v1/quiet/config", http.HandlerFunc(a.handleQuietConfigPut))
	writeMux.Handle("GET /v1/weather/config", http.HandlerFunc(a.handleWeatherConfigGet))
	writeMux.Handle("PUT /v1/weather/config", http.HandlerFunc(a.handleWeatherConfigPut))
	writeMux.Handle("GET /v1/meetings/config", http.HandlerFunc(a.handleMeetingsConfigGet))
	writeMux.Handle("PUT /v1/meetings/config", http.HandlerFunc(a.handleMeetingsConfigPut))
	writeMux.Handle("POST /v1/reminders/fire", http.HandlerFunc(a.handleReminderFire))
	writeMux.Handle("GET /v1/device/discover", http.HandlerFunc(a.handleDeviceDiscover))
	writeMux.Handle("GET /v1/device/config", http.HandlerFunc(a.handleDeviceConfigGet))
	writeMux.Handle("PUT /v1/device/config", http.HandlerFunc(a.handleDeviceConfigPut))
	writeMux.Handle("GET /v1/device/settings", http.HandlerFunc(a.handleDeviceSettingsGet))
	writeMux.Handle("PUT /v1/device/settings", http.HandlerFunc(a.handleDeviceSettingsPut))
	writeMux.Handle("GET /v1/device/stats", http.HandlerFunc(a.handleDeviceStats))
	writeMux.Handle("GET /v1/device/sensors", http.HandlerFunc(a.handleDeviceSensorsGet))
	writeMux.Handle("PUT /v1/device/sensors", http.HandlerFunc(a.handleDeviceSensorsPut))
	writeMux.Handle("GET /v1/device/screen", http.HandlerFunc(a.handleDeviceScreen))
	writeMux.Handle("POST /v1/device/reboot", http.HandlerFunc(a.handleDeviceReboot))
	writeMux.Handle("POST /v1/device/notify/dismiss", http.HandlerFunc(a.handleDeviceDismiss))
	writeMux.Handle("POST /v1/device/app/next", http.HandlerFunc(a.handleDeviceNextApp))
	writeMux.Handle("POST /v1/device/app/previous", http.HandlerFunc(a.handleDevicePrevApp))
	writeMux.Handle("GET /v1/device/buttons", http.HandlerFunc(a.handleDeviceButtons))
	// Limiter outermost, auth inside: requests rejected by auth (401) still
	// consume rate-limit budget, so an attacker hammering wrong tokens gets
	// throttled to 429 instead of probing at full speed.
	mux.Handle("/v1/", rateLimit(a, requireAuth(a, a.logger, writeMux)))

	adminMux := http.NewServeMux()
	adminMux.Handle("GET /admin/doctor", handleAdminDoctor(a))
	adminMux.Handle("POST /admin/reload", handleAdminReload(a))
	// Same limiter-outside-auth ordering as /v1/: admin endpoints authenticate
	// with the same token, so their 401s must consume rate-limit budget too —
	// otherwise an attacker throttled on /v1/ could probe the token at full
	// speed via /admin/ 401s.
	mux.Handle("/admin/", rateLimit(a, adminRequireAuth(a, a.logger, adminMux)))

	// Order: logging outermost so the access log sees the original
	// response status; observeRequests inside so it can read the same.
	return loggingMiddleware(a.logger, observeRequests(a, mux))
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	var req StatusRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		var maxBytes *http.MaxBytesError
		reason := "parse"
		status := http.StatusBadRequest
		if errors.As(err, &maxBytes) {
			reason = "too_large"
			status = http.StatusRequestEntityTooLarge
		}
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", reason,
		)
		writeError(w, status, err)
		return
	}
	if err := req.validate(); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", validationField(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	normalized := req.normalized()
	render, prior := a.Upsert(req)
	a.recordActivityHeartbeat(normalized, time.Now())
	a.coord.Send(coordCmd{
		kind:       cmdUpsert,
		sessionKey: normalized.Key(),
		priorState: prior,
		newState:   normalized.State,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

func (a *App) handleClear(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "too_large",
		)
		writeError(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	render := a.Clear()
	a.coord.Send(coordCmd{kind: cmdClear})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

type NotifyRequest struct {
	Text     string `json:"text"`
	Color    string `json:"color"`
	Duration int    `json:"duration"`
	Hold     bool   `json:"hold"`
}

type DeleteRequest struct {
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
}

func (r DeleteRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	return nil
}

func (r DeleteRequest) key() string {
	return strings.TrimSpace(r.Source) + "/" + strings.ToLower(strings.TrimSpace(r.Tool)) + "/" + strings.TrimSpace(r.Session)
}

func (a *App) handleDeleteStatus(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		var maxBytes *http.MaxBytesError
		reason := "parse"
		status := http.StatusBadRequest
		if errors.As(err, &maxBytes) {
			reason = "too_large"
			status = http.StatusRequestEntityTooLarge
		}
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", reason,
		)
		writeError(w, status, err)
		return
	}
	if err := req.validate(); err != nil {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", validationField(err),
		)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.Delete(req.key())
	a.coord.Send(coordCmd{kind: cmdDelete, sessionKey: req.key()})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleNotify(w http.ResponseWriter, r *http.Request) {
	var req NotifyRequest
	if err := decodeJSON(w, r, &req, true); err != nil {
		var maxBytes *http.MaxBytesError
		reason := "parse"
		status := http.StatusBadRequest
		if errors.As(err, &maxBytes) {
			reason = "too_large"
			status = http.StatusRequestEntityTooLarge
		}
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", reason,
		)
		writeError(w, status, err)
		return
	}
	if req.Text == "" {
		a.logger.InfoContext(r.Context(), "request rejected",
			"remote_addr", r.RemoteAddr,
			"path", r.URL.Path,
			"reason", "validation",
			"field", "text",
		)
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	if req.Color == "" {
		req.Color = "#FFFFFF"
	}
	if req.Duration <= 0 {
		req.Duration = 5
	}
	if err := a.publisher.Notify(r.Context(), map[string]any{
		"text":       req.Text,
		"textColor":  req.Color,
		"durationMs": req.Duration * 1000, // the request carries seconds; NG takes ms
		"hold":       req.Hold,
		"wakeup":     true,
		"stack":      false,
	}); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validationField extracts a field name from validation errors that
// follow the convention "field-name <reason>" (e.g. "source is required").
// Returns the first whitespace-delimited token. Best-effort; falls back
// to the full message if the format doesn't match.
func validationField(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for i, c := range msg {
		if c == ' ' {
			return msg[:i]
		}
	}
	return msg
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, strict bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Trailing-tokens detection: a second Decode must return io.EOF.
	// dec.More() (the prior implementation) only reports true for nested
	// continuations (mid-array/mid-object), not for trailing top-level
	// values like {...}{...}.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing tokens after JSON body")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// requireAuth wraps next with bearer-token auth. Reads the token from
// app.cfg.Load() per request so token rotation via container restart
// (or future /admin/reload) takes effect for the next request after the
// swap. Fails closed: an empty configured token rejects every write, so a
// misconfigured deploy never silently exposes /v1 writes to the LAN.
// (Admin endpoints use the identical policy via adminRequireAuth.)
func requireAuth(app *App, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := app.cfg.Load().Auth.StatusToken
		if token == "" {
			logger.InfoContext(r.Context(), "auth disabled",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"method", r.Method,
			)
			writeError(w, http.StatusUnauthorized, errors.New("writes disabled: EMBER_TOKEN unset"))
			return
		}
		expected := "Bearer " + token
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			logger.InfoContext(r.Context(), "auth rejected",
				"remote_addr", r.RemoteAddr,
				"path", r.URL.Path,
				"method", r.Method,
			)
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

type Publisher interface {
	// CustomApp creates or replaces a pushed app
	// (PUT /api/v1/apps/pushed/{name}). Pushed apps are RAM-only on awtrix-ng
	// and vanish on device reboot.
	CustomApp(ctx context.Context, name string, payload map[string]any) error
	// ClearApp removes a pushed app (DELETE /api/v1/apps/{name}). Used by the
	// usage-app reconcile to drop stale/hidden apps.
	ClearApp(ctx context.Context, name string) error
	// ListApps returns the names of every app on the device
	// (GET /api/v1/apps), builtin and pushed alike. Used on startup to adopt
	// ember-managed pushed apps left from a previous run so they can be
	// reconciled/cleared even though the in-memory push trackers start empty.
	ListApps(ctx context.Context) ([]string, error)
	Notify(ctx context.Context, payload map[string]any) error
	// DismissNotify clears the currently-shown notification
	// (DELETE /api/v1/notifications/active). Used to acknowledge a held
	// reminder alarm when the user presses a clock button.
	DismissNotify(ctx context.Context) error
	// PlayRTTTL plays an inline RTTTL melody (POST /api/v1/sounds/play).
	// Used for reminder/weather chimes out-of-band of notifications; whether
	// NG still drops a notification's own sound alongside draw/icon is
	// re-evaluated in the sounds-consolidation ticket.
	PlayRTTTL(ctx context.Context, rtttl string) error
	// PlaySound plays a melody file already on the device (/MELODIES) by name
	// (POST /api/v1/sounds/play) — the device-sound-name counterpart to
	// PlayRTTTL.
	PlaySound(ctx context.Context, name string) error
	// Indicator lights one of the three corner LEDs
	// (PUT /api/v1/indicators/{1-3}).
	Indicator(ctx context.Context, index int, payload map[string]any) error
	// ClearIndicator turns a corner LED off (DELETE /api/v1/indicators/{1-3}).
	ClearIndicator(ctx context.Context, index int) error
	// Settings partially updates device settings (PATCH /api/v1/settings),
	// e.g. toggling app rotation and native button navigation for Pomodoro
	// takeover.
	Settings(ctx context.Context, payload map[string]any) error
	// Switch forces the device to the named app (PUT /api/v1/apps/active).
	Switch(ctx context.Context, name string) error
	// ListIcons returns the filenames in the device's /ICONS folder
	// (GET /api/v1/files?dir=/ICONS). Used by the weather icon provisioner to
	// find missing gallery icons.
	ListIcons(ctx context.Context) ([]string, error)
	// PutIcon uploads an icon file into /ICONS
	// (multipart POST /api/v1/files?dir=/ICONS). The AWTRIX3 firmware's own
	// on-demand gallery downloads were unreliable, so the server provisions
	// icons itself.
	PutIcon(ctx context.Context, filename string, data []byte) error
}

// HTTPPublisher drives the clock over awtrix-ng's API v1, delegating every
// call to internal/awtrix. A fresh client is built per call from the live
// config so URL changes from rediscovery take effect immediately.
type HTTPPublisher struct {
	app *App // for reading current AWTRIX config
}

// NewHTTPPublisher returns a publisher with no app reference yet. Callers
// must set p.app before calling any publish method (NewApp does this).
func NewHTTPPublisher() (*HTTPPublisher, error) {
	return &HTTPPublisher{}, nil
}

func (p *HTTPPublisher) client() (*awtrix.Client, error) {
	cfg := p.app.cfg.Load().AWTRIX
	base := strings.TrimRight(cfg.HTTPBaseURL, "/")
	if base == "" {
		return nil, errors.New("awtrix.http_base_url is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid awtrix.http_base_url: %w", err)
	}
	return awtrix.NewClient(base, time.Duration(cfg.TimeoutSeconds)*time.Second), nil
}

func (p *HTTPPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PushApp(ctx, name, payload)
}

func (p *HTTPPublisher) ClearApp(ctx context.Context, name string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.DeleteApp(ctx, name)
}

func (p *HTTPPublisher) ListApps(ctx context.Context) ([]string, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	apps, err := c.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	return names, nil
}

func (p *HTTPPublisher) ListIcons(ctx context.Context) ([]string, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	return c.ListIcons(ctx)
}

func (p *HTTPPublisher) PutIcon(ctx context.Context, filename string, data []byte) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PutIcon(ctx, filename, data)
}

func (p *HTTPPublisher) Notify(ctx context.Context, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.Notify(ctx, payload)
}

func (p *HTTPPublisher) DismissNotify(ctx context.Context) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.DismissNotify(ctx)
}

func (p *HTTPPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PlayRTTTL(ctx, rtttl)
}

func (p *HTTPPublisher) PlaySound(ctx context.Context, name string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PlaySound(ctx, name)
}

func (p *HTTPPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.SetIndicator(ctx, index, payload)
}

func (p *HTTPPublisher) ClearIndicator(ctx context.Context, index int) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.ClearIndicator(ctx, index)
}

func (p *HTTPPublisher) Settings(ctx context.Context, payload map[string]any) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.PatchSettings(ctx, payload)
}

func (p *HTTPPublisher) Switch(ctx context.Context, name string) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	return c.SwitchApp(ctx, name)
}

func main() {
	if sub, args, ok := scanSubcommand(os.Args[1:]); ok {
		switch sub {
		case "version", "-v", "--version":
			runVersion()
			return
		case "healthcheck":
			runHealthcheck()
			return
		case "doctor":
			runDoctor(args)
			return
		}
	}

	configFlag := flag.String("config", "", "path to config JSON file")
	printConfig := flag.Bool("print-config", false, "print loaded config (post-defaults, secrets redacted) to stdout and exit")
	flag.Parse()

	if *printConfig {
		runPrintConfig(*configFlag)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	configPath, configSource := resolveConfigPath(*configFlag)
	cfg, err := loadConfig(*configFlag, logger)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}
	if cfg.Display.PulseStyle != "" {
		logger.Warn("display.pulse_style is deprecated and ignored — AWTRIX firmware animates attention via blinkText", "value", cfg.Display.PulseStyle)
	}

	tlsCfg, err := readTLSEnv()
	if err != nil {
		logger.Error("TLS configuration invalid", "err", err)
		os.Exit(1)
	}

	publisher, err := NewHTTPPublisher()
	if err != nil {
		logger.Error("create publisher failed", "err", err)
		os.Exit(1)
	}

	app := NewApp(cfg, publisher, logger)
	app.configPath = configPath
	app.configSource = configSource

	// Always wire the Pomodoro engine so the feature can be toggled at runtime
	// from the app (cfg.Pomodoro.Enabled — persisted to the store — gates whether
	// it runs). Non-fatal: if the store can't open, the feature is simply
	// unavailable until the data dir is writable.
	if err := app.initPomodoro(cfg.Pomodoro); err != nil {
		logger.Warn("pomodoro init failed; feature unavailable until the data store is writable", "err", err, "db_path", cfg.Pomodoro.DBPath)
	} else {
		logger.Info("pomodoro wired", "enabled", app.cfg.Load().Pomodoro.Enabled, "db_path", cfg.Pomodoro.DBPath, "button_callback", cfg.Pomodoro.ButtonCallback)
	}
	// Weather only needs the store to *persist* menu edits; it runs fine
	// in-memory. A store-open failure here (e.g. Pomodoro disabled and no
	// writable DB volume) must not block startup — warn and carry on.
	if err := app.initWeather(cfg); err != nil {
		logger.Warn("weather store init failed; config will not persist across restarts", "err", err)
	}
	// ICS calendar URLs are credentials; they live only in the env var and are
	// never logged as strings, stored, or echoed in API responses (count only).
	{
		var meetingsDropped int
		app.meetingsURLs, meetingsDropped = parseICSURLs(os.Getenv("EMBER_MEETINGS_ICS_URLS"))
		if meetingsDropped > 0 {
			logger.Warn("meetings ICS feed entries ignored (unsupported scheme)", "dropped", meetingsDropped)
		}
	}
	if len(app.meetingsURLs) > 0 {
		logger.Info("meetings ICS feeds configured", "count", len(app.meetingsURLs))
	}
	if err := app.initMeetings(cfg); err != nil {
		logger.Warn("meetings store init failed; config will not persist across restarts", "err", err)
	}
	app.loadPersistedUsageSettings()   // runtime usage-widget toggles over the file baseline
	app.loadPersistedDisplaySettings() // runtime display config overrides over the file baseline
	app.loadPersistedQuietSettings()   // quiet-hours override over the file baseline
	if cfg.Weather.Enabled {
		logger.Info("weather enabled", "provider", cfg.Weather.Provider, "location", cfg.Weather.LocationName)
	}
	if cfg.Meetings.IsEnabled() && len(app.meetingsURLs) > 0 {
		logger.Info("meetings enabled", "ics_feeds", len(app.meetingsURLs))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Resolve the clock address before the coordinator publishes: store override
	// > reachable config.json baseline > mDNS auto-discovery. Bounded so it can't
	// stall startup for long; a no-op when a reachable URL is already configured.
	app.initDeviceDiscovery(ctx)

	// Advertise the server over mDNS so the macOS app can discover it (requires
	// host/macvlan networking to reach the LAN). Non-fatal; off via
	// EMBER_MDNS_ADVERTISE=0.
	if discovery.AdvertiseEnabled(os.Getenv("EMBER_MDNS_ADVERTISE")) {
		if port, perr := discovery.PortFromAddr(cfg.HTTP.Addr); perr == nil {
			ver := app.versionInfo.Revision
			if ver == "" {
				ver = "dev"
			}
			logger.Info("mDNS advertising enabled", "service", "_ember._tcp", "port", port)
			go func() {
				if err := discovery.Advertise(ctx, "Ember", port, ver); err != nil && ctx.Err() == nil {
					logger.Warn("mDNS advertise stopped", "err", err)
				}
			}()
		} else {
			logger.Warn("mDNS advertise skipped: cannot parse port", "addr", cfg.HTTP.Addr, "err", perr)
		}
	} else {
		logger.Info("mDNS advertising disabled (EMBER_MDNS_ADVERTISE)")
	}

	if err := app.ClearIndicators(context.Background()); err != nil {
		logger.Warn("clear indicators on startup failed", "err", err)
	}

	go app.limiter.runSweeper(ctx)
	go app.StartCoordinator(ctx)
	go app.StartWeather(ctx)
	go app.StartMeetings(ctx)

	// Periodic self-healing watch: re-check the effective clock URL and swap to
	// a reachable mDNS candidate if it's gone dark, and re-push everything when
	// the clock's uptime shows it rebooted. Off via awtrix.auto_rediscover=false.
	if cfg.AWTRIX.AutoRediscoverEnabled() {
		logger.Info("clock auto-rediscover enabled", "interval", deviceWatchInterval.String())
		go app.StartDeviceWatch(ctx, deviceWatchInterval)
	} else {
		logger.Info("clock auto-rediscover disabled (awtrix.auto_rediscover)")
	}

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	listener, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		logger.Error("listen failed", "err", err, "addr", cfg.HTTP.Addr)
		os.Exit(1)
	}
	app.listener = listener

	go func() {
		addr := listener.Addr().String()
		var serveErr error
		if tlsCfg.enabled {
			// Wrap the existing TCP listener so app.listener still points at
			// the raw socket for doctor's http_listening detail.
			tlsListener := tls.NewListener(listener, &tls.Config{
				Certificates: []tls.Certificate{tlsCfg.cert},
				MinVersion:   tls.VersionTLS12,
			})
			logger.Info("server listening (https)", "addr", addr)
			serveErr = server.Serve(tlsListener)
		} else {
			logger.Info("server listening", "addr", addr)
			serveErr = server.Serve(listener)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("server failed", "err", serveErr)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("server shutdown failed", "err", err)
	}
	// Close the Pomodoro stats DB so WAL is checkpointed and in-flight writes
	// are flushed before exit.
	if app.store != nil {
		if err := app.store.Close(); err != nil {
			logger.Warn("pomodoro store close failed", "err", err)
		}
	}
}

// scanSubcommand walks args to find the first non-flag positional. It
// recognises `-flag=value` (single token) and `-flag value` (two tokens)
// for the server's own flags. Returns (token, remaining-after-token, true)
// if the token matches a known subcommand; otherwise ("", nil, false).
func scanSubcommand(args []string) (sub string, rest []string, ok bool) {
	known := map[string]bool{
		"version": true, "-v": true, "--version": true,
		"healthcheck": true,
		"doctor":      true,
	}
	flagWithValue := map[string]bool{
		"-config": true, "--config": true,
	}
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if strings.Contains(tok, "=") && (strings.HasPrefix(tok, "-config=") || strings.HasPrefix(tok, "--config=")) {
			continue
		}
		if flagWithValue[tok] {
			i++ // skip its value
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue // unknown flag; skip
		}
		if known[tok] {
			return tok, args[i+1:], true
		}
		return "", nil, false
	}
	return "", nil, false
}
