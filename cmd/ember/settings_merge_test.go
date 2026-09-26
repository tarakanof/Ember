package main

import (
	"net/http/httptest"
	"strings"
	"testing"
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
