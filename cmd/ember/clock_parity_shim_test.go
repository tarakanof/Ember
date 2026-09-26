package main

import "testing"

func parityApp(t *testing.T, cfg Config) *App {
	t.Helper()
	pub, _ := NewHTTPPublisher()
	return NewApp(cfg, pub, testLogger())
}
