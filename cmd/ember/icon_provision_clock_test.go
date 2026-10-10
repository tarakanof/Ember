package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	script      string
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
		case r.Method == http.MethodGet && r.URL.Path == scriptPath && s.script != "":
			_, _ = io.WriteString(w, s.script)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/apps/"+berry.BootPingName:
			w.WriteHeader(http.StatusOK)
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

const unreachableClockURL = "http://unreachable-clock.invalid"

func stubRemoteClock(t *testing.T, a *App) {
	t.Helper()
	live := (&iconClockStub{fingerprint: true}).server(t)
	dead := deadClockServer(t)
	a.clock.connect = func(base string, timeout time.Duration) *awtrix.Client {
		switch {
		case loopbackURL(base):
		case invalidHostURL(base):
			base = dead.URL
		default:
			base = live.URL
		}
		return awtrix.NewClient(base, timeout)
	}
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) { return nil, nil }
}

func deadClockServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func invalidHostURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".invalid")
}

type loopbackOnlyTransport struct {
	mu      sync.Mutex
	refused []string
	next    http.RoundTripper
}

func (l *loopbackOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !loopbackURL(r.URL.String()) {
		l.mu.Lock()
		l.refused = append(l.refused, r.Method+" "+r.URL.String())
		l.mu.Unlock()
		return nil, errors.New("test refused a non-loopback request")
	}
	return l.next.RoundTrip(r)
}

func swapDefaultTransport(t *testing.T, wrap func(http.RoundTripper) http.RoundTripper) {
	t.Helper()
	t.Setenv("EMBER_TEST_DEFAULT_TRANSPORT_SWAP", "1")
	prev := http.DefaultTransport
	http.DefaultTransport = wrap(prev)
	t.Cleanup(func() { http.DefaultTransport = prev })
}

func requireLoopbackOnly(t *testing.T) {
	t.Helper()
	var guard *loopbackOnlyTransport
	swapDefaultTransport(t, func(next http.RoundTripper) http.RoundTripper {
		guard = &loopbackOnlyTransport{next: next}
		return guard
	})
	t.Cleanup(func() {
		guard.mu.Lock()
		defer guard.mu.Unlock()
		if len(guard.refused) > 0 {
			t.Errorf("non-loopback requests: %v", guard.refused)
		}
	})
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

func TestRequireLoopbackOnly_RefusesParallelTests(t *testing.T) {
	before := http.DefaultTransport
	t.Run("parallel", func(t *testing.T) {
		t.Parallel()
		var refused any
		func() {
			defer func() { refused = recover() }()
			requireLoopbackOnly(t)
		}()
		msg, _ := refused.(string)
		if !strings.Contains(msg, "t.Parallel") {
			t.Errorf("requireLoopbackOnly in a parallel test: recovered %v, want the testing package's t.Parallel panic", refused)
		}
		if http.DefaultTransport != before {
			t.Error("a parallel test swapped http.DefaultTransport")
		}
	})
}

func TestClockReachable_RejectsUnfingerprintedDevice(t *testing.T) {
	esphome := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"uid":"esp_1","boardType":"esphome"}`)
	}))
	t.Cleanup(esphome.Close)
	cases := []struct {
		name string
		base string
		want bool
	}{
		{"awtrix-ng", (&iconClockStub{fingerprint: true}).server(t).URL, true},
		{"no device route", (&iconClockStub{}).server(t).URL, false},
		{"other board", esphome.URL, false},
	}
	a := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := a.clock.reachable(context.Background(), c.base); got != c.want {
				t.Fatalf("reachable = %v, want %v", got, c.want)
			}
		})
	}
}

func TestStubRemoteClock_ProbesAndBrowseStayLocal(t *testing.T) {
	requireLoopbackOnly(t)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = unreachableClockURL
	a := NewApp(cfg, &recordingPublisher{}, testLogger())
	stubRemoteClock(t, a)
	ctx := context.Background()

	if !a.clock.reachable(ctx, "http://192.0.2.10") {
		t.Fatal("probe of a remote clock URL missed the stub")
	}
	if a.rediscoverClock(ctx) {
		t.Fatal("rediscovery swapped although the stubbed browse finds nothing")
	}
	if got := a.lastRediscoverResult.Load(); got != "no-device" {
		t.Fatalf("rediscover result = %v, want no-device", got)
	}
}

func TestStubRemoteClock_UnreachableHostFails(t *testing.T) {
	requireLoopbackOnly(t)
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = unreachableClockURL
	a := NewApp(cfg, &recordingPublisher{}, testLogger())
	stubRemoteClock(t, a)
	ctx := context.Background()

	if a.clock.reachable(ctx, unreachableClockURL) {
		t.Fatal("unreachable clock URL probed as reachable")
	}
	err := a.clock.do(ctx, callProbe, func(ctx context.Context, cl *awtrix.Client) error {
		_, err := cl.DeviceInfo(ctx)
		return err
	})
	if err == nil {
		t.Fatal("request to the unreachable clock URL succeeded")
	}
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

	if got := clock.requests(""); len(got) != 0 {
		t.Fatalf("unchanged clock URL got requests %v", got)
	}
}

type swapOnReadBody struct {
	io.Reader
	once sync.Once
	swap func()
}

func (b *swapOnReadBody) Read(p []byte) (int, error) {
	b.once.Do(b.swap)
	return b.Reader.Read(p)
}

func TestDeviceConfigPut_SwapDuringPutStillProvisions(t *testing.T) {
	pinned := &iconClockStub{fingerprint: true}
	pinnedSrv := pinned.server(t)
	swapped := &iconClockStub{fingerprint: true}
	swappedSrv := swapped.server(t)

	a := iconClockApp(t, pinnedSrv.URL)
	body := &swapOnReadBody{
		Reader: strings.NewReader(`{"base_url":"` + pinnedSrv.URL + `"}`),
		swap: func() {
			if !a.swapDiscoveredClock(pinnedSrv.URL, swappedSrv.URL) {
				t.Error("rediscovery swap did not apply")
			}
		},
	}
	w := httptest.NewRecorder()
	a.handleDeviceConfigPut(w, httptest.NewRequest("PUT", "/v1/device/config", body))
	a.clockJobs.Wait()
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /v1/device/config: %d %s", w.Code, w.Body)
	}

	if got := a.cfg.Load().effectiveClockURL(); got != pinnedSrv.URL {
		t.Fatalf("effective clock URL %s, want the pinned %s", got, pinnedSrv.URL)
	}
	if got := pinned.requests("POST /api/v1/files"); !slices.Equal(got, twoIconUploads) {
		t.Fatalf("pinned clock uploads %v, want the 2 pomodoro icons after the PUT moved the clock back from %s", got, swappedSrv.URL)
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

	started := make(chan struct{})
	waited := make(chan struct{})
	go func() {
		close(started)
		app.clockJobs.Wait()
		close(waited)
	}()
	<-started
	select {
	case <-waited:
		t.Fatal("clockJobs.Wait returned while the reload boot ping was still running")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	<-waited
}

func TestDeviceConfigPut_BootPingOffRemovesScript(t *testing.T) {
	old := &iconClockStub{fingerprint: true}
	oldSrv := old.server(t)
	moved := &iconClockStub{fingerprint: true, script: "stale boot ping"}
	movedSrv := moved.server(t)

	a := iconClockApp(t, oldSrv.URL)
	if err := putClockOverride(a, movedSrv.URL); err != nil {
		t.Fatal(err)
	}

	if got := moved.requests("DELETE /api/v1/apps/" + berry.BootPingName); len(got) != 1 {
		t.Fatalf("boot ping deletes %v on the new clock, want one; saw %v", got, moved.requests(""))
	}
	if got := moved.puts(); len(got) != 0 {
		t.Fatalf("boot ping installs %q with boot_ping off", got)
	}
}

func TestDeviceConfigPut_ClockOffStartsNoJobs(t *testing.T) {
	t.Setenv("EMBER_CLOCK", "off")
	old := &iconClockStub{fingerprint: true}
	oldSrv := old.server(t)
	moved := &iconClockStub{fingerprint: true}
	movedSrv := moved.server(t)

	a := iconClockApp(t, oldSrv.URL)
	var logs syncBuffer
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a.updateConfig(func(c *Config) { c.AWTRIX.BootPing = true })
	if err := putClockOverride(a, movedSrv.URL); err != nil {
		t.Fatal(err)
	}

	if got := append(old.requests(""), moved.requests("")...); len(got) != 0 {
		t.Fatalf("clock requests with EMBER_CLOCK=off: %v", got)
	}
	if strings.Contains(logs.String(), "boot ping") || strings.Contains(logs.String(), "icon provision") {
		t.Fatalf("clock jobs ran with EMBER_CLOCK=off:\n%s", logs.String())
	}
}

func TestEnsureNativeIcons_CancelStopsQuietly(t *testing.T) {
	clock := &iconClockStub{fingerprint: true}
	clockSrv := clock.server(t)
	a := iconClockApp(t, clockSrv.URL)
	var logs syncBuffer
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fetches atomic.Int32
	a.iconFetch = func(ctx context.Context, _ string) ([]byte, string, error) {
		fetches.Add(1)
		cancel()
		return nil, "", ctx.Err()
	}
	a.ensureNativeIcons(ctx)

	if n := fetches.Load(); n != 1 {
		t.Fatalf("icon fetches after cancel = %d, want the loop to stop at the first", n)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a cancelled icon job logged a warning:\n%s", logs.String())
	}
}

func TestEnsureBootPingScript_CancelIsQuiet(t *testing.T) {
	clock := &iconClockStub{fingerprint: true, stallScript: make(chan struct{})}
	clockSrv := clock.server(t)
	t.Cleanup(func() { close(clock.stallScript) })
	a := iconClockApp(t, clockSrv.URL)
	a.updateConfig(func(c *Config) { c.AWTRIX.BootPing = true })
	var logs syncBuffer
	a.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polled := make(chan struct{})
	var stalled atomic.Bool
	go func() {
		defer close(polled)
		defer cancel()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		deadline := time.After(5 * time.Second)
		for len(clock.requests("GET /api/v1/apps/script/")) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-deadline:
				stalled.Store(true)
				return
			case <-tick.C:
			}
		}
	}()
	a.ensureBootPingScript(ctx)
	cancel()
	<-polled

	if stalled.Load() {
		t.Fatal("boot ping job sent no script read within 5 s")
	}
	if len(clock.requests("GET /api/v1/apps/script/")) == 0 {
		t.Fatal("boot ping job returned before reading the script")
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a cancelled boot ping job logged a warning:\n%s", logs.String())
	}
}
