package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/weather"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(v int) *int    { return &v }

const defaultWeatherSevereSound = "storm:d=4,o=5,b=160:8c6,8a,8c6,8a,8c6"

var weatherIconIDPattern = regexp.MustCompile(`^[0-9]{1,10}$`)

func validateWeather(c weather.Config) error {
	if !slices.Contains(weather.Providers, c.Provider) {
		return fmt.Errorf("weather.provider %q must be one of %s", c.Provider, strings.Join(weather.Providers, ", "))
	}
	if c.OpenMeteoModel != "" && !slices.Contains(openMeteoModels, c.OpenMeteoModel) {
		return fmt.Errorf("weather.open_meteo_model %q must be one of %s", c.OpenMeteoModel, strings.Join(openMeteoModels, ", "))
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

type weatherStore struct {
	mu        sync.RWMutex
	source    weather.Source
	obs       weather.Observation
	have      bool
	lastFetch time.Time
	failures  int
	retryAt   time.Time

	lastModified string
	notBefore    time.Time

	prevCondition string
	prevSevere    bool
	lastPopupAt   time.Time
	sunPopups     sunPopupLog

	quotas map[string]*providerQuota

	snapshotLoaded bool

	air         weather.AirObservation
	haveAir     bool
	airTriedAt  time.Time
	airFailures int
	airRetryAt  time.Time
	prevAQI     float64
	havePrevAQI bool
}

func newWeatherStore() *weatherStore { return &weatherStore{} }

func (s *weatherStore) serves(src weather.Source) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.source == src
}

func (s *weatherStore) switchSource(src weather.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.source == src {
		return
	}
	if s.source.Loc != src.Loc {
		s.air, s.haveAir, s.havePrevAQI = weather.AirObservation{}, false, false
		s.airTriedAt, s.airFailures, s.airRetryAt = time.Time{}, 0, time.Time{}
	}
	s.source = src
	s.obs = weather.Observation{}
	s.have = false
	s.lastFetch = time.Time{}
	s.failures = 0
	s.retryAt = time.Time{}
	s.lastModified = ""
	s.notBefore = time.Time{}
	s.prevCondition = ""
}

func weatherOverlay(obs weather.Observation, cfg weather.Config) string {
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

func weatherLabel(obs weather.Observation, cfg weather.Config) string {
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
		if !a.weather.serves(a.cfg.Load().Weather.Source()) {
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
	entered := time.Now()
	cfg := a.cfg.Load().Weather
	src := cfg.Source()
	a.weather.switchSource(src)
	if !cfg.Enabled {
		return
	}
	available := a.weatherFetcher.ProviderAvailable(cfg.Provider)
	if available {
		a.restoreWeatherSnapshot(src, now)
	}
	a.checkSunPopups(ctx, now, cfg)
	a.pollAir(ctx, now, cfg)
	if !available {
		return
	}
	a.loadWeatherQuota(cfg, now)
	if !a.weather.due(cfg, now) {
		return
	}
	hint, wait := a.weather.fetchHint(cfg, now)
	if wait {
		a.markWeatherPolled(now, src)
		a.popKeptWeather(ctx, now, src, cfg)
		return
	}
	at := now.Add(time.Since(entered))
	if !a.weather.reserveQuota(cfg, at) {
		return
	}
	a.saveWeatherQuota(cfg)
	started := time.Now()
	res, err := a.weatherFetcher.Fetch(ctx, cfg, hint)
	if err != nil {
		a.logger.Warn("weather fetch failed", "provider", cfg.Provider, "model", cfg.Model(), "err", err)
		a.markWeatherFailedAfter(now, at.Add(time.Since(started)), src, retryAfterOf(err), statusOf(err) == http.StatusTooManyRequests)
		a.saveWeatherQuota(cfg)
		return
	}
	if res.NotModified {
		if a.confirmWeather(now, src, cfg, res) {
			a.saveWeatherSnapshot(src)
			a.changes.notify(topicWeather)
			a.popKeptWeather(ctx, now, src, cfg)
		} else {
			a.markWeatherPolled(now, src)
		}
		return
	}
	obs := res.Obs
	obs.FetchedAt = now
	obs.ValidUntil = res.ExpiresAt(now, cfg.MaxProviderFreshness())
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.Source() != src {
		a.weather.mu.Unlock()
		return
	}
	a.weather.obs = obs
	a.weather.have = true
	a.weather.lastFetch = now
	a.weather.failures = 0
	a.weather.retryAt = time.Time{}
	a.weather.lastModified = res.LastModified
	a.weather.notBefore = obs.ValidUntil
	a.weather.mu.Unlock()
	a.saveWeatherSnapshot(src)
	a.changes.notify(topicWeather)
	a.popWeather(ctx, now, src, cfg, obs.Current(now))
	a.nudgePomo()
}

func (a *App) popKeptWeather(ctx context.Context, now time.Time, src weather.Source, cfg weather.Config) {
	a.weather.mu.RLock()
	obs, have := a.weather.obs.Current(now), a.weather.have && a.weather.source == src
	a.weather.mu.RUnlock()
	if have {
		a.popWeather(ctx, now, src, cfg, obs)
	}
}

func (a *App) popWeather(ctx context.Context, now time.Time, src weather.Source, cfg weather.Config, obs weather.Observation) {
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.Source() != src {
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

func (s *weatherStore) due(cfg weather.Config, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now.Before(s.retryAt) {
		return false
	}
	if floor := cfg.MinFetchInterval(); floor > 0 && !s.quotas[cfg.Provider].open(floor, now) {
		return false
	}
	every := cfg.RefreshEvery()
	if !s.have {
		every = max(weatherRetryDelay(s.failures, every), cfg.MinFetchInterval())
	}
	return s.lastFetch.IsZero() || now.Sub(s.lastFetch) >= every
}

func weatherRetryDelay(failures int, refresh time.Duration) time.Duration {
	d := weatherRetryBase
	for i := 1; i < failures && d < refresh; i++ {
		d *= 2
	}
	return min(d, refresh)
}

func (a *App) markWeatherFailedAfter(now, received time.Time, src weather.Source, retryAfter time.Duration, durable bool) {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if (weather.Config{Provider: src.Provider}).MinFetchInterval() > 0 && retryAfter > 0 {
		q := a.weather.quota(src.Provider)
		until := received.Add(min(retryAfter, weather.RetryAfterMax))
		q.retryAt = later(q.retryAt, until)
		if durable {
			q.savedRetryAt = later(q.savedRetryAt, until)
		}
	}
	cfg := a.cfg.Load().Weather
	if cfg.Source() != src {
		return
	}
	a.weather.lastFetch = now
	a.weather.failures++
	a.weather.retryAt = now.Add(min(retryAfter, cfg.RetryAfterCap()))
}

func (s *weatherStore) fetchHint(cfg weather.Config, now time.Time) (weather.FetchHint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := weather.FetchHint{LastModified: s.lastModified}
	if cfg.AirWanted() && s.haveAir && s.air.TZKnown && now.Sub(s.air.FetchedAt) < weatherStaleAfter {
		h.TZOffsetSeconds, h.TZKnown, h.TZName = s.air.TZOffsetSeconds, true, s.air.TZName
	}
	return h, s.have && now.Before(s.notBefore)
}

func (a *App) markWeatherPolled(now time.Time, src weather.Source) {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if a.cfg.Load().Weather.Source() == src {
		a.weather.lastFetch = now
	}
}

func (a *App) confirmWeather(now time.Time, src weather.Source, cfg weather.Config, res weather.FetchResult) bool {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	if a.cfg.Load().Weather.Source() != src || !a.weather.have {
		return false
	}
	a.weather.lastFetch = now
	a.weather.failures = 0
	a.weather.retryAt = time.Time{}
	a.weather.obs.FetchedAt = now
	if exp := res.ExpiresAt(now, cfg.MaxProviderFreshness()); !exp.IsZero() {
		a.weather.notBefore = exp
		a.weather.obs.ValidUntil = exp
	}
	return true
}

func (s *weatherStore) airDue(cfg weather.Config, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	age := now.Sub(s.air.FetchedAt)
	if s.haveAir && age >= 0 && age < cfg.RefreshEvery() {
		return false
	}
	if now.Before(s.airRetryAt) {
		return false
	}
	wait := weatherRetryDelay(s.airFailures, cfg.RefreshEvery())
	if tried := now.Sub(s.airTriedAt); !s.airTriedAt.IsZero() && tried >= 0 && tried < wait {
		return false
	}
	s.airTriedAt = now
	return true
}

func (a *App) markAirFailed(now time.Time, loc weather.Location, retryAfter time.Duration) {
	a.weather.mu.Lock()
	defer a.weather.mu.Unlock()
	cfg := a.cfg.Load().Weather
	if cfg.Location() != loc {
		return
	}
	a.weather.airFailures++
	a.weather.airRetryAt = now.Add(min(retryAfter, cfg.RefreshEvery()))
}

func (a *App) pollAir(ctx context.Context, now time.Time, cfg weather.Config) {
	if !cfg.AirWanted() || !a.weather.airDue(cfg, now) {
		return
	}
	obs, err := a.weatherFetcher.FetchAirQuality(ctx, cfg)
	if err != nil {
		a.logger.Warn("air-quality fetch failed", "err", err)
		a.markAirFailed(now, cfg.Location(), retryAfterOf(err))
		return
	}
	obs.FetchedAt = now
	a.weather.mu.Lock()
	if a.cfg.Load().Weather.Location() != cfg.Location() {
		a.weather.mu.Unlock()
		return
	}
	prev, havePrev := a.weather.prevAQI, a.weather.havePrevAQI
	a.weather.air = obs
	a.weather.haveAir = true
	a.weather.airFailures = 0
	a.weather.airRetryAt = time.Time{}
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

func (a *App) evaluateWeatherPopup(ctx context.Context, now time.Time, obs weather.Observation, prevCond string, prevSevere bool, lastPopup time.Time, cfg weather.Config) bool {
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

type sunPopupLog struct{ rise, set time.Time }

func (l *sunPopupLog) due(evs []weather.SunEvent, now time.Time) []weather.SunEvent {
	var out []weather.SunEvent
	for _, ev := range evs {
		if now.Before(ev.At) || now.Sub(ev.At) >= sunPopupGrace {
			continue
		}
		last := &l.set
		if ev.Rise {
			last = &l.rise
		}
		if ev.At.Equal(*last) {
			continue
		}
		*last = ev.At
		out = append(out, ev)
	}
	return out
}

func (a *App) checkSunPopups(ctx context.Context, now time.Time, cfg weather.Config) {
	if !cfg.SunPopupsEnabled() {
		return
	}
	wx := a.weather.state(cfg, now)
	if !wx.HaveLocation {
		return
	}
	evs := weather.SunEvents(wx.Lat, wx.Lon, now)
	a.weather.mu.Lock()
	due := a.weather.sunPopups.due(evs, now)
	a.weather.mu.Unlock()
	for _, ev := range due {
		a.fireSunPopup(ctx, ev.At, ev.Rise, cfg, wx.Obs)
	}
}

func (a *App) fireSunPopup(ctx context.Context, event time.Time, rising bool, cfg weather.Config, obs weather.Observation) {
	word := "SUNSET"
	if rising {
		word = "SUNRISE"
	}
	label := word + " " + weather.SunClock(event, obs, cfg.Longitude)
	n := notice{app: "weather", kind: noticeSun, priority: noticeInterrupt,
		payload: render.SunPopupPayload(rising, label, cfg.PopupDurationSeconds)}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.coord.showNotice(cctx, n); err != nil {
		a.logger.Warn("sun popup failed", "err", err)
	}
}

func (a *App) sendWeatherPopup(ctx context.Context, obs weather.Observation, cfg weather.Config, durationSec int, sound string) {
	iconID := ""
	if cfg.UseNativeIcons {
		iconID = cfg.IconID(obs.Condition)
	}
	n := notice{app: "weather", kind: noticeWeather, priority: noticeInterrupt,
		payload: render.WithOverlay(render.WeatherPopupPayload(obs.Condition, weatherLabel(obs, cfg), iconID, durationSec),
			weatherOverlay(obs, cfg))}
	n.sound = soundSetting(sound)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.coord.showNotice(cctx, n); err != nil {
		a.logger.Warn("weather popup failed", "err", err)
	}
}

const weatherSettingsKey = "weather_json"

func (a *App) weatherSettingSpec() settingSpec[weather.Config] {
	return settingSpec[weather.Config]{
		key:          weatherSettingsKey,
		presentation: weatherPresentationKeys,
		decode:       a.dropUnknownOpenMeteoModel,
		view:         func(c Config) weather.Config { return c.Weather },
		check: func(cur, next weather.Config) error {
			if next.Provider != cur.Provider && !a.weatherFetcher.ProviderAvailable(next.Provider) {
				return fmt.Errorf("weather.provider %q needs %s set on the server", next.Provider, weather.KeyedProviders[next.Provider].Env)
			}
			if next.SevereSound != cur.SevereSound {
				return validSoundSetting("weather.severe_sound", next.SevereSound)
			}
			return nil
		},
		apply: func(c *Config, w weather.Config) error {
			w.FillAbsent()
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

func (a *App) dropUnknownOpenMeteoModel(blob string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		return []byte(blob)
	}
	var model string
	raw, ok := m["open_meteo_model"]
	if !ok || (json.Unmarshal(raw, &model) == nil && (model == "" || slices.Contains(openMeteoModels, model))) {
		return []byte(blob)
	}
	a.logger.Warn("persisted weather model ignored", "model", string(raw), "fallback", cmp.Or(a.cfg.Load().Weather.OpenMeteoModel, weather.DefaultOpenMeteoModel))
	delete(m, "open_meteo_model")
	out, err := json.Marshal(m)
	if err != nil {
		return []byte(blob)
	}
	return out
}
