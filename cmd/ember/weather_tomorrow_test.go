package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const testTomorrowKey = "tk-SECRET+key/42"

var tomorrowT0 = time.Date(2026, 10, 11, 8, 5, 0, 0, time.UTC)

func tomorrowFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/weather/tomorrow_forecast.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type tomorrowStub struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	retry  atomic.Pointer[string]
	body   atomic.Pointer[[]byte]
	query  atomic.Pointer[string]
}

func newTomorrowStub(t *testing.T, body []byte) *tomorrowStub {
	t.Helper()
	st := &tomorrowStub{}
	st.body.Store(&body)
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		if r.URL.Path != "/v4/weather/forecast" {
			t.Errorf("path = %s, want /v4/weather/forecast", r.URL.Path)
		}
		q := r.URL.RawQuery
		st.query.Store(&q)
		if code := int(st.status.Load()); code != 0 {
			if v := st.retry.Load(); v != nil {
				w.Header().Set("Retry-After", *v)
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"code":429001,"type":"Too Many Calls","message":"rate limit for key ` + r.URL.Query().Get("apikey") + `"}`))
			return
		}
		_, _ = w.Write(*st.body.Load())
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func newTomorrowFetcher(base string) *weatherFetcher {
	wf := newWeatherFetcher()
	wf.tomorrowBase = base
	wf.tomorrowKey = testTomorrowKey
	return wf
}

var belgrade = WeatherConfig{Provider: tomorrowIO, Latitude: 44.8176, Longitude: 20.4633}

func TestFetchTomorrowSendsKeyLocationAndMetricUnits(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	if _, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{}); err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(*st.query.Load())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"location": "44.8176,20.4633", "units": "metric", "apikey": testTomorrowKey}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("timesteps") {
		t.Errorf("timesteps = %q, want none so the plan's default timelines come back", q.Get("timesteps"))
	}
}

func TestFetchTomorrowMapsCurrentHourlyAndDays(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.ObservedAt.Equal(tomorrowT0) || obs.TempC != 14.2 || obs.Condition != render.WeatherClouds || obs.ConditionCode != "2" || obs.Severe {
		t.Errorf("current = %v %v %q %q %v, want the first minutely entry: 14.2 °C, partly cloudy as WMO 2",
			obs.ObservedAt, obs.TempC, obs.Condition, obs.ConditionCode, obs.Severe)
	}
	n := obs.Now
	if !approx(n.Cloud, 63) || !approx(n.UV, 2) || !approx(n.WindKmh, 18) || !approx(n.GustKmh, 36) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v, want m/s turned into km/h", n)
	}
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Errorf("tz = (%v, %d), want the hint's 7200", obs.TZKnown, obs.TZOffsetSeconds)
	}
	if len(obs.Hourly) != forecastFetchHours || !obs.HourlyStart.Equal(time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)) || obs.Hourly[1] != 13.5 {
		t.Errorf("hourly start %v, %d values (%v), want 24 from 08:00", obs.HourlyStart, len(obs.Hourly), obs.Hourly)
	}
	if len(obs.Steps) != forecastFetchHours || !obs.Steps[1].At.Equal(time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("steps = %d, second at %v, want the minutely now then hourly entries after it", len(obs.Steps), obs.Steps[1].At)
	}
	if s := obs.Steps[5]; s.Condition != render.WeatherRain || s.Code != "65" || !s.Severe || s.Overlay != render.OverlayStorm {
		t.Errorf("13:00 step = %+v, want heavy rain as WMO 65", s)
	}

	if len(obs.Days) != forecastDays {
		t.Fatalf("days = %+v, want %d", obs.Days, forecastDays)
	}
	d0 := obs.Days[0]
	if d0.Start != time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC) || d0.Cond != render.WeatherSnow || d0.Code != "73" ||
		!approx(d0.MinC, 9.1) || !approx(d0.MaxC, 18.5) || !approx(d0.Pop, 80) || !approx(d0.PrecipMM, 2.7) ||
		!approx(d0.UVMax, 4) || !approx(d0.GustMaxKmh, 43.2) {
		t.Errorf("today = %+v, want Oct 11 local midnight (yesterday's 06:00 period skipped), rain + snow LWE summed", d0)
	}
	if d1 := obs.Days[1]; d1.Cond != render.WeatherClear || d1.Code != "0" || d1.Pop != nil || d1.PrecipMM != nil {
		t.Errorf("day 1 = %+v, want clear with pop and precipitation left out", d1)
	}
	if d2 := obs.Days[2]; d2.Cond != render.WeatherStorm || d2.Code != "95" || !approx(d2.PrecipMM, 11.2) {
		t.Errorf("day 2 = %+v, want a thunderstorm day", d2)
	}
}

func TestFetchTomorrowBuildsQuarterHourRainFromMinutelyThenHourly(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.PrecipStep != 15*time.Minute || len(obs.Precip) != precipStepsTomorrow {
		t.Fatalf("step %v, %d steps, want 15 min and %d (12 h)", obs.PrecipStep, len(obs.Precip), precipStepsTomorrow)
	}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 11, h, m, 0, 0, time.UTC) }
	want := []precipStep{
		{At: at(8, 0)},
		{At: at(8, 15), MMh: 1.2},
		{At: at(8, 30), MMh: 1.2},
		{At: at(8, 45), MMh: 0.6, Snow: true},
		{At: at(9, 0), MMh: 0.6, Snow: true},
		{At: at(9, 15), MMh: 0.6, Snow: true},
		{At: at(9, 30), MMh: 0.6, Snow: true},
		{At: at(9, 45), MMh: 0.6, Snow: true},
		{At: at(10, 0)},
	}
	for i, w := range want {
		if g := obs.Precip[i]; !g.At.Equal(w.At) || !approxEq(g.MMh, w.MMh) || g.Snow != w.Snow {
			t.Errorf("step %d = %+v, want %+v", i, g, w)
		}
	}
	if g := obs.Precip[20]; !g.At.Equal(at(13, 0)) || !approxEq(g.MMh, 3) || g.Snow {
		t.Errorf("step 20 = %+v, want the 13:00 hour's 3 mm/h", g)
	}
	if last := obs.Precip[len(obs.Precip)-1]; !last.At.Equal(at(19, 45)) {
		t.Errorf("last step at %v, want 19:45 (48 quarter-hour steps from 08:00)", last.At)
	}
}

func approxEq(a, b float64) bool { return approx(&a, b) }

func TestFetchTomorrowWithoutMinutelyUsesHourlyRain(t *testing.T) {
	body := `{"timelines":{"hourly":[
{"time":"2026-10-11T08:00:00Z","values":{"temperature":10,"weatherCode":4200,"rainIntensity":0.8}},
{"time":"2026-10-11T09:00:00Z","values":{"temperature":11,"weatherCode":1000,"rainIntensity":0}}]}}`
	st := newTomorrowStub(t, []byte(body))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Precip) != 8 || !obs.Precip[0].At.Equal(time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)) || !approxEq(obs.Precip[3].MMh, 0.8) || obs.Precip[4].MMh != 0 {
		t.Errorf("precip = %+v, want each hour split into four 15-min steps", obs.Precip)
	}
	if obs.Condition != render.WeatherRain || obs.ConditionCode != "61" || obs.TempC != 10 {
		t.Errorf("current = %q %q %v, want the first hour's light rain", obs.Condition, obs.ConditionCode, obs.TempC)
	}
}

func TestFetchTomorrowLeavesMissingValuesOut(t *testing.T) {
	body := `{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{"temperature":7,"weatherCode":0}}],
"daily":[{"time":"2026-10-11T04:00:00Z","values":{"temperatureMax":9}}]}}`
	st := newTomorrowStub(t, []byte(body))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := obs.Now; n.Cloud != nil || n.UV != nil || n.WindKmh != nil || n.GustKmh != nil || n.WindDir != nil {
		t.Errorf("now = %+v, want every value nil", n)
	}
	if obs.Condition != render.WeatherClouds || obs.ConditionCode != "" || obs.Severe {
		t.Errorf("unknown code 0 = %q %q, want clouds without a code", obs.Condition, obs.ConditionCode)
	}
	if len(obs.Precip) != 0 {
		t.Errorf("precip = %+v, want none without intensities", obs.Precip)
	}
	if len(obs.Days) != 1 || !approx(obs.Days[0].MaxC, 9) || obs.Days[0].MinC != nil || obs.Days[0].PrecipMM != nil || obs.Days[0].Code != "" {
		t.Errorf("days = %+v, want one day with only max", obs.Days)
	}
}

func TestFetchTomorrowRejectsEmptyTimelines(t *testing.T) {
	st := newTomorrowStub(t, []byte(`{"timelines":{}}`))
	if _, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{}); err == nil {
		t.Fatal("empty timelines accepted")
	}
}

func TestFetchTomorrowWithoutKeyMakesNoRequest(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	wf := newTomorrowFetcher(st.srv.URL)
	wf.tomorrowKey = ""
	_, err := wf.fetchTomorrow(t.Context(), belgrade, fetchHint{})
	if !errors.Is(err, errTomorrowNoKey) || !strings.Contains(err.Error(), tomorrowKeyEnv) {
		t.Errorf("err = %v, want errTomorrowNoKey naming %s", err, tomorrowKeyEnv)
	}
	if st.hits.Load() != 0 {
		t.Errorf("%d requests without a key, want 0", st.hits.Load())
	}
}

func TestTomorrowWeatherCodesAllMapToWMO(t *testing.T) {
	documented := []int{1000, 1100, 1101, 1102, 1001, 2000, 2100, 4000, 4001, 4200, 4201,
		5000, 5001, 5100, 5101, 6000, 6001, 6200, 6201, 7000, 7101, 7102, 8000}
	for _, c := range documented {
		if _, ok := tomorrowWMO(c); !ok {
			t.Errorf("code %d has no WMO equivalent", c)
		}
	}
	cases := map[int]string{1000: render.WeatherClear, 1001: render.WeatherClouds, 2100: render.WeatherFog,
		4000: render.WeatherRain, 6001: render.WeatherRain, 5101: render.WeatherSnow, 7000: render.WeatherSnow, 8000: render.WeatherStorm}
	for code, want := range cases {
		wmo, _ := tomorrowWMO(code)
		if got, _ := wmoCondition(wmo); got != want {
			t.Errorf("code %d → WMO %d → %q, want %q", code, wmo, got, want)
		}
	}
	if _, ok := tomorrowWMO(0); ok {
		t.Error("code 0 (unknown) mapped")
	}
}

func TestFetchTomorrowErrorsAreFixedText(t *testing.T) {
	st := newTomorrowStub(t, nil)
	st.status.Store(http.StatusUnauthorized)
	wf := newTomorrowFetcher(st.srv.URL)
	_, err := wf.fetchTomorrow(t.Context(), belgrade, fetchHint{})
	if err == nil || err.Error() != "weather http 401" {
		t.Errorf("http error = %v, want the numeric status only", err)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	wf.tomorrowBase = dead.URL
	_, err = wf.fetchTomorrow(t.Context(), belgrade, fetchHint{})
	if err == nil || err.Error() != "tomorrow-io: request failed" {
		t.Errorf("transport error = %v, want fixed text", err)
	}
}

func TestFetchTomorrowNeverLeaksKeyFragmentsFromTheReasonPhrase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 429 " + straddleKey[:9] + "\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	wf := newTomorrowFetcher(srv.URL)
	wf.tomorrowKey = straddleKey
	_, err := wf.fetchTomorrow(t.Context(), belgrade, fetchHint{})
	assertNoKeyFragment(t, "reason phrase", err)
	if retryAfterOf(err) <= 0 {
		t.Errorf("429 with a custom reason lost its back-off: %v", err)
	}
}

func assertNoKeyFragment(t *testing.T, where string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", where)
	}
	for i := 0; i+4 <= len(straddleKey); i++ {
		if frag := straddleKey[i : i+4]; strings.Contains(err.Error(), frag) {
			t.Fatalf("%s: error %q carries key fragment %q", where, err, frag)
		}
	}
}

func TestFetchTomorrowNeverLeaksKeyFragmentsFromA200Body(t *testing.T) {
	fields := []string{
		`{"timelines":{"minutely":[{"time":%q,"values":{}}]}}`,
		`{"timelines":{"hourly":[{"time":%q,"values":{}}]}}`,
		`{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{}}],"daily":[{"time":%q,"values":{}}]}}`,
		`{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{"temperature":%q}}]}}`,
		`{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{"weatherCode":%q}}]}}`,
		`{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{"rainIntensity":%q}}]}}`,
		`{"timelines":{"hourly":[{"time":"2026-10-11T08:00:00Z","values":{}}],"daily":[{"time":"2026-10-11T04:00:00Z","values":{"temperatureMax":%q}}]}}`,
		`{"timelines":%q}`,
		`%s`,
	}
	for fi, f := range fields {
		for n := 4; n <= len(straddleKey); n++ {
			body := []byte(fmt.Sprintf(f, straddleKey[:n]))
			st := newTomorrowStub(t, body)
			wf := newTomorrowFetcher(st.srv.URL)
			wf.tomorrowKey = straddleKey
			if _, err := wf.fetchTomorrow(t.Context(), belgrade, fetchHint{}); err != nil {
				assertNoKeyFragment(t, fmt.Sprintf("field %d prefix %d", fi, n), err)
			}
		}
	}
}

func newTomorrowApp(t *testing.T, st *tomorrowStub, logs *bytes.Buffer) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Provider = tomorrowIO
	cfg.Weather.Latitude, cfg.Weather.Longitude = belgrade.Latitude, belgrade.Longitude
	cfg.Weather.RefreshMinutes = 1
	cfg.Weather.PopupOnChange = boolPtr(false)
	cfg.Weather.PopupIntervalMinutes = intPtr(0)
	cfg.Weather.SunPopups = boolPtr(false)
	cfg.Weather.SevereAlert = boolPtr(false)
	cfg.Weather.AirTile = boolPtr(false)
	cfg.Weather.AirPopupThreshold = 0
	logger := testLogger()
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	app := NewApp(cfg, &recordingPublisher{}, logger)
	app.weatherFetcher = newTomorrowFetcher(st.srv.URL)
	return app
}

func TestTomorrowKnobViewCarriesSpellsAndWMOCodes(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	now := tomorrowT0.Add(time.Minute)
	app.pollWeather(context.Background(), now)
	kw := app.knobWeatherFor(now, true)
	if kw == nil || kw.Provider != tomorrowIO || kw.Code != "2" || kw.Cond != render.WeatherClouds || kw.Model != "" {
		t.Fatalf("knob weather = %+v", kw)
	}
	if kw.Now == nil || kw.Now.Rain == nil || *kw.Now.Rain != 0 {
		t.Errorf("now.rain = %v, want 0 (dry 08:00 step)", kw.Now)
	}
	at := func(h, m int) int64 { return time.Date(2026, 10, 11, h, m, 0, 0, time.UTC).Unix() }
	want := [][4]int64{{at(8, 15), at(10, 0), 1, 1}, {at(13, 0), at(14, 0), 2, 0}}
	if kw.Rain == nil || !slices.Equal(kw.Rain.Spells, want) {
		t.Errorf("spells = %+v, want %v", kw.Rain, want)
	}
	if kw.Today == nil || kw.Today.Pop == nil || *kw.Today.Pop != 80 || len(kw.Days) != 3 || kw.Days[1].Code != "95" {
		t.Errorf("today %+v days %+v", kw.Today, kw.Days)
	}
	b, _ := json.Marshal(kw)
	if !strings.Contains(string(b), `"provider":"tomorrow-io"`) {
		t.Errorf("view = %s", b)
	}
}

func TestTomorrowRefreshNeverFasterThanFiveMinutes(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.pollWeather(ctx, t0.Add(4*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests within 4 min at refresh_minutes 1, want 1", st.hits.Load())
	}
	app.pollWeather(ctx, t0.Add(5*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("%d requests after 5 min, want 2", st.hits.Load())
	}
}

func TestTomorrowFailureRetriesNoFasterThanFiveMinutes(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	st.status.Store(http.StatusServiceUnavailable)
	app := newTomorrowApp(t, st, nil)
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.pollWeather(ctx, t0.Add(3*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests 3 min after a failure, want 1 (the 2-min retry floor is raised to 5)", st.hits.Load())
	}
	app.pollWeather(ctx, t0.Add(5*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("%d requests 5 min after a failure, want 2", st.hits.Load())
	}
}

func TestTomorrow429BacksOffPastTheRefreshInterval(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
		min, max   time.Duration
	}{
		{"retry-after header", "1800", 30 * time.Minute, 30*time.Minute + time.Second},
		{"no header waits for the hourly reset", "", time.Nanosecond, time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTomorrowStub(t, tomorrowFixture(t))
			st.status.Store(http.StatusTooManyRequests)
			if c.retryAfter != "" {
				st.retry.Store(&c.retryAfter)
			}
			app := newTomorrowApp(t, st, nil)
			now := time.Now()
			app.pollWeather(context.Background(), now)
			app.weather.mu.RLock()
			wait := app.weather.retryAt.Sub(now)
			app.weather.mu.RUnlock()
			if wait < c.min || wait > c.max {
				t.Errorf("retry in %v, want %v..%v (not capped at the 5-min refresh)", wait, c.min, c.max)
			}
			app.pollWeather(context.Background(), now.Add(10*time.Minute))
			if st.hits.Load() != 1 && wait > 10*time.Minute {
				t.Errorf("%d requests before the back-off ended, want 1", st.hits.Load())
			}
		})
	}
}

func TestTomorrowFailureLogNeverCarriesTheKey(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	st.status.Store(http.StatusTooManyRequests)
	var logs bytes.Buffer
	app := newTomorrowApp(t, st, &logs)
	app.pollWeather(context.Background(), time.Now())
	if !strings.Contains(logs.String(), "weather fetch failed") {
		t.Fatalf("no failure logged: %s", logs.String())
	}
	if strings.Contains(logs.String(), "SECRET") {
		t.Errorf("log carries the key: %s", logs.String())
	}
}

func weatherConfigBody(t *testing.T, app *App, method, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/v1/weather/config", strings.NewReader(body))
	if method == http.MethodGet {
		app.handleWeatherConfigGet(w, r)
	} else {
		app.handleWeatherConfigPut(w, r)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestWeatherConfigReportsProvidersAvailable(t *testing.T) {
	for _, key := range []string{"", testTomorrowKey} {
		app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
		app.weatherFetcher.tomorrowKey = key
		code, m := weatherConfigBody(t, app, http.MethodGet, "")
		var got []string
		if code != http.StatusOK || json.Unmarshal(m["providers_available"], &got) != nil {
			t.Fatalf("GET = %d %v", code, m)
		}
		want := []string{"open-meteo", "met-no"}
		if key != "" {
			want = []string{"open-meteo", "met-no", tomorrowIO}
		}
		if !slices.Equal(got, want) {
			t.Errorf("key set %v: providers_available = %q, want %q", key != "", got, want)
		}
		if strings.Contains(string(m["providers_available"]), "SECRET") || m["tomorrow_api_key"] != nil {
			t.Errorf("config carries the key: %v", m)
		}
	}
}

func TestWeatherConfigPutTomorrowNeedsTheKey(t *testing.T) {
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	code, m := weatherConfigBody(t, app, http.MethodPut, `{"provider":"tomorrow-io","refresh_minutes":20}`)
	if code != http.StatusBadRequest || !strings.Contains(string(m["error"]), tomorrowKeyEnv) {
		t.Fatalf("PUT without key = %d %v, want 400 naming %s", code, m, tomorrowKeyEnv)
	}
	if w := app.cfg.Load().Weather; w.Provider != "open-meteo" || w.RefreshMinutes == 20 {
		t.Errorf("rejected PUT changed config: %+v", w)
	}

	app.weatherFetcher.tomorrowKey = testTomorrowKey
	code, m = weatherConfigBody(t, app, http.MethodPut, `{"provider":"tomorrow-io"}`)
	if code != http.StatusOK || string(m["provider"]) != `"tomorrow-io"` {
		t.Fatalf("PUT with key = %d %v", code, m)
	}

	app.weatherFetcher.tomorrowKey = ""
	if code, _ = weatherConfigBody(t, app, http.MethodPut, `{"refresh_minutes":15}`); code != http.StatusOK {
		t.Errorf("PUT of another key under a chosen provider whose key went away = %d, want 200", code)
	}
}

func keylessStoredTomorrowApp(t *testing.T, st *tomorrowStub, db, key string) *App {
	t.Helper()
	app := newTomorrowApp(t, st, nil)
	app.weatherFetcher.tomorrowKey = key
	app.updateConfig(func(c *Config) { c.Weather.Provider = "open-meteo" })
	if err := app.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	app.settings.reapply()
	return app
}

func TestKeylessStartKeepsTheStoredTomorrowChoiceAndSaysWhy(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newTomorrowStub(t, tomorrowFixture(t))
	seed := newTomorrowApp(t, st, nil)
	if err := seed.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	stored := `{"enabled":true,"provider":"tomorrow-io","refresh_minutes":25,"latitude":44.8176,"longitude":20.4633}`
	if err := seed.store.PutSetting(weatherSettingsKey, stored); err != nil {
		t.Fatal(err)
	}

	a := keylessStoredTomorrowApp(t, st, db, "")
	if w := a.cfg.Load().Weather; w.Provider != tomorrowIO || w.RefreshMinutes != 25 {
		t.Fatalf("keyless start = provider %q refresh %d, want the stored tomorrow-io kept", w.Provider, w.RefreshMinutes)
	}
	if blob, _, _ := a.store.GetSetting(weatherSettingsKey); !strings.Contains(blob, `"provider":"tomorrow-io"`) {
		t.Errorf("stored row rewritten to %s", blob)
	}
	now := time.Now()
	a.pollWeather(context.Background(), now)
	if st.hits.Load() != 0 {
		t.Fatalf("%d requests without a key, want 0", st.hits.Load())
	}
	out := a.buildWeatherState(now)
	if out.Provider != tomorrowIO || out.Current != nil || !strings.Contains(out.Error, tomorrowKeyEnv) {
		t.Errorf("state = provider %q current %v error %q, want tomorrow-io, nothing current and a reason naming %s", out.Provider, out.Current, out.Error, tomorrowKeyEnv)
	}
	if _, m := weatherConfigBody(t, a, http.MethodGet, ""); strings.Contains(string(m["providers_available"]), tomorrowIO) {
		t.Errorf("providers_available = %s without a key", m["providers_available"])
	}

	b := keylessStoredTomorrowApp(t, st, db, testTomorrowKey)
	b.pollWeather(context.Background(), now)
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests once the key is back, want 1", st.hits.Load())
	}
	if out := b.buildWeatherState(now); out.Error != "" || out.Current == nil {
		t.Errorf("state with the key = error %q current %v", out.Error, out.Current)
	}
}

func TestKeylessTomorrowCanBeSwitchedAway(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	app.weatherFetcher.tomorrowKey = ""
	code, m := weatherConfigBody(t, app, http.MethodPut, `{"provider":"met-no"}`)
	if code != http.StatusOK || string(m["provider"]) != `"met-no"` {
		t.Fatalf("PUT met-no from a keyless tomorrow-io = %d %v", code, m)
	}
	if out := app.buildWeatherState(time.Now()); out.Error != "" {
		t.Errorf("state error = %q under met-no", out.Error)
	}
}

func TestTomorrowQuotaLoadIgnoresAClockThatWasAhead(t *testing.T) {
	cases := []struct {
		name, row string
		pollAfter time.Duration
	}{
		{"attempt in the future is dropped", `{"attempt_at":%q}`, 0},
		{"retry deadline capped at a day", `{"retry_until":%q}`, weatherRetryAfterMax + time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTomorrowStub(t, tomorrowFixture(t))
			app := newTomorrowApp(t, st, nil)
			if err := app.ensureStore(t.TempDir() + "/s.db"); err != nil {
				t.Fatal(err)
			}
			ahead := time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)
			if err := app.store.PutSetting(weatherQuotaKey(tomorrowIO), fmt.Sprintf(c.row, ahead)); err != nil {
				t.Fatal(err)
			}
			t0 := time.Now()
			app.pollWeather(context.Background(), t0)
			app.pollWeather(context.Background(), t0.Add(c.pollAfter))
			if st.hits.Load() != 1 {
				t.Errorf("%d requests, want 1: a row written while the clock was a year ahead must not block", st.hits.Load())
			}
		})
	}
}

func renameSettings(t *testing.T, dbPath, from, to string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE ` + from + ` RENAME TO ` + to); err != nil {
		t.Fatal(err)
	}
}

func TestTomorrowQuotaReadFailureFailsClosedAndRetries(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	if err := app.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	if err := app.store.PutSetting(weatherQuotaKey(tomorrowIO), fmt.Sprintf(`{"retry_until":%q}`, t0.Add(30*time.Minute).Format(time.RFC3339Nano))); err != nil {
		t.Fatal(err)
	}
	renameSettings(t, db, "settings", "settings_away")
	app.pollWeather(context.Background(), t0)
	if st.hits.Load() != 0 {
		t.Fatalf("%d requests with the quota row unreadable, want 0 (5-min floor from start)", st.hits.Load())
	}
	renameSettings(t, db, "settings_away", "settings")
	app.pollWeather(context.Background(), t0.Add(6*time.Minute))
	if st.hits.Load() != 0 {
		t.Fatalf("%d requests after the row became readable inside its Retry-After, want 0 (the read is retried)", st.hits.Load())
	}
	app.pollWeather(context.Background(), t0.Add(31*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests after the Retry-After, want 1", st.hits.Load())
	}
}

func TestTomorrowQuotaWriteFailureKeepsTheReservation(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	if err := app.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	failWrites(t, db, weatherQuotaKey(tomorrowIO))
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)
	app.pollWeather(context.Background(), t0.Add(2*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests within 5 min while the quota row can't be written, want 1", st.hits.Load())
	}
}

func TestTomorrowQuotaTimesIncludePreFetchWork(t *testing.T) {
	air := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"current":{"european_aqi":10}}`))
	}))
	t.Cleanup(air.Close)
	st := newTomorrowStub(t, tomorrowFixture(t))
	st.status.Store(http.StatusTooManyRequests)
	ra := "1800"
	st.retry.Store(&ra)
	app := newTomorrowApp(t, st, nil)
	app.updateConfig(func(c *Config) { c.Weather.AirTile = boolPtr(true) })
	app.weatherFetcher.airQualityBase = air.URL
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)
	q := quotaOf(app, tomorrowIO)
	attempt, until := q.attemptAt, q.retryAt
	if attempt.Sub(t0) < 250*time.Millisecond || until.Sub(t0) < 30*time.Minute+250*time.Millisecond {
		t.Errorf("attempt +%v, deadline +%v after the poll began, want both past the 300-ms air call", attempt.Sub(t0), until.Sub(t0))
	}
}

func TestTomorrowDayCodeCountsClearHoursAndOnlyFutureGaps(t *testing.T) {
	utc := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	var hours []string
	hours = append(hours, tomorrowHours(utc(11, 10), 18, 1000)...)
	hours = append(hours, tomorrowHours(utc(12, 4), 23, 1000)...)
	hours = append(hours, tomorrowHours(utc(13, 3), 1, 2000)...)
	body := `{"timelines":{"hourly":[` + strings.Join(hours, ",") + `],
"daily":[{"time":"2026-10-11T04:00:00Z","values":{"weatherCodeMax":2100}},
{"time":"2026-10-12T04:00:00Z","values":{"weatherCodeMax":2000}}]}}`
	st := newTomorrowStub(t, []byte(body))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Days) != 2 {
		t.Fatalf("days = %+v", obs.Days)
	}
	if d := obs.Days[0]; d.Code != "0" {
		t.Errorf("today = %q %q, want clear: the 6 past morning hours don't count for weatherCodeMax fog", d.Cond, d.Code)
	}
	if d := obs.Days[1]; d.Code != "0" {
		t.Errorf("tomorrow = %q %q, want clear: 23 h clear beat 1 h fog", d.Cond, d.Code)
	}
}

const straddleKey = "Zq8xV3kP9mW2rT7yHc"

func TestFetchTomorrowNeverLeaksKeyFragmentsFromErrorBodies(t *testing.T) {
	var body atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(*body.Load()))
	}))
	t.Cleanup(srv.Close)
	wf := newTomorrowFetcher(srv.URL)
	wf.tomorrowKey = straddleKey
	for off := 0; off <= 300; off++ {
		b := strings.Repeat("x", off) + straddleKey + strings.Repeat("y", 40)
		body.Store(&b)
		_, err := wf.fetchTomorrow(t.Context(), belgrade, fetchHint{})
		assertNoKeyFragment(t, fmt.Sprintf("offset %d", off), err)
	}
}

func TestTomorrowErrorKeepsRetryAfterOfAWrappedError(t *testing.T) {
	wf := newTomorrowFetcher("http://unused")
	inner := &weatherHTTPError{code: http.StatusTooManyRequests, status: "429 Too Many Requests", retryAfter: 10 * time.Minute}
	err := wf.tomorrowError(t.Context(), fmt.Errorf("call with %s: %w", testTomorrowKey, inner), time.Now())
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("err = %v, want fixed text", err)
	}
	if got := retryAfterOf(err); got != 10*time.Minute {
		t.Errorf("retryAfter = %v, want the wrapped 10m kept", got)
	}
}

func tomorrowHours(from time.Time, n, code int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = fmt.Sprintf(`{"time":%q,"values":{"temperature":10,"weatherCode":%d}}`, from.Add(time.Duration(i)*time.Hour).Format(time.RFC3339), code)
	}
	return out
}

func TestTomorrowDayCodeWeighsHoursInTheProvidersDay(t *testing.T) {
	utc := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	var hours []string
	hours = append(hours, tomorrowHours(utc(11, 5), 1, 5001)...)
	hours = append(hours, tomorrowHours(utc(11, 18), 10, 4201)...)
	hours = append(hours, tomorrowHours(utc(12, 4), 6, 1000)...)
	hours = append(hours, tomorrowHours(utc(12, 10), 3, 5101)...)
	hours = append(hours, tomorrowHours(utc(12, 13), 15, 1000)...)
	body := `{"timelines":{"hourly":[` + strings.Join(hours, ",") + `],
"daily":[{"time":"2026-10-11T04:00:00Z","values":{"weatherCodeMax":4200}},
{"time":"2026-10-12T04:00:00Z","values":{"weatherCodeMax":5101}},
{"time":"2026-10-13T04:00:00Z","values":{"weatherCodeMax":8000}}]}}`
	st := newTomorrowStub(t, []byte(body))
	obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ cond, code, why string }{
		{render.WeatherRain, "65", "10 h of heavy rain (to 05:00 local the next day) outweigh 1 h of flurries and the 13 uncovered hours of weatherCodeMax light rain"},
		{render.WeatherSnow, "75", "3 h of heavy snow; the rain hours before 06:00 local belong to the day before"},
		{render.WeatherStorm, "95", "no hourly data: weatherCodeMax"},
	}
	if len(obs.Days) != len(want) {
		t.Fatalf("days = %+v", obs.Days)
	}
	for i, w := range want {
		if d := obs.Days[i]; d.Cond != w.cond || d.Code != w.code {
			t.Errorf("day %d = %q %q, want %q %q: %s", i, d.Cond, d.Code, w.cond, w.code, w.why)
		}
	}
}

func TestTomorrowQuotaSurvivesARestart(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newTomorrowStub(t, tomorrowFixture(t))
	st.status.Store(http.StatusTooManyRequests)
	ra := "1800"
	st.retry.Store(&ra)
	a := newTomorrowApp(t, st, nil)
	if err := a.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	a.pollWeather(context.Background(), t0)

	b := newTomorrowApp(t, st, nil)
	if err := b.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	b.pollWeather(context.Background(), t0.Add(time.Minute))
	b.pollWeather(context.Background(), t0.Add(20*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests after a restart inside a 30-min Retry-After, want 1", st.hits.Load())
	}
	if v, _, _ := b.store.GetSetting(weatherQuotaKey(tomorrowIO)); strings.Contains(v, "SECRET") || !strings.Contains(v, "retry_until") {
		t.Errorf("stored quota = %s, want deadlines and no credential", v)
	}
	st.status.Store(0)
	b.pollWeather(context.Background(), t0.Add(31*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("%d requests after the Retry-After, want 2", st.hits.Load())
	}

	c := newTomorrowApp(t, st, nil)
	if err := c.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	c.pollWeather(context.Background(), t0.Add(33*time.Minute))
	if st.hits.Load() != 2 {
		t.Errorf("%d requests 2 min after the last attempt across a restart, want 2 (5-min floor)", st.hits.Load())
	}
}

func TestTomorrowBackoffCountsFromTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	app := newTomorrowApp(t, &tomorrowStub{srv: srv}, nil)
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)
	until := quotaOf(app, tomorrowIO).retryAt
	if d := until.Sub(t0); d < 30*time.Minute+250*time.Millisecond {
		t.Errorf("retry deadline %v after the request started, want 30 min after the response (≥ 30m0.25s)", d)
	}
}

func TestTomorrowKeepsTheLongestBackoff(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	src := app.cfg.Load().Weather.source()
	t0 := time.Now()
	app.markWeatherFailedAfter(t0, t0, src, 30*time.Minute, true)
	app.markWeatherFailedAfter(t0, t0, src, time.Minute, true)
	until := quotaOf(app, tomorrowIO).retryAt
	if !until.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("deadline = %v after t0, want the 30-min one kept", until.Sub(t0))
	}
}

func TestTomorrowReserveIsCheckAndRecordInOne(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	cfg := app.cfg.Load().Weather
	t0 := time.Now()
	if !app.weather.reserveQuota(cfg, t0) {
		t.Fatal("first reservation refused")
	}
	if app.weather.reserveQuota(cfg, t0) {
		t.Error("second reservation at the same instant granted")
	}
	other := cfg
	other.Provider = "open-meteo"
	if !app.weather.reserveQuota(other, t0) {
		t.Error("a provider without a quota was refused")
	}
}

func TestTomorrowFloorHoldsAcrossLocationChanges(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	for i := 1; i <= 4; i++ {
		app.updateConfig(func(c *Config) { c.Weather.Latitude += 0.01 })
		app.pollWeather(ctx, t0.Add(time.Duration(i)*time.Minute))
	}
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests in 4 min of location changes, want 1", st.hits.Load())
	}
	app.pollWeather(ctx, t0.Add(5*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("%d requests after 5 min, want 2", st.hits.Load())
	}
}

func TestTomorrow429BackoffHoldsAcrossProviderSwitches(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	st.status.Store(http.StatusTooManyRequests)
	ra := "1800"
	st.retry.Store(&ra)
	app := newTomorrowApp(t, st, nil)
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(met.Close)
	app.weatherFetcher.metNoBase = met.URL
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
	app.pollWeather(ctx, t0.Add(time.Minute))
	app.updateConfig(func(c *Config) { c.Weather.Provider = tomorrowIO })
	app.pollWeather(ctx, t0.Add(10*time.Minute))
	if st.hits.Load() != 1 {
		t.Fatalf("%d requests 10 min into a 30-min Retry-After after a switch away and back, want 1", st.hits.Load())
	}
	st.status.Store(0)
	app.pollWeather(ctx, t0.Add(31*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("%d requests after the Retry-After, want 2", st.hits.Load())
	}
}

func TestTomorrow429DuringALocationChangeStillBacksOff(t *testing.T) {
	var app *App
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			app.updateConfig(func(c *Config) { c.Weather.Latitude += 0.01 })
		}
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	app = newTomorrowApp(t, &tomorrowStub{srv: srv}, nil)
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.pollWeather(ctx, t0.Add(10*time.Minute))
	if hits.Load() != 1 {
		t.Fatalf("%d requests 10 min into the Retry-After of a fetch whose source changed mid-flight, want 1", hits.Load())
	}
}

func TestTomorrowKnobBlockStaysUnderTheMetWorstCase(t *testing.T) {
	tio, _ := json.Marshal(knobWeather{Provider: tomorrowIO, Cond: "storm", Code: "99"})
	met, _ := json.Marshal(knobWeather{Provider: "met-no", Cond: "storm", Code: "lightssleetshowersandthunder_polartwilight"})
	if len(tio) > len(met) {
		t.Errorf("tomorrow-io block %d B, more than the MET worst case's %d B", len(tio), len(met))
	}
	if len(tomorrowIO) >= 16 {
		t.Errorf("provider %q does not fit cinder's 16-byte provider field", tomorrowIO)
	}
}

func quotaOf(app *App, provider string) providerQuota {
	app.weather.mu.RLock()
	defer app.weather.mu.RUnlock()
	if q := app.weather.quotas[provider]; q != nil {
		return *q
	}
	return providerQuota{}
}

func TestTomorrowDayCodeOnTheSharedWMOScale(t *testing.T) {
	utc := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	day := func(hours ...[]string) []string {
		var out []string
		for _, h := range hours {
			out = append(out, h...)
		}
		return out
	}
	cases := []struct {
		name  string
		hours []string
		want  string
	}{
		{"mostly cloudy and cloudy pool as overcast", day(tomorrowHours(utc(11, 10), 1, 1102), tomorrowHours(utc(11, 11), 1, 1001), tomorrowHours(utc(11, 12), 7, 1000)), "3"},
		{"light freezing rain does not outrank plain rain", day(tomorrowHours(utc(11, 10), 1, 6200), tomorrowHours(utc(11, 11), 1, 4001)), "63"},
		{"heavy freezing rain outranks heavy rain", day(tomorrowHours(utc(11, 10), 1, 6201), tomorrowHours(utc(11, 11), 1, 4201)), "67"},
		{"ice pellets pool and rank with snow", day(tomorrowHours(utc(11, 10), 1, 7101), tomorrowHours(utc(11, 11), 1, 7000), tomorrowHours(utc(11, 12), 1, 5000)), "77"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"timelines":{"hourly":[` + strings.Join(c.hours, ",") + `],"daily":[{"time":"2026-10-11T04:00:00Z","values":{}}]}}`
			st := newTomorrowStub(t, []byte(body))
			obs, err := newTomorrowFetcher(st.srv.URL).fetchTomorrow(t.Context(), belgrade, fetchHint{TZOffsetSeconds: 7200, TZKnown: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Days) != 1 || obs.Days[0].Code != c.want {
				t.Errorf("days = %+v, want code %s", obs.Days, c.want)
			}
		})
	}
}

func TestTomorrowQuotaReadFailureWarnsOncePerStreak(t *testing.T) {
	db := t.TempDir() + "/s.db"
	st := newTomorrowStub(t, tomorrowFixture(t))
	var logs bytes.Buffer
	app := newTomorrowApp(t, st, &logs)
	if err := app.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	renameSettings(t, db, "settings", "settings_away")
	t0 := time.Now()
	for i := range 3 {
		app.pollWeather(context.Background(), t0.Add(time.Duration(i)*time.Minute))
	}
	if n := strings.Count(logs.String(), "weather quota read failed"); n != 1 {
		t.Errorf("%d warnings over 3 failed reads, want 1", n)
	}
	renameSettings(t, db, "settings_away", "settings")
	app.pollWeather(context.Background(), t0.Add(6*time.Minute))
	if st.hits.Load() != 1 {
		t.Errorf("%d requests once the row is readable again, want 1", st.hits.Load())
	}
}
