package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
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
	FetchedAt       time.Time
	HourlyStart     time.Time
	Hourly          []float64
	Overlay         string
	TZOffsetSeconds int
	TZKnown         bool
}

const forecastFetchHours = 24

const airFetchHours = 24

type airObservation struct {
	AQI         float64
	PM25        float64
	PM10        float64
	HourlyAQI   []float64
	HourlyStart time.Time
	FetchedAt   time.Time
}

type weatherStore struct {
	mu        sync.RWMutex
	obs       weatherObservation
	have      bool
	lastFetch time.Time

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

func (s *weatherStore) current() (weatherObservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.obs, s.have
}

func (s *weatherStore) currentAir() (airObservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.air, s.haveAir
}

type weatherFetcher struct {
	client         *http.Client
	openMeteoBase  string
	metNoBase      string
	airQualityBase string
	userAgent      string
}

func newWeatherFetcher() *weatherFetcher {
	return &weatherFetcher{
		client:         &http.Client{Timeout: 12 * time.Second},
		openMeteoBase:  "https://api.open-meteo.com",
		metNoBase:      "https://api.met.no",
		airQualityBase: "https://air-quality-api.open-meteo.com",
		userAgent:      "ember-weather/0.1 (github.com/tarakanof/ember)",
	}
}

func (wf *weatherFetcher) fetch(ctx context.Context, cfg WeatherConfig) (weatherObservation, error) {
	switch cfg.Provider {
	case "met-no":
		return wf.fetchMetNo(ctx, cfg)
	default:
		return wf.fetchOpenMeteo(ctx, cfg)
	}
}

func (wf *weatherFetcher) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", wf.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := wf.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("weather http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (wf *weatherFetcher) fetchOpenMeteo(ctx context.Context, cfg WeatherConfig) (weatherObservation, error) {
	url := fmt.Sprintf("%s/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code&hourly=temperature_2m&forecast_hours=%d&timezone=auto&timeformat=unixtime",
		strings.TrimRight(wf.openMeteoBase, "/"), cfg.Latitude, cfg.Longitude, forecastFetchHours)
	var body struct {
		UTCOffsetSeconds int `json:"utc_offset_seconds"`
		Current          struct {
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
		} `json:"current"`
		Hourly struct {
			Time        []int64   `json:"time"`
			Temperature []float64 `json:"temperature_2m"`
		} `json:"hourly"`
	}
	if err := wf.getJSON(ctx, url, &body); err != nil {
		return weatherObservation{}, err
	}
	var start time.Time
	if len(body.Hourly.Time) > 0 {
		start = time.Unix(body.Hourly.Time[0], 0)
	}
	cond, severe := wmoCondition(body.Current.WeatherCode)
	hourly := body.Hourly.Temperature
	if len(hourly) > forecastFetchHours {
		hourly = hourly[:forecastFetchHours]
	}
	return weatherObservation{
		Condition: cond, ConditionCode: strconv.Itoa(body.Current.WeatherCode),
		TempC: body.Current.Temperature, Severe: severe, Hourly: hourly, HourlyStart: start,
		Overlay:         wmoOverlay(body.Current.WeatherCode),
		TZOffsetSeconds: body.UTCOffsetSeconds, TZKnown: true,
	}, nil
}

func (wf *weatherFetcher) fetchMetNo(ctx context.Context, cfg WeatherConfig) (weatherObservation, error) {
	url := fmt.Sprintf("%s/weatherapi/locationforecast/2.0/compact?lat=%.4f&lon=%.4f",
		strings.TrimRight(wf.metNoBase, "/"), cfg.Latitude, cfg.Longitude)
	var body struct {
		Properties struct {
			Timeseries []struct {
				Time time.Time `json:"time"`
				Data struct {
					Instant struct {
						Details struct {
							AirTemperature float64 `json:"air_temperature"`
						} `json:"details"`
					} `json:"instant"`
					Next1Hours struct {
						Summary struct {
							SymbolCode string `json:"symbol_code"`
						} `json:"summary"`
					} `json:"next_1_hours"`
				} `json:"data"`
			} `json:"timeseries"`
		} `json:"properties"`
	}
	if err := wf.getJSON(ctx, url, &body); err != nil {
		return weatherObservation{}, err
	}
	if len(body.Properties.Timeseries) == 0 {
		return weatherObservation{}, errors.New("met-no: empty timeseries")
	}
	first := body.Properties.Timeseries[0]
	cond, severe := metSymbolCondition(first.Data.Next1Hours.Summary.SymbolCode)
	n := len(body.Properties.Timeseries)
	if n > forecastFetchHours {
		n = forecastFetchHours
	}
	hourly := make([]float64, n)
	for i := 0; i < n; i++ {
		hourly[i] = body.Properties.Timeseries[i].Data.Instant.Details.AirTemperature
	}
	return weatherObservation{
		Condition: cond, ConditionCode: first.Data.Next1Hours.Summary.SymbolCode,
		TempC: first.Data.Instant.Details.AirTemperature, Severe: severe,
		Hourly: hourly, HourlyStart: first.Time,
		Overlay: metSymbolOverlay(first.Data.Next1Hours.Summary.SymbolCode),
	}, nil
}

func (wf *weatherFetcher) fetchAirQuality(ctx context.Context, cfg WeatherConfig) (airObservation, error) {
	url := fmt.Sprintf("%s/v1/air-quality?latitude=%.4f&longitude=%.4f&current=european_aqi,pm2_5,pm10&hourly=european_aqi&forecast_hours=%d&timezone=auto&timeformat=unixtime",
		strings.TrimRight(wf.airQualityBase, "/"), cfg.Latitude, cfg.Longitude, airFetchHours)
	var body struct {
		Current struct {
			AQI  float64 `json:"european_aqi"`
			PM25 float64 `json:"pm2_5"`
			PM10 float64 `json:"pm10"`
		} `json:"current"`
		Hourly struct {
			Time []int64   `json:"time"`
			AQI  []float64 `json:"european_aqi"`
		} `json:"hourly"`
	}
	if err := wf.getJSON(ctx, url, &body); err != nil {
		return airObservation{}, err
	}
	hourly := body.Hourly.AQI
	if len(hourly) > airFetchHours {
		hourly = hourly[:airFetchHours]
	}
	var start time.Time
	if len(body.Hourly.Time) > 0 {
		start = time.Unix(body.Hourly.Time[0], 0)
	}
	return airObservation{AQI: body.Current.AQI, PM25: body.Current.PM25, PM10: body.Current.PM10, HourlyAQI: hourly, HourlyStart: start}, nil
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
	a.ensureNativeIcons(ctx)
	a.pollWeather(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollWeather(ctx, time.Now())
		}
	}
}

func (a *App) pollWeather(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Weather
	if !cfg.Enabled {
		return
	}
	a.checkSunPopups(ctx, now, cfg)
	a.weather.mu.RLock()
	due := a.weather.lastFetch.IsZero() || now.Sub(a.weather.lastFetch) >= time.Duration(cfg.RefreshMinutes)*time.Minute
	a.weather.mu.RUnlock()
	if !due {
		return
	}
	a.pollAir(ctx, now, cfg)
	obs, err := a.weatherFetcher.fetch(ctx, cfg)
	if err != nil {
		a.logger.Warn("weather fetch failed", "provider", cfg.Provider, "err", err)
		a.weather.mu.Lock()
		a.weather.lastFetch = now
		a.weather.mu.Unlock()
		return
	}
	obs.FetchedAt = now
	a.weather.mu.Lock()
	a.weather.obs = obs
	a.weather.have = true
	a.weather.lastFetch = now
	prevCond := a.weather.prevCondition
	prevSevere := a.weather.prevSevere
	lastPopup := a.weather.lastPopupAt
	if lastPopup.IsZero() {
		lastPopup = now
		a.weather.lastPopupAt = now
	}
	a.weather.mu.Unlock()
	a.changes.notify(topicWeather)

	popped := a.evaluateWeatherPopup(ctx, now, obs, prevCond, prevSevere, lastPopup, cfg)

	a.weather.mu.Lock()
	a.weather.prevCondition = obs.Condition
	a.weather.prevSevere = obs.Severe
	if popped {
		a.weather.lastPopupAt = now
	}
	a.weather.mu.Unlock()
	a.nudgePomo()
}

func (a *App) pollAir(ctx context.Context, now time.Time, cfg WeatherConfig) {
	if !cfg.AirTileEnabled() && cfg.AirPopupThreshold <= 0 {
		return
	}
	obs, err := a.weatherFetcher.fetchAirQuality(ctx, cfg)
	if err != nil {
		a.logger.Warn("air-quality fetch failed", "err", err)
		return
	}
	obs.FetchedAt = now
	a.weather.mu.Lock()
	prev, havePrev := a.weather.prevAQI, a.weather.havePrevAQI
	a.weather.air = obs
	a.weather.haveAir = true
	a.weather.prevAQI = obs.AQI
	a.weather.havePrevAQI = true
	a.weather.mu.Unlock()

	t := float64(cfg.AirPopupThreshold)
	if cfg.AirPopupThreshold > 0 && obs.AQI >= t && (!havePrev || prev < t) {
		payload := render.AirPopupPayload(obs.AQI, cfg.PopupDurationSeconds)
		payload["name"] = notifyNameAirPopup
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := a.publisher.Notify(cctx, payload); err != nil {
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
	if cfg.Latitude == 0 && cfg.Longitude == 0 {
		return
	}
	a.weather.mu.RLock()
	obs := a.weather.obs
	a.weather.mu.RUnlock()
	sunrise, sunset, ok := localSunTimes(cfg.Latitude, cfg.Longitude, now, obs)
	if !ok {
		return
	}
	today := localDay(now, obs, cfg.Longitude)
	a.maybeFireSun(ctx, now, sunrise, true, today, cfg, obs)
	a.maybeFireSun(ctx, now, sunset, false, today, cfg, obs)
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
	payload := render.SunPopupPayload(rising, label, cfg.PopupDurationSeconds)
	payload["name"] = notifyNameSunPopup
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(cctx, payload); err != nil {
		a.logger.Warn("sun popup failed", "err", err)
	}
}

func (a *App) sendWeatherPopup(ctx context.Context, obs weatherObservation, cfg WeatherConfig, durationSec int, sound string) {
	iconID := ""
	if cfg.UseNativeIcons {
		iconID = cfg.weatherIconID(obs.Condition)
	}
	payload := render.WithOverlay(render.WeatherPopupPayload(obs.Condition, weatherLabel(obs, cfg), iconID, durationSec),
		weatherOverlay(obs, cfg))
	payload["name"] = notifyNameWeatherPopup
	if sound != "" {
		if strings.Contains(sound, ":") {
			payload["soundRtttl"] = sound
		} else {
			payload["sound"] = sound
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(cctx, payload); err != nil {
		a.logger.Warn("weather popup failed", "err", err)
	}
}

const weatherSettingsKey = "weather_json"

func (a *App) weatherSettingSpec() settingSpec[WeatherConfig] {
	return settingSpec[WeatherConfig]{
		key:  weatherSettingsKey,
		view: func(c Config) WeatherConfig { return c.Weather },
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
			go a.ensureNativeIcons(context.Background())
		},
	}
}
