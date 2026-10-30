package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

const (
	statePath     = "/state"
	pomoStatePath = "/v1/pomodoro/state"
)

func newStateFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := newViewFixture(t)
	f.app.viewRecheck = time.Hour
	f.app.viewClock = f.clk.Now
	return f
}

func (f *viewFixture) feed(ctx context.Context, t *testing.T, path, etag, wait string) (*http.Response, []byte, error) {
	t.Helper()
	url := f.srv.URL + path
	if wait != "" {
		url += "?wait=" + wait
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, b, err
}

func (f *viewFixture) feedOK(t *testing.T, path, etag, wait string) (*http.Response, []byte) {
	t.Helper()
	resp, b, err := f.feed(t.Context(), t, path, etag, wait)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func (f *viewFixture) feedTag(t *testing.T, path string) string {
	t.Helper()
	resp, _ := f.feedOK(t, path, "", "")
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatalf("%s: no ETag", path)
	}
	return tag
}

func waitForStateWaiters(t *testing.T, a *App, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for a.stateWaiters.count() != n {
		if time.Now().After(deadline) {
			t.Fatalf("state waiters = %d, want %d", a.stateWaiters.count(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type feedReply struct {
	resp *http.Response
	body []byte
	err  error
}

func (f *viewFixture) park(ctx context.Context, t *testing.T, path, etag string) <-chan feedReply {
	t.Helper()
	before := f.app.stateWaiters.count()
	ch := make(chan feedReply, 1)
	go func() {
		resp, b, err := f.feed(ctx, t, path, etag, "20")
		ch <- feedReply{resp, b, err}
	}()
	waitForStateWaiters(t, f.app, before+1)
	return ch
}

func TestStateCarriesAWeakETagAndAnswers304(t *testing.T) {
	f := newStateFixture(t)
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running"})
	resp, body := f.feedOK(t, statePath, "", "")
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q, want a weak tag: the body varies under one tag", etag)
	}
	if resp.Header.Get(knobViewWaitHeader) != "25" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers = %v", resp.Header)
	}
	if !bytes.HasSuffix(body, []byte("}\n")) {
		t.Fatalf("body must keep writeJSON's trailing newline: %q", body)
	}
	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil || len(snap.Sessions) != 1 {
		t.Fatalf("snapshot %+v err %v", snap, err)
	}

	got, b := f.feedOK(t, statePath, etag, "")
	if got.StatusCode != http.StatusNotModified || len(b) != 0 {
		t.Fatalf("status %d body %q, want 304", got.StatusCode, b)
	}
	if got.Header.Get(devicesEpochHeader) == "" || got.Header.Get("ETag") != etag {
		t.Fatalf("a 304 must still carry the epoch and tag: %v", got.Header)
	}
}

func TestStateWithoutConditionKeepsTheOldBytesAndHeader(t *testing.T) {
	f := newStateFixture(t)
	resp, body := f.feedOK(t, statePath, "", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	if resp.Header.Get(devicesEpochHeader) == "" {
		t.Fatal("epoch header lost")
	}
	for _, key := range []string{`"now"`, `"sessions"`, `"render"`} {
		if !bytes.Contains(body, []byte(key)) {
			t.Fatalf("body lacks %s: %s", key, body)
		}
	}
}

func TestStateETagIgnoresNowHeartbeatsAndTheirReordering(t *testing.T) {
	f := newStateFixture(t)
	a := StatusRequest{Source: "M4", Tool: "claude", Session: "a", State: "running"}
	b := StatusRequest{Source: "M4", Tool: "codex", Session: "b", State: "running"}
	f.app.Upsert(a)
	f.app.Upsert(b)
	first := f.feedTag(t, statePath)
	time.Sleep(2 * time.Millisecond)
	if got := f.feedTag(t, statePath); got != first {
		t.Fatal("the tag moved with now")
	}
	f.app.Upsert(a)
	if got := f.feedTag(t, statePath); got != first {
		t.Fatal("a heartbeat that only reordered the sessions moved the tag")
	}
}

func TestStateETagMovesWhenUpdatedAtCrossesAMinute(t *testing.T) {
	base := time.Date(2026, 10, 30, 12, 0, 10, 0, time.UTC)
	s := Session{Source: "M4", Tool: "claude", Session: "a", State: "running", UpdatedAt: base}
	same, later := s, s
	same.UpdatedAt = base.Add(30 * time.Second)
	later.UpdatedAt = base.Add(time.Minute)
	if (moodState{Sessions: []Session{s}}).etag() != (moodState{Sessions: []Session{same}}).etag() {
		t.Fatal("two heartbeats inside one minute moved the tag")
	}
	if (moodState{Sessions: []Session{s}}).etag() == (moodState{Sessions: []Session{later}}).etag() {
		t.Fatal("updated_at can stay stale past a minute")
	}
}

func TestStateETagMovesOnEverythingTheAppShows(t *testing.T) {
	f := newStateFixture(t)
	req := StatusRequest{Source: "M4", Tool: "claude", Session: "a", State: "running", Message: "one"}
	f.app.Upsert(req)
	first := f.feedTag(t, statePath)
	req.Message = "two"
	f.app.Upsert(req)
	second := f.feedTag(t, statePath)
	if second == first {
		t.Fatal("a new message did not move the tag")
	}
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "codex", Session: "b", State: "running"})
	if f.feedTag(t, statePath) == second {
		t.Fatal("a new session did not move the tag")
	}
}

func TestUpsertWakesStateWaitersForANonWinningChange(t *testing.T) {
	f := newStateFixture(t)
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "a", State: "waiting", Message: "approve"})
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "codex", Session: "b", State: "running", Message: "one"})
	etag := f.feedTag(t, statePath)
	got := f.park(t.Context(), t, statePath, etag)
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "codex", Session: "b", State: "running", Message: "two"})
	r := recvWithin(t, got, "long poll")
	if r.err != nil || r.resp.StatusCode != http.StatusOK || !bytes.Contains(r.body, []byte(`"two"`)) {
		t.Fatalf("reply %+v", r)
	}
}

func TestStateWaitTimesOutWith304(t *testing.T) {
	f := newStateFixture(t)
	etag := f.feedTag(t, statePath)
	start := time.Now()
	resp, body := f.feedOK(t, statePath, etag, "1")
	if took := time.Since(start); took < time.Second || took > 3*time.Second {
		t.Fatalf("took %v", took)
	}
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 || resp.Header.Get(devicesEpochHeader) == "" {
		t.Fatalf("status %d body %q headers %v", resp.StatusCode, body, resp.Header)
	}
}

func TestStateWaitRechecksTimeBasedChanges(t *testing.T) {
	f := newStateFixture(t)
	f.app.viewRecheck = 10 * time.Millisecond
	f.app.updateConfig(func(c *Config) { c.Display.DoneTTLSeconds = 1 })
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "a", State: "done"})
	etag := f.feedTag(t, statePath)
	resp, body := f.feedOK(t, statePath, etag, "5")
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte(`"session":"a"`)) {
		t.Fatalf("status %d body %s, want the reaped session gone", resp.StatusCode, body)
	}
}

func TestStateWaitIsSkippedWhenItCouldNotPark(t *testing.T) {
	f := newStateFixture(t)
	for name, c := range map[string]struct {
		inm  string
		want int
	}{
		"no condition": {"", http.StatusOK},
		"stale tag":    {`W/"0000000000000000"`, http.StatusOK},
		"star":         {"*", http.StatusNotModified},
		"not the tag":  {`"nope", *`, http.StatusNotModified},
	} {
		start := time.Now()
		resp, _ := f.feedOK(t, statePath, c.inm, "20")
		if resp.StatusCode != c.want || time.Since(start) > 2*time.Second {
			t.Errorf("%s: status %d after %v, want an immediate %d", name, resp.StatusCode, time.Since(start), c.want)
		}
	}
	if f.app.stateWaiters.count() != 0 {
		t.Fatal("a waiter leaked")
	}
}

func TestStateFeedsRejectABadWait(t *testing.T) {
	f := newStateFixture(t)
	for _, path := range []string{statePath, pomoStatePath} {
		resp, body := f.feedOK(t, path, "", "x")
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("ETag") != "" {
			t.Fatalf("%s: status %d body %s", path, resp.StatusCode, body)
		}
	}
}

func TestPomodoroETagIgnoresTheCountdownWhileCounting(t *testing.T) {
	f := newStateFixture(t)
	f.eng.Start(pomodoro.PhaseFocus)
	resp, body := f.feedOK(t, pomoStatePath, "", "")
	etag := resp.Header.Get("ETag")
	f.clk.advance(10 * time.Second)
	resp2, body2 := f.feedOK(t, pomoStatePath, "", "")
	if resp2.Header.Get("ETag") != etag {
		t.Fatal("the tag moved with the countdown")
	}
	if bytes.Equal(body, body2) {
		t.Fatalf("remaining_sec must keep counting in the body: %s", body2)
	}
	got, b := f.feedOK(t, pomoStatePath, etag, "")
	if got.StatusCode != http.StatusNotModified || len(b) != 0 {
		t.Fatalf("status %d body %q, want 304", got.StatusCode, b)
	}
}

func TestPomodoroETagMovesOnEveryTimerChange(t *testing.T) {
	f := newStateFixture(t)
	seen := map[string]string{"idle": f.feedTag(t, pomoStatePath)}
	note := func(name string) {
		t.Helper()
		tag := f.feedTag(t, pomoStatePath)
		for other, old := range seen {
			if old == tag {
				t.Fatalf("%s has the same tag as %s", name, other)
			}
		}
		seen[name] = tag
	}
	f.eng.Start(pomodoro.PhaseFocus)
	note("started")
	f.clk.advance(5 * time.Second)
	f.eng.Pause(f.clk.Now())
	note("paused")
	f.clk.advance(time.Minute)
	if got := f.feedTag(t, pomoStatePath); got != seen["paused"] {
		t.Fatal("a paused timer's tag moved with time")
	}
	f.eng.Resume(f.clk.Now())
	note("resumed")
	f.eng.Skip(f.clk.Now())
	note("skipped")
}

func TestPomodoroTwoRoundsOfOnePhaseGetDifferentTags(t *testing.T) {
	f := newStateFixture(t)
	f.eng.Start(pomodoro.PhaseFocus)
	first := f.feedTag(t, pomoStatePath)
	f.clk.advance(time.Minute)
	f.eng.Stop(f.clk.Now())
	f.eng.Start(pomodoro.PhaseFocus)
	if f.feedTag(t, pomoStatePath) == first {
		t.Fatal("a restarted timer kept the tag, so the app would keep the old deadline")
	}
}

func TestPomodoroWaitWakesOnAnAction(t *testing.T) {
	f := newStateFixture(t)
	f.eng.Start(pomodoro.PhaseFocus)
	etag := f.feedTag(t, pomoStatePath)
	got := f.park(t.Context(), t, pomoStatePath, etag)
	f.eng.Pause(f.clk.Now())
	f.app.pomoChanged()
	r := recvWithin(t, got, "long poll")
	if r.err != nil || r.resp.StatusCode != http.StatusOK || !bytes.Contains(r.body, []byte(`"paused":true`)) {
		t.Fatalf("reply %+v", r)
	}
}

func TestPomodoroDisabledMidWaitAnswers404(t *testing.T) {
	f := newStateFixture(t)
	etag := f.feedTag(t, pomoStatePath)
	got := f.park(t.Context(), t, pomoStatePath, etag)
	f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
	f.app.changes.notify(topicConfig | topicPomodoro)
	r := recvWithin(t, got, "long poll")
	if r.err != nil || r.resp.StatusCode != http.StatusNotFound || r.resp.Header.Get("ETag") != "" {
		t.Fatalf("reply %+v", r)
	}
}

func TestPomodoroDisabledAnswers404(t *testing.T) {
	f := newStateFixture(t)
	f.app.updateConfig(func(c *Config) { c.Pomodoro.Enabled = false })
	resp, _ := f.feedOK(t, pomoStatePath, "", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestStateWaitersAreBoundedPerRouteAndClient(t *testing.T) {
	f := newStateFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	etag := f.feedTag(t, statePath)
	for range knobViewWaitersPerDevice {
		f.park(ctx, t, statePath, etag)
	}
	resp, body := f.feedOK(t, statePath, etag, "20")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "2" {
		t.Fatalf("status %d body %s headers %v", resp.StatusCode, body, resp.Header)
	}
	pomoTag := f.feedTag(t, pomoStatePath)
	f.park(ctx, t, pomoStatePath, pomoTag)
	metrics := getMetrics(t, f)
	for _, want := range []string{
		"ember_state_waiters 3\n",
		`ember_state_longpoll_total{result="busy"} 1` + "\n",
		`ember_knob_view_longpoll_total{result="busy"} 0` + "\n",
		"ember_knob_view_waiters 0\n",
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics lack %q:\n%s", want, grepLines(metrics, "longpoll"))
		}
	}
	cancel()
	waitForStateWaiters(t, f.app, 0)
}

func TestStateWaitersCannotStarveTheKnob(t *testing.T) {
	f := newStateFixture(t)
	for i := range knobViewWaitersTotal {
		if _, ok := f.app.stateWaiters.acquire("state|" + strconv.Itoa(i)); !ok {
			t.Fatalf("slot %d refused", i)
		}
	}
	if _, ok := f.app.stateWaiters.acquire("state|one-more"); ok {
		t.Fatal("the state pool has no total cap")
	}
	if _, ok := f.app.viewWaiters.acquire(f.m.ID); !ok {
		t.Fatal("a full state pool took a slot from the knob")
	}
}

func TestStateWaitReleasesOnClientDisconnect(t *testing.T) {
	f := newStateFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	got := f.park(ctx, t, statePath, f.feedTag(t, statePath))
	cancel()
	if r := recvWithin(t, got, "long poll"); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v", r.err)
	}
	waitForStateWaiters(t, f.app, 0)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(getMetrics(t, f), `ember_state_longpoll_total{result="gone"} 1`) {
		if time.Now().After(deadline) {
			t.Fatal("gone not counted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStateWaitAnswers304OnShutdown(t *testing.T) {
	f := newStateFixture(t)
	got := f.park(t.Context(), t, statePath, f.feedTag(t, statePath))
	f.app.changes.close()
	r := recvWithin(t, got, "long poll")
	if r.err != nil || r.resp.StatusCode != http.StatusNotModified {
		t.Fatalf("reply %+v", r)
	}
	if body := getMetrics(t, f); !strings.Contains(body, `ember_state_longpoll_total{result="shutdown"} 1`) {
		t.Fatalf("metrics:\n%s", grepLines(body, "longpoll"))
	}
}

func TestStateFeedsNeedNoAuthWhileWaiting(t *testing.T) {
	f := newStateFixture(t)
	for _, path := range []string{statePath, pomoStatePath} {
		resp, _ := f.feedOK(t, path, f.feedTag(t, path), "1")
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
}
