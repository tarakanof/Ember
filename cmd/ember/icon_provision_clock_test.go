package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
	"github.com/tarakanof/ember/internal/berry"
	"github.com/tarakanof/ember/internal/discovery"
)

type iconClockStub struct {
	mu          sync.Mutex
	fingerprint bool
	stallScript chan struct{}
	stallList   chan struct{}
	seen        []string
	scriptPuts  []string
}

func (s *iconClockStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	scriptPath := "/api/v1/apps/script/" + berry.BootPingName
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		if !s.fingerprint {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == scriptPath && s.stallScript != nil {
			select {
			case <-s.stallScript:
			case <-r.Context().Done():
			}
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/files" && s.stallList != nil {
			select {
			case <-s.stallList:
			case <-r.Context().Done():
			}
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/device":
			_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files":
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == scriptPath:
			b, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.scriptPuts = append(s.scriptPuts, string(b))
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"name":"`+berry.BootPingName+`","error":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *iconClockStub) requests(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, line := range s.seen {
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

func (s *iconClockStub) puts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.scriptPuts...)
}

func stubRemoteClock(t *testing.T, a *App) *iconClockStub {
	t.Helper()
	stub := &iconClockStub{fingerprint: true}
	srv := stub.server(t)
	a.clock.connect = func(base string, timeout time.Duration) *awtrix.Client {
		if !loopbackURL(base) {
			base = srv.URL
		}
		return awtrix.NewClient(base, timeout)
	}
	t.Cleanup(a.clockJobs.Wait)
	return stub
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func iconClockApp(t *testing.T, staleURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = staleURL
	cfg.Pomodoro.Enabled = true
	a := NewApp(cfg, nil, testLogger())
	a.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}
	t.Cleanup(a.clockJobs.Wait)
	return a
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var twoIconUploads = []string{"POST /api/v1/files?dir=%2FICONS", "POST /api/v1/files?dir=%2FICONS"}

func TestDeviceWatch_ProvisionsMovedClock(t *testing.T) {
	stale := &iconClockStub{}
	staleSrv := stale.server(t)
	moved := &iconClockStub{fingerprint: true}
	movedSrv := moved.server(t)

	a := iconClockApp(t, staleSrv.URL)
	a.updateConfig(func(c *Config) { c.AWTRIX.BootPing = true })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: movedSrv.URL, UID: "awtrix_test"}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.StartDeviceWatch(ctx, 20*time.Millisecond)
	}()
	waitFor(t, "icon uploads and boot ping install on the moved clock", func() bool {
		return len(moved.requests("POST /api/v1/files")) >= 2 && len(moved.puts()) >= 1
	})
	cancel()
	<-done
	a.clockJobs.Wait()

	if got := a.cfg.Load().effectiveClockURL(); got != movedSrv.URL {
		t.Fatalf("effective clock URL %s, want %s", got, movedSrv.URL)
	}
	if got := moved.requests("POST /api/v1/files"); !slices.Equal(got, twoIconUploads) {
		t.Fatalf("moved clock uploads %v, want the 2 pomodoro icons", got)
	}
	want := berry.BootPingSource(a.expectedBootCallback())
	if got := moved.puts(); len(got) != 1 || got[0] != want {
		t.Fatalf("boot ping installs %q, want one with callback %s", got, a.expectedBootCallback())
	}
	if got := stale.requests("GET /api/v1/files"); len(got) != 0 {
		t.Fatalf("stale clock URL got icon requests %v", got)
	}
	if got := stale.requests("GET /api/v1/apps/"); len(got) != 0 {
		t.Fatalf("stale clock URL got boot ping requests %v", got)
	}
}

func TestRediscoverClock_DoesNotWaitOnStalledBootPing(t *testing.T) {
	stale := &iconClockStub{}
	staleSrv := stale.server(t)
	moved := &iconClockStub{fingerprint: true, stallScript: make(chan struct{})}
	movedSrv := moved.server(t)

	a := iconClockApp(t, staleSrv.URL)
	t.Cleanup(func() { close(moved.stallScript) })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: movedSrv.URL, UID: "awtrix_test"}}, nil
	}

	start := time.Now()
	if !a.rediscoverClock(context.Background()) {
		t.Fatalf("expected rediscover to move the clock to %s", movedSrv.URL)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("rediscoverClock took %v behind a stalled boot ping script request", took)
	}
}

func TestInitDeviceDiscovery_ProvisionsReachableClockOnce(t *testing.T) {
	clock := &iconClockStub{fingerprint: true}
	clockSrv := clock.server(t)

	a := iconClockApp(t, clockSrv.URL)
	a.updateConfig(func(c *Config) { c.AWTRIX.BootPing = true })
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		t.Error("reachable clock must not be browsed for")
		return nil, nil
	}

	a.initDeviceDiscovery(context.Background())
	a.clockJobs.Wait()

	if got := clock.requests("GET /api/v1/files"); len(got) != 1 {
		t.Fatalf("icon list requests %v, want exactly one boot run", got)
	}
	if got := clock.requests("POST /api/v1/files"); !slices.Equal(got, twoIconUploads) {
		t.Fatalf("uploads %v, want the 2 pomodoro icons", got)
	}
	if got := clock.puts(); len(got) != 1 {
		t.Fatalf("boot ping installs %d, want exactly one boot run", len(got))
	}
}

func TestReapplySettings_PanickingHookReleasesHolds(t *testing.T) {
	a := newTestAppWithStore(t)
	register(a.settings.settingsOverlay, settingSpec[struct{}]{
		key:   "panic_hook_test",
		view:  func(Config) struct{} { return struct{}{} },
		apply: func(*Config, struct{}) error { return nil },
		after: func(Config) { panic("hook failed") },
	})
	if err := a.store.PutSetting("panic_hook_test", `{}`); err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() { _ = recover() }()
		a.reapplySettings()
	}()

	if got := a.iconHold.Load(); got != 0 {
		t.Fatalf("iconHold=%d after a panicking reapply, want 0", got)
	}
	a.clockSync.mu.Lock()
	paused := a.clockSync.paused
	a.clockSync.mu.Unlock()
	if paused != 0 {
		t.Fatalf("clock sync paused=%d after a panicking reapply, want 0", paused)
	}
}

func TestAdminReload_ProvisionsIcons(t *testing.T) {
	clock := &iconClockStub{fingerprint: true}
	clockSrv := clock.server(t)
	body := `{"awtrix":{"http_base_url":"` + clockSrv.URL + `"},"pomodoro":{"enabled":true}}`
	app, _ := newAppForReload(t, body)
	app.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}
	t.Cleanup(app.clockJobs.Wait)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload status=%d want 200", resp.StatusCode)
	}
	waitFor(t, "icon list request after reload", func() bool {
		return len(clock.requests("GET /api/v1/files")) > 0
	})
}

func TestBootSequence_ProvisionsIconsOnlyAfterRediscover(t *testing.T) {
	stale := &iconClockStub{}
	staleSrv := stale.server(t)
	moved := &iconClockStub{fingerprint: true}
	movedSrv := moved.server(t)

	a := iconClockApp(t, staleSrv.URL)
	if err := a.ensureStore(t.TempDir() + "/s.db"); err != nil {
		t.Fatal(err)
	}
	weatherBlob := `{"enabled":true,"provider":"open-meteo","latitude":52,"longitude":4,` +
		`"units":"metric","refresh_minutes":10,"popup_duration_seconds":30}`
	if err := a.store.PutSetting(weatherSettingsKey, weatherBlob); err != nil {
		t.Fatal(err)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: movedSrv.URL, UID: "awtrix_test"}}, nil
	}

	a.reapplySettings()
	a.clockJobs.Wait()
	a.initDeviceDiscovery(context.Background())
	a.clockJobs.Wait()
	if got := moved.requests("GET /api/v1/files"); len(got) != 1 {
		t.Fatalf("moved clock icon lists %v, want exactly one boot run", got)
	}

	if got := a.cfg.Load().effectiveClockURL(); got != movedSrv.URL {
		t.Fatalf("effective clock URL %s, want rediscovered %s", got, movedSrv.URL)
	}
	if got := stale.requests("GET /api/v1/files"); len(got) != 0 {
		t.Fatalf("stale clock URL got icon requests before rediscover: %v", got)
	}
	want := []string{"POST /api/v1/files?dir=%2FICONS", "POST /api/v1/files?dir=%2FICONS"}
	if got := moved.requests("POST /api/v1/files"); !slices.Equal(got, want) {
		t.Fatalf("rediscovered clock uploads %v, want the 2 pomodoro icons; stale saw %v", got, stale.requests(""))
	}
}

func TestDeviceConfigPut_ProvisionsNewClock(t *testing.T) {
	old := &iconClockStub{fingerprint: true}
	oldSrv := old.server(t)
	moved := &iconClockStub{fingerprint: true}
	movedSrv := moved.server(t)

	a := iconClockApp(t, oldSrv.URL)
	a.updateConfig(func(c *Config) { c.AWTRIX.BootPing = true })

	if err := putClockOverride(a, movedSrv.URL); err != nil {
		t.Fatal(err)
	}
	a.clockJobs.Wait()

	if got := moved.requests("POST /api/v1/files"); !slices.Equal(got, twoIconUploads) {
		t.Fatalf("new clock uploads %v, want the 2 pomodoro icons", got)
	}
	want := berry.BootPingSource(a.expectedBootCallback())
	if got := moved.puts(); len(got) != 1 || got[0] != want {
		t.Fatalf("boot ping installs %q, want one with callback %s", got, a.expectedBootCallback())
	}
	if got := old.requests(""); len(got) != 0 {
		t.Fatalf("previous clock URL got requests %v", got)
	}
}

func TestDeviceConfigPut_SameURLDoesNotProvision(t *testing.T) {
	clock := &iconClockStub{fingerprint: true}
	clockSrv := clock.server(t)

	a := iconClockApp(t, clockSrv.URL)
	if err := putClockOverride(a, clockSrv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	a.clockJobs.Wait()

	if got := clock.requests(""); len(got) != 0 {
		t.Fatalf("unchanged clock URL got requests %v", got)
	}
}

func TestAdminReload_TracksBootPing(t *testing.T) {
	clock := &iconClockStub{fingerprint: true, stallScript: make(chan struct{})}
	clockSrv := clock.server(t)
	app, _ := newAppForReload(t, `{"awtrix":{"http_base_url":"`+clockSrv.URL+`"}}`)
	release := sync.OnceFunc(func() { close(clock.stallScript) })
	t.Cleanup(release)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/admin/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload status=%d want 200", resp.StatusCode)
	}
	waitFor(t, "boot ping read after reload", func() bool {
		return len(clock.requests("GET /api/v1/apps/script/")) > 0
	})

	waited := make(chan struct{})
	go func() {
		app.clockJobs.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("clockJobs.Wait returned while the reload boot ping was still running")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	<-waited
}
