package producer

import (
	"testing"
	"time"
)

func TestRepost(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var r Repost
	if r.Posted() || !r.Due("a", t0) || !r.Posted() {
		t.Fatal("first Due must fire and mark posted")
	}
	if r.Due("a", t0.Add(KeepaliveInterval-time.Second)) {
		t.Fatal("unchanged within the keepalive must not fire")
	}
	if !r.Due("b", t0.Add(time.Second)) {
		t.Fatal("a change must fire")
	}
	if !r.Due("b", t0.Add(time.Second+KeepaliveInterval)) {
		t.Fatal("the keepalive must fire")
	}
	r.Reset()
	if r.Posted() || !r.Due("b", t0.Add(2*time.Second+KeepaliveInterval)) {
		t.Fatal("Reset must forget the last post")
	}
}
