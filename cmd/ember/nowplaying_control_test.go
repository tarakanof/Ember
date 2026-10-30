package main

import (
	"context"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/nowplaying"
)

func control(t *testing.T, srv *httptest.Server, token, body, key string) (int, nowplaying.ControlResult, string) {
	t.Helper()
	var h map[string]string
	if key != "" {
		h = map[string]string{"Idempotency-Key": key}
	}
	resp, b := npDo(t, srv, "POST", "/v1/nowplaying/control", token, []byte(body), h)
	var res nowplaying.ControlResult
	_ = json.Unmarshal(b, &res)
	return resp.StatusCode, res, string(b)
}

func pollCommands(t *testing.T, srv *httptest.Server, player string, wait int) []nowplaying.QueuedCommand {
	t.Helper()
	resp, b := npDo(t, srv, "GET", "/v1/nowplaying/commands?player="+player+"&wait="+strconv.Itoa(wait), testToken, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commands: %d %s", resp.StatusCode, b)
	}
	var a commandsAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return a.Commands
}

func TestControlAuthAndValidation(t *testing.T) {
	_, srv := npServer(t)
	if code, _, _ := control(t, srv, "", `{"action":"next"}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _, _ := control(t, srv, "wrong", `{"action":"next"}`, ""); code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", code)
	}
	for _, body := range []string{`{"action":"stop"}`, `{"action":"volume"}`, `{"action":"volume","delta":101}`,
		`{"action":"next","delta":3}`, `{"action":"next","extra":1}`, `not json`} {
		if code, _, b := control(t, srv, testToken, body, ""); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, code, b)
		}
	}
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, ""); code != http.StatusConflict {
		t.Fatalf("nothing playing: %d, want 409", code)
	}
	resp, _ := npDo(t, srv, "GET", "/v1/nowplaying/commands?player=M4", "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("commands without token: %d", resp.StatusCode)
	}
}

func TestControlAcceptsKnobDeviceToken(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	knob := mintKnob(t, srv, http.StatusCreated)
	if _, err := app.nowPlaying.Reg.Report(nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing,
		Title: "Song", TrackID: "T1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = app.nowPlaying.Commands.Take(context.Background(), "M4", 2*time.Second, nil) }()
	waitPolling(t, app.nowPlaying.Commands, "M4")
	if code, _, b := control(t, srv, knob.Token, `{"action":"play_pause"}`, "k1"); code != http.StatusForbidden {
		t.Fatalf("page off: %d %s", code, b)
	}
	if resp, b := devReq(t, srv, "PUT", "/v1/devices/"+knob.ID+"/config", testToken,
		`{"pages":[{"id":"bot","on":true},{"id":"nowplaying","on":true}]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("enable page: %d %s", resp.StatusCode, b)
	}
	if code, res, b := control(t, srv, knob.Token, `{"action":"play_pause"}`, "k1"); code != http.StatusAccepted ||
		res.Status != "queued" || res.Source != "music" {
		t.Fatalf("device token: %d %s", code, b)
	}
}

func waitPolling(t *testing.T, q *nowplaying.CommandQueue, player string) {
	t.Helper()
	for range 200 {
		n := q.Pollers(player)
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("poll never started")
}

func playMusic(t *testing.T, app *App, player string) {
	t.Helper()
	rep := nowplaying.Report{Source: "music", Player: player, State: nowplaying.Playing, Title: "Song", TrackID: "T1",
		DurationMS: 200_000}
	if _, err := app.nowPlaying.Reg.Report(rep, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestControlMusicNeedsAListeningMac(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	if code, _, b := control(t, srv, testToken, `{"action":"next"}`, "a"); code != http.StatusServiceUnavailable {
		t.Fatalf("no Mac polling: %d %s", code, b)
	}
	pollCommands(t, srv, "M4", 0)
	if code, _, b := control(t, srv, testToken, `{"action":"next"}`, "a"); code != http.StatusAccepted {
		t.Fatalf("after a poll: %d %s", code, b)
	}
	if got := pollCommands(t, srv, "M4", 0); len(got) != 1 || got[0].Action != nowplaying.ActNext {
		t.Fatalf("commands = %+v", got)
	}
	if got := pollCommands(t, srv, "M4", 0); len(got) != 0 {
		t.Fatalf("delivered twice: %+v", got)
	}
	control(t, srv, testToken, `{"action":"previous"}`, "")
	if got := pollCommands(t, srv, "Other", 0); len(got) != 0 {
		t.Fatalf("other player got %+v", got)
	}
}

func TestControlIdempotencyKeyStopsDoubleSkip(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	pollCommands(t, srv, "M4", 0)
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, "press-7"); code != http.StatusAccepted {
		t.Fatalf("first: %d", code)
	}
	code, res, _ := control(t, srv, testToken, `{"action":"next"}`, "press-7")
	if code != http.StatusOK || res.Status != "duplicate" {
		t.Fatalf("retry: %d %+v", code, res)
	}
	control(t, srv, testToken, `{"action":"next"}`, "press-8")
	if got := pollCommands(t, srv, "M4", 0); len(got) != 2 {
		t.Fatalf("want 2 commands (press-7 once, press-8), got %+v", got)
	}
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, strings.Repeat("k", 129)); code != http.StatusBadRequest {
		t.Fatalf("long key: %d", code)
	}
}

func TestCommandsLongPollWakesOnPush(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	var got []nowplaying.QueuedCommand
	var wg sync.WaitGroup
	start := time.Now()
	wg.Go(func() { got = pollCommands(t, srv, "M4", 5) })
	waitPolling(t, app.nowPlaying.Commands, "M4")
	if code, _, b := control(t, srv, testToken, `{"action":"volume","delta":-4}`, ""); code != http.StatusAccepted {
		t.Fatalf("control: %d %s", code, b)
	}
	wg.Wait()
	if len(got) != 1 || got[0].Action != nowplaying.ActVolume || got[0].Delta != -4 || got[0].ID == "" {
		t.Fatalf("got %+v", got)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("long-poll did not wake on the command")
	}
}

func TestControlIsRateLimitedPerCaller(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	pollCommands(t, srv, "M4", 0)
	limited := 0
	for i := range nowplaying.ControlBurst + 5 {
		code, _, _ := control(t, srv, testToken, `{"action":"volume","delta":1}`, "v"+strconv.Itoa(i))
		if code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("no step was limited")
	}
	app.nowPlaying.ControlLimit = &nowplaying.CallerLimiter{Burst: nowplaying.ControlBurst, PerSec: nowplaying.ControlPerSec}
	if code, res, _ := control(t, srv, testToken, `{"action":"next"}`, "v"+strconv.Itoa(nowplaying.ControlBurst+4)); code != http.StatusAccepted ||
		res.Status != "queued" {
		t.Fatalf("retry of a limited key: %d %+v", code, res)
	}
}

func plexControlApp(t *testing.T) (*App, *httptest.Server, *fakePlex) {
	t.Helper()
	fake, psrv := newFakePlex(t, pngBytes(t, 32, color.White))
	fake.set(readFixture(t, "plex_sessions.json"))
	app, srv := npServer(t)
	app.nowPlaying.Plex = nowplaying.NewPlexSource(nowplaying.PlexConfig{URL: psrv.URL, Token: fakePlexToken, User: "dt"})
	if !app.nowPlaying.Plex.Poll(context.Background(), app.nowPlaying, testLogger(), time.Now()) {
		t.Fatal("fixture should play")
	}
	return app, srv, fake
}

func TestControlPlexSendsPlayerCommands(t *testing.T) {
	app, srv, fake := plexControlApp(t)
	for _, c := range []struct{ body, want string }{
		{`{"action":"play_pause"}`, "/player/playback/pause amp-1 type=music"},
		{`{"action":"next"}`, "/player/playback/skipNext amp-1 type=music"},
		{`{"action":"previous"}`, "/player/playback/skipPrevious amp-1 type=music"},
	} {
		code, res, b := control(t, srv, testToken, c.body, "")
		if code != http.StatusOK || res.Status != "done" || res.Source != "plex" {
			t.Fatalf("%s: %d %s", c.body, code, b)
		}
		fake.mu.Lock()
		last := fake.playerHits[len(fake.playerHits)-1]
		fake.mu.Unlock()
		if last != c.want {
			t.Fatalf("%s sent %q, want %q", c.body, last, c.want)
		}
	}
	if fake.badToken {
		t.Fatal("token not sent as the header")
	}
	_ = app
}

func TestControlPlexVolumeReadsTimelineThenSteps(t *testing.T) {
	app, srv, fake := plexControlApp(t)
	fake.mu.Lock()
	fake.timelineVol = "98"
	fake.mu.Unlock()
	code, res, b := control(t, srv, testToken, `{"action":"volume","delta":5}`, "")
	if code != http.StatusOK || res.Volume == nil || *res.Volume != 100 {
		t.Fatalf("volume: %d %s", code, b)
	}
	control(t, srv, testToken, `{"action":"volume","delta":-10}`, "")
	fake.mu.Lock()
	hits := strings.Join(fake.playerHits, "\n")
	fake.mu.Unlock()
	want := "/player/timeline/poll amp-1\n" +
		"/player/timeline/poll amp-1\n" +
		"/player/playback/setParameters amp-1 type=music volume=100\n" +
		"/player/playback/setParameters amp-1 type=music volume=90"
	if hits != want {
		t.Fatalf("hits:\n%s\nwant:\n%s", hits, want)
	}
	app.nowPlaying.Plex.Poll(context.Background(), app.nowPlaying, testLogger(), time.Now())
	if e, _ := app.nowPlaying.Reg.Current(time.Now()); e.Volume == nil || *e.Volume != 90 {
		t.Fatalf("entry volume = %v", e.Volume)
	}
}

func TestControlPlexFailures(t *testing.T) {
	_, srv, fake := plexControlApp(t)
	fake.mu.Lock()
	fake.playerCode = http.StatusNotFound
	fake.mu.Unlock()
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, "p1"); code != http.StatusBadGateway {
		t.Fatalf("player refused: %d, want 502", code)
	}
	if code, res, _ := control(t, srv, testToken, `{"action":"next"}`, "p1"); code != http.StatusOK || res.Status != "duplicate" {
		t.Fatalf("retry after 502: %d %+v", code, res)
	}
	fake.mu.Lock()
	fake.playerCode, fake.timelineVol = 0, ""
	fake.mu.Unlock()
	if code, _, _ := control(t, srv, testToken, `{"action":"volume","delta":2}`, ""); code != http.StatusBadGateway {
		t.Fatalf("volume with no known level: %d, want 502 (never a guessed level)", code)
	}
}

func TestControlPlexWithoutPlexConfigIs503(t *testing.T) {
	app, srv := npServer(t)
	if _, err := app.nowPlaying.Reg.Report(nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing,
		Title: "x", TrackID: "1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("plex unconfigured: %d", code)
	}
}

func TestKnobViewCarriesVolume(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if np := app.knobNowPlaying(time.Now(), false); np.Volume != nil {
		t.Fatal("volume with nothing playing")
	}
	rep := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Title: "x", TrackID: "1", Volume: new(35)}
	if _, err := app.nowPlaying.Reg.Report(rep, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(app.knobNowPlaying(time.Now(), false))
	if !strings.Contains(string(b), `"volume":35`) {
		t.Fatalf("block = %s", b)
	}
}

func TestControlPlexVolumeRereadsAStaleLevel(t *testing.T) {
	app, srv, fake := plexControlApp(t)
	now := time.Now()
	app.nowPlaying.Plex.Ctl.Now = func() time.Time { return now }
	fake.mu.Lock()
	fake.timelineVol = "80"
	fake.mu.Unlock()
	control(t, srv, testToken, `{"action":"volume","delta":2}`, "")
	fake.mu.Lock()
	fake.timelineVol = "10"
	fake.mu.Unlock()
	now = now.Add(nowplaying.PlexVolumeTTL + time.Second)
	code, res, b := control(t, srv, testToken, `{"action":"volume","delta":2}`, "")
	if code != http.StatusOK || res.Volume == nil || *res.Volume != 12 {
		t.Fatalf("stale level: %d %s, want 12 (never 84)", code, b)
	}
	code, res, _ = control(t, srv, testToken, `{"action":"volume","delta":2}`, "")
	if code != http.StatusOK || *res.Volume != 14 {
		t.Fatalf("continuous turn: %d %v", code, res.Volume)
	}
}

func TestControlPlexPlayPauseFollowsWhatWasCommanded(t *testing.T) {
	_, srv, fake := plexControlApp(t)
	last := func() string {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.playerHits[len(fake.playerHits)-1]
	}
	control(t, srv, testToken, `{"action":"play_pause"}`, "")
	if l := last(); l != "/player/playback/pause amp-1 type=music" {
		t.Fatalf("first toggle: %s", l)
	}
	control(t, srv, testToken, `{"action":"play_pause"}`, "")
	if l := last(); l != "/player/playback/play amp-1 type=music" {
		t.Fatalf("second toggle: %s", l)
	}
	control(t, srv, testToken, `{"action":"play"}`, "")
	if l := last(); l != "/player/playback/play amp-1 type=music" {
		t.Fatalf("explicit play: %s", l)
	}
	control(t, srv, testToken, `{"action":"pause"}`, "")
	if l := last(); l != "/player/playback/pause amp-1 type=music" {
		t.Fatalf("explicit pause: %s", l)
	}
}

func TestControlRefusesWhenTheShownTrackChanged(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	pollCommands(t, srv, "M4", 0)
	for _, body := range []string{`{"action":"next","source":"plex"}`, `{"action":"next","track_id":"T9"}`,
		`{"action":"next","source":"music","player":"Other"}`} {
		if code, _, b := control(t, srv, testToken, body, "same"); code != http.StatusConflict {
			t.Fatalf("%s: %d %s", body, code, b)
		}
	}
	if code, _, b := control(t, srv, testToken, `{"action":"next","source":"music","track_id":"T1"}`, "same"); code != http.StatusAccepted {
		t.Fatalf("matching target: %d %s", code, b)
	}
	if got := pollCommands(t, srv, "M4", 0); len(got) != 1 {
		t.Fatalf("commands = %+v", got)
	}
}

func TestKnobBurstIsNotChargedPerIP(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	cfg := *app.cfg.Load()
	cfg.RateLimit.Disabled = false
	cfg.RateLimit.Burst, cfg.RateLimit.RefillPerSec = 60, 5
	app.cfg.Store(&cfg)
	now := time.Now()
	clock := func() time.Time { return now }
	app.limiter.clock, app.viewLimit.Clock, app.nowPlaying.ControlLimit.Clock = clock, clock, clock
	knob := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "PUT", "/v1/devices/"+knob.ID+"/config", testToken, `{"pages":[{"id":"nowplaying","on":true}]}`)
	playMusic(t, app, "M4")
	go func() { _, _ = app.nowPlaying.Commands.Take(context.Background(), "M4", 25*time.Second, nil) }()
	waitPolling(t, app.nowPlaying.Commands, "M4")
	for i := range 100 {
		now = now.Add(200 * time.Millisecond)
		if code, _, b := control(t, srv, knob.Token, `{"action":"volume","delta":1}`, "s"+strconv.Itoa(i)); code == http.StatusTooManyRequests {
			t.Fatalf("step %d limited: %s", i, b)
		}
		if resp, b := devReq(t, srv, "GET", "/v1/devices/self/view", knob.Token, ""); resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("view %d limited: %s", i, b)
		}
	}
	limited := false
	for range 80 {
		if resp, _ := devReq(t, srv, "GET", "/v1/devices/self/view", "bad-token", ""); resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("failed tokens were never limited")
	}
}

func TestNowPlayingCommandsIsMasterOnly(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	app.updateConfig(func(c *Config) { c.RateLimit.Disabled = true })
	knob := mintKnob(t, srv, http.StatusCreated)
	admin := mintClient(t, srv, "admin", scopeAdmin)
	playMusic(t, app, "M4")
	pollCommands(t, srv, "M4", 0)
	if code, _, b := control(t, srv, testToken, `{"action":"next"}`, ""); code != http.StatusAccepted {
		t.Fatalf("queue one: %d %s", code, b)
	}
	for _, c := range []struct {
		name, token string
		want        int
	}{{"none", "", 401}, {"wrong", "wrong", 401}, {"knob", knob.Token, 401}, {"admin client", admin.Token, 403}} {
		if resp, b := npDo(t, srv, "GET", "/v1/nowplaying/commands?player=M4&wait=0", c.token, nil, nil); resp.StatusCode != c.want {
			t.Errorf("as %s: %d %s, want %d", c.name, resp.StatusCode, b, c.want)
		}
	}
	if got := pollCommands(t, srv, "M4", 0); len(got) != 1 || got[0].Action != nowplaying.ActNext {
		t.Fatalf("a rejected poll drained the queue: %+v", got)
	}
	if resp, b := npDo(t, srv, "GET", "/v1/nowplaying/commands?player=M5&wait=0", admin.Token, nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin poll for M5: %d %s", resp.StatusCode, b)
	}
	if err := app.nowPlaying.Commands.Push("M5", nowplaying.ControlRequest{Action: nowplaying.ActNext}); err != nowplaying.ErrNoController {
		t.Fatalf("a rejected poll marked M5 as listening: push err = %v", err)
	}
}
