package main

import (
	"testing"
	"time"
)

const chanWaitBound = 5 * time.Second

func waitEntered(t *testing.T, entered <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(chanWaitBound):
		t.Fatalf("%s never started", what)
	}
}

func recvWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(chanWaitBound):
		t.Fatalf("%s: nothing received within %v", what, chanWaitBound)
		var zero T
		return zero
	}
}
