package nowplaying

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestControlKeysAreScopedPerCaller(t *testing.T) {
	var k ControlKeys
	now := time.Now()
	if !k.Claim("device:a", "k", now) || !k.Claim("owner", "k", now) {
		t.Fatal("same key from two callers must both pass")
	}
	if k.Claim("owner", "k", now.Add(controlKeyTTL-time.Second)) {
		t.Fatal("duplicate within the TTL passed")
	}
	if !k.Claim("owner", "k", now.Add(controlKeyTTL)) {
		t.Fatal("key not forgotten after the TTL")
	}
	k.Claim("device:knob", "press-1", now)
	for i := range controlKeysPerCaller * 3 {
		k.Claim("owner", "x"+strconv.Itoa(i), now)
	}
	if len(k.callers["owner"]) > controlKeysPerCaller {
		t.Fatalf("keys unbounded: %d", len(k.callers["owner"]))
	}
	if k.Claim("device:knob", "press-1", now.Add(time.Minute)) {
		t.Fatal("another caller's burst evicted the knob's key")
	}
	for i := range controlCallersMax * 2 {
		k.Claim("c"+strconv.Itoa(i), "k", now)
	}
	if len(k.callers) > controlCallersMax {
		t.Fatalf("callers unbounded: %d", len(k.callers))
	}
}

func TestCommandQueueCoalescesVolumeAndDropsStale(t *testing.T) {
	q := newCommandQueue()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	q.clock = func() time.Time { return now }
	if err := q.Push("M4", ControlRequest{Action: ActNext}); err != ErrNoController {
		t.Fatalf("no poll yet: %v", err)
	}
	_, _ = q.Take(context.Background(), "M4", 0, nil)
	for _, d := range []int{3, 3, -1} {
		if err := q.Push("M4", ControlRequest{Action: ActVolume, Delta: d}); err != nil {
			t.Fatal(err)
		}
	}
	cmds, _ := q.Take(context.Background(), "M4", 0, nil)
	if len(cmds) != 1 || cmds[0].Delta != 5 {
		t.Fatalf("coalesced = %+v, want one volume +5", cmds)
	}
	_ = q.Push("M4", ControlRequest{Action: actPlayPause})
	now = now.Add(commandTTL)
	if cmds, _ := q.Take(context.Background(), "M4", 0, nil); len(cmds) != 0 {
		t.Fatalf("stale command delivered: %+v", cmds)
	}
	now = now.Add(CommandListenGrace + time.Second)
	if err := q.Push("M4", ControlRequest{Action: ActNext}); err != ErrNoController {
		t.Fatalf("Mac silent past the grace: %v", err)
	}
}

func TestCommandsCarryTheirAge(t *testing.T) {
	q := newCommandQueue()
	now := time.Now()
	q.clock = func() time.Time { return now }
	_, _ = q.Take(context.Background(), "M4", 0, nil)
	_ = q.Push("M4", ControlRequest{Action: ActNext})
	now = now.Add(1200 * time.Millisecond)
	cmds, _ := q.Take(context.Background(), "M4", 0, nil)
	if len(cmds) != 1 || cmds[0].AgeMS != 1200 {
		t.Fatalf("cmds = %+v", cmds)
	}
}
