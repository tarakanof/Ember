package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
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
	loc := time.FixedZone("test", 2*3600)
	ts, _ := time.Parse(time.RFC3339, "2026-06-05T12:10:00+00:00")
	if got := clockLabel(ts, loc); got != "14:10" {
		t.Errorf("clockLabel = %q, want 14:10", got)
	}
	day, _ := time.Parse(time.RFC3339, "2026-06-08T12:00:00+00:00")
	if got := dayLabel(day, loc); got != "MON" {
		t.Errorf("dayLabel = %q, want MON", got)
	}
}

func TestUsageModelsCache_SetGetReset(t *testing.T) {
	t.Cleanup(usageModels.reset)
	if got := usageModels.get(""); got != nil {
		t.Fatalf("fresh cache should be nil, got %v", got)
	}
	models := map[string]*producer.UsageWindow{"opus": {UsedPercent: 82}}
	usageModels.set("", models)
	got := usageModels.get("")
	if got["opus"] == nil || got["opus"].UsedPercent != 82 {
		t.Errorf("get() = %v, want opus=82", got)
	}
	usageModels.reset()
	if got := usageModels.get(""); got != nil {
		t.Errorf("reset() should clear the cache, got %v", got)
	}
}

func TestFetchUsage_UsesDedicatedTimeoutClient(t *testing.T) {
	if usageHTTPClient.Timeout != 30*time.Second {
		t.Errorf("usageHTTPClient.Timeout = %v, want 30s", usageHTTPClient.Timeout)
	}
}

type usageRoundTripper func(*http.Request) (*http.Response, error)

func (f usageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubUsageBody(t *testing.T, body io.Reader) {
	t.Helper()
	old := usageHTTPClient
	usageHTTPClient = &http.Client{Transport: usageRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { usageHTTPClient = old })
}

type cutReader struct {
	data string
	done bool
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.ErrUnexpectedEOF
	}
	c.done = true
	return copy(p, c.data), nil
}

func TestFetchUsage_TruncatedBodySurfacesReadError(t *testing.T) {
	stubUsageBody(t, &cutReader{data: `{"five_hour":{"utiliz`})
	_, _, err := fetchUsage(context.Background(), "tok")
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestFetchUsage_OversizedBodyIsCapped(t *testing.T) {
	body := strings.NewReader(`{"five_hour":null}` + strings.Repeat(" ", usageMaxBody*2))
	stubUsageBody(t, body)
	_, _, err := fetchUsage(context.Background(), "tok")
	if err == nil {
		t.Fatal("want error for oversized body")
	}
	if read := usageMaxBody*2 + 18 - body.Len(); read > usageMaxBody+1 {
		t.Fatalf("read %d bytes, cap is %d", read, usageMaxBody+1)
	}
}
