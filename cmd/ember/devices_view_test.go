package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

const knobViewPath = "/v1/devices/self/view"

type viewFixture struct {
	app *App
	srv *httptest.Server
	clk *stepClock
	eng *pomodoro.Engine
	m   mintResp
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.RateLimit.Disabled = true })
	clk := &stepClock{now: time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)}
	eng := pomodoro.New(pomodoro.Settings{FocusMin: 25, ShortMin: 5, LongMin: 15, RoundsBeforeLong: 4}, clk)
	app.EnablePomodoro(eng, app.store)
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return &viewFixture{app: app, srv: srv, clk: clk, eng: eng, m: mintKnob(t, srv, http.StatusCreated)}
}

func (f *viewFixture) get(t *testing.T, etag string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", f.srv.URL+knobViewPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.m.Token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func TestKnobViewBodyIsCompactAndOrdered(t *testing.T) {
	f := newViewFixture(t)
	now := f.clk.Now()
	f.app.updateConfig(func(c *Config) {
		c.Weather.Enabled = true
		c.Weather.Provider = "open-meteo"
		c.Weather.Latitude, c.Weather.Longitude = lonLat, lonLon
	})
	f.app.weather.obs = weatherObservation{Condition: "rain", ConditionCode: "61", TempC: 12.5, FetchedAt: now.Add(-5 * time.Minute)}
	f.app.weather.have = true
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "waiting"})
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "codex", Session: "s2", State: "running"})
	f.eng.Start(pomodoro.PhaseFocus)

	body, etag, err := f.app.knobView(f.m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	rise, set, _ := sunTimes(lonLat, lonLon, now)
	want := fmt.Sprintf(`{"v":1,"epoch":1,"config_version":1,`+
		`"mood":{"waiting":1,"errors":0,"running":1,"done":0,"source":"M4","tool":"claude"},`+
		`"pomo":{"phase":"focus","running":true,"paused":false,"ends_at":%d,"planned_sec":1500,"round":0},`+
		`"weather":{"provider":"open-meteo","cond":"rain","code":"61","temp_c":12.5,"stale":false,"severe":false,"night":false,"sunrise":%d,"sunset":%d},`+
		`"brightness":{"level":255,"night":false}}`,
		now.Add(25*time.Minute).Unix(), rise.Round(sunRounding).Unix(), set.Round(sunRounding).Unix())
	if string(body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
	if len(etag) != 18 || etag[0] != '"' || etag[17] != '"' {
		t.Fatalf("etag = %s, want a quoted 16-hex strong tag", etag)
	}
	t.Logf("typical view: %d bytes", len(body))
}

func TestKnobViewIsStableWhileAPomodoroCountsDown(t *testing.T) {
	f := newViewFixture(t)
	f.eng.Start(pomodoro.PhaseFocus)
	a, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	f.clk.advance(7 * time.Second)
	b, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	if string(a) != string(b) {
		t.Fatalf("view changed with the clock alone:\n%s\n%s", a, b)
	}
}

func TestKnobViewPausedPomodoroCarriesRemaining(t *testing.T) {
	f := newViewFixture(t)
	f.eng.Start(pomodoro.PhaseFocus)
	f.clk.advance(100 * time.Second)
	f.eng.Pause(f.clk.Now())
	body, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	want := `"pomo":{"phase":"focus","running":true,"paused":true,"remaining_sec":1400,"planned_sec":1500,"round":0}`
	if !strings.Contains(string(body), want) {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestKnobViewNullsDisabledSections(t *testing.T) {
	f := newViewFixture(t)
	f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false; c.Weather.Enabled = false })
	f.app.weather.obs = weatherObservation{Condition: "rain", FetchedAt: f.clk.Now()}
	f.app.weather.have = true
	body, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	for _, want := range []string{`"pomo":null`, `"weather":null`, `"mood":{"waiting":0,"errors":0,"running":0,"done":0,"source":""}`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body = %s, want %s", body, want)
		}
	}
}

func TestKnobViewAnswers304OnMatchingETag(t *testing.T) {
	f := newViewFixture(t)
	resp, body := f.get(t, "")
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || etag == "" || len(body) == 0 {
		t.Fatalf("first GET = %d etag=%q body=%s", resp.StatusCode, etag, body)
	}
	if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Errorf("headers = %v", resp.Header)
	}
	if now, err := strconv.ParseInt(resp.Header.Get(knobNowHeader), 10, 64); err != nil || now < time.Now().Add(-time.Minute).Unix() {
		t.Errorf("%s = %q", knobNowHeader, resp.Header.Get(knobNowHeader))
	}
	for _, inm := range []string{etag, "W/" + etag, `"nope", ` + etag, "*"} {
		resp, body = f.get(t, inm)
		if resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get("ETag") != etag || resp.Header.Get(knobNowHeader) == "" {
			t.Fatalf("If-None-Match %s = %d etag=%q body=%q", inm, resp.StatusCode, resp.Header.Get("ETag"), body)
		}
	}
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "waiting"})
	resp, _ = f.get(t, etag)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") == etag {
		t.Fatalf("after a change = %d etag=%q, want 200 with a new tag", resp.StatusCode, resp.Header.Get("ETag"))
	}
}

func TestKnobViewReflectsDeviceConfigChange(t *testing.T) {
	f := newViewFixture(t)
	devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"home":"weather"}`)
	body, _, _ := f.app.knobView(f.m.ID, f.clk.Now())
	if !strings.Contains(string(body), `"epoch":2,"config_version":2,`) {
		t.Fatalf("body = %s, want epoch 2 and config_version 2", body)
	}
}

func TestKnobViewNeedsADeviceToken(t *testing.T) {
	f := newViewFixture(t)
	for _, tok := range []string{"", testToken, "ekd_forged"} {
		if resp, _ := devReq(t, f.srv, "GET", knobViewPath, tok, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q = %d, want 401", tok, resp.StatusCode)
		}
	}
}

func TestKnobViewNeverWritesStore(t *testing.T) {
	f := newViewFixture(t)
	kv := &countingKV{settingsKV: f.app.store}
	f.app.devices.kv = func() settingsKV { return kv }
	for range 3 {
		if resp, _ := f.get(t, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("view = %d", resp.StatusCode)
		}
	}
	if got := kv.puts.Load(); got != 0 {
		t.Fatalf("store writes = %d, want 0", got)
	}
	if seen := f.app.devices.list()[0].LastCheckin; seen != nil {
		t.Fatalf("view recorded a checkin: %+v", seen)
	}
}

func TestKnobViewWeatherNightWestOfGreenwich(t *testing.T) {
	f := newViewFixture(t)
	const sfLat, sfLon = 37.77, -122.42
	f.app.updateConfig(func(c *Config) {
		c.Weather.Enabled = true
		c.Weather.Latitude, c.Weather.Longitude = sfLat, sfLon
	})
	f.app.weather.have = true
	cases := []struct {
		name      string
		now       time.Time
		wantNight bool
	}{
		{"18:00 PDT, after UTC midnight, before sunset", time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC), false},
		{"19:30 PDT, after sunset", time.Date(2026, 10, 5, 2, 30, 0, 0, time.UTC), true},
		{"06:00 PDT, before sunrise", time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.app.weather.obs = weatherObservation{Condition: "clear", FetchedAt: tc.now, TZKnown: true, TZOffsetSeconds: -7 * 3600}
			w := f.app.knobWeather(tc.now)
			if w == nil || w.Night != tc.wantNight {
				t.Fatalf("weather = %+v, want night %v", w, tc.wantNight)
			}
			local := tc.now.Add(-7 * time.Hour)
			rise, set := time.Unix(*w.Sunrise, 0).Add(-7*time.Hour), time.Unix(*w.Sunset, 0).Add(-7*time.Hour)
			if rise.UTC().YearDay() != local.UTC().YearDay() || set.UTC().YearDay() != local.UTC().YearDay() {
				t.Fatalf("sun times %v / %v are not on the local date of %v", rise.UTC(), set.UTC(), local.UTC())
			}
		})
	}
}

func TestKnobViewWeatherObservedAt(t *testing.T) {
	f := newViewFixture(t)
	f.app.updateConfig(func(c *Config) { c.Weather.Enabled = true })
	f.app.weather.have = true
	weatherOf := func(t *testing.T) (map[string]json.RawMessage, string) {
		t.Helper()
		resp, b := f.get(t, "")
		mustOK(t, "view", resp, b)
		var view struct {
			Weather map[string]json.RawMessage `json:"weather"`
		}
		if err := json.Unmarshal(b, &view); err != nil {
			t.Fatal(err)
		}
		if view.Weather == nil {
			t.Fatalf("view has no weather: %s", b)
		}
		return view.Weather, string(b)
	}

	t.Run("present as Unix seconds when known", func(t *testing.T) {
		amsterdam := time.FixedZone("CEST", 2*3600)
		observed := time.Date(2026, 6, 21, 13, 45, 0, 500_000_000, amsterdam)
		f.app.weather.obs = weatherObservation{Condition: "rain", ObservedAt: observed, FetchedAt: f.clk.Now()}
		w, body := weatherOf(t)
		raw, ok := w["observed_at"]
		if !ok {
			t.Fatalf("observed_at missing: %s", body)
		}
		var got int64
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("observed_at = %s, want an integer of Unix seconds: %v", raw, err)
		}
		if want := time.Date(2026, 6, 21, 11, 45, 0, 0, time.UTC).Unix(); got != want {
			t.Fatalf("observed_at = %d, want %d", got, want)
		}
	})
	t.Run("absent when unknown", func(t *testing.T) {
		f.app.weather.obs = weatherObservation{Condition: "rain", FetchedAt: f.clk.Now()}
		if w, body := weatherOf(t); w["observed_at"] != nil {
			t.Fatalf("observed_at present, want the key left out: %s", body)
		}
	})
}

func TestKnobViewCarriesQuietOnlyDuringQuietHours(t *testing.T) {
	f := newViewFixture(t)
	noon := f.clk.Now()
	cases := []struct {
		name  string
		quiet QuietHoursConfig
		want  bool
	}{
		{"off", QuietHoursConfig{}, false},
		{"disabled window covering now", QuietHoursConfig{Enabled: false, Start: "11:00", End: "13:00"}, false},
		{"enabled outside the window", QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}, false},
		{"enabled inside the window", QuietHoursConfig{Enabled: true, Start: "11:00", End: "13:00"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.app.updateConfig(func(cfg *Config) { cfg.QuietHours = c.quiet })
			body, _, err := f.app.knobView(f.m.ID, noon)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(body), `"quiet":true`); got != c.want {
				t.Fatalf("quiet in view = %v, want %v: %s", got, c.want, body)
			}
			if !c.want && strings.Contains(string(body), `"quiet"`) {
				t.Fatalf("an inactive quiet must be left out: %s", body)
			}
		})
	}
}
