package main

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/render"
)

// These tests pin the menu previews to what the clock is actually sent: for
// the same config, observation and instant, a preview frame must equal the
// bitmap of the payload the coordinator pushes. Deliberate exceptions, which
// the canvas cannot show: the NG overlay (animated by the firmware on top of
// the finished page) and the native gallery icon at cols 0-7 (the preview
// draws the condition sprite there). Both live only in the payload.

// pushedTiles runs one tile reconcile at now and returns the payload pushed to
// each device app.
func pushedTiles(t *testing.T, app *App, pub *recordingPublisher, now time.Time) map[string]map[string]any {
	t.Helper()
	app.coord.reconcileTiles(now)
	out := map[string]map[string]any{}
	names, apps := pub.CustomNamesSnapshot(), pub.CustomAppsSnapshot()
	for i, n := range names {
		out[n] = apps[i]
	}
	return out
}

// payloadBitmap returns the first bitmap draw op of a pushed payload as
// "#rrggbb" strings, with its x origin and width.
func payloadBitmap(t *testing.T, p map[string]any) (x, w int, px []string) {
	t.Helper()
	draw, _ := p["draw"].([]any)
	for _, op := range draw {
		o, ok := op.([]any)
		if !ok || len(o) != 6 || o[0] != "bitmap" {
			continue
		}
		data := o[5].([]int)
		px = make([]string, len(data))
		for i, v := range data {
			px[i] = fmt.Sprintf("#%06x", v)
		}
		return o[1].(int), o[3].(int), px
	}
	t.Fatalf("payload has no bitmap op: %v", p)
	return 0, 0, nil
}

// assertFrameMatchesPayload compares preview pixels against the payload's
// bitmap over the columns the bitmap covers.
func assertFrameMatchesPayload(t *testing.T, card string, preview []string, payload map[string]any) {
	t.Helper()
	x0, w, px := payloadBitmap(t, payload)
	if len(preview) != 256 {
		t.Fatalf("%s: preview has %d pixels, want 256", card, len(preview))
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < w; x++ {
			if got, want := preview[y*32+x0+x], px[y*w+x]; got != want {
				t.Fatalf("%s: pixel (%d,%d) preview %s, device %s", card, x0+x, y, got, want)
			}
		}
	}
}

func previewCard(t *testing.T, p render.Preview, card string) []string {
	t.Helper()
	for _, f := range p.Frames {
		if f.Card == card {
			return f.Pixels
		}
	}
	t.Fatalf("preview has no %q frame: %+v", card, p.Frames)
	return nil
}

func weatherParityApp(t *testing.T, mut func(*WeatherConfig), obs weatherObservation) (*App, *recordingPublisher) {
	t.Helper()
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	mut(&cfg.Weather)
	pub := &recordingPublisher{}
	app := NewApp(cfg, pub, testLogger())
	app.weather.mu.Lock()
	app.weather.obs = obs
	app.weather.have = true
	app.weather.air = airObservation{AQI: 57, HourlyAQI: []float64{57, 61, 70, 30}, FetchedAt: obs.FetchedAt}
	app.weather.haveAir = true
	app.weather.mu.Unlock()
	return app, pub
}

// weatherQuery is the query the menu sends for a stored config.
func weatherQuery(c WeatherConfig) url.Values {
	return url.Values{
		"rotate_in_apps": {strconv.FormatBool(c.RotateInAppsEnabled())},
		"forecast_tile":  {strconv.FormatBool(c.ForecastTileEnabled())},
		"air_tile":       {strconv.FormatBool(c.AirTileEnabled())},
		"forecast_hours": {strconv.Itoa(c.ForecastHours)},
		"units":          {c.Units},
	}
}

func arc(n int) []float64 {
	h := make([]float64, n)
	for i := range h {
		h[i] = 10 + 12*math.Sin(float64(i)/float64(n)*math.Pi)
	}
	return h
}

func TestWeatherPreviewMatchesDevicePayload(t *testing.T) {
	// 23:00 UTC in mid-January: night in London.
	night := time.Date(2026, 1, 15, 23, 0, 0, 0, time.UTC)
	if !isNight(51.5, -0.1, night) {
		t.Fatal("fixture: expected night in London")
	}
	noon := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		now    time.Time
		cond   string
		mut    func(*WeatherConfig)
		native bool
	}{
		{"drawn day", noon, render.WeatherClouds, func(c *WeatherConfig) {}, false},
		{"imperial", noon, render.WeatherRain, func(c *WeatherConfig) { c.Units = "imperial" }, false},
		{"moon on a clear night", night, render.WeatherClear, func(c *WeatherConfig) {
			c.Latitude, c.Longitude = 51.5, -0.1
		}, false},
		{"moon off on a clear night", night, render.WeatherClear, func(c *WeatherConfig) {
			c.Latitude, c.Longitude = 51.5, -0.1
			c.MoonPhase = boolPtr(false)
		}, false},
		{"native icon", noon, render.WeatherSnow, func(c *WeatherConfig) { c.TileNativeIcons = true }, true},
		// forecast_hours as the PUT path can store it (it does not clamp).
		{"forecast hours 3", noon, render.WeatherClouds, func(c *WeatherConfig) { c.ForecastHours = 3 }, false},
		{"forecast hours 0", noon, render.WeatherClouds, func(c *WeatherConfig) { c.ForecastHours = 0 }, false},
		{"forecast hours 30", noon, render.WeatherClouds, func(c *WeatherConfig) { c.ForecastHours = 30 }, false},
		{"forecast hours 12", noon, render.WeatherClouds, func(c *WeatherConfig) { c.ForecastHours = 12 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := weatherObservation{Condition: tc.cond, TempC: 7, Hourly: arc(24),
				Overlay: render.OverlayRain, FetchedAt: tc.now}
			app, pub := weatherParityApp(t, tc.mut, obs)
			pushed := pushedTiles(t, app, pub, tc.now)
			preview := app.weatherPreview(weatherQuery(app.cfg.Load().Weather), tc.now)

			for app, card := range map[string]string{"ember-weather": "weather", "ember-forecast": "forecast", "ember-air": "air"} {
				p, ok := pushed[app]
				if !ok {
					t.Fatalf("%s not pushed (pushed: %v)", app, pushed)
				}
				assertFrameMatchesPayload(t, card, previewCard(t, preview, card), p)
			}
			if tc.native {
				if pushed["ember-weather"]["icon"] == nil {
					t.Error("native mode: device payload must carry the gallery icon")
				}
			}
			// The overlay reaches the device only; the preview can't animate it.
			if got := pushed["ember-weather"]["overlay"]; got != render.OverlayRain {
				t.Errorf("device overlay = %v, want %q", got, render.OverlayRain)
			}
		})
	}
}

func TestMeetingsPreviewMatchesDevicePayload(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	cfg := defaultConfig()
	cfg.Meetings.applyDefaults()
	cfg.Meetings.Enabled = boolPtr(true)
	pub := &recordingPublisher{}
	app := NewApp(cfg, pub, testLogger())
	app.meetings.mu.Lock()
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()
	seedMeeting(app.meetings, meetings.Occurrence{UID: "u", Title: "Design review", Start: now.Add(17*time.Minute + 20*time.Second)})

	payload := pushedTiles(t, app, pub, now)["ember-meet"]
	if payload == nil {
		t.Fatal("ember-meet not pushed")
	}
	text, _ := payload["text"].(string)
	mins, title, ok := strings.Cut(text, "M ")
	if !ok {
		t.Fatalf("payload text %q not <N>M <TITLE>", text)
	}
	n, err := strconv.Atoi(mins)
	if err != nil {
		t.Fatal(err)
	}
	// The device renders the text natively (textCase upper); the preview draws
	// the same text in the 3×5 font.
	want := render.MeetingTileFrame(title, n)
	got := previewCard(t, app.meetingsPreview(now), "meeting")
	if !slicesEqualStr(got, render.HexPixels(&want)) {
		t.Errorf("meeting preview differs from the pushed tile text %q", text)
	}
}
