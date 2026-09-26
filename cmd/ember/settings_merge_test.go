package main

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// Every settings PUT merges (#144): a body naming one field leaves the others
// at their current value. Weather and meetings used to decode into a zero
// config, so a partial body reset omitted fields to zero or their defaults.

func TestWeatherConfigPartialPutKeepsOmittedFields(t *testing.T) {
	a := newTestAppWithStore(t)
	full := httptest.NewRecorder()
	a.handleWeatherConfigPut(full, httptest.NewRequest("PUT", "/v1/weather/config", strings.NewReader(
		`{"enabled":true,"provider":"met-no","units":"imperial","latitude":52,"longitude":4,`+
			`"refresh_minutes":15,"popup_duration_seconds":20,"sun_popups":false,"icon_ids":{"rain":"9"}}`)))
	if full.Code != 200 {
		t.Fatalf("full PUT = %d body=%s", full.Code, full.Body)
	}

	pw := httptest.NewRecorder()
	a.handleWeatherConfigPut(pw, httptest.NewRequest("PUT", "/v1/weather/config",
		strings.NewReader(`{"refresh_minutes":30}`)))
	if pw.Code != 200 {
		t.Fatalf("partial PUT = %d body=%s", pw.Code, pw.Body)
	}
	w := a.cfg.Load().Weather
	if w.RefreshMinutes != 30 || w.Provider != "met-no" || w.Units != "imperial" ||
		w.Latitude != 52 || w.SunPopupsEnabled() || w.IconIDs["rain"] != "9" {
		t.Fatalf("partial PUT must only change refresh_minutes: %+v", w)
	}
	if v, _, _ := a.store.GetSetting(weatherSettingsKey); !strings.Contains(v, `"provider":"met-no"`) ||
		!strings.Contains(v, `"refresh_minutes":30`) {
		t.Fatalf("persisted blob not the merged config: %s", v)
	}
}

func TestMeetingsConfigPartialPutKeepsOmittedFields(t *testing.T) {
	a := newTestAppWithStore(t)
	full := httptest.NewRecorder()
	a.handleMeetingsConfigPut(full, httptest.NewRequest("PUT", "/v1/meetings/config", strings.NewReader(
		`{"enabled":true,"tile_lead_minutes":90,"popup_lead_minutes":0,"chime":false}`)))
	if full.Code != 200 {
		t.Fatalf("full PUT = %d body=%s", full.Code, full.Body)
	}

	pw := httptest.NewRecorder()
	a.handleMeetingsConfigPut(pw, httptest.NewRequest("PUT", "/v1/meetings/config",
		strings.NewReader(`{"enabled":false}`)))
	if pw.Code != 200 {
		t.Fatalf("partial PUT = %d body=%s", pw.Code, pw.Body)
	}
	m := a.cfg.Load().Meetings
	if m.IsEnabled() || m.TileLeadMinutes != 90 || m.PopupLeadMins() != 0 || m.ChimeEnabled() {
		t.Fatalf("partial PUT must only change enabled: %+v", m)
	}
	if !strings.Contains(pw.Body.String(), `"ics_urls_configured":0`) {
		t.Fatalf("PUT response lost ics_urls_configured: %s", pw.Body)
	}
}

func TestSettingsPutRejectsNonObjectBody(t *testing.T) {
	a := newTestAppWithStore(t)
	for _, h := range []struct {
		name string
		put  func(*httptest.ResponseRecorder, string)
	}{
		{"display", func(w *httptest.ResponseRecorder, b string) {
			a.handleDisplayConfigPut(w, httptest.NewRequest("PUT", "/", strings.NewReader(b)))
		}},
		{"quiet", func(w *httptest.ResponseRecorder, b string) {
			a.handleQuietConfigPut(w, httptest.NewRequest("PUT", "/", strings.NewReader(b)))
		}},
		{"usage", func(w *httptest.ResponseRecorder, b string) {
			a.handleUsageConfigPut(w, httptest.NewRequest("PUT", "/", strings.NewReader(b)))
		}},
		{"weather", func(w *httptest.ResponseRecorder, b string) {
			a.handleWeatherConfigPut(w, httptest.NewRequest("PUT", "/", strings.NewReader(b)))
		}},
		{"meetings", func(w *httptest.ResponseRecorder, b string) {
			a.handleMeetingsConfigPut(w, httptest.NewRequest("PUT", "/", strings.NewReader(b)))
		}},
	} {
		for _, body := range []string{`null`, `[]`, `"x"`} {
			w := httptest.NewRecorder()
			h.put(w, body)
			if w.Code != 400 {
				t.Errorf("%s PUT %s = %d, want 400", h.name, body, w.Code)
			}
		}
	}
}

// A value of the wrong type is an undecodable body: 400 plus the same
// "request rejected" log line as any other body decodeOrReject refuses.
func TestSettingsPutTypeErrorLogsRejection(t *testing.T) {
	var buf bytes.Buffer
	a := NewApp(defaultConfig(), &recordingPublisher{}, slog.New(slog.NewTextHandler(&buf, nil)))
	w := httptest.NewRecorder()
	a.handleDisplayConfigPut(w, httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"attention_chime":"yes"}`)))
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400", w.Code)
	}
	if !strings.Contains(buf.String(), "request rejected") || !strings.Contains(buf.String(), "reason=parse") {
		t.Fatalf("missing request-rejected log line: %s", buf.String())
	}
	// A validation failure is not a parse error: no rejection line.
	buf.Reset()
	a.handleDisplayConfigPut(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v1/display/config",
		strings.NewReader(`{"attention_hold_seconds":1}`)))
	if strings.Contains(buf.String(), "request rejected") {
		t.Fatalf("validation error logged as a rejected body: %s", buf.String())
	}
}

// Disabling Pomodoro through the settings PUT stops a running timer; a PUT
// that leaves it enabled (enabled omitted) does not.
func TestPomodoroSettingsDisableStopsRunningTimer(t *testing.T) {
	a := newPomodoroApp(t)
	running := func() bool { return a.engine.Status(time.Now()).Phase != pomodoro.PhaseIdle }
	a.engine.Start(pomodoro.PhaseFocus)
	if !running() {
		t.Fatal("setup: timer not running")
	}

	if _, err := a.settings.pomodoro.put([]byte(`{"focus_minutes":30}`)); err != nil {
		t.Fatal(err)
	}
	if !running() {
		t.Fatal("a PUT that keeps pomodoro enabled stopped the timer")
	}

	if _, err := a.settings.pomodoro.put([]byte(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if running() {
		t.Fatal("disabling pomodoro left the timer running")
	}
}
