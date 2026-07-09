package main

import (
	"net/http"
	"testing"
)

func TestUsageHTTPStoresPayload(t *testing.T) {
	app, srv := newTestServerWithToken(t, "secret-token")
	resp := postJSON(t, srv, "/v1/usage", map[string]any{
		"tool":      "claude",
		"source":    "endpoint",
		"five_hour": map[string]any{"used_percent": 15, "resets_at": 1780669527, "reset_label": "14:25"},
		"seven_day": map[string]any{"used_percent": 58, "reset_label": "MON"},
	}, map[string]string{"Authorization": "Bearer secret-token"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	u, ok := app.usage.Get("claude")
	if !ok || u.FiveHour == nil || u.FiveHour.ResetLabel != "14:25" || u.SevenDay.UsedPercent != 58 {
		t.Errorf("stored = %+v ok=%v", u, ok)
	}
}

func TestUsageHTTPRequiresAuth(t *testing.T) {
	_, srv := newTestServerWithToken(t, "secret-token")
	resp := postJSON(t, srv, "/v1/usage", map[string]any{"tool": "claude"}, map[string]string{}) // no Authorization header
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
