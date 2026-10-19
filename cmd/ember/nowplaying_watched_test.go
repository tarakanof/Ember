package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
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

func TestMusicWatchedStaysOnWithoutAReportingKnob(t *testing.T) {
	t.Run("no knob", func(t *testing.T) {
		_, srv := newDevicesApp(t, "")
		if !watchedNow(t, srv) {
			t.Fatal("watched off with no knob paired")
		}
	})
	t.Run("old firmware", func(t *testing.T) {
		f := newViewFixture(t)
		postCapsCheckin(t, f, "0.9.0", knobCapsFull)
		if !watchedNow(t, f.srv) {
			t.Fatal("watched off for a knob that can't report")
		}
	})
	t.Run("old knob next to a reporting one", func(t *testing.T) {
		f := shownFixture(t)
		pollShown(t, f, "weather:temp")
		mintSecondKnob(t, f.srv)
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
	fire := make(chan time.Time)
	asked := make(chan time.Duration, 4)
	f.app.watchedAfter = func(d time.Duration) <-chan time.Time {
		asked <- d
		return fire
	}
	pollShown(t, f, "nowplaying")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=true")
	select {
	case d := <-asked:
		if want := knobShownFresh + time.Millisecond; d != want {
			t.Fatalf("stale timer = %v, want %v", d, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no stale timer armed")
	}
	p.stillWaiting(t)
	f.clk.advance(knobShownFresh + time.Second)
	fire <- time.Time{}
	if a := p.answer(t, time.Second); *a.Watched {
		t.Fatalf("answer after staleness = %+v, want not watched", a)
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
	pollShown(t, f, "bot")
	p := startWatchedPoll(t, f.app, f.srv, "&wait=25&watched=false")
	p.stillWaiting(t)
	resp, b := devReq(t, f.srv, "DELETE", "/v1/devices/"+f.m.ID, testToken, "")
	mustOK(t, "delete", resp, b)
	if a := p.answer(t, time.Second); !*a.Watched {
		t.Fatalf("answer after the last knob went = %+v, want watched", a)
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
