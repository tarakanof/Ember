package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/weather"
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

func newTomorrowFetcher(base string) *weather.Fetcher {
	wf := weather.NewFetcher()
	wf.TomorrowBase = base
	wf.TomorrowKey = testTomorrowKey
	return wf
}

var belgrade = weather.Config{Provider: weather.TomorrowIO, Latitude: 44.8176, Longitude: 20.4633}

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
	wf.TomorrowKey = straddleKey
	_, err := wf.FetchTomorrow(t.Context(), belgrade, weather.FetchHint{})
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

func newTomorrowApp(t *testing.T, st *tomorrowStub, logs *bytes.Buffer) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.Provider = weather.TomorrowIO
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
	if kw == nil || kw.Provider != weather.TomorrowIO || kw.Code != "2" || kw.Cond != render.WeatherClouds || kw.Model != "" {
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
		app.weatherFetcher.TomorrowKey = key
		code, m := weatherConfigBody(t, app, http.MethodGet, "")
		var got []string
		if code != http.StatusOK || json.Unmarshal(m["providers_available"], &got) != nil {
			t.Fatalf("GET = %d %v", code, m)
		}
		want := []string{"open-meteo", "met-no"}
		if key != "" {
			want = []string{"open-meteo", "met-no", weather.TomorrowIO}
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
	cfg.Weather.ApplyDefaults()
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	code, m := weatherConfigBody(t, app, http.MethodPut, `{"provider":"tomorrow-io","refresh_minutes":20}`)
	if code != http.StatusBadRequest || !strings.Contains(string(m["error"]), weather.TomorrowKeyEnv) {
		t.Fatalf("PUT without key = %d %v, want 400 naming %s", code, m, weather.TomorrowKeyEnv)
	}
	if w := app.cfg.Load().Weather; w.Provider != "open-meteo" || w.RefreshMinutes == 20 {
		t.Errorf("rejected PUT changed config: %+v", w)
	}

	app.weatherFetcher.TomorrowKey = testTomorrowKey
	code, m = weatherConfigBody(t, app, http.MethodPut, `{"provider":"tomorrow-io"}`)
	if code != http.StatusOK || string(m["provider"]) != `"tomorrow-io"` {
		t.Fatalf("PUT with key = %d %v", code, m)
	}

	app.weatherFetcher.TomorrowKey = ""
	if code, _ = weatherConfigBody(t, app, http.MethodPut, `{"refresh_minutes":15}`); code != http.StatusOK {
		t.Errorf("PUT of another key under a chosen provider whose key went away = %d, want 200", code)
	}
}

func keylessStoredTomorrowApp(t *testing.T, st *tomorrowStub, db, key string) *App {
	t.Helper()
	app := newTomorrowApp(t, st, nil)
	app.weatherFetcher.TomorrowKey = key
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
	if w := a.cfg.Load().Weather; w.Provider != weather.TomorrowIO || w.RefreshMinutes != 25 {
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
	if out.Provider != weather.TomorrowIO || out.Current != nil || !strings.Contains(out.Error, weather.TomorrowKeyEnv) {
		t.Errorf("state = provider %q current %v error %q, want tomorrow-io, nothing current and a reason naming %s", out.Provider, out.Current, out.Error, weather.TomorrowKeyEnv)
	}
	if _, m := weatherConfigBody(t, a, http.MethodGet, ""); strings.Contains(string(m["providers_available"]), weather.TomorrowIO) {
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
	app.weatherFetcher.TomorrowKey = ""
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
		{"retry deadline capped at a day", `{"retry_until":%q}`, weather.RetryAfterMax + time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTomorrowStub(t, tomorrowFixture(t))
			app := newTomorrowApp(t, st, nil)
			if err := app.ensureStore(t.TempDir() + "/s.db"); err != nil {
				t.Fatal(err)
			}
			ahead := time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)
			if err := app.store.PutSetting(weatherQuotaKey(weather.TomorrowIO), fmt.Sprintf(c.row, ahead)); err != nil {
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
	if err := app.store.PutSetting(weatherQuotaKey(weather.TomorrowIO), fmt.Sprintf(`{"retry_until":%q}`, t0.Add(30*time.Minute).Format(time.RFC3339Nano))); err != nil {
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
	failWrites(t, db, weatherQuotaKey(weather.TomorrowIO))
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
	app.weatherFetcher.AirQualityBase = air.URL
	t0 := time.Now()
	app.pollWeather(context.Background(), t0)
	q := quotaOf(app, weather.TomorrowIO)
	attempt, until := q.attemptAt, q.retryAt
	if attempt.Sub(t0) < 250*time.Millisecond || until.Sub(t0) < 30*time.Minute+250*time.Millisecond {
		t.Errorf("attempt +%v, deadline +%v after the poll began, want both past the 300-ms air call", attempt.Sub(t0), until.Sub(t0))
	}
}

const straddleKey = "Zq8xV3kP9mW2rT7yHc"

func TestTomorrowErrorKeepsRetryAfterOfAWrappedError(t *testing.T) {
	wf := newTomorrowFetcher("http://unused")
	inner := &weather.HTTPError{Code: http.StatusTooManyRequests, Status: "429 Too Many Requests", RetryAfter: 10 * time.Minute}
	err := wf.TomorrowError(t.Context(), fmt.Errorf("call with %s: %w", testTomorrowKey, inner), time.Now())
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("err = %v, want fixed text", err)
	}
	if got := retryAfterOf(err); got != 10*time.Minute {
		t.Errorf("retryAfter = %v, want the wrapped 10m kept", got)
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
	if v, _, _ := b.store.GetSetting(weatherQuotaKey(weather.TomorrowIO)); strings.Contains(v, "SECRET") || !strings.Contains(v, "retry_until") {
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
	until := quotaOf(app, weather.TomorrowIO).retryAt
	if d := until.Sub(t0); d < 30*time.Minute+250*time.Millisecond {
		t.Errorf("retry deadline %v after the request started, want 30 min after the response (≥ 30m0.25s)", d)
	}
}

func TestTomorrowKeepsTheLongestBackoff(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	src := app.cfg.Load().Weather.Source()
	t0 := time.Now()
	app.markWeatherFailedAfter(t0, t0, src, 30*time.Minute, true)
	app.markWeatherFailedAfter(t0, t0, src, time.Minute, true)
	until := quotaOf(app, weather.TomorrowIO).retryAt
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
	app.weatherFetcher.MetNoBase = met.URL
	ctx := context.Background()
	t0 := time.Now()
	app.pollWeather(ctx, t0)
	app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
	app.pollWeather(ctx, t0.Add(time.Minute))
	app.updateConfig(func(c *Config) { c.Weather.Provider = weather.TomorrowIO })
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
	tio, _ := json.Marshal(knobWeather{Provider: weather.TomorrowIO, Cond: "storm", Code: "99"})
	met, _ := json.Marshal(knobWeather{Provider: "met-no", Cond: "storm", Code: "lightssleetshowersandthunder_polartwilight"})
	if len(tio) > len(met) {
		t.Errorf("tomorrow-io block %d B, more than the MET worst case's %d B", len(tio), len(met))
	}
	if len(weather.TomorrowIO) >= 16 {
		t.Errorf("provider %q does not fit cinder's 16-byte provider field", weather.TomorrowIO)
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
	if n := strings.Count(logs.String(), "weather quota read recovered"); n != 1 {
		t.Errorf("%d recovery lines, want 1", n)
	}
}

func TestWeatherStateErrorOnlyWhileWeatherIsOn(t *testing.T) {
	st := newTomorrowStub(t, tomorrowFixture(t))
	app := newTomorrowApp(t, st, nil)
	app.weatherFetcher.TomorrowKey = ""
	now := time.Now()
	if out := app.buildWeatherState(now); out.Error == "" {
		t.Fatalf("keyless and on: error empty")
	}
	app.updateConfig(func(c *Config) { c.Weather.Enabled = false })
	if out := app.buildWeatherState(now); out.Error != "" {
		t.Errorf("weather off: error = %q, want none", out.Error)
	}
}
