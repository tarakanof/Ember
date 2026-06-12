package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeetingsConfigDefaults(t *testing.T) {
	var c MeetingsConfig
	c.applyDefaults()
	if !c.Enabled {
		t.Error("Enabled: got false, want true")
	}
	if c.TileLeadMinutes != 60 {
		t.Errorf("TileLeadMinutes: got %d, want 60", c.TileLeadMinutes)
	}
	if c.PopupLeadMinutes != 2 {
		t.Errorf("PopupLeadMinutes: got %d, want 2", c.PopupLeadMinutes)
	}
	if !c.Chime {
		t.Error("Chime: got false, want true")
	}
}

func TestMeetingsConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     MeetingsConfig
		wantErr bool
	}{
		{"tile 0 → error", MeetingsConfig{TileLeadMinutes: 0, PopupLeadMinutes: 0}, true},
		{"tile 481 → error", MeetingsConfig{TileLeadMinutes: 481, PopupLeadMinutes: 0}, true},
		{"popup -1 → error", MeetingsConfig{TileLeadMinutes: 60, PopupLeadMinutes: -1}, true},
		{"popup 61 → error", MeetingsConfig{TileLeadMinutes: 60, PopupLeadMinutes: 61}, true},
		{"tile 60, popup 0 → OK", MeetingsConfig{TileLeadMinutes: 60, PopupLeadMinutes: 0}, false},
		{"tile 1, popup 1 → OK", MeetingsConfig{TileLeadMinutes: 1, PopupLeadMinutes: 1}, false},
		{"tile 480, popup 60 → OK", MeetingsConfig{TileLeadMinutes: 480, PopupLeadMinutes: 60}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMeetings(tc.cfg)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestMeetingsConfigPutPersistsAndZeroSurvives(t *testing.T) {
	const token = "test-token"
	cfg := defaultConfig()
	cfg.Auth.StatusToken = token
	app, srv := newTestServer(t, cfg)
	_ = app.ensureStore(t.TempDir() + "/m.db")

	body := `{"enabled":true,"tile_lead_minutes":60,"popup_lead_minutes":0,"chime":true}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/meetings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body = %s", resp.StatusCode, b)
	}

	// GET must return popup_lead_minutes = 0 (no re-defaulting on the runtime write path)
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/meetings/config", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp2.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if v, ok := got["popup_lead_minutes"]; !ok {
		t.Error("GET response missing popup_lead_minutes")
	} else if v != float64(0) {
		t.Errorf("popup_lead_minutes: got %v, want 0 (must not re-default)", v)
	}
}

func TestMeetingsConfigGetReportsURLCount(t *testing.T) {
	const token = "test-token"
	cfg := defaultConfig()
	cfg.Auth.StatusToken = token
	app, srv := newTestServer(t, cfg)

	// Inject two fake ICS URLs into the app (env-only path; not real credentials)
	app.meetingsURLs = []string{"http://a.example/x.ics", "http://b.example/y.ics"}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/meetings/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	body := string(rawBody)

	if !strings.Contains(body, `"ics_urls_configured":2`) {
		t.Errorf("GET body must contain ics_urls_configured:2; got: %s", body)
	}
	// The actual URL strings must never appear in the response (they're credentials)
	if strings.Contains(body, "a.example") {
		t.Errorf("GET body must not contain the ICS URL hostname 'a.example'; got: %s", body)
	}
}

func TestParseICSURLs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"two trimmed entries", " http://a/x.ics , ,https://b/y.ics ", 2},
		{"ftp dropped", "ftp://x", 0},
		{"no scheme dropped", "x.ics", 0},
		{"empty string", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseICSURLs(tc.input)
			if len(got) != tc.want {
				t.Errorf("parseICSURLs(%q) = %v (len %d), want len %d", tc.input, got, len(got), tc.want)
			}
		})
	}
	// Verify trimming for the two-entry case
	trimmed := parseICSURLs(" http://a/x.ics , ,https://b/y.ics ")
	if len(trimmed) == 2 {
		if trimmed[0] != "http://a/x.ics" {
			t.Errorf("first entry not trimmed: got %q", trimmed[0])
		}
		if trimmed[1] != "https://b/y.ics" {
			t.Errorf("second entry not trimmed: got %q", trimmed[1])
		}
	}
}

func TestMeetingsConfigRoundTrip(t *testing.T) {
	a := newTestAppWithStore(t)

	// GET returns defaults.
	gw := httptest.NewRecorder()
	a.handleMeetingsConfigGet(gw, httptest.NewRequest("GET", "/v1/meetings/config", nil))
	if gw.Code != http.StatusOK {
		t.Fatalf("GET default: code=%d body=%s", gw.Code, gw.Body)
	}

	// PUT valid config.
	pw := httptest.NewRecorder()
	a.handleMeetingsConfigPut(pw, httptest.NewRequest("PUT", "/v1/meetings/config",
		strings.NewReader(`{"enabled":true,"tile_lead_minutes":30,"popup_lead_minutes":5,"chime":false}`)))
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT: code=%d body=%s", pw.Code, pw.Body)
	}
	cfg := a.cfg.Load().Meetings
	if cfg.TileLeadMinutes != 30 || cfg.PopupLeadMinutes != 5 || cfg.Chime {
		t.Fatalf("config not applied: %+v", cfg)
	}

	// PUT invalid (tile out of range) → 400.
	bw := httptest.NewRecorder()
	a.handleMeetingsConfigPut(bw, httptest.NewRequest("PUT", "/v1/meetings/config",
		strings.NewReader(`{"tile_lead_minutes":0,"popup_lead_minutes":0}`)))
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid tile, got %d", bw.Code)
	}
}
