package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

var knobCapsCover = map[string]any{
	"view":     []int{1, 1},
	"pages":    []string{"bot", "nowplaying"},
	"features": []string{"np_cover", "view_wait"},
	"limits":   map[string]any{"view_bytes": 16383, "config_bytes": 1024},
}

func coverViewFixture(t *testing.T, live bool) *viewFixture {
	t.Helper()
	f := goldenViewFixture(t)
	if live {
		f.clk.now = time.Now()
	}
	now := f.clk.Now()
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`)
	rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: "Example Song",
		Artist: "Example Artist", Album: "Example Album", TrackID: "4242", DurationMS: 330_000, PositionMS: 61_000, Volume: new(40)}
	if _, err := f.app.nowPlaying.reg.Report(rep, now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	album, err := nowplaying.Prepare(pngBytes(t, 64, color.RGBA{0x40, 0x10, 0x90, 255}))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.nowPlaying.reg.SetArt("plex", "Plexamp", "4242", nowplaying.Album, album); err != nil {
		t.Fatal(err)
	}
	return f
}

func viewAccent(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	var v struct {
		NowPlaying map[string]any `json:"nowplaying"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	a, ok := v.NowPlaying["accent"].(string)
	return a, ok
}

func TestKnobViewNPCoverGolden(t *testing.T) {
	f := coverViewFixture(t, false)
	e, _ := f.app.nowPlaying.reg.Current(f.clk.Now())
	stable := nowplaying.NewImage([]byte("album"))
	stable.Accent = e.AlbumArt.Accent
	if err := f.app.nowPlaying.reg.SetArt("plex", "Plexamp", "4242", nowplaying.Album, stable); err != nil {
		t.Fatal(err)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsCover)
	assertDeviceGolden(t, "view_np_cover", goldenView(t, f))
}

func TestKnobViewSendsAccentOnlyWithNPCover(t *testing.T) {
	f := coverViewFixture(t, false)
	legacy := goldenView(t, f)
	if _, ok := viewAccent(t, legacy); ok {
		t.Fatalf("legacy knob got accent: %s", legacy)
	}
	postCapsCheckin(t, f, "0.10.0", map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "nowplaying"}, "features": []string{"view_wait"}})
	plain := goldenView(t, f)
	if _, ok := viewAccent(t, plain); ok {
		t.Fatalf("caps knob without np_cover got accent: %s", plain)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsCover)
	body := goldenView(t, f)
	a, ok := viewAccent(t, body)
	if !ok || len(a) != 7 || a[0] != '#' {
		t.Fatalf("np_cover knob accent = %q: %s", a, body)
	}
	e, _ := f.app.nowPlaying.reg.Current(f.clk.Now())
	tail := `,"accent":"` + a + `","cover_version":"` + e.CoverVersion() + `"}`
	if string(viewKeys(t, body)["nowplaying"]) != strings.Replace(string(viewKeys(t, plain)["nowplaying"]), `}`, tail, 1) {
		t.Errorf("accent and cover_version must be the only change to nowplaying:\n%s\n%s", viewKeys(t, plain)["nowplaying"], viewKeys(t, body)["nowplaying"])
	}
}

func TestKnobViewLeavesOutAccentWithoutAPicture(t *testing.T) {
	f := goldenViewFixture(t)
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`)
	rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: "Song", TrackID: "1"}
	if _, err := f.app.nowPlaying.reg.Report(rep, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsCover)
	if a, ok := viewAccent(t, goldenView(t, f)); ok {
		t.Fatalf("accent %q without a picture", a)
	}
}

func TestNowPlayingCoverRoute(t *testing.T) {
	f := coverViewFixture(t, true)
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover&size=240", "", nil, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cover at 240: %d, want 400", resp.StatusCode)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsCover)
	var view struct {
		NowPlaying struct {
			ArtVersion   string `json:"art_version"`
			CoverVersion string `json:"cover_version"`
		} `json:"nowplaying"`
	}
	if err := json.Unmarshal(goldenView(t, f), &view); err != nil {
		t.Fatal(err)
	}
	cv := view.NowPlaying.CoverVersion
	if want := view.NowPlaying.ArtVersion + "-" + nowplaying.CoverRecipe; cv != want {
		t.Fatalf("cover_version = %q, want %q: a recipe bump must change the cover URL", cv, want)
	}
	e, _ := f.app.nowPlaying.reg.Current(f.clk.Now())
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover&size=466&v="+e.ArtVersion(), "", nil, nil); resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("cover at the bare art_version: Cache-Control %q, want no-cache so a recipe bump is refetched", resp.Header.Get("Cache-Control"))
	}
	resp, b := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover&size=466&v="+cv, "", nil, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 466 || img.Bounds().Dy() != 466 {
		t.Fatalf("cover: %v %v", err, img.Bounds())
	}
	etag := resp.Header.Get("ETag")
	if want := `"` + e.AlbumArt.Hash + `-cover-466-` + nowplaying.CoverRecipe + `"`; etag != want {
		t.Fatalf("ETag = %s, want %s (the recipe version invalidates tuned covers)", etag, want)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned cover Cache-Control = %q", cc)
	}
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover", "", nil, map[string]string{"If-None-Match": etag}); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d, want 304", resp.StatusCode)
	}
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=album", "", nil, nil); strings.Contains(resp.Header.Get("ETag"), nowplaying.CoverRecipe) {
		t.Fatalf("album ETag %s carries the cover recipe", resp.Header.Get("ETag"))
	}
}

func TestNowPlayingCoverFallsBackToTheArtistPicture(t *testing.T) {
	f := goldenViewFixture(t)
	f.clk.now = time.Now()
	rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: "Song", Artist: "Band", TrackID: "1"}
	if _, err := f.app.nowPlaying.reg.Report(rep, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover", "", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cover without pictures: %d, want 404", resp.StatusCode)
	}
	if !f.app.nowPlaying.reg.SetArtistArt("plex", "Plexamp", "Band", nowplaying.NewImage(pngBytes(t, 32, color.White))) {
		t.Fatal("artist art not set")
	}
	if resp, _ := npDo(t, f.srv, "GET", "/v1/nowplaying/art?kind=cover", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cover from the artist picture: %d, want 200", resp.StatusCode)
	}
}
