package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func getReminders(t *testing.T, do func(method, body string) (int, string)) RemindersConfig {
	t.Helper()
	code, b := do("GET", "")
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, b)
	}
	var c RemindersConfig
	if err := json.Unmarshal([]byte(b), &c); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return c
}

func remindersClient(t *testing.T, dbPath string) (*App, func(method, body string) (int, string), func()) {
	t.Helper()
	app, srv := newDevicesApp(t, dbPath)
	do := func(method, body string) (int, string) {
		resp, b := devReq(t, srv, method, "/v1/reminders/config", testToken, body)
		return resp.StatusCode, string(b)
	}
	return app, do, srv.Close
}

func TestRemindersConfigDefaultsMatchTheApp(t *testing.T) {
	_, do, closeSrv := remindersClient(t, "")
	defer closeSrv()
	code, b := do("GET", "")
	want := `{"enabled":false,"sound":true,"lead_minutes":0,"popup_duration":8,"use_native_icon":false,"native_icon_id":"","hold":true,"repeat_sound":false}`
	if code != http.StatusOK || strings.TrimSpace(b) != want {
		t.Fatalf("get: %d %s\nwant %s", code, b, want)
	}
}

func TestRemindersConfigPutMergesAndRoundTrips(t *testing.T) {
	_, do, closeSrv := remindersClient(t, "")
	defer closeSrv()
	code, b := do("PUT", `{"enabled":true,"lead_minutes":5,"native_icon_id":" 1234 "}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	got := getReminders(t, do)
	want := defaultRemindersConfig()
	want.Enabled, want.LeadMinutes, want.NativeIconID = true, 5, "1234"
	if got != want {
		t.Fatalf("after put: %+v, want %+v", got, want)
	}
	if code, b := do("PUT", `{"hold":false}`); code != http.StatusOK {
		t.Fatalf("second put: %d %s", code, b)
	}
	want.Hold = false
	if got := getReminders(t, do); got != want {
		t.Fatalf("merge lost keys: %+v, want %+v", got, want)
	}
}

func TestRemindersConfigRejectsOutOfRangeValues(t *testing.T) {
	_, do, closeSrv := remindersClient(t, "")
	defer closeSrv()
	for _, body := range []string{
		`{"lead_minutes":-1}`,
		`{"lead_minutes":31}`,
		`{"popup_duration":0}`,
		`{"popup_duration":301}`,
		`{"native_icon_id":"` + strings.Repeat("9", maxReminderIconID+1) + `"}`,
		`{"enabled":"yes"}`,
		`[]`,
	} {
		if code, b := do("PUT", body); code != http.StatusBadRequest {
			t.Errorf("put %s: %d %s, want 400", body, code, b)
		}
	}
	if got := getReminders(t, do); got != defaultRemindersConfig() {
		t.Fatalf("rejected puts changed the config: %+v", got)
	}
	for _, body := range []string{`{"lead_minutes":30,"popup_duration":1}`, `{"popup_duration":300}`} {
		if code, b := do("PUT", body); code != http.StatusOK {
			t.Errorf("put %s: %d %s, want 200", body, code, b)
		}
	}
}

func TestRemindersConfigSurvivesARestart(t *testing.T) {
	db := t.TempDir() + "/s.db"
	app, do, closeSrv := remindersClient(t, db)
	if code, b := do("PUT", `{"enabled":true,"sound":false,"lead_minutes":10,"repeat_sound":true}`); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, b)
	}
	closeSrv()
	_ = app.store.Close()
	app2, _ := newDevicesApp(t, db)
	app2.settings.reapply()
	want := defaultRemindersConfig()
	want.Enabled, want.Sound, want.LeadMinutes, want.RepeatSound = true, false, 10, true
	if got := app2.settings.reminders.get(); got != want {
		t.Fatalf("after restart: %+v, want %+v", got, want)
	}
}

func TestRemindersConfigFileIsValidated(t *testing.T) {
	cfg := defaultConfig()
	cfg.Reminders.PopupDuration = 0
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "reminders.popup_duration") {
		t.Fatalf("err = %v, want a reminders.popup_duration error", err)
	}
}

func TestRemindersConfigScopes(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	read := mintClient(t, srv, "read", scopeRead)
	ingest := mintClient(t, srv, "ingest", scopeIngest)
	admin := mintClient(t, srv, "admin", scopeAdmin)
	cases := []struct {
		name, method, token, body string
		want                      int
	}{
		{"read gets", "GET", read.Token, "", http.StatusOK},
		{"ingest can't get", "GET", ingest.Token, "", http.StatusForbidden},
		{"read can't put", "PUT", read.Token, `{"enabled":true}`, http.StatusForbidden},
		{"admin puts", "PUT", admin.Token, `{"enabled":true}`, http.StatusOK},
		{"no token", "GET", "", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, b := devReq(t, srv, c.method, "/v1/reminders/config", c.token, c.body)
			if resp.StatusCode != c.want {
				t.Fatalf("%s: %d %s, want %d", c.name, resp.StatusCode, b, c.want)
			}
		})
	}
}

func TestReminderFiredByTwoMacsRingsOnce(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	pub := app.publisher.(*recordingPublisher)
	m4 := mintClient(t, srv, "m4", scopeIngest)
	m5 := mintClient(t, srv, "m5", scopeIngest)
	fire := func(token string) int {
		t.Helper()
		req, err := http.NewRequest("POST", srv.URL+"/v1/reminders/fire", strings.NewReader(`{"text":"Walk","duration":8}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "x-ical-uid|1700000000")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := fire(m4.Token); code != http.StatusNoContent {
		t.Fatalf("m4 fire: %d, want 204", code)
	}
	if code := fire(m5.Token); code != http.StatusOK {
		t.Fatalf("m5 fire: %d, want 200 (duplicate)", code)
	}
	if n := len(pub.NotifySnapshot()); n != 1 {
		t.Fatalf("popups = %d, want 1", n)
	}
}

func TestRemindersConfigFileKeepsDefaultsForAbsentKeys(t *testing.T) {
	path := t.TempDir() + "/ember.json"
	if err := os.WriteFile(path, []byte(`{"reminders":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	want := defaultRemindersConfig()
	want.Enabled = true
	if cfg.Reminders != want {
		t.Fatalf("reminders = %+v, want %+v", cfg.Reminders, want)
	}
}
