package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
	"github.com/tarakanof/ember/internal/pomodoro"
)

var knobCapsFaces = map[string]any{
	"view":     []int{1, 1},
	"pages":    []string{"bot", "weather"},
	"features": []string{"view_wait", "wx_faces"},
	"limits":   map[string]any{"view_bytes": 16383, "config_bytes": 1024},
}

type facesProviders struct {
	openMeteo, metNo string
	airOffset        int
	aqi              float64
}

func stubBody(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func facesViewFixture(t *testing.T, at time.Time, provider string, lat, lon float64, p facesProviders) *viewFixture {
	t.Helper()
	f := goldenViewFixture(t)
	f.clk.now = at
	f.app.weatherFetcher = newWeatherFetcher()
	f.app.weatherFetcher.openMeteoBase = stubBody(t, p.openMeteo)
	f.app.weatherFetcher.metNoBase = stubBody(t, p.metNo)
	airOn := p.aqi > 0
	if airOn {
		f.app.weatherFetcher.airQualityBase = stubBody(t, fmt.Sprintf(`{"utc_offset_seconds":%d,"current":{"european_aqi":%v}}`, p.airOffset, p.aqi))
	}
	f.app.updateConfig(func(c *Config) {
		w := &c.Weather
		w.Enabled, w.Provider = true, provider
		w.Latitude, w.Longitude = lat, lon
		w.PopupOnChange, w.PopupIntervalMinutes, w.SunPopups, w.SevereAlert = boolPtr(false), intPtr(0), boolPtr(false), boolPtr(false)
		w.AirTile, w.AirPopupThreshold = boolPtr(airOn), 0
	})
	f.app.pollWeather(context.Background(), at)
	if !f.app.weather.state(f.app.cfg.Load().Weather, at).HaveObs {
		t.Fatal("stub provider gave no observation")
	}
	return f
}

func openMeteoFacesStub(now time.Time, offset int, wet func(i int) (mm15, snow float64)) string {
	var times, precip, snow []string
	for i := range 48 {
		mm, sn := wet(i)
		times = append(times, fmt.Sprint(now.Add(time.Duration(i)*15*time.Minute).Unix()))
		precip = append(precip, fmt.Sprint(mm))
		snow = append(snow, fmt.Sprint(sn))
	}
	y, m, d := now.Add(time.Duration(offset) * time.Second).Date()
	day0 := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-time.Duration(offset) * time.Second).Unix()
	return fmt.Sprintf(`{"utc_offset_seconds":%d,
"current":{"time":%d,"temperature_2m":15.4,"weather_code":61,"cloud_cover":94,"uv_index":3.4,"wind_speed_10m":21.3,"wind_gusts_10m":38.2,"wind_direction_10m":225},
"minutely_15":{"time":[%s],"precipitation":[%s],"snowfall":[%s]},
"hourly":{"time":[%d],"temperature_2m":[15.4]},
"daily":{"time":[%d,%d,%d,%d],"weather_code":[61,3,61,71],"temperature_2m_max":[14.2,16.4,17.1,2.5],"temperature_2m_min":[9.1,7.4,11,-3.6],
"precipitation_sum":[2.04,0,0.8,1.26],"precipitation_probability_max":[83,32,19,40],"uv_index_max":[4.1,2.8,2.9,1.2],"wind_gusts_10m_max":[41.3,37,25.6,34.9]}}`,
		offset, now.Unix(), strings.Join(times, ","), strings.Join(precip, ","), strings.Join(snow, ","), now.Unix(),
		day0, day0+86400, day0+2*86400, day0+3*86400)
}

func metFacesStub(now time.Time) string {
	var es []string
	for i := range 14 {
		at := now.Add(time.Duration(i) * time.Hour)
		sym, mm := "cloudy", 0.0
		switch i {
		case 2, 3:
			sym, mm = "lightrain", 0.6
		case 4:
			sym, mm = "rain", 3.0
		case 9:
			sym, mm = "lightsleet", 0.4
		}
		es = append(es, fmt.Sprintf(`{"time":"%s","data":{"instant":{"details":{"air_temperature":%v,"cloud_area_fraction":70,"ultraviolet_index_clear_sky":3.2,"wind_speed":4.2,"wind_from_direction":200}},
"next_1_hours":{"summary":{"symbol_code":"%s"},"details":{"precipitation_amount":%v}},
"next_6_hours":{"summary":{"symbol_code":"%s"},"details":{"air_temperature_min":%v,"air_temperature_max":%v,"precipitation_amount":1.2}}}}`,
			at.Format(time.RFC3339), 14+0.3*float64(i), sym, mm, sym, 11+0.2*float64(i), 16+0.2*float64(i)))
	}
	syms := []string{"partlycloudy_day", "rain", "heavyrainandthunder", "fog", "clearsky_day", "snow"}
	for i, at := 0, now.Truncate(24*time.Hour).Add(30*time.Hour); at.Before(now.Add(96 * time.Hour)); i, at = i+1, at.Add(6*time.Hour) {
		es = append(es, fmt.Sprintf(`{"time":"%s","data":{"instant":{"details":{"air_temperature":%v,"wind_speed":3}},
"next_6_hours":{"summary":{"symbol_code":"%s"},"details":{"air_temperature_min":%v,"air_temperature_max":%v,"precipitation_amount":%v}}}}`,
			at.Format(time.RFC3339), 10+float64(i%4), syms[i%len(syms)], 8+float64(i%3), 13+float64(i%5), 0.1*float64(i%4)))
	}
	return `{"properties":{"timeseries":[` + strings.Join(es, ",") + `]}}`
}

func openMeteoTwoSpells(i int) (float64, float64) {
	switch {
	case i < 4:
		return 0.3, 0
	case i == 4:
		return 0, 0
	case i < 8:
		return 0.8, 0
	case i >= 20 && i < 24:
		return 0.2, 0.1
	}
	return 0, 0
}

func TestKnobViewWeatherFacesGolden(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	t.Run("open-meteo", func(t *testing.T) {
		f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
			facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells), airOffset: 3600, aqi: 42.4})
		postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
		assertDeviceGolden(t, "view_faces", goldenView(t, f))
	})
	t.Run("met-no", func(t *testing.T) {
		f := facesViewFixture(t, june, "met-no", lonLat, lonLon, facesProviders{metNo: metFacesStub(june)})
		postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
		assertDeviceGolden(t, "view_faces_met", goldenView(t, f))
	})
	t.Run("polar dry", func(t *testing.T) {
		sept := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		dry := func(int) (float64, float64) { return 0, 0 }
		f := facesViewFixture(t, sept, "open-meteo", 78.22, 15.65, facesProviders{openMeteo: openMeteoFacesStub(sept, 7200, dry)})
		postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
		assertDeviceGolden(t, "view_faces_polar_dry", goldenView(t, f))
	})
}

type facesWire struct {
	Weather struct {
		UTCOffset *int64           `json:"utc_offset"`
		Now       map[string]int   `json:"now"`
		Today     map[string]int   `json:"today"`
		Rain      *knobWxRain      `json:"rain"`
		Days      []map[string]any `json:"days"`
		Twilight  map[string]int64 `json:"twilight"`
		Moon      *struct {
			Phase, Illum int
			Up           *bool
			Rise, Set    *int64
		} `json:"moon"`
		AQI *int `json:"aqi"`
	} `json:"weather"`
}

func decodeFaces(t *testing.T, body []byte) facesWire {
	t.Helper()
	var v facesWire
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestKnobViewWeatherFacesContent(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells), airOffset: 3600, aqi: 42.4})
	postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
	w := decodeFaces(t, goldenView(t, f)).Weather
	if w.UTCOffset == nil || *w.UTCOffset != 3600 {
		t.Errorf("utc_offset = %v, want 3600", w.UTCOffset)
	}
	wantNow := map[string]int{"cloud": 94, "uv": 3, "wind_kmh": 21, "gust_kmh": 38, "wind_dir": 225, "rain": 1}
	if fmt.Sprint(w.Now) != fmt.Sprint(wantNow) {
		t.Errorf("now = %v, want %v", w.Now, wantNow)
	}
	wantToday := map[string]int{"min_c": 9, "max_c": 15, "uv_max": 4, "gust_max_kmh": 41, "pop": 83}
	if fmt.Sprint(w.Today) != fmt.Sprint(wantToday) {
		t.Errorf("today = %v, want %v (max widened to temp_c 15.4)", w.Today, wantToday)
	}
	n := june.Unix()
	q := int64(15 * 60)
	wantSpells := [][4]int64{{n - q, n + 7*q, 2, 0}, {n + 19*q, n + 23*q, 1, 1}}
	if w.Rain == nil || w.Rain.Until == nil || *w.Rain.Until != n+7*q || !slices.Equal(w.Rain.Spells, wantSpells) {
		t.Errorf("rain = %+v, want until %d and spells %v (each 15-min sum covers the quarter before its stamp)", w.Rain, n+7*q, wantSpells)
	}
	if len(w.Days) != 3 || w.Days[0]["precip_mm10"] != float64(0) || w.Days[2]["min_c"] != float64(-4) || w.Days[2]["cond"] != "snow" {
		t.Errorf("days = %v, want the 3 days after today, rounded", w.Days)
	}
	if m := w.Moon; len(w.Twilight) != 6 || m == nil || m.Up == nil || !*m.Up || m.Rise == nil || *m.Rise > n || m.Set == nil || *m.Set <= n {
		t.Errorf("twilight %v moon %+v, want six crossings and the moon up since rise until set", w.Twilight, w.Moon)
	}
	if w.AQI == nil || *w.AQI != 42 {
		t.Errorf("aqi = %v, want 42", w.AQI)
	}
}

func TestKnobViewLeavesOutRainWithoutDataForNow(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells)})
	postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
	f.clk.now = june.Add(13 * time.Hour)
	body := goldenView(t, f)
	w := decodeFaces(t, body).Weather
	if w.Rain != nil || w.Now["rain"] != 0 {
		t.Errorf("13 h after the last fetch: rain %+v now.rain %v, want rain left out, not a dry forecast", w.Rain, w.Now["rain"])
	}
	if w.Today == nil || len(w.Days) != 2 {
		t.Errorf("today %v days %v, want 22 June as today and the two after it", w.Today, w.Days)
	}
	if !strings.Contains(string(body), `"stale":true`) {
		t.Errorf("setup: view not stale: %s", body)
	}
}

func TestKnobViewMoonUpTellsPolarUpFromDown(t *testing.T) {
	dry := func(int) (float64, float64) { return 0, 0 }
	for _, c := range []struct {
		name string
		at   time.Time
		up   bool
	}{
		{"up for days", time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC), true},
		{"down for days", time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := facesViewFixture(t, c.at, "open-meteo", 78.22, 15.65, facesProviders{openMeteo: openMeteoFacesStub(c.at, 7200, dry)})
			postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
			m := decodeFaces(t, goldenView(t, f)).Weather.Moon
			if m == nil || m.Up == nil || *m.Up != c.up || m.Rise != nil || m.Set != nil {
				t.Fatalf("moon = %+v, want up %v with neither rise nor set", m, c.up)
			}
		})
	}
}

func TestCapsKnobWithoutWxFacesGetsNoWeatherDetail(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells), airOffset: 3600, aqi: 42.4})
	legacy := goldenView(t, f)
	caps := map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "pomodoro", "weather"}, "features": []string{"view_wait"}}
	postCapsCheckin(t, f, "0.10.0", caps)
	body := goldenView(t, f)
	for _, b := range [][]byte{legacy, body} {
		if strings.Contains(string(b), `"utc_offset"`) || strings.Contains(string(b), `"spells"`) {
			t.Fatalf("view without wx_faces carries weather detail: %s", b)
		}
	}
	if string(viewKeys(t, body)["weather"]) != string(viewKeys(t, legacy)["weather"]) {
		t.Errorf("weather block differs between legacy and caps-without-token:\n%s\n%s", viewKeys(t, legacy)["weather"], viewKeys(t, body)["weather"])
	}
}

func TestCapsKnobWithWxFacesButWeatherPageOffGetsNoWeather(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells)})
	postCapsCheckin(t, f, "0.10.0", knobCapsFaces)
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"weather","on":false}]}`)
	if _, ok := viewKeys(t, goldenView(t, f))["weather"]; ok {
		t.Fatal("weather detail sent with the weather page off")
	}
}

func TestCapsKnobWithWeatherHiddenSkipsBuildingTheDetail(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells)})
	allocs := func(features ...string) float64 {
		plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: []string{"bot"}, Features: features})
		return testing.AllocsPerRun(20, func() {
			if _, _, err := f.app.knobView(f.m.ID, june); err != nil {
				t.Fatal(err)
			}
		})
	}
	plain, faces := allocs("view_wait", "coredump"), allocs("view_wait", featureWxFaces)
	if faces > plain {
		t.Errorf("weather page not in caps: %v allocs with wx_faces, %v without; the hidden detail was built", faces, plain)
	}
}

func TestCapsKnobViewDropsWeatherDetailFirst(t *testing.T) {
	june := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	f := facesViewFixture(t, june, "open-meteo", lonLat, lonLon,
		facesProviders{openMeteo: openMeteoFacesStub(june, 3600, openMeteoTwoSpells), airOffset: 3600, aqi: 42.4})
	f.eng.Start(pomodoro.PhaseFocus)
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
	rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: strings.Repeat("<", 200),
		Artist: strings.Repeat("<", 200), Album: strings.Repeat("<", 200), TrackID: "4242", DurationMS: 330_000}
	if _, err := f.app.nowPlaying.reg.Report(rep, june); err != nil {
		t.Fatal(err)
	}
	pages := []string{"bot", "pomodoro", "weather", "nowplaying"}
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{featureWxFaces}})
	full := goldenView(t, f)
	if !strings.Contains(string(full), `"utc_offset"`) {
		t.Fatalf("setup: full view without detail: %s", full)
	}
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: pages, Features: []string{featureWxFaces}, Limits: &deviceCapsLimits{ViewBytes: len(full) - 1}})
	body := goldenView(t, f)
	keys := viewKeys(t, body)
	if strings.Contains(string(body), `"utc_offset"`) || keys["nowplaying"] == nil || keys["weather"] == nil {
		t.Fatalf("over the limit by 1 B: %s, want only the weather detail dropped", body)
	}
}

func TestKnobViewWorstCaseFitsHalfTheBuffer(t *testing.T) {
	f := goldenViewFixture(t)
	now := f.clk.Now()
	long := strings.Repeat("W", 64)
	color := "#00C8C8"
	f.app.Upsert(StatusRequest{Source: long, Tool: "claude", Session: "s1", State: "waiting", SourceColor: &color})
	f.app.Upsert(StatusRequest{Source: long + "2", Tool: "claude", Session: "s2", State: "waiting"})
	f.app.Upsert(StatusRequest{Source: long + "2", Tool: "claude", Session: "s3", State: "waiting"})
	f.eng.Start(pomodoro.PhaseFocus)
	esc := strings.Repeat("<", 200)
	npSource := strings.Repeat("w", 32)
	rep := nowplaying.Report{Source: npSource, Player: long, State: nowplaying.Playing, Title: esc, Artist: esc, Album: esc,
		TrackID: strings.Repeat("9", 128), DurationMS: 3_600_000, PositionMS: 1_800_000, Volume: new(100)}
	if _, err := f.app.nowPlaying.reg.Report(rep, now); err != nil {
		t.Fatal(err)
	}
	if err := f.app.nowPlaying.reg.SetArt(npSource, long, rep.TrackID, nowplaying.Album, nowplaying.NewImage([]byte("album"))); err != nil {
		t.Fatal(err)
	}
	f.app.updateConfig(func(c *Config) {
		c.Weather.Enabled, c.Weather.Provider = true, "met-no"
		c.Weather.Latitude, c.Weather.Longitude = -45.8788, -170.5028
		c.QuietHours = QuietHoursConfig{Enabled: true, Start: "11:00", End: "13:00"}
	})
	code := "lightssleetshowersandthunder_polartwilight"
	neg := func(v float64) *float64 { return &v }
	obs := weatherObservation{Condition: "storm", ConditionCode: code, TempC: -12.5, Severe: true,
		ObservedAt: now.Add(-time.Hour), FetchedAt: now, TZKnown: true, TZOffsetSeconds: -34200,
		Now:        weatherNow{Cloud: neg(100), UV: neg(11), WindKmh: neg(150.4), GustKmh: neg(199.4), WindDir: neg(359)},
		PrecipStep: time.Hour}
	for i := range 12 {
		mm := 0.01
		if i%2 == 0 {
			mm = 20
		}
		obs.Precip = append(obs.Precip, precipStep{At: now.Add(time.Duration(i) * time.Hour), MMh: mm, Snow: true})
	}
	y, m, d := now.Add(-34200 * time.Second).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(34200 * time.Second)
	for i := range 4 {
		obs.Days = append(obs.Days, weatherDay{Start: start.AddDate(0, 0, i), Cond: "storm", Code: code, MinC: neg(-45.5), MaxC: neg(-30.5),
			PrecipMM: neg(999.9), Pop: neg(100), UVMax: neg(11), GustMaxKmh: neg(199)})
	}
	f.app.weather.switchSource(f.app.cfg.Load().Weather.source())
	f.app.weather.obs, f.app.weather.have = obs, true
	f.app.weather.air, f.app.weather.haveAir = airObservation{AQI: 500, FetchedAt: now}, true
	putKnobConfig(t, f.srv, f.m.ID, `{"diagnostics":"full","pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":true}]}`)
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":300}`)
	mustOK(t, "live", resp, b)
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: []string{"bot", "pomodoro", "weather", "nowplaying"},
		Features: []string{featureWxFaces}, Limits: &deviceCapsLimits{ViewBytes: 16383}})
	body := goldenView(t, f)
	w := decodeFaces(t, body).Weather
	if w.Rain == nil || w.Rain.Until == nil || len(w.Rain.Spells) != rainSpellsMax || len(w.Days) != faceForecastDays || w.AQI == nil ||
		len(w.Now) != 6 || len(w.Today) != 5 || len(w.Twilight) != 6 || w.UTCOffset == nil || *w.UTCOffset >= 0 ||
		w.Moon == nil || w.Moon.Up == nil || w.Moon.Rise == nil || w.Moon.Set == nil {
		t.Fatalf("worst case lacks parts: %s", body)
	}
	for _, d := range w.Days {
		if len(d) != 7 || d["min_c"].(float64) >= 0 {
			t.Fatalf("worst case day lacks keys or negatives: %v", d)
		}
	}
	for _, k := range []string{"mood", "pomo", "nowplaying", "quiet", "diag_live_until"} {
		if _, ok := viewKeys(t, body)[k]; !ok {
			t.Fatalf("worst case lacks %q: %s", k, body)
		}
	}
	t.Logf("worst-case view: %d B (weather %d B)", len(body), len(viewKeys(t, body)["weather"]))
	if len(body) >= 8192 {
		t.Fatalf("worst-case view is %d B, want under half of the knob's 16 KB buffer", len(body))
	}
}
