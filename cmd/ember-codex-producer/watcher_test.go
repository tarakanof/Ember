package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func writeRollout(t *testing.T, sessionsDir, name string, when time.Time, lines ...string) string {
	t.Helper()
	d := when.Local()
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
	w := newWatcher(Config{Common: producer.Common{Source: "mbp"}, Gauges: producer.Gauges{ContextPctEnabled: true}, SessionsDir: dir, StateDir: filepath.Join(dir, "markers"), ActivityWindowSeconds: 90})
	w.now = func() time.Time { return now }
	return w
}

func TestWatcher_FiltersSubagentSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	writeRollout(t, dir, "rollout-vscode.jsonl", now, metaVSCode, evStarted)
	exec := writeRollout(t, dir, "rollout-exec.jsonl", now, metaExec, evStarted)
	claude := writeRollout(t, dir, "rollout-claude.jsonl", now, metaClaude, evStarted)
	sub := writeRollout(t, dir, "rollout-sub.jsonl", now, metaSub, evStarted)
	w := newTestWatcher(dir, now)
	posts, _, _ := w.tick()
	got := map[string]string{}
	for _, p := range posts {
		got[p.Session] = p.State
	}
	if len(posts) != 2 || got["u-123"] != "running" || got["u-vs"] != "running" {
		t.Fatalf("want cli + vscode running by default, got %+v", posts)
	}
	if !w.ignored[exec] || !w.ignored[claude] {
		t.Error("exec and Claude Code plugin sessions must be skipped by default")
	}
	if !w.ignored[sub] {
		t.Error("object-valued (subagent) source must be cached as ignored, not re-read every tick")
	}
}

func TestWatcher_OptInExecAndClaude(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-exec.jsonl", now, metaExec, evStarted)
	writeRollout(t, dir, "rollout-claude.jsonl", now, metaClaude, evStarted, evAgent)
	w := newTestWatcher(dir, now)
	w.cfg.Sources = parseSources("vscode,exec")
	w.cfg.IncludeClaude = true
	posts, _, _ := w.tick()
	got := map[string]string{}
	for _, p := range posts {
		got[p.Session] = p.Message
	}
	if len(posts) != 2 {
		t.Fatalf("want exec + Claude sessions, got %+v", posts)
	}
	if got["u-cc"] != "via Claude: Doing the thing" {
		t.Errorf("Claude session message = %q, want it marked via Claude", got["u-cc"])
	}
	if got["u-9"] != "" {
		t.Errorf("exec session message = %q", got["u-9"])
	}
}

func TestWatcher_IdleRolloutNotReReadOrDeleted(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-time.Hour)
	path := writeRollout(t, dir, "rollout-cli.jsonl", old, metaCLI, evStarted, evDone)
	w := newTestWatcher(dir, now)
	for i := 0; i < 5; i++ {
		w.now = func() time.Time { return now.Add(time.Duration(i) * 2 * time.Second) }
		posts, deletes, _ := w.tick()
		if len(posts) != 0 || len(deletes) != 0 {
			t.Fatalf("tick %d: idle rollout produced posts %+v deletes %+v", i, posts, deletes)
		}
	}
	if w.reads != 0 {
		t.Errorf("idle rollout opened %d times, want 0", w.reads)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(evStarted + "\n")
	f.Close()
	later := now.Add(20 * time.Second)
	os.Chtimes(path, later, later)
	w.now = func() time.Time { return later }
	if posts, _, _ := w.tick(); len(posts) != 1 || posts[0].State != "running" {
		t.Fatalf("changed rollout want 1 running post, got %+v", posts)
	}
}

func TestWatcher_UnpostedSessionNotDeleted(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI)
	w := newTestWatcher(dir, now)
	if posts, _, _ := w.tick(); len(posts) != 0 {
		t.Fatalf("want no post, got %+v", posts)
	}
	w.now = func() time.Time { return now.Add(200 * time.Second) }
	if _, deletes, _ := w.tick(); len(deletes) != 0 {
		t.Fatalf("never-posted session must not be DELETEd, got %+v", deletes)
	}
}

func TestWatcher_UsageFromNewestSnapshot(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	reset := now.Add(time.Hour).Unix()
	tok := func(ts string, pct int) string {
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":` + strconv.Itoa(pct) + `,"resets_at":` + strconv.FormatInt(reset, 10) + `}}}}`
	}
	writeRollout(t, dir, "rollout-a.jsonl", now, metaCLI, evStarted, tok("2026-10-05T08:00:10Z", 30))
	b := writeRollout(t, dir, "rollout-b.jsonl", now, metaVSCode, evStarted, tok("2026-10-05T08:00:05Z", 20))
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	_, _, usages := w.tick()
	if len(usages) != 1 || usages[0].FiveHour == nil || usages[0].FiveHour.UsedPercent != 30 {
		t.Fatalf("want one usage with the newest snapshot (30), got %+v", usages)
	}
	f, _ := os.OpenFile(b, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(evAgent + "\n")
	f.Close()
	later := now.Add(3 * time.Second)
	os.Chtimes(b, later, later)
	w.now = func() time.Time { return later }
	if _, _, usages := w.tick(); len(usages) != 0 {
		t.Fatalf("unchanged newest snapshot must not re-post, got %+v", usages)
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
	exec := writeRollout(t, dir, "rollout-sub.jsonl", day0, metaSub, evStarted)
	w := newTestWatcher(dir, day0)
	w.tick()
	if !w.ignored[exec] {
		t.Fatalf("subagent session should be ignored after first tick")
	}
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
	w.tick()
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

func TestWatcher_FindsLocalDayDirAheadOfUTC(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*3600)
	now := time.Date(2026, 10, 5, 0, 30, 0, 0, loc)
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "10", "05")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-cli.jsonl")
	if err := os.WriteFile(path, []byte(metaCLI+"\n"+evStarted+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, now, now)
	w := newTestWatcher(dir, now)
	w.loc = loc
	w.lastWalk = now
	if posts, _, _ := w.tick(); len(posts) != 1 || posts[0].Session != "u-123" {
		t.Fatalf("want the local-day session posted, got %+v", posts)
	}
}

func TestWatcher_FindsResumedSessionInOldDayDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.AddDate(0, 0, -10)
	path := writeRollout(t, dir, "rollout-old.jsonl", old, metaCLI, evStarted, evDone)
	w := newTestWatcher(dir, now)
	if posts, _, _ := w.tick(); len(posts) != 0 {
		t.Fatalf("idle old session must not post, got %+v", posts)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(evStarted + "\n")
	f.Close()
	later := now.Add(5 * time.Second)
	os.Chtimes(path, later, later)
	w.now = func() time.Time { return later }
	w.tick()
	walk := now.Add(resumeScanInterval + time.Second)
	w.now = func() time.Time { return walk }
	posts, _, _ := w.tick()
	if len(posts) != 1 || posts[0].Session != "u-123" || posts[0].State != "running" {
		t.Fatalf("want resumed session running after walk, got %+v", posts)
	}
}
