package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

const testWeatherAPIKey = "wa-SECRET+key/7"

var waT0 = time.Date(2026, 10, 11, 8, 15, 0, 0, time.UTC)

func waFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/weather/weatherapi_forecast.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type waStub struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	retry  atomic.Pointer[string]
	body   atomic.Pointer[[]byte]
	query  atomic.Pointer[string]
	path   atomic.Pointer[string]
}

func newWAStub(t *testing.T, body []byte) *waStub {
	t.Helper()
	st := &waStub{}
	st.body.Store(&body)
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		q, p := r.URL.RawQuery, r.URL.Path
		st.query.Store(&q)
		st.path.Store(&p)
		if code := int(st.status.Load()); code != 0 {
			if v := st.retry.Load(); v != nil {
				w.Header().Set("Retry-After", *v)
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"code":2007,"message":"quota exceeded for key ` + r.URL.Query().Get("key") + `"}}`))
			return
		}
		_, _ = w.Write(*st.body.Load())
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func newWAFetcher(base string) *weather.Fetcher {
	wf := weather.NewFetcher()
	wf.WeatherAPIBase = base
	wf.WeatherAPIKey = testWeatherAPIKey
	return wf
}

var waBelgrade = weather.Config{Provider: weather.WeatherAPI, Latitude: 44.8176, Longitude: 20.4633}

func newWAApp(t *testing.T, st *waStub) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Provider = weather.WeatherAPI
	cfg.Weather.Latitude, cfg.Weather.Longitude = waBelgrade.Latitude, waBelgrade.Longitude
	cfg.Weather.RefreshMinutes = 1
	cfg.Weather.PopupOnChange = boolPtr(false)
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.SunPopups = boolPtr(false)
	cfg.Weather.SevereAlert = boolPtr(false)
	cfg.Weather.AirTile = boolPtr(false)
	cfg.Weather.AirPopupThreshold = 0
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	app.weatherFetcher = newWAFetcher(st.srv.URL)
	return app
}

func TestWeatherAPIKnobViewCarriesSpellsAndWMOCodes(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	app := newWAApp(t, st)
	now := waT0.Add(time.Minute)
	app.pollWeather(context.Background(), now)
	kw := app.knobWeatherFor(now, true)
	if kw == nil || kw.Provider != weather.WeatherAPI || kw.Code != "2" || kw.Model != "" {
		t.Fatalf("knob weather = %+v", kw)
	}
	if kw.Now == nil || kw.Now.Rain == nil || *kw.Now.Rain != 1 {
		t.Errorf("now.rain = %+v, want 1 (light rain in the 08:15 quarter)", kw.Now)
	}
	at := func(h int) int64 { return time.Date(2026, 10, 11, h, 0, 0, 0, time.UTC).Unix() }
	want := [][4]int64{{at(8), at(11), 1, 0}, {at(16), at(17), 2, 0}}
	if kw.Rain == nil || !slices.Equal(kw.Rain.Spells, want) || kw.Rain.Until == nil || *kw.Rain.Until != at(11) {
		t.Errorf("rain = %+v, want spells %v until 11:00Z", kw.Rain, want)
	}
	if kw.Today == nil || kw.Today.Pop == nil || *kw.Today.Pop != 80 || len(kw.Days) != 2 || kw.Days[1].Code != "65" {
		t.Errorf("today %+v days %+v, want pop 80 and the 2 free-plan days ahead", kw.Today, kw.Days)
	}
	b, _ := json.Marshal(kw)
	if strings.Contains(string(b), "SECRET") {
		t.Errorf("view carries the key: %s", b)
	}
}

func TestWeatherAPIRefreshFloorAndOwnQuota(t *testing.T) {
	wa := newWAStub(t, waFixture(t))
	app := newWAApp(t, wa)
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.pollWeather(ctx, t0.Add(4*time.Minute))
	if wa.hits.Load() != 1 {
		t.Fatalf("%d requests within 4 min at refresh_minutes 1, want 1", wa.hits.Load())
	}
	app.pollWeather(ctx, t0.Add(5*time.Minute))
	if wa.hits.Load() != 2 {
		t.Fatalf("%d requests after 5 min, want 2", wa.hits.Load())
	}
	if q := quotaOf(app, weather.WeatherAPI); q.attemptAt.IsZero() || !quotaOf(app, weather.TomorrowIO).attemptAt.IsZero() {
		t.Errorf("quota state: weatherapi %+v, tomorrow-io %+v, want only weatherapi's set", q, quotaOf(app, weather.TomorrowIO))
	}
}

func TestWeatherAPIAuthFailuresBackOffToTheNextHour(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			st := newWAStub(t, waFixture(t))
			st.status.Store(int32(code))
			app := newWAApp(t, st)
			now := time.Now()
			app.pollWeather(context.Background(), now)
			until := quotaOf(app, weather.WeatherAPI).retryAt
			next := time.Now().Truncate(time.Hour).Add(time.Hour)
			if until.Before(next.Add(-time.Second)) || until.After(next.Add(time.Second)) {
				t.Errorf("retry at %v, want the next top of the hour %v (a bad key or spent quota must not be retried every 5 min)", until, next)
			}
		})
	}
}

func TestWeatherAPIQuotaPersistsUnderItsOwnRow(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newWAStub(t, waFixture(t))
	st.status.Store(http.StatusTooManyRequests)
	ra := "1800"
	st.retry.Store(&ra)
	a := newWAApp(t, st)
	if err := a.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	a.pollWeather(context.Background(), t0)
	v, ok, _ := a.store.GetSetting(weatherQuotaKey(weather.WeatherAPI))
	if !ok || !strings.Contains(v, "retry_until") || strings.Contains(v, "SECRET") || weatherQuotaKey(weather.WeatherAPI) != "weather_quota_weatherapi" {
		t.Fatalf("row %s = %q, want deadlines without the key", weatherQuotaKey(weather.WeatherAPI), v)
	}
	if _, ok, _ := a.store.GetSetting(weatherQuotaKey(weather.TomorrowIO)); ok {
		t.Error("a weatherapi 429 wrote Tomorrow.io's row")
	}
	b := newWAApp(t, st)
	if err := b.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	b.pollWeather(context.Background(), t0.Add(10*time.Minute))
	if st.hits.Load() != 1 {
		t.Errorf("%d requests after a restart inside the Retry-After, want 1", st.hits.Load())
	}
}

func TestKeylessWeatherAPIStaysChosenAndSaysWhy(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	app := newWAApp(t, st)
	app.weatherFetcher.WeatherAPIKey = ""
	now := time.Now()
	app.pollWeather(context.Background(), now)
	if st.hits.Load() != 0 {
		t.Fatalf("%d requests without a key, want 0", st.hits.Load())
	}
	out := app.buildWeatherState(now)
	if out.Provider != weather.WeatherAPI || out.Error != "weatherapi needs EMBER_WEATHERAPI_KEY on the server" {
		t.Errorf("state = %q %q", out.Provider, out.Error)
	}
	code, m := weatherConfigBody(t, app, http.MethodPut, `{"provider":"met-no"}`)
	if code != http.StatusOK || string(m["provider"]) != `"met-no"` {
		t.Errorf("switching away = %d %v", code, m)
	}
	code, m = weatherConfigBody(t, app, http.MethodPut, `{"provider":"weatherapi"}`)
	if code != http.StatusBadRequest || !strings.Contains(string(m["error"]), weather.WeatherAPIKeyEnv) {
		t.Errorf("PUT weatherapi without the key = %d %v, want 400 naming %s", code, m, weather.WeatherAPIKeyEnv)
	}
}

func TestWeatherAPIEmptyReplyKeepsTheCachedObservation(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	app := newWAApp(t, st)
	t0 := waT0.Add(time.Minute)
	app.pollWeather(context.Background(), t0)
	empty := []byte(`{"forecast":{"forecastday":[{"date":"2026-10-11","hour":[]}]}}`)
	st.body.Store(&empty)
	app.pollWeather(context.Background(), t0.Add(10*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", st.hits.Load())
	}
	wx := app.weather.state(app.cfg.Load().Weather, t0.Add(10*time.Minute))
	if !wx.HaveObs || wx.Obs.TempC != 14.2 || !wx.Obs.ObservedAt.Equal(waT0) {
		t.Errorf("cache after an empty reply = have %v, %v °C at %v; want the earlier observation kept", wx.HaveObs, wx.Obs.TempC, wx.Obs.ObservedAt)
	}
}

func TestKeyRejectionIsNotPersistedAcrossARestart(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			db := t.TempDir() + "/s.db"
			st := newWAStub(t, waFixture(t))
			st.status.Store(int32(code))
			a := newWAApp(t, st)
			if err := a.ensureStore(db); err != nil {
				t.Fatal(err)
			}
			t0 := time.Now()
			a.pollWeather(context.Background(), t0)
			if quotaOf(a, weather.WeatherAPI).retryAt.IsZero() {
				t.Fatalf("a %d set no in-memory hold", code)
			}
			if v, _, _ := a.store.GetSetting(weatherQuotaKey(weather.WeatherAPI)); !strings.Contains(v, `"retry_until":"0001-01-01T00:00:00Z"`) {
				t.Errorf("stored row = %s, want no retry deadline for a %d", v, code)
			}
			st.status.Store(0)
			b := newWAApp(t, st)
			if err := b.ensureStore(db); err != nil {
				t.Fatal(err)
			}
			b.pollWeather(context.Background(), t0.Add(6*time.Minute))
			if st.hits.Load() != 2 {
				t.Errorf("%d requests after a restart (the fixed key), want 2: a %d hold must not outlive the process", st.hits.Load(), code)
			}
		})
	}
}

func TestWeatherAPIKnobBlockStaysUnderTheMetWorstCase(t *testing.T) {
	wa, _ := json.Marshal(knobWeather{Provider: weather.WeatherAPI, Cond: "storm", Code: "99"})
	met, _ := json.Marshal(knobWeather{Provider: "met-no", Cond: "storm", Code: "lightssleetshowersandthunder_polartwilight"})
	if len(wa) > len(met) || len(weather.WeatherAPI) >= 16 {
		t.Errorf("weatherapi block %d B (MET worst %d B), provider %d B (cinder field 16)", len(wa), len(met), len(weather.WeatherAPI))
	}
}
