package main

import (
	"context"
	"testing"
	"time"
)

// quietTestPub returns a decorator over rec whose window is 22:00-08:00 and
// whose clock reads the given wall-clock hour.
func quietTestPub(rec *recordingPublisher, enabled bool, hour int) *quietPublisher {
	cfg := Config{QuietHours: QuietHoursConfig{Enabled: enabled, Start: "22:00", End: "08:00"}}
	return &quietPublisher{
		next: rec,
		cfg:  func() *Config { return &cfg },
		now:  func() time.Time { return time.Date(2026, 6, 11, hour, 0, 0, 0, time.UTC) },
	}
}

func TestQuietPublisherMutesDuringWindow(t *testing.T) {
	rec := &recordingPublisher{}
	q := quietTestPub(rec, true, 23) // 23:00, inside 22:00-08:00

	orig := map[string]any{"text": "PING", "sound": "bell", "rtttl": "x:d=4:c"}
	if err := q.Notify(context.Background(), orig); err != nil {
		t.Fatal(err)
	}
	got := rec.NotifySnapshot()[0]
	if _, ok := got["sound"]; ok {
		t.Error("sound key not stripped")
	}
	if _, ok := got["rtttl"]; ok {
		t.Error("rtttl key not stripped")
	}
	if got["text"] != "PING" {
		t.Error("visual keys must pass through")
	}
	if _, ok := orig["sound"]; !ok {
		t.Error("caller's payload map must not be mutated")
	}

	if err := q.PlayRTTTL(context.Background(), "x:d=4:c"); err != nil {
		t.Fatal(err)
	}
	if err := q.PlaySound(context.Background(), "bell"); err != nil {
		t.Fatal(err)
	}
	if len(rec.rtttls) != 0 || len(rec.sounds) != 0 {
		t.Error("melody endpoints must no-op during quiet hours")
	}

	if err := q.CustomApp(context.Background(), "ember", nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.customApps) != 1 {
		t.Error("non-audio methods must delegate")
	}
}

func TestQuietPublisherPassesOutsideWindowAndWhenDisabled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		hour    int
	}{
		{"daytime", true, 12},
		{"disabled at night", false, 23},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingPublisher{}
			q := quietTestPub(rec, tc.enabled, tc.hour)
			payload := map[string]any{"text": "PING", "sound": "bell"}
			if err := q.Notify(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if rec.NotifySnapshot()[0]["sound"] != "bell" {
				t.Error("sound stripped outside quiet hours")
			}
			if err := q.PlayRTTTL(context.Background(), "x"); err != nil {
				t.Fatal(err)
			}
			if len(rec.rtttls) != 1 {
				t.Error("PlayRTTTL must delegate")
			}
		})
	}
}

func TestNewAppWrapsPublisherInQuietGate(t *testing.T) {
	a := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if _, ok := a.publisher.(*quietPublisher); !ok {
		t.Fatalf("App.publisher = %T, want *quietPublisher", a.publisher)
	}
}
