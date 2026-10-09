package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var legacyConfigPaths = []string{"/v1/weather/config", "/v1/meetings/config", "/v1/usage/config", "/v1/pomodoro/config"}

var legacyConfigWrites = []struct{ path, body string }{
	{"/v1/weather/config", `{"refresh_minutes":30,"forecast_tile":false,"moon_phase":false,"popup_interval_minutes":0,"icon_ids":{"rain":"77"}}`},
	{"/v1/meetings/config", `{"chime":false,"tile_lead_minutes":15,"popup_lead_minutes":0}`},
	{"/v1/usage/config", `{"usage_threshold_pct":40,"usage_widget":false}`},
	{"/v1/pomodoro/config", `{"focus_minutes":30,"focus_color":"#112233"}`},
}

var presentationRows = []string{clockConfigKey, weatherSettingsKey, meetingsSettingsKey, usageSettingsKey, pomodoroSettingsKey}

const legacyClockRecord = `{"epoch":3,"devices":[{"id":"clock-05ffb8","kind":"awtrix-ng","hw_id":"e868e705ffb8","name":"Clock 05FFB8","token_sha256":"","config_version":777,"created_at":"2026-10-01T00:00:00Z"}]}`

func checkLegacyGETs(t *testing.T, srv *httptest.Server, stage string) {
	t.Helper()
	for _, p := range legacyConfigPaths {
		resp, b := devReq(t, srv, "GET", p, testToken, "")
		assertClockGolden(t, "legacy_"+stage+"_"+legacyGoldenName(p), mustOK(t, p, resp, b))
	}
}

func legacyGoldenName(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "/v1/"), "/config")
}

func writeLegacy(t *testing.T, srv *httptest.Server) {
	t.Helper()
	for _, w := range legacyConfigWrites {
		resp, b := devReq(t, srv, "PUT", w.path, testToken, w.body)
		mustOK(t, w.path, resp, b)
	}
}

func TestLegacyConfigGETShapeUnchanged(t *testing.T) {
	for _, migrated := range []bool{false, true} {
		t.Run(map[bool]string{false: "slices", true: "clock record"}[migrated], func(t *testing.T) {
			a, srv, _ := newClockApp(t)
			if migrated {
				registeredClock(t, a, srv)
				if a.cfg.Load().clockPresentation == nil {
					t.Fatal("clock config not migrated at record creation")
				}
			}
			checkLegacyGETs(t, srv, "default")
			writeLegacy(t, srv)
			checkLegacyGETs(t, srv, "custom")
		})
	}
}

func storedRows(t *testing.T, a *App) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, k := range presentationRows {
		v, ok, err := a.store.GetSetting(k)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			out[k] = v
		}
	}
	return out
}

func bootApp(t *testing.T, dbPath string, seed map[string]string) *App {
	t.Helper()
	t.Setenv("EMBER_CLOCK", "")
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	cfg.RateLimit.Disabled = true
	a := NewApp(cfg, &recordingPublisher{}, testLogger())
	if err := a.ensureStore(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	if len(seed) > 0 {
		if err := a.store.PutSettings(seed); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	a.reapplySettings()
	return a
}

func storedClock(t *testing.T, a *App) clockStoredConfig {
	t.Helper()
	blob, ok, err := a.store.GetSetting(clockConfigKey)
	if err != nil || !ok {
		t.Fatalf("clock_config_json missing (ok=%v err=%v)", ok, err)
	}
	var c clockStoredConfig
	if err := json.Unmarshal([]byte(blob), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func effective(t *testing.T, a *App) string {
	t.Helper()
	b, err := json.Marshal(publicConfig(*a.cfg.Load()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var mixedLegacyRows = map[string]string{
	devicesKey:          legacyClockRecord,
	weatherSettingsKey:  `{"enabled":true,"provider":"open-meteo","latitude":52.5,"longitude":13.4,"location_name":"Berlin","units":"metric","refresh_minutes":15,"forecast_tile":false,"moon_phase":false,"icon_ids":{"rain":"77"}}`,
	usageSettingsKey:    `{"usage_threshold_pct":40}`,
	pomodoroSettingsKey: `{"focus_minutes":30,"focus_color":"#112233"}`,
}

func TestClockConfigMigratesMixedLegacyRows(t *testing.T) {
	a := bootApp(t, filepath.Join(t.TempDir(), "s.db"), mixedLegacyRows)
	before := effective(t, a)
	a.versionInfo.Version = "9.9.9"
	a.migrateClockConfig()

	c := storedClock(t, a)
	if c.Schema != 1 || c.MigratedFromOverlay != "9.9.9" {
		t.Fatalf("marker = %+v", c)
	}
	if got := c.Apps.names(); !reflect.DeepEqual(got, []string{"agents", "focus", "weather"}) {
		t.Fatalf("stored apps = %v, want the apps whose slices had a stored row", got)
	}
	w := c.Apps.Weather
	if w.Forecast || w.Moon || !w.On || w.IconIDs["rain"] != "77" {
		t.Fatalf("weather app = %+v", w)
	}
	if c.Apps.Focus.FocusColor != "#112233" || c.Apps.Focus.BreakColor != "#00FF00" {
		t.Fatalf("focus app = %+v", c.Apps.Focus)
	}
	rows := storedRows(t, a)
	for key, keys := range map[string][]string{
		weatherSettingsKey:  weatherPresentationKeys,
		usageSettingsKey:    usagePresentationKeys,
		pomodoroSettingsKey: pomodoroPresentationKeys,
	} {
		if hasAnyKey(rows[key], keys) {
			t.Errorf("%s still holds presentation: %s", key, rows[key])
		}
	}
	if !strings.Contains(rows[weatherSettingsKey], `"location_name":"Berlin"`) || !strings.Contains(rows[pomodoroSettingsKey], `"focus_minutes":30`) {
		t.Fatalf("source fields lost: %v", rows)
	}
	if _, ok := rows[meetingsSettingsKey]; ok {
		t.Fatal("migration wrote a meetings row that never existed")
	}
	if got := effective(t, a); got != before {
		t.Fatalf("effective config changed by migration:\nbefore %s\nafter  %s", before, got)
	}
}

func TestClockConfigMigrationRerunIsNoOp(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	a := bootApp(t, db, mixedLegacyRows)
	a.migrateClockConfig()
	rows := storedRows(t, a)
	cfg := effective(t, a)
	a.migrateClockConfig()
	if got := storedRows(t, a); !reflect.DeepEqual(got, rows) {
		t.Fatalf("rerun rewrote rows:\n%v\n%v", rows, got)
	}
	_ = a.store.Close()

	b := bootApp(t, db, nil)
	b.migrateClockConfig()
	if got := storedRows(t, b); !reflect.DeepEqual(got, rows) {
		t.Fatalf("reboot rewrote rows:\n%v\n%v", rows, got)
	}
	if got := effective(t, b); got != cfg {
		t.Fatalf("reboot changed the effective config:\n%s\n%s", cfg, got)
	}
}

func failWrites(t *testing.T, dbPath, key string) (drop func()) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, op := range []string{"INSERT", "UPDATE"} {
		if _, err := db.Exec(`CREATE TRIGGER fail_` + op + ` BEFORE ` + op + ` ON settings WHEN NEW.key = '` + key + `' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		for _, op := range []string{"INSERT", "UPDATE"} {
			if _, err := db.Exec(`DROP TRIGGER fail_` + op); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestClockConfigMigrationFailureLeavesSlicesAndLogsOnce(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	a := bootApp(t, db, mixedLegacyRows)
	var buf bytes.Buffer
	a.logger = captureLogger(&buf)
	rows := storedRows(t, a)
	cfg := effective(t, a)
	drop := failWrites(t, db, weatherSettingsKey)
	a.migrateClockConfig()
	a.migrateClockConfig()
	if got := storedRows(t, a); !reflect.DeepEqual(got, rows) {
		t.Fatalf("a failed migration changed the store:\n%v\n%v", rows, got)
	}
	if a.cfg.Load().clockPresentation != nil || effective(t, a) != cfg {
		t.Fatal("a failed migration changed memory")
	}
	if n := strings.Count(buf.String(), "clock config not migrated"); n != 1 {
		t.Fatalf("failure logged %d times, want once:\n%s", n, buf.String())
	}
	if got := checkDevices(a); got.Status != StatusWarn || !strings.Contains(got.Detail, "clock config not migrated") {
		t.Fatalf("doctor = %+v", got)
	}
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	drop()
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"air_tile":false}`)
	mustOK(t, "weather put", resp, b)
	if blob, _, _ := a.store.GetSetting(weatherSettingsKey); !strings.Contains(blob, `"air_tile":false`) {
		t.Fatalf("façade mode no longer writes presentation to the slice: %s", blob)
	}

	a.migrateClockConfig()
	if a.cfg.Load().clockPresentation == nil || storedClock(t, a).Apps.Weather.Air {
		t.Fatal("migration did not succeed on the next attempt")
	}
	if got := checkDevices(a); !strings.Contains(got.Detail, "clock config migrated") {
		t.Fatalf("doctor = %+v", got)
	}
}

func TestClockConfigMigrationCommittedSurvivesCrashBeforeSwap(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	a := bootApp(t, db, mixedLegacyRows)
	cfg := effective(t, a)
	a.migrateClockConfig()
	_ = a.store.Close()

	b := bootApp(t, db, nil)
	if b.cfg.Load().clockPresentation == nil {
		t.Fatal("stored clock config not loaded at boot")
	}
	if got := effective(t, b); got != cfg {
		t.Fatalf("restart after migration changed the effective config:\n%s\n%s", cfg, got)
	}
}

func TestClockConfigNotMigratedOverUnreadableRow(t *testing.T) {
	seed := map[string]string{devicesKey: legacyClockRecord, clockConfigKey: `{"schema":7}`, weatherSettingsKey: `{"forecast_tile":false}`}
	a := bootApp(t, filepath.Join(t.TempDir(), "s.db"), seed)
	a.migrateClockConfig()
	if got, _, _ := a.store.GetSetting(clockConfigKey); got != `{"schema":7}` {
		t.Fatalf("an unreadable clock_config_json was overwritten: %s", got)
	}
	if a.cfg.Load().Weather.ForecastTileEnabled() {
		t.Fatal("façade mode lost the slice's presentation")
	}
}

func TestClockConfigMigratesWhenRecordCreated(t *testing.T) {
	a, srv, _ := newClockApp(t)
	a.migrateClockConfig()
	if a.cfg.Load().clockPresentation != nil {
		t.Fatal("migrated with no clock record")
	}
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"forecast_tile":false}`)
	mustOK(t, "weather put", resp, b)
	if blob, _, _ := a.store.GetSetting(weatherSettingsKey); !strings.Contains(blob, `"forecast_tile":false`) {
		t.Fatalf("before the record, presentation lives in the slice: %s", blob)
	}
	registeredClock(t, a, srv)
	if c := storedClock(t, a); c.Apps.Weather == nil || c.Apps.Weather.Forecast {
		t.Fatalf("clock config = %+v", c)
	}
	if blob, _, _ := a.store.GetSetting(weatherSettingsKey); hasAnyKey(blob, weatherPresentationKeys) {
		t.Fatalf("slice not stripped: %s", blob)
	}
}

func TestClockConfigVersionStaysMonotonicAcrossMigration(t *testing.T) {
	a := bootApp(t, filepath.Join(t.TempDir(), "s.db"), mixedLegacyRows)
	a.migrateClockConfig()
	a.syncClockConfigVersion()
	v := a.clockConfigVersion()
	if v <= 777 {
		t.Fatalf("config_version %d, want above the stored hash 777", v)
	}
	a.syncClockConfigVersion()
	if got := a.clockConfigVersion(); got != v {
		t.Fatalf("an unchanged config moved the version %d -> %d", v, got)
	}
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"air_tile":false}`)
	mustOK(t, "weather put", resp, b)
	if got := a.clockConfigVersion(); got != v+1 {
		t.Fatalf("version after an old-endpoint change = %d, want %d", got, v+1)
	}
}

func TestClockConfigVersionAdoptsMatchingLegacyHash(t *testing.T) {
	a := bootApp(t, filepath.Join(t.TempDir(), "s.db"), nil)
	cfg := a.composeClockConfig()
	hash := clockConfigHash(cfg)
	if err := a.store.PutSetting(devicesKey, strings.Replace(legacyClockRecord, `"config_version":777`, `"config_version":`+itoa(hash), 1)); err != nil {
		t.Fatal(err)
	}
	if err := a.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	epoch := a.devices.epochValue()
	a.syncClockConfigVersion()
	if got := a.clockConfigVersion(); got != hash || a.devices.epochValue() != epoch {
		t.Fatalf("version %d epoch %d, want the unchanged hash %d and no bump", got, a.devices.epochValue(), hash)
	}
	if a.clockConfigDigestStored() != clockConfigDigest(cfg) {
		t.Fatal("digest not adopted")
	}
}

func TestOldEndpointPresentationWritesGoToClockRecord(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	writeLegacy(t, srv)
	c := storedClock(t, a)
	if c.Apps.Weather == nil || c.Apps.Weather.Forecast || c.Apps.Calendar == nil || c.Apps.Calendar.TileLeadMinutes != 15 ||
		c.Apps.Agents == nil || c.Apps.Agents.UsageCards || c.Apps.Focus == nil || c.Apps.Focus.FocusColor != "#112233" {
		t.Fatalf("clock config = %+v", c.Apps)
	}
	rows := storedRows(t, a)
	for key, keys := range map[string][]string{
		weatherSettingsKey:  weatherPresentationKeys,
		meetingsSettingsKey: meetingsPresentationKeys,
		usageSettingsKey:    usagePresentationKeys,
		pomodoroSettingsKey: pomodoroPresentationKeys,
	} {
		if hasAnyKey(rows[key], keys) {
			t.Errorf("%s holds presentation: %s", key, rows[key])
		}
	}
	all := strings.Join([]string{rows[weatherSettingsKey], rows[meetingsSettingsKey], rows[usageSettingsKey], rows[pomodoroSettingsKey]}, " ")
	for _, want := range []string{`"refresh_minutes":30`, `"chime":false`, `"usage_threshold_pct":40`, `"focus_minutes":30`} {
		if !strings.Contains(all, want) {
			t.Errorf("source field %s not stored", want)
		}
	}
}

func TestPresentationOnlyWriteLeavesSourceRowAlone(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", testToken, `{"moon_phase":false}`)
	mustOK(t, "weather put", resp, b)
	if _, ok, _ := a.store.GetSetting(weatherSettingsKey); ok {
		t.Fatal("a presentation-only write stored the weather slice")
	}
}

func TestCoordinatorInputSameWithClockRecord(t *testing.T) {
	legacy, lsrv, _ := newClockApp(t)
	migrated, msrv, _ := newClockApp(t)
	registeredClock(t, migrated, msrv)
	writeLegacy(t, lsrv)
	writeLegacy(t, msrv)
	l, _ := json.Marshal(publicConfig(*legacy.coord.loadCfg()))
	m, _ := json.Marshal(publicConfig(*migrated.coord.loadCfg()))
	if string(l) != string(m) {
		t.Fatalf("coordinator input differs:\nslices %s\nrecord %s", l, m)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if !reflect.DeepEqual(legacy.coord.tileInputs(now), migrated.coord.tileInputs(now)) {
		t.Fatal("tile inputs differ")
	}
}

func TestCoordinatorIgnoresPresentationLeftInSliceRows(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	a := bootApp(t, db, mixedLegacyRows)
	a.migrateClockConfig()
	_ = a.store.Close()

	b := bootApp(t, db, map[string]string{
		weatherSettingsKey: `{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2,"location_name":"Older","units":"metric","refresh_minutes":15,"forecast_tile":true,"moon_phase":true}`,
	})
	cfg := b.coord.loadCfg()
	if cfg.Weather.ForecastTileEnabled() || cfg.Weather.MoonPhaseEnabled() {
		t.Fatal("coordinator read presentation from the weather slice instead of the clock record")
	}
	if cfg.Weather.LocationName != "Older" {
		t.Fatalf("source edit from the older server lost: %q", cfg.Weather.LocationName)
	}
	if blob, _, _ := b.store.GetSetting(weatherSettingsKey); hasAnyKey(blob, weatherPresentationKeys) {
		t.Fatalf("slice keeps presentation after boot: %s", blob)
	}
}

func TestRollbackReadsSourceOnlySlicesAndRecord(t *testing.T) {
	a := bootApp(t, filepath.Join(t.TempDir(), "s.db"), mixedLegacyRows)
	a.migrateClockConfig()
	a.syncClockConfigVersion()
	base := defaultConfig()
	base.applyDefaults()
	blob, _, _ := a.store.GetSetting(weatherSettingsKey)
	w, err := mergeSetting(base.Weather, []byte(blob))
	if err != nil {
		t.Fatalf("an older server can't read the stripped weather row: %v", err)
	}
	if w.LocationName != "Berlin" || !w.ForecastTileEnabled() {
		t.Fatalf("older server view = %+v (want source kept, presentation at the baseline)", w)
	}
	devs, _, _ := a.store.GetSetting(devicesKey)
	var older struct {
		Devices []struct {
			Kind   string          `json:"kind"`
			Config json.RawMessage `json:"config"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(devs), &older); err != nil || len(older.Devices) != 1 || older.Devices[0].Config != nil {
		t.Fatalf("devices_json gained a clock config an older server would decode as a knob's: %v %s", err, devs)
	}
}

func TestClockConfigBaselineTierAndReload(t *testing.T) {
	body := func(forecast bool) string {
		f := "false"
		if forecast {
			f = "true"
		}
		return `{"awtrix":{"http_base_url":"http://192.0.2.1"},"weather":{"forecast_tile":` + f + `,"moon_phase":true}}`
	}
	t.Setenv("EMBER_CLOCK", "off")
	a, path := newAppForReload(t, body(true))
	if err := a.ensureStore(filepath.Join(t.TempDir(), "s.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.store.Close() })
	if err := a.store.PutSetting(devicesKey, legacyClockRecord); err != nil {
		t.Fatal(err)
	}
	if err := a.loadRegistries(); err != nil {
		t.Fatal(err)
	}
	a.reapplySettings()
	a.migrateClockConfig()
	if a.cfg.Load().clockPresentation == nil || storedClock(t, a).Apps.Weather != nil {
		t.Fatalf("want a migrated clock with no weather override, got %+v", a.cfg.Load().clockPresentation)
	}
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	reload := func(forecast bool) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body(forecast)), 0o644); err != nil {
			t.Fatal(err)
		}
		resp, b := devReq(t, srv, "POST", "/admin/reload", "tok", "")
		mustOK(t, "reload", resp, b)
	}
	reload(false)
	if a.cfg.Load().Weather.ForecastTileEnabled() {
		t.Fatal("config.json baseline ignored while no store override exists")
	}
	resp, b := devReq(t, srv, "PUT", "/v1/weather/config", "tok", `{"moon_phase":false}`)
	mustOK(t, "weather put", resp, b)
	reload(true)
	cfg := a.cfg.Load()
	if cfg.Weather.ForecastTileEnabled() || cfg.Weather.MoonPhaseEnabled() {
		t.Fatalf("store override lost on reload: forecast=%v moon=%v", cfg.Weather.ForecastTileEnabled(), cfg.Weather.MoonPhaseEnabled())
	}
	if cfg.clockPresentation == nil || cfg.clockPresentation.Apps.Weather == nil {
		t.Fatal("reload dropped the clock config")
	}
}

func TestDoctorReportsClockConfigMigrated(t *testing.T) {
	a, srv, _ := newClockApp(t)
	registeredClock(t, a, srv)
	if got := checkDevices(a); !strings.Contains(got.Detail, "clock config migrated") {
		t.Fatalf("doctor devices = %+v", got)
	}
}

func (a *App) clockConfigDigestStored() string {
	a.devices.mu.Lock()
	defer a.devices.mu.Unlock()
	if d := a.devices.state.findClock(); d != nil {
		return d.ConfigDigest
	}
	return ""
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
