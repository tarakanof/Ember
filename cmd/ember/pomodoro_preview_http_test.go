package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func pomodoroPreviewServer(t *testing.T) *httptest.Server {
	t.Helper()
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pomodoro/preview", app.handlePomodoroPreview)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPomodoroPreview_ThreePhaseFrames(t *testing.T) {
	srv := pomodoroPreviewServer(t)
	p := decodeWeatherPreview(t, srv.URL+"/v1/pomodoro/preview")
	if len(p.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(p.Frames))
	}
	want := []string{"focus", "short_break", "long_break"}
	for i, card := range want {
		if p.Frames[i].Card != card {
			t.Errorf("frame %d card = %q, want %q", i, p.Frames[i].Card, card)
		}
		if len(p.Frames[i].Pixels) != 256 {
			t.Errorf("frame %d pixels = %d, want 256", i, len(p.Frames[i].Pixels))
		}
	}
}

func TestPomodoroPreview_ColorsApply(t *testing.T) {
	srv := pomodoroPreviewServer(t)
	def := decodeWeatherPreview(t, srv.URL+"/v1/pomodoro/preview")
	q := url.Values{"focus_color": {"#ff00ff"}, "break_color": {"#00ffff"}}
	custom := decodeWeatherPreview(t, srv.URL+"/v1/pomodoro/preview?"+q.Encode())
	for i := range def.Frames {
		if slicesEqualStr(def.Frames[i].Pixels, custom.Frames[i].Pixels) {
			t.Errorf("frame %q unchanged by colour override", def.Frames[i].Card)
		}
	}
}

func TestPomodoroPreview_DurationAffectsCountdown(t *testing.T) {
	srv := pomodoroPreviewServer(t)
	short := decodeWeatherPreview(t, srv.URL+"/v1/pomodoro/preview?focus_minutes=10")
	long := decodeWeatherPreview(t, srv.URL+"/v1/pomodoro/preview?focus_minutes=50")
	if slicesEqualStr(short.Frames[0].Pixels, long.Frames[0].Pixels) {
		t.Error("focus frame unchanged by focus_minutes")
	}
}
