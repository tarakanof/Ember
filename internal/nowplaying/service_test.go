package nowplaying

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func pngBytes(t *testing.T, side int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < side*side; i++ {
		r, g, b, _ := c.RGBA()
		copy(img.Pix[i*4:], []byte{byte(r >> 8), byte(g >> 8), byte(b >> 8), 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestArtistLookupBacksOffAfterError(t *testing.T) {
	var calls int
	deezer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(deezer.Close)
	l := NewArtistLookup(deezer.URL)
	if _, err := l.Find(context.Background(), "Band"); err == nil {
		t.Fatal("want the error once")
	}
	if img, err := l.Find(context.Background(), "Band"); err != nil || img != nil || calls != 1 {
		t.Fatalf("error not cached: calls=%d err=%v", calls, err)
	}
}

func TestArtistLookupNeedsExactNameAndDeezerCDN(t *testing.T) {
	var fetched int
	deezer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/artist" {
			fetched++
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"name":"Bandits","picture_xl":"http://`+r.Host+`/a/1000x1000-0.jpg"},`+
			`{"name":"Band","picture_xl":"http://`+r.Host+`/b/1000x1000-0.jpg"}]}`)
	}))
	t.Cleanup(deezer.Close)
	l := NewArtistLookup(deezer.URL)
	if img, err := l.Find(context.Background(), "Band"); err != nil || img != nil || fetched != 0 {
		t.Fatalf("non-CDN picture fetched: img=%v fetched=%d err=%v", img, fetched, err)
	}
	l = NewArtistLookup(deezer.URL)
	l.PictureOK = func(*url.URL) bool { return true }
	if img, _ := l.Find(context.Background(), "Ban"); img != nil {
		t.Fatal("picked a picture without an exact name match")
	}
	for _, c := range []struct {
		u  string
		ok bool
	}{
		{"https://e-cdns-images.dzcdn.net/images/artist/x/500x500-0.jpg", true},
		{"http://e-cdns-images.dzcdn.net/x.jpg", false},
		{"https://dzcdn.net.evil.example/x.jpg", false},
		{"https://192.168.0.1/x.jpg", false},
	} {
		u, _ := url.Parse(c.u)
		if deezerCDN(u) != c.ok {
			t.Errorf("deezerCDN(%s) = %v", c.u, !c.ok)
		}
	}
}
