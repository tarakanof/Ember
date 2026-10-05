package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestRunOnce_PostsAndDeletes(t *testing.T) {
	var posts, deletes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			atomic.AddInt32(&posts, 1)
		case http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, nil, client)
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("want 1 POST, got %d", posts)
	}
	w.now = func() time.Time { return now.Add(200 * time.Second) }
	runOnce(context.Background(), w, nil, client)
	if atomic.LoadInt32(&deletes) != 1 {
		t.Fatalf("want 1 DELETE, got %d", deletes)
	}
}

func TestRunOnce_WritesAndRemovesMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, nil, client)
	marker := filepath.Join(w.cfg.StateDir, "u-123.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not written after POST: %v", err)
	}

	w.now = func() time.Time { return now.Add(200 * time.Second) }
	runOnce(context.Background(), w, nil, client)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker should be removed after reap, stat err = %v", err)
	}
}

func TestRunOnce_WarnsOnPostFailure(t *testing.T) {
	daemonFailLog.Reset()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	client := producer.NewClient(srv.URL, "", time.Second)

	runOnce(context.Background(), w, nil, client)
	if !strings.Contains(buf.String(), "kind=codex_post") {
		t.Errorf("expected a throttled codex_post warning, got: %s", buf.String())
	}
}

// connectedAppServer is an app-server source with one loaded thread, as the
// bootstrap leaves it.
func connectedAppServer(cfg Config, id string, status wireStatus) *appServer {
	as := newAppServer(cfg)
	as.mu.Lock()
	defer as.mu.Unlock()
	as.connected = true
	as.addThreadLocked(wireThread{ID: id, Source: json.RawMessage(`"cli"`), Status: status})
	return as
}

func TestCycle_AppServerWinsForItsThreads(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted) // id u-123
	writeRollout(t, dir, "rollout-vscode.jsonl", now, metaVSCode, evStarted)
	w := newTestWatcher(dir, now)
	as := connectedAppServer(w.cfg, "u-123", wireStatus{Type: "active", ActiveFlags: []string{"waitingOnApproval"}})

	posts, deletes, _ := cycle(w, as)
	got := map[string][]string{}
	for _, p := range posts {
		got[p.Session] = append(got[p.Session], p.State)
	}
	if len(got["u-123"]) != 1 || got["u-123"][0] != "waiting" {
		t.Errorf("u-123 posts = %v, want one from the app-server (waiting)", got["u-123"])
	}
	if len(got["u-vs"]) != 1 {
		t.Errorf("the watcher must keep covering other sessions, got %v", got)
	}
	if len(deletes) != 0 {
		t.Errorf("deletes = %+v", deletes)
	}
}

func TestCycle_DisconnectHandsSessionsBackToTheWatcher(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
	w := newTestWatcher(dir, now)
	as := connectedAppServer(w.cfg, "u-123", wireStatus{Type: "active"})
	as.mu.Lock()
	as.addThreadLocked(wireThread{ID: "t-gone", Source: json.RawMessage(`"cli"`), Status: wireStatus{Type: "active"}})
	as.mu.Unlock()
	cycle(w, as)

	as.disconnect()
	posts, deletes, _ := cycle(w, as)
	if p, ok := postFor(posts, "u-123"); !ok || p.State != "running" {
		t.Errorf("watcher did not take over u-123: %+v", posts)
	}
	var del []string
	for _, d := range deletes {
		del = append(del, d.Session)
	}
	if len(del) != 1 || del[0] != "t-gone" {
		t.Errorf("deletes = %v, want only t-gone (no live rollout)", del)
	}
}

func TestCycle_UsageTakesTheNewerAppServerSnapshot(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	reset := now.Add(time.Hour).Unix()
	tok := `{"timestamp":"2020-01-01T00:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":10,"resets_at":` + strconv.FormatInt(reset, 10) + `}}}}`
	writeRollout(t, dir, "rollout-vscode.jsonl", now, metaVSCode, evStarted, tok)
	w := newTestWatcher(dir, now)
	w.cfg.RatePctEnabled = true
	as := connectedAppServer(w.cfg, "t1", wireStatus{Type: "active"})
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":55,"resetsAt":`+strconv.FormatInt(reset, 10)+`},"secondary":null}}`))
	_, _, usages := cycle(w, as)
	if len(usages) != 1 || usages[0].FiveHour == nil || usages[0].FiveHour.UsedPercent != 55 {
		t.Fatalf("usage = %+v, want the app-server's 55%%", usages)
	}
	// A different meter never replaces the plan's windows.
	as.onNotification("account/rateLimits/updated", json.RawMessage(`{"rateLimits":{"limitId":"premium","primary":{"usedPercent":99,"resetsAt":`+strconv.FormatInt(reset, 10)+`}}}`))
	if tk := as.tick(); tk.rate == nil || *tk.rate.rateWindowPct != 55 {
		t.Errorf("premium meter leaked into the codex windows: %+v", tk.rate)
	}
}

// The watcher posted a session before the app-server connected and owned it.
func TestCycle_HandoverFromWatcherLeavesNoGhost(t *testing.T) {
	cases := []struct {
		name       string
		status     wireStatus
		preview    string
		updatedAt  int64
		wantDelete bool
	}{
		{"active: app-server posts it", wireStatus{Type: "active"}, "", 0, false},
		{"no turn yet: nobody posts it", wireStatus{Type: "idle"}, "", 0, true},
		{"idle past the window", wireStatus{Type: "idle"}, "hi", time.Now().Add(-time.Hour).Unix(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			writeRollout(t, dir, "rollout-cli.jsonl", now, metaCLI, evStarted)
			w := newTestWatcher(dir, now)
			if posts, _, _ := cycle(w, nil); len(posts) != 1 {
				t.Fatalf("watcher did not post first: %+v", posts)
			}
			as := newAppServer(w.cfg)
			as.mu.Lock()
			as.connected = true
			as.addThreadLocked(wireThread{ID: "u-123", Source: json.RawMessage(`"cli"`), Status: c.status, Preview: c.preview, UpdatedAt: c.updatedAt})
			as.mu.Unlock()
			posts, deletes, _ := cycle(w, as)
			deleted := false
			for _, d := range deletes {
				deleted = deleted || d.Session == "u-123"
			}
			_, posted := postFor(posts, "u-123")
			if deleted != c.wantDelete || posted == c.wantDelete {
				t.Fatalf("posted=%v deleted=%v, want deleted=%v", posted, deleted, c.wantDelete)
			}
			if _, deletes, _ := cycle(w, as); len(deletes) != 0 {
				t.Errorf("repeat DELETE: %+v", deletes)
			}
		})
	}
}
