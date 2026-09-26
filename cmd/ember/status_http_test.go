package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusRequestNormalizesDefaults(t *testing.T) {
	session := StatusRequest{}.normalized()

	if session.Source != "unknown" {
		t.Fatalf("Source = %q, want unknown", session.Source)
	}
	if session.Tool != "ai" {
		t.Fatalf("Tool = %q, want ai", session.Tool)
	}
	if session.Session != "default" {
		t.Fatalf("Session = %q, want default", session.Session)
	}
	if session.State != "idle" {
		t.Fatalf("State = %q, want idle (empty input should default)", session.State)
	}
}

func TestPostStatusRejectsMissingFields(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty source", map[string]any{"source": "", "tool": "claude", "session": "x", "state": "running"}},
		{"empty tool", map[string]any{"source": "dt-mbp", "tool": "", "session": "x", "state": "running"}},
		{"empty session", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "", "state": "running"}},
		{"empty state", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "x", "state": ""}},
		{"unknown state", map[string]any{"source": "dt-mbp", "tool": "claude", "session": "x", "state": "potato"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, srv, "/v1/status", c.body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestPostStatusAcceptsValidRequest(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source":  "dt-mbp",
		"tool":    "claude",
		"session": "awtrix",
		"state":   "running",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDeleteStatusRemovesSession(t *testing.T) {
	app, srv := newTestServer(t, defaultConfig())
	app.Upsert(StatusRequest{Source: "dt-mbp", Tool: "claude", Session: "x", State: "running"})

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"dt-mbp","tool":"claude","session":"x"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if _, ok := app.sessions["dt-mbp/claude/x"]; ok {
		t.Errorf("session not deleted")
	}
}

func TestDeleteStatusIdempotent(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"a","tool":"b","session":"c"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestDeleteStatusRejectsEmptyKey(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/status", bytes.NewReader([]byte(`{"source":"","tool":"b","session":"c"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPostStatusRejectsOversizedBody(t *testing.T) {
	_, srv := newTestServer(t, defaultConfig())

	huge := strings.Repeat("x", (1<<20)+1) // 1 MiB + 1 byte
	body := map[string]any{
		"source":  "dt-mbp",
		"tool":    "claude",
		"session": "x",
		"state":   "running",
		"message": huge,
	}
	resp := postJSON(t, srv, "/v1/status", body, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestHandleStatus_413OnOversizeBody(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	req := authedRequest(t, "POST", srv.URL+"/v1/status", strings.Repeat("x", (1<<20)+100))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized body: code = %d, want 413 or 400", resp.StatusCode)
	}
}

func TestHandleClear_413OnOversizeBody(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	req := authedRequest(t, "POST", srv.URL+"/v1/clear", strings.Repeat("x", 2048))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized /v1/clear body: code = %d, want 413 or 400", resp.StatusCode)
	}
}

func TestHandleStatus_ForwardCompat_AcceptsUnknownField(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	body := `{"source":"a","tool":"b","session":"s1","state":"running","rate_window_pct":42}`
	req := authedRequest(t, "POST", srv.URL+"/v1/status", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 200; body = %s", resp.StatusCode, out)
	}
}

func TestHandleDeleteStatus_RemainsStrict_OnUnknownField(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	body := `{"source":"a","tool":"b","session":"s1","weirdfield":true}`
	req := authedRequest(t, "DELETE", srv.URL+"/v1/status", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400 (strict mode on DELETE)", resp.StatusCode)
	}
}

func TestHandleStatus_LogsInfoOnParseFailure(t *testing.T) {
	var buf bytes.Buffer
	srv := newRawTestServer(t, captureLogger(&buf))

	req := authedRequest(t, "POST", srv.URL+"/v1/status", "not json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"level":"INFO"`) ||
		!strings.Contains(logs, `"msg":"request rejected"`) ||
		!strings.Contains(logs, `"reason":"parse"`) {
		t.Errorf("expected Info request-rejected reason=parse log, got: %s", logs)
	}
}

func TestHandleStatus_LogsInfoOnValidationFailure(t *testing.T) {
	var buf bytes.Buffer
	srv := newRawTestServer(t, captureLogger(&buf))

	body := `{"source":"","tool":"t","session":"s","state":"running"}`
	req := authedRequest(t, "POST", srv.URL+"/v1/status", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"validation"`) {
		t.Errorf("expected reason=validation log, got: %s", logs)
	}
}

func TestHandleNotify_LogsInfoOnEmptyText(t *testing.T) {
	var buf bytes.Buffer
	srv := newRawTestServer(t, captureLogger(&buf))

	body := `{"text":""}`
	req := authedRequest(t, "POST", srv.URL+"/v1/notify", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, `"reason":"validation"`) ||
		!strings.Contains(logs, `"field":"text"`) {
		t.Errorf("expected reason=validation field=text log, got: %s", logs)
	}
}

// TestHandleNotify_EmitsNGPayload pins the /v1/notify handler's ad-hoc payload
// on awtrix-ng's schema: the request's seconds become durationMs, and the
// request colour lands on textColor. AWTRIX3's `duration`/`color` 422 on NG.
func TestHandleNotify_EmitsNGPayload(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/notify",
		strings.NewReader(`{"text":"PING","color":"#FF00AA","duration":7,"hold":true}`))
	w := httptest.NewRecorder()
	app.handleNotify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	notes := pub.NotifySnapshot()
	if len(notes) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notes))
	}
	p := notes[0]
	want := map[string]any{
		"text": "PING", "textColor": "#FF00AA", "durationMs": 7000,
		"hold": true, "wakeup": true, "stack": false,
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("payload[%s] = %v, want %v", k, p[k], v)
		}
	}
	for _, k := range []string{"duration", "color"} {
		if _, has := p[k]; has {
			t.Errorf("legacy AWTRIX3 key %q present — NG rejects the whole payload", k)
		}
	}
	// Casing and scroll are pinned, not inherited from the clock's globals.
	if p["textCase"] != "upper" {
		t.Errorf("textCase = %v, want upper", p["textCase"])
	}
	if scroll, _ := p["scroll"].(map[string]any); scroll["whenFits"] != "static" {
		t.Errorf("scroll = %v, want whenFits:static", p["scroll"])
	}
}

// TestHandleNotify_TextCasePassesThrough: a caller that asks for a textCase
// gets it; only an unset one defaults to upper; an unknown one is a 400 and
// nothing reaches the clock (NG would 422 it).
func TestHandleNotify_TextCasePassesThrough(t *testing.T) {
	cases := []struct {
		body     string
		wantCode int
		wantCase any
	}{
		{`{"text":"Hello"}`, http.StatusOK, "upper"},
		{`{"text":"Hello","text_case":"asTyped"}`, http.StatusOK, "asTyped"},
		{`{"text":"Hello","text_case":"inherit"}`, http.StatusOK, "inherit"},
		{`{"text":"Hello","text_case":"lower"}`, http.StatusBadRequest, nil},
	}
	for _, c := range cases {
		pub := &recordingPublisher{}
		app := NewApp(defaultConfig(), pub, testLogger())
		w := httptest.NewRecorder()
		app.handleNotify(w, httptest.NewRequest(http.MethodPost, "/v1/notify", strings.NewReader(c.body)))
		if w.Code != c.wantCode {
			t.Errorf("%s: status = %d, want %d", c.body, w.Code, c.wantCode)
			continue
		}
		notes := pub.NotifySnapshot()
		if c.wantCase == nil {
			if len(notes) != 0 {
				t.Errorf("%s: pushed %v despite the 400", c.body, notes)
			}
			continue
		}
		if len(notes) != 1 || notes[0]["textCase"] != c.wantCase {
			t.Errorf("%s: textCase = %v, want %v", c.body, notes, c.wantCase)
		}
	}
}

// The default colour must be the same canonical "#RRGGBB" form every render
// builder emits.
func TestHandleNotify_DefaultColorIsCanonicalHex(t *testing.T) {
	pub := &recordingPublisher{}
	app := NewApp(defaultConfig(), pub, testLogger())
	app.handleNotify(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost,
		"/v1/notify", strings.NewReader(`{"text":"PING"}`)))
	p := pub.NotifySnapshot()[0]
	if p["textColor"] != "#FFFFFF" {
		t.Errorf("default textColor = %v, want #FFFFFF", p["textColor"])
	}
	if p["durationMs"] != 5000 {
		t.Errorf("default durationMs = %v, want 5000 (the 5s default)", p["durationMs"])
	}
}

func TestStatusRequestValidate_OptionalFields(t *testing.T) {
	mk := func(ctxPct *int, srcColor *string) StatusRequest {
		return StatusRequest{
			Source: "a", Tool: "b", Session: "c", State: "running",
			ContextPct: ctxPct, SourceColor: srcColor,
		}
	}
	good := []StatusRequest{
		mk(nil, nil),
		mk(intPtr(0), nil),
		mk(intPtr(100), nil),
		mk(nil, strPtr("#aabbcc")),
		mk(intPtr(50), strPtr("#AABBCC")),
	}
	for _, r := range good {
		if err := r.validate(); err != nil {
			t.Errorf("validate(%+v) = %v, want nil", r, err)
		}
	}
	bad := []struct {
		name string
		r    StatusRequest
	}{
		{"ctx<0", mk(intPtr(-1), nil)},
		{"ctx>100", mk(intPtr(101), nil)},
		{"color missing #", mk(nil, strPtr("aabbcc"))},
		{"color short", mk(nil, strPtr("#aabb"))},
		{"color non-hex", mk(nil, strPtr("#xxxxxx"))},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.r.validate(); err == nil {
				t.Errorf("validate(%+v) = nil, want error", tc.r)
			}
		})
	}
}

func TestStatusRequest_RateWindowPctRoundTrips(t *testing.T) {
	rw := 73
	s := StatusRequest{Source: "mbp", Tool: "codex", Session: "u1", State: "running", RateWindowPct: &rw}.normalized()
	if s.RateWindowPct == nil || *s.RateWindowPct != 73 {
		t.Fatalf("RateWindowPct = %v, want 73", s.RateWindowPct)
	}
}

func TestStatusRequest_RateWindowPctRangeValidated(t *testing.T) {
	for _, bad := range []int{-1, 101} {
		b := bad
		err := StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running", RateWindowPct: &b}.validate()
		if err == nil {
			t.Errorf("rate_window_pct=%d should be rejected", bad)
		}
	}
	ok := 0
	if err := (StatusRequest{Source: "x", Tool: "codex", Session: "s", State: "running", RateWindowPct: &ok}).validate(); err != nil {
		t.Errorf("rate_window_pct=0 should be valid, got %v", err)
	}
}

func TestStatusRequest_ActivityRoundTrips(t *testing.T) {
	s := StatusRequest{Source: "mbp", Tool: "claude", Session: "u1", State: "running", Activity: "  Bash: npm test  "}.normalized()
	if s.Activity != "Bash: npm test" {
		t.Fatalf("Activity = %q, want trimmed %q", s.Activity, "Bash: npm test")
	}
}

func TestStatusRequest_ActivityLengthValidated(t *testing.T) {
	long := strings.Repeat("x", 81)
	if err := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", Activity: long}).validate(); err == nil {
		t.Errorf("activity of 81 chars should be rejected")
	}
	ok := strings.Repeat("x", 80)
	if err := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", Activity: ok}).validate(); err != nil {
		t.Errorf("activity of 80 chars should be valid, got %v", err)
	}
	if err := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", Activity: ""}).validate(); err != nil {
		t.Errorf("empty activity should be valid, got %v", err)
	}
}

// Producers truncate activity to 80 runes; the server must count runes, not
// bytes, or a multibyte activity (≤80 chars but >80 bytes) 400s every status
// POST for that session. Regression guard for the rune/byte mismatch.
func TestStatusRequest_ActivityMultibyteWithin80Runes(t *testing.T) {
	// 80 Cyrillic runes = 160 bytes: valid by rune count, would fail by bytes.
	activity := strings.Repeat("я", 80)
	if err := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", Activity: activity}).validate(); err != nil {
		t.Errorf("80-rune multibyte activity should be valid, got %v", err)
	}
	// 81 runes must still be rejected.
	if err := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", Activity: strings.Repeat("я", 81)}).validate(); err == nil {
		t.Errorf("81-rune activity should be rejected")
	}
}

func TestStatusRequest_ContextNumberRoundTrips(t *testing.T) {
	s := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", ContextNumber: true}.normalized()
	if !s.ContextNumber {
		t.Errorf("ContextNumber = false, want true")
	}
	s2 := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running"}.normalized()
	if s2.ContextNumber {
		t.Errorf("ContextNumber default = true, want false")
	}
}

func TestStatusRequest_RateBottomBarRoundTrips(t *testing.T) {
	s := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", RateBottomBar: true}.normalized()
	if !s.RateBottomBar {
		t.Errorf("RateBottomBar = false, want true")
	}
	s2 := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running"}.normalized()
	if s2.RateBottomBar {
		t.Errorf("RateBottomBar default = true, want false")
	}
}

func TestStatusRequest_RateResetRoundTrips(t *testing.T) {
	s := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", RateResetAt: 1778614633, RateReset: true}.normalized()
	if s.RateResetAt != 1778614633 || !s.RateReset {
		t.Errorf("reset fields not carried: %+v", s)
	}
	s2 := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running"}.normalized()
	if s2.RateResetAt != 0 || s2.RateReset {
		t.Errorf("reset defaults wrong: %+v", s2)
	}
}

func TestValidate_RejectsNegativeReset(t *testing.T) {
	err := StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", RateResetAt: -5}.validate()
	if err == nil {
		t.Error("negative rate_reset_at should be rejected")
	}
	if e := (StatusRequest{Source: "a", Tool: "claude", Session: "s", State: "running", RateResetAt: 1778614633}).validate(); e != nil {
		t.Errorf("positive rate_reset_at should be accepted, got %v", e)
	}
}
