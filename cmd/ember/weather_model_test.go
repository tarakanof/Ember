package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchOpenMeteoModelQuery(t *testing.T) {
	cases := []struct {
		model   string
		want    string
		present bool
	}{
		{"", "", false},
		{"best_match", "", false},
		{"ecmwf_ifs", "ecmwf_ifs", true},
		{"ukmo_seamless", "ukmo_seamless", true},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			var got []string
			var has bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, has = r.URL.Query()["models"], r.URL.Query().Has("models")
				_, _ = w.Write([]byte(openMeteoFacesBody))
			}))
			defer srv.Close()
			wf := newWeatherFetcher()
			wf.openMeteoBase = srv.URL
			cfg := WeatherConfig{Provider: "open-meteo", OpenMeteoModel: c.model, Latitude: 44.8, Longitude: 20.5}
			if _, err := wf.fetch(t.Context(), cfg, fetchHint{}); err != nil {
				t.Fatal(err)
			}
			if has != c.present || (c.present && (len(got) != 1 || got[0] != c.want)) {
				t.Errorf("models = %v (present %v), want %q (present %v)", got, has, c.want, c.present)
			}
		})
	}
}

func newDefaultedApp(t *testing.T) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	return NewApp(cfg, &recordingPublisher{}, testLogger())
}

func TestWeatherConfigDefaultsOpenMeteoModelToBestMatch(t *testing.T) {
	app := newDefaultedApp(t)
	w := httptest.NewRecorder()
	app.handleWeatherConfigGet(w, httptest.NewRequest("GET", "/v1/weather/config", nil))
	mustJSONHave(t, w.Body.Bytes(), `"open_meteo_model":"best_match"`)

	w = httptest.NewRecorder()
	app.handleWeatherConfigPut(w, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(`{"open_meteo_model":""}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT empty model = %d %s", w.Code, w.Body.String())
	}
	mustJSONHave(t, w.Body.Bytes(), `"open_meteo_model":"best_match"`)
}

func TestWeatherConfigPutRejectsUnknownOpenMeteoModel(t *testing.T) {
	app := newDefaultedApp(t)
	for _, m := range []string{"nope", "meteofrance_seamless", "ECMWF_IFS", "ecmwf_ifs,gfs_seamless"} {
		w := httptest.NewRecorder()
		app.handleWeatherConfigPut(w, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(`{"open_meteo_model":"`+m+`"}`)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("PUT model %q = %d, want 400", m, w.Code)
		}
	}
	if got := app.cfg.Load().Weather.OpenMeteoModel; got != "best_match" {
		t.Errorf("model after rejected PUTs = %q, want best_match", got)
	}
}

func TestValidateWeatherRejectsUnknownOpenMeteoModel(t *testing.T) {
	c := defaultConfig()
	c.applyDefaults()
	c.Weather.OpenMeteoModel = "nope"
	if err := validateWeather(c.Weather); err == nil {
		t.Fatal("validateWeather accepted an unknown model")
	}
	c.Weather.OpenMeteoModel = "ecmwf_ifs025"
	if err := validateWeather(c.Weather); err != nil {
		t.Fatalf("validateWeather rejected ecmwf_ifs025: %v", err)
	}
}

func newDefaultedAppWithStore(t *testing.T) *App {
	t.Helper()
	a := newTestAppWithStore(t)
	cur := *a.cfg.Load()
	cur.applyDefaults()
	a.cfg.Store(&cur)
	return a
}

func TestStoredWeatherBlobWithUnknownModelKeepsTheRest(t *testing.T) {
	a := newDefaultedAppWithStore(t)
	blob := `{"enabled":true,"provider":"open-meteo","open_meteo_model":"retired_model","latitude":44.8,"longitude":20.5,"refresh_minutes":15}`
	if err := a.store.PutSetting(weatherSettingsKey, blob); err != nil {
		t.Fatal(err)
	}
	a.settings.reapply()
	w := a.cfg.Load().Weather
	if w.OpenMeteoModel != "best_match" || w.RefreshMinutes != 15 || w.Latitude != 44.8 || !w.Enabled {
		t.Errorf("weather after reapply = model %q refresh %d lat %v enabled %v", w.OpenMeteoModel, w.RefreshMinutes, w.Latitude, w.Enabled)
	}
}

func TestStoredWeatherBlobKeepsAKnownModel(t *testing.T) {
	a := newDefaultedAppWithStore(t)
	if err := a.store.PutSetting(weatherSettingsKey, `{"open_meteo_model":"ecmwf_ifs"}`); err != nil {
		t.Fatal(err)
	}
	a.settings.reapply()
	if got := a.cfg.Load().Weather.OpenMeteoModel; got != "ecmwf_ifs" {
		t.Errorf("model after reapply = %q, want ecmwf_ifs", got)
	}
}

type modelStub struct {
	mu     sync.Mutex
	models []string
	req    chan struct{}
}

func newModelSwitchApp(t *testing.T) (*App, *modelStub) {
	t.Helper()
	app, _ := newProviderSwitchApp(t)
	st := &modelStub{req: make(chan struct{}, 1)}
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := r.URL.Query().Get("models")
		st.mu.Lock()
		st.models = append(st.models, m)
		st.mu.Unlock()
		code := "3"
		if m == "ecmwf_ifs" {
			code = "80"
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":14,"weather_code":` + code + `,"time":1790409600}}`))
		signalStub(st.req)
	}))
	t.Cleanup(om.Close)
	app.weatherFetcher.openMeteoBase = om.URL
	return app, st
}

func (s *modelStub) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.models...)
}

func putWeatherModel(t *testing.T, app *App, model string) {
	t.Helper()
	w := httptest.NewRecorder()
	app.handleWeatherConfigPut(w, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(`{"open_meteo_model":"`+model+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT model %s = %d %s", model, w.Code, w.Body.String())
	}
}

func TestWeatherModelPutHidesOldObservation(t *testing.T) {
	app, _ := newModelSwitchApp(t)
	now := time.Now()
	app.pollWeather(context.Background(), now)
	if kw := app.knobWeather(now); kw == nil || kw.Code != "3" {
		t.Fatalf("knob weather before switch = %+v", kw)
	}

	putWeatherModel(t, app, "ecmwf_ifs")

	if kw := app.knobWeather(now); kw != nil {
		t.Errorf("knob view keeps best_match data after the model switch: %+v", kw)
	}
	if out := app.buildWeatherState(now); out.Current != nil {
		t.Errorf("weather state keeps best_match data after the model switch: %+v", out.Current)
	}
}

func TestStartWeatherRefetchesOnModelChange(t *testing.T) {
	app, st := newModelSwitchApp(t)
	seq, _ := app.changes.subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.StartWeather(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	recvWithin(t, st.req, "best_match request")
	waitWeatherCommit(t, app, seq, "best_match observation")

	seq, _ = app.changes.subscribe()
	putWeatherModel(t, app, "ecmwf_ifs")

	recvWithin(t, st.req, "ecmwf_ifs request")
	waitWeatherCommit(t, app, seq, "ecmwf_ifs observation")
	kw := app.knobWeather(time.Now())
	if kw == nil || kw.Code != "80" || kw.Model != "ecmwf_ifs" {
		t.Fatalf("knob weather after switch = %+v", kw)
	}
	if got := st.seen(); len(got) != 2 || got[0] != "" || got[1] != "ecmwf_ifs" {
		t.Errorf("models fetched = %q, want [\"\" ecmwf_ifs]", got)
	}
}

func TestWeatherModelChangeUnderMetNoDoesNotRefetch(t *testing.T) {
	app, st := newProviderSwitchApp(t)
	app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
	now := time.Now()
	app.pollWeather(context.Background(), now)
	putWeatherModel(t, app, "ecmwf_ifs")
	app.pollWeather(context.Background(), now.Add(time.Second))
	if st.metHits.Load() != 1 || st.omHits.Load() != 0 {
		t.Errorf("fetches met-no=%d open-meteo=%d, want 1 and 0", st.metHits.Load(), st.omHits.Load())
	}
	if kw := app.knobWeather(now); kw == nil || kw.Model != "" {
		t.Errorf("met-no knob weather = %+v, want it shown without a model", kw)
	}
}

func TestWeatherReadoutsNameTheOpenMeteoModel(t *testing.T) {
	app, _ := newModelSwitchApp(t)
	now := time.Now()
	app.pollWeather(context.Background(), now)
	kw := app.knobWeather(now)
	b, _ := json.Marshal(kw)
	if strings.Contains(string(b), `"model"`) {
		t.Errorf("best_match knob weather carries a model: %s", b)
	}
	if out := app.buildWeatherState(now); out.Model != "best_match" {
		t.Errorf("weather state model = %q, want best_match", out.Model)
	}

	putWeatherModel(t, app, "ecmwf_ifs")
	app.pollWeather(context.Background(), now.Add(time.Second))
	b, _ = json.Marshal(app.knobWeather(now.Add(time.Second)))
	if !strings.Contains(string(b), `"provider":"open-meteo","model":"ecmwf_ifs"`) {
		t.Errorf("knob weather = %s, want the model after the provider", b)
	}
	if out := app.buildWeatherState(now); out.Model != "ecmwf_ifs" {
		t.Errorf("weather state model = %q, want ecmwf_ifs", out.Model)
	}

	app.updateConfig(func(c *Config) { c.Weather.Provider = "met-no" })
	if out := app.buildWeatherState(now); out.Model != "" {
		t.Errorf("met-no weather state model = %q, want none", out.Model)
	}
}

func TestLoadConfigFallsBackFromAnUnknownOpenMeteoModel(t *testing.T) {
	for _, bad := range []string{"nope", "ecmwf_ifs,gfs_seamless"} {
		t.Run(bad, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			body := `{"awtrix":{"http_base_url":"http://x"},"weather":{"enabled":true,"open_meteo_model":"` + bad + `"}}`
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			cfg, err := loadConfig(path, slog.New(slog.NewTextHandler(&logs, nil)))
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.Weather.OpenMeteoModel != "best_match" {
				t.Errorf("model = %q, want best_match", cfg.Weather.OpenMeteoModel)
			}
			if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "weather.open_meteo_model invalid") {
				t.Errorf("no warning for the unknown model: %q", logs.String())
			}
		})
	}
}

func TestLoadConfigKeepsAKnownOpenMeteoModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	body := `{"awtrix":{"http_base_url":"http://x"},"weather":{"open_meteo_model":"ecmwf_ifs"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Weather.OpenMeteoModel != "ecmwf_ifs" {
		t.Errorf("model = %q, want ecmwf_ifs", cfg.Weather.OpenMeteoModel)
	}
}

func TestAdminReloadFallsBackFromAnUnknownOpenMeteoModel(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://x"},"weather":{"open_meteo_model":"ecmwf_ifs"}}`)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	if got := app.cfg.Load().Weather.OpenMeteoModel; got != "ecmwf_ifs" {
		t.Fatalf("model before reload = %q", got)
	}
	for _, bad := range []string{"nope", "ecmwf_ifs,gfs_seamless"} {
		if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"http://x"},"weather":{"open_meteo_model":"`+bad+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reload with %q = %d, want 200", bad, resp.StatusCode)
		}
		if got := app.cfg.Load().Weather.OpenMeteoModel; got != "best_match" {
			t.Errorf("model after reload with %q = %q, want best_match", bad, got)
		}
	}
}
