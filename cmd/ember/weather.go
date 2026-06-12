package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

// WeatherConfig holds the weather widget's settings. Everything except the
// secret-free defaults is runtime-editable from the menu (persisted to the
// store, layered over the file config like Pomodoro). The widget fetches current
// conditions server-side from a free, keyless provider and renders them as a
// rotating tile + optional popups.
type WeatherConfig struct {
	Enabled              bool    `json:"enabled"`
	Provider             string  `json:"provider"` // "open-meteo" | "met-no"
	Latitude             float64 `json:"latitude"`
	Longitude            float64 `json:"longitude"`
	LocationName         string  `json:"location_name"`
	Units                string  `json:"units"`                  // "metric" | "imperial"
	RefreshMinutes       int     `json:"refresh_minutes"`        // poll cadence
	RotateInApps         bool    `json:"rotate_in_apps"`         // show the rotating tile
	ForecastTile         bool    `json:"forecast_tile"`          // show the separate hourly-forecast bar tile
	ForecastHours        int     `json:"forecast_hours"`         // hours shown in the strip/tile (6..24)
	SunPopups            bool    `json:"sun_popups"`             // popup at sunrise/sunset
	MoonPhase            bool    `json:"moon_phase"`             // show the moon phase on clear nights
	PopupIntervalMinutes int     `json:"popup_interval_minutes"` // 0 = no interval popups
	PopupDurationSeconds int     `json:"popup_duration_seconds"`
	PopupOnChange        bool    `json:"popup_on_change"` // popup when the condition changes
	SevereAlert          bool    `json:"severe_alert"`    // popup + sound on severe weather
	SevereSound          string  `json:"severe_sound"`    // RTTTL or device sound name; empty = default
	UseNativeIcons       bool    `json:"use_native_icons"`
	// IconIDs optionally overrides the per-condition native AWTRIX/LaMetric icon
	// ID used for popups (when UseNativeIcons is on). Keyed by condition bucket
	// ("clear"/"clouds"/"fog"/"rain"/"snow"/"storm"); an empty/absent entry falls
	// back to defaultWeatherIconIDs. Lets the user curate icons from the LaMetric
	// gallery (developer.lametric.com/icons) without a code change.
	IconIDs map[string]string `json:"icon_ids,omitempty"`
	// TileNativeIcons swaps the drawn 8×8 condition sprite on the rotating
	// weather/forecast tiles for the native animated icon (same IconIDs
	// mapping as popups). Digits/strip/bars stay drawn. Independent of
	// UseNativeIcons, which is popup-only.
	TileNativeIcons bool `json:"tile_native_icons"`
}

// weatherIconID resolves the native icon ID for a condition: the per-config
// override if set, else the built-in default.
func (c WeatherConfig) weatherIconID(cond string) string {
	if id := c.IconIDs[cond]; id != "" {
		return id
	}
	return defaultWeatherIconIDs[cond]
}

// defaultWeatherSevereSound is a short urgent RTTTL chime for severe-weather
// popups when no custom sound is configured (TC001 piezo is RTTTL-only).
const defaultWeatherSevereSound = "storm:d=4,o=5,b=160:8c6,8a,8c6,8a,8c6"

// defaultWeatherIconIDs maps a condition to an AWTRIX/LaMetric weather icon ID,
// used for popups (UseNativeIcons) and the rotating tiles (TileNativeIcons).
// These are widely-used gallery IDs (developer.lametric.com/icons); the device
// downloads one on first reference (briefly blank until cached), and the user
// can override any of them via WeatherConfig.IconIDs.
var defaultWeatherIconIDs = map[string]string{
	render.WeatherClear:  "1338",  // sunny
	render.WeatherClouds: "2286",  // partly cloudy
	render.WeatherFog:    "17056", // fog
	render.WeatherRain:   "72",    // rain
	render.WeatherSnow:   "2289",  // snow
	render.WeatherStorm:  "11428", // thunderstorm
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
	if c.PopupIntervalMinutes < 0 {
		c.PopupIntervalMinutes = 0
	} else if c.PopupIntervalMinutes == 0 {
		c.PopupIntervalMinutes = 120
	}
	if c.PopupDurationSeconds <= 0 {
		c.PopupDurationSeconds = 30
	}
	// Friendly defaults for the opt-in toggles, applied only at config LOAD (this
	// runs over the file config). The menu's explicit values are layered on top
	// afterwards by loadPersistedWeatherSettings, whose applyWeatherSettings does
	// NOT re-run applyDefaults — so a user can still turn these off (and set
	// popup_interval=0) and have it stick. Matches the Pomodoro convention of
	// seeding bool defaults without clobbering persisted runtime edits.
	if !c.RotateInApps {
		c.RotateInApps = true
	}
	if !c.ForecastTile {
		c.ForecastTile = true
	}
	if c.ForecastHours <= 0 {
		c.ForecastHours = 24
	} else if c.ForecastHours < 6 {
		c.ForecastHours = 6
	} else if c.ForecastHours > 24 {
		c.ForecastHours = 24
	}
	if !c.PopupOnChange {
		c.PopupOnChange = true
	}
	if !c.SevereAlert {
		c.SevereAlert = true
	}
	if !c.SunPopups {
		c.SunPopups = true
	}
	if !c.MoonPhase {
		c.MoonPhase = true
	}
}

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
	return nil
}

// weatherObservation is one resolved reading: a render condition bucket, the
// temperature in Celsius (converted per-units at render time), and whether the
// condition counts as severe (drives the alert popup).
type weatherObservation struct {
	Condition string
	TempC     float64
	Severe    bool
	FetchedAt time.Time
	// Hourly holds the next ~24 hourly temperatures (°C), ordered from the
	// current hour. Drives the compact strip and the forecast tile; nil when the
	// provider returned none (everything else still works).
	Hourly []float64
	// TZOffsetSeconds is the location's UTC offset (from Open-Meteo's timezone=auto);
	// TZKnown is false when the provider didn't supply one (e.g. MET Norway), in
	// which case sun labels fall back to the longitude approximation.
	TZOffsetSeconds int
	TZKnown         bool
}

// forecastFetchHours is how many hourly temps we ask providers for. The render
// layer slices to the configured ForecastHours; we over-fetch a fixed window so
// a config change doesn't require a refetch.
const forecastFetchHours = 24

// weatherStore holds the latest observation plus popup bookkeeping. All access
// goes through the mutex; the poller writes, the coordinator reads for the tile.
type weatherStore struct {
	mu        sync.RWMutex
	obs       weatherObservation
	have      bool
	lastFetch time.Time

	// Popup bookkeeping (poller-owned).
	prevCondition string
	prevSevere    bool
	lastPopupAt   time.Time
	// Sunrise/sunset popup dedupe: the UTC date ("2006-01-02") each last fired, so
	// a given day's event fires at most once.
	sunriseDoneDay string
	sunsetDoneDay  string
}

func newWeatherStore() *weatherStore { return &weatherStore{} }

func (s *weatherStore) current() (weatherObservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.obs, s.have
}

// ---- provider fetch ----

// weatherFetcher performs provider HTTP calls. Base URLs are fields so tests can
// point them at httptest servers.
type weatherFetcher struct {
	client        *http.Client
	openMeteoBase string
	metNoBase     string
	userAgent     string
}

func newWeatherFetcher() *weatherFetcher {
	return &weatherFetcher{
		client:        &http.Client{Timeout: 12 * time.Second},
		openMeteoBase: "https://api.open-meteo.com",
		metNoBase:     "https://api.met.no",
		userAgent:     "ember-weather/0.1 (github.com/tarakanof/ember)",
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
	req.Header.Set("User-Agent", wf.userAgent) // required by api.met.no; harmless elsewhere
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
	url := fmt.Sprintf("%s/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code&hourly=temperature_2m&forecast_hours=%d&timezone=auto",
		strings.TrimRight(wf.openMeteoBase, "/"), cfg.Latitude, cfg.Longitude, forecastFetchHours)
	var body struct {
		UTCOffsetSeconds int `json:"utc_offset_seconds"`
		Current          struct {
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
		} `json:"current"`
		Hourly struct {
			Temperature []float64 `json:"temperature_2m"`
		} `json:"hourly"`
	}
	if err := wf.getJSON(ctx, url, &body); err != nil {
		return weatherObservation{}, err
	}
	cond, severe := wmoCondition(body.Current.WeatherCode)
	hourly := body.Hourly.Temperature
	if len(hourly) > forecastFetchHours {
		hourly = hourly[:forecastFetchHours]
	}
	return weatherObservation{
		Condition: cond, TempC: body.Current.Temperature, Severe: severe, Hourly: hourly,
		TZOffsetSeconds: body.UTCOffsetSeconds, TZKnown: true,
	}, nil
}

func (wf *weatherFetcher) fetchMetNo(ctx context.Context, cfg WeatherConfig) (weatherObservation, error) {
	url := fmt.Sprintf("%s/weatherapi/locationforecast/2.0/compact?lat=%.4f&lon=%.4f",
		strings.TrimRight(wf.metNoBase, "/"), cfg.Latitude, cfg.Longitude)
	var body struct {
		Properties struct {
			Timeseries []struct {
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
	// The near-term timeseries entries are hourly (they widen to 6-hourly further
	// out); take the next forecastFetchHours as the hourly window.
	n := len(body.Properties.Timeseries)
	if n > forecastFetchHours {
		n = forecastFetchHours
	}
	hourly := make([]float64, n)
	for i := 0; i < n; i++ {
		hourly[i] = body.Properties.Timeseries[i].Data.Instant.Details.AirTemperature
	}
	return weatherObservation{Condition: cond, TempC: first.Data.Instant.Details.AirTemperature, Severe: severe, Hourly: hourly}, nil
}

// wmoCondition maps an Open-Meteo WMO weather code to a render condition bucket
// and a severe flag. https://open-meteo.com/en/docs (WMO Weather interpretation).
func wmoCondition(code int) (string, bool) {
	switch {
	case code == 0:
		return render.WeatherClear, false
	case code >= 1 && code <= 3:
		return render.WeatherClouds, false
	case code == 45 || code == 48:
		return render.WeatherFog, false
	case code >= 51 && code <= 57:
		return render.WeatherRain, false // drizzle
	case code >= 61 && code <= 67:
		return render.WeatherRain, code == 65 || code == 67 // heavy / freezing
	case code >= 71 && code <= 77:
		return render.WeatherSnow, code == 75 // heavy snowfall
	case code >= 80 && code <= 82:
		return render.WeatherRain, code == 82 // violent showers
	case code == 85 || code == 86:
		return render.WeatherSnow, code == 86
	case code >= 95:
		return render.WeatherStorm, true // thunderstorm
	default:
		return render.WeatherClouds, false
	}
}

// metSymbolCondition maps a MET Norway symbol_code (e.g. "heavyrainandthunder",
// "partlycloudy_day") to a render condition bucket + severe flag.
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

// ---- display helpers ----

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

// ---- poller ----

// StartWeather runs the weather poll loop until ctx is done. It ticks every
// minute, fetches no more often than RefreshMinutes, stores the observation, and
// fires change/interval/severe popups. A no-op while the feature is disabled.
func (a *App) StartWeather(ctx context.Context) {
	if a.weather == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.pollWeather(ctx, time.Now()) // initial attempt so the tile appears promptly
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollWeather(ctx, time.Now())
		}
	}
}

// pollWeather fetches when due and evaluates popups. Exported-for-tests via the
// (now) parameter so a fake clock can drive it deterministically.
func (a *App) pollWeather(ctx context.Context, now time.Time) {
	cfg := a.cfg.Load().Weather
	if !cfg.Enabled {
		return
	}
	// Sunrise/sunset popups are time-driven, not fetch-driven: check every tick
	// (this runs each minute) so they fire near the actual event, independent of
	// the provider refresh interval.
	a.checkSunPopups(ctx, now, cfg)
	// "Due" is gated on lastFetch (set on BOTH success and failure), not on
	// `have`: a provider that keeps failing at startup must still back off a full
	// refresh interval between attempts rather than retry every tick (api.met.no
	// throttles aggressive clients). lastFetch zero = never attempted → fetch now.
	a.weather.mu.RLock()
	due := a.weather.lastFetch.IsZero() || now.Sub(a.weather.lastFetch) >= time.Duration(cfg.RefreshMinutes)*time.Minute
	a.weather.mu.RUnlock()
	if !due {
		return
	}
	obs, err := a.weatherFetcher.fetch(ctx, cfg)
	if err != nil {
		a.logger.Warn("weather fetch failed", "provider", cfg.Provider, "err", err)
		a.weather.mu.Lock()
		a.weather.lastFetch = now // back off a full interval on failure
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
	// Seed the interval clock on the first-ever observation so a non-severe
	// startup doesn't immediately fire an interval popup — the first interval
	// popup then lands one full interval after launch, not at launch.
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
	a.nudgePomo() // prompt the coordinator to reconcile the tile promptly
}

// evaluateWeatherPopup decides whether to fire a popup and does so. Returns true
// when a popup was sent (so the caller can record lastPopupAt). Priority: severe
// transition (with sound) > condition change > interval elapsed.
func (a *App) evaluateWeatherPopup(ctx context.Context, now time.Time, obs weatherObservation, prevCond string, prevSevere bool, lastPopup time.Time, cfg WeatherConfig) bool {
	conditionChanged := prevCond != "" && prevCond != obs.Condition

	if cfg.SevereAlert && obs.Severe && !prevSevere {
		sound := cfg.SevereSound
		if sound == "" {
			sound = defaultWeatherSevereSound
		}
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, sound)
		return true
	}
	if cfg.PopupOnChange && conditionChanged {
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, "")
		return true
	}
	if cfg.PopupIntervalMinutes > 0 && !lastPopup.IsZero() &&
		now.Sub(lastPopup) >= time.Duration(cfg.PopupIntervalMinutes)*time.Minute {
		a.sendWeatherPopup(ctx, obs, cfg, cfg.PopupDurationSeconds, "")
		return true
	}
	return false
}

// sunPopupGrace is how soon after a sunrise/sunset instant we still fire the
// popup. The poll runs each minute, so a ~2-min window reliably catches the
// event without firing for an event that already passed (e.g. at startup).
const sunPopupGrace = 2 * time.Minute

// checkSunPopups fires a sunrise/sunset popup when `now` has just crossed the
// event for the configured location, at most once per UTC day per event.
func (a *App) checkSunPopups(ctx context.Context, now time.Time, cfg WeatherConfig) {
	if !cfg.SunPopups {
		return
	}
	if cfg.Latitude == 0 && cfg.Longitude == 0 {
		return // no location set
	}
	sunrise, sunset, ok := sunTimes(cfg.Latitude, cfg.Longitude, now)
	if !ok {
		return // polar day/night — no event today
	}
	// Prefer the location's real UTC offset (from the last Open-Meteo fetch) for the
	// label; fall back to the longitude approximation when unknown (e.g. MET Norway).
	a.weather.mu.RLock()
	tzKnown, tzOff := a.weather.obs.TZKnown, a.weather.obs.TZOffsetSeconds
	a.weather.mu.RUnlock()

	today := now.UTC().Format("2006-01-02")
	a.maybeFireSun(ctx, now, sunrise, true, today, cfg, tzKnown, tzOff)
	a.maybeFireSun(ctx, now, sunset, false, today, cfg, tzKnown, tzOff)
}

// sunClock formats an event instant for the popup label, using the known UTC
// offset when available, else the longitude approximation.
func sunClock(event time.Time, cfg WeatherConfig, tzKnown bool, tzOff int) string {
	if tzKnown {
		return event.UTC().Add(time.Duration(tzOff) * time.Second).Format("15:04")
	}
	return localClock(event, cfg.Longitude)
}

func (a *App) maybeFireSun(ctx context.Context, now, event time.Time, rising bool, today string, cfg WeatherConfig, tzKnown bool, tzOff int) {
	if now.Before(event) || now.Sub(event) >= sunPopupGrace {
		return // not in the firing window
	}
	a.weather.mu.Lock()
	done := &a.weather.sunsetDoneDay
	if rising {
		done = &a.weather.sunriseDoneDay
	}
	if *done == today {
		a.weather.mu.Unlock()
		return // already fired today
	}
	*done = today
	a.weather.mu.Unlock()

	word := "SUNSET"
	if rising {
		word = "SUNRISE"
	}
	label := word + " " + sunClock(event, cfg, tzKnown, tzOff)
	payload := render.SunPopupPayload(rising, label, cfg.PopupDurationSeconds)
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
	payload := render.WeatherPopupPayload(obs.Condition, weatherLabel(obs, cfg), iconID, durationSec)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.publisher.Notify(cctx, payload); err != nil {
		a.logger.Warn("weather popup failed", "err", err)
		return
	}
	// Chime separately: the firmware drops a notification's own sound when it also
	// draws/uses an icon, so play the severe-alert sound via /api/rtttl (an RTTTL
	// string, detected by its ':' separators) or /api/sound (a device melody name).
	if sound != "" {
		var err error
		if strings.Contains(sound, ":") {
			err = a.publisher.PlayRTTTL(cctx, sound)
		} else {
			err = a.publisher.PlaySound(cctx, sound)
		}
		if err != nil {
			a.logger.Warn("weather chime failed", "err", err)
		}
	}
}

// ---- config persistence (mirrors the Pomodoro store pattern) ----

const weatherSettingsKey = "weather_json"

func (a *App) applyWeatherSettings(cfg WeatherConfig) error {
	// NB: do NOT call cfg.applyDefaults() here. This is the runtime write/persist
	// path (menu PUT + loadPersistedWeatherSettings); re-defaulting would force
	// the opt-in toggles back on and rewrite popup_interval=0 → 120, making them
	// impossible to disable. The menu always sends a complete config, so we
	// validate verbatim (mirrors applyPomodoroSettings).
	if err := validateWeather(cfg); err != nil {
		return err
	}
	cur := *a.cfg.Load()
	cur.Weather = cfg
	a.cfg.Store(&cur)
	if a.store != nil {
		if blob, err := json.Marshal(cfg); err == nil {
			if err := a.store.PutSetting(weatherSettingsKey, string(blob)); err != nil {
				a.logger.Warn("weather settings persist failed", "err", err)
			}
		}
	}
	a.nudgePomo()
	return nil
}

func (a *App) loadPersistedWeatherSettings() {
	if a.store == nil {
		return
	}
	blob, ok, err := a.store.GetSetting(weatherSettingsKey)
	if err != nil || !ok {
		return
	}
	var cfg WeatherConfig
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		a.logger.Warn("weather persisted settings parse failed", "err", err)
		return
	}
	if err := a.applyWeatherSettings(cfg); err != nil {
		a.logger.Warn("weather persisted settings invalid, ignoring", "err", err)
	}
}
