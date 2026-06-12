package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReminderPreview_BellFrame(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reminders/preview", app.handleReminderPreview)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p := decodeWeatherPreview(t, srv.URL+"/v1/reminders/preview")
	if len(p.Frames) != 1 || p.Frames[0].Card != "reminder" {
		t.Fatalf("frames = %+v, want one \"reminder\" frame", p.Frames)
	}
	px := p.Frames[0].Pixels
	if len(px) != 256 {
		t.Fatalf("pixels = %d, want 256", len(px))
	}
	// Bell row 5 is fully lit gold (#ffcc33) across cols 0-7.
	if px[5*32] != "#ffcc33" {
		t.Errorf("pixel (0,5) = %q, want bell gold #ffcc33", px[5*32])
	}
}
