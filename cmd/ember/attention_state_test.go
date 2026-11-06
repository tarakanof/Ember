package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

func TestAttentionStateTransition(t *testing.T) {
	held := attentionState{locked: true, lockedKey: "a/b/s"}
	cases := []struct {
		name             string
		state            attentionState
		key, prior, next string
		want             attentionStep
	}{
		{"running to waiting acquires", attentionState{}, "a/b/s", "running", "waiting", attentionAcquire},
		{"new session in error acquires", attentionState{}, "a/b/s", "", "error", attentionAcquire},
		{"another session steals the lock", held, "a/b/t", "idle", "waiting", attentionAcquire},
		{"waiting heartbeat keeps", held, "a/b/s", "waiting", "waiting", attentionKeep},
		{"waiting to error on the holder renews", held, "a/b/s", "waiting", "error", attentionRenew},
		{"error to waiting on the holder renews", held, "a/b/s", "error", "waiting", attentionRenew},
		{"shift on another session keeps", held, "a/b/t", "waiting", "error", attentionKeep},
		{"shift with no lock keeps", attentionState{}, "a/b/s", "waiting", "error", attentionKeep},
		{"holder leaving attention drains", held, "a/b/s", "error", "running", attentionDrain},
		{"holder idle heartbeat drains", held, "a/b/s", "idle", "idle", attentionDrain},
		{"another session leaving keeps", held, "a/b/t", "waiting", "done", attentionKeep},
		{"running with no lock keeps", attentionState{}, "a/b/s", "idle", "running", attentionKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.onTransition(tc.key, tc.prior, tc.next); got != tc.want {
				t.Fatalf("onTransition(%q, %q, %q) = %v, want %v", tc.key, tc.prior, tc.next, got, tc.want)
			}
		})
	}
}

func TestAttentionStateEnded(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ack := 30 * time.Second
	held := attentionState{locked: true, lockedKey: "a/b/s", lockEnteredAt: t0}
	sess := func(state string) []render.Session {
		return []render.Session{
			{Source: "a", Tool: "b", Session: "t", State: "running"},
			{Source: "a", Tool: "b", Session: "s", State: state},
		}
	}
	cases := []struct {
		name     string
		state    attentionState
		active   []string
		sessions []render.Session
		now      time.Time
		want     attentionEnd
	}{
		{"no lock", attentionState{}, nil, nil, t0.Add(time.Hour), attentionHeld},
		{"still waiting inside the hold", held, []string{"a/b/t", "a/b/s"}, sess("waiting"), t0.Add(ack - time.Second), attentionHeld},
		{"error inside the hold", held, []string{"a/b/s"}, sess("error"), t0, attentionHeld},
		{"hold elapsed", held, []string{"a/b/s"}, sess("waiting"), t0.Add(ack), attentionEndAckTimeout},
		{"holder left attention", held, []string{"a/b/s"}, sess("running"), t0, attentionEndDrain},
		{"drain wins over the timeout", held, []string{"a/b/s"}, sess("done"), t0.Add(time.Hour), attentionEndDrain},
		{"holder no longer active", held, []string{"a/b/t"}, sess("waiting"), t0, attentionEndReap},
		{"reap wins over drain", held, nil, sess("running"), t0.Add(time.Hour), attentionEndReap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.ended(tc.active, tc.sessions, tc.now, ack); got != tc.want {
				t.Fatalf("ended = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAttentionStateLifecycle(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ack := 30 * time.Second
	active := []string{"a/b/s"}
	waiting := []render.Session{{Source: "a", Tool: "b", Session: "s", State: "waiting"}}
	var a attentionState

	a.acquire("a/b/s", t0)
	if !a.holds("a/b/s") || a.holds("a/b/t") {
		t.Fatalf("after acquire: %+v", a)
	}
	a.renew(t0.Add(20 * time.Second))
	if got := a.ended(active, waiting, t0.Add(ack), ack); got != attentionHeld {
		t.Fatalf("renew must restart the hold, got %q", got)
	}
	if got := a.ended(active, waiting, t0.Add(20*time.Second+ack), ack); got != attentionEndAckTimeout {
		t.Fatalf("renewed hold elapsed: got %q", got)
	}
	a.release()
	if a.locked || a.lockedKey != "" || a.holds("a/b/s") {
		t.Fatalf("after release: %+v", a)
	}
	if got := a.ended(active, waiting, t0.Add(time.Hour), ack); got != attentionHeld {
		t.Fatalf("released state ended = %q", got)
	}
}
