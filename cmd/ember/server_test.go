package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRequiredOnWriteEndpoints(t *testing.T) {
	_, srv := newTestServerWithToken(t, "secret-token")

	// No auth header
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, map[string]string{})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", resp.StatusCode)
	}

	// Wrong token
	resp = postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, map[string]string{"Authorization": "Bearer wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", resp.StatusCode)
	}

	// Correct token
	resp = postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, map[string]string{"Authorization": "Bearer secret-token"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthClosedWhenTokenEmpty(t *testing.T) {
	_, srv := newTestServerWithToken(t, "")
	resp := postJSON(t, srv, "/v1/status", map[string]any{
		"source": "a", "tool": "b", "session": "c", "state": "running",
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("empty token / write: status = %d, want 401 (fail closed)", resp.StatusCode)
	}
}

func TestAuthNotRequiredOnReadEndpoints(t *testing.T) {
	_, srv := newTestServerWithToken(t, "secret-token")
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/state without auth: status = %d, want 200", resp.StatusCode)
	}

	resp, err = client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz without auth: status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireAuth_TokenRotation(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	cfg.Auth.StatusToken = "old"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := NewApp(cfg, &noopPublisher{}, logger)

	handler := requireAuth(app, logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Old token works.
	req1, _ := http.NewRequest("GET", srv.URL, nil)
	req1.Header.Set("Authorization", "Bearer old")
	resp1, err := srv.Client().Do(req1)
	if err != nil {
		t.Fatalf("old token request: %v", err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("old token: got %d, want 200", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Rotate.
	newCfg := *app.cfg.Load()
	newCfg.Auth.StatusToken = "new"
	app.cfg.Store(&newCfg)

	// Old now rejected.
	req2, _ := http.NewRequest("GET", srv.URL, nil)
	req2.Header.Set("Authorization", "Bearer old")
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("old-after-rotation request: %v", err)
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after rotation: got %d, want 401", resp2.StatusCode)
	}
	resp2.Body.Close()

	// New accepted.
	req3, _ := http.NewRequest("GET", srv.URL, nil)
	req3.Header.Set("Authorization", "Bearer new")
	resp3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatalf("new token request: %v", err)
	}
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("new token: got %d, want 200", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestDecodeJSON_RejectsTrailingValue(t *testing.T) {
	srv := newRawTestServer(t, discardLogger())

	// Two top-level JSON values back-to-back.
	body := `{"source":"a","tool":"t","session":"s","state":"running"}{"x":1}`
	req := authedRequest(t, "POST", srv.URL+"/v1/status", body)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("trailing value: code = %d, want 400", resp.StatusCode)
	}
}

// TestOversizedJSONBodyIs413 pins the shared decode contract on every JSON
// write endpoint: a body past the cap answers 413, not a generic 400. The
// body opens as valid JSON so the decoder reads up to the cap instead of
// stopping at the first bad byte.
func TestOversizedJSONBodyIs413(t *testing.T) {
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(clock.Close)
	app := newPomodoroApp(t)
	app.updateConfig(func(c *Config) { c.AWTRIX.HTTPBaseURL = clock.URL })
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)

	big := `{"x":"` + strings.Repeat("a", 1<<20) + `"}`
	endpoints := []struct{ method, path string }{
		{"POST", "/v1/status"},
		{"DELETE", "/v1/status"},
		{"POST", "/v1/notify"},
		{"POST", "/v1/usage"},
		{"PUT", "/v1/usage/config"},
		{"PUT", "/v1/apps"},
		{"PUT", "/v1/display/config"},
		{"PUT", "/v1/quiet/config"},
		{"PUT", "/v1/weather/config"},
		{"PUT", "/v1/meetings/config"},
		{"PUT", "/v1/pomodoro/config"},
		{"POST", "/v1/pomodoro/start"},
		{"POST", "/v1/reminders/fire"},
		{"PUT", "/v1/device/config"},
		{"PUT", "/v1/device/settings"},
		{"PUT", "/v1/device/display"},
		{"PUT", "/v1/device/display/power"},
		{"POST", "/v1/device/audio/test"},
		{"PUT", "/v1/device/apps"},
		{"PUT", "/v1/device/sensors"},
		{"PUT", "/v1/device/buttons"},
	}
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			resp, err := srv.Client().Do(authedRequest(t, ep.method, srv.URL+ep.path, big))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("code = %d, want 413", resp.StatusCode)
			}
		})
	}

	// A small valid value followed by padding past the cap trips the cap in
	// the trailing-token read, which must still be a 413, not a 400.
	t.Run("valid JSON then 2 MB whitespace", func(t *testing.T) {
		padded := `{"text":"hi"}` + strings.Repeat(" ", 2<<20)
		resp, err := srv.Client().Do(authedRequest(t, "POST", srv.URL+"/v1/notify", padded))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("code = %d, want 413", resp.StatusCode)
		}
	})
}
