package main

import (
	"context"
	"encoding/json"
	"go/constant"
	"go/types"
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
		golden := filepath.Join("testdata", "notices", strings.ReplaceAll(s.name, "/", "_")+".jsonl")
		t.Run(s.name, func(t *testing.T) {
			compareGolden(t, golden, runNoticeScenario(t, s, func(app *App) { setQuietHours(app, s.quiet) }))
		})
		if s.quiet {
			continue
		}
		t.Run(s.name+"_outside_quiet_window", func(t *testing.T) {
			compareGolden(t, golden, runNoticeScenario(t, s, setQuietHoursElsewhere))
		})
	}
}

func setQuietHoursElsewhere(app *App) {
	now := time.Now()
	app.updateConfig(func(c *Config) {
		c.QuietHours = QuietHoursConfig{Enabled: true,
			Start: now.Add(2 * time.Hour).Format("15:04"), End: now.Add(3 * time.Hour).Format("15:04")}
	})
}

func runNoticeScenario(t *testing.T, s noticeScenario, quiet func(*App)) []byte {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Weather.Enabled = true
	if s.cfg != nil {
		s.cfg(&cfg)
	}
	log := newClockCallLog()
	app := NewApp(cfg, log, testLogger())
	quiet(app)
	s.run(t, app)
	var got []byte
	for _, c := range log.snapshot() {
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		got = append(append(got, line...), '\n')
	}
	return got
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

func nightClockApp(t *testing.T, pub Publisher, at time.Time) (*App, *fakeClock) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Display.AttentionChime = true
	cfg.Pomodoro.Sound = true
	cfg.QuietHours = QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}
	app := NewApp(cfg, pub, testLogger())
	clk := &fakeClock{now: at}
	app.coord.clk = clk
	app.coord.ctx = context.Background()
	app.coord.snapshot = func() Snapshot { return Snapshot{} }
	return app, clk
}

func TestQuietHoursEndResumesNoticeSoundAndChime(t *testing.T) {
	log := newClockCallLog()
	app, clk := nightClockApp(t, log, time.Date(2026, 1, 1, 7, 59, 0, 0, time.Local))
	ring := func() {
		app.pomoPhaseEndAlert(&pomodoro.PhaseResult{Phase: pomodoro.PhaseFocus})
		app.coord.onUpsert("mbp/claude/a", "running", "waiting")
		app.coord.onUpsert("mbp/claude/a", "waiting", "running")
	}
	ring()
	clk.Advance(time.Minute)
	ring()
	var notes []map[string]any
	var chimes int
	for _, c := range log.snapshot() {
		switch c.Op {
		case "notify":
			notes = append(notes, c.Payload)
		case "rtttl":
			chimes++
		}
	}
	if len(notes) != 2 {
		t.Fatalf("notifications = %d, want 2", len(notes))
	}
	if _, has := notes[0]["soundRtttl"]; has {
		t.Errorf("07:59 inside 22:00–08:00 must be silent: %v", notes[0])
	}
	if notes[1]["soundRtttl"] != defaultPomoMelody {
		t.Errorf("08:00 is past the window, sound must resume: %v", notes[1])
	}
	if chimes != 1 {
		t.Errorf("attention chimes = %d, want 1 (only after the window ends)", chimes)
	}
}

func TestReminderLoopArmsOnTheCoordinatorClock(t *testing.T) {
	pub := &recordingPublisher{}
	app, _ := nightClockApp(t, pub, time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local))
	fireReminder(t, app, `{"text":"Walk","sound":true,"hold":true,"repeat_sound":true}`)
	if _, has := pub.NotifySnapshot()[0]["soundRtttl"]; has {
		t.Errorf("alarm pushed with sound at 03:00 inside quiet hours: %v", pub.NotifySnapshot()[0])
	}
	if _, n, _ := app.reminderLoop.current(); n != nil {
		t.Error("loop armed for an alarm pushed silent; quiet hours must read one clock")
	}
}

func TestDismissNoticeIgnoresQuietHours(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	setQuietHours(app, true)
	if err := app.coord.dismissNotice(context.Background(), noticeReminder); err != nil {
		t.Fatal(err)
	}
	if got := pub.DismissedNamesSnapshot(); len(got) != 1 || got[0] != notifyNameReminder {
		t.Errorf("dismissed = %v, want [%s] even during quiet hours", got, notifyNameReminder)
	}
}

func TestEveryNoticeKindHasAUniqueName(t *testing.T) {
	_, _, info := typeCheckPackage(t)
	seen := map[string]string{}
	kinds := 0
	for _, id := range info.pkg.Scope().Names() {
		c, ok := info.pkg.Scope().Lookup(id).(*types.Const)
		if !ok || types.TypeString(c.Type(), nil) != info.pkg.Path()+".noticeKind" {
			continue
		}
		kinds++
		v, _ := constant.Int64Val(c.Val())
		name := noticeNames[noticeKind(v)]
		if name == "" {
			t.Errorf("%s has no name in noticeNames", id)
			continue
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("%s and %s share the name %q", prev, id, name)
		}
		seen[name] = id
	}
	if kinds == 0 {
		t.Fatal("found no noticeKind constants")
	}
}
