package main

import (
	"math"
	"net/http"
	"strings"
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

func TestUsageHTTP_OversizedBodyRejected(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	// Valid JSON but padded well past the cap via an oversized source field,
	// so the rejection actually exercises MaxBytesReader rather than a plain
	// JSON-syntax error.
	body := `{"tool":"claude","source":"` + strings.Repeat("x", 2<<20) + `"}`
	req := authedRequest(t, "POST", srv.URL+"/v1/usage", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized /v1/usage body: code = %d, want 413 or 400", resp.StatusCode)
	}
}

func TestUsageHTTP_RejectsTrailingGarbage(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	body := `{"tool":"claude"}{"x":1}`
	req := authedRequest(t, "POST", srv.URL+"/v1/usage", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("trailing garbage: code = %d, want 400", resp.StatusCode)
	}
}

func TestUsageHTTP_RejectsUnknownField(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	body := `{"tool":"claude","bogus_field":true}`
	req := authedRequest(t, "POST", srv.URL+"/v1/usage", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: code = %d, want 400 (strict decode)", resp.StatusCode)
	}
}

func TestUsageHTTP_RejectsBadToolName(t *testing.T) {
	cases := []string{
		"",
		"Claude",                // uppercase not allowed
		"claude codex",          // space not allowed
		"claude!",               // punctuation not allowed
		strings.Repeat("a", 33), // over 32 chars
	}
	for _, tool := range cases {
		srv := newRawTestServer(t, discardLogger())
		body := `{"tool":"` + tool + `"}`
		req := authedRequest(t, "POST", srv.URL+"/v1/usage", body)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("tool %q: status = %d, want 400", tool, resp.StatusCode)
		}
	}
}

// TestUsageHTTP_ClampsOutOfRangeUsedPercent: the producers post UNCLAMPED
// upstream values, so out-of-[0,100] percents are accepted and clamped in
// place (matching the coordinator's pctInt convention), not rejected.
func TestUsageHTTP_ClampsOutOfRangeUsedPercent(t *testing.T) {
	app, srv := newTestServerWithToken(t, "secret-token")
	auth := map[string]string{"Authorization": "Bearer secret-token"}

	resp := postJSON(t, srv, "/v1/usage", map[string]any{
		"tool":      "claude",
		"five_hour": map[string]any{"used_percent": -1},
		"seven_day": map[string]any{"used_percent": 150},
		"models":    map[string]any{"opus": map[string]any{"used_percent": 101.5}, "sonnet": map[string]any{"used_percent": -5}},
	}, auth)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (out-of-range percents clamp, not reject)", resp.StatusCode)
	}

	u, ok := app.usage.Get("claude")
	if !ok {
		t.Fatal("usage not stored")
	}
	if got := u.FiveHour.UsedPercent; got != 0 {
		t.Errorf("five_hour.used_percent = %v, want 0 (clamped from -1)", got)
	}
	if got := u.SevenDay.UsedPercent; got != 100 {
		t.Errorf("seven_day.used_percent = %v, want 100 (clamped from 150)", got)
	}
	if got := u.Models["opus"].UsedPercent; got != 100 {
		t.Errorf("models.opus.used_percent = %v, want 100 (clamped from 101.5)", got)
	}
	if got := u.Models["sonnet"].UsedPercent; got != 0 {
		t.Errorf("models.sonnet.used_percent = %v, want 0 (clamped from -5)", got)
	}
}

// JSON has no NaN literal, so a NaN used_percent can only arrive as invalid
// JSON — the strict decoder rejects it at parse time with 400. The clamp
// helper additionally normalizes an in-process NaN to 0 as defense in depth.
func TestUsageHTTP_NaNUsedPercent(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())
	req := authedRequest(t, "POST", srv.URL+"/v1/usage", `{"tool":"claude","five_hour":{"used_percent":NaN}}`)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("NaN literal: status = %d, want 400 (invalid JSON)", resp.StatusCode)
	}

	win := &UsageWindow{UsedPercent: math.NaN()}
	clampUsageWindow(win)
	if win.UsedPercent != 0 {
		t.Errorf("clampUsageWindow(NaN) = %v, want 0", win.UsedPercent)
	}
}
