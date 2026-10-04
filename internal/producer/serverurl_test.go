package producer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsAutoServerURL(t *testing.T) {
	for v, want := range map[string]bool{
		"": true, "  ": true, "auto": true, "AUTO": true, " Auto ": true,
		"http://h:3627": false,
	} {
		if got := IsAutoServerURL(v); got != want {
			t.Errorf("IsAutoServerURL(%q)=%v want %v", v, got, want)
		}
	}
}

var (
	srvA = DiscoveredServer{Name: "Ember", Host: "unraid.local.", URL: "http://192.0.2.36:3627", Version: "1.4.0"}
	srvB = DiscoveredServer{Name: "Ember (2)", Host: "build-1.local.", URL: "http://192.0.2.50:3627"}
)

func TestPickServerSingle(t *testing.T) {
	got, err := PickServer([]DiscoveredServer{srvA}, "")
	if err != nil || got != srvA {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPickServerNoneIsErrNoServer(t *testing.T) {
	if _, err := PickServer(nil, ""); !errors.Is(err, ErrNoServer) {
		t.Fatalf("err = %v, want ErrNoServer", err)
	}
}

func TestPickServerDedupesSameURL(t *testing.T) {
	dup := srvA
	dup.Host = "unraid.local"
	got, err := PickServer([]DiscoveredServer{srvA, dup}, "")
	if err != nil || got.URL != srvA.URL {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPickServerMultipleWithoutPreferenceIsAmbiguous(t *testing.T) {
	_, err := PickServer([]DiscoveredServer{srvA, srvB}, "")
	var amb *AmbiguousServersError
	if !errors.As(err, &amb) || len(amb.Servers) != 2 {
		t.Fatalf("err = %v, want AmbiguousServersError with 2", err)
	}
	for _, want := range []string{"2 Ember servers", srvA.URL, srvB.URL, "EMBER_SERVER_URL", "EMBER_SERVER_INSTANCE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestPickServerPrefersInstanceOrHost(t *testing.T) {
	for _, prefer := range []string{"Ember (2)", "ember (2)", "build-1", "build-1.local", "192.0.2.50"} {
		got, err := PickServer([]DiscoveredServer{srvA, srvB}, prefer)
		if err != nil || got != srvB {
			t.Errorf("prefer %q: got %+v, %v", prefer, got, err)
		}
	}
}

func TestPickServerPreferenceMatchingNothingFails(t *testing.T) {
	_, err := PickServer([]DiscoveredServer{srvA}, "nas")
	if err == nil || !strings.Contains(err.Error(), `"nas"`) {
		t.Fatalf("err = %v, want no-match error naming the preference", err)
	}
}

func fakeBrowser(found []DiscoveredServer, err error, calls *int32) ServerBrowser {
	return func(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error) {
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		return found, err
	}
}

func TestLocatorDiscoverCachesResult(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "state", "server.json")
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	loc := &ServerLocator{Browse: fakeBrowser([]DiscoveredServer{srvA}, nil, nil), CachePath: cache, Now: func() time.Time { return now }}
	got, err := loc.Discover(context.Background())
	if err != nil || got != srvA {
		t.Fatalf("got %+v, %v", got, err)
	}
	c, ok := ReadServerCache(cache)
	if !ok || c.URL != srvA.URL || c.Name != "Ember" || !c.DiscoveredAt.Equal(now) || c.Prefer != "" {
		t.Fatalf("cache = %+v, %v", c, ok)
	}
}

func TestLocatorDiscoverFailureKeepsCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "server.json")
	if err := WriteServerCache(cache, ServerCache{DiscoveredServer: srvA}); err != nil {
		t.Fatal(err)
	}
	loc := &ServerLocator{Browse: fakeBrowser(nil, nil, nil), CachePath: cache}
	if _, err := loc.Discover(context.Background()); !errors.Is(err, ErrNoServer) {
		t.Fatalf("err = %v", err)
	}
	if c, ok := ReadServerCache(cache); !ok || c.URL != srvA.URL {
		t.Fatalf("cache lost: %+v %v", c, ok)
	}
}

func TestResolveServerURL(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	if got, auto := ResolveServerURL(home, "http://explicit:1", ""); got != "http://explicit:1" || auto {
		t.Errorf("explicit: %q %v", got, auto)
	}
	if got, auto := ResolveServerURL(home, "auto", ""); got != "" || !auto {
		t.Errorf("auto, no cache: %q %v", got, auto)
	}
	if err := WriteServerCache(ServerCachePath(home), ServerCache{DiscoveredServer: srvA}); err != nil {
		t.Fatal(err)
	}
	if got, auto := ResolveServerURL(home, "", ""); got != srvA.URL || !auto {
		t.Errorf("empty, cached: %q %v", got, auto)
	}
	if got, _ := ResolveServerURL(home, "", "nas"); got != "" {
		t.Errorf("cache made without EMBER_SERVER_INSTANCE used for prefer=nas: %q", got)
	}
	if got := ServerCachePath(home); got != filepath.Join(home, ".local", "state", "ember", "server.json") {
		t.Errorf("cache path %q", got)
	}
}

func TestAutoServerRebrowsesAfterRepeatedTransportFailures(t *testing.T) {
	var calls int32
	cache := filepath.Join(t.TempDir(), "server.json")
	moved := srvA
	moved.URL = "http://192.0.2.99:3627"
	now := time.Unix(1000, 0)
	loc := &ServerLocator{Browse: fakeBrowser([]DiscoveredServer{moved}, nil, &calls), CachePath: cache}
	a := NewAutoServer(loc, srvA.URL)
	a.now = func() time.Time { return now }
	transport := errors.New("dial tcp: connection refused")

	for i := 0; i < autoServerFailureThreshold-1; i++ {
		a.Report(transport)
	}
	if calls != 0 || a.URL() != srvA.URL {
		t.Fatalf("browsed too early: calls=%d url=%s", calls, a.URL())
	}
	a.Report(nil) // a success resets the streak
	for i := 0; i < autoServerFailureThreshold; i++ {
		a.Report(transport)
	}
	a.browsing.Wait()
	if calls != 1 || a.URL() != moved.URL {
		t.Fatalf("after %d failures: calls=%d url=%s", autoServerFailureThreshold, calls, a.URL())
	}
	if c, _ := ReadServerCache(cache); c.URL != moved.URL {
		t.Errorf("cache not updated: %+v", c)
	}

	// Still failing: no new browse until the min interval passes.
	for i := 0; i < 2*autoServerFailureThreshold; i++ {
		a.Report(transport)
	}
	a.browsing.Wait()
	if calls != 1 {
		t.Fatalf("re-browsed within the interval: calls=%d", calls)
	}
	now = now.Add(autoServerMinInterval)
	for i := 0; i < autoServerFailureThreshold; i++ {
		a.Report(transport)
	}
	a.browsing.Wait()
	if calls != 2 {
		t.Fatalf("no re-browse after the interval: calls=%d", calls)
	}
}

func TestAutoServerIgnoresContextErrors(t *testing.T) {
	var calls int32
	a := NewAutoServer(&ServerLocator{Browse: fakeBrowser([]DiscoveredServer{srvB}, nil, &calls)}, srvA.URL)
	for i := 0; i < 3*autoServerFailureThreshold; i++ {
		a.Report(context.DeadlineExceeded)
		a.Report(context.Canceled)
	}
	if calls != 0 {
		t.Fatalf("context errors triggered a browse: %d", calls)
	}
}

func TestAutoServerEnsureDiscoversWhenEmpty(t *testing.T) {
	a := NewAutoServer(&ServerLocator{Browse: fakeBrowser([]DiscoveredServer{srvB}, nil, nil)}, "")
	got, err := a.Ensure(context.Background())
	if err != nil || got != srvB.URL || a.URL() != srvB.URL {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestClientWithAutoServerFollowsRediscovery(t *testing.T) {
	var hits int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer good.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	loc := &ServerLocator{Browse: fakeBrowser([]DiscoveredServer{{Name: "Ember", URL: good.URL}}, nil, nil)}
	a := NewAutoServer(loc, deadURL)
	c := NewClient("", "tok", time.Second).WithAutoServer(a)
	ctx := context.Background()
	for i := 0; i < autoServerFailureThreshold; i++ {
		if err := c.Post(ctx, StatusRequest{Source: "s", Tool: "codex", Session: "x", State: "running"}); err == nil {
			t.Fatalf("post %d to a dead server succeeded", i)
		}
	}
	a.browsing.Wait()
	if err := c.Post(ctx, StatusRequest{Source: "s", Tool: "codex", Session: "x", State: "running"}); err != nil {
		t.Fatalf("post after rediscovery: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
}

func TestServerReportExplicitURL(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	defer ok.Close()
	lines := ServerReport(context.Background(), ServerReportInput{Configured: ok.URL})
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "reachable ("+ok.URL+")") {
		t.Errorf("got:\n%s", got)
	}
}

func TestServerReportAutoShowsDiscoveredURL(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	home := t.TempDir()
	found := DiscoveredServer{Name: "Ember", Host: "unraid.local.", URL: ok.URL, Version: "1.4.0"}
	lines := ServerReport(context.Background(), ServerReportInput{
		Configured: "auto",
		Home:       home,
		Browse:     fakeBrowser([]DiscoveredServer{found}, nil, nil),
	})
	got := strings.Join(lines, "\n")
	for _, want := range []string{"auto (mDNS _ember._tcp)", "cached: (none)", "found 1", "Ember @ unraid.local. → " + ok.URL, "using " + ok.URL, "reachable"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if c, ok2 := ReadServerCache(ServerCachePath(home)); !ok2 || c.URL != ok.URL {
		t.Errorf("doctor did not cache the pick: %+v", c)
	}
}

func TestServerReportAutoAmbiguousTellsUserWhatToSet(t *testing.T) {
	lines := ServerReport(context.Background(), ServerReportInput{
		Configured: "",
		Home:       t.TempDir(),
		Browse:     fakeBrowser([]DiscoveredServer{srvA, srvB}, nil, nil),
	})
	got := strings.Join(lines, "\n")
	for _, want := range []string{"found 2", "set EMBER_SERVER_URL"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
}

func TestServerReportAutoNothingFoundMentionsHostNetworking(t *testing.T) {
	lines := ServerReport(context.Background(), ServerReportInput{
		Configured: "auto",
		Home:       t.TempDir(),
		Browse:     fakeBrowser(nil, nil, nil),
	})
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "found 0") || !strings.Contains(got, "host networking") {
		t.Errorf("got:\n%s", got)
	}
}

func TestTokenHint(t *testing.T) {
	for tok, wantHint := range map[string]bool{"": true, TokenPlaceholder: true, " ": true, "s3cret": false} {
		if got := TokenHint(tok) != ""; got != wantHint {
			t.Errorf("TokenHint(%q) hint=%v want %v", tok, got, wantHint)
		}
	}
}

func TestDaemonServerExplicitURLNeedsNoDiscovery(t *testing.T) {
	a, ok := DaemonServer(context.Background(), t.TempDir(), "http://h:1", false, "")
	if a != nil || !ok {
		t.Fatalf("got %v, %v", a, ok)
	}
	if _, ok := DaemonServer(context.Background(), t.TempDir(), "", false, ""); ok {
		t.Fatal("no URL and no discovery must be not ok")
	}
}

func TestDaemonServerAutoWithCacheStartsFromIt(t *testing.T) {
	a, ok := DaemonServer(context.Background(), t.TempDir(), srvA.URL, true, "")
	if !ok || a == nil || a.URL() != srvA.URL {
		t.Fatalf("got %v, %v", a, ok)
	}
}

func TestAutoServerReportDoesNotBlockOnBrowse(t *testing.T) {
	release := make(chan struct{})
	slow := func(ctx context.Context, timeout time.Duration) ([]DiscoveredServer, error) {
		<-release
		return []DiscoveredServer{srvB}, nil
	}
	a := NewAutoServer(&ServerLocator{Browse: slow}, srvA.URL)
	done := make(chan struct{})
	go func() {
		for i := 0; i < autoServerFailureThreshold; i++ {
			a.Report(errors.New("connection refused"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Report blocked on the re-browse")
	}
	if a.URL() != srvA.URL {
		t.Fatal("URL changed before the browse finished")
	}
	close(release)
	a.browsing.Wait()
	if a.URL() != srvB.URL {
		t.Fatalf("URL = %s after the browse, want %s", a.URL(), srvB.URL)
	}
}
