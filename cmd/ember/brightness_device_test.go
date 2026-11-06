package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

type luxKnob struct {
	id, tok string
}

type luxFixture struct {
	app   *App
	srv   *httptest.Server
	clk   *stepClock
	knobs []luxKnob
}

func newLuxFixture(t *testing.T, hwIDs ...string) *luxFixture {
	t.Helper()
	app, srv := newDevicesApp(t, "")
	clk := &stepClock{now: time.Date(2026, 6, 21, 23, 30, 0, 0, time.UTC)}
	app.devices.Now = clk.Now
	f := &luxFixture{app: app, srv: srv, clk: clk}
	for _, hw := range hwIDs {
		resp, b := devReq(t, srv, "POST", "/v1/devices", testToken, `{"kind":"cinder-knob","hw_id":"`+hw+`"}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("mint %s = %d: %s", hw, resp.StatusCode, b)
		}
		var m mintResp
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		f.knobs = append(f.knobs, luxKnob{id: m.ID, tok: m.Token})
	}
	return f
}

func (f *luxFixture) checkinLux(t *testing.T, k luxKnob, lux string) {
	t.Helper()
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", k.tok, `{"fw":"0.10.0","room":{"lux":`+lux+`}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin = %d: %s", resp.StatusCode, b)
	}
}

func (f *luxFixture) putConfig(t *testing.T, k luxKnob, body string) {
	t.Helper()
	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+k.id+"/config", testToken, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config %s = %d: %s", body, resp.StatusCode, b)
	}
}

func (f *luxFixture) seedClock(lux float64) {
	c := f.app.cfg.Load().Brightness.resolved()
	now := f.clk.Now()
	f.app.brightness.mu.Lock()
	f.app.brightness.advance(c, devices.BrightnessSourceClock, luxAt(lux, now), f.app.brightnessGeo(), now)
	f.app.brightness.mu.Unlock()
}

func (f *luxFixture) age(t *testing.T, d time.Duration) {
	t.Helper()
	f.clk.advance(d)
	c := f.app.cfg.Load().Brightness.resolved()
	now := f.clk.Now()
	f.app.brightness.mu.Lock()
	for key := range f.app.brightness.st {
		f.app.brightness.advance(c, key, nil, f.app.brightnessGeo(), now)
	}
	f.app.brightness.mu.Unlock()
}

func (f *luxFixture) viewLevel(t *testing.T, k luxKnob) int {
	t.Helper()
	body, _, err := f.app.knobView(k.id, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Brightness knobLight `json:"brightness"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.Brightness.Level
}

func (f *luxFixture) global() brightnessOut { return f.app.currentBrightness(f.clk.Now()) }

func TestKnobWithoutLuxGetsTheGlobalLevel(t *testing.T) {
	cases := []struct {
		name     string
		location bool
		clockLux float64
		want     int
		source   string
	}{
		{"clock", true, 1000, 255, "lux"},
		{"sun", true, -1, brightnessNightLevelDefault, "sun"},
		{"default", false, -1, 255, "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLuxFixture(t, "aabbccdd0001")
			if tc.location {
				f.app.updateConfig(func(c *Config) { c.Weather.Latitude, c.Weather.Longitude = lonLat, lonLon })
			}
			if tc.clockLux >= 0 {
				f.seedClock(tc.clockLux)
			}
			resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.knobs[0].tok, `{"fw":"0.10.0","room":{"t_c":21}}`)
			mustOK(t, "checkin", resp, b)
			g := f.global()
			if g.Source != tc.source || g.Level != tc.want {
				t.Fatalf("global = %+v, want %s %d", g, tc.source, tc.want)
			}
			if got := f.viewLevel(t, f.knobs[0]); got != tc.want {
				t.Fatalf("view level = %d, want the global %d", got, tc.want)
			}
		})
	}
}

func TestTwoKnobsWithDifferentLuxGetDifferentLevels(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001", "aabbccdd0002")
	f.seedClock(20)
	dark, bright := f.knobs[0], f.knobs[1]
	f.checkinLux(t, dark, "0.5")
	f.checkinLux(t, bright, "5000")
	if got := f.viewLevel(t, dark); got != 10 {
		t.Errorf("dark knob level = %d, want the floor 10", got)
	}
	if got := f.viewLevel(t, bright); got != 255 {
		t.Errorf("bright knob level = %d, want the ceiling 255", got)
	}
	if g := f.global(); g.Level == 10 || g.Level == 255 {
		t.Errorf("the global answer moved with a knob: %+v", g)
	}
}

func TestBrightnessSourceWinsOverTheOwnSensor(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001", "aabbccdd0002")
	f.seedClock(1)
	a, b := f.knobs[0], f.knobs[1]
	f.checkinLux(t, a, "0.5")
	f.checkinLux(t, b, "5000")

	f.putConfig(t, a, `{"brightness":{"source":"`+b.id+`"}}`)
	if got := f.viewLevel(t, a); got != 255 {
		t.Fatalf("source %s: level = %d, want b's 255", b.id, got)
	}
	f.putConfig(t, b, `{"brightness":{"source":"clock"}}`)
	if got := f.viewLevel(t, b); got != 10 {
		t.Fatalf("source clock: level = %d, want the clock's 10", got)
	}

	if resp, body := devReq(t, f.srv, "DELETE", "/v1/devices/"+b.id, testToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", resp.StatusCode, body)
	}
	if got := f.viewLevel(t, a); got != 10 {
		t.Fatalf("after deleting the source: level = %d, want its own 10", got)
	}
	f.app.brightness.mu.Lock()
	_, kept := f.app.brightness.st[b.id]
	f.app.brightness.mu.Unlock()
	if kept {
		t.Fatal("deleting a device kept its tracker")
	}
}

func TestBrightnessSourceNamesTheClockByItsRegistryID(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001")
	if _, _, err := f.app.devices.SeenClock("a1b2c3d4e5f6", &devices.ClockSeen{FW: "1.1.1"}, "d"); err != nil {
		t.Fatal(err)
	}
	clockID := ""
	for _, v := range f.app.devices.List() {
		if v.Kind == devices.KindClock {
			clockID = v.ID
		}
	}
	if clockID == "" {
		t.Fatal("no clock registered")
	}
	f.seedClock(5000)
	f.checkinLux(t, f.knobs[0], "0.5")
	f.putConfig(t, f.knobs[0], `{"brightness":{"source":"`+clockID+`"}}`)
	if got := f.viewLevel(t, f.knobs[0]); got != 255 {
		t.Fatalf("source %s: level = %d, want the clock's 255", clockID, got)
	}
}

func TestAnUnknownOrStaleSourceFallsToTheOwnSensor(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001", "aabbccdd0002")
	a, b := f.knobs[0], f.knobs[1]
	f.checkinLux(t, b, "5000")
	f.putConfig(t, a, `{"brightness":{"source":"knob-ffffff"}}`)
	f.checkinLux(t, a, "0.5")
	if got := f.viewLevel(t, a); got != 10 {
		t.Fatalf("unknown source: level = %d, want its own 10", got)
	}
	f.putConfig(t, a, `{"brightness":{"source":"`+b.id+`"}}`)
	if got := f.viewLevel(t, a); got != 255 {
		t.Fatalf("fresh source: level = %d, want b's 255", got)
	}
	f.age(t, 100*time.Second)
	f.checkinLux(t, a, "0.5")
	f.age(t, 100*time.Second)
	if got := f.viewLevel(t, a); got != 10 {
		t.Fatalf("stale source: level = %d, want its own 10", got)
	}
}

func TestAStaleKnobSampleFallsToTheClockThenTheSun(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001")
	f.app.updateConfig(func(c *Config) { c.Weather.Latitude, c.Weather.Longitude = lonLat, lonLon })
	k := f.knobs[0]
	f.seedClock(5000)
	f.checkinLux(t, k, "0.5")
	if got := f.viewLevel(t, k); got != 10 {
		t.Fatalf("fresh knob sample: level = %d, want 10", got)
	}
	f.age(t, 3*time.Minute+5*time.Second)
	f.seedClock(5000)
	if got, want := f.viewLevel(t, k), f.global().Level; got != want || want != 255 {
		t.Fatalf("stale knob sample: level = %d, want the clock's %d (255)", got, want)
	}
	f.age(t, 5*time.Minute)
	g := f.global()
	if g.Source != "sun" {
		t.Fatalf("global = %+v, want sun", g)
	}
	if got := f.viewLevel(t, k); got != g.Level {
		t.Fatalf("knob and clock stale: level = %d, want the sun's %d", got, g.Level)
	}
}

func TestAKnobLuxChangeWakesItsLongPoll(t *testing.T) {
	f := newViewFixture(t)
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	resp, body, after := waitThenAct(t, f, etag, func() {
		devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token, `{"fw":"0.10.0","room":{"lux":0.5}}`)
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"brightness":{"level":10,`) || after > 5*time.Second {
		t.Fatalf("status %d after %v: %s", resp.StatusCode, after, body)
	}
}

func TestAKnobSampleSurvivesBetweenCheckinsAtTheMinimumStaleSeconds(t *testing.T) {
	c := BrightnessConfig{StaleSeconds: minStaleSeconds}.resolved()
	geo := brightnessGeo{Lat: lonLat, Lon: lonLon, Set: true}
	t0 := time.Date(2026, 6, 21, 23, 30, 0, 0, time.UTC)
	var tr brightnessTrackers
	const knob = "knob-aabbcc"
	tr.advance(c, knob, luxAt(5000, t0), geo, t0)
	tr.advance(c, knob, nil, geo, t0.Add(30*time.Second))
	tr.advance(c, knob, nil, geo, t0.Add(65*time.Second))
	if out, ok := tr.lux(c, knob, geo, t0.Add(65*time.Second)); !ok || out.Level != 255 {
		t.Fatalf("after a tick 65 s past the sample: %+v %v, want lux 255", out, ok)
	}
	tr.advance(c, knob, luxAt(5000, t0.Add(66*time.Second)), geo, t0.Add(66*time.Second))
	if st := tr.st[knob]; !st.HasEMA || st.EMA != 5000 {
		t.Fatalf("the filter was reset between checkins: %+v", st)
	}
	tr.advance(c, devices.BrightnessSourceClock, luxAt(5000, t0), geo, t0)
	tr.advance(c, devices.BrightnessSourceClock, nil, geo, t0.Add(65*time.Second))
	if _, ok := tr.lux(c, devices.BrightnessSourceClock, geo, t0.Add(2*time.Minute+31*time.Second)); ok {
		t.Fatal("the clock's own stale limit moved with the knob's")
	}
}

func TestBrightnessSourceLimitsMoveOnlyThatSource(t *testing.T) {
	f := newLuxFixture(t, "aabbccdd0001", "aabbccdd0002")
	tuned, other := f.knobs[0], f.knobs[1]
	srv := httptest.NewServer(f.app.routes())
	defer srv.Close()
	resp, b := devReq(t, srv, "PUT", "/v1/brightness/config", testToken, `{"sources":{"`+tuned.id+`":{"lux_bright":20000}}}`)
	mustOK(t, "brightness config", resp, b)
	f.seedClock(200)
	f.checkinLux(t, tuned, "200")
	f.checkinLux(t, other, "200")
	if got := f.viewLevel(t, other); got != 255 {
		t.Errorf("untuned knob at 200 lx = %d, want 255", got)
	}
	if g := f.global(); g.Level != 255 {
		t.Errorf("clock at 200 = %+v, want 255", g)
	}
	c := f.app.cfg.Load().Brightness.resolved()
	want := luxToLevel(BrightnessConfig{Floor: c.Floor, Ceiling: c.Ceiling, LuxDark: c.LuxDark, LuxBright: 20000}, 200)
	if got := f.viewLevel(t, tuned); got != want || got >= 255 {
		t.Errorf("tuned knob at 200 lx = %d, want %d", got, want)
	}
}

func TestBrightnessSourcesValidate(t *testing.T) {
	base := BrightnessConfig{}.resolved()
	cases := []struct {
		name    string
		sources map[string]BrightnessLimits
		ok      bool
	}{
		{"clock", map[string]BrightnessLimits{"clock": {LuxDark: 2, LuxBright: 400}}, true},
		{"knob, one side", map[string]BrightnessLimits{"knob-aabbcc": {LuxBright: 2000}}, true},
		{"clock registry id", map[string]BrightnessLimits{"clock-a1b2c3": {LuxBright: 2000}}, false},
		{"bad key", map[string]BrightnessLimits{"Knob A": {LuxBright: 2000}}, false},
		{"dark over bright", map[string]BrightnessLimits{"clock": {LuxDark: 500}}, false},
		{"negative", map[string]BrightnessLimits{"clock": {LuxDark: -1}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.Sources = tc.sources
			if err := c.validate(); (err == nil) != tc.ok {
				t.Fatalf("validate = %v, want ok %v", err, tc.ok)
			}
		})
	}
	many := base
	many.Sources = map[string]BrightnessLimits{}
	for i := range brightnessSourcesMax + 1 {
		many.Sources[strings.Repeat("k", 1+i%16)+string(rune('a'+i/16))] = BrightnessLimits{}
	}
	if err := many.validate(); err == nil {
		t.Fatal("more than brightnessSourcesMax sources accepted")
	}
}

func TestKnobStatsBrightnessLevelIsTheDevicesOwn(t *testing.T) {
	f := newStatsFixture(t)
	f.setDiagnostics(t, "basic")
	c := f.app.cfg.Load().Brightness.resolved()
	f.app.brightness.mu.Lock()
	f.app.brightness.advance(c, devices.BrightnessSourceClock, luxAt(5000, f.clk.Now()), f.app.brightnessGeo(), f.clk.Now())
	f.app.brightness.mu.Unlock()
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", f.m.Token,
		`{"fw":"0.6.0","uptime_s":900,"config_version":1,"room":{"lux":0.5},"stats":`+basicStats+`}`)
	mustOK(t, "checkin", resp, b)
	if g := f.app.currentBrightness(f.clk.Now()).Level; g != 255 {
		t.Fatalf("global level = %d, want the clock's 255", g)
	}
	if got := num(t, f.stats(t, "15m").Latest, "brightness_level"); got != 10 {
		t.Fatalf("brightness_level = %v, want the knob's own 10", got)
	}
}
