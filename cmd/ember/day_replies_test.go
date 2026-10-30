package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/pomodoro"
)

func exactBody(replies ...string) string {
	return `{"replies":[` + strings.Join(replies, ",") + `]}`
}

func exactReply(at time.Time, tool, session, repo string) string {
	return fmt.Sprintf(`{"at":%d,"source":"mbp","tool":%q,"session":%q,"repo":%q}`, at.Unix(), tool, session, repo)
}

func (f *dayFixture) postExact(t *testing.T, body string, want int) {
	t.Helper()
	resp, b := devReq(t, f.srv, "POST", "/v1/day/replies", testToken, body)
	if resp.StatusCode != want {
		t.Fatalf("POST /v1/day/replies = %d, want %d: %s", resp.StatusCode, want, b)
	}
}

func TestExactLiveReplyWhileOffRaisesTheOfferOnce(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 52))
	body := exactBody(exactReply(dayAt(10, 9, 51), "claude", "s1", "Ember"))
	f.postExact(t, body, http.StatusOK)
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want 1", n)
	}
	if k := f.state(t); k.OfferStart != "09:51" || k.State != "off" {
		t.Fatalf("state = %+v, want an offer from 09:51", k)
	}
	f.postExact(t, body, http.StatusOK)
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies after a replay = %d, want 1", n)
	}
}

func TestBackfilledRepliesGoToHistoryOnly(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 12, 0))
	seq, _ := f.app.changes.subscribe()
	f.postExact(t, exactBody(
		exactReply(dayAt(10, 9, 30), "claude", "s1", "Ember"),
		exactReply(dayAt(10, 11, 57), "claude", "s1", "Ember"),
		exactReply(dayAt(9, 23, 10), "t3", "th1", "cinder"),
	), http.StatusOK)
	if got := f.app.changes.since(seq); got&topicDay != 0 {
		t.Fatalf("backfill notified %v; the knob must not wake", got)
	}
	if k := f.state(t); k.OfferStart != "" || k.RepliesOffLastNight != 0 {
		t.Fatalf("state = %+v, want no offer from old replies", k)
	}
	h := f.history(t, 2)
	if h[0].RepliesOff != 2 || h[1].RepliesOff != 1 {
		t.Fatalf("history = %+v, want 1 reply on the 9th and 2 on the 10th", h)
	}
	if h[0].FirstReply == nil || !h[0].FirstReply.Equal(dayAt(10, 9, 30)) {
		t.Fatalf("first reply = %v, want 09:30", h[0].FirstReply)
	}
}

func TestExactRepliesReplaceTheEstimateForTheirPairOnly(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.postExact(t, exactBody(exactReply(dayAt(10, 8, 0), "claude", "s9", "")), http.StatusOK)
	f.reply(t, "s1")
	if n := f.replies(t); n != 1 {
		t.Fatalf("replies = %d, want only the exact one: the claude estimate must be skipped", n)
	}
	body := func(state string) string {
		return `{"source":"mbp","tool":"codex","session":"c1","state":"` + state + `"}`
	}
	for _, s := range []string{"done", "running"} {
		resp, b := devReq(t, f.srv, "POST", "/v1/status", testToken, body(s))
		mustOK(t, "status", resp, b)
	}
	if n := f.replies(t); n != 2 {
		t.Fatalf("replies = %d, want the codex estimate kept", n)
	}

	g := f.restart(t)
	g.clk.advance(2 * time.Minute)
	g.reply(t, "s2")
	if n := g.replies(t); n != 2 {
		t.Fatalf("replies after restart = %d, want the claude estimate still skipped", n)
	}
	g.clk.set(dayAt(11, 8, 1))
	g.reply(t, "s3")
	if n := g.replies(t); n != 3 {
		t.Fatalf("replies = %d, want the estimate back 24 h after the last exact reply", n)
	}
}

func TestHistoryDropsEstimatesCoveredByExactReplies(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.reply(t, "s1")
	f.clk.advance(time.Minute)
	for _, state := range []string{"done", "running"} {
		resp, b := devReq(t, f.srv, "POST", "/v1/status", testToken, `{"source":"mbp","tool":"codex","session":"c1","state":"`+state+`"}`)
		mustOK(t, "status", resp, b)
	}
	if n := f.replies(t); n != 2 {
		t.Fatalf("estimated replies = %d, want 2", n)
	}
	f.postExact(t, exactBody(exactReply(dayAt(10, 8, 40), "claude", "s1", "Ember")), http.StatusOK)
	if n := f.replies(t); n != 2 {
		t.Fatalf("replies = %d, want the claude estimate replaced by the exact reply and codex kept", n)
	}
	if h := f.history(t, 1); h[0].RepliesOff != 2 || !h[0].FirstReply.Equal(dayAt(10, 8, 40)) {
		t.Fatalf("history = %+v, want 2 replies from 08:40", h[0])
	}
}

func TestDayRepliesValidation(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	many := make([]string, dayRepliesMax+1)
	for i := range many {
		many[i] = exactReply(dayAt(10, 8, 0), "claude", fmt.Sprintf("s%d", i), "")
	}
	cases := map[string]string{
		"too many":     exactBody(many...),
		"prompt text":  `{"replies":[{"at":1,"source":"mbp","tool":"claude","session":"s1","text":"hi"}]}`,
		"future":       exactBody(exactReply(dayAt(10, 9, 11), "claude", "s1", "")),
		"no at":        `{"replies":[{"source":"mbp","tool":"claude","session":"s1"}]}`,
		"no session":   `{"replies":[{"at":1,"source":"mbp","tool":"claude"}]}`,
		"no source":    `{"replies":[{"at":1,"tool":"claude","session":"s1"}]}`,
		"no tool":      `{"replies":[{"at":1,"source":"mbp","session":"s1"}]}`,
		"not an array": `{"replies":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { f.postExact(t, body, http.StatusBadRequest) })
	}
	f.postExact(t, exactBody(many[:dayRepliesMax]...), http.StatusOK)
	if n := f.replies(t); n != dayRepliesMax {
		t.Fatalf("replies = %d, want %d", n, dayRepliesMax)
	}
	if got := requiredScope("POST /v1/day/replies"); got != scopeIngest {
		t.Fatalf("scope = %q, want ingest", got)
	}
}

func TestDayRepliesAreGoneWhenDisabled(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.call(t, "PUT", "/v1/day/config", `{"enabled":false}`, http.StatusOK)
	f.postExact(t, exactBody(exactReply(dayAt(10, 8, 0), "claude", "s1", "")), http.StatusNotFound)
}

func TestDayRepliesMigrationKeepsOldRowsAndDropsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE day_replies (id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, source TEXT NOT NULL, tool TEXT NOT NULL, session_key TEXT NOT NULL)`,
		`INSERT INTO day_replies (at, source, tool, session_key) VALUES (100, 'mbp', 'claude', 'k'), (100, 'mbp', 'claude', 'k'), (200, 'mbp', 'claude', 'k')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := pomodoro.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := s.DayRepliesBetween(time.Unix(0, 0), time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Exact || rs[0].Repo != "" {
		t.Fatalf("replies = %+v, want 2 estimated rows", rs)
	}
	s.Close()
	if s, err = pomodoro.Open(path); err != nil {
		t.Fatalf("reopening a migrated store: %v", err)
	}
	s.Close()
}

func TestExactReplyPromotesAnEstimateInTheSameSecond(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.reply(t, "s1")
	f.postExact(t, exactBody(exactReply(dayAt(10, 9, 0), "claude", "s1", "Ember")), http.StatusOK)
	rs, err := f.app.store.DayRepliesBetween(dayAt(10, 0, 0), dayAt(11, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || !rs[0].Exact || rs[0].Repo != "Ember" {
		t.Fatalf("replies = %+v, want the estimate promoted to exact", rs)
	}
	g := f.restart(t)
	g.clk.advance(2 * time.Minute)
	g.reply(t, "s2")
	if n := g.replies(t); n != 1 {
		t.Fatalf("replies after restart = %d, want the pair still exact", n)
	}
}

func TestExactRepliesSecondsApartInOneSessionAreOnePrompt(t *testing.T) {
	f := newDayFixture(t, "", dayAt(10, 9, 0))
	f.postExact(t, exactBody(exactReply(dayAt(10, 8, 0), "claude", "s1", "")), http.StatusOK)
	f.postExact(t, exactBody(
		exactReply(dayAt(10, 8, 0).Add(time.Second), "claude", "s1", ""),
		exactReply(dayAt(10, 8, 0).Add(-2*time.Second), "claude", "s1", ""),
		exactReply(dayAt(10, 8, 0).Add(time.Second), "claude", "s2", ""),
	), http.StatusOK)
	if n := f.replies(t); n != 2 {
		t.Fatalf("replies = %d, want 2: a hook and a transcript stamp of one prompt are one reply", n)
	}
	f.postExact(t, exactBody(exactReply(dayAt(10, 8, 0).Add(3*time.Second), "claude", "s1", "")), http.StatusOK)
	if n := f.replies(t); n != 3 {
		t.Fatalf("replies = %d, want a prompt 3 s later counted", n)
	}
}
