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

func control(t *testing.T, srv *httptest.Server, token, body, key string) (int, controlResult, string) {
	t.Helper()
	var h map[string]string
	if key != "" {
		h = map[string]string{"Idempotency-Key": key}
	}
	resp, b := npDo(t, srv, "POST", "/v1/nowplaying/control", token, []byte(body), h)
	var res controlResult
	_ = json.Unmarshal(b, &res)
	return resp.StatusCode, res, string(b)
}

func pollCommands(t *testing.T, srv *httptest.Server, player string, wait int) []queuedCommand {
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
	if _, err := app.nowPlaying.reg.Report(nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing,
		Title: "Song", TrackID: "T1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = app.nowPlaying.commands.take(context.Background(), "M4", 2*time.Second) }()
	waitPolling(t, app.nowPlaying.commands, "M4")
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

func waitPolling(t *testing.T, q *commandQueue, player string) {
	t.Helper()
	for range 200 {
		q.mu.Lock()
		n := q.polling[player]
		q.mu.Unlock()
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
	if _, err := app.nowPlaying.reg.Report(rep, time.Now()); err != nil {
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
	if got := pollCommands(t, srv, "M4", 0); len(got) != 1 || got[0].Action != actNext {
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

func TestControlKeysAreScopedPerCaller(t *testing.T) {
	var k controlKeys
	now := time.Now()
	if !k.claim("device:a", "k", now) || !k.claim("owner", "k", now) {
		t.Fatal("same key from two callers must both pass")
	}
	if k.claim("owner", "k", now.Add(controlKeyTTL-time.Second)) {
		t.Fatal("duplicate within the TTL passed")
	}
	if !k.claim("owner", "k", now.Add(controlKeyTTL)) {
		t.Fatal("key not forgotten after the TTL")
	}
	k.claim("device:knob", "press-1", now)
	for i := range controlKeysPerCaller * 3 {
		k.claim("owner", "x"+strconv.Itoa(i), now)
	}
	if len(k.callers["owner"]) > controlKeysPerCaller {
		t.Fatalf("keys unbounded: %d", len(k.callers["owner"]))
	}
	if k.claim("device:knob", "press-1", now.Add(time.Minute)) {
		t.Fatal("another caller's burst evicted the knob's key")
	}
	for i := range controlCallersMax * 2 {
		k.claim("c"+strconv.Itoa(i), "k", now)
	}
	if len(k.callers) > controlCallersMax {
		t.Fatalf("callers unbounded: %d", len(k.callers))
	}
}

func TestCommandQueueCoalescesVolumeAndDropsStale(t *testing.T) {
	q := newCommandQueue()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	q.clock = func() time.Time { return now }
	if err := q.push("M4", controlRequest{Action: actNext}); err != errNoController {
		t.Fatalf("no poll yet: %v", err)
	}
	_, _ = q.take(context.Background(), "M4", 0)
	for _, d := range []int{3, 3, -1} {
		if err := q.push("M4", controlRequest{Action: actVolume, Delta: d}); err != nil {
			t.Fatal(err)
		}
	}
	cmds, _ := q.take(context.Background(), "M4", 0)
	if len(cmds) != 1 || cmds[0].Delta != 5 {
		t.Fatalf("coalesced = %+v, want one volume +5", cmds)
	}
	_ = q.push("M4", controlRequest{Action: actPlayPause})
	now = now.Add(commandTTL)
	if cmds, _ := q.take(context.Background(), "M4", 0); len(cmds) != 0 {
		t.Fatalf("stale command delivered: %+v", cmds)
	}
	now = now.Add(commandListenGrace + time.Second)
	if err := q.push("M4", controlRequest{Action: actNext}); err != errNoController {
		t.Fatalf("Mac silent past the grace: %v", err)
	}
}

func TestCommandsLongPollWakesOnPush(t *testing.T) {
	app, srv := npServer(t)
	playMusic(t, app, "M4")
	var got []queuedCommand
	var wg sync.WaitGroup
	start := time.Now()
	wg.Go(func() { got = pollCommands(t, srv, "M4", 5) })
	waitPolling(t, app.nowPlaying.commands, "M4")
	if code, _, b := control(t, srv, testToken, `{"action":"volume","delta":-4}`, ""); code != http.StatusAccepted {
		t.Fatalf("control: %d %s", code, b)
	}
	wg.Wait()
	if len(got) != 1 || got[0].Action != actVolume || got[0].Delta != -4 || got[0].ID == "" {
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
	for i := range controlBurst + 5 {
		code, _, _ := control(t, srv, testToken, `{"action":"volume","delta":1}`, "v"+strconv.Itoa(i))
		if code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("no step was limited")
	}
	app.nowPlaying.controlLimit = &callerLimiter{burst: controlBurst, perSec: controlPerSec}
	if code, res, _ := control(t, srv, testToken, `{"action":"next"}`, "v"+strconv.Itoa(controlBurst+4)); code != http.StatusAccepted ||
		res.Status != "queued" {
		t.Fatalf("retry of a limited key: %d %+v", code, res)
	}
}

func plexControlApp(t *testing.T) (*App, *httptest.Server, *fakePlex) {
	t.Helper()
	fake, psrv := newFakePlex(t, pngBytes(t, 32, color.White))
	fake.set(readFixture(t, "plex_sessions.json"))
	app, srv := npServer(t)
	app.nowPlaying.plex = newPlexSource(plexConfig{URL: psrv.URL, Token: fakePlexToken, User: "dt"})
	if !app.nowPlaying.plex.poll(context.Background(), app.nowPlaying, testLogger(), time.Now()) {
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
	app.nowPlaying.plex.poll(context.Background(), app.nowPlaying, testLogger(), time.Now())
	if e, _ := app.nowPlaying.reg.Current(time.Now()); e.Volume == nil || *e.Volume != 90 {
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
	if _, err := app.nowPlaying.reg.Report(nowplaying.Report{Source: "plex", Player: "Plexamp", State: nowplaying.Playing,
		Title: "x", TrackID: "1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := control(t, srv, testToken, `{"action":"next"}`, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("plex unconfigured: %d", code)
	}
}

func TestKnobViewCarriesVolume(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	if np := app.knobNowPlaying(time.Now()); np.Volume != nil {
		t.Fatal("volume with nothing playing")
	}
	rep := nowplaying.Report{Source: "music", Player: "M4", State: nowplaying.Playing, Title: "x", TrackID: "1", Volume: new(35)}
	if _, err := app.nowPlaying.reg.Report(rep, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(app.knobNowPlaying(time.Now()))
	if !strings.Contains(string(b), `"volume":35`) {
		t.Fatalf("block = %s", b)
	}
}

func TestControlPlexVolumeRereadsAStaleLevel(t *testing.T) {
	app, srv, fake := plexControlApp(t)
	now := time.Now()
	app.nowPlaying.plex.ctl.now = func() time.Time { return now }
	fake.mu.Lock()
	fake.timelineVol = "80"
	fake.mu.Unlock()
	control(t, srv, testToken, `{"action":"volume","delta":2}`, "")
	fake.mu.Lock()
	fake.timelineVol = "10"
	fake.mu.Unlock()
	now = now.Add(plexVolumeTTL + time.Second)
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

func TestCommandsCarryTheirAge(t *testing.T) {
	q := newCommandQueue()
	now := time.Now()
	q.clock = func() time.Time { return now }
	_, _ = q.take(context.Background(), "M4", 0)
	_ = q.push("M4", controlRequest{Action: actNext})
	now = now.Add(1200 * time.Millisecond)
	cmds, _ := q.take(context.Background(), "M4", 0)
	if len(cmds) != 1 || cmds[0].AgeMS != 1200 {
		t.Fatalf("cmds = %+v", cmds)
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
	app.limiter.clock, app.viewLimit.clock, app.nowPlaying.controlLimit.clock = clock, clock, clock
	knob := mintKnob(t, srv, http.StatusCreated)
	devReq(t, srv, "PUT", "/v1/devices/"+knob.ID+"/config", testToken, `{"pages":[{"id":"nowplaying","on":true}]}`)
	playMusic(t, app, "M4")
	go func() { _, _ = app.nowPlaying.commands.take(context.Background(), "M4", 25*time.Second) }()
	waitPolling(t, app.nowPlaying.commands, "M4")
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
