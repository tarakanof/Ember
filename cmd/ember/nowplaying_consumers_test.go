package main

import (
	"image/color"
	"net/http"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

func TestNowPlayingConsumersShareTheRegistryExpiry(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC)
	playing := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Title: "Song", DurationMS: 60_000}
	paused := playing
	paused.State = nowplaying.Paused
	songEnd := t0.Add(time.Minute + nowplaying.PlayingGrace)
	cases := []struct {
		name string
		rep  nowplaying.Report
		at   time.Time
		want string
	}{
		{"playing", playing, t0, "playing"},
		{"playing at the end grace", playing, songEnd, "playing"},
		{"playing past the end grace", playing, songEnd.Add(time.Millisecond), nowPlayingNone},
		{"paused just under the TTL", paused, t0.Add(nowplaying.PausedTTL - time.Second), "paused"},
		{"paused at the TTL", paused, t0.Add(nowplaying.PausedTTL), nowPlayingNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
			if _, err := app.nowPlaying.reg.Report(tc.rep, t0); err != nil {
				t.Fatal(err)
			}
			knob := app.knobNowPlaying(tc.at)
			state := app.nowPlayingState(tc.at)
			if knob.State != tc.want || state.State != tc.want {
				t.Fatalf("knob %q, /v1/nowplaying/state %q, want %q", knob.State, state.State, tc.want)
			}
		})
	}
}

func TestNowPlayingArtAndControlFollowTheRegistryExpiry(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		art     int
		control int
	}{
		{"paused a minute under the TTL", nowplaying.PausedTTL - time.Minute, http.StatusOK, http.StatusServiceUnavailable},
		{"paused a minute past the TTL", nowplaying.PausedTTL + time.Minute, http.StatusNotFound, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, srv := npServer(t)
			rep := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Paused, Title: "Song", TrackID: "T1", DurationMS: 60_000}
			if _, err := app.nowPlaying.reg.Report(rep, time.Now().Add(-tc.age)); err != nil {
				t.Fatal(err)
			}
			if err := app.nowPlaying.reg.SetArt("music", "M4", "T1", nowplaying.Album, nowplaying.NewImage(pngBytes(t, 8, color.White))); err != nil {
				t.Fatal(err)
			}
			if resp, b := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album", "", nil, nil); resp.StatusCode != tc.art {
				t.Errorf("art = %d %s, want %d", resp.StatusCode, b, tc.art)
			}
			if code, _, b := control(t, srv, testToken, `{"action":"next"}`, ""); code != tc.control {
				t.Errorf("control = %d %s, want %d", code, b, tc.control)
			}
		})
	}
}
