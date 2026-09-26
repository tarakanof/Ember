package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/render"
)

// TestServerFinishedPayloadsPassNGSchema checks the payloads as they reach the
// publisher, after the server has added its own keys (name, sound, soundLoop,
// overlay), against NG 1.1.2's schema: the render builders are checked on
// their own in internal/render, but a key added here would 422 just the same.
func TestServerFinishedPayloadsPassNGSchema(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Pomodoro.Sound = true
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	app := NewApp(cfg, pub, testLogger())
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
	obs := weatherObservation{Condition: render.WeatherStorm, TempC: 18, Severe: true,
		Overlay: render.OverlayThunder, FetchedAt: time.Now()}
	app.sendWeatherPopup(ctx, obs, cfg.Weather, 30, defaultWeatherSevereSound)
	app.sendWeatherPopup(ctx, obs, cfg.Weather, 30, "storm")
	app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})

	notes := pub.NotifySnapshot()
	if len(notes) != 9 {
		t.Fatalf("notifications = %d, want 9", len(notes))
	}
	for i, p := range notes {
		for _, e := range render.CheckNGPayload(p, true) {
			t.Errorf("notification %d (%v): %s", i, p["text"], e)
		}
	}

	// The conditions tile with its overlay, as the coordinator pushes it.
	app.weather.mu.Lock()
	app.weather.obs, app.weather.have = obs, true
	app.weather.mu.Unlock()
	app.coord.reconcileWeatherApp(time.Now())
	pub.mu.Lock()
	apps := append([]map[string]any(nil), pub.customApps...)
	pub.mu.Unlock()
	if len(apps) == 0 {
		t.Fatal("no weather tile pushed")
	}
	for _, p := range apps {
		for _, e := range render.CheckNGPayload(p, false) {
			t.Errorf("pushed app: %s", e)
		}
	}
}
