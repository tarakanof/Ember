package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

type WeatherConfig struct {
	Enabled              bool              `json:"enabled"`
	Provider             string            `json:"provider"`
	Latitude             float64           `json:"latitude"`
	Longitude            float64           `json:"longitude"`
	LocationName         string            `json:"location_name"`
	Units                string            `json:"units"`
	RefreshMinutes       int               `json:"refresh_minutes"`
	RotateInApps         *bool             `json:"rotate_in_apps"`
	ForecastTile         *bool             `json:"forecast_tile"`
	ForecastHours        int               `json:"forecast_hours"`
	SunPopups            *bool             `json:"sun_popups"`
	MoonPhase            *bool             `json:"moon_phase"`
	PopupIntervalMinutes *int              `json:"popup_interval_minutes"`
	PopupDurationSeconds int               `json:"popup_duration_seconds"`
	PopupOnChange        *bool             `json:"popup_on_change"`
	SevereAlert          *bool             `json:"severe_alert"`
	SevereSound          string            `json:"severe_sound"`
	UseNativeIcons       bool              `json:"use_native_icons"`
	IconIDs              map[string]string `json:"icon_ids,omitempty"`
	TileNativeIcons      bool              `json:"tile_native_icons"`
	AirTile              *bool             `json:"air_tile"`
	AirPopupThreshold    int               `json:"air_popup_threshold"`
	Overlay              *bool             `json:"overlay"`
}

func boolPtr(b bool) *bool { return &b }
func intPtr(v int) *int    { return &v }

const defaultWeatherPopupIntervalMinutes = 120

func (c WeatherConfig) RotateInAppsEnabled() bool  { return c.RotateInApps == nil || *c.RotateInApps }
func (c WeatherConfig) ForecastTileEnabled() bool  { return c.ForecastTile == nil || *c.ForecastTile }
func (c WeatherConfig) SunPopupsEnabled() bool     { return c.SunPopups == nil || *c.SunPopups }
func (c WeatherConfig) MoonPhaseEnabled() bool     { return c.MoonPhase == nil || *c.MoonPhase }
func (c WeatherConfig) PopupOnChangeEnabled() bool { return c.PopupOnChange == nil || *c.PopupOnChange }
func (c WeatherConfig) SevereAlertEnabled() bool   { return c.SevereAlert == nil || *c.SevereAlert }
func (c WeatherConfig) AirTileEnabled() bool       { return c.AirTile == nil || *c.AirTile }
func (c WeatherConfig) OverlayEnabled() bool       { return c.Overlay == nil || *c.Overlay }

// nil means 120, explicit 0 turns it off.
func (c WeatherConfig) PopupIntervalMins() int {
	if c.PopupIntervalMinutes == nil {
		return defaultWeatherPopupIntervalMinutes
	}
	if v := *c.PopupIntervalMinutes; v > 0 {
		return v
	}
	return 0
}

func (c *WeatherConfig) fillAbsent() {
	if c.RotateInApps == nil {
		c.RotateInApps = boolPtr(true)
	}
	if c.ForecastTile == nil {
		c.ForecastTile = boolPtr(true)
	}
	if c.SunPopups == nil {
		c.SunPopups = boolPtr(true)
	}
	if c.MoonPhase == nil {
		c.MoonPhase = boolPtr(true)
	}
	if c.PopupOnChange == nil {
		c.PopupOnChange = boolPtr(true)
	}
	if c.SevereAlert == nil {
		c.SevereAlert = boolPtr(true)
	}
	if c.AirTile == nil {
		c.AirTile = boolPtr(true)
	}
	if c.Overlay == nil {
		c.Overlay = boolPtr(true)
	}
	if c.PopupIntervalMinutes == nil {
		c.PopupIntervalMinutes = intPtr(defaultWeatherPopupIntervalMinutes)
	}
}

func (c WeatherConfig) weatherIconID(cond string) string {
	if id := c.IconIDs[cond]; id != "" {
		return id
	}
	return defaultWeatherIconIDs[cond]
}

const defaultWeatherSevereSound = "storm:d=4,o=5,b=160:8c6,8a,8c6,8a,8c6"

var defaultWeatherIconIDs = map[string]string{
	render.WeatherClear:  "1338",
	render.WeatherClouds: "2286",
	render.WeatherFog:    "17056",
	render.WeatherRain:   "72",
	render.WeatherSnow:   "2289",
	render.WeatherStorm:  "11428",
}

func (c *WeatherConfig) applyDefaults() {
	if c.Provider == "" {
		c.Provider = "open-meteo"
	}
	if c.Units == "" {
		c.Units = "metric"
	}
	if c.RefreshMinutes <= 0 {
		c.RefreshMinutes = 10
	}
	if c.PopupDurationSeconds <= 0 {
		c.PopupDurationSeconds = 30
	}
	c.fillAbsent()
	if *c.PopupIntervalMinutes < 0 {
		c.PopupIntervalMinutes = intPtr(0)
	}
	if c.ForecastHours <= 0 || c.ForecastHours > 24 {
		c.ForecastHours = 24
	}
	if c.AirPopupThreshold < 0 {
		c.AirPopupThreshold = 0
	} else if c.AirPopupThreshold == 0 {
		c.AirPopupThreshold = 80
	}
}

var weatherIconIDPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

func validateWeather(c WeatherConfig) error {
	switch c.Provider {
	case "open-meteo", "met-no":
	default:
		return fmt.Errorf("weather.provider %q must be open-meteo or met-no", c.Provider)
	}
	switch c.Units {
	case "metric", "imperial":
	default:
		return fmt.Errorf("weather.units %q must be metric or imperial", c.Units)
	}
	if c.Latitude < -90 || c.Latitude > 90 {
		return fmt.Errorf("weather.latitude %v out of range", c.Latitude)
	}
	if c.Longitude < -180 || c.Longitude > 180 {
		return fmt.Errorf("weather.longitude %v out of range", c.Longitude)
	}
	if c.RefreshMinutes < 1 {
		return errors.New("weather.refresh_minutes must be >= 1")
	}
	if c.PopupDurationSeconds < 1 || c.PopupDurationSeconds > 300 {
		return errors.New("weather.popup_duration_seconds must be 1..300")
	}
	if c.AirPopupThreshold < 0 || c.AirPopupThreshold > 200 {
		return errors.New("weather.air_popup_threshold must be 0..200")
	}
	if c.ForecastHours < 0 || c.ForecastHours > 24 {
		return errors.New("weather.forecast_hours must be 1..24")
	}
	keys := make([]string, 0, len(c.IconIDs))
	for k := range c.IconIDs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := c.IconIDs[k]
		if !weatherIconIDPattern.MatchString(v) {
			return fmt.Errorf("weather.icon_ids[%s] %q must be a numeric LaMetric icon id (1-10 digits)", k, v)
		}
	}
	return nil
}

type weatherObservation struct {
	Condition       string
	ConditionCode   string
	TempC           float64
	Severe          bool
	ObservedAt      time.Time
	FetchedAt       time.Time
	HourlyStart     time.Time
	Hourly          []float64
	Overlay         string
	TZOffsetSeconds int
	TZKnown         bool
	ValidUntil      time.Time
	Now             weatherNow
	PrecipStep      time.Duration
	Precip          []precipStep
	Days            []weatherDay
	Steps           []currentStep
}

type currentStep struct {
	At        time.Time
	Condition string
	Code      string
	TempC     float64
	Severe    bool
	Overlay   string
	Now       weatherNow
}

func (o weatherObservation) current(now time.Time) weatherObservation {
	i := sort.Search(len(o.Steps), func(i int) bool { return o.Steps[i].At.After(now) }) - 1
	if i < 0 {
		return o
	}
	c := o.Steps[i]
	o.Condition, o.ConditionCode, o.TempC, o.Severe = c.Condition, c.Code, c.TempC, c.Severe
	o.Overlay, o.Now, o.ObservedAt = c.Overlay, c.Now, c.At
	return o
}

type weatherNow struct {
	Cloud   *float64
	UV      *float64
	WindKmh *float64
	GustKmh *float64
	WindDir *float64
}

type precipStep struct {
	At   time.Time
	MMh  float64
	Snow bool
}

type weatherDay struct {
	Start      time.Time
	Cond       string
	Code       string
	MinC       *float64
	MaxC       *float64
	PrecipMM   *float64
	Pop        *float64
	UVMax      *float64
	GustMaxKmh *float64
}

const forecastFetchHours = 24

const airFetchHours = 24

const forecastDays = 4

type airObservation struct {
	AQI             float64
	PM25            float64
	PM10            float64
	HourlyAQI       []float64
	HourlyStart     time.Time
	FetchedAt       time.Time
	TZOffsetSeconds int
	TZKnown         bool
}

type weatherLocation struct{ lat, lon float64 }

type weatherSource struct {
	provider string
	loc      weatherLocation
}

func (c WeatherConfig) location() weatherLocation { return weatherLocation{c.Latitude, c.Longitude} }

func (c WeatherConfig) source() weatherSource { return weatherSource{c.Provider, c.location()} }

type weatherStore struct {
	mu        sync.RWMutex
	source    weatherSource
	obs       weatherObservation
	have      bool
	lastFetch time.Time
	failures  int
	retryAt   time.Time

	lastModified string
	notBefore    time.Time

	prevCondition  string
	prevSevere     bool
	lastPopupAt    time.Time
	sunriseDoneDay string
	sunsetDoneDay  string

	air         airObservation
	haveAir     bool
	prevAQI     float64
	havePrevAQI bool
}

func newWeatherStore() *weatherStore { return &weatherStore{} }

func (s *weatherStore) serves(src weatherSource) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.source == src
}

func (s *weatherStore) switchSource(src weatherSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.source == src {
		return
	}
	if s.source.loc != src.loc {
		s.air, s.haveAir, s.havePrevAQI = airObservation{}, false, false
	}
	s.source = src
	s.obs = weatherObservation{}
	s.have = false
	s.lastFetch = time.Time{}
	s.failures = 0
	s.retryAt = time.Time{}
	s.lastModified = ""
	s.notBefore = time.Time{}
	s.prevCondition = ""
}

func wmoCondition(code int) (string, bool) {
	switch {
	case code == 0:
		return render.WeatherClear, false
	case code >= 1 && code <= 3:
		return render.WeatherClouds, false
	case code == 45 || code == 48:
		return render.WeatherFog, false
	case code >= 51 && code <= 57:
		return render.WeatherRain, false
	case code >= 61 && code <= 67:
		return render.WeatherRain, code == 65 || code == 67
	case code >= 71 && code <= 77:
		return render.WeatherSnow, code == 75
	case code >= 80 && code <= 82:
		return render.WeatherRain, code == 82
	case code == 85 || code == 86:
		return render.WeatherSnow, code == 86
	case code >= 95:
		return render.WeatherStorm, true
	default:
		return render.WeatherClouds, false
	}
}

func metSymbolCondition(sym string) (string, bool) {
	s := strings.ToLower(sym)
	switch {
	case strings.Contains(s, "thunder"):
		return render.WeatherStorm, true
	case strings.Contains(s, "snow") || strings.Contains(s, "sleet"):
		return render.WeatherSnow, strings.Contains(s, "heavy")
	case strings.Contains(s, "rain") || strings.Contains(s, "showers"):
		return render.WeatherRain, strings.Contains(s, "heavy")
	case strings.Contains(s, "fog"):
		return render.WeatherFog, false
	case strings.Contains(s, "cloud"):
		return render.WeatherClouds, false
	case strings.Contains(s, "clear") || strings.Contains(s, "fair"):
		return render.WeatherClear, false
	default:
		return render.WeatherClouds, false
	}
}

func wmoOverlay(code int) string {
	switch {
	case code == 48:
		return render.OverlayFrost
	case code >= 51 && code <= 57:
		return render.OverlayDrizzle
	case code == 65 || code == 67 || code == 82:
		return render.OverlayStorm
	case (code >= 61 && code <= 67) || (code >= 80 && code <= 82):
		return render.OverlayRain
	case (code >= 71 && code <= 77) || code == 85 || code == 86:
		return render.OverlaySnow
	case code >= 95:
		return render.OverlayThunder
	default:
		return ""
	}
}

func metSymbolOverlay(sym string) string {
	s := strings.ToLower(sym)
	switch {
	case strings.Contains(s, "thunder"):
		return render.OverlayThunder
	case strings.Contains(s, "snow"):
		return render.OverlaySnow
	case strings.Contains(s, "rain") || strings.Contains(s, "sleet"):
		if strings.Contains(s, "heavy") {
			return render.OverlayStorm
		}
		return render.OverlayRain
	default:
		return ""
	}
}

func weatherOverlay(obs weatherObservation, cfg WeatherConfig) string {
	if !cfg.OverlayEnabled() {
		return ""
	}
	return obs.Overlay
}

func weatherTempText(tempC float64, units string) string {
	t := tempC
	if units == "imperial" {
		t = tempC*9/5 + 32
	}
	return fmt.Sprintf("%d°", int(math.Round(t)))
}

var weatherWords = map[string]string{
	render.WeatherClear:  "CLEAR",
	render.WeatherClouds: "CLOUDY",
	render.WeatherFog:    "FOG",
	render.WeatherRain:   "RAIN",
	render.WeatherSnow:   "SNOW",
	render.WeatherStorm:  "STORM",
}

func weatherLabel(obs weatherObservation, cfg WeatherConfig) string {
	word := weatherWords[obs.Condition]
	if word == "" {
		word = strings.ToUpper(obs.Condition)
	}
	return word + " " + weatherTempText(obs.TempC, cfg.Units)
}

func (a *App) StartWeather(ctx context.Context) {
	if a.weather == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.pollWeather(ctx, time.Now())
	for {
		_, changed := a.changes.subscribe()
		if !a.weather.serves(a.cfg.Load().Weather.source()) {
			a.pollWeather(ctx, time.Now())
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollWeather(ctx, time.Now())
		case <-changed:
		}
	}
}

func (a *App) pollWeather(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Weather
	src := cfg.source()
	a.weather.switchSource(src)
	if !cfg.Enabled {
		return
	}
	a.checkSunPopups(ctx, now, cfg)
	if !a.weather.due(cfg, now) {
		return
	}
	a.pollAir(ctx, now, cfg)
	hint, wait := a.weather.fetchHint(cfg, now)
	if wait {
		a.markWeatherPolled(now, src)
		a.popKeptWeather(ctx, now, src, cfg)
		return
	}
	res, err := a.weatherFetcher.fetch(ctx, cfg, hint)
	if err != nil {
		a.logger.Warn("weather fetch failed", "provider", cfg.Provider, "err", err)
		a.markWeatherFailed(now, src, retryAfterOf(err))
		return
	}
	if res.NotModified {
		if a.confirmWeather(now, src, res) {
			a.changes.notify(topicWeather)
			a.popKeptWeather(ctx, now, src, cfg)
		} else {
			a.markWeatherPolled(now, src)
		}
		return
	}
	obs := res.Obs
	obs.FetchedAt = now
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.source() != src {
		a.weather.mu.Unlock()
		return
	}
	a.weather.obs = obs
	a.weather.have = true
	a.weather.lastFetch = now
	a.weather.failures = 0
	a.weather.retryAt = time.Time{}
	a.weather.lastModified = res.LastModified
	a.weather.notBefore = res.Expires
	a.weather.mu.Unlock()
	a.changes.notify(topicWeather)
	a.popWeather(ctx, now, src, cfg, obs.current(now))
	a.nudgePomo()
}

func (a *App) popKeptWeather(ctx context.Context, now time.Time, src weatherSource, cfg WeatherConfig) {
	a.weather.mu.RLock()
	obs, have := a.weather.obs.current(now), a.weather.have && a.weather.source == src
	a.weather.mu.RUnlock()
	if have {
		a.popWeather(ctx, now, src, cfg, obs)
	}
}

func (a *App) popWeather(ctx context.Context, now time.Time, src weatherSource, cfg WeatherConfig, obs weatherObservation) {
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.source() != src {
		a.weather.mu.Unlock()
		return
	}
	prevCond := a.weather.prevCondition
	prevSevere := a.weather.prevSevere
	lastPopup := a.weather.lastPopupAt
	if lastPopup.IsZero() {
		lastPopup = now
		a.weather.lastPopupAt = now
	}
	a.weather.mu.Unlock()

	popped := a.evaluateWeatherPopup(ctx, now, obs, prevCond, prevSevere, lastPopup, cfg)

	a.weather.mu.Lock()
	a.weather.prevCondition = obs.Condition
	a.weather.prevSevere = obs.Severe
	if popped {
		a.weather.lastPopupAt = now
	}
	a.weather.mu.Unlock()
}

const weatherRetryBase = 2 * time.Minute

func (s *weatherStore) due(cfg WeatherConfig, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now.Before(s.retryAt) {
		return false
	}
	every := cfg.refreshEvery()
	if !s.have {
		every = weatherRetryDelay(s.failures, every)
	}
	return s.lastFetch.IsZero() || now.Sub(s.lastFetch) >= every
}

func (c WeatherConfig) refreshEvery() time.Duration {
	return time.Duration(c.RefreshMinutes) * time.Minute
}

func weatherRetryDelay(failures int, refresh time.Duration) time.Duration {
	d := weatherRetryBase
	for i := 1; i < failures && d < refresh; i++ {
		d *= 2
	}
	return min(d, refresh)
}

func (a *App) markWeatherFailed(now time.Time, src weatherSource, retryAfter time.Duration) {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if a.cfg.Load().Weather.source() != src {
		return
	}
	a.weather.lastFetch = now
	a.weather.failures++
	a.weather.retryAt = now.Add(retryAfter)
}

func (s *weatherStore) fetchHint(cfg WeatherConfig, now time.Time) (fetchHint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := fetchHint{LastModified: s.lastModified}
	if cfg.airWanted() && s.haveAir && s.air.TZKnown && now.Sub(s.air.FetchedAt) < weatherStaleAfter {
		h.TZOffsetSeconds, h.TZKnown = s.air.TZOffsetSeconds, true
	}
	return h, s.have && now.Before(s.notBefore)
}

func (a *App) markWeatherPolled(now time.Time, src weatherSource) {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if a.cfg.Load().Weather.source() == src {
		a.weather.lastFetch = now
	}
}

func (a *App) confirmWeather(now time.Time, src weatherSource, res fetchResult) bool {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if a.cfg.Load().Weather.source() != src || !a.weather.have {
		return false
	}
	a.weather.lastFetch = now
	a.weather.failures = 0
	a.weather.retryAt = time.Time{}
	a.weather.obs.FetchedAt = now
	if !res.Expires.IsZero() {
		a.weather.notBefore = res.Expires
		a.weather.obs.ValidUntil = res.Expires
	}
	return true
}

func (c WeatherConfig) airWanted() bool { return c.AirTileEnabled() || c.AirPopupThreshold > 0 }

func (s *weatherStore) airCurrent(cfg WeatherConfig, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	age := now.Sub(s.air.FetchedAt)
	return s.haveAir && age >= 0 && age < cfg.refreshEvery()
}

func (a *App) pollAir(ctx context.Context, now time.Time, cfg WeatherConfig) {
	if !cfg.airWanted() || a.weather.airCurrent(cfg, now) {
		return
	}
	obs, err := a.weatherFetcher.fetchAirQuality(ctx, cfg)
	if err != nil {
		a.logger.Warn("air-quality fetch failed", "err", err)
		return
	}
	obs.FetchedAt = now
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.location() != cfg.location() {
		a.weather.mu.Unlock()
		return
	}
	prev, havePrev := a.weather.prevAQI, a.weather.havePrevAQI
	a.weather.air = obs
	a.weather.haveAir = true
	a.weather.prevAQI = obs.AQI
	a.weather.havePrevAQI = true
	a.weather.mu.Unlock()

	t := float64(cfg.AirPopupThreshold)
	if cfg.AirPopupThreshold > 0 && obs.AQI >= t && (!havePrev || prev < t) {
		n := notice{app: "weather", kind: noticeAir, priority: noticeInterrupt,
			payload: render.AirPopupPayload(obs.AQI, cfg.PopupDurationSeconds)}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := a.coord.showNotice(cctx, n); err != nil {
			a.logger.Warn("air popup failed", "err", err)
		}
	}
}

func (a *App) evaluateWeatherPopup(ctx context.Context, now time.Time, obs weatherObservation, prevCond string, prevSevere bool, lastPopup time.Time, cfg WeatherConfig) bool {
	conditionChanged := prevCond != "" && prevCond != obs.Condition

	if cfg.SevereAlertEnabled() && obs.Severe && !prevSevere {
		sound := cfg.SevereSound
		if sound == "" {
			sound = defaultWeatherSevereSound
		}
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, sound)
		return true
	}
	if cfg.PopupOnChangeEnabled() && conditionChanged {
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, "")
		return true
	}
	if cfg.PopupIntervalMins() > 0 && !lastPopup.IsZero() &&
		now.Sub(lastPopup) >= time.Duration(cfg.PopupIntervalMins())*time.Minute {
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, "")
		return true
	}
	return false
}

const sunPopupGrace = 2 * time.Minute

func (a *App) checkSunPopups(ctx context.Context, now time.Time, cfg WeatherConfig) {
	if !cfg.SunPopupsEnabled() {
		return
	}
	wx := a.weather.state(cfg, now)
	if !wx.HaveSun {
		return
	}
	a.maybeFireSun(ctx, now, wx.Sunrise, true, wx.LocalDay, cfg, wx.Obs)
	a.maybeFireSun(ctx, now, wx.Sunset, false, wx.LocalDay, cfg, wx.Obs)
}

func (a *App) maybeFireSun(ctx context.Context, now, event time.Time, rising bool, today string, cfg WeatherConfig, obs weatherObservation) {
	if now.Before(event) || now.Sub(event) >= sunPopupGrace {
		return
	}
	a.weather.mu.Lock()
	done := &a.weather.sunsetDoneDay
	if rising {
		done = &a.weather.sunriseDoneDay
	}
	if *done == today {
		a.weather.mu.Unlock()
		return
	}
	*done = today
	a.weather.mu.Unlock()

	word := "SUNSET"
	if rising {
		word = "SUNRISE"
	}
	label := word + " " + sunClock(event, obs, cfg.Longitude)
	n := notice{app: "weather", kind: noticeSun, priority: noticeInterrupt,
		payload: render.SunPopupPayload(rising, label, cfg.PopupDurationSeconds)}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.coord.showNotice(cctx, n); err != nil {
		a.logger.Warn("sun popup failed", "err", err)
	}
}

func (a *App) sendWeatherPopup(ctx context.Context, obs weatherObservation, cfg WeatherConfig, durationSec int, sound string) {
	iconID := ""
	if cfg.UseNativeIcons {
		iconID = cfg.weatherIconID(obs.Condition)
	}
	n := notice{app: "weather", kind: noticeWeather, priority: noticeInterrupt,
		payload: render.WithOverlay(render.WeatherPopupPayload(obs.Condition, weatherLabel(obs, cfg), iconID, durationSec),
			weatherOverlay(obs, cfg))}
	if strings.Contains(sound, ":") {
		n.sound.rtttl = sound
	} else {
		n.sound.melody = sound
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.coord.showNotice(cctx, n); err != nil {
		a.logger.Warn("weather popup failed", "err", err)
	}
}

const weatherSettingsKey = "weather_json"

func (a *App) weatherSettingSpec() settingSpec[WeatherConfig] {
	return settingSpec[WeatherConfig]{
		key:          weatherSettingsKey,
		presentation: weatherPresentationKeys,
		view:         func(c Config) WeatherConfig { return c.Weather },
		apply: func(c *Config, w WeatherConfig) error {
			w.fillAbsent()
			if w.ForecastHours == 0 {
				w.ForecastHours = 24
			}
			if err := validateWeather(w); err != nil {
				return err
			}
			c.Weather = w
			return nil
		},
		after: func(Config) {
			a.nudgePomo()
			a.provisionIconsInBackground()
		},
	}
}
