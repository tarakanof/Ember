package main

import (
	"encoding/json"
	"testing"
	"time"
)

const metaWithCwd = `{"type":"session_meta","payload":{"id":"u-cwd","source":"cli","originator":"codex-tui","cwd":"/Users/x/Github/learn-language"}}`

func TestWatcher_SendsProjectFromSessionMetaCwd(t *testing.T) {
	for _, send := range []bool{true, false} {
		dir := t.TempDir()
		now := time.Now()
		writeRollout(t, dir, "rollout-cwd.jsonl", now, metaWithCwd, evStarted)
		w := newTestWatcher(dir, now)
		w.cfg.SendProject = send
		posts, _, _ := w.tick()
		want := ""
		if send {
			want = "learn-language"
		}
		if len(posts) != 1 || posts[0].Project != want {
			t.Fatalf("send=%v posts=%+v; want project %q", send, posts, want)
		}
	}
}

func TestAppServer_SendsProjectFromThreadCwd(t *testing.T) {
	cfg := testAppServerConfig("")
	cfg.SendProject = true
	as := newAppServer(cfg)
	var th wireThread
	if err := json.Unmarshal([]byte(`{"id":"t1","source":"cli","originator":"codex-tui","cwd":"/srv/ember/","status":{"type":"systemError"},"updatedAt":`+
		jsonInt(time.Now().Unix())+`}`), &th); err != nil {
		t.Fatal(err)
	}
	as.mu.Lock()
	as.addThreadLocked(th)
	as.mu.Unlock()
	out := as.tick()
	if len(out.posts) != 1 || out.posts[0].Project != "ember" {
		t.Fatalf("posts = %+v; want project ember", out.posts)
	}
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
