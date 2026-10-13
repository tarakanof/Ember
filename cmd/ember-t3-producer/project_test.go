package main

import (
	"testing"
	"time"
)

func TestTickSendsThreadTitleAsProject(t *testing.T) {
	w := testWatcher()
	w.cfg.SendProject = true
	th := thread{ID: "th-1", Title: "Fix   login\nbug", Schema: 2, Status: "running", ChangedAt: t0}
	posts, _ := w.tick([]thread{th}, t0)
	if len(posts) != 1 || posts[0].Project != "Fix login bug" {
		t.Fatalf("posts = %+v; want project from the title", posts)
	}
	th.Title = "Renamed"
	if posts, _ := w.tick([]thread{th}, t0.Add(time.Second)); len(posts) != 1 || posts[0].Project != "Renamed" {
		t.Fatalf("a rename must post the new project at once: %+v", posts)
	}
}

func TestTickSendProjectOffSendsNone(t *testing.T) {
	w := testWatcher()
	w.cfg.SendProject = false
	posts, _ := w.tick([]thread{{ID: "th-1", Title: "Fix", Schema: 2, Status: "running", ChangedAt: t0}}, t0)
	if len(posts) != 1 || posts[0].Project != "" {
		t.Fatalf("posts = %+v", posts)
	}
}

func TestTickRenameIsPostedWithTheTrailOff(t *testing.T) {
	w := testWatcher()
	w.cfg.SendProject = true
	w.cfg.ActivityTrailEnabled = false
	th := thread{ID: "th-1", Title: "A", Schema: 2, Status: "running", ChangedAt: t0}
	w.tick([]thread{th}, t0)
	th.Title = "B"
	if posts, _ := w.tick([]thread{th}, t0.Add(time.Second)); len(posts) != 1 || posts[0].Project != "B" {
		t.Fatalf("posts = %+v", posts)
	}
}
