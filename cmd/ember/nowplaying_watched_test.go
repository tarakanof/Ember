package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type watchedAnswer struct {
	Commands []queuedCommand `json:"commands"`
	Watched  *bool           `json:"watched"`
}

func getWatched(t *testing.T, srv *httptest.Server, query string) (int, watchedAnswer) {
	t.Helper()
	resp, b := npDo(t, srv, "GET", "/v1/nowplaying/commands?player=M4"+query, testToken, nil, nil)
	var a watchedAnswer
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatal(err)
		}
		if a.Watched == nil {
			t.Fatalf("answer without watched: %s", b)
		}
	}
	return resp.StatusCode, a
}

func watchedNow(t *testing.T, srv *httptest.Server) bool {
	t.Helper()
	code, a := getWatched(t, srv, "&wait=0")
	if code != http.StatusOK {
		t.Fatalf("commands = %d", code)
	}
	return *a.Watched
}

type watchedPoll struct {
	done  chan struct{}
	code  int
	ans   watchedAnswer
	taken time.Duration
}

func startWatchedPoll(t *testing.T, app *App, srv *httptest.Server, query string) *watchedPoll {
	t.Helper()
	p := &watchedPoll{done: make(chan struct{})}
	began := time.Now()
	go func() {
		defer close(p.done)
		resp, b := npDo(t, srv, "GET", "/v1/nowplaying/commands?player=M4"+query, testToken, nil, nil)
		p.code, p.taken = resp.StatusCode, time.Since(began)
		_ = json.Unmarshal(b, &p.ans)
	}()
	waitPolling(t, app.nowPlaying.commands, "M4")
	return p
}

func (p *watchedPoll) answer(t *testing.T, within time.Duration) watchedAnswer {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(within):
		t.Fatalf("long poll still waiting after %v", within)
	}
	if p.code != http.StatusOK || p.ans.Watched == nil {
		t.Fatalf("long poll = %d %+v", p.code, p.ans)
	}
	return p.ans
}

func (p *watchedPoll) stillWaiting(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		t.Fatalf("long poll answered early: %d %+v", p.code, p.ans)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestMusicWatchedFollowsTheKnobsShownPage(t *testing.T) {
	f := shownFixture(t)
	if watchedNow(t, f.srv) {
		t.Fatal("watched before the knob reported a page")
	}
	pollShown(t, f, "nowplaying")
	if !watchedNow(t, f.srv) {
		t.Fatal("not watched while the knob shows now playing")
	}
	pollShown(t, f, "weather:temp")
	if watchedNow(t, f.srv) {
		t.Fatal("watched after the knob turned to weather")
	}
}

func TestMusicWatchedGoesOffFiftySecondsAfterTheLastNowPlayingReport(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "nowplaying")
	f.clk.advance(knobShownFresh)
	if !watchedNow(t, f.srv) {
		t.Fatal("not watched at the freshness edge")
	}
	f.clk.advance(time.Second)
	if watchedNow(t, f.srv) {
		t.Fatal("still watched after the report went stale")
	}
}

func TestMusicWatchedIsOffWithNoKnobPaired(t *testing.T) {
	_, srv := newDevicesApp(t, "")
	if watchedNow(t, srv) {
		t.Fatal("watched with no knob paired")
	}
}

func oldKnobFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := newViewFixture(t)
	f.app.viewClock = f.clk.Now
	postCapsCheckin(t, f, "0.9.0", knobCapsFull)
	return f
}

func TestMusicWatchedStaysOnWhileAnOldKnobPolls(t *testing.T) {
	t.Run("old firmware", func(t *testing.T) {
		f := oldKnobFixture(t)
		if watchedNow(t, f.srv) {
			t.Fatal("watched for an old knob that never polled")
		}
		pollShown(t, f, "weather")
		if !watchedNow(t, f.srv) {
			t.Fatal("watched off for a polling knob that can't report")
		}
		f.clk.advance(knobShownFresh)
		if !watchedNow(t, f.srv) {
			t.Fatal("watched off at the edge of the old knob's last poll")
		}
		f.clk.advance(time.Second)
		if watchedNow(t, f.srv) {
			t.Fatal("still watched for an old knob that went quiet")
		}
	})
	t.Run("old knob next to a reporting one", func(t *testing.T) {
		f := shownFixture(t)
		pollShown(t, f, "weather:temp")
		old := mintSecondKnob(t, f.srv)
		if watchedNow(t, f.srv) {
			t.Fatal("watched for an old knob that never polled")
		}
		resp, b := devReq(t, f.srv, "GET", knobViewPath, old.Token, "")
		mustOK(t, "old view", resp, b)
		if !watchedNow(t, f.srv) {
			t.Fatal("watched off while an old knob might show now playing")
		}
	})
}

func TestMusicWatchedOnOneOfTwoReportingKnobs(t *testing.T) {
	f := shownFixture(t)
	other := mintSecondKnob(t, f.srv)
	caps := maps.Clone(knobCapsFull)
	caps["features"] = []string{featureViewWait, featureShown}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", other.Token, capsCheckinBody(t, "0.10.0", 1, caps))
	mustOK(t, "checkin", resp, b)
	pollShown(t, f, "bot")
	if watchedNow(t, f.srv) {
		t.Fatal("watched with no knob on now playing")
	}
	resp, b = devReq(t, f.srv, "GET", knobViewPath+"?shown=nowplaying", other.Token, "")
	mustOK(t, "other view", resp, b)
	if !watchedNow(t, f.srv) {
		t.Fatal("not watched while the second knob shows now playing")
	}
}

func TestMusicWatchedAnswersAtOnceWhenTheCallerIsBehind(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "nowplaying")
	code, a := getWatched(t, f.srv, "&wait=25&watched=false")
	if code != http.StatusOK || !*a.Watched || len(a.Commands) != 0 {
		t.Fatalf("answer = %d %+v", code, a)
	}
}

func TestMusicWatchedRejectsAMalformedValue(t *testing.T) {
	f := shownFixture(t)
	if code, _ := getWatched(t, f.srv, "&wait=0&watched=maybe"); code != http.StatusBadRequest {
		t.Fatalf("watched=maybe = %d, want 400", code)
	}
}

func TestMusicWatchedChangeWakesTheLongPoll(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "weather:temp")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=false")
	p.stillWaiting(t)
	pollShown(t, f, "nowplaying")
	if a := p.answer(t, time.Second); !*a.Watched {
		t.Fatalf("woken answer = %+v, want watched", a)
	}
	p = startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	pollShown(t, f, "nowplaying")
	p.stillWaiting(t)
	pollShown(t, f, "pomodoro")
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("woken answer = %+v, want not watched", a)
	}
}

func TestMusicWatchedStalenessWakesTheLongPoll(t *testing.T) {
	f := shownFixture(t)
	timers := fakeWatchedTimers(f.app)
	pollShown(t, f, "nowplaying")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	timers.armed(t, knobShownFresh+time.Millisecond)
	p.stillWaiting(t)
	f.clk.advance(knobShownFresh + time.Second)
	timers.fire <- time.Time{}
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after staleness = %+v, want not watched", a)
	}
}

type watchedTimers struct {
	fire    chan time.Time
	asked   chan time.Duration
	stopped atomic.Int32
}

func fakeWatchedTimers(app *App) *watchedTimers {
	w := &watchedTimers{fire: make(chan time.Time), asked: make(chan time.Duration, 8)}
	app.watchedAfter = func(d time.Duration) (<-chan time.Time, func()) {
		w.asked <- d
		return w.fire, func() { w.stopped.Add(1) }
	}
	return w
}

func (w *watchedTimers) armed(t *testing.T, want time.Duration) {
	t.Helper()
	select {
	case d := <-w.asked:
		if d != want {
			t.Fatalf("stale timer = %v, want %v", d, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no stale timer armed")
	}
}

func (w *watchedTimers) noneArmed(t *testing.T) {
	t.Helper()
	select {
	case d := <-w.asked:
		t.Fatalf("timer re-armed (%v) without a wake", d)
	default:
	}
}

func TestMusicWatchedSamePageHeartbeatExtendsWithoutAWake(t *testing.T) {
	f := shownFixture(t)
	timers := fakeWatchedTimers(f.app)
	pollShown(t, f, "nowplaying")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	timers.armed(t, knobShownFresh+time.Millisecond)
	f.clk.advance(30 * time.Second)
	pollShown(t, f, "nowplaying")
	p.stillWaiting(t)
	timers.noneArmed(t)
	f.clk.advance(21 * time.Second)
	timers.fire <- time.Time{}
	timers.armed(t, 29*time.Second+time.Millisecond)
	p.stillWaiting(t)
	f.clk.advance(30 * time.Second)
	timers.fire <- time.Time{}
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after the heartbeat went stale = %+v, want not watched", a)
	}
	if n := timers.stopped.Load(); n != 2 {
		t.Fatalf("%d stale timers stopped, want both", n)
	}
}

func TestMusicWatchedWithTwoKnobsEndsWhenTheLastReportExpires(t *testing.T) {
	f := shownFixture(t)
	timers := fakeWatchedTimers(f.app)
	other := mintSecondKnob(t, f.srv)
	caps := maps.Clone(knobCapsFull)
	caps["features"] = []string{featureViewWait, featureShown}
	resp, b := devReq(t, f.srv, "POST", "/v1/devices/self/checkin", other.Token, capsCheckinBody(t, "0.10.0", 1, caps))
	mustOK(t, "checkin", resp, b)
	pollShown(t, f, "nowplaying")
	f.clk.advance(20 * time.Second)
	resp, b = devReq(t, f.srv, "GET", knobViewPath+"?shown=nowplaying", other.Token, "")
	mustOK(t, "other view", resp, b)
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	timers.armed(t, knobShownFresh+time.Millisecond)
	f.clk.advance(31 * time.Second)
	if !watchedNow(t, f.srv) {
		t.Fatal("not watched while the second knob's report is fresh")
	}
	p.stillWaiting(t)
	f.clk.advance(20 * time.Second)
	timers.fire <- time.Time{}
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after both reports expired = %+v, want not watched", a)
	}
}

func TestMusicWatchedOldKnobGoingQuietEndsTheLongPoll(t *testing.T) {
	f := oldKnobFixture(t)
	timers := fakeWatchedTimers(f.app)
	pollShown(t, f, "weather")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	timers.armed(t, knobShownFresh+time.Millisecond)
	p.stillWaiting(t)
	f.clk.advance(knobShownFresh + time.Second)
	timers.fire <- time.Time{}
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after the old knob went quiet = %+v, want not watched", a)
	}
}

func TestMusicWatchedOldKnobComingBackWakesTheLongPoll(t *testing.T) {
	f := oldKnobFixture(t)
	pollShown(t, f, "weather")
	f.clk.advance(knobShownFresh + time.Second)
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=false")
	p.stillWaiting(t)
	pollShown(t, f, "weather")
	if a := p.answer(t, time.Second); !*a.Watched {
		t.Fatalf("answer after the old knob polled = %+v, want watched", a)
	}
}

func TestMusicWatchedChangeBetweenSubscribeAndReadIsNotLost(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "bot")
	var once sync.Once
	f.app.watchedRead = func() {
		once.Do(func() {
			f.app.knobShown.set(f.m.ID, knobShown{Page: knobNowPlayingPage, At: f.clk.Now()})
		})
	}
	began := time.Now()
	code, a := getWatched(t, f.srv, "&wait=3&watched=false")
	if took := time.Since(began); code != http.StatusOK || !*a.Watched || took > time.Second {
		t.Fatalf("answer after %v = %d %+v, want watched at once", took, code, a)
	}
}

func TestMusicWatchedReportAfterAStaleOneWakesTheLongPoll(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "nowplaying")
	f.clk.advance(knobShownFresh + time.Second)
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=false")
	p.stillWaiting(t)
	pollShown(t, f, "nowplaying")
	if a := p.answer(t, time.Second); !*a.Watched {
		t.Fatalf("answer after the knob came back = %+v, want watched", a)
	}
}

func TestMusicWatchedCapsChangeWakesTheLongPoll(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "bot")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=false")
	p.stillWaiting(t)
	postCapsCheckin(t, f, "0.10.0", knobCapsFull)
	if a := p.answer(t, time.Second); !*a.Watched {
		t.Fatalf("answer after the caps lost shown = %+v, want watched", a)
	}
}

func TestMusicWatchedDeleteWakesTheLongPoll(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "nowplaying")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	p.stillWaiting(t)
	resp, b := devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
	mustOK(t, "delete", resp, b)
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after the last knob went = %+v, want not watched", a)
	}
	if _, ok := f.app.knobShown.polledUntil(f.m.ID, f.clk.Now()); ok {
		t.Fatal("the deleted knob's view-request time is still kept")
	}
}

func TestMusicWatchedLongPollWithoutTheParamWaitsForCommandsOnly(t *testing.T) {
	f := shownFixture(t)
	pollShown(t, f, "bot")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=1")
	pollShown(t, f, "nowplaying")
	a := p.answer(t, 3*time.Second)
	if p.taken < 900*time.Millisecond || !*a.Watched {
		t.Fatalf("answer after %v = %+v, want the full wait and watched", p.taken, a)
	}
}

func TestMusicWatchedSurvivesConcurrentReportsAndPolls(t *testing.T) {
	f := shownFixture(t)
	f.app.viewLimit.burst, f.app.viewLimit.perSec = 1e6, 1e6
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 20 {
				page := "nowplaying"
				if (i+j)%2 == 0 {
					page = "weather:temp"
				}
				if resp, b := f.getQuery(t, "?shown="+page, ""); resp.StatusCode != http.StatusOK {
					t.Errorf("view = %d: %s", resp.StatusCode, b)
				}
			}
		})
		wg.Go(func() {
			for j := range 10 {
				q := "/v1/nowplaying/commands?player=M4&wait=1&watched=" + strconv.FormatBool(j%2 == 0)
				if resp, b := npDo(t, f.srv, "GET", q, testToken, nil, nil); resp.StatusCode != http.StatusOK {
					t.Errorf("commands = %d: %s", resp.StatusCode, b)
				}
			}
		})
	}
	wg.Wait()
	q := f.app.nowPlaying.commands
	q.mu.Lock()
	n := q.waiters
	q.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d command waiters left", n)
	}
}
