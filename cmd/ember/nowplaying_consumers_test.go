package main

import (
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
