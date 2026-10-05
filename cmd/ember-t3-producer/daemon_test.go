package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type recorded struct {
	method string
	body   map[string]any
}

func recordingServer(t *testing.T) (*httptest.Server, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var got []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, recorded{r.Method, body})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), got...)
	}
}

func TestPollPostsThenDeletesWhenServerStops(t *testing.T) {
	srv, got := recordingServer(t)
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql",
		`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (56, 'RemoveRedundantProjectionIndexes')`,
		`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, payload_json) VALUES ('th-1', 'p', 'Fix login', 'codex', 'full-access', 'default', '2026-10-02T10:00:00.000Z', '2026-10-02T10:00:00.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, payload_json) VALUES ('r1', 'th-1', 1, 'codex', 'running', '2026-10-02T10:01:00.000Z', '{}')`,
	)
	stateDir := t.TempDir()
	cfg := Config{Common: producer.Common{Source: "mbp", ActivityTrailEnabled: true}, T3Home: home, StateDir: stateDir, ActivityWindowSeconds: 300}
	d := newDaemon(cfg, producer.NewClient(srv.URL, "tok", time.Second))
	alive := true
	d.alive = func(string) bool { return alive }

	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := got()
	if len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].body["tool"] != "t3" || calls[0].body["session"] != "th-1" || calls[0].body["activity"] != "Fix login" {
		t.Fatalf("calls = %+v", calls)
	}
	if _, err := os.Stat(markerPath(stateDir, "th-1")); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	alive = false
	if err := d.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls = got()
	if len(calls) != 2 || calls[1].method != http.MethodDelete || calls[1].body["session"] != "th-1" {
		t.Fatalf("calls after T3 stopped = %+v", calls)
	}
	if _, err := os.Stat(markerPath(stateDir, "th-1")); !os.IsNotExist(err) {
		t.Fatal("marker not removed")
	}
}

func TestPollUnknownSchemaKeepsSessionsAndReportsError(t *testing.T) {
	srv, got := recordingServer(t)
	home := t.TempDir()
	makeDB(t, home, "statev2.sqlite", "schema_v1.sql")
	d := newDaemon(Config{Common: producer.Common{Source: "mbp"}, T3Home: home, StateDir: t.TempDir()}, producer.NewClient(srv.URL, "", time.Second))
	d.alive = func(string) bool { return true }
	err := d.poll(context.Background())
	if !errors.Is(err, errUnknownSchema) {
		t.Fatalf("err = %v, want errUnknownSchema", err)
	}
	if len(got()) != 0 {
		t.Fatal("an unreadable schema must not post or delete anything")
	}
}

func TestBackoff(t *testing.T) {
	base := 2 * time.Second
	if got := backoff(base, 0); got != base {
		t.Fatalf("backoff(0) = %v", got)
	}
	if got := backoff(base, 1); got != 4*time.Second {
		t.Fatalf("backoff(1) = %v", got)
	}
	if got := backoff(base, 30); got != maxBackoff {
		t.Fatalf("backoff(30) = %v, want cap %v", got, maxBackoff)
	}
}

func TestNoteMigrationWarnsOncePerUntestedVersion(t *testing.T) {
	d := newDaemon(Config{}, nil)
	if d.noteMigration(2, 56) {
		t.Fatal("pinned v2 migration must not warn")
	}
	if d.noteMigration(1, 54) {
		t.Fatal("pinned v1 migration must not warn")
	}
	if !d.noteMigration(2, 60) {
		t.Fatal("newer migration must warn")
	}
	if d.noteMigration(2, 60) {
		t.Fatal("same migration must warn only once")
	}
}

func TestNoteMigrationKeyedBySchema(t *testing.T) {
	d := newDaemon(Config{}, nil)
	// The same id above both pins is a distinct warning per schema.
	if !d.noteMigration(1, 60) {
		t.Fatal("v1 migration 60 must warn")
	}
	if !d.noteMigration(2, 60) {
		t.Fatal("v2 migration 60 must warn even after v1's")
	}
	if d.noteMigration(1, 60) || d.noteMigration(2, 60) {
		t.Fatal("each (schema, id) must warn only once")
	}
}
