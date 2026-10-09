package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/meetings"
	"github.com/tarakanof/ember/internal/pomodoro"
	"github.com/tarakanof/ember/internal/render"
)

type clockCall struct {
	Op      string         `json:"op"`
	Name    string         `json:"name,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

type clockCallLog struct {
	*recordingPublisher
	logMu sync.Mutex
	calls []clockCall
}

func newClockCallLog() *clockCallLog {
	return &clockCallLog{recordingPublisher: &recordingPublisher{}}
}

func (l *clockCallLog) add(c clockCall) {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	l.calls = append(l.calls, c)
}

func (l *clockCallLog) Notify(ctx context.Context, payload map[string]any) error {
	l.add(clockCall{Op: "notify", Payload: payload})
	return l.recordingPublisher.Notify(ctx, payload)
}

func (l *clockCallLog) DismissNotifyByName(ctx context.Context, name string) error {
	l.add(clockCall{Op: "dismiss", Name: name})
	return l.recordingPublisher.DismissNotifyByName(ctx, name)
}

func (l *clockCallLog) PlayRTTTL(ctx context.Context, rtttl string) error {
	l.add(clockCall{Op: "rtttl", Name: rtttl})
	return l.recordingPublisher.PlayRTTTL(ctx, rtttl)
}

func (l *clockCallLog) snapshot() []clockCall {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	return append([]clockCall(nil), l.calls...)
}

func setQuietHours(app *App, on bool) {
	now := time.Now()
	app.updateConfig(func(c *Config) {
		c.QuietHours = QuietHoursConfig{Enabled: on,
			Start: now.Add(-time.Hour).Format("15:04"), End: now.Add(time.Hour).Format("15:04")}
	})
}

type noticeScenario struct {
	name  string
	cfg   func(*Config)
	quiet bool
	run   func(t *testing.T, app *App)
}

func noticeScenarios() []noticeScenario {
	base := []noticeScenario{
		{name: "weather_severe_rtttl", run: func(t *testing.T, app *App) {
			now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
			storm := weatherObservation{Condition: render.WeatherStorm, TempC: 18, Severe: true, FetchedAt: now}
			app.evaluateWeatherPopup(context.Background(), now, storm, render.WeatherClouds, false, time.Time{}, app.cfg.Load().Weather)
		}},
		{name: "weather_severe_melody", cfg: func(c *Config) { c.Weather.SevereSound = "chime" }, run: func(t *testing.T, app *App) {
			now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
			storm := weatherObservation{Condition: render.WeatherStorm, TempC: 18, Severe: true, FetchedAt: now}
			app.evaluateWeatherPopup(context.Background(), now, storm, render.WeatherClouds, false, time.Time{}, app.cfg.Load().Weather)
		}},
		{name: "weather_change", run: func(t *testing.T, app *App) {
			now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
			rain := weatherObservation{Condition: render.WeatherRain, TempC: 16, Overlay: render.OverlayRain, FetchedAt: now}
			app.evaluateWeatherPopup(context.Background(), now, rain, render.WeatherClouds, false, now, app.cfg.Load().Weather)
		}},
		{name: "weather_air", cfg: func(c *Config) { c.Weather.AirPopupThreshold = 80 }, run: func(t *testing.T, app *App) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"current":{"european_aqi":120,"pm2_5":40,"pm10":60},"hourly":{"time":[1700000000],"european_aqi":[120]}}`))
			}))
			t.Cleanup(srv.Close)
			app.weatherFetcher.airQualityBase = srv.URL
			app.pollAir(context.Background(), time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC), app.cfg.Load().Weather)
		}},
		{name: "weather_sun", cfg: func(c *Config) {
			c.Weather.SunPopups = boolPtr(true)
			c.Weather.Latitude, c.Weather.Longitude = 10, 10
		}, run: func(t *testing.T, app *App) {
			sunrise, _, ok := sunTimes(10, 10, time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC))
			if !ok {
				t.Fatal("no sunrise")
			}
			app.checkSunPopups(context.Background(), sunrise, app.cfg.Load().Weather)
		}},
		{name: "reminder_chime", run: func(t *testing.T, app *App) {
			fireReminder(t, app, `{"text":"Stretch","sound":true}`)
		}},
		{name: "reminder_silent_native", run: func(t *testing.T, app *App) {
			fireReminder(t, app, `{"text":"Water","sound":false,"native_icon_id":"1234","duration":12}`)
		}},
		{name: "reminder_hold_loop_window_end", run: func(t *testing.T, app *App) {
			at := time.Now()
			fireReminder(t, app, `{"text":"Walk","sound":true,"hold":true,"repeat_sound":true}`)
			app.checkReminderLoop(context.Background(), at.Add(reminderHoldWindow+time.Second))
		}},
		{name: "meeting_chime", cfg: meetingsNoticeCfg(true), run: runMeetingNotice},
		{name: "meeting_silent", cfg: meetingsNoticeCfg(false), run: runMeetingNotice},
		{name: "pomodoro_default_melody", cfg: func(c *Config) { c.Pomodoro.Sound = true }, run: func(t *testing.T, app *App) {
			app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})
		}},
		{name: "pomodoro_device_melody", cfg: func(c *Config) {
			c.Pomodoro.Sound = true
			c.Pomodoro.SoundMelody = "chime"
		}, run: func(t *testing.T, app *App) {
			app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseShort})
		}},
		{name: "status_notify", run: func(t *testing.T, app *App) {
			w := httptest.NewRecorder()
			app.handleNotify(w, httptest.NewRequest(http.MethodPost, "/v1/notify",
				strings.NewReader(`{"text":"PING","color":"#FF00AA","duration":7,"hold":true,"text_case":"asTyped"}`)))
			if w.Code != http.StatusOK {
				t.Fatalf("notify status = %d", w.Code)
			}
		}},
		{name: "usage_alarm", run: func(t *testing.T, app *App) {
			c := app.coord
			t0 := time.Unix(1_700_000_000, 0)
			app.usage.Put("claude", ToolUsage{FiveHour: &UsageWindow{UsedPercent: 100, ResetsAt: t0.Unix() + 90}, UpdatedAt: t0})
			c.checkLimitAlarms(t0, Snapshot{})
			c.checkLimitAlarms(t0.Add(160*time.Second), Snapshot{})
		}},
		{name: "attention_chime", cfg: func(c *Config) { c.Display.AttentionChime = true }, run: func(t *testing.T, app *App) {
			c := app.coord
			c.ctx = context.Background()
			c.snapshot = func() Snapshot { return Snapshot{} }
			c.onUpsert("mbp/claude/a", "running", "waiting")
		}},
	}
	out := make([]noticeScenario, 0, 2*len(base)+1)
	for _, s := range base {
		loud, quiet := s, s
		loud.name += "/loud"
		quiet.name += "/quiet"
		quiet.quiet = true
		out = append(out, loud, quiet)
	}
	return append(out, noticeScenario{name: "reminder_hold_loop_enters_quiet", run: func(t *testing.T, app *App) {
		fireReminder(t, app, `{"text":"Walk","sound":true,"hold":true,"repeat_sound":true}`)
		setQuietHours(app, true)
		app.checkReminderLoop(context.Background(), time.Now().Add(time.Minute))
	}})
}

func fireReminder(t *testing.T, app *App, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	app.handleReminderFire(w, httptest.NewRequest(http.MethodPost, "/v1/reminders/fire", strings.NewReader(body)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("reminder status = %d", w.Code)
	}
}

func meetingsNoticeCfg(chime bool) func(*Config) {
	return func(c *Config) {
		c.Meetings.Enabled = boolPtr(true)
		c.Meetings.PopupLeadMinutes = intPtr(2)
		c.Meetings.Chime = boolPtr(chime)
	}
}

func runMeetingNotice(t *testing.T, app *App) {
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	start := now.Add(2 * time.Minute)
	app.meetings.mu.Lock()
	app.meetings.upcoming = []meetings.Occurrence{{UID: "standup@test", Title: "Standup", Start: start, End: start.Add(15 * time.Minute)}}
	app.meetings.lastFetchOK = now
	app.meetings.mu.Unlock()
	app.checkMeetingPopup(context.Background(), now, app.cfg.Load().Meetings)
}

func TestNoticesReachTheClockUnchanged(t *testing.T) {
	for _, s := range noticeScenarios() {
		t.Run(s.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.applyDefaults()
			cfg.Weather.Enabled = true
			if s.cfg != nil {
				s.cfg(&cfg)
			}
			log := newClockCallLog()
			app := NewApp(cfg, log, testLogger())
			setQuietHours(app, s.quiet)
			s.run(t, app)
			var got []byte
			for _, c := range log.snapshot() {
				line, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				got = append(append(got, line...), '\n')
			}
			compareGolden(t, filepath.Join("testdata", "notices", strings.ReplaceAll(s.name, "/", "_")+".jsonl"), got)
		})
	}
}

func TestShowNoticeLeavesCallerPayloadUntouched(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	payload := map[string]any{"text": "hi"}
	n := notice{app: "test", kind: noticeMessage, sound: noticeSound{rtttl: "x:d=4:c"}, payload: payload}
	if err := app.coord.showNotice(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 {
		t.Errorf("caller payload mutated: %v", payload)
	}
}
