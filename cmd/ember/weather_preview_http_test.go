package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func weatherPreviewServer(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/weather/preview", app.handleWeatherPreview)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return app, srv
}

func decodeWeatherPreview(t *testing.T, url string) render.Preview {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var p render.Preview
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWeatherPreview_SampleFallbackAndFlags(t *testing.T) {
	_, srv := weatherPreviewServer(t)

	// No live observation → sample data, both tiles by default.
	p := decodeWeatherPreview(t, srv.URL+"/v1/weather/preview")
	if len(p.Frames) != 2 || p.Frames[0].Card != "weather" || p.Frames[1].Card != "forecast" {
		t.Fatalf("default frames = %+v, want [weather forecast]", p.Frames)
	}
	if len(p.Frames[0].Pixels) != 256 {
		t.Errorf("pixels len = %d, want 256", len(p.Frames[0].Pixels))
	}

	// Toggles drop their tile.
	p = decodeWeatherPreview(t, srv.URL+"/v1/weather/preview?forecast_tile=false")
	if len(p.Frames) != 1 || p.Frames[0].Card != "weather" {
		t.Fatalf("forecast off: frames = %+v, want [weather]", p.Frames)
	}
	p = decodeWeatherPreview(t, srv.URL+"/v1/weather/preview?rotate_in_apps=false&forecast_tile=false")
	if len(p.Frames) != 0 {
		t.Fatalf("both off: %d frames, want 0", len(p.Frames))
	}
}

func TestWeatherPreview_LiveObservationWins(t *testing.T) {
	app, srv := weatherPreviewServer(t)
	app.weather.mu.Lock()
	app.weather.obs = weatherObservation{Condition: render.WeatherSnow, TempC: -3,
		Hourly: []float64{-3, -2}, FetchedAt: time.Now()}
	app.weather.have = true
	app.weather.mu.Unlock()

	live := decodeWeatherPreview(t, srv.URL+"/v1/weather/preview")
	app.weather.mu.Lock()
	app.weather.have = false
	app.weather.mu.Unlock()
	sample := decodeWeatherPreview(t, srv.URL+"/v1/weather/preview")
	if len(live.Frames) == 0 || len(sample.Frames) == 0 {
		t.Fatal("both variants must render frames")
	}
	if slicesEqualStr(live.Frames[0].Pixels, sample.Frames[0].Pixels) {
		t.Errorf("live observation must render differently from the sample")
	}
}

func slicesEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
