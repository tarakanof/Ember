package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

func restartedWAApp(t *testing.T, st *waStub, db string, edit func(*weather.Config)) *App {
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
	if kw := b.knobWeatherFor(now, true); kw == nil || kw.Provider != weather.WeatherAPI {
		t.Errorf("knob weather after restore = %+v, want the restored reading", kw)
	}
}

func TestWeatherSnapshotIgnoredWhenStaleOrForAnotherSource(t *testing.T) {
	cases := map[string]struct {
		after time.Duration
		edit  func(*weather.Config)
	}{
		"stale":          {after: weatherStaleAfter + time.Hour},
		"other location": {after: 2 * time.Minute, edit: func(c *weather.Config) { c.Latitude += 1 }},
		"other provider": {after: 2 * time.Minute, edit: func(c *weather.Config) { c.Provider = "open-meteo" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := newWAStub(t, waFixture(t))
			t0 := waT0.Add(time.Minute)
			db := seededWeatherStore(t, st, t0)
			b := restartedWAApp(t, st, db, tc.edit)
			b.weatherFetcher.OpenMeteoBase, b.weatherFetcher.AirQualityBase = st.srv.URL, st.srv.URL
			now := t0.Add(tc.after)
			b.pollWeather(context.Background(), now)
			if wx := b.weather.state(b.cfg.Load().Weather, now); wx.HaveObs {
				t.Errorf("state has an observation fetched %v, want none", wx.Obs.FetchedAt)
			}
		})
	}
}

func TestWeatherSnapshotRestoreNudgesTheClock(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	t0 := waT0.Add(time.Minute)
	db := seededWeatherStore(t, st, t0)
	b := restartedWAApp(t, st, db, nil)
	b.pollWeather(context.Background(), t0.Add(2*time.Minute))
	if len(b.coord.ticks) != 1 {
		t.Error("restored weather queued no coordinator tick, so the clock waits for its next tick")
	}
}

func TestWeatherSnapshotRestoredBeforeTheFirstSunPopup(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	day := time.Date(waT0.Year(), waT0.Month(), waT0.Day(), 0, 0, 0, 0, time.UTC)
	rise, _, ok := weather.SunTimes(waBelgrade.Latitude, waBelgrade.Longitude, day).Pair()
	if !ok {
		t.Fatal("no sunrise")
	}
	db := seededWeatherStore(t, st, rise.Add(-5*time.Minute))
	b := restartedWAApp(t, st, db, func(c *weather.Config) { c.SunPopups = boolPtr(true) })
	b.pollWeather(context.Background(), rise)
	b.weather.mu.RLock()
	want := "SUNRISE " + weather.SunClock(rise, b.weather.obs, waBelgrade.Longitude)
	b.weather.mu.RUnlock()
	if want == "SUNRISE "+weather.SunClock(rise, weather.Observation{}, waBelgrade.Longitude) {
		t.Fatal("fixture offset equals the longitude guess; the test can't tell them apart")
	}
	pub := b.publisher.(*recordingPublisher)
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.notify) != 1 || pub.notify[0]["text"] != want {
		t.Errorf("sun popups %v, want one %q in the restored zone", pub.notify, want)
	}
}

func TestWeatherSnapshotIgnoredForAnotherModel(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	t0 := waT0.Add(time.Minute)
	db := seededWeatherStore(t, st, t0)
	b := restartedWAApp(t, st, db, nil)
	src := b.cfg.Load().Weather.Source()
	other := src
	other.Model = "ecmwf_ifs025"
	b.weather.switchSource(other)
	b.restoreWeatherSnapshot(other, t0.Add(time.Minute))
	if _, have, _, _ := b.weather.snapshot(other); have {
		t.Fatal("restored a snapshot saved under another model")
	}
	b.weather.snapshotLoaded = false
	b.weather.switchSource(src)
	b.restoreWeatherSnapshot(src, t0.Add(time.Minute))
	if _, have, _, _ := b.weather.snapshot(src); !have {
		t.Fatal("did not restore under the saved source")
	}
}

func TestWeatherSnapshotKeepsMETRevalidation(t *testing.T) {
	db := t.TempDir() + "/s.db"
	t0 := metT0
	st := &metStub{lastModified: "Sun, 21 Jun 2026 19:55:00 GMT", expires: t0.Add(25 * time.Minute)}
	a := newMetCacheApp(t, st, false)
	if err := a.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a.pollWeather(ctx, t0)
	st.mu.Lock()
	st.notModified = true
	st.expires = t0.Add(60 * time.Minute)
	st.mu.Unlock()
	a.pollWeather(ctx, t0.Add(30*time.Minute))
	if st.hits.Load() != 2 {
		t.Fatalf("setup: %d requests, want a 200 then a 304", st.hits.Load())
	}

	b := newMetCacheApp(t, st, false)
	if err := b.ensureStore(db); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(80 * time.Minute)
	b.pollWeather(ctx, now)
	wx := b.weather.state(b.cfg.Load().Weather, now)
	if !wx.HaveObs || !wx.Fresh || !wx.Obs.FetchedAt.Equal(now) {
		t.Fatalf("after restart: have %v fresh %v fetched %v, want the restored observation renewed by a 304", wx.HaveObs, wx.Fresh, wx.Obs.FetchedAt)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.hits.Load() != 3 || st.ims[2] != st.lastModified {
		t.Errorf("restart request: hits %d If-Modified-Since %q, want a revalidation with %q", st.hits.Load(), st.ims, st.lastModified)
	}
}
