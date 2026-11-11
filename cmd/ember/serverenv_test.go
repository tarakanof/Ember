package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/discovery"
)

func isolateEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func writeEnvFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.env")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsUnchangedWithoutSwitches(t *testing.T) {
	isolateEnv(t, dataDirEnv)
	cfg, err := loadConfig("", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pomodoro.DBPath != "/var/lib/ember/pomodoro.db" {
		t.Fatalf("db path = %q", cfg.Pomodoro.DBPath)
	}
	if cfg.AWTRIX.clockUnset || cfg.effectiveClockURL() != defaultDeviceBaseURL {
		t.Fatalf("clock changed without -user: unset=%v url=%q", cfg.AWTRIX.clockUnset, cfg.effectiveClockURL())
	}
	e, err := setupServerEnv(serverEnvInput{})
	if err != nil || e.active() {
		t.Fatalf("env = %+v err=%v", e, err)
	}
}

func TestDataDirRelocatesStores(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	isolateEnv(t, dataDirEnv)
	t.Setenv(dataDirEnv, dir)
	e, err := setupServerEnv(serverEnvInput{mkdir: true})
	if err != nil {
		t.Fatal(err)
	}
	if e.DataDir != dir || e.DataDirFrom != "env" {
		t.Fatalf("env = %+v", e)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("data dir: %v %v", st, err)
	}
	cfg, err := loadConfig("", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "pomodoro.db"); cfg.Pomodoro.DBPath != want {
		t.Fatalf("db path = %q want %q", cfg.Pomodoro.DBPath, want)
	}
	app := NewApp(cfg, &recordingPublisher{}, discardLogger())
	if err := app.ensureStore(cfg.Pomodoro.DBPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.store.Close() })
	if app.knobFW.dir != filepath.Join(dir, "firmware", "cinder-knob") {
		t.Fatalf("firmware dir = %q", app.knobFW.dir)
	}
	if app.coredumps.dir != filepath.Join(dir, "coredumps") {
		t.Fatalf("coredump dir = %q", app.coredumps.dir)
	}
}

func TestDataDirConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	isolateEnv(t, dataDirEnv, "CONFIG_PATH")
	t.Setenv(dataDirEnv, dir)
	if p, src := resolveConfigPath(""); p != "" {
		t.Fatalf("no file yet: %q %q", p, src)
	}
	cfgFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgFile, []byte(`{"pomodoro":{"db_path":"/explicit/p.db"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, src := resolveConfigPath(""); p != cfgFile || src != "data-dir" {
		t.Fatalf("got %q %q", p, src)
	}
	t.Setenv("CONFIG_PATH", "/elsewhere.json")
	if p, src := resolveConfigPath(""); p != "/elsewhere.json" || src != "env" {
		t.Fatalf("CONFIG_PATH must win: %q %q", p, src)
	}
	os.Unsetenv("CONFIG_PATH")
	cfg, err := loadConfig("", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pomodoro.DBPath != "/explicit/p.db" {
		t.Fatalf("explicit db_path must win, got %q", cfg.Pomodoro.DBPath)
	}
}

func TestEnvFileRejectsLooseModeWithoutLeaking(t *testing.T) {
	isolateEnv(t, "EMBER_TOKEN")
	p := writeEnvFile(t, 0o644, "EMBER_TOKEN=hunter2-secret\n")
	_, err := setupServerEnv(serverEnvInput{envFile: p})
	if err == nil {
		t.Fatal("0644 env file must fail startup")
	}
	if !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("error must name the path and reason: %v", err)
	}
	if strings.Contains(err.Error(), "hunter2") || os.Getenv("EMBER_TOKEN") != "" {
		t.Fatal("contents must not be read or echoed")
	}
}

func TestEnvFileMissingIsNotAnError(t *testing.T) {
	e, err := setupServerEnv(serverEnvInput{envFile: filepath.Join(t.TempDir(), "absent.env")})
	if err != nil || e.EnvFileFrom != "flag" {
		t.Fatalf("%+v %v", e, err)
	}
}

func TestEnvFileTokenAuthorisesWriteAndRealEnvWins(t *testing.T) {
	isolateEnv(t, "EMBER_TOKEN", dataDirEnv)
	p := writeEnvFile(t, 0o600, "# c\nEMBER_TOKEN=\"from-file\"\n")
	if _, err := setupServerEnv(serverEnvInput{envFile: p}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig("", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, &recordingPublisher{}, discardLogger())
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	body := map[string]any{"source": "a", "tool": "b", "session": "c", "state": "running"}
	if r := postJSON(t, srv, "/v1/status", body, map[string]string{"Authorization": "Bearer from-file"}); r.StatusCode != http.StatusOK {
		t.Fatalf("file token: %d", r.StatusCode)
	}

	os.Setenv("EMBER_TOKEN", "from-env")
	if _, err := setupServerEnv(serverEnvInput{envFile: p}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("EMBER_TOKEN"); got != "from-env" {
		t.Fatalf("real environment must win, got %q", got)
	}
}

func TestUserModeResolvesPathsFromInjectedHome(t *testing.T) {
	isolateEnv(t, dataDirEnv)
	home := t.TempDir()
	e, err := setupServerEnv(serverEnvInput{user: true, home: home, goos: "darwin", mkdir: true})
	if err != nil {
		t.Fatal(err)
	}
	wantData := filepath.Join(home, "Library", "Application Support", "Ember")
	if e.DataDir != wantData || e.DataDirFrom != "-user" {
		t.Fatalf("data dir %+v", e)
	}
	if e.EnvFile != filepath.Join(home, ".config", "ember", "server.env") || e.EnvFileFrom != "-user" {
		t.Fatalf("env file %+v", e)
	}
	if e.LogFile != filepath.Join(home, "Library", "Logs", "Ember", "server.log") {
		t.Fatalf("log %q", e.LogFile)
	}
	if st, err := os.Stat(wantData); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("data dir not created 0700: %v %v", st, err)
	}
	if os.Getenv(dataDirEnv) != wantData {
		t.Fatalf("EMBER_DATA_DIR = %q", os.Getenv(dataDirEnv))
	}

	isolateEnv(t, dataDirEnv)
	l, err := setupServerEnv(serverEnvInput{user: true, home: home, goos: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if l.DataDir != filepath.Join(home, ".local", "share", "ember") || l.LogFile != filepath.Join(l.DataDir, "server.log") {
		t.Fatalf("linux paths %+v", l)
	}
}

func TestUserModeExplicitSwitchesWin(t *testing.T) {
	isolateEnv(t, dataDirEnv)
	t.Setenv(dataDirEnv, "/explicit/data")
	home := t.TempDir()
	e, err := setupServerEnv(serverEnvInput{user: true, home: home, goos: "darwin", envFile: filepath.Join(home, "x.env")})
	if err != nil {
		t.Fatal(err)
	}
	if e.DataDir != "/explicit/data" || e.DataDirFrom != "env" || e.EnvFileFrom != "flag" {
		t.Fatalf("%+v", e)
	}
}

func TestServerLogRotatesAndWrites(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })
	p := filepath.Join(t.TempDir(), "logs", "server.log")
	if err := startServerLog(p); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log file: %v %v", st, err)
	}
}

func TestUserModeWithoutClockMakesNoClockRequest(t *testing.T) {
	isolateEnv(t, dataDirEnv)
	var hits atomic.Int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"uid":"awtrix_x","boardType":"awtrixng"}`))
	}))
	defer stub.Close()

	cfg, err := loadConfigFor("", discardLogger(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.effectiveClockURL(); got != "" {
		t.Fatalf("clock url = %q, want none", got)
	}
	a := guardApp(t, NewApp(cfg, &recordingPublisher{}, discardLogger()))
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: stub.URL, UID: "awtrix_x"}}, nil
	}
	ctx := context.Background()
	a.initDeviceDiscovery(ctx)
	a.clockJobs.Wait()
	a.watchTick(ctx, deviceProbe{}, time.Second)
	if n := hits.Load(); n != 0 {
		t.Fatalf("clock received %d requests", n)
	}
	if got := a.cfg.Load().effectiveClockURL(); got != "" {
		t.Fatalf("discovered clock was adopted: %q", got)
	}
	if got := a.lastRediscoverResult.Load(); got != "unclaimed" {
		t.Fatalf("lastRediscoverResult = %v", got)
	}

	a.updateConfig(func(c *Config) { c.AWTRIX.clockOverride = stub.URL })
	if got := a.cfg.Load().effectiveClockURL(); got != stub.URL {
		t.Fatalf("API-set clock must take effect, got %q", got)
	}
}

func TestUserModeExplicitClockURLIsKept(t *testing.T) {
	isolateEnv(t, dataDirEnv)
	f := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(f, []byte(`{"awtrix":{"http_base_url":"http://192.168.1.5"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfigFor(f, discardLogger(), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.effectiveClockURL() != "http://192.168.1.5" {
		t.Fatalf("url = %q", cfg.effectiveClockURL())
	}
}

func TestDoctorServerPathsCheck(t *testing.T) {
	var res DoctorResult
	res.Checks = map[string]CheckResult{}
	addServerPathsCheck(&res, serverEnv{})
	if _, ok := res.Checks["server_paths"]; ok {
		t.Fatal("no check when nothing is set")
	}
	addServerPathsCheck(&res, serverEnv{User: true, DataDir: "/d", DataDirFrom: "-user", EnvFile: "/e", EnvFileFrom: "-user"})
	c := res.Checks["server_paths"]
	if c.Status != StatusOK || !strings.Contains(c.Detail, "data_dir=/d (from -user)") || !strings.Contains(c.Detail, "env_file=/e (from -user)") {
		t.Fatalf("%+v", c)
	}
}

func TestScanSubcommandDoctorTakesUserFlags(t *testing.T) {
	if !doctorFlagTakesValue("-env-file") || doctorFlagTakesValue("-user") {
		t.Fatal("doctor flag table wrong")
	}
}
