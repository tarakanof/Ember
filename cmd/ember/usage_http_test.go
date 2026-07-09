package main

import (
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

func TestUsageHTTP_RejectsOutOfRangeUsedPercent(t *testing.T) {
	cases := []string{
		`{"tool":"claude","five_hour":{"used_percent":-1}}`,
		`{"tool":"claude","five_hour":{"used_percent":101}}`,
		`{"tool":"claude","seven_day":{"used_percent":150}}`,
		`{"tool":"claude","models":{"opus":{"used_percent":-5}}}`,
	}
	for _, body := range cases {
		srv := newRawTestServer(t, discardLogger())
		req := authedRequest(t, "POST", srv.URL+"/v1/usage", body)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
	}
}
