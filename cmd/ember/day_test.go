package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/workday"
)

var dayZone = time.FixedZone("CET", 3600)

func dayAt(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, dayZone) }

type dayFixture struct {
	app  *App
	srv  *httptest.Server
	clk  *stepClock
	path string
}

func newDayFixture(t *testing.T, path string, now time.Time) *dayFixture {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "day.db")
	}
	app, srv := newDevicesApp(t, path)
	clk := &stepClock{now: now}
	app.zone = dayZone
	app.dayClock = clk.Now
	app.sessions = app.newSessionRegistry(clk.Now)
	return &dayFixture{app: app, srv: srv, clk: clk, path: path}
}

func (f *dayFixture) restart(t *testing.T) *dayFixture {
	t.Helper()
	return newDayFixture(t, f.path, f.clk.Now())
}

func (f *dayFixture) call(t *testing.T, method, path, body string, want int) knobDay {
	t.Helper()
	resp, b := devReq(t, f.srv, method, path, testToken, body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	var k knobDay
	if want == http.StatusOK {
		if err := json.Unmarshal(b, &k); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func (f *dayFixture) state(t *testing.T) knobDay {
	t.Helper()
	return f.call(t, "GET", "/v1/day/state", "", http.StatusOK)
}

func (f *dayFixture) status(t *testing.T, session, state string) {
	t.Helper()
	body := `{"source":"mbp","tool":"claude","session":"` + session + `","state":"` + state + `"}`
	resp, b := devReq(t, f.srv, "POST", "/v1/status", testToken, body)
	mustOK(t, "status", resp, b)
}

func (f *dayFixture) reply(t *testing.T, session string) {
	t.Helper()
	f.status(t, session, "done")
	f.status(t, session, "running")
}

func (f *dayFixture) replies(t *testing.T) int {
	t.Helper()
	n, err := f.app.store.CountDayReplies(time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *dayFixture) history(t *testing.T, days int) []dayHistoryOut {
	t.Helper()
	resp, b := devReq(t, f.srv, "GET", "/v1/day/history?days="+strconv.Itoa(days), testToken, "")
	mustOK(t, "history", resp, b)
	var out struct {
		Days []dayHistoryOut `json:"days"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Days
}

func (c *stepClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func TestDayStartBreakResumeStopSurviveARestart(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	k := f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	if k.State != "on" || k.Started != "10:00" || k.StopAt != "18:00" {
		t.Fatalf("start = %+v", k)
	}
	f.clk.advance(2 * time.Hour)
	if k = f.call(t, "POST", "/v1/day/break", "", http.StatusOK); k.State != "break" || k.OnMin != 120 {
		t.Fatalf("break = %+v", k)
	}
	f.clk.advance(15 * time.Minute)
	if k = f.state(t); k.BreakMin != 15 || k.OnMin != 120 {
		t.Fatalf("on break = %+v, want 15 min break, 120 on", k)
	}
	if k = f.call(t, "POST", "/v1/day/resume", "", http.StatusOK); k.State != "on" || k.BreakMin != 15 {
		t.Fatalf("resume = %+v", k)
	}
	f.clk.advance(30 * time.Minute)

	g := f.restart(t)
	if k = g.state(t); k.State != "on" || k.Started != "10:00" || k.OnMin != 150 || k.BreakMin != 15 || k.StopAt != "18:00" {
		t.Fatalf("after restart = %+v", k)
	}
	k = g.call(t, "POST", "/v1/day/stop", "", http.StatusOK)
	want := knobDaySummary{OnMin: 150, InRange: true, WeekInRange: 1, WeekDays: 1}
	if k.State != "off" || k.Summary == nil || *k.Summary != want {
		t.Fatalf("stop = %+v summary %+v, want %+v", k, k.Summary, want)
	}
	g.clk.advance(daySummaryFor)
	if k = g.state(t); k.Summary != nil || k.StopAt != "18:00" {
		t.Fatalf("after the summary = %+v, want no summary and the last stop time", k)
	}
	if k = g.restart(t).state(t); k.State != "off" || k.StopAt != "18:00" {
		t.Fatalf("after second restart = %+v", k)
	}
}

func TestDayActionsAreIdempotentOrConflict(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	f.call(t, "POST", "/v1/day/break", "", http.StatusConflict)
	f.call(t, "POST", "/v1/day/resume", "", http.StatusConflict)
	if k := f.call(t, "POST", "/v1/day/stop", "", http.StatusOK); k.State != "off" || k.StopAt != dayDefaultStop {
		t.Fatalf("stop while off = %+v", k)
	}
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"25:00"}`, http.StatusBadRequest)
	f.call(t, "POST", "/v1/day/start", `{"at":"soon"}`, http.StatusBadRequest)
	f.call(t, "POST", "/v1/day/start", `{"stop":"18:00"}`, http.StatusBadRequest)
	f.call(t, "POST", "/v1/day/start", "", http.StatusOK)
	k := f.call(t, "POST", "/v1/day/start", `{"stop_at":"17:00"}`, http.StatusOK)
	if k.State != "on" || k.StopAt != dayDefaultStop {
		t.Fatalf("second start = %+v, want the first start kept", k)
	}
	f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	if k = f.call(t, "POST", "/v1/day/break", "", http.StatusOK); k.State != "break" {
		t.Fatalf("second break = %+v", k)
	}
	f.call(t, "PUT", "/v1/day/stop_at", `{"stop_at":"7pm"}`, http.StatusBadRequest)
	if k = f.call(t, "PUT", "/v1/day/stop_at", `{"stop_at":"23:30"}`, http.StatusOK); k.StopAt != "23:30" {
		t.Fatalf("stop_at = %+v", k)
	}
	events, err := f.app.store.LastDayEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []workday.Kind{workday.Start, workday.Break, workday.StopAt}
	if len(events) != len(kinds) {
		t.Fatalf("events = %+v, want %v", events, kinds)
	}
	for i, e := range events {
		if e.Kind != kinds[i] {
			t.Fatalf("events = %+v, want %v", events, kinds)
		}
	}
}

func TestDayRoutesAreGoneWhenDisabled(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	resp, b := devReq(t, f.srv, "PUT", "/v1/day/config", testToken, `{"enabled":false}`)
	mustOK(t, "config", resp, b)
	f.call(t, "GET", "/v1/day/state", "", http.StatusNotFound)
	f.call(t, "POST", "/v1/day/start", "", http.StatusNotFound)
	if resp, _ := devReq(t, f.srv, "GET", "/v1/day/history", testToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("history = %d, want 404", resp.StatusCode)
	}
}

func TestDayConfigValidatesAndDefaults(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	resp, b := devReq(t, f.srv, "GET", "/v1/day/config", testToken, "")
	mustOK(t, "get", resp, b)
	want := `{"enabled":true,"default_stop":"00:00","break_hint_minutes":90,"auto_stop_minutes":30,"grace_minutes":15}`
	if strings.TrimSpace(string(b)) != want {
		t.Fatalf("config = %s, want %s", b, want)
	}
	for _, bad := range []string{`{"default_stop":"24:61"}`, `{"auto_stop_minutes":1}`, `{"grace_minutes":-1}`, `{"break_hint_minutes":601}`} {
		if resp, _ := devReq(t, f.srv, "PUT", "/v1/day/config", testToken, bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %s = %d, want 400", bad, resp.StatusCode)
		}
	}
	resp, b = devReq(t, f.srv, "PUT", "/v1/day/config", testToken, `{"default_stop":"22:30","break_hint_minutes":0}`)
	mustOK(t, "put", resp, b)
	if k := f.state(t); k.StopAt != "22:30" {
		t.Fatalf("state stop_at = %q, want the new default", k.StopAt)
	}
}

func noticeHasSound(p map[string]any) bool {
	for k := range p {
		if strings.HasPrefix(k, "sound") || k == "rtttl" || k == "loopSound" {
			return true
		}
	}
	return false
}

func TestDayOffIsQuietOnceAClockInIsOnRecord(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	now := f.clk.Now()
	if f.app.quietAt(now) || f.app.coord.quietAt(now) {
		t.Fatal("quiet before the day clock was ever used")
	}
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	if f.app.quietAt(now) {
		t.Fatal("quiet while on the clock")
	}
	f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	if f.app.quietAt(now) {
		t.Fatal("quiet on a break")
	}
	f.call(t, "POST", "/v1/day/stop", "", http.StatusOK)
	if !f.app.quietAt(now) || !f.app.coord.quietAt(now) {
		t.Fatal("not quiet after clocking out")
	}
	f.app.updateConfig(func(c *Config) { c.Display.Indicators = true })
	if want := f.app.coord.desiredIndicators(Snapshot{}, now); want[2].color != indicatorQuietColor {
		t.Errorf("clock quiet indicator = %+v, want the quiet colour", want[2])
	}

	pub := f.app.publisher.(*recordingPublisher)
	n := notice{app: "test", kind: noticeReminder, priority: noticeQueue,
		sound: noticeSound{rtttl: defaultReminderSound}, payload: map[string]any{"text": "hi"}}
	if err := f.app.coord.showNotice(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	sent := pub.NotifySnapshot()
	if len(sent) == 0 || noticeHasSound(sent[len(sent)-1]) {
		t.Fatalf("notice while off = %v, want it without sound", sent)
	}

	m := mintKnob(t, f.srv, http.StatusCreated)
	body, _, err := f.app.knobView(m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"quiet":true`) {
		t.Fatalf("knob view while off = %s, want quiet", body)
	}

	if g := f.restart(t); !g.app.quietAt(now) {
		t.Fatal("restart lost quiet when off")
	}

	resp, b := devReq(t, f.srv, "PUT", "/v1/day/config", testToken, `{"enabled":false}`)
	mustOK(t, "config", resp, b)
	if f.app.quietAt(now) {
		t.Fatal("quiet with the day clock disabled")
	}
}

func TestReplyToADoneSessionWhileOffRecordsOneReplyAndRaisesTheOffer(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 52))
	f.status(t, "s1", "running")
	f.status(t, "s1", "running")
	if n := f.replies(t); n != 0 {
		t.Fatalf("a new session recorded %d replies", n)
	}
	f.status(t, "s1", "done")
	f.status(t, "s1", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
	if k := f.state(t); k.OfferStart != "09:52" || k.State != "off" {
		t.Fatalf("state = %+v, want an offer from 09:52", k)
	}
	f.reply(t, "s1")
	f.status(t, "s2", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want 1 (throttled per minute, new session ignored)", n)
	}
	f.clk.advance(3 * time.Minute)
	f.status(t, "s2", "waiting")
	f.status(t, "s2", "running")
	if n := f.replies(t); n != 2 {
		t.Fatalf("replies = %d, want 2 (waiting → running counts)", n)
	}
	if k := f.state(t); k.OfferStart != "09:52" {
		t.Fatalf("offer moved to %q", k.OfferStart)
	}

	k := f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00","at":"09:52"}`, http.StatusOK)
	if k.State != "on" || k.Started != "09:52" || k.OfferStart != "" || k.OnMin != 3 {
		t.Fatalf("confirm = %+v, want back-dated to 09:52", k)
	}
	h := f.history(t, 1)
	if h[0].RepliesOn != 2 || h[0].RepliesOff != 0 {
		t.Fatalf("history = %+v, want both replies on the clock", h[0])
	}
}

func TestReplyToAReapedDoneSessionStillCounts(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.status(t, "s1", "running")
	f.status(t, "s1", "done")
	f.status(t, "s2", "running")
	f.clk.advance(10 * time.Minute)
	f.status(t, "s1", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want 1 for a reply after the done session was reaped", n)
	}
	f.status(t, "s2", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, a session reaped while running counted", n)
	}
	f.status(t, "s3", "done")
	f.clk.advance(dayEndedMemory + time.Minute)
	f.status(t, "s3", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, a session done for over a day counted", n)
	}
}

func TestReplyToADeletedDoneSessionStillCounts(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.status(t, "s1", "running")
	f.status(t, "s1", "done")
	f.status(t, "s2", "running")
	for _, key := range []string{"s1", "s2"} {
		resp, b := devReq(t, f.srv, "DELETE", "/v1/status", testToken, `{"source":"mbp","tool":"claude","session":"`+key+`"}`)
		mustOK(t, "delete", resp, b)
	}
	f.clk.advance(10 * time.Minute)
	f.status(t, "s1", "running")
	f.status(t, "s2", "running")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want 1 for the deleted done session only", n)
	}
}

func TestStartBeforeTheLastDayEventIsRejected(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	f.call(t, "POST", "/v1/day/start", "", http.StatusOK)
	f.clk.advance(time.Hour)
	f.call(t, "POST", "/v1/day/stop", "", http.StatusOK)
	f.call(t, "POST", "/v1/day/start", `{"at":"10:30"}`, http.StatusBadRequest)
	if k := f.call(t, "POST", "/v1/day/start", `{"at":"11:00"}`, http.StatusOK); k.Started != "11:00" {
		t.Fatalf("start at the clock-out = %+v", k)
	}
}

func TestOfferIsDismissedForTheDayAndExpires(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.reply(t, "s1")
	if k := f.call(t, "POST", "/v1/day/offer/dismiss", "", http.StatusOK); k.OfferStart != "" {
		t.Fatalf("dismiss = %+v", k)
	}
	f.clk.advance(2 * time.Minute)
	f.reply(t, "s1")
	if k := f.state(t); k.OfferStart != "" {
		t.Fatalf("offer came back the same day: %+v", k)
	}

	g := newDayFixture(t, "", dayAt(10, 9, 0))
	g.reply(t, "s1")
	g.clk.advance(dayOfferFor - time.Second)
	if k := g.state(t); k.OfferStart != "09:00" {
		t.Fatalf("offer before expiry = %+v", k)
	}
	g.clk.advance(time.Second)
	if k := g.state(t); k.OfferStart != "" {
		t.Fatalf("offer after 60 min = %+v", k)
	}
}

func TestNoOfferAfterTheDayWasStarted(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	f.clk.advance(8 * time.Hour)
	f.call(t, "POST", "/v1/day/stop", "", http.StatusOK)
	f.clk.advance(4 * time.Hour)
	f.reply(t, "s1")
	if k := f.state(t); k.OfferStart != "" || k.RepliesOffLastNight != 0 {
		t.Fatalf("evening reply = %+v, want no offer and no last-night count yet", k)
	}
	f.clk.advance(time.Hour)
	f.reply(t, "s1")
	f.clk.set(dayAt(11, 9, 30))
	if k := f.state(t); k.RepliesOffLastNight != 2 || k.OfferStart != "" {
		t.Fatalf("next morning = %+v, want 2 replies off last night", k)
	}
	f.reply(t, "s1")
	k := f.state(t)
	if k.RepliesOffLastNight != 3 || k.OfferStart != "09:30" {
		t.Fatalf("first reply of the morning = %+v", k)
	}
	if k = f.restart(t).state(t); k.RepliesOffLastNight != 3 {
		t.Fatalf("restart = %+v, want the count kept", k)
	}
	if k = f.call(t, "POST", "/v1/day/start", "", http.StatusOK); k.RepliesOffLastNight != 0 {
		t.Fatalf("on the clock = %+v", k)
	}
}

func TestAutoStopAtTheLaterOfStopTimeAndLastReply(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	f.status(t, "s1", "running")
	f.clk.set(dayAt(10, 18, 20))
	f.reply(t, "s1")
	f.clk.set(dayAt(10, 18, 49))
	f.app.dayTick(f.clk.Now())
	if k := f.state(t); k.State != "on" || k.OverMin != 49 {
		t.Fatalf("29 min after the last reply = %+v", k)
	}
	f.clk.set(dayAt(10, 18, 50))
	f.app.dayTick(f.clk.Now())
	if k := f.state(t); k.State != "off" || k.Summary != nil {
		t.Fatalf("30 min after the last reply = %+v, want off with no summary", k)
	}
	h := f.history(t, 1)[0]
	if !h.Auto || h.Stop == nil || !h.Stop.Equal(dayAt(10, 18, 20)) || h.InRange {
		t.Fatalf("history = %+v, want auto stop at 18:20, out of range", h)
	}

	g := newDayFixture(t, "", dayAt(10, 10, 0))
	g.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	g.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	g.clk.set(dayAt(10, 18, 29))
	g.app.dayTick(g.clk.Now())
	if k := g.state(t); k.State != "break" {
		t.Fatalf("29 min past stop = %+v", k)
	}
	g.clk.set(dayAt(10, 18, 30))
	g.app.dayTick(g.clk.Now())
	h = g.history(t, 1)[0]
	if !h.Auto || !h.Stop.Equal(dayAt(10, 18, 0)) || !h.InRange || h.OnSec != 0 {
		t.Fatalf("history = %+v, want auto stop at 18:00 in range", h)
	}
}

func TestAutoStopAtTheDayStartHour(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	resp, b := devReq(t, f.srv, "PUT", "/v1/day/config", testToken, `{"auto_stop_minutes":240}`)
	mustOK(t, "config", resp, b)
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"03:30"}`, http.StatusOK)
	f.status(t, "s1", "running")
	f.clk.set(dayAt(11, 3, 10))
	f.reply(t, "s1")
	f.clk.set(dayAt(11, 3, 59))
	f.app.dayTick(f.clk.Now())
	if k := f.state(t); k.State != "on" {
		t.Fatalf("03:59 = %+v", k)
	}
	f.clk.set(dayAt(11, 4, 0))
	f.app.dayTick(f.clk.Now())
	h := f.history(t, 2)[1]
	if !h.Auto || h.Stop == nil || !h.Stop.Equal(dayAt(11, 3, 30)) {
		t.Fatalf("history = %+v, want auto stop at 03:30", h)
	}
}

func TestDayBlockShowsBreakHintAndOvertime(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 16, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"17:00"}`, http.StatusOK)
	f.clk.advance(89 * time.Minute)
	if k := f.state(t); k.BreakHint || k.OverMin != 29 {
		t.Fatalf("89 min = %+v", k)
	}
	f.clk.advance(time.Minute)
	if k := f.state(t); !k.BreakHint {
		t.Fatalf("90 min = %+v, want a break hint", k)
	}
	f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	f.clk.advance(10 * time.Minute)
	if k := f.call(t, "POST", "/v1/day/resume", "", http.StatusOK); k.BreakHint {
		t.Fatalf("after a break = %+v", k)
	}
}

func TestDayBlockListsFinishedBreaks(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	if k := f.state(t); k.Breaks != nil {
		t.Fatalf("no break yet = %+v", k.Breaks)
	}
	f.clk.advance(3 * time.Hour)
	f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	f.clk.advance(40 * time.Minute)
	f.call(t, "POST", "/v1/day/resume", "", http.StatusOK)
	f.clk.advance(2 * time.Hour)
	f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
	if k := f.state(t); len(k.Breaks) != 1 || k.Breaks[0] != [2]string{"12:00", "12:40"} {
		t.Fatalf("on a second break = %+v, want only the finished one", k.Breaks)
	}
	f.clk.advance(15 * time.Minute)
	f.call(t, "POST", "/v1/day/resume", "", http.StatusOK)
	k := f.state(t)
	if want := [][2]string{{"12:00", "12:40"}, {"14:40", "14:55"}}; len(k.Breaks) != 2 || k.Breaks[0] != want[0] || k.Breaks[1] != want[1] {
		t.Fatalf("breaks = %+v, want %v", k.Breaks, want)
	}
	f.call(t, "POST", "/v1/day/stop", "", http.StatusOK)
	if k := f.state(t); k.Breaks != nil {
		t.Fatalf("off = %+v, want no breaks", k.Breaks)
	}
}

func TestDayBlockKeepsTheLatestBreaks(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 6, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"23:00"}`, http.StatusOK)
	for i := 0; i < dayKnobBreaks+2; i++ {
		f.clk.advance(20 * time.Minute)
		f.call(t, "POST", "/v1/day/break", "", http.StatusOK)
		f.clk.advance(5 * time.Minute)
		f.call(t, "POST", "/v1/day/resume", "", http.StatusOK)
	}
	k := f.state(t)
	if len(k.Breaks) != dayKnobBreaks || k.Breaks[0] != [2]string{"07:10", "07:15"} {
		t.Fatalf("%d breaks, first %v; want %d, the oldest two dropped (07:10 is the third)", len(k.Breaks), k.Breaks[0], dayKnobBreaks)
	}
}

func seedDayWeek(t *testing.T, a *App) {
	t.Helper()
	ev := []workday.Event{
		{At: dayAt(9, 10, 0), Kind: workday.Start, Value: "18:00"},
		{At: dayAt(9, 18, 10), Kind: workday.Stop},
		{At: dayAt(10, 11, 0), Kind: workday.Start, Value: "18:00"},
		{At: dayAt(10, 13, 0), Kind: workday.Break},
		{At: dayAt(10, 14, 0), Kind: workday.Resume},
		{At: dayAt(10, 18, 30), Kind: workday.Stop},
		{At: dayAt(11, 9, 0), Kind: workday.Start, Value: "00:00"},
		{At: dayAt(12, 0, 40), Kind: workday.AutoStop},
		{At: dayAt(13, 10, 0), Kind: workday.Start, Value: "23:30"},
		{At: dayAt(13, 23, 0), Kind: workday.Stop},
	}
	for _, e := range ev {
		if err := a.store.RecordDayEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []time.Time{dayAt(9, 12, 0), dayAt(9, 22, 0), dayAt(10, 2, 0), dayAt(11, 23, 50), dayAt(12, 1, 0), dayAt(14, 15, 0)} {
		if err := a.store.RecordDayReply(workday.Reply{At: r, Source: "mbp", Tool: "claude", SessionKey: "mbp/claude/a"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDayHistoryAndWeeksForASeededWeek(t *testing.T) {
	f := newDayFixture(t, "", dayAt(16, 12, 0))
	seedDayWeek(t, f.app)
	h := f.history(t, 8)
	if len(h) != 8 || h[0].Date != "2026-11-16" || h[7].Date != "2026-11-09" {
		t.Fatalf("history dates = %s … %s (%d)", h[0].Date, h[len(h)-1].Date, len(h))
	}
	byDate := map[string]dayHistoryOut{}
	for _, d := range h {
		byDate[d.Date] = d
	}
	type want struct {
		inRange, auto       bool
		onSec, on, off, brk int
	}
	for date, w := range map[string]want{
		"2026-11-09": {true, false, 8*3600 + 10*60, 1, 2, 0},
		"2026-11-10": {false, false, 6*3600 + 30*60, 0, 0, 3600},
		"2026-11-11": {false, true, 15*3600 + 40*60, 1, 1, 0},
		"2026-11-13": {true, false, 13 * 3600, 0, 0, 0},
		"2026-11-14": {false, false, 0, 0, 1, 0},
	} {
		d := byDate[date]
		if d.InRange != w.inRange || d.Auto != w.auto || d.OnSec != w.onSec || d.RepliesOn != w.on ||
			d.RepliesOff != w.off || d.BreakSec != w.brk {
			t.Errorf("%s = %+v, want %+v", date, d, w)
		}
	}
	if d := byDate["2026-11-14"]; d.Start != nil || d.StopTarget != nil || d.FirstReply == nil {
		t.Errorf("day off = %+v, want null start/stop_target and a reply", d)
	}
	if d := byDate["2026-11-10"]; len(d.Breaks) != 1 || d.Breaks[0].End == nil || *d.StopTarget != "18:00" {
		t.Errorf("10th = %+v", d)
	}

	resp, b := devReq(t, f.srv, "GET", "/v1/day/weeks?weeks=2", testToken, "")
	mustOK(t, "weeks", resp, b)
	var weeks struct {
		Weeks []dayWeekOut `json:"weeks"`
	}
	if err := json.Unmarshal(b, &weeks); err != nil {
		t.Fatal(err)
	}
	wantWeeks := []dayWeekOut{
		{Week: "2026-W47"},
		{Week: "2026-W46", DaysWorked: 4, DaysInRange: 2, OnSec: (8*3600 + 10*60) + (6*3600 + 30*60) + (15*3600 + 40*60) + 13*3600, RepliesOff: 4},
	}
	if len(weeks.Weeks) != 2 || weeks.Weeks[0] != wantWeeks[0] || weeks.Weeks[1] != wantWeeks[1] {
		t.Fatalf("weeks = %+v, want %+v", weeks.Weeks, wantWeeks)
	}
}

func TestDayPruneDropsRowsPastRetention(t *testing.T) {
	f := newDayFixture(t, "", dayAt(16, 12, 0))
	seedDayWeek(t, f.app)
	f.app.dayTick(dayAt(10, 0, 0).Add(dayRetention))
	events, _ := f.app.store.LastDayEvents(100)
	if len(events) != 8 || events[0].Kind != workday.Start || !events[0].At.Equal(dayAt(10, 11, 0)) {
		t.Fatalf("events after prune = %+v", events)
	}
}

func dayKnobFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := goldenViewFixture(t)
	f.app.zone = time.UTC
	f.app.dayClock = f.clk.Now
	caps := map[string]any{"view": []int{1, 1}, "pages": []string{"bot", "day", "weather"}, "features": []string{"view_wait"}}
	postCapsCheckin(t, f, "0.10.0", caps)
	return f
}

func TestKnobViewCarriesTheDayBlockOnlyForADayPage(t *testing.T) {
	f := dayKnobFixture(t)
	if _, ok := viewKeys(t, goldenView(t, f))["day"]; !ok {
		t.Fatal("view lacks day with the day page in caps and on")
	}
	putKnobConfig(t, f.srv, f.m.ID, `{"pages":[{"id":"bot","on":true},{"id":"day","on":false}]}`)
	if _, ok := viewKeys(t, goldenView(t, f))["day"]; ok {
		t.Fatal("day block sent with the day page off")
	}

	legacy := goldenViewFixture(t)
	if _, ok := viewKeys(t, goldenView(t, legacy))["day"]; ok {
		t.Fatal("a knob without caps got the day block")
	}
}

func TestKnobDayGolden(t *testing.T) {
	f := dayKnobFixture(t)
	f.app.Upsert(StatusRequest{Source: "mbp", Tool: "claude", Session: "s1", State: "running"})
	f.app.Upsert(StatusRequest{Source: "mbp", Tool: "codex", Session: "s2", State: "running"})
	assertDeviceGolden(t, "view_day_off", goldenView(t, f))

	resp, b := devReq(t, f.srv, "POST", "/v1/day/start", f.m.Token, `{"stop_at":"00:00"}`)
	assertDeviceGolden(t, "day_action", mustOK(t, "start", resp, b))
	f.clk.advance(14*time.Hour + 20*time.Minute)
	assertDeviceGolden(t, "view_day_on", goldenView(t, f))

	resp, b = devReq(t, f.srv, "POST", "/v1/day/stop", f.m.Token, "")
	mustOK(t, "stop", resp, b)
	assertDeviceGolden(t, "view_day_summary", goldenView(t, f))
}

func TestKnobDayBreakGolden(t *testing.T) {
	f := dayKnobFixture(t)
	act := func(path, body string) {
		t.Helper()
		resp, b := devReq(t, f.srv, "POST", path, f.m.Token, body)
		mustOK(t, path, resp, b)
	}
	act("/v1/day/start", `{"stop_at":"20:00"}`)
	f.clk.advance(2 * time.Hour)
	act("/v1/day/break", "")
	f.clk.advance(35 * time.Minute)
	act("/v1/day/resume", "")
	f.clk.advance(90 * time.Minute)
	act("/v1/day/break", "")
	f.clk.advance(12 * time.Minute)
	assertDeviceGolden(t, "view_day_break", goldenView(t, f))
}

func TestRestartKeepsAnOpenDayAfterManyStopTimeChanges(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 10, 0))
	f.call(t, "POST", "/v1/day/start", `{"stop_at":"18:00"}`, http.StatusOK)
	for range 210 {
		f.call(t, "PUT", "/v1/day/stop_at", `{"stop_at":"18:15"}`, http.StatusOK)
	}
	g := f.restart(t)
	if k := g.state(t); k.State != "on" || k.Started != "10:00" || k.StopAt != "18:15" {
		t.Fatalf("after restart = %+v", k)
	}
	if g.app.quietAt(g.clk.Now()) {
		t.Fatal("quiet after restart while on the clock")
	}
}
