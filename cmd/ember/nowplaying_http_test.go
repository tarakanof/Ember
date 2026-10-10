package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

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

func npServer(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.RateLimit.Disabled = true
	return newTestServer(t, cfg)
}

func npDo(t *testing.T, srv *httptest.Server, method, path, token string, body []byte, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

const musicReport = `{"source":"music","player":"M4","state":"playing","title":"Song","artist":"Band","album":"Record","track_id":"T1","duration_ms":200000,"position_ms":1000,"future_field":1}`

func TestNowPlayingReportNeedsOwnerToken(t *testing.T) {
	_, srv := npServer(t)
	if resp, _ := npDo(t, srv, "POST", "/v1/nowplaying", "", []byte(musicReport), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	resp, b := npDo(t, srv, "POST", "/v1/nowplaying", testToken, []byte(musicReport), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"has_album_art":false`) {
		t.Fatalf("report: %d %s", resp.StatusCode, b)
	}
}

func TestNowPlayingReportRejectsInvalid(t *testing.T) {
	_, srv := npServer(t)
	resp, _ := npDo(t, srv, "POST", "/v1/nowplaying", testToken, []byte(`{"source":"music","state":"loud"}`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestNowPlayingStateIsPublicAndReflectsReport(t *testing.T) {
	_, srv := npServer(t)
	resp, b := npDo(t, srv, "GET", "/v1/nowplaying/state", "", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"state":"none"`) {
		t.Fatalf("empty state: %d %s", resp.StatusCode, b)
	}
	if sec, err := strconv.ParseInt(resp.Header.Get("X-Ember-Now"), 10, 64); err != nil || time.Now().Unix()-sec > 2 {
		t.Fatalf("X-Ember-Now = %q", resp.Header.Get("X-Ember-Now"))
	}
	npDo(t, srv, "POST", "/v1/nowplaying", testToken, []byte(musicReport), nil)
	_, b = npDo(t, srv, "GET", "/v1/nowplaying/state", "", nil, nil)
	var s nowPlayingState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.State != "playing" || *s.Title != "Song" || s.ArtVersion != nil || s.HasAlbumArt {
		t.Fatalf("state = %s", b)
	}
	if strings.Contains(string(b), "M4") || strings.Contains(string(b), "player") {
		t.Fatalf("public state leaks the player name: %s", b)
	}
}

func TestNowPlayingStateGolden(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	assertGolden(t, "nowplaying_state_none", app.nowPlayingState(goldenNow))
	rep := nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing, Title: "Teardrop",
		Artist: "Massive Attack", Album: "Mezzanine", TrackID: "4242", DurationMS: 330_000, PositionMS: 61_000, Volume: new(40)}
	if _, err := app.nowPlaying.reg.Report(rep, goldenNow); err != nil {
		t.Fatal(err)
	}
	if err := app.nowPlaying.reg.SetArt("plex", "Plexamp", "4242", nowplaying.Album, nowplaying.NewImage([]byte("album"))); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "nowplaying_state", app.nowPlayingState(goldenNow.Add(2*time.Second)))
}

func TestNowPlayingArtPutAndGet(t *testing.T) {
	_, srv := npServer(t)
	art := pngBytes(t, 300, color.RGBA{10, 200, 30, 255})
	put := func(q string, body []byte) int {
		resp, _ := npDo(t, srv, "PUT", "/v1/nowplaying/art?"+q, testToken, body, map[string]string{"Content-Type": "image/png"})
		return resp.StatusCode
	}
	if got := put("source=music&player=M4&kind=album&track_id=T1", art); got != http.StatusNotFound {
		t.Fatalf("no entry: %d, want 404", got)
	}
	npDo(t, srv, "POST", "/v1/nowplaying", testToken, []byte(musicReport), nil)
	if got := put("source=music&player=M4&kind=album&track_id=OLD", art); got != http.StatusConflict {
		t.Fatalf("stale track: %d, want 409", got)
	}
	if got := put("source=music&player=M4&kind=album&track_id=T1", []byte("not an image")); got != http.StatusBadRequest {
		t.Fatalf("garbage: %d, want 400", got)
	}
	if got := put("source=music&player=M4&kind=album&track_id=T1", make([]byte, nowplaying.MaxArtBytes+1)); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big: %d, want 413", got)
	}
	if got := put("source=music&player=M4&kind=album", art); got != http.StatusBadRequest {
		t.Fatalf("missing track_id: %d, want 400", got)
	}
	if got := put("source=music&player=M4&kind=album&track_id=T1", art[:len(art)/2]); got != http.StatusBadRequest {
		t.Fatalf("truncated PNG: %d, want 400", got)
	}
	if got := put("source=music&player=M4&kind=backdrop&track_id=T1", art); got != http.StatusBadRequest {
		t.Fatalf("backdrop upload: %d, want 400", got)
	}
	if resp, _ := npDo(t, srv, "PUT", "/v1/nowplaying/art?source=music&player=M4&kind=album", "", art, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated PUT: %d", resp.StatusCode)
	}
	if got := put("source=music&player=M4&kind=album&track_id=T1", art); got != http.StatusNoContent {
		t.Fatalf("put: %d, want 204", got)
	}

	resp, b := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album", "", nil, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("get: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 240 {
		t.Fatalf("album art: %v %v", err, img.Bounds())
	}
	etag := resp.Header.Get("ETag")
	if resp, _ := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album", "", nil, map[string]string{"If-None-Match": etag}); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d, want 304", resp.StatusCode)
	}
	if resp, _ := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=artist", "", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("artist without picture: %d, want 404", resp.StatusCode)
	}
	resp, b = npDo(t, srv, "GET", "/v1/nowplaying/art?kind=backdrop", "", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("backdrop from album: %d", resp.StatusCode)
	}
	if img, _ := jpeg.Decode(bytes.NewReader(b)); img.Bounds().Dx() != 466 {
		t.Fatalf("backdrop size %v", img.Bounds())
	}
	if resp, _ := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album&size=120", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("size 120: %d, want 200", resp.StatusCode)
	}
	for _, q := range []string{"kind=album&size=1000", "kind=album&size=239", "kind=backdrop&size=240"} {
		if resp, _ := npDo(t, srv, "GET", "/v1/nowplaying/art?"+q, "", nil, nil); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", q, resp.StatusCode)
		}
	}

	_, sb := npDo(t, srv, "GET", "/v1/nowplaying/state", "", nil, nil)
	var s nowPlayingState
	_ = json.Unmarshal(sb, &s)
	resp, _ = npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album&v="+*s.ArtVersion, "", nil, nil)
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned URL Cache-Control = %q", cc)
	}
	if cc := func() string {
		r, _ := npDo(t, srv, "GET", "/v1/nowplaying/art?kind=album&v=stale", "", nil, nil)
		return r.Header.Get("Cache-Control")
	}(); cc != "no-cache" {
		t.Fatalf("stale v Cache-Control = %q", cc)
	}
}

func TestNowPlayingRenderIsCached(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	img := nowplaying.NewImage(pngBytes(t, 64, color.White))
	a, err := app.nowPlaying.render(img, nowplaying.Album, 32)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := app.nowPlaying.render(img, nowplaying.Album, 32)
	if &a[0] != &b[0] {
		t.Fatal("second render did not come from the cache")
	}
	if n, _ := app.nowPlaying.cache.Len(); n != 1 {
		t.Fatalf("cache entries = %d", n)
	}
}

func TestKnobViewNowPlayingOnlyWithPage(t *testing.T) {
	f := newViewFixture(t)
	now := f.clk.Now()
	rep := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Title: "Song",
		Artist: "Band", Album: "Record", TrackID: "T1", DurationMS: 200_000, PositionMS: 1000}
	if _, err := f.app.nowPlaying.reg.Report(rep, now); err != nil {
		t.Fatal(err)
	}
	body, etag, err := f.app.knobView(f.m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "nowplaying") {
		t.Fatalf("view without the page carries nowplaying: %s", body)
	}

	resp, b := devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable page: %d %s", resp.StatusCode, b)
	}
	body, etag2, err := f.app.knobView(f.m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	want := `"nowplaying":{"state":"playing","source":"music","title":"Song","artist":"Band","album":"Record","track_id":"T1","duration_ms":200000,"position_ms":1000,"position_at":` +
		strconv.FormatInt(now.UnixMilli(), 10) + `}`
	if !strings.Contains(string(body), want) || etag2 == etag {
		t.Fatalf("view = %s\nwant block %s", body, want)
	}

	rep.PositionMS = 3000
	later := now.Add(2 * time.Second)
	if _, err := f.app.nowPlaying.reg.Report(rep, later); err != nil {
		t.Fatal(err)
	}
	_, etag3, _ := f.app.knobView(f.m.ID, later)
	if etag3 != etag2 {
		t.Fatal("a consistent position report moved the view's ETag")
	}
	f.app.nowPlaying.reg.Remove("music", "M4")
	body, _, _ = f.app.knobView(f.m.ID, later)
	if !strings.Contains(string(body), `"nowplaying":{"state":"none"}`) {
		t.Fatalf("nothing playing: %s", body)
	}
}

func TestArtistLookupAttachesDeezerPicture(t *testing.T) {
	pic := pngBytes(t, 50, color.RGBA{200, 0, 0, 255})
	var searches, fetched int
	var fetchedPath string
	deezer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/artist":
			searches++
			if r.URL.Query().Get("q") == "nobody" {
				_, _ = io.WriteString(w, `{"data":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[{"name":"Other","picture_xl":"http://`+r.Host+`/images/artist/zzz/1000x1000-0.jpg"},`+
				`{"name":"Band","picture_xl":"http://`+r.Host+`/images/artist/abc/1000x1000-0.jpg"}]}`)
		default:
			fetched++
			fetchedPath = r.URL.Path
			_, _ = w.Write(pic)
		}
	}))
	t.Cleanup(deezer.Close)

	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	np := app.nowPlaying
	np.artists = newArtistLookup(deezer.URL)
	np.artists.pictureOK = func(*url.URL) bool { return true }
	rep := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Title: "Song", Artist: "Band"}
	if err := np.report(rep, time.Now()); err != nil {
		t.Fatal(err)
	}
	j := recvWithin(t, np.jobs, "artist lookup job")
	np.lookupArtist(context.Background(), j, app)
	e, _ := np.reg.Get("music", "M4")
	if e.ArtistArt == nil || fetchedPath != "/images/artist/abc/500x500-0.jpg" {
		t.Fatalf("artist art not attached from the exact-name hit (fetched %q)", fetchedPath)
	}
	if _, err := np.artists.find(context.Background(), "BAND"); err != nil || searches != 1 || fetched != 1 {
		t.Fatalf("second lookup not cached: searches=%d fetched=%d err=%v", searches, fetched, err)
	}
	img, err := np.artists.find(context.Background(), "nobody")
	if err != nil || img != nil {
		t.Fatalf("miss = %v %v", img, err)
	}
	_, _ = np.artists.find(context.Background(), "nobody")
	if searches != 2 {
		t.Fatalf("miss not cached: searches=%d", searches)
	}
}

func TestArtistLookupOffQueuesNothing(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	_ = app.nowPlaying.report(nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Artist: "Band"}, time.Now())
	select {
	case <-app.nowPlaying.jobs:
		t.Fatal("lookup queued with lookups off")
	default:
	}
}

func TestArtistLookupBacksOffAfterError(t *testing.T) {
	var calls int
	deezer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(deezer.Close)
	l := newArtistLookup(deezer.URL)
	if _, err := l.find(context.Background(), "Band"); err == nil {
		t.Fatal("want the error once")
	}
	if img, err := l.find(context.Background(), "Band"); err != nil || img != nil || calls != 1 {
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
	l := newArtistLookup(deezer.URL)
	if img, err := l.find(context.Background(), "Band"); err != nil || img != nil || fetched != 0 {
		t.Fatalf("non-CDN picture fetched: img=%v fetched=%d err=%v", img, fetched, err)
	}
	l = newArtistLookup(deezer.URL)
	l.pictureOK = func(*url.URL) bool { return true }
	if img, _ := l.find(context.Background(), "Ban"); img != nil {
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

func TestKnobViewWithoutPageIsByteIdentical(t *testing.T) {
	f := newViewFixture(t)
	now := f.clk.Now()
	before, etag, err := f.app.knobView(f.m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	rep := nowplaying.Report{Source: "plex", Player: "amp", State: nowplaying.Playing, Title: "x", TrackID: "1", DurationMS: 1000}
	if _, err := f.app.nowPlaying.reg.Report(rep, now); err != nil {
		t.Fatal(err)
	}
	_ = f.app.nowPlaying.reg.SetArt("plex", "amp", "1", nowplaying.Album, nowplaying.NewImage([]byte("a")))
	after, etag2, _ := f.app.knobView(f.m.ID, now)
	if !bytes.Equal(before, after) || etag != etag2 {
		t.Fatalf("view changed without the page:\n%s\n%s", before, after)
	}
}

func TestEnvOptIn(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "off": false, "1": true, "TRUE": true, " yes ": true} {
		if envOptIn(v) != want {
			t.Errorf("envOptIn(%q) = %v", v, !want)
		}
	}
}
