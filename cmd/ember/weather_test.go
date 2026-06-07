package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestWMOCondition(t *testing.T) {
	cases := []struct {
		code   int
		cond   string
		severe bool
	}{
		{0, render.WeatherClear, false},
		{2, render.WeatherClouds, false},
		{45, render.WeatherFog, false},
		{51, render.WeatherRain, false},
		{65, render.WeatherRain, true},
		{71, render.WeatherSnow, false},
		{75, render.WeatherSnow, true},
		{82, render.WeatherRain, true},
		{95, render.WeatherStorm, true},
		{99, render.WeatherStorm, true},
	}
	for _, c := range cases {
		cond, severe := wmoCondition(c.code)
		if cond != c.cond || severe != c.severe {
			t.Errorf("wmoCondition(%d) = (%s,%v), want (%s,%v)", c.code, cond, severe, c.cond, c.severe)
		}
	}
}

func TestMetSymbolCondition(t *testing.T) {
	cases := []struct {
		sym    string
		cond   string
		severe bool
	}{
		{"clearsky_day", render.WeatherClear, false},
		{"fair_night", render.WeatherClear, false},
		{"partlycloudy_day", render.WeatherClouds, false},
		{"cloudy", render.WeatherClouds, false},
		{"fog", render.WeatherFog, false},
		{"lightrain", render.WeatherRain, false},
		{"heavyrain", render.WeatherRain, true},
		{"snow", render.WeatherSnow, false},
		{"heavysnowshowers_day", render.WeatherSnow, true},
		{"rainandthunder", render.WeatherStorm, true},
	}
	for _, c := range cases {
		cond, severe := metSymbolCondition(c.sym)
		if cond != c.cond || severe != c.severe {
			t.Errorf("metSymbolCondition(%q) = (%s,%v), want (%s,%v)", c.sym, cond, severe, c.cond, c.severe)
		}
	}
}

func TestWeatherTempText(t *testing.T) {
	if got := weatherTempText(21.4, "metric"); got != "21°" {
		t.Errorf("metric = %q, want 21°", got)
	}
	if got := weatherTempText(0, "imperial"); got != "32°" {
		t.Errorf("imperial 0C = %q, want 32°", got)
	}
	if got := weatherTempText(-4.6, "metric"); got != "-5°" {
		t.Errorf("negative = %q, want -5°", got)
	}
}

func TestFetchOpenMeteo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("latitude") == "" {
			t.Error("missing latitude query")
		}
		w.Write([]byte(`{"current":{"temperature_2m":12.5,"weather_code":61}}`))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.openMeteoBase = srv.URL
	obs, err := wf.fetch(context.Background(), WeatherConfig{Provider: "open-meteo", Latitude: 52.1, Longitude: 4.3})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if obs.Condition != render.WeatherRain || obs.TempC != 12.5 {
		t.Errorf("obs = %+v, want rain/12.5", obs)
	}
}

func TestFetchMetNoSendsUserAgent(t *testing.T) {
	gotUA := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`{"properties":{"timeseries":[{"data":{"instant":{"details":{"air_temperature":3.2}},"next_1_hours":{"summary":{"symbol_code":"heavysnow"}}}}]}}`))
	}))
	defer srv.Close()
	wf := newWeatherFetcher()
	wf.metNoBase = srv.URL
	obs, err := wf.fetch(context.Background(), WeatherConfig{Provider: "met-no", Latitude: 59.9, Longitude: 10.7})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotUA == "" {
		t.Error("met.no requires a User-Agent; none sent")
	}
	if obs.Condition != render.WeatherSnow || !obs.Severe || obs.TempC != 3.2 {
		t.Errorf("obs = %+v, want severe snow / 3.2", obs)
	}
}

func TestEvaluateWeatherPopupPriority(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	app := NewApp(cfg, pub, testLogger())
	wcfg := app.cfg.Load().Weather

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	// Severe transition fires + chimes. The chime is NOT on the notification
	// (firmware drops it under an icon) — it's played via /api/rtttl (the default
	// severe sound is an RTTTL string).
	storm := weatherObservation{Condition: render.WeatherStorm, TempC: 18, Severe: true, FetchedAt: now}
	if !app.evaluateWeatherPopup(context.Background(), now, storm, render.WeatherClouds, false, time.Time{}, wcfg) {
		t.Fatal("severe transition should fire")
	}
	pub.mu.Lock()
	if len(pub.notify) != 1 {
		t.Errorf("severe transition should send one popup: %+v", pub.notify)
	}
	if _, has := pub.notify[0]["sound"]; has {
		t.Error("severe popup must not carry sound on the notification")
	}
	if len(pub.rtttls) != 1 || pub.rtttls[0] != defaultWeatherSevereSound {
		t.Errorf("severe alert should chime via /api/rtttl %q, got %v", defaultWeatherSevereSound, pub.rtttls)
	}
	pub.mu.Unlock()

	// Already-severe (prevSevere true) does NOT re-fire on severe path; but a
	// condition change still does (without any chime).
	rain := weatherObservation{Condition: render.WeatherRain, TempC: 16, Severe: false, FetchedAt: now}
	if !app.evaluateWeatherPopup(context.Background(), now, rain, render.WeatherStorm, true, now, wcfg) {
		t.Fatal("condition change should fire")
	}
	pub.mu.Lock()
	last := pub.notify[len(pub.notify)-1]
	if last["sound"] != nil {
		t.Errorf("change popup should be silent: %+v", last)
	}
	if len(pub.rtttls) != 1 {
		t.Errorf("a non-severe change popup must not chime: %v", pub.rtttls)
	}
	pub.mu.Unlock()

	// No change, interval not elapsed → no popup.
	if app.evaluateWeatherPopup(context.Background(), now, rain, render.WeatherRain, false, now, wcfg) {
		t.Error("stable weather within interval should not fire")
	}
}

func TestApplyWeatherSettingsValidation(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	bad := WeatherConfig{Provider: "open-meteo", Units: "metric", Latitude: 200, Longitude: 0, RefreshMinutes: 10, PopupDurationSeconds: 30}
	if err := app.applyWeatherSettings(bad); err == nil {
		t.Error("latitude 200 should be rejected")
	}
	good := WeatherConfig{Provider: "met-no", Units: "imperial", Latitude: 52, Longitude: 4, RefreshMinutes: 15, PopupDurationSeconds: 20}
	if err := app.applyWeatherSettings(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if app.cfg.Load().Weather.Provider != "met-no" {
		t.Error("config not swapped into live cfg")
	}
}

func TestWeatherIconIDOverride(t *testing.T) {
	cfg := WeatherConfig{}
	if got := cfg.weatherIconID(render.WeatherRain); got != "72" {
		t.Errorf("default rain icon = %q, want 72", got)
	}
	cfg.IconIDs = map[string]string{render.WeatherRain: "999"}
	if got := cfg.weatherIconID(render.WeatherRain); got != "999" {
		t.Errorf("override rain icon = %q, want 999", got)
	}
	// A condition with no override still falls back to the default.
	if got := cfg.weatherIconID(render.WeatherSnow); got != "2289" {
		t.Errorf("unset snow icon = %q, want default 2289", got)
	}
}

// TestApplyWeatherSettingsPreservesDisables guards the review fix: a menu PUT
// that turns the opt-in toggles off (and sets interval=0) must survive — earlier
// applyWeatherSettings re-ran applyDefaults and forced them back on.
func TestApplyWeatherSettingsPreservesDisables(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	off := WeatherConfig{
		Enabled: true, Provider: "open-meteo", Units: "metric", Latitude: 52, Longitude: 4,
		RefreshMinutes: 10, PopupDurationSeconds: 30,
		RotateInApps: false, PopupOnChange: false, SevereAlert: false, PopupIntervalMinutes: 0,
	}
	if err := app.applyWeatherSettings(off); err != nil {
		t.Fatalf("config rejected: %v", err)
	}
	got := app.cfg.Load().Weather
	if got.RotateInApps || got.PopupOnChange || got.SevereAlert || got.PopupIntervalMinutes != 0 {
		t.Errorf("disables were clobbered: rotate=%v onChange=%v severe=%v interval=%d",
			got.RotateInApps, got.PopupOnChange, got.SevereAlert, got.PopupIntervalMinutes)
	}
}

// TestPollWeatherBackoffAndSeed guards two review fixes: (1) a failing provider
// is not refetched until a full refresh interval elapses (no 60s hammering while
// have==false); (2) the first successful observation seeds the interval clock so
// no interval popup fires on startup.
func TestPollWeatherBackoffAndSeed(t *testing.T) {
	var hits int32
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0}}`))
	}))
	defer srv.Close()

	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.RefreshMinutes = 10
	cfg.Weather.PopupIntervalMinutes = 120
	app := NewApp(cfg, pub, testLogger())
	app.weatherFetcher = newWeatherFetcher()
	app.weatherFetcher.openMeteoBase = srv.URL

	t0 := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	// Failing fetch: one attempt, lastFetch recorded.
	app.pollWeather(context.Background(), t0)
	// A minute later, still inside the refresh interval -> NOT due, no new hit.
	app.pollWeather(context.Background(), t0.Add(time.Minute))
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("failing provider was refetched within the interval: %d hits, want 1", got)
	}

	// Past the refresh interval, provider recovers: fetch succeeds, seeds the
	// interval clock, and must NOT fire an interval popup on this first success.
	fail = false
	app.pollWeather(context.Background(), t0.Add(11*time.Minute))
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("recovered fetch didn't happen: %d hits, want 2", got)
	}
	pub.mu.Lock()
	popups := len(pub.notify)
	pub.mu.Unlock()
	if popups != 0 {
		t.Errorf("first successful observation fired %d popups, want 0 (interval clock should seed silently)", popups)
	}

	// One full interval after the seed -> the interval popup now fires.
	app.pollWeather(context.Background(), t0.Add(11*time.Minute+121*time.Minute))
	pub.mu.Lock()
	popups = len(pub.notify)
	pub.mu.Unlock()
	if popups != 1 {
		t.Errorf("interval popup didn't fire after a full interval: %d popups, want 1", popups)
	}
}
