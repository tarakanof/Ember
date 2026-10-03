package main

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func testWatcher() *watcher {
	return newWatcher(Config{
		Source: "mbp", SourceColor: "#123456", SourceCardEnabled: true, SessionBarEnabled: true,
		ActivityTrailEnabled: true, ActivityWindowSeconds: 300,
	})
}

func TestTickPostsRunningThread(t *testing.T) {
	w := testWatcher()
	posts, deletes := w.tick([]thread{{ID: "th-1", Title: "Fix login bug", Schema: 2, Status: "running", ChangedAt: t0}}, t0)
	if len(deletes) != 0 || len(posts) != 1 {
		t.Fatalf("posts=%d deletes=%d", len(posts), len(deletes))
	}
	p := posts[0]
	if p.Tool != "t3" || p.Source != "mbp" || p.Session != "th-1" || p.State != "running" || p.Activity != "Fix login bug" {
		t.Fatalf("post = %+v", p)
	}
	if p.SourceColor == nil || *p.SourceColor != "#123456" || p.SourceCard == nil || !*p.SourceCard {
		t.Fatalf("post enrichment = %+v", p)
	}
}

func TestTickKeepaliveAndChange(t *testing.T) {
	w := testWatcher()
	th := thread{ID: "th-1", Title: "T", Schema: 2, Status: "running", ChangedAt: t0}
	w.tick([]thread{th}, t0)
	if posts, _ := w.tick([]thread{th}, t0.Add(5*time.Second)); len(posts) != 0 {
		t.Fatalf("unchanged thread re-posted before keepalive: %+v", posts)
	}
	if posts, _ := w.tick([]thread{th}, t0.Add(keepaliveInterval)); len(posts) != 1 {
		t.Fatal("keepalive post missing")
	}
	th.PendingKind = "user_input"
	posts, _ := w.tick([]thread{th}, t0.Add(keepaliveInterval+time.Second))
	if len(posts) != 1 || posts[0].State != "waiting" || posts[0].Message != "needs input" {
		t.Fatalf("state change not posted at once: %+v", posts)
	}
	th.Title = "Renamed"
	if posts, _ := w.tick([]thread{th}, t0.Add(keepaliveInterval+2*time.Second)); len(posts) != 1 || posts[0].Activity != "Renamed" {
		t.Fatalf("title change not posted at once: %+v", posts)
	}
}

func TestTickDeletesGoneAndNonReportable(t *testing.T) {
	w := testWatcher()
	a := thread{ID: "a", Title: "A", Schema: 2, Status: "running", ChangedAt: t0}
	b := thread{ID: "b", Title: "B", Schema: 2, Status: "running", ChangedAt: t0}
	w.tick([]thread{a, b}, t0)
	b.Status = "interrupted"
	_, deletes := w.tick([]thread{b}, t0.Add(time.Second))
	if len(deletes) != 2 {
		t.Fatalf("deletes = %+v, want a (gone) and b (interrupted)", deletes)
	}
	for _, d := range deletes {
		if d.Tool != "t3" || d.Source != "mbp" {
			t.Fatalf("delete = %+v", d)
		}
	}
	if _, deletes := w.tick(nil, t0.Add(2*time.Second)); len(deletes) != 0 {
		t.Fatalf("deleted twice: %+v", deletes)
	}
}

func TestTickDoneOnlyWithinActivityWindow(t *testing.T) {
	w := testWatcher()
	old := thread{ID: "old", Title: "Old", Schema: 2, Status: "completed", ChangedAt: t0.Add(-time.Hour)}
	fresh := thread{ID: "fresh", Title: "Fresh", Schema: 2, Status: "completed", ChangedAt: t0.Add(-time.Minute)}
	posts, _ := w.tick([]thread{old, fresh}, t0)
	if len(posts) != 1 || posts[0].Session != "fresh" || posts[0].State != "done" {
		t.Fatalf("posts = %+v, want only the fresh done thread", posts)
	}
	_, deletes := w.tick([]thread{old, fresh}, t0.Add(5*time.Minute))
	if len(deletes) != 1 || deletes[0].Session != "fresh" {
		t.Fatalf("done thread past the window not deleted: %+v", deletes)
	}
}

func TestTickRunningIgnoresActivityWindow(t *testing.T) {
	w := testWatcher()
	long := thread{ID: "long", Title: "Long run", Schema: 2, Status: "running", ChangedAt: t0.Add(-2 * time.Hour)}
	if posts, _ := w.tick([]thread{long}, t0); len(posts) != 1 {
		t.Fatal("a long-running thread must still be reported")
	}
}

func TestTickActivityTrailDisabled(t *testing.T) {
	w := testWatcher()
	w.cfg.ActivityTrailEnabled = false
	posts, _ := w.tick([]thread{{ID: "x", Title: "Secret project", Schema: 2, Status: "running", ChangedAt: t0}}, t0)
	if posts[0].Activity != "" {
		t.Fatalf("activity = %q, want empty when the trail is off", posts[0].Activity)
	}
}

func TestTickTruncatesTitle(t *testing.T) {
	w := testWatcher()
	title := ""
	for i := 0; i < 30; i++ {
		title += "word "
	}
	posts, _ := w.tick([]thread{{ID: "x", Title: title, Schema: 2, Status: "running", ChangedAt: t0}}, t0)
	if n := len([]rune(posts[0].Activity)); n > maxActivityRunes {
		t.Fatalf("activity is %d runes, want <= %d", n, maxActivityRunes)
	}
}

func TestDropAllDeletesEveryLiveThread(t *testing.T) {
	w := testWatcher()
	w.tick([]thread{{ID: "a", Schema: 2, Status: "running", ChangedAt: t0}, {ID: "b", Schema: 2, Status: "running", ChangedAt: t0}}, t0)
	if deletes := w.dropAll(); len(deletes) != 2 {
		t.Fatalf("dropAll = %+v", deletes)
	}
	if deletes := w.dropAll(); len(deletes) != 0 {
		t.Fatal("dropAll must forget what it dropped")
	}
}
