package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

// --- broadcaster ---

func TestChangeBroadcasterWakesAllWaitersAndReportsTopics(t *testing.T) {
	b := newChangeBroadcaster()
	seq, ch1 := b.subscribe()
	_, ch2 := b.subscribe()
	b.notify(topicSessions | topicWeather)
	for i, ch := range []<-chan struct{}{ch1, ch2} {
		select {
		case <-ch:
		default:
			t.Fatalf("waiter %d not woken", i)
		}
	}
	if got := b.since(seq); got != topicSessions|topicWeather {
		t.Fatalf("since = %v, want sessions|weather", got)
	}
	seq2, ch3 := b.subscribe()
	if seq2 == seq {
		t.Fatal("sequence did not move")
	}
	select {
	case <-ch3:
		t.Fatal("a fresh subscription is already closed")
	default:
	}
	b.notify(topicPomodoro)
	if got := b.since(seq2); got != topicPomodoro {
		t.Fatalf("since = %v, want pomodoro", got)
	}
	if got := topicPomodoro | topicDevices; got.String() != "pomodoro|devices" {
		t.Fatalf("String = %q", got.String())
	}
}

// A notify between subscribe and the wait must not be lost: the channel
// taken before the read is the one it closes.
func TestChangeBroadcasterNoLostWakeupBetweenReadAndWait(t *testing.T) {
	b := newChangeBroadcaster()
	_, ch := b.subscribe()
	b.notify(topicSessions) // lands "between the hash check and the wait"
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("wakeup lost")
	}
}

func TestChangeBroadcasterCloseEndsWaitsAndIsIdempotent(t *testing.T) {
	b := newChangeBroadcaster()
	b.close()
	b.close()
	b.notify(topicSessions) // no panic after close
	select {
	case <-b.stopped():
	default:
		t.Fatal("stopped not closed")
	}
	var nilB *changeBroadcaster
	nilB.notify(topicSessions) // nil-safe
}

// Every source the knob view reads notifies the broadcaster.
func TestChangeSourcesNotify(t *testing.T) {
	cases := []struct {
		name  string
		topic changeTopic
		setup func(t *testing.T, f *viewFixture)
		act   func(t *testing.T, f *viewFixture)
	}{
		{"status upsert", topicSessions, nil, func(t *testing.T, f *viewFixture) {
			f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "waiting"})
		}},
		{"status delete", topicSessions, nil, func(t *testing.T, f *viewFixture) { f.app.Delete("M4/claude/s1") }},
		{"status clear", topicSessions, nil, func(t *testing.T, f *viewFixture) { f.app.Clear() }},
		{"pomodoro start", topicPomodoro, nil, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "POST", "/v1/pomodoro/start", f.m.Token, "")
		}},
		{"pomodoro phase end", topicPomodoro, nil, func(t *testing.T, f *viewFixture) {
			f.eng.Start(pomodoro.PhaseFocus)
			f.clk.advance(26 * time.Minute)
			f.app.pomoTick()
		}},
		{"device config", topicDevices, nil, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"poll_ms":3000}`)
		}},
		{"device live mode", topicDevices, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"diagnostics":"basic"}`)
		}, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "POST", "/v1/devices/"+f.m.ID+"/stats/live", testToken, `{"seconds":60}`)
		}},
		{"config", topicConfig, nil, func(t *testing.T, f *viewFixture) {
			f.app.updateConfig(func(c *Config) { c.Display.IdleText = "zzz" })
		}},
		{"brightness", topicBrightness, nil, func(t *testing.T, f *viewFixture) {
			clock := lightClock(t, `{"version":"1.1.1","lightLevel":1000}`)
			f.app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
			f.app.tickBrightness(t.Context(), time.Now())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newViewFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			seq, ch := f.app.changes.subscribe()
			tc.act(t, f)
			select {
			case <-ch:
			default:
				t.Fatal("no notify")
			}
			if got := f.app.changes.since(seq); got&tc.topic == 0 {
				t.Fatalf("topics = %v, want %v", got, tc.topic)
			}
		})
	}
}

func TestWeatherFetchNotifies(t *testing.T) {
	aqi := 10.0
	var hits int32
	app := newAirTestApp(t, &recordingPublisher{}, &aqi, &hits)
	seq, _ := app.changes.subscribe()
	app.pollWeather(context.Background(), time.Now())
	if got := app.changes.since(seq); got&topicWeather == 0 {
		t.Fatalf("topics = %v, want weather", got)
	}
}

// --- wait parameter ---

func TestParseViewWait(t *testing.T) {
	for raw, want := range map[string]time.Duration{"": 0, "0": 0, "1": time.Second, "25": 25 * time.Second, "999": knobViewWaitMax} {
		got, err := parseViewWait(raw)
		if err != nil || got != want {
			t.Errorf("parseViewWait(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"-1", "x", "1.5", "1s"} {
		if _, err := parseViewWait(raw); err == nil {
			t.Errorf("parseViewWait(%q) accepted", raw)
		}
	}
}

// --- HTTP ---

func (f *viewFixture) wait(ctx context.Context, t *testing.T, etag, wait string) (*http.Response, []byte, time.Duration, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", f.srv.URL+knobViewPath+"?wait="+wait, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.m.Token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	start := time.Now()
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		return nil, nil, time.Since(start), err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, b, time.Since(start), err
}

// newWaitFixture never rechecks on its own, so a wake can only come from a
// notify (or the wait running out).
func newWaitFixture(t *testing.T) (*viewFixture, string) {
	f := newViewFixture(t)
	f.app.viewRecheck = time.Hour
	resp, _ := f.get(t, "")
	return f, resp.Header.Get("ETag")
}

func TestKnobViewAdvertisesWaitCap(t *testing.T) {
	f := newViewFixture(t)
	resp, _ := f.get(t, "")
	if got := resp.Header.Get(knobViewWaitHeader); got != "25" {
		t.Fatalf("%s = %q, want 25", knobViewWaitHeader, got)
	}
}

func TestKnobViewWaitTimesOutWith304SameETag(t *testing.T) {
	f, etag := newWaitFixture(t)
	resp, body, took, err := f.wait(t.Context(), t, etag, "1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Fatalf("status %d body %q, want empty 304", resp.StatusCode, body)
	}
	if resp.Header.Get("ETag") != etag || resp.Header.Get(knobNowHeader) == "" {
		t.Fatalf("headers %v", resp.Header)
	}
	if took < 900*time.Millisecond {
		t.Fatalf("took %v, want ~1s", took)
	}
	assertLongPollMetric(t, f, "timeout", 1)
}

func TestKnobViewWaitAnswersAtOnceWhenAlreadyChanged(t *testing.T) {
	f, _ := newWaitFixture(t)
	resp, body, took, err := f.wait(t.Context(), t, `"0000000000000000"`, "20")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body) == 0 || took > 10*time.Second {
		t.Fatalf("status %d, %d bytes, took %v", resp.StatusCode, len(body), took)
	}
}

func TestKnobViewWaitWithoutETagAnswersAtOnce(t *testing.T) {
	f, _ := newWaitFixture(t)
	resp, _, took, err := f.wait(t.Context(), t, "", "20")
	if err != nil || resp.StatusCode != http.StatusOK || took > 10*time.Second {
		t.Fatalf("err %v status %v took %v", err, resp, took)
	}
}

func TestKnobViewWaitRejectsBadWait(t *testing.T) {
	f, etag := newWaitFixture(t)
	resp, _, _, err := f.wait(t.Context(), t, etag, "soon")
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("err %v status %v", err, resp)
	}
}

// waitThenAct starts a long-poll, waits until it blocks, runs act, and returns
// the answer and how long after act it came.
func waitThenAct(t *testing.T, f *viewFixture, etag string, act func()) (*http.Response, []byte, time.Duration) {
	t.Helper()
	type res struct {
		resp *http.Response
		body []byte
		at   time.Time
		err  error
	}
	done := make(chan res, 1)
	go func() {
		resp, body, _, err := f.wait(context.Background(), t, etag, "10")
		done <- res{resp, body, time.Now(), err}
	}()
	waitForWaiters(t, f.app, 1)
	acted := time.Now()
	act()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.resp, r.body, r.at.Sub(acted)
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake")
		return nil, nil, 0
	}
}

func waitForWaiters(t *testing.T, a *App, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.viewWaiters.count() != n {
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", a.viewWaiters.count(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestKnobViewWaitWakesOnEachSource(t *testing.T) {
	cases := []struct {
		name string
		want string
		act  func(t *testing.T, f *viewFixture)
	}{
		{"status", `"waiting":1`, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "POST", "/v1/status", testToken, `{"source":"M4","tool":"claude","session":"s1","state":"waiting"}`)
		}},
		{"pomodoro", `"phase":"focus","running":true`, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "POST", "/v1/pomodoro/start", f.m.Token, "")
		}},
		{"device config", `"config_version":2`, func(t *testing.T, f *viewFixture) {
			devReq(t, f.srv, "PUT", "/v1/devices/"+f.m.ID+"/config", testToken, `{"poll_ms":3000}`)
		}},
		{"pomodoro disabled", `"pomo":null`, func(t *testing.T, f *viewFixture) {
			f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, etag := newWaitFixture(t)
			resp, body, after := waitThenAct(t, f, etag, func() { tc.act(t, f) })
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tc.want) {
				t.Fatalf("status %d body %s, want %s", resp.StatusCode, body, tc.want)
			}
			if resp.Header.Get("ETag") == etag {
				t.Fatal("ETag unchanged")
			}
			if after > 5*time.Second { // the wait is 10 s; a 200 at all already proves the wake
				t.Fatalf("woke %v after the change", after)
			}
			waitForWaiters(t, f.app, 0)
		})
	}
}

// State that moves without a notify (here: written straight into the store)
// is still picked up by the periodic recheck.
func TestKnobViewWaitRecheckCatchesSilentChanges(t *testing.T) {
	f := newViewFixture(t)
	f.app.viewRecheck = 20 * time.Millisecond
	f.app.updateConfig(func(c *Config) { c.Weather.Enabled = true; c.Weather.Provider = "open-meteo" })
	resp, _ := f.get(t, "")
	etag := resp.Header.Get("ETag")
	resp, body, _ := waitThenAct(t, f, etag, func() {
		f.app.weather.mu.Lock()
		f.app.weather.obs = weatherObservation{Condition: "rain", FetchedAt: time.Now()}
		f.app.weather.have = true
		f.app.weather.mu.Unlock()
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"cond":"rain"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

// A change that lands after the long-poll's read but before it blocks (the
// hook runs exactly there) must still wake it: a lost one would end in 304
// when the wait runs out.
func TestKnobViewWaitNoLostWakeupBetweenReadAndBlock(t *testing.T) {
	f, _ := newWaitFixture(t)
	for i := range 10 {
		resp, _ := f.get(t, "")
		etag := resp.Header.Get("ETag")
		state := []string{"waiting", "running"}[i%2]
		var once sync.Once
		f.app.viewWaitHook = func() {
			once.Do(func() { f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: state}) })
		}
		got, _, _, err := f.wait(t.Context(), t, etag, "10")
		if err != nil {
			t.Fatal(err)
		}
		if got.StatusCode != http.StatusOK {
			t.Fatalf("iteration %d: status %d, want 200 (lost wakeup)", i, got.StatusCode)
		}
	}
	f.app.viewWaitHook = nil
}

func TestKnobViewWaitersAreBoundedPerDevice(t *testing.T) {
	f, etag := newWaitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range knobViewWaitersPerDevice {
		go func() { _, _, _, _ = f.wait(ctx, t, etag, "10") }()
	}
	waitForWaiters(t, f.app, knobViewWaitersPerDevice)
	resp, _, took, err := f.wait(t.Context(), t, etag, "10")
	if err != nil || resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" || took > 5*time.Second {
		t.Fatalf("err %v resp %v took %v, want an immediate 429 with Retry-After", err, resp, took)
	}
	metricsBody := getMetrics(t, f)
	if !strings.Contains(metricsBody, "ember_knob_view_waiters 2\n") {
		t.Fatalf("metrics lack the waiters gauge at 2:\n%s", grepLines(metricsBody, "knob_view"))
	}
	assertLongPollMetric(t, f, "busy", 1)
	cancel()
	waitForWaiters(t, f.app, 0)
}

// A client that goes away releases its slot and its goroutine.
func TestKnobViewWaitReleasesOnClientDisconnect(t *testing.T) {
	f, etag := newWaitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		_, _, _, err := f.wait(ctx, t, etag, "20")
		errc <- err
	}()
	waitForWaiters(t, f.app, 1)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	waitForWaiters(t, f.app, 0)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(getMetrics(t, f), `ember_knob_view_longpoll_total{result="gone"} 1`) {
		if time.Now().After(deadline) {
			t.Fatal("gone not counted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestKnobViewWaitAnswersAtOnceOnShutdown(t *testing.T) {
	f, etag := newWaitFixture(t)
	resp, _, after := waitThenAct(t, f, etag, f.app.changes.close)
	if resp.StatusCode != http.StatusNotModified || after > 5*time.Second {
		t.Fatalf("status %d after %v, want a prompt 304", resp.StatusCode, after)
	}
}

func TestKnobViewWaitDeviceDeletedWhileWaiting(t *testing.T) {
	f, etag := newWaitFixture(t)
	resp, _, _ := waitThenAct(t, f, etag, func() {
		devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestViewWaitersReleaseIsIdempotent(t *testing.T) {
	var v viewWaiters
	rel, ok := v.acquire("a")
	if !ok {
		t.Fatal("first acquire refused")
	}
	rel()
	rel()
	if v.count() != 0 || len(v.perDevice) != 0 {
		t.Fatalf("count %d map %v", v.count(), v.perDevice)
	}
	var held []func()
	for i := range knobViewWaitersTotal {
		r, ok := v.acquire(string(rune('a'+i%26)) + string(rune('a'+i/26)))
		if !ok {
			t.Fatalf("acquire %d refused", i)
		}
		held = append(held, r)
	}
	if _, ok := v.acquire("zz"); ok {
		t.Fatal("total cap not enforced")
	}
	for _, r := range held {
		r()
	}
}

func getMetrics(t *testing.T, f *viewFixture) string {
	t.Helper()
	resp, err := f.srv.Client().Get(f.srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func assertLongPollMetric(t *testing.T, f *viewFixture, result string, n int) {
	t.Helper()
	want := `ember_knob_view_longpoll_total{result="` + result + `"} ` + strconv.Itoa(n)
	if body := getMetrics(t, f); !strings.Contains(body, want+"\n") {
		t.Fatalf("metrics lack %s:\n%s", want, grepLines(body, "knob_view"))
	}
}

func grepLines(s, sub string) string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.Contains(line, sub) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// The server's Read/WriteTimeout (30 s in main) count from the request; a
// wait must extend both, or the answer is lost and the context cancelled.
func TestKnobViewWaitOutlivesServerTimeouts(t *testing.T) {
	f, etag := newWaitFixture(t)
	srv := httptest.NewUnstartedServer(f.app.routes())
	srv.Config.ReadTimeout = 300 * time.Millisecond
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	f.srv = srv
	resp, _, took, err := f.wait(t.Context(), t, etag, "1")
	if err != nil {
		t.Fatalf("err %v after %v", err, took)
	}
	if resp.StatusCode != http.StatusNotModified || took < 900*time.Millisecond {
		t.Fatalf("status %d after %v, want 304 after ~1s", resp.StatusCode, took)
	}
}

func TestAdminReloadNotifies(t *testing.T) {
	app, path := newAppForReload(t, `{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"old"}}`)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	if err := os.WriteFile(path, []byte(`{"awtrix":{"http_base_url":"http://1.2.3.4"},"display":{"idle_text":"new"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, _ := app.changes.subscribe()
	resp, _ := devReq(t, srv, "POST", "/admin/reload", "tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload status %d", resp.StatusCode)
	}
	if got := app.changes.since(seq); got&topicConfig == 0 || got&topicPomodoro == 0 {
		t.Fatalf("topics = %v, want config|pomodoro", got)
	}
}

func TestStatusHeartbeatDoesNotNotify(t *testing.T) {
	f := newViewFixture(t)
	req := StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running"}
	f.app.Upsert(req)
	seq, _ := f.app.changes.subscribe()
	f.app.Upsert(req)
	if got := f.app.changes.since(seq); got != 0 {
		t.Fatalf("a repeated upsert notified %v", got)
	}
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "waiting"})
	if got := f.app.changes.since(seq); got&topicSessions == 0 {
		t.Fatal("a state change did not notify")
	}
}

func TestBrightnessUnchangedOutputDoesNotNotify(t *testing.T) {
	f := newViewFixture(t)
	clock := lightClock(t, `{"version":"1.1.1","lightLevel":1000}`)
	f.app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	f.app.tickBrightness(t.Context(), time.Now())
	seq, _ := f.app.changes.subscribe()
	f.app.tickBrightness(t.Context(), time.Now())
	if got := f.app.changes.since(seq); got != 0 {
		t.Fatalf("an unchanged brightness notified %v", got)
	}
}

// At the waiter cap a stale tag still gets its 200 at once, not 429.
func TestKnobViewWaitStaleTagAtCapAnswers200(t *testing.T) {
	f, etag := newWaitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range knobViewWaitersPerDevice {
		go func() { _, _, _, _ = f.wait(ctx, t, etag, "10") }()
	}
	waitForWaiters(t, f.app, knobViewWaitersPerDevice)
	resp, body, _, err := f.wait(t.Context(), t, `"0000000000000000"`, "10")
	if err != nil || resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("err %v resp %v", err, resp)
	}
	cancel()
	waitForWaiters(t, f.app, 0)
}

// If-None-Match: * matches any view; with wait it answers 304 at once.
func TestKnobViewWaitStarDoesNotWait(t *testing.T) {
	f, _ := newWaitFixture(t)
	resp, _, took, err := f.wait(t.Context(), t, "*", "20")
	if err != nil || resp.StatusCode != http.StatusNotModified || took > 10*time.Second {
		t.Fatalf("err %v resp %v took %v", err, resp, took)
	}
	if f.app.viewWaiters.count() != 0 {
		t.Fatal("a waiter slot was taken")
	}
}
