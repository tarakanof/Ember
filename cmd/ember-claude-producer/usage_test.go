package main

import (
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

func TestParseUsageResponse(t *testing.T) {
	body := []byte(`{"five_hour":{"utilization":15.0,"resets_at":"2026-06-05T12:10:00.005938+00:00"},
	                 "seven_day":{"utilization":58.0,"resets_at":"2026-06-08T12:00:00+00:00"},
	                 "seven_day_opus":{"utilization":82.0,"resets_at":"2026-06-08T12:00:00+00:00"},
	                 "seven_day_sonnet":{"utilization":12.0,"resets_at":"2026-06-08T12:00:00+00:00"},
	                 "extra_usage":{"is_enabled":false}}`)
	u, err := parseUsageResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if u.FiveHour.Utilization != 15.0 {
		t.Errorf("5h pct = %v", u.FiveHour.Utilization)
	}
	if u.SevenDayOpus.Utilization != 82.0 {
		t.Errorf("opus = %v", u.SevenDayOpus.Utilization)
	}
}

func TestResetLabels(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)                          // UTC+2
	ts, _ := time.Parse(time.RFC3339, "2026-06-05T12:10:00+00:00") // 14:10 local
	if got := clockLabel(ts, loc); got != "14:10" {
		t.Errorf("clockLabel = %q, want 14:10", got)
	}
	day, _ := time.Parse(time.RFC3339, "2026-06-08T12:00:00+00:00") // a Monday
	if got := dayLabel(day, loc); got != "MON" {
		t.Errorf("dayLabel = %q, want MON", got)
	}
}

// TestUsageModelsCache_SetGetReset covers the cross-tick cache that lets the
// statusline-driven /v1/usage POST (dispatchTick) carry forward the OAuth
// poller's per-model breakdown, since Claude Code's statusline JSON has no
// per-model figures of its own.
func TestUsageModelsCache_SetGetReset(t *testing.T) {
	t.Cleanup(usageModels.reset)
	if got := usageModels.get(); got != nil {
		t.Fatalf("fresh cache should be nil, got %v", got)
	}
	models := map[string]*producer.UsageWindow{"opus": {UsedPercent: 82}}
	usageModels.set(models)
	got := usageModels.get()
	if got["opus"] == nil || got["opus"].UsedPercent != 82 {
		t.Errorf("get() = %v, want opus=82", got)
	}
	usageModels.reset()
	if got := usageModels.get(); got != nil {
		t.Errorf("reset() should clear the cache, got %v", got)
	}
}

// Note: usagePollOnce itself is not unit-tested end-to-end here — it shells
// out to `security find-generic-password` for the real Keychain item before
// falling back to a file, so a test run on a machine with genuine Claude Code
// credentials installed could accidentally hit the real (network) OAuth usage
// endpoint. The cache-population line (usageModels.set(req.Models)) is a
// single statement right next to the existing, already-tested req-building
// code above; TestDispatchTick_UsagePost_MergesModelsCache in tick_test.go
// covers the consuming side by setting the cache directly.

// TestFetchUsage_UsesDedicatedTimeoutClient guards against fetchUsage falling
// back to http.DefaultClient (no Timeout, so one stalled TLS connection hangs
// the usage widget until the daemon restarts).
func TestFetchUsage_UsesDedicatedTimeoutClient(t *testing.T) {
	if usageHTTPClient.Timeout != 30*time.Second {
		t.Errorf("usageHTTPClient.Timeout = %v, want 30s", usageHTTPClient.Timeout)
	}
}
