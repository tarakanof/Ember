package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
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

	app.ensureNativeIcons(context.Background())

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
	app.ensureNativeIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 0 {
		t.Fatalf("uploaded %v with both native toggles off, want none", got)
	}
}

func TestEnsureWeatherIcons_PopupToggleAloneProvisions(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) { w.UseNativeIcons = true })
	app.ensureNativeIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 6 {
		t.Fatalf("uploaded %d icons, want all 6 (popups need them too)", len(got))
	}
}

func TestEnsureWeatherIcons_HonorsOverrides(t *testing.T) {
	app, pub := iconTestApp(t, func(w *WeatherConfig) {
		w.TileNativeIcons = true
		w.IconIDs = map[string]string{"clear": "9999"}
	})
	app.ensureNativeIcons(context.Background())
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
	app.ensureNativeIcons(context.Background())
	if got := pub.PutIconNamesSnapshot(); len(got) != 0 {
		t.Fatalf("uploaded %v despite list failure (would re-upload blindly forever)", got)
	}
}

func TestEnsureNativeIcons_PomodoroEnabledProvisionsTomatoAndCoffee(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = false // isolate: weather must contribute nothing here
	cfg.Pomodoro.Enabled = true
	app := NewApp(cfg, pub, testLogger())
	app.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}

	app.ensureNativeIcons(context.Background())

	got := pub.PutIconNamesSnapshot()
	slices.Sort(got)
	want := []string{"29802.gif", "6396.gif"}
	if !slices.Equal(got, want) {
		t.Fatalf("uploaded %v, want %v", got, want)
	}
}

func TestEnsureNativeIcons_PomodoroDisabledProvisionsWeatherOnly(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Weather.applyDefaults()
	cfg.Weather.Enabled = true
	cfg.Weather.TileNativeIcons = true
	cfg.Pomodoro.Enabled = false
	app := NewApp(cfg, pub, testLogger())
	app.iconFetch = func(_ context.Context, id string) ([]byte, string, error) {
		return []byte("gif-bytes-" + id), "gif", nil
	}

	app.ensureNativeIcons(context.Background())

	got := pub.PutIconNamesSnapshot()
	if slices.Contains(got, "29802.gif") || slices.Contains(got, "6396.gif") {
		t.Fatalf("uploaded pomodoro icons %v despite pomodoro disabled", got)
	}
	if len(got) != 6 {
		t.Fatalf("uploaded %d weather icons, want all 6 defaults", len(got))
	}
}

// tinyPNG encodes an 8x8 opaque PNG, mirroring the real gallery's
// extensionless-URL fallback response for icons like 29802 that have no
// .gif/.jpg form.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

// TestFetchIconFrom_PNGFallbackConvertsToGIF exercises the real wire scenario
// for icon 29802 (tomato): the gallery has neither a .gif nor a .jpg, but
// serves an 8x8 PNG at the extensionless URL. awtrix-ng's upload endpoint
// rejects PNG (415, magic-byte check), so the fallback must convert it to a
// GIF before it's usable.
func TestFetchIconFrom_PNGFallbackConvertsToGIF(t *testing.T) {
	pngBytes := tinyPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/29802.gif", "/29802.jpg":
			http.NotFound(w, r)
		case "/29802":
			w.Write(pngBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	data, ext, err := fetchIconFrom(context.Background(), srv.URL+"/", "29802")
	if err != nil {
		t.Fatalf("fetchIconFrom: %v", err)
	}
	if ext != "gif" {
		t.Fatalf("ext = %q, want gif", ext)
	}
	if !bytes.HasPrefix(data, []byte("GIF8")) {
		t.Fatalf("uploaded bytes lack GIF magic: %x", data[:min(len(data), 8)])
	}
}

// TestFetchIconFrom_AllFormsMissingFails covers the case where the gallery
// has no .gif, .jpg, or extensionless form at all (a genuinely absent icon).
func TestFetchIconFrom_AllFormsMissingFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, _, err := fetchIconFrom(context.Background(), srv.URL+"/", "99999999"); err == nil {
		t.Fatal("expected an error when no form of the icon exists")
	}
}

// TestFetchIconFrom_ExtensionlessNonPNGFails guards pngToGIF against
// converting something that isn't a small PNG (e.g. an HTML error page
// mislabeled with a 200).
func TestFetchIconFrom_ExtensionlessNonPNGFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.gif", "/1.jpg":
			http.NotFound(w, r)
		case "/1":
			w.Write([]byte("<html>not an icon</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, _, err := fetchIconFrom(context.Background(), srv.URL+"/", "1"); err == nil {
		t.Fatal("expected an error for a non-PNG extensionless response")
	}
}
