package main

import (
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
	// Usage-widget toggles. Pointers so the file can distinguish "unset"
	// (nil → default on) from an explicit false; resolved via the helpers below.
	UsageWidget   *bool `json:"usage_widget,omitempty"`
	UsagePerModel *bool `json:"usage_per_model,omitempty"`
}

// usageWidgetEnabled reports whether the standalone AI-usage apps should be
// pushed to the device. Default on (nil pointer).
func (c Config) usageWidgetEnabled() bool { return c.UsageWidget == nil || *c.UsageWidget }

// usagePerModelEnabled reports whether the Claude per-model (Opus/Sonnet) usage
// frames should be pushed. Default on (nil pointer).
func (c Config) usagePerModelEnabled() bool { return c.UsagePerModel == nil || *c.UsagePerModel }

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
	FrameLifetimeSeconds int `json:"frame_lifetime_seconds"`
	IdleRestoreSeconds   int `json:"idle_restore_seconds"`
	// PulseStyle is parsed but ignored. Kept so configs from G.1b that
	// still carry "pulse_style": "breathe" continue to parse under
	// DisallowUnknownFields. AWTRIX firmware has no multi-frame draw
	// mode; attention is animated via blinkText instead.
	PulseStyle string `json:"pulse_style,omitempty"`
}

func defaultConfig() Config {
	return Config{
		HTTP: HTTPConfig{
			Addr: ":8080",
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
			IdleRestoreSeconds:   1200,
		},
		RateLimit: RateLimitConfig{
			Disabled:         false,
			Burst:            10,
			RefillPerSec:     2.0,
			IdleEvictSeconds: 300,
		},
		Pomodoro: PomodoroConfig{
			Enabled:               false,
			FocusMinutes:          25,
			ShortBreakMinutes:     5,
			LongBreakMinutes:      15,
			RoundsBeforeLongBreak: 4,
			AutoStartNext:         true,
			Sound:                 true,
			FocusColor:            "#FF0000",
			BreakColor:            "#00FF00",
			DBPath:                "/var/lib/ember/pomodoro.db",
			ButtonCallback:        true,
			MaxSessionMinutes:     480,
			WorkHoursGapMinutes:   15,
			DayStartHour:          4,
			StreakGraceDays:       1,
			DailyGoalSessions:     8,
			WeeklyGoalDays:        5,
		},
	}
}

func loadConfig(path string) (Config, error) {
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
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":8080"
	}
	if c.AWTRIX.HTTPBaseURL == "" {
		c.AWTRIX.HTTPBaseURL = "http://192.168.0.14"
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
		c.Display.IdleRestoreSeconds = 1200
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
	if len(strings.TrimSpace(r.Activity)) > 80 {
		return fmt.Errorf("activity too long (%d chars, max 80)", len(strings.TrimSpace(r.Activity)))
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
}

func NewApp(cfg Config, publisher Publisher, logger *slog.Logger) *App {
	a := &App{
		publisher:   publisher,
		logger:      logger,
		sessions:    make(map[string]Session),
		versionInfo: computeVersionInfo(),
		startedAt:   time.Now(),
		usage:       newUsageStore(),
		weather:     newWeatherStore(),
	}
	a.weatherFetcher = newWeatherFetcher()
	a.cfg.Store(&cfg)
	a.metrics = newMetrics()
	a.limiter = NewIPLimiter(a)
	if hp, ok := publisher.(*HTTPPublisher); ok {
		hp.app = a
	}
	a.coord = newCoordinator(cfg, a.cfg.Load, publisher, realClock{}, logger, a.metrics)
	a.coord.snapshot = a.Snapshot
	a.coord.onPublishResult = a.recordPublish
	a.hiddenApps = map[string]bool{}
	a.coord.hiddenApps = a.hiddenAppsSet
	a.coord.usage = a.usage
	a.coord.weather = a.weather
	return a
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
	payload := map[string]any{"color": "0"}
	for i := 1; i <= 3; i++ {
		if err := a.publisher.Indicator(ctx, i, payload); err != nil {
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
	mux.HandleFunc("POST /hooks/awtrix/button", a.handleAwtrixButton)

	writeMux := http.NewServeMux()
	writeMux.Handle("POST /v1/status", rateLimit(a, http.HandlerFunc(a.handleStatus)))
	writeMux.Handle("DELETE /v1/status", rateLimit(a, http.HandlerFunc(a.handleDeleteStatus)))
	writeMux.Handle("POST /v1/clear", rateLimit(a, http.HandlerFunc(a.handleClear)))
	writeMux.Handle("POST /v1/notify", rateLimit(a, http.HandlerFunc(a.handleNotify)))
	writeMux.Handle("POST /v1/pomodoro/start", rateLimit(a, http.HandlerFunc(a.handlePomodoroStart)))
	writeMux.Handle("POST /v1/pomodoro/pause", rateLimit(a, http.HandlerFunc(a.handlePomodoroPause)))
	writeMux.Handle("POST /v1/pomodoro/resume", rateLimit(a, http.HandlerFunc(a.handlePomodoroResume)))
	writeMux.Handle("POST /v1/pomodoro/stop", rateLimit(a, http.HandlerFunc(a.handlePomodoroStop)))
	writeMux.Handle("POST /v1/pomodoro/skip", rateLimit(a, http.HandlerFunc(a.handlePomodoroSkip)))
	writeMux.Handle("GET /v1/pomodoro/config", rateLimit(a, http.HandlerFunc(a.handlePomodoroConfigGet)))
	writeMux.Handle("PUT /v1/pomodoro/config", rateLimit(a, http.HandlerFunc(a.handlePomodoroConfigPut)))
	writeMux.Handle("GET /v1/apps", rateLimit(a, http.HandlerFunc(a.handleAppsGet)))
	writeMux.Handle("PUT /v1/apps", rateLimit(a, http.HandlerFunc(a.handleAppsPut)))
	writeMux.Handle("POST /v1/usage", rateLimit(a, http.HandlerFunc(a.handleUsage)))
	writeMux.Handle("GET /v1/weather/config", rateLimit(a, http.HandlerFunc(a.handleWeatherConfigGet)))
	writeMux.Handle("PUT /v1/weather/config", rateLimit(a, http.HandlerFunc(a.handleWeatherConfigPut)))
	writeMux.Handle("POST /v1/reminders/fire", rateLimit(a, http.HandlerFunc(a.handleReminderFire)))
	mux.Handle("/v1/", requireAuth(a, a.logger, writeMux))

	adminMux := http.NewServeMux()
	adminMux.Handle("GET /admin/doctor", handleAdminDoctor(a))
	adminMux.Handle("POST /admin/reload", handleAdminReload(a))
	mux.Handle("/admin/", adminRequireAuth(a, a.logger, adminMux))

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
		"text":     req.Text,
		"color":    req.Color,
		"duration": req.Duration,
		"hold":     req.Hold,
		"wakeup":   true,
		"stack":    false,
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
// swap. Empty token preserves the existing dev-mode policy: writes
// remain open. (Admin endpoints get the stricter adminRequireAuth.)
func requireAuth(app *App, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := app.cfg.Load().Auth.StatusToken
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		expected := "Bearer " + token
		if r.Header.Get("Authorization") != expected {
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
	CustomApp(ctx context.Context, name string, payload map[string]any) error
	// ClearApp removes a custom app from the device (POST empty body to
	// /api/custom?name=…). Used by the usage-app reconcile to drop stale/hidden
	// apps.
	ClearApp(ctx context.Context, name string) error
	Notify(ctx context.Context, payload map[string]any) error
	// DismissNotify clears the currently-shown notification (POST
	// /api/notify/dismiss). Used to acknowledge a held reminder alarm when the
	// user presses a clock button (the firmware's own dismissal doesn't run while
	// a button callback is configured).
	DismissNotify(ctx context.Context) error
	// PlayRTTTL plays an RTTTL melody via the device's dedicated /api/rtttl
	// endpoint. Used for reminder/weather chimes because a notification's own sound
	// is dropped by the firmware when the notification also draws an icon.
	PlayRTTTL(ctx context.Context, rtttl string) error
	// PlaySound plays a melody file already on the device (MELODIES) by name via
	// /api/sound — the device-sound-name counterpart to PlayRTTTL.
	PlaySound(ctx context.Context, name string) error
	Indicator(ctx context.Context, index int, payload map[string]any) error
	// Settings writes device settings (POST /api/settings), e.g. toggling app
	// rotation (ATRANS) and native button navigation (BLOCKN) for Pomodoro
	// takeover.
	Settings(ctx context.Context, payload map[string]any) error
	// Switch forces the device to the named app (POST /api/switch).
	Switch(ctx context.Context, name string) error
}

type HTTPPublisher struct {
	app *App // for reading current AWTRIX config
}

// NewHTTPPublisher returns a publisher with no app reference yet. Callers
// must set p.app before calling any publish method (NewApp does this).
func NewHTTPPublisher() (*HTTPPublisher, error) {
	return &HTTPPublisher{}, nil
}

func (p *HTTPPublisher) baseAndClient() (string, *http.Client, error) {
	cfg := p.app.cfg.Load().AWTRIX
	base := strings.TrimRight(cfg.HTTPBaseURL, "/")
	if base == "" {
		return "", nil, errors.New("awtrix.http_base_url is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return "", nil, fmt.Errorf("invalid awtrix.http_base_url: %w", err)
	}
	return base, &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}, nil
}

func (p *HTTPPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/custom?name="+url.QueryEscape(name), payload)
}

// ClearApp removes a custom app by POSTing an empty body to its /api/custom
// slot — AWTRIX deletes a custom app when it receives an empty JSON object.
func (p *HTTPPublisher) ClearApp(ctx context.Context, name string) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/custom?name="+url.QueryEscape(name), map[string]any{})
}

func (p *HTTPPublisher) Notify(ctx context.Context, payload map[string]any) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/notify", payload)
}

func (p *HTTPPublisher) DismissNotify(ctx context.Context) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/notify/dismiss", map[string]any{})
}

func (p *HTTPPublisher) PlayRTTTL(ctx context.Context, rtttl string) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/rtttl", strings.NewReader(rtttl))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("awtrix rtttl http %s", resp.Status)
	}
	return nil
}

func (p *HTTPPublisher) PlaySound(ctx context.Context, name string) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/sound", map[string]any{"sound": name})
}

func (p *HTTPPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	if index < 1 || index > 3 {
		return fmt.Errorf("indicator index must be 1-3, got %d", index)
	}
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/indicator"+strconv.Itoa(index), payload)
}

func (p *HTTPPublisher) Settings(ctx context.Context, payload map[string]any) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/settings", payload)
}

func (p *HTTPPublisher) Switch(ctx context.Context, name string) error {
	base, client, err := p.baseAndClient()
	if err != nil {
		return err
	}
	return p.postJSON(ctx, client, base+"/api/switch", map[string]any{"name": name})
}

func (p *HTTPPublisher) postJSON(ctx context.Context, client *http.Client, endpoint string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("awtrix http %s: %s", resp.Status, strings.TrimSpace(string(limited)))
	}
	return nil
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
	cfg, err := loadConfig(*configFlag)
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

	if cfg.Pomodoro.Enabled {
		if err := app.initPomodoro(cfg.Pomodoro); err != nil {
			logger.Error("pomodoro init failed", "err", err, "db_path", cfg.Pomodoro.DBPath)
			os.Exit(1)
		}
		logger.Info("pomodoro enabled", "db_path", cfg.Pomodoro.DBPath, "button_callback", cfg.Pomodoro.ButtonCallback)
	}
	// Weather only needs the store to *persist* menu edits; it runs fine
	// in-memory. A store-open failure here (e.g. Pomodoro disabled and no
	// writable DB volume) must not block startup — warn and carry on.
	if err := app.initWeather(cfg); err != nil {
		logger.Warn("weather store init failed; config will not persist across restarts", "err", err)
	}
	if cfg.Weather.Enabled {
		logger.Info("weather enabled", "provider", cfg.Weather.Provider, "location", cfg.Weather.LocationName)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.ClearIndicators(context.Background()); err != nil {
		logger.Warn("clear indicators on startup failed", "err", err)
	}

	go app.limiter.runSweeper(ctx)
	go app.StartCoordinator(ctx)
	go app.StartWeather(ctx)

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
