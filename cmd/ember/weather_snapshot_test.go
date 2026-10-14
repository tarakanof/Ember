package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func restartedWAApp(t *testing.T, st *waStub, db string, edit func(*WeatherConfig)) *App {
	t.Helper()
	b := newWAApp(t, st)
	if edit != nil {
		cur := *b.cfg.Load()
		edit(&cur.Weather)
		b.cfg.Store(&cur)
	}
	if err := b.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	return b
}

func seededWeatherStore(t *testing.T, st *waStub, t0 time.Time) string {
	t.Helper()
	db := t.TempDir() + "/s.db"
	a := newWAApp(t, st)
	if err := a.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	a.pollWeather(context.Background(), t0)
	if !a.weather.state(a.cfg.Load().Weather, t0).Fresh {
		t.Fatal("first fetch left no fresh weather")
	}
	v, ok, _ := a.store.GetSetting(weatherSnapshotKey)
	if !ok || strings.Contains(v, "SECRET") {
		t.Fatalf("snapshot row = %q, want the observation without the key", v)
	}
	st.status.Store(http.StatusInternalServerError)
	return db
}

func TestWeatherSnapshotRestoresAfterRestartUnderTheQuota(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	t0 := waT0.Add(time.Minute)
	db := seededWeatherStore(t, st, t0)
	b := restartedWAApp(t, st, db, nil)
	now := t0.Add(2 * time.Minute)
	b.pollWeather(context.Background(), now)
	if st.hits.Load() != 1 {
		t.Errorf("%d requests, want 1: the restart must wait out the quota floor", st.hits.Load())
	}
	wx := b.weather.state(b.cfg.Load().Weather, now)
	if !wx.HaveObs || !wx.Fresh || !wx.Obs.FetchedAt.Equal(t0) || len(wx.Obs.Days) == 0 || len(wx.Obs.Hourly) == 0 {
		t.Fatalf("restored state = have %v fresh %v fetched %v days %d hourly %d, want the saved observation", wx.HaveObs, wx.Fresh, wx.Obs.FetchedAt, len(wx.Obs.Days), len(wx.Obs.Hourly))
	}
	if kw := b.knobWeatherFor(now, true); kw == nil || kw.Provider != weatherAPI {
		t.Errorf("knob weather after restore = %+v, want the restored reading", kw)
	}
}

func TestWeatherSnapshotIgnoredWhenStaleOrForAnotherSource(t *testing.T) {
	cases := map[string]struct {
		after time.Duration
		edit  func(*WeatherConfig)
	}{
		"stale":          {after: weatherStaleAfter + time.Hour},
		"other location": {after: 2 * time.Minute, edit: func(c *WeatherConfig) { c.Latitude += 1 }},
		"other provider": {after: 2 * time.Minute, edit: func(c *WeatherConfig) { c.Provider = "open-meteo" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := newWAStub(t, waFixture(t))
			t0 := waT0.Add(time.Minute)
			db := seededWeatherStore(t, st, t0)
			b := restartedWAApp(t, st, db, tc.edit)
			b.weatherFetcher.openMeteoBase, b.weatherFetcher.airQualityBase = st.srv.URL, st.srv.URL
			now := t0.Add(tc.after)
			b.pollWeather(context.Background(), now)
			if wx := b.weather.state(b.cfg.Load().Weather, now); wx.HaveObs {
				t.Errorf("state has an observation fetched %v, want none", wx.Obs.FetchedAt)
			}
		})
	}
}
