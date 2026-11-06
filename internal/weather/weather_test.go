package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

var (
	lonLat, lonLon = 51.5, -0.12
)

func mustJSONHave(t *testing.T, blob []byte, wants ...string) {
	t.Helper()
	s := string(blob)
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("marshalled config missing %q in %s", w, s)
		}
	}
	if strings.Contains(s, "null") {
		t.Errorf("marshalled config contains null: %s", s)
	}
}

func TestWeatherFileFalseSticks(t *testing.T) {
	src := `{
		"enabled": true, "rotate_in_apps": false, "forecast_tile": false,
		"popup_on_change": false, "severe_alert": false, "sun_popups": false,
		"moon_phase": false, "air_tile": false, "overlay": false,
		"popup_interval_minutes": 0
	}`
	var c Config
	if err := json.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	c.ApplyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob,
		`"rotate_in_apps":false`, `"forecast_tile":false`, `"popup_on_change":false`,
		`"severe_alert":false`, `"sun_popups":false`, `"moon_phase":false`,
		`"air_tile":false`, `"overlay":false`, `"popup_interval_minutes":0`)
}

func TestWeatherFileAbsentDefaults(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{}`), &c); err != nil {
		t.Fatal(err)
	}
	c.ApplyDefaults()
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	mustJSONHave(t, blob,
		`"rotate_in_apps":true`, `"forecast_tile":true`, `"popup_on_change":true`,
		`"severe_alert":true`, `"sun_popups":true`, `"moon_phase":true`,
		`"air_tile":true`, `"popup_interval_minutes":120`)
}

func TestWeatherFetchersStampHourlyStartAndCode(t *testing.T) {
	om := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("timeformat") != "unixtime" {
			t.Errorf("open-meteo timeformat = %q, want unixtime", r.URL.Query().Get("timeformat"))
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":9,"weather_code":61},"hourly":{"time":[1790409600,1790413200],"temperature_2m":[9,10]}}`))
	}))
	defer om.Close()
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"properties":{"timeseries":[{"time":"2026-09-26T07:00:00Z","data":{"instant":{"details":{"air_temperature":8}},"next_1_hours":{"summary":{"symbol_code":"rain_showers_day"}}}}]}}`))
	}))
	defer met.Close()
	wf := NewFetcher()
	wf.OpenMeteoBase, wf.MetNoBase = om.URL, met.URL

	obs, err := wf.FetchOpenMeteo(t.Context(), Config{Latitude: 1, Longitude: 2})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ConditionCode != "61" || obs.HourlyStart.Unix() != 1790409600 {
		t.Errorf("open-meteo code/start = %q/%v", obs.ConditionCode, obs.HourlyStart)
	}
	res, err := wf.fetchMetNo(t.Context(), Config{Latitude: 1, Longitude: 2}, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	obs = res.Obs
	if obs.ConditionCode != "rain_showers_day" || !obs.HourlyStart.Equal(time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("met-no code/start = %q/%v", obs.ConditionCode, obs.HourlyStart)
	}
}

func precipSeries(start time.Time, step time.Duration, mmh []float64, snow ...int) []PrecipStep {
	out := make([]PrecipStep, len(mmh))
	for i, v := range mmh {
		out[i] = PrecipStep{At: start.Add(time.Duration(i) * step), MMh: v}
	}
	for _, i := range snow {
		out[i].Snow = true
	}
	return out
}

func TestRainSpells(t *testing.T) {
	t0 := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	q := 15 * time.Minute
	at := func(steps int) time.Time { return t0.Add(time.Duration(steps) * q) }
	cases := []struct {
		name      string
		steps     []PrecipStep
		step      time.Duration
		now       time.Time
		wantNow   int
		haveNow   bool
		wantUntil time.Time
		want      []RainSpell
	}{
		{name: "dry", steps: precipSeries(t0, q, []float64{0, 0.05, 0}), step: q, now: t0, haveNow: true, want: []RainSpell{}},
		{name: "a 30-min gap merges, peak intensity", step: q, now: at(1), haveNow: true, wantNow: 1, wantUntil: at(6),
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0, 8, 3, 0}),
			want:  []RainSpell{{Start: at(0), End: at(6), Intensity: 3}}},
		{name: "a 45-min gap splits", step: q, now: at(2), haveNow: true,
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0, 0, 3}),
			want:  []RainSpell{{Start: at(5), End: at(6), Intensity: 2}}},
		{name: "snow when most wet steps are snow", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(3 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{0.5, 0.5, 0.5, 0}, 1, 2),
			want:  []RainSpell{{Start: t0, End: t0.Add(3 * time.Hour), Intensity: 1, Snow: true}}},
		{name: "half snow is rain", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(2 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{0.5, 0.5, 0}, 1),
			want:  []RainSpell{{Start: t0, End: t0.Add(2 * time.Hour), Intensity: 1}}},
		{name: "beyond 12 h and after four are left out", step: 3 * time.Hour, now: t0, haveNow: true,
			steps: precipSeries(t0, 3*time.Hour, []float64{0, 1, 0, 1, 0, 1, 0, 1, 0, 1}),
			want: []RainSpell{{Start: t0.Add(3 * time.Hour), End: t0.Add(6 * time.Hour), Intensity: 1},
				{Start: t0.Add(9 * time.Hour), End: t0.Add(12 * time.Hour), Intensity: 1}}},
		{name: "no step covers now", step: time.Hour, now: t0.Add(-2 * time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{1}),
			want:  []RainSpell{{Start: t0, End: t0.Add(time.Hour), Intensity: 1}}},
		{name: "an ended spell is dropped", step: q, now: at(3), haveNow: true,
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0}), want: []RainSpell{}},
		{name: "a spell running to the data edge has no until", step: q, now: t0, haveNow: true, wantNow: 1,
			steps: precipSeries(t0, q, []float64{1, 1}),
			want:  []RainSpell{{Start: at(0), End: at(2), Intensity: 1}}},
		{name: "a spell starting exactly at now+12h is left out", step: time.Hour, now: t0, haveNow: true,
			steps: precipSeries(t0, time.Hour, []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0}),
			want:  []RainSpell{}},
		{name: "now in a merged dry gap: listed, but dry now and no until", step: q, now: at(2), haveNow: true,
			steps: precipSeries(t0, q, []float64{1, 1, 0, 0, 1, 0}),
			want:  []RainSpell{{Start: at(0), End: at(5), Intensity: 1}}},
		{name: "max four", step: time.Hour, now: t0, haveNow: true, wantNow: 1, wantUntil: t0.Add(time.Hour),
			steps: precipSeries(t0, time.Hour, []float64{1, 0, 1, 0, 1, 0, 1, 0, 1}),
			want: []RainSpell{{Start: t0, End: t0.Add(time.Hour), Intensity: 1}, {Start: t0.Add(2 * time.Hour), End: t0.Add(3 * time.Hour), Intensity: 1},
				{Start: t0.Add(4 * time.Hour), End: t0.Add(5 * time.Hour), Intensity: 1}, {Start: t0.Add(6 * time.Hour), End: t0.Add(7 * time.Hour), Intensity: 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rainNow, haveNow, until, spells := RainSpells(c.steps, c.step, c.now)
			if rainNow != c.wantNow || haveNow != c.haveNow || !until.Equal(c.wantUntil) {
				t.Errorf("now = %d (have %v), until %v; want %d (have %v), until %v", rainNow, haveNow, until, c.wantNow, c.haveNow, c.wantUntil)
			}
			if spells == nil || len(spells) != len(c.want) {
				t.Fatalf("spells = %#v, want %+v", spells, c.want)
			}
			for i := range spells {
				if spells[i] != c.want[i] {
					t.Errorf("spell %d = %+v, want %+v", i, spells[i], c.want[i])
				}
			}
		})
	}
}

func TestRainIntensityBands(t *testing.T) {
	for _, c := range []struct {
		mmh  float64
		want int
	}{{0, 0}, {0.099, 0}, {0.1, 1}, {2.49, 1}, {2.5, 2}, {7.59, 2}, {7.6, 3}, {50, 3}} {
		if got := rainIntensity(c.mmh); got != c.want {
			t.Errorf("rainIntensity(%v) = %d, want %d", c.mmh, got, c.want)
		}
	}
}

func TestFaceDaysWidenTodayToTheCurrentTemperature(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	day := func(d int, lo, hi float64) Day {
		return Day{Start: time.Date(2026, 6, 21+d, 0, 0, 0, 0, time.UTC), MinC: &lo, MaxC: &hi}
	}
	obs := Observation{TempC: 21, Days: []Day{day(-1, 0, 1), day(0, 9, 14), day(1, 1, 2), day(2, 1, 2), day(3, 1, 2), day(4, 1, 2)}}
	today, next := FaceDays(obs, now)
	if today == nil || *today.MinC != 9 || *today.MaxC != 21 {
		t.Fatalf("today = %+v, want 9..21", today)
	}
	if *obs.Days[1].MaxC != 14 {
		t.Error("widening wrote through to the stored observation")
	}
	if len(next) != 3 || !next[0].Start.Equal(obs.Days[2].Start) {
		t.Errorf("next days = %+v, want the 3 after today", next)
	}
	obs.TempC = -3
	if today, _ = FaceDays(obs, now); *today.MinC != -3 || *today.MaxC != 14 {
		t.Errorf("cold today = %v..%v, want -3..14", *today.MinC, *today.MaxC)
	}
	if today, next = FaceDays(Observation{TempC: 5}, now); today != nil || next != nil {
		t.Errorf("no days: today %+v next %+v, want none", today, next)
	}
	if today, next = FaceDays(obs, now.AddDate(0, 0, 5)); today != nil || next != nil {
		t.Errorf("every day over: today %+v next %+v, want none rather than the last stale day", today, next)
	}
}

func TestSolarElevationMatchesSunTimesHorizon(t *testing.T) {
	for _, c := range [][2]float64{{52.52, 13.405}, {34.05, -118.24}, {-33.87, 151.21}, {69.65, 18.96}, {-77.8, 166.7}} {
		for d := 0; d < 365; d += 30 {
			rise, set, ok := SunTimes(c[0], c[1], time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, d)).Pair()
			if !ok {
				continue
			}
			for _, at := range []time.Time{rise, set} {
				if e := SolarElevation(c[0], c[1], at); e < SunHorizonDeg-0.05 || e > SunHorizonDeg+0.05 {
					t.Errorf("%v at %v: elevation %.3f°, want ≈ %.3f°", c, at, e, SunHorizonDeg)
				}
			}
		}
	}
}

func sunYearStep(t *testing.T) time.Duration {
	if testing.Short() {
		return 97 * time.Minute
	}
	return 7 * time.Minute
}

const sunWiringEvery = 10

func TestSunNightAtNormalLatitudesStaysOnTheEventBracket(t *testing.T) {
	t.Parallel()
	sites := map[string][2]float64{"Berlin": {52.52, 13.405}, "LA": {34.05, -118.24}, "Sydney": {-33.87, 151.21}}
	step := sunYearStep(t)
	for name, c := range sites {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			evs := sunEventsByDate{lat: c[0], lon: c[1]}
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; at.Year() == 2026; i, at = i+1, at.Add(step) {
				last, next := SunBracket(evs.at(at), at)
				if last == nil || next == nil || last.Rise == next.Rise {
					t.Fatalf("%v: bracket %+v..%+v, want a sunrise and a sunset on either side", at, last, next)
				}
				if NightBetween(last, next, c[0], c[1], at) != !last.Rise {
					t.Fatalf("%v: night must be the bracket's call", at)
				}
				if i%sunWiringEvery == 0 && SunNight(c[0], c[1], at) != !last.Rise {
					t.Fatalf("%v: sunNight is not the bracket's call", at)
				}
			}
		})
	}
}

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

func TestWMOOverlay(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{0, ""}, {3, ""}, {45, ""},
		{48, render.OverlayFrost},
		{51, render.OverlayDrizzle}, {57, render.OverlayDrizzle},
		{61, render.OverlayRain}, {63, render.OverlayRain}, {66, render.OverlayRain}, {80, render.OverlayRain},
		{65, render.OverlayStorm}, {67, render.OverlayStorm}, {82, render.OverlayStorm},
		{71, render.OverlaySnow}, {77, render.OverlaySnow}, {86, render.OverlaySnow},
		{95, render.OverlayThunder}, {99, render.OverlayThunder},
	}
	for _, c := range cases {
		if got := wmoOverlay(c.code); got != c.want {
			t.Errorf("wmoOverlay(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestMetSymbolOverlay(t *testing.T) {
	cases := []struct {
		sym  string
		want string
	}{
		{"clearsky_day", ""}, {"cloudy", ""}, {"fog", ""},
		{"lightrain", render.OverlayRain},
		{"lightrainshowers_day", render.OverlayRain},
		{"rain", render.OverlayRain},
		{"rainshowers_night", render.OverlayRain},
		{"heavyrain", render.OverlayStorm},
		{"sleet", render.OverlayRain},
		{"lightsleetshowers_day", render.OverlayRain},
		{"heavysleet", render.OverlayStorm},
		{"heavysnowshowers_day", render.OverlaySnow},
		{"rainandthunder", render.OverlayThunder},
		{"lightssnowshowersandthunder_day", render.OverlayThunder},
	}
	for _, c := range cases {
		if got := metSymbolOverlay(c.sym); got != c.want {
			t.Errorf("metSymbolOverlay(%q) = %q, want %q", c.sym, got, c.want)
		}
	}
}

func TestFetchOpenMeteo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("latitude") == "" {
			t.Error("missing latitude query")
		}
		if r.URL.Query().Get("hourly") != "temperature_2m" {
			t.Error("open-meteo fetch must request hourly temperature_2m")
		}
		if r.URL.Query().Get("timezone") != "auto" {
			t.Error("open-meteo fetch must request timezone=auto")
		}
		w.Write([]byte(`{"utc_offset_seconds":7200,"current":{"time":1782036000,"temperature_2m":12.5,"weather_code":61},"hourly":{"temperature_2m":[12.5,13.0,13.5]}}`))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.OpenMeteoBase = srv.URL
	res, err := wf.Fetch(context.Background(), Config{Provider: "open-meteo", Latitude: 52.1, Longitude: 4.3}, FetchHint{})
	obs := res.Obs
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if obs.Condition != render.WeatherRain || obs.TempC != 12.5 {
		t.Errorf("obs = %+v, want rain/12.5", obs)
	}
	if len(obs.Hourly) != 3 || obs.Hourly[0] != 12.5 || obs.Hourly[2] != 13.5 {
		t.Errorf("hourly = %v, want [12.5 13 13.5]", obs.Hourly)
	}
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Errorf("tz = (%v,%d), want (true,7200)", obs.TZKnown, obs.TZOffsetSeconds)
	}
	if want := time.Unix(1782036000, 0); !obs.ObservedAt.Equal(want) {
		t.Errorf("observed at = %v, want %v", obs.ObservedAt, want)
	}
}

func TestFetchWithoutProviderTimeLeavesObservedAtZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "locationforecast") {
			w.Write([]byte(`{"properties":{"timeseries":[{"data":{"instant":{"details":{"air_temperature":3}}}}]}}`))
			return
		}
		w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0}}`))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.OpenMeteoBase, wf.MetNoBase = srv.URL, srv.URL
	for _, provider := range []string{"open-meteo", "met-no"} {
		res, err := wf.Fetch(context.Background(), Config{Provider: provider, Latitude: 52.1, Longitude: 4.3}, FetchHint{})
		obs := res.Obs
		if err != nil {
			t.Fatalf("%s fetch: %v", provider, err)
		}
		if !obs.ObservedAt.IsZero() {
			t.Errorf("%s observed at = %v, want zero", provider, obs.ObservedAt)
		}
	}
}

func TestFetchMetNoSendsUserAgent(t *testing.T) {
	gotUA := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`{"properties":{"timeseries":[{"time":"2026-06-21T10:00:00Z","data":{"instant":{"details":{"air_temperature":3.2}},"next_1_hours":{"summary":{"symbol_code":"heavysnow"}}}}]}}`))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.MetNoBase = srv.URL
	res, err := wf.Fetch(context.Background(), Config{Provider: "met-no", Latitude: 59.9, Longitude: 10.7}, FetchHint{})
	obs := res.Obs
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotUA == "" {
		t.Error("met.no requires a User-Agent; none sent")
	}
	if obs.Condition != render.WeatherSnow || !obs.Severe || obs.TempC != 3.2 {
		t.Errorf("obs = %+v, want severe snow / 3.2", obs)
	}
	if len(obs.Hourly) != 1 || obs.Hourly[0] != 3.2 {
		t.Errorf("met-no hourly = %v, want [3.2]", obs.Hourly)
	}
	if want := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC); !obs.ObservedAt.Equal(want) {
		t.Errorf("met-no observed at = %v, want %v", obs.ObservedAt, want)
	}
}

func TestWeatherIconIDOverride(t *testing.T) {
	cfg := Config{}
	if got := cfg.IconID(render.WeatherRain); got != "72" {
		t.Errorf("default rain icon = %q, want 72", got)
	}
	cfg.IconIDs = map[string]string{render.WeatherRain: "999"}
	if got := cfg.IconID(render.WeatherRain); got != "999" {
		t.Errorf("override rain icon = %q, want 999", got)
	}
	if got := cfg.IconID(render.WeatherSnow); got != "2289" {
		t.Errorf("unset snow icon = %q, want default 2289", got)
	}
}

func TestWeatherAirDefaults(t *testing.T) {
	var c Config
	c.ApplyDefaults()
	if !c.AirTileEnabled() {
		t.Error("air_tile should default on at file load")
	}
	if c.AirPopupThreshold != 80 {
		t.Errorf("air_popup_threshold default = %d, want 80", c.AirPopupThreshold)
	}
	neg := Config{AirPopupThreshold: -5}
	neg.ApplyDefaults()
	if neg.AirPopupThreshold != 0 {
		t.Errorf("negative threshold = %d, want clamped to 0 (off)", neg.AirPopupThreshold)
	}
	set := Config{AirPopupThreshold: 60}
	set.ApplyDefaults()
	if set.AirPopupThreshold != 60 {
		t.Errorf("explicit threshold clobbered: %d, want 60", set.AirPopupThreshold)
	}
}

func TestFetchAirQuality(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("latitude") == "" {
			t.Error("missing latitude query")
		}
		if q.Get("current") != "european_aqi,pm2_5,pm10" {
			t.Errorf("current = %q, want european_aqi,pm2_5,pm10", q.Get("current"))
		}
		if q.Get("hourly") != "european_aqi" {
			t.Error("air fetch must request hourly european_aqi")
		}
		w.Write([]byte(`{"current":{"european_aqi":38,"pm2_5":4.8,"pm10":6.5},"hourly":{"european_aqi":[38,39,40]}}`))
	}))
	defer srv.Close()
	wf := NewFetcher()
	wf.AirQualityBase = srv.URL
	obs, err := wf.FetchAirQuality(context.Background(), Config{Latitude: 44.8, Longitude: 20.5})
	if err != nil {
		t.Fatalf("fetchAirQuality: %v", err)
	}
	if obs.AQI != 38 || obs.PM25 != 4.8 || obs.PM10 != 6.5 {
		t.Errorf("obs = %+v, want AQI 38 / pm2.5 4.8 / pm10 6.5", obs)
	}
	if len(obs.HourlyAQI) != 3 || obs.HourlyAQI[2] != 40 {
		t.Errorf("hourly = %v, want [38 39 40]", obs.HourlyAQI)
	}
}
