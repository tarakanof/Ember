package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/berry"
	"github.com/tarakanof/ember/internal/discovery"
)

type iconClockStub struct {
	mu          sync.Mutex
	fingerprint bool
	seen        []string
}

func (s *iconClockStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		if !s.fingerprint {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/device":
			_, _ = w.Write([]byte(`{"uid":"awtrix_test","boardType":"awtrixng"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/files":
			_, _ = w.Write([]byte(`{"files":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/files":
			w.WriteHeader(http.StatusOK)
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

func iconClockApp(t *testing.T, staleURL string) *App {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = staleURL
	cfg.Pomodoro.Enabled = true
	a := NewApp(cfg, nil, testLogger())
	a.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}
	t.Cleanup(a.iconJobs.Wait)
	return a
}

func TestRediscoverClock_ProvisionsIconsOnMovedClock(t *testing.T) {
	stale := &iconClockStub{}
	staleSrv := stale.server(t)
	moved := &iconClockStub{fingerprint: true}
	movedSrv := moved.server(t)

	a := iconClockApp(t, staleSrv.URL)
	a.browseFn = func(context.Context, time.Duration) ([]discovery.Candidate, error) {
		return []discovery.Candidate{{BaseURL: movedSrv.URL, UID: "awtrix_test"}}, nil
	}

	if !a.rediscoverClock(context.Background()) {
		t.Fatalf("expected rediscover to move the clock to %s", movedSrv.URL)
	}
	a.iconJobs.Wait()

	if got := moved.requests("GET /api/v1/files"); len(got) == 0 {
		t.Fatalf("moved clock got no icon list request; saw %v", moved.requests(""))
	}
	uploads := moved.requests("POST /api/v1/files")
	if len(uploads) != 2 {
		t.Fatalf("moved clock got %d icon uploads, want 2 (pomodoro icons); saw %v", len(uploads), moved.requests(""))
	}
	if got := stale.requests("GET /api/v1/files"); len(got) != 0 {
		t.Fatalf("stale clock URL got icon requests %v", got)
	}
	if got := moved.requests("GET /api/v1/apps/script/" + berry.BootPingName); len(got) == 0 {
		t.Fatalf("moved clock got no boot ping script check; saw %v", moved.requests(""))
	}
	if got := stale.requests("GET /api/v1/apps/"); len(got) != 0 {
		t.Fatalf("stale clock URL got boot ping requests %v", got)
	}
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
	a.iconJobs.Wait()
	a.initDeviceDiscovery(context.Background())
	a.iconJobs.Wait()

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
