package main

import (
	"bytes"
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakePlexToken = "plex-fake-token"

type fakePlex struct {
	mu        sync.Mutex
	sessions  string
	photoHits []string
	badToken  bool
}

func newFakePlex(t *testing.T, art []byte) (*fakePlex, *httptest.Server) {
	t.Helper()
	f := &fakePlex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Plex-Token") != fakePlexToken || r.URL.Query().Has("X-Plex-Token") {
			f.badToken = true
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/status/sessions":
			_, _ = w.Write([]byte(f.sessions))
		case "/photo/:/transcode":
			f.photoHits = append(f.photoHits, r.URL.Query().Get("url"))
			_, _ = w.Write(art)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakePlex) set(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = s
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/nowplaying/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlexPollReportsMusicSessionWithArt(t *testing.T) {
	fake, srv := newFakePlex(t, pngBytes(t, 120, color.RGBA{0, 0, 200, 255}))
	fake.set(readFixture(t, "plex_sessions.json"))
	np := newNowPlayingService()
	p := newPlexSource(plexConfig{URL: srv.URL, Token: fakePlexToken, User: "dt"})
	var logs bytes.Buffer
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	if !p.poll(context.Background(), np, captureLogger(&logs), now) {
		t.Fatalf("poll should report playing; logs: %s", logs.String())
	}
	e, ok := np.reg.Current(now)
	if !ok {
		t.Fatal("no entry")
	}
	if e.Source != "plex" || e.Player != "Plexamp" || e.Title != "Teardrop" || e.Artist != "Massive Attack" ||
		e.Album != "Mezzanine" || e.TrackID != "4242" || e.PositionMS != 61000 || e.DurationMS != 330000 {
		t.Fatalf("entry = %+v", e.Report)
	}
	if e.AlbumArt == nil || e.ArtistArt == nil {
		t.Fatal("album and artist art should come from Plex")
	}
	if fake.badToken {
		t.Fatal("token not sent as the X-Plex-Token header only")
	}
	if len(fake.photoHits) != 2 || fake.photoHits[0] != "/library/metadata/4240/thumb/1" || fake.photoHits[1] != "/library/metadata/4239/thumb/1" {
		t.Fatalf("photo fetches = %v", fake.photoHits)
	}
	if strings.Contains(logs.String(), fakePlexToken) {
		t.Fatal("token logged")
	}

	p.poll(context.Background(), np, testLogger(), now.Add(2*time.Second))
	if len(fake.photoHits) != 2 {
		t.Fatalf("unchanged art fetched again: %v", fake.photoHits)
	}

	fake.set(`{"MediaContainer":{"size":0}}`)
	if p.poll(context.Background(), np, testLogger(), now.Add(4*time.Second)) {
		t.Fatal("no session should not count as playing")
	}
	if _, ok := np.reg.Current(now.Add(4 * time.Second)); ok {
		t.Fatal("entry kept after the session ended")
	}
}

func TestPlexPollFiltersUserAndSkipsVideo(t *testing.T) {
	fake, srv := newFakePlex(t, pngBytes(t, 10, color.White))
	fake.set(readFixture(t, "plex_sessions.json"))
	np := newNowPlayingService()
	p := newPlexSource(plexConfig{URL: srv.URL, Token: fakePlexToken, User: "someone-else"})
	p.poll(context.Background(), np, testLogger(), time.Now())
	if _, ok := np.reg.Current(time.Now()); ok {
		t.Fatal("other user's track or a video session was reported")
	}
}

func TestPlexPollFailureKeepsGoing(t *testing.T) {
	np := newNowPlayingService()
	p := newPlexSource(plexConfig{URL: closedURL(t), Token: fakePlexToken})
	var logs bytes.Buffer
	if p.poll(context.Background(), np, captureLogger(&logs), time.Now()) {
		t.Fatal("unreachable Plex reported playing")
	}
	p.poll(context.Background(), np, captureLogger(&logs), time.Now())
	if n := strings.Count(logs.String(), "plex poll failed"); n != 1 {
		t.Fatalf("failure logged %d times, want once", n)
	}
}

func TestPlexConfigFromEnv(t *testing.T) {
	env := map[string]string{"EMBER_PLEX_URL": "http://plex.lan:32400/", "EMBER_PLEX_TOKEN": "x"}
	c, ok := plexConfigFromEnv(func(k string) string { return env[k] })
	if !ok || c.URL != "http://plex.lan:32400" {
		t.Fatalf("config = %+v ok=%v", c, ok)
	}
	env["EMBER_PLEX_TOKEN"] = ""
	if _, ok := plexConfigFromEnv(func(k string) string { return env[k] }); ok {
		t.Fatal("enabled without a token")
	}
	env["EMBER_PLEX_TOKEN"], env["EMBER_PLEX_URL"] = "x", "plex.lan"
	if _, ok := plexConfigFromEnv(func(k string) string { return env[k] }); ok {
		t.Fatal("enabled with a schemeless URL")
	}
}

func TestPlexWebhookWakesPoller(t *testing.T) {
	app, srv := npServer(t)
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=k", "", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("without plex: %d, want 404", resp.StatusCode)
	}
	app.nowPlaying.plex = newPlexSource(plexConfig{URL: "http://plex", Token: "t", WebhookKey: "secret"})
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=wrong", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d, want 401", resp.StatusCode)
	}
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=secret", "", nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("right key: %d, want 204", resp.StatusCode)
	}
	select {
	case <-app.nowPlaying.plex.nudge:
	default:
		t.Fatal("webhook did not wake the poller")
	}
}

func TestStartNowPlayingStopsWithContext(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	_, srv := newFakePlex(t, nil)
	app.nowPlaying.plex = newPlexSource(plexConfig{URL: srv.URL, Token: fakePlexToken})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { app.StartNowPlaying(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartNowPlaying did not stop")
	}
}

func TestPlexArtRetriesAfterFailure(t *testing.T) {
	fake, srv := newFakePlex(t, []byte("broken"))
	fake.set(readFixture(t, "plex_sessions.json"))
	np := newNowPlayingService()
	p := newPlexSource(plexConfig{URL: srv.URL, Token: fakePlexToken, User: "dt"})
	var logs bytes.Buffer
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	p.poll(context.Background(), np, captureLogger(&logs), now)
	p.poll(context.Background(), np, captureLogger(&logs), now.Add(2*time.Second))
	if len(fake.photoHits) != 4 {
		t.Fatalf("failed art not retried: %v", fake.photoHits)
	}
	if n := strings.Count(logs.String(), "plex artwork fetch failed"); n != 2 {
		t.Fatalf("failure logged %d times, want once per path", n)
	}
}

func TestPlexStaleViewOffsetKeepsExtrapolating(t *testing.T) {
	fake, srv := newFakePlex(t, pngBytes(t, 10, color.White))
	fake.set(readFixture(t, "plex_sessions.json"))
	np := newNowPlayingService()
	p := newPlexSource(plexConfig{URL: srv.URL, Token: fakePlexToken, User: "dt"})
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	p.poll(context.Background(), np, testLogger(), now)
	p.poll(context.Background(), np, testLogger(), now.Add(10*time.Second))
	e, _ := np.reg.Current(now.Add(10 * time.Second))
	if !e.PositionAt.Equal(now) || e.Position(now.Add(10*time.Second)) != 71000 {
		t.Fatalf("stale viewOffset re-anchored: %d at %v", e.PositionMS, e.PositionAt)
	}
}

func TestPlexRefusesRedirects(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Plex-Token") != ""
	}))
	t.Cleanup(other.Close)
	plex := httptest.NewServer(http.RedirectHandler(other.URL+"/status/sessions", http.StatusFound))
	t.Cleanup(plex.Close)
	p := newPlexSource(plexConfig{URL: plex.URL, Token: fakePlexToken})
	if _, err := p.session(context.Background()); err == nil {
		t.Fatal("redirect answered as sessions")
	}
	if leaked {
		t.Fatal("token followed a redirect")
	}
}
