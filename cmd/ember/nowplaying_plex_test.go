package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

const fakePlexToken = "plex-fake-token"

type fakePlex struct {
	mu          sync.Mutex
	sessions    string
	photoHits   []string
	badToken    bool
	playerHits  []string
	timelineVol string
	playerCode  int
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
		case "/player/timeline/poll":
			f.playerHits = append(f.playerHits, r.URL.Path+" "+r.Header.Get("X-Plex-Target-Client-Identifier"))
			_, _ = w.Write([]byte(`<MediaContainer commandID="1"><Timeline type="video" state="stopped"/>` +
				`<Timeline type="music" state="playing" volume="` + f.timelineVol + `"/></MediaContainer>`))
		default:
			if strings.HasPrefix(r.URL.Path, "/player/playback/") {
				q := r.URL.Query()
				hit := r.URL.Path + " " + r.Header.Get("X-Plex-Target-Client-Identifier") + " type=" + q.Get("type")
				if v := q.Get("volume"); v != "" {
					hit += " volume=" + v
				}
				if q.Get("commandID") == "" || r.Header.Get("X-Plex-Client-Identifier") == "" {
					hit += " MISSING-IDS"
				}
				f.playerHits = append(f.playerHits, hit)
				if f.playerCode != 0 {
					w.WriteHeader(f.playerCode)
				}
				return
			}
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

func TestPlexWebhookWakesPoller(t *testing.T) {
	app, srv := npServer(t)
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=k", "", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("without plex: %d, want 404", resp.StatusCode)
	}
	app.nowPlaying.Plex = nowplaying.NewPlexSource(nowplaying.PlexConfig{URL: "http://plex", Token: "t", WebhookKey: "secret"})
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=wrong", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d, want 401", resp.StatusCode)
	}
	if resp, _ := npDo(t, srv, "POST", "/hooks/plex?key=secret", "", nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("right key: %d, want 204", resp.StatusCode)
	}
	select {
	case <-app.nowPlaying.Plex.Nudge:
	default:
		t.Fatal("webhook did not wake the poller")
	}
}

func TestStartNowPlayingStopsWithContext(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	_, srv := newFakePlex(t, nil)
	app.nowPlaying.Plex = nowplaying.NewPlexSource(nowplaying.PlexConfig{URL: srv.URL, Token: fakePlexToken})
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
