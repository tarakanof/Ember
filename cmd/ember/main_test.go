package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingPublisher struct {
	mu                sync.Mutex
	customApps        []map[string]any
	customNames       []string
	clearedApps       []string
	notify            []map[string]any
	indicator         []map[string]any
	clearedIndicators []int
	settings          []map[string]any
	switches          []string
	dismissals        int
	rtttls            []string
	sounds            []string
	loopApps          []string // app names returned by ListApps (device rotation)
	icons             []string // filenames returned by ListIcons (/ICONS folder)
	iconsErr          error    // when non-nil, ListIcons fails with it
	putIcons          []string // filenames uploaded via PutIcon

	// failNotify, when non-nil, is called on each Notify call and returns an
	// error to simulate a device-unreachable condition. Return nil to succeed.
	failNotify func() error
}

func (p *recordingPublisher) ListIcons(_ context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.iconsErr != nil {
		return nil, p.iconsErr
	}
	out := make([]string, len(p.icons))
	copy(out, p.icons)
	return out, nil
}

func (p *recordingPublisher) PutIcon(_ context.Context, filename string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putIcons = append(p.putIcons, filename)
	return nil
}

// PutIconNamesSnapshot returns a copy of uploaded icon filenames under the lock.
func (p *recordingPublisher) PutIconNamesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.putIcons))
	copy(out, p.putIcons)
	return out
}

func (p *recordingPublisher) ListApps(_ context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.loopApps))
	copy(out, p.loopApps)
	return out, nil
}

func (p *recordingPublisher) CustomApp(_ context.Context, name string, payload map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.customApps = append(p.customApps, payload)
	p.customNames = append(p.customNames, name)
	return nil
}

func (p *recordingPublisher) ClearApp(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearedApps = append(p.clearedApps, name)
	return nil
}

// ClearedAppsSnapshot returns a copy of cleared app names under the lock.
func (p *recordingPublisher) ClearedAppsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.clearedApps))
	copy(out, p.clearedApps)
	return out
}

// CustomNamesSnapshot returns a copy of pushed app names under the lock.
func (p *recordingPublisher) CustomNamesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.customNames))
	copy(out, p.customNames)
	return out
}

// NotifySnapshot returns a copy of recorded Notify payloads under the lock.
func (p *recordingPublisher) NotifySnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.notify))
	copy(out, p.notify)
	return out
}

func (p *recordingPublisher) Notify(_ context.Context, payload map[string]any) error {
	p.mu.Lock()
	fn := p.failNotify
	p.mu.Unlock()
	if fn != nil {
		if err := fn(); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notify = append(p.notify, payload)
	return nil
}

func (p *recordingPublisher) DismissNotify(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dismissals++
	return nil
}

func (p *recordingPublisher) PlayRTTTL(_ context.Context, rtttl string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rtttls = append(p.rtttls, rtttl)
	return nil
}

func (p *recordingPublisher) PlaySound(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sounds = append(p.sounds, name)
	return nil
}

func (p *recordingPublisher) Indicator(_ context.Context, _ int, payload map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.indicator = append(p.indicator, payload)
	return nil
}

func (p *recordingPublisher) ClearIndicator(_ context.Context, index int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearedIndicators = append(p.clearedIndicators, index)
	return nil
}

func (p *recordingPublisher) Settings(_ context.Context, payload map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.settings = append(p.settings, payload)
	return nil
}

func (p *recordingPublisher) Switch(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.switches = append(p.switches, name)
	return nil
}

// SettingsSnapshot returns a copy of recorded settings calls under the lock.
func (p *recordingPublisher) SettingsSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.settings))
	copy(out, p.settings)
	return out
}

// SwitchesSnapshot returns a copy of recorded switch target names under the lock.
func (p *recordingPublisher) SwitchesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.switches))
	copy(out, p.switches)
	return out
}

// CustomAppsSnapshot returns a copy of customApps under the lock, safe for
// concurrent-test reads (race detector).
func (p *recordingPublisher) CustomAppsSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.customApps))
	copy(out, p.customApps)
	return out
}

// IndicatorSnapshot returns a copy of indicator under the lock, safe for
// concurrent-test reads (race detector).
func (p *recordingPublisher) IndicatorSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.indicator))
	copy(out, p.indicator)
	return out
}

// RTTTLsSnapshot returns a copy of recorded PlayRTTTL calls under the lock.
func (p *recordingPublisher) RTTTLsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.rtttls))
	copy(out, p.rtttls)
	return out
}

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

func TestWaitingStatusWinsOverRunningStatus(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "codex",
		Session: "repo",
		State:   "running",
	})
	render, _ := app.Upsert(StatusRequest{
		Source:  "macbook",
		Tool:    "claude",
		Session: "desktop",
		State:   "waiting",
		Message: "approve Bash",
	})

	if render.Text != "WAIT approve Bash" {
		t.Fatalf("Text = %q, want WAIT approve Bash", render.Text)
	}
	if render.Waiting != 1 {
		t.Fatalf("Waiting = %d, want 1", render.Waiting)
	}
	if render.Running != 1 {
		t.Fatalf("Running = %d, want 1", render.Running)
	}
}

func TestCoord_PublishesDrawPayload_OnUpsert(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	app := NewApp(cfg, publisher, testLogger())
	app.Upsert(StatusRequest{Source: "dt", Tool: "claude", Session: "s1", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.coord.Run(ctx)
	app.coord.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	customs := publisher.CustomAppsSnapshot()
	if len(customs) != 1 {
		t.Fatalf("custom app publishes = %d, want 1", len(customs))
	}
	if _, ok := customs[0]["draw"]; !ok {
		t.Errorf("payload missing draw key")
	}

	indicators := publisher.IndicatorSnapshot()
	if len(indicators) != 0 {
		t.Errorf("indicator publishes = %d, want 0 (retired in G.1a)", len(indicators))
	}
}

// TestCoord_IdleSession_EmitsIdleFrame replaces the pre-G.2 NoPublish
// expectation. An "idle" session is excluded from sortedActiveKeys, so
// the snapshot has zero active sessions — the coordinator enters the
// idle countdown and emits a dimmed idle frame on the first tick
// instead of ceding the slot immediately.
func TestCoord_IdleSession_EmitsIdleFrame(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	publisher := &recordingPublisher{}
	app := NewApp(cfg, publisher, testLogger())
	app.Upsert(StatusRequest{Source: "dt", Tool: "claude", Session: "s1", State: "idle"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.coord.Run(ctx)
	app.coord.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	customs := publisher.CustomAppsSnapshot()
	if got := len(customs); got != 1 {
		t.Fatalf("custom app publishes on idle = %d, want 1 (idle countdown dim frame)", got)
	}
	// Idle frame must not carry a text key (robot-only dim frame).
	if _, hasText := customs[0]["text"]; hasText {
		t.Errorf("idle frame has text key; want robot-only dim frame")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testToken is the bearer token wired into the default test servers. Writes
// fail closed on an empty token, so the shared HTTP test helpers always
// configure a token and authenticate with it unless a test overrides it.
const testToken = "test-token"

func newTestServer(t *testing.T, cfg Config) (*App, *httptest.Server) {
	t.Helper()
	if cfg.Auth.StatusToken == "" {
		cfg.Auth.StatusToken = testToken
	}
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return app, srv
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// nil headers means "don't care about auth" — inject the shared test token
	// so fail-closed write endpoints are reachable. A test exercising auth
	// passes an explicit (possibly empty) map to control the Authorization
	// header itself.
	if headers == nil {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// newRawTestServer builds a test server backed by NewHTTPPublisher (base URL
// http://x) with the shared test token configured. It suits tests that assert
// on decode/validation/logging behaviour and drive raw request bodies; logger
// lets a test capture emitted log lines.
func newRawTestServer(t *testing.T, logger *slog.Logger) *httptest.Server {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	pub, _ := NewHTTPPublisher()
	app := NewApp(cfg, pub, logger)
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return srv
}

// authedRequest builds a JSON request to a test server carrying the shared
// bearer token, so it clears the fail-closed write auth.
func authedRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
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

func TestDefaultConfigTimingValues(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Display.StaleSeconds != 300 {
		t.Errorf("StaleSeconds = %d, want 300", cfg.Display.StaleSeconds)
	}
	if cfg.Display.DoneTTLSeconds != 30 {
		t.Errorf("DoneTTLSeconds = %d, want 30", cfg.Display.DoneTTLSeconds)
	}
	if cfg.Display.HeartbeatSeconds != 10 {
		t.Errorf("HeartbeatSeconds = %d, want 10", cfg.Display.HeartbeatSeconds)
	}
}

func TestPerStateStalenessReapsActiveSessions(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	cfg.Display.DoneTTLSeconds = 30
	app := NewApp(cfg, &recordingPublisher{}, testLogger())

	// Inject a running session that's 26s old — should be reaped.
	app.sessions["src/claude/x"] = Session{
		Source: "src", Tool: "claude", Session: "x",
		State:     "running",
		UpdatedAt: time.Now().Add(-26 * time.Second),
	}
	// Inject a running session 24s old — should survive.
	app.sessions["src/claude/y"] = Session{
		Source: "src", Tool: "claude", Session: "y",
		State:     "running",
		UpdatedAt: time.Now().Add(-24 * time.Second),
	}
	app.Snapshot() // triggers reaping

	if _, ok := app.sessions["src/claude/x"]; ok {
		t.Errorf("expected src/claude/x to be reaped (26s > stale_seconds=25)")
	}
	if _, ok := app.sessions["src/claude/y"]; !ok {
		t.Errorf("expected src/claude/y to survive (24s <= stale_seconds=25)")
	}
}

func TestPerStateStalenessLingersDoneAndError(t *testing.T) {
	cfg := defaultConfig()
	cfg.Display.StaleSeconds = 25
	cfg.Display.DoneTTLSeconds = 30
	app := NewApp(cfg, &recordingPublisher{}, testLogger())

	// Done session 28s old (over StaleSeconds, under DoneTTL) — should survive.
	app.sessions["src/claude/d"] = Session{
		Source: "src", Tool: "claude", Session: "d",
		State:     "done",
		UpdatedAt: time.Now().Add(-28 * time.Second),
	}
	// Done session 31s old — over DoneTTL — should be reaped.
	app.sessions["src/claude/e"] = Session{
		Source: "src", Tool: "claude", Session: "e",
		State:     "done",
		UpdatedAt: time.Now().Add(-31 * time.Second),
	}
	// Error session 28s old — should survive (uses DoneTTL).
	app.sessions["src/claude/f"] = Session{
		Source: "src", Tool: "claude", Session: "f",
		State:     "error",
		UpdatedAt: time.Now().Add(-28 * time.Second),
	}
	app.Snapshot()

	if _, ok := app.sessions["src/claude/d"]; !ok {
		t.Errorf("done at 28s should linger (DoneTTL=30)")
	}
	if _, ok := app.sessions["src/claude/e"]; ok {
		t.Errorf("done at 31s should be reaped (DoneTTL=30)")
	}
	if _, ok := app.sessions["src/claude/f"]; !ok {
		t.Errorf("error at 28s should linger (DoneTTL=30)")
	}
}

func TestRenderDoneLingersWhenAlone(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	render, _ := app.Upsert(StatusRequest{
		Source: "dt-mbp", Tool: "claude", Session: "x",
		State: "done", Message: "build green",
	})
	if render.Done != 1 {
		t.Errorf("Done = %d, want 1", render.Done)
	}
	// Per-session label for single-session done group:
	if !contains(render.Text, "build green") && !contains(render.Text, "done") {
		t.Errorf("Text = %q, want a per-session done label", render.Text)
	}
	if render.Color != "#707070" {
		t.Errorf("Color = %q, want grey #707070", render.Color)
	}
}

func TestRenderIdleSessionNeverWins(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	render, _ := app.Upsert(StatusRequest{
		Source: "dt-mbp", Tool: "claude", Session: "x",
		State: "idle",
	})
	if render.Text != "AI idle" {
		t.Errorf("Text = %q, want AI idle (idle never wins)", render.Text)
	}
}

func TestRenderAggregateLabelForMultipleWaiting(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{Source: "a", Tool: "claude", Session: "1", State: "waiting"})
	render, _ := app.Upsert(StatusRequest{Source: "b", Tool: "claude", Session: "2", State: "waiting"})
	if render.Waiting != 2 {
		t.Errorf("Waiting = %d, want 2", render.Waiting)
	}
	if !contains(render.Text, "W2") {
		t.Errorf("Text = %q, want aggregate including W2", render.Text)
	}
}

func TestRenderAggregateMixedGroups(t *testing.T) {
	app := NewApp(defaultConfig(), &recordingPublisher{}, testLogger())
	app.Upsert(StatusRequest{Source: "a", Tool: "claude", Session: "1", State: "waiting"})
	app.Upsert(StatusRequest{Source: "b", Tool: "claude", Session: "2", State: "waiting"})
	app.Upsert(StatusRequest{Source: "c", Tool: "claude", Session: "3", State: "running"})
	app.Upsert(StatusRequest{Source: "d", Tool: "claude", Session: "4", State: "running"})
	render, _ := app.Upsert(StatusRequest{Source: "e", Tool: "claude", Session: "5", State: "running"})
	if !contains(render.Text, "W2") || !contains(render.Text, "R3") {
		t.Errorf("Text = %q, want aggregate AI W2 R3", render.Text)
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

// helper
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func newTestServerWithToken(t *testing.T, token string) (*App, *httptest.Server) {
	t.Helper()
	cfg := defaultConfig()
	cfg.Auth.StatusToken = token
	app := NewApp(cfg, &recordingPublisher{}, testLogger())
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return app, srv
}

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

type noopPublisher struct{}

func (noopPublisher) CustomApp(context.Context, string, map[string]any) error { return nil }
func (noopPublisher) ClearApp(context.Context, string) error                  { return nil }
func (noopPublisher) ListIcons(context.Context) ([]string, error)             { return nil, nil }
func (noopPublisher) PutIcon(context.Context, string, []byte) error           { return nil }
func (noopPublisher) ListApps(context.Context) ([]string, error)              { return nil, nil }
func (noopPublisher) Notify(context.Context, map[string]any) error            { return nil }
func (noopPublisher) DismissNotify(context.Context) error                     { return nil }
func (noopPublisher) PlayRTTTL(context.Context, string) error                 { return nil }
func (noopPublisher) PlaySound(context.Context, string) error                 { return nil }
func (noopPublisher) Indicator(context.Context, int, map[string]any) error    { return nil }
func (noopPublisher) ClearIndicator(context.Context, int) error               { return nil }
func (noopPublisher) Settings(context.Context, map[string]any) error          { return nil }
func (noopPublisher) Switch(context.Context, string) error                    { return nil }

func TestHTTPPublisher_BaseURLReloadable(t *testing.T) {
	hits1, hits2 := 0, 0
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits1++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv1.URL
	cfg.applyDefaults()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub, err := NewHTTPPublisher() // app filled below
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, logger)
	pub.app = app

	if err := pub.Notify(context.Background(), map[string]any{"text": "x"}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if hits1 != 1 || hits2 != 0 {
		t.Errorf("after publish 1: hits1=%d hits2=%d, want 1/0", hits1, hits2)
	}

	// Swap cfg to point at srv2.
	newCfg := *app.cfg.Load()
	newCfg.AWTRIX.HTTPBaseURL = srv2.URL
	app.cfg.Store(&newCfg)

	if err := pub.Notify(context.Background(), map[string]any{"text": "y"}); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if hits1 != 1 || hits2 != 1 {
		t.Errorf("after publish 2: hits1=%d hits2=%d, want 1/1", hits1, hits2)
	}
}

func TestApp_PublishUpdatesLastPublishFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = srv.URL
	cfg.applyDefaults()

	pub, err := NewHTTPPublisher()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// pub.app is wired by NewApp; no manual setting needed.
	// Seed a running session so RenderForCoord produces a non-nil payload.
	app.Upsert(StatusRequest{Source: "dt", Tool: "claude", Session: "s1", State: "running"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.coord.Run(ctx)
	app.coord.Send(coordCmd{kind: cmdTick})
	time.Sleep(50 * time.Millisecond)

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.lastPublishAt.IsZero() {
		t.Error("lastPublishAt not updated")
	}
	if !app.lastPublishOK {
		t.Errorf("lastPublishOK = false, want true (err=%q)", app.lastPublishErr)
	}
	if app.lastPublishErr != "" {
		t.Errorf("lastPublishErr = %q, want empty", app.lastPublishErr)
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

// captureLogger returns a logger that writes JSON-formatted entries to
// the provided buffer at Debug level (so all Info entries are captured).
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
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

func strPtr(s string) *string { return &s }

func TestIndicatorsOffOnStartup(t *testing.T) {
	cfg := defaultConfig()
	publisher := &recordingPublisher{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := NewApp(cfg, publisher, logger)

	if err := app.ClearIndicators(context.Background()); err != nil {
		t.Fatalf("ClearIndicators: %v", err)
	}

	if len(publisher.clearedIndicators) != 3 {
		t.Fatalf("cleared indicators = %d, want 3 (all off)", len(publisher.clearedIndicators))
	}
	for i, idx := range publisher.clearedIndicators {
		if idx != i+1 {
			t.Errorf("cleared indicator %d = %d, want %d", i, idx, i+1)
		}
	}
}

func TestDefaultConfig_NewDisplayFields(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	if cfg.Display.RotationDwellSeconds != 3 {
		t.Errorf("RotationDwellSeconds = %d, want 3", cfg.Display.RotationDwellSeconds)
	}
	if cfg.Display.AckTimeoutSeconds != 30 {
		t.Errorf("AckTimeoutSeconds = %d, want 30", cfg.Display.AckTimeoutSeconds)
	}
}

func TestDefaultConfig_PublishTimeoutTenSeconds(t *testing.T) {
	// Bumped 5→10 to tolerate slow-but-reachable ESP32 responses on flaky
	// WiFi; the coordinator still retries on the next tick.
	if got := defaultConfig().AWTRIX.TimeoutSeconds; got != 10 {
		t.Errorf("defaultConfig timeout_seconds = %d, want 10", got)
	}
	c := Config{}
	c.applyDefaults()
	if c.AWTRIX.TimeoutSeconds != 10 {
		t.Errorf("applyDefaults timeout_seconds = %d, want 10", c.AWTRIX.TimeoutSeconds)
	}
}

func TestDefaultConfig_G2Fields(t *testing.T) {
	cfg := defaultConfig()
	cfg.applyDefaults()
	if cfg.Display.FrameLifetimeSeconds != 30 {
		t.Errorf("FrameLifetimeSeconds = %d, want 30", cfg.Display.FrameLifetimeSeconds)
	}
	if cfg.Display.IdleRestoreSeconds != 120 {
		t.Errorf("IdleRestoreSeconds = %d, want 120", cfg.Display.IdleRestoreSeconds)
	}
}

func TestValidateConfig_G2_FrameLifetimeBounds(t *testing.T) {
	for _, badVal := range []int{0, -1, 9, 121, 999} {
		t.Run(fmt.Sprintf("lifetime=%d", badVal), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.applyDefaults()
			cfg.Display.FrameLifetimeSeconds = badVal
			if err := validateConfig(cfg); err == nil {
				t.Errorf("validateConfig(frame_lifetime_seconds=%d) returned nil, want error", badVal)
			}
		})
	}
}

func TestValidateConfig_G2_IdleRestoreBounds(t *testing.T) {
	for _, badVal := range []int{0, -1, 59, 3601, 99999} {
		t.Run(fmt.Sprintf("idle=%d", badVal), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.applyDefaults()
			cfg.Display.IdleRestoreSeconds = badVal
			if err := validateConfig(cfg); err == nil {
				t.Errorf("validateConfig(idle_restore_seconds=%d) returned nil, want error", badVal)
			}
		})
	}
}

func TestValidateConfig_G2_BoundaryAccepted(t *testing.T) {
	cases := []struct {
		name     string
		lifetime int
		idle     int
	}{
		{"lifetime min, idle min", 10, 60},
		{"lifetime max, idle max", 120, 3600},
		{"lifetime min, idle max", 10, 3600},
		{"lifetime max, idle min", 120, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.applyDefaults()
			cfg.Display.FrameLifetimeSeconds = tc.lifetime
			cfg.Display.IdleRestoreSeconds = tc.idle
			if err := validateConfig(cfg); err != nil {
				t.Errorf("validateConfig(lifetime=%d, idle=%d) returned %v, want nil — inclusive boundary must be accepted", tc.lifetime, tc.idle, err)
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
