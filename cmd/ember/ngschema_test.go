package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/weather"
)

func TestServerFinishedPayloadsPassNGSchema(t *testing.T) {
	checkServerFinishedPayloads(t, nil, render.CheckNGPayload)
}

func TestServerFinishedPayloadsPassNG12Schema(t *testing.T) {
	checkServerFinishedPayloads(t, &awtrix.Capabilities{Audio: ng12Audio(true)}, render.CheckNG12Payload)
}

func checkServerFinishedPayloads(t *testing.T, caps *awtrix.Capabilities, check func(map[string]any, bool) []string) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Pomodoro.Sound = true
	cfg.Weather.ApplyDefaults()
	cfg.Weather.Enabled = true
	app := NewApp(cfg, pub, testLogger())
	storeCaps(app, caps)
	ctx := context.Background()

	for _, body := range []string{
		`{"text":"Walk","sound":true,"duration":8}`,
		`{"text":"Walk","sound":true,"hold":true}`,
		`{"text":"Walk","sound":true,"hold":true,"repeat_sound":true}`,
		`{"text":"Walk","native_icon_id":"1234"}`,
	} {
		app.handleReminderFire(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
			"/v1/reminders/fire", strings.NewReader(body)))
	}
	for _, body := range []string{
		`{"text":"PING"}`,
		`{"text":"PING","color":"#FF00AA","duration":7,"hold":true}`,
	} {
		app.handleNotify(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
			"/v1/notify", strings.NewReader(body)))
	}
	obs := weather.Observation{Condition: render.WeatherStorm, TempC: 18, Severe: true,
		Overlay: render.OverlayThunder, FetchedAt: time.Now()}
	app.sendWeatherPopup(ctx, obs, cfg.Weather, 30, defaultWeatherSevereSound)
	app.sendWeatherPopup(ctx, obs, cfg.Weather, 30, "storm")
	app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})
	for _, melody := range []string{"bell", "pomo:d=4,o=5,b=125:8g6,8c7"} {
		app.updateConfig(func(c *Config) { c.Pomodoro.SoundMelody = melody })
		app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})
	}

	notes := pub.NotifySnapshot()
	if len(notes) != 11 {
		t.Fatalf("notifications = %d, want 11", len(notes))
	}
	for i, p := range notes {
		for _, e := range check(p, true) {
			t.Errorf("notification %d (%v): %s", i, p["text"], e)
		}
	}

	app.weather.mu.Lock()
	app.weather.obs, app.weather.have = obs, true
	app.weather.source = app.cfg.Load().Weather.Source()
	app.weather.mu.Unlock()
	app.coord.reconcileTiles(time.Now())
	pub.mu.Lock()
	apps := append([]map[string]any(nil), pub.customApps...)
	pub.mu.Unlock()
	if len(apps) == 0 {
		t.Fatal("no weather tile pushed")
	}
	for _, p := range apps {
		for _, e := range check(p, false) {
			t.Errorf("pushed app: %s", e)
		}
	}
}
