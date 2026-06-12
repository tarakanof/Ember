package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

var errTest = errors.New("test error")

func iconTestApp(t *testing.T, mutate func(*WeatherConfig)) (*App, *recordingPublisher) {
	t.Helper()
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	mutate(&cfg.Weather)
	app := NewApp(cfg, pub, testLogger())
	app.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}
	return app, pub
}

func TestEnsureWeatherIcons_UploadsMissing(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) { w.TileNativeIcons = true })
	pub.icons = []string{"2289.gif"} // snow already on the device

	app.ensureWeatherIcons(context.Background())

	got := pub.PutIconNamesSnapshot()
	slices.Sort(got)
	// All six defaults minus the present snow icon.
	want := []string{"11428.gif", "1338.gif", "17056.gif", "2286.gif", "72.gif"}
	if !slices.Equal(got, want) {
		t.Fatalf("uploaded %v, want %v", got, want)
	}
}

func TestEnsureWeatherIcons_NoopWhenNativeOff(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) {
		w.TileNativeIcons = false
		w.UseNativeIcons = false
	})
	app.ensureWeatherIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 0 {
		t.Fatalf("uploaded %v with both native toggles off, want none", got)
	}
}

func TestEnsureWeatherIcons_PopupToggleAloneProvisions(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) { w.UseNativeIcons = true })
	app.ensureWeatherIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 6 {
		t.Fatalf("uploaded %d icons, want all 6 (popups need them too)", len(got))
	}
}

func TestEnsureWeatherIcons_HonorsOverrides(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) {
		w.TileNativeIcons = true
		w.IconIDs = map[string]string{"clear": "9999"}
	})
	app.ensureWeatherIcons(context.Background())
	got := pub.PutIconNamesSnapshot()
	if !slices.Contains(got, "9999.gif") {
		t.Errorf("override ID 9999 not uploaded: %v", got)
	}
	if slices.Contains(got, "1338.gif") {
		t.Errorf("default clear ID uploaded despite override: %v", got)
	}
}

func TestEnsureWeatherIcons_ListFailureUploadsNothing(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) { w.TileNativeIcons = true })
	pub.iconsErr = errTest
	app.ensureWeatherIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 0 {
		t.Fatalf("uploaded %v despite list failure (would re-upload blindly forever)", got)
	}
}
