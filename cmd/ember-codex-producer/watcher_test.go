package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRollout creates a rollout file under sessionsDir in the UTC date dir for
// `when`, with the given lines, and sets its mtime to `when`.
func writeRollout(t *testing.T, sessionsDir, name string, when time.Time, lines ...string) string {
	t.Helper()
	d := when.UTC()
	dir := filepath.Join(sessionsDir, d.Format("2006"), d.Format("01"), d.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	var buf []byte
	for _, l := range lines {
		buf = append(buf, []byte(l+"\n")...)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestWatcher(dir string, now time.Time) *watcher {
	w := newWatcher(Config{Source: "mbp", SessionsDir: dir, StateDir: filepath.Join(dir, "markers"), ActivityWindowSeconds: 90, ContextPctEnabled: true})
	w.now = func() time.Time { return now }
	return w
}

func TestWatcher_FiltersExecSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	writeRollout(t, dir, "rollout-exec.jsonl", now, metaExec, evStarted)
	w := newTestWatcher(dir, now)
	posts, _, _ := w.tick()
	if len(posts) != 1 {
		t.Fatalf("want 1 post (cli only), got %d: %+v", len(posts), posts)
	}
	if posts[0].Session != "u-123" || posts[0].State != "running" {
		t.Errorf("post = %+v", posts[0])
	}
}

func TestWatcher_PostsOnChangeNotEveryTick(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	if posts, _, _ := w.tick(); len(posts) != 1 {
		t.Fatalf("first tick want 1 post, got %d", len(posts))
	}
	w.now = func() time.Time { return now.Add(2 * time.Second) }
	if posts, _, _ := w.tick(); len(posts) != 0 {
		t.Fatalf("unchanged tick want 0 posts, got %d", len(posts))
	}
}

func TestWatcher_KeepalivePost(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	w.tick()
	w.now = func() time.Time { return now.Add(16 * time.Second) }
	if posts, _, _ := w.tick(); len(posts) != 1 {
		t.Fatalf("keepalive tick want 1 post, got %d", len(posts))
	}
}

func TestWatcher_ReapsAgedSession(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	w.tick()
	w.now = func() time.Time { return now.Add(200 * time.Second) }
	posts, deletes, _ := w.tick()
	if len(posts) != 0 {
		t.Errorf("aged session must not keepalive-post, got %d", len(posts))
	}
	if len(deletes) != 1 || deletes[0].Session != "u-123" {
		t.Fatalf("want 1 delete for u-123, got %+v", deletes)
	}
	if len(w.sessions) != 0 {
		t.Errorf("session should be dropped, have %d", len(w.sessions))
	}
}

func TestWatcher_RecoversFromTruncation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted, evAgent)
	w := newTestWatcher(dir, now)
	if posts, _, _ := w.tick(); len(posts) != 1 || posts[0].State != "running" {
		t.Fatalf("first tick want 1 running post, got %+v", posts)
	}
	// Rewrite the file smaller (truncation/rotation): the new size is below the
	// stored offset, so a naive seek would read nothing and keep stale state.
	later := now.Add(3 * time.Second)
	if err := os.WriteFile(path, []byte(metaCLI+"\n"+evDone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, later, later)
	w.now = func() time.Time { return later }
	posts, _, _ := w.tick()
	if len(posts) != 1 || posts[0].State != "done" {
		t.Fatalf("want 1 done post after truncation recovery, got %+v", posts)
	}
}

func TestWatcher_TracksSessionOlderThanWindow(t *testing.T) {
	dir := t.TempDir()
	day0 := time.Now().UTC()
	path := writeRollout(t, dir, "rollout-cli.jsonl", day0, metaCLI, evStarted)
	w := newTestWatcher(dir, day0)
	if posts, _, _ := w.tick(); len(posts) != 1 {
		t.Fatalf("initial discovery want 1 post, got %d", len(posts))
	}
	// Two UTC days later the file's date dir falls outside the candidate window,
	// but the session is still active (fresh append + recent mtime).
	day2 := day0.Add(48 * time.Hour)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(evDone + "\n")
	f.Close()
	os.Chtimes(path, day2, day2)
	w.now = func() time.Time { return day2 }
	posts, deletes, _ := w.tick()
	if len(deletes) != 0 {
		t.Fatalf("active tracked session must not be deleted, got %+v", deletes)
	}
	if len(posts) != 1 || posts[0].State != "done" {
		t.Fatalf("want 1 done post from out-of-window tracked session, got %+v", posts)
	}
}

func TestWatcher_PrunesIgnoredOutsideWindow(t *testing.T) {
	dir := t.TempDir()
	day0 := time.Now().UTC()
	exec := writeRollout(t, dir, "rollout-exec.jsonl", day0, metaExec, evStarted)
	w := newTestWatcher(dir, day0)
	w.tick()
	if !w.ignored[exec] {
		t.Fatalf("exec (non-cli) session should be ignored after first tick")
	}
	// Two UTC days later the exec file is outside the candidate window.
	day2 := day0.Add(48 * time.Hour)
	w.now = func() time.Time { return day2 }
	w.tick()
	if w.ignored[exec] {
		t.Fatalf("ignored entry outside candidate window should be pruned, have %d", len(w.ignored))
	}
}

func TestWatcher_TailsAppendedEvents(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	w.tick() // running
	later := now.Add(3 * time.Second)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(evDone + "\n")
	f.Close()
	os.Chtimes(path, later, later)
	w.now = func() time.Time { return later }
	posts, _, _ := w.tick()
	if len(posts) != 1 || posts[0].State != "done" {
		t.Fatalf("want 1 done post after append, got %+v", posts)
	}
}
