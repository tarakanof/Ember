package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func shownSince(th thread, since time.Time) bool {
	state, _, ok := mapThread(th)
	return ok && (state == "running" || state == "waiting" || !th.ChangedAt.Before(since))
}

func scopedRead(t *testing.T, home string, scope readScope) map[string]thread {
	t.Helper()
	path := filepath.Join(home, "userdata", "statev2.sqlite")
	db, err := openReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err := readTables(context.Background(), db, path, 2, v2Query, scope)
	if err != nil {
		t.Fatalf("scoped read since %v: %v", scope.since, err)
	}
	return byID(snap.Threads)
}

func checkScopedMatchesFull(t *testing.T, home string, full snapshot) {
	t.Helper()
	if full.Migration < t3AwarenessMigration {
		return
	}
	want := byID(full.Threads)
	for _, since := range []time.Time{
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 10, 3, 0, 0, time.UTC),
		time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		var shown []string
		for id, th := range want {
			if shownSince(th, since) {
				shown = append(shown, id)
			}
		}
		for _, keep := range [][]string{nil, shown} {
			got := scopedRead(t, home, readScope{since: since, keep: keep})
			for id, g := range got {
				if !reflect.DeepEqual(g, want[id]) {
					t.Errorf("since %v keep %d: scoped row %s = %+v, full row %+v", since, len(keep), id, g, want[id])
				}
			}
			if keep == nil && !since.Equal(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
				continue
			}
			for _, id := range shown {
				if _, ok := got[id]; !ok {
					t.Errorf("since %v keep %d: shown thread %s missing from the scoped read", since, len(keep), id)
				}
			}
		}
	}
}

func TestScopedQueryReadsOnlyThreadsThatCanShow(t *testing.T) {
	var f v2Fixture
	old := func(id, payload string) {
		f.thread(id, payload)
		f.run(id+"-r", id, 1, "completed", done, `{}`)
	}
	recent := "2026-11-01T12:01:00.000Z"
	old("old-done", `{}`)
	f.thread("old-failed", `{}`)
	f.run("old-failed-r", "old-failed", 1, "failed", done, `{}`)
	old("old-archived", `{"archivedAt":"2026-11-01T10:06:00.000Z"}`)
	old("old-pr-merged", `{"pullRequests":[{"number":2,"source":"manual","watch":{},"snapshot":{"state":"merged"}}]}`)
	old("old-pr-unkept", `{"pullRequests":[{"number":3,"source":"manual","watch":{}}]}`)
	f.thread("sub", `{"lineage":{"relationshipToParent":"subagent","parentThreadId":"top"},"creationSource":"mcp"}`)
	f.run("sub-r", "sub", 1, "running", "", `{}`)
	f.request("sub-q", "sub", "command", "2026-11-01T10:06:00.000Z", `{}`)
	f.thread("side", `{"lineage":{"sideChat":true}}`)
	f.run("side-r", "side", 1, "running", "", `{}`)
	old("top", `{}`)
	old("kept-pr", `{"pullRequests":[{"number":1,"source":"manual","watch":{}}]}`)
	old("kept-roster", `{}`)
	f.roster("kr-pt", "kept-roster", `[{"taskId":"b","kind":"background_task"}]`)
	old("touched-pr", `{"pullRequests":[{"number":4,"source":"manual","watch":{}}]}`)
	f.stmts = append(f.stmts, `UPDATE orchestration_v2_projection_threads SET updated_at = '`+recent+`' WHERE thread_id = 'touched-pr'`)
	old("touched-roster", `{}`)
	f.roster("tr-pt", "touched-roster", `[{"taskId":"b","kind":"background_task"}]`)
	old("untouched-roster", `{}`)
	f.roster("ur-pt", "untouched-roster", `[{"taskId":"b","kind":"background_task"}]`)
	old("held-item", `{}`)
	f.item("hi-i", "held-item", "held-item-r", "n", "dynamic_tool", "running", done, `{}`)
	f.thread("recent-done", `{}`)
	f.run("recent-done-r", "recent-done", 1, "completed", recent, `{}`)
	f.stmts = append(f.stmts, `UPDATE orchestration_v2_projection_threads SET updated_at = '`+recent+`' WHERE thread_id IN ('touched-roster', 'recent-done')`)
	old("pending", `{}`)
	f.request("pending-q", "pending", "approval", "2026-11-01T10:06:00.000Z", `{}`)
	old("link", `{}`)
	f.item("link-i", "link", "link-r", "n", "link_request", "waiting", "2026-11-01T10:06:00.000Z", `{}`)
	f.thread("running", `{}`)
	f.run("running-r", "running", 1, "running", "", `{}`)
	old("kept", `{}`)

	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql", append([]string{`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (81, 'x')`}, f.stmts...)...)
	full, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	checkScopedMatchesFull(t, home, full)

	got := scopedRead(t, home, readScope{since: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), keep: []string{"kept", "kept-pr", "kept-roster"}})
	for _, id := range []string{"top", "kept", "kept-pr", "kept-roster", "touched-pr", "touched-roster", "held-item", "recent-done", "pending", "link", "running"} {
		if _, ok := got[id]; !ok {
			t.Errorf("%s missing from the scoped read", id)
		}
	}
	for _, id := range []string{"old-done", "old-failed", "old-archived", "old-pr-merged", "old-pr-unkept", "untouched-roster", "sub", "side"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s read although it cannot show", id)
		}
	}
	running := mapped{"running", "", true}
	for _, id := range []string{"kept-pr", "kept-roster", "touched-pr", "touched-roster", "held-item"} {
		if m := mappedOf(got[id]); m != running {
			t.Errorf("%s = %+v, want running", id, m)
		}
	}
	if m := mappedOf(got["top"]); m != (mapped{"waiting", "approve command", true}) {
		t.Errorf("top = %+v, want the subagent's approval", m)
	}
	for _, id := range []string{"old-pr-unkept", "untouched-roster"} {
		if m := mappedOf(byID(full.Threads)[id]); m != running {
			t.Errorf("%s in the full read = %+v, want running (the full read shows it)", id, m)
		}
	}
}

func TestStoreReaderSkipsAnUnchangedDatabaseAndSeesEveryCommit(t *testing.T) {
	home := t.TempDir()
	var f v2Fixture
	f.thread("t", `{}`)
	f.run("t-r", "t", 1, "running", "", `{}`)
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql", append([]string{`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (81, 'x')`}, f.stmts...)...)
	w, err := sql.Open("sqlite", filepath.Join(home, "userdata", "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}

	r := &storeReader{}
	defer r.Close()
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	read := func() snapshot {
		t.Helper()
		at = at.Add(2 * time.Second)
		snap, err := r.ReadScoped(context.Background(), home, readScope{now: at, since: at.Add(-5 * time.Minute), keep: []string{"t"}})
		if err != nil || len(snap.Threads) != 1 {
			t.Fatalf("read: threads=%d err=%v", len(snap.Threads), err)
		}
		return snap
	}
	first := read()
	if again := read(); &again.Threads[0] != &first.Threads[0] {
		t.Fatal("an unchanged database was queried again")
	}
	for i := 0; i < 40; i++ {
		title := fmt.Sprintf("v%03d", i)
		if _, err := w.Exec(`UPDATE orchestration_v2_projection_threads SET title = ? WHERE thread_id = 't'`, title); err != nil {
			t.Fatal(err)
		}
		if i%7 == 3 {
			if _, err := w.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
		}
		if got := read().Threads[0].Title; got != title {
			t.Fatalf("commit %d: title = %q, want %q", i, got, title)
		}
	}

	last := read()
	snap, err := r.ReadScoped(context.Background(), home, readScope{now: at, since: at.Add(-5 * time.Minute), keep: []string{"t", "unseen"}})
	if err != nil {
		t.Fatal(err)
	}
	if &snap.Threads[0] == &last.Threads[0] {
		t.Fatal("a keep id the cached read never covered must re-query")
	}
	if snap, err = r.ReadScoped(context.Background(), home, readScope{now: at, since: at.Add(-time.Hour), keep: []string{"t"}}); err != nil || &snap.Threads[0] == &last.Threads[0] {
		t.Fatalf("an earlier cutoff must re-query (err %v)", err)
	}
}

func seedScale(t *testing.T, home string, settled int) {
	t.Helper()
	pad := strings.Repeat("x", 1200)
	gen := fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < %d) `, settled)
	id := `printf('settled-%05d', i)`
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (81, 'x')`,
		gen+`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, payload_json, provider_instance_id)
		 SELECT `+id+`, 'p', `+id+`, 'codex', 'full-access', 'default', '2026-01-01T10:00:00.000Z', '2026-01-02T10:00:00.000Z',
		   '{"pullRequests":[{"number":1,"source":"manual","snapshot":{"state":"merged","title":"`+pad+`"}}],"worktreePath":"/w/' || `+id+` || '"}', 'codex' FROM n`,
		gen+`, o(k) AS (VALUES (1), (2)) INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, completed_at, payload_json)
		 SELECT `+id+` || '-r' || k, `+id+`, k, 'codex', 'completed', '2026-01-01T10:00:00.000Z', '2026-01-01T10:05:00.000Z', '{}' FROM n, o`,
		gen+`, o(k) AS (VALUES (1), (2)), y(j, typ) AS (VALUES (0, 'user_message'), (1, 'assistant_message'), (2, 'command_execution'))
		 INSERT INTO orchestration_v2_projection_turn_items (turn_item_id, thread_id, run_id, node_id, ordinal, type, status, updated_at, payload_json)
		 SELECT `+id+` || '-r' || k || '-i' || j, `+id+`, `+id+` || '-r' || k, 'n', j, typ, 'completed', '2026-01-01T10:05:00.000Z', '{}' FROM n, o, y`,
		gen+`INSERT INTO orchestration_v2_projection_provider_threads (provider_thread_id, thread_id, provider, status, updated_at, payload_json)
		 SELECT `+id+` || '-pt', `+id+`, 'claudeAgent', 'idle', '2026-01-01T10:05:00.000Z', '{"pendingBackgroundTasks":[]}' FROM n`,
		`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i + 1 < 5)
		 INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, payload_json, provider_instance_id)
		 SELECT 'active-' || i, 'p', 'active-' || i, 'codex', 'full-access', 'default', '2026-11-16T11:59:00.000Z', '2026-11-16T11:59:00.000Z', '{}', 'codex' FROM n`,
		`INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, payload_json)
		 SELECT thread_id || '-r', thread_id, 1, 'codex', 'running', '2026-11-16T11:59:00.000Z', '{}' FROM orchestration_v2_projection_threads WHERE thread_id LIKE 'active-%'`,
	)
}

func scopedPollCost(t *testing.T, settled int) time.Duration {
	t.Helper()
	home := t.TempDir()
	seedScale(t, home, settled)
	path := filepath.Join(home, "userdata", "statev2.sqlite")
	db, err := openReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := readScope{since: time.Date(2026, 11, 16, 11, 55, 0, 0, time.UTC)}
	best := time.Duration(1 << 62)
	for i := 0; i < 9; i++ {
		start := time.Now()
		snap, err := readTables(context.Background(), db, path, 2, v2Query, scope)
		if elapsed := time.Since(start); elapsed < best {
			best = elapsed
		}
		if err != nil || len(snap.Threads) != 5 {
			t.Fatalf("settled %d: threads=%d err=%v", settled, len(snap.Threads), err)
		}
	}
	return best
}

func TestScopedPollCostDoesNotGrowWithSettledThreads(t *testing.T) {
	small, large := scopedPollCost(t, 500), scopedPollCost(t, 4000)
	t.Logf("500 settled: %v, 4000 settled: %v", small, large)
	if large >= 2*small {
		t.Fatalf("4000 settled threads cost %v, 500 cost %v: want under 2x", large, small)
	}
}

func TestStartCacheLooksUpEachPidOnce(t *testing.T) {
	lookups := map[int]int{}
	c := &startCache{lookup: func(pid int) (time.Time, bool) {
		lookups[pid]++
		return time.Now().Add(-time.Hour), true
	}}
	self, parent := os.Getpid(), os.Getppid()
	for i := 0; i < 5; i++ {
		if !c.alive(self, time.Now()) {
			t.Fatal("own pid not alive")
		}
	}
	if lookups[self] != 1 {
		t.Fatalf("start time looked up %d times for one pid, want 1", lookups[self])
	}
	if !c.alive(parent, time.Now()) || lookups[parent] != 1 {
		t.Fatalf("a new pid must be looked up (lookups %v)", lookups)
	}
	if c.alive(-1, time.Now()) || c.alive(1<<30, time.Now()) {
		t.Fatal("invalid or dead pid alive")
	}
	if !c.alive(parent, time.Now()) || lookups[parent] != 2 {
		t.Fatalf("a pid seen again after a dead one must be looked up again (lookups %v)", lookups)
	}
	stale := &startCache{lookup: func(int) (time.Time, bool) { return time.Now(), true }}
	if stale.alive(self, time.Now().Add(-time.Hour)) {
		t.Fatal("a process that started after the recorded startedAt was accepted")
	}
}

func TestPollKeepsAThreadTheWatcherShowsAfterItsHoldEnds(t *testing.T) {
	srv, got := recordingServer(t)
	home := t.TempDir()
	var f v2Fixture
	f.thread("th-1", `{}`)
	f.run("r1", "th-1", 1, "running", "", `{}`)
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql", append([]string{`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (81, 'x')`}, f.stmts...)...)
	cfg := Config{Common: producer.Common{Source: "mbp"}, T3Home: home, StateDir: t.TempDir(), ActivityWindowSeconds: 300}
	d := newDaemon(cfg, producer.NewClient(srv.URL, "tok", time.Second))
	defer d.store.Close()
	d.alive = func(string) bool { return true }
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	last := func() recorded {
		t.Helper()
		calls := got()
		if len(calls) == 0 {
			t.Fatal("no calls")
		}
		return calls[len(calls)-1]
	}

	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := last(); c.method != http.MethodPost || c.body["state"] != "running" {
		t.Fatalf("first poll = %+v, want running", c)
	}
	w, err := sql.Open("sqlite", filepath.Join(home, "userdata", "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Exec(`UPDATE orchestration_v2_projection_runs SET status = 'completed', completed_at = ? WHERE run_id = 'r1'`, done); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := last(); c.method != http.MethodPost || c.body["state"] != "done" {
		t.Fatalf("after the run settled = %+v, want done", c)
	}
	now = now.Add(6 * time.Minute)
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := last(); c.method != http.MethodDelete {
		t.Fatalf("after the window = %+v, want DELETE", c)
	}
}

func TestPollFindsUntouchedHoldsOnTheFullRead(t *testing.T) {
	srv, got := recordingServer(t)
	home := t.TempDir()
	var f v2Fixture
	f.thread("held", `{"pullRequests":[{"number":1,"source":"manual","watch":{}}]}`)
	f.run("held-r", "held", 1, "completed", done, `{}`)
	f.thread("later", `{"pullRequests":[{"number":2,"source":"manual"}]}`)
	f.run("later-r", "later", 1, "completed", done, `{}`)
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql", append([]string{`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (81, 'x')`}, f.stmts...)...)
	cfg := Config{Common: producer.Common{Source: "mbp"}, T3Home: home, StateDir: t.TempDir(), ActivityWindowSeconds: 300}
	d := newDaemon(cfg, producer.NewClient(srv.URL, "tok", time.Second))
	defer d.store.Close()
	d.alive = func(string) bool { return true }
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	running := func() map[string]bool {
		out := map[string]bool{}
		for _, c := range got() {
			if c.method == http.MethodPost && c.body["state"] == "running" {
				out[c.body["session"].(string)] = true
			}
		}
		return out
	}
	poll := func(after time.Duration) {
		t.Helper()
		now = now.Add(after)
		if err := d.poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	poll(0)
	if !running()["held"] {
		t.Fatalf("the first read must show a thread held since long ago: %+v", got())
	}
	w, err := sql.Open("sqlite", filepath.Join(home, "userdata", "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Exec(`UPDATE orchestration_v2_projection_threads SET payload_json = '{"pullRequests":[{"number":2,"source":"manual","watch":{}}]}' WHERE thread_id = 'later'`); err != nil {
		t.Fatal(err)
	}
	poll(2 * time.Second)
	if running()["later"] {
		t.Fatal("a scoped read cannot see a watch added without an updated_at bump; the test's premise is wrong")
	}
	poll(fullReadEvery)
	if !running()["later"] {
		t.Fatalf("the next full read must show the new hold: %+v", got())
	}
	if c := got()[len(got())-1]; c.method == http.MethodDelete {
		t.Fatalf("held thread dropped: %+v", got())
	}
}
