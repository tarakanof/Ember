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

// WeatherConfig holds the weather widget's settings.
type WeatherConfig struct {
	Enabled bool `json:"enabled"`
	// Provider is "open-meteo" or "met-no".
	Provider     string  `json:"provider"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	LocationName string  `json:"location_name"`
	// Units is "metric" or "imperial".
	Units string `json:"units"`
	// RefreshMinutes is the poll cadence.
	RefreshMinutes int `json:"refresh_minutes"`
	// RotateInApps shows the rotating tile.
	RotateInApps *bool `json:"rotate_in_apps"`
	// ForecastTile shows the separate hourly-forecast bar tile.
	ForecastTile *bool `json:"forecast_tile"`
	// ForecastHours is the number of hours shown in the strip and tile (1..24).
	ForecastHours int `json:"forecast_hours"`
	// SunPopups pops up at sunrise and sunset.
	SunPopups *bool `json:"sun_popups"`
	// MoonPhase shows the moon phase on clear nights.
	MoonPhase *bool `json:"moon_phase"`
	// PopupIntervalMinutes is the interval-popup cadence (0 = none).
	PopupIntervalMinutes *int `json:"popup_interval_minutes"`
	PopupDurationSeconds int  `json:"popup_duration_seconds"`
	// PopupOnChange pops up when the condition changes.
	PopupOnChange *bool `json:"popup_on_change"`
	// SevereAlert pops up with a sound on severe weather.
	SevereAlert *bool `json:"severe_alert"`
	// SevereSound is an RTTTL or device sound name; empty means the default.
	SevereSound    string `json:"severe_sound"`
	UseNativeIcons bool   `json:"use_native_icons"`
	// IconIDs overrides the per-condition native icon ID used for popups, keyed by condition bucket; an empty entry falls back to the default.
	IconIDs map[string]string `json:"icon_ids,omitempty"`
	// TileNativeIcons swaps the drawn condition sprite on the rotating tiles for the native animated icon; independent of the popup-only UseNativeIcons.
	TileNativeIcons bool `json:"tile_native_icons"`
	// AirTile shows the rotating air-quality tile, always sourced from Open-Meteo regardless of Provider.
	AirTile *bool `json:"air_tile"`
	// AirPopupThreshold fires a popup when the European AQI rises across this value (edge-triggered); 0 disables it.
	AirPopupThreshold int `json:"air_popup_threshold"`
	// Overlay lets the clock animate current precipitation over the conditions tile and popups (default on).
	Overlay *bool `json:"overlay"`
}

func boolPtr(b bool) *bool { return &b }
func intPtr(v int) *int    { return &v }

const defaultWeatherPopupIntervalMinutes = 120

// RotateInAppsEnabled reports the rotate_in_apps toggle, defaulting to on when absent.
func (c WeatherConfig) RotateInAppsEnabled() bool  { return c.RotateInApps == nil || *c.RotateInApps }
func (c WeatherConfig) ForecastTileEnabled() bool  { return c.ForecastTile == nil || *c.ForecastTile }
func (c WeatherConfig) SunPopupsEnabled() bool     { return c.SunPopups == nil || *c.SunPopups }
func (c WeatherConfig) MoonPhaseEnabled() bool     { return c.MoonPhase == nil || *c.MoonPhase }
func (c WeatherConfig) PopupOnChangeEnabled() bool { return c.PopupOnChange == nil || *c.PopupOnChange }
func (c WeatherConfig) SevereAlertEnabled() bool   { return c.SevereAlert == nil || *c.SevereAlert }
func (c WeatherConfig) AirTileEnabled() bool       { return c.AirTile == nil || *c.AirTile }
func (c WeatherConfig) OverlayEnabled() bool       { return c.Overlay == nil || *c.Overlay }

// PopupIntervalMins resolves the interval-popup cadence: nil → default 120, explicit 0 → off, negatives clamp to 0.
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
	Condition string
	// ConditionCode is the provider's raw code: the WMO code for Open-Meteo, the symbol_code for MET Norway.
	ConditionCode string
	TempC         float64
	Severe        bool
	FetchedAt     time.Time
	// HourlyStart is the time of Hourly[0] as the provider stamped it; zero when unsaid.
	HourlyStart time.Time
	// Hourly holds the next ~24 hourly temperatures (°C) from the current hour; nil when the provider returned none.
	Hourly []float64
	// Overlay is the clock weather overlay matching current conditions, empty when nothing is falling.
	Overlay string
	// TZOffsetSeconds is the location's UTC offset from Open-Meteo; TZKnown is false when the provider supplied none, and sun labels then fall back to longitude.
	TZOffsetSeconds int
	TZKnown         bool
}

const forecastFetchHours = 24

const airFetchHours = 24

type airObservation struct {
	AQI       float64
	PM25      float64
	PM10      float64
	HourlyAQI []float64
	// HourlyStart is the time of HourlyAQI[0]; zero when unknown.
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

// StartWeather runs the weather poll loop until ctx is done.
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
	sunrise, sunset, ok := sunTimes(cfg.Latitude, cfg.Longitude, now)
	if !ok {
		return
	}
	a.weather.mu.RLock()
	tzKnown, tzOff := a.weather.obs.TZKnown, a.weather.obs.TZOffsetSeconds
	a.weather.mu.RUnlock()

	today := now.UTC().Format("2006-01-02")
	a.maybeFireSun(ctx, now, sunrise, true, today, cfg, tzKnown, tzOff)
	a.maybeFireSun(ctx, now, sunset, false, today, cfg, tzKnown, tzOff)
}

func sunClock(event time.Time, cfg WeatherConfig, tzKnown bool, tzOff int) string {
	if tzKnown {
		return event.UTC().Add(time.Duration(tzOff) * time.Second).Format("15:04")
	}
	return localClock(event, cfg.Longitude)
}

func (a *App) maybeFireSun(ctx context.Context, now, event time.Time, rising bool, today string, cfg WeatherConfig, tzKnown bool, tzOff int) {
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
	label := word + " " + sunClock(event, cfg, tzKnown, tzOff)
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
