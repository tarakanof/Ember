package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/awtrix"
)

type recordingPublisher struct {
	mu                sync.Mutex
	customApps        []map[string]any
	customNames       []string
	clearedApps       []string
	notify            []map[string]any
	indicator         []map[string]any
	indicatorCalls    []indicatorCall
	indicatorErr      error
	clearedIndicators []int
	settings          []map[string]any
	switches          []string
	deviceSettings    map[string]any
	readSettingsErr   error
	settingsFails     int
	switchFails       int
	switchModes       []awtrix.SwitchMode
	dismissedNames    []string
	dismissByNameErr  error
	rtttls            []string
	loopApps          []string
	ops               []string
	icons             []string
	iconsErr          error
	putIcons          []string

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
	p.ops = append(p.ops, "push "+name)
	return nil
}

func (p *recordingPublisher) ClearApp(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearedApps = append(p.clearedApps, name)
	p.ops = append(p.ops, "clear "+name)
	return nil
}

func (p *recordingPublisher) ClearedAppsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.clearedApps))
	copy(out, p.clearedApps)
	return out
}

func (p *recordingPublisher) CustomNamesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.customNames))
	copy(out, p.customNames)
	return out
}

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

func (p *recordingPublisher) DismissNotifyByName(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dismissedNames = append(p.dismissedNames, name)
	return p.dismissByNameErr
}

func (p *recordingPublisher) DismissedNamesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.dismissedNames))
	copy(out, p.dismissedNames)
	return out
}

func (p *recordingPublisher) PlayRTTTL(_ context.Context, rtttl string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rtttls = append(p.rtttls, rtttl)
	return nil
}

type indicatorCall struct {
	index   int
	payload map[string]any
}

var errFakeDeviceDown = errors.New("fake device unreachable")

func (p *recordingPublisher) Indicator(_ context.Context, index int, payload map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.indicatorCalls = append(p.indicatorCalls, indicatorCall{index: index, payload: payload})
	if p.indicatorErr != nil {
		return p.indicatorErr
	}
	p.indicator = append(p.indicator, payload)
	return nil
}

func (p *recordingPublisher) IndicatorCallsSnapshot() []indicatorCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]indicatorCall, len(p.indicatorCalls))
	copy(out, p.indicatorCalls)
	return out
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
	p.ops = append(p.ops, "settings")
	if p.settingsFails > 0 {
		p.settingsFails--
		return errUnreachableDevice
	}
	return nil
}

func (p *recordingPublisher) ReadSettings(_ context.Context) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readSettingsErr != nil {
		return nil, p.readSettingsErr
	}
	return p.deviceSettings, nil
}

func (p *recordingPublisher) Switch(_ context.Context, name string, mode awtrix.SwitchMode) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.switches = append(p.switches, name)
	p.switchModes = append(p.switchModes, mode)
	p.ops = append(p.ops, "switch "+name)
	if p.switchFails > 0 {
		p.switchFails--
		return errUnreachableDevice
	}
	return nil
}

func (p *recordingPublisher) OpsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.ops))
	copy(out, p.ops)
	return out
}

func (p *recordingPublisher) SettingsSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.settings))
	copy(out, p.settings)
	return out
}

func (p *recordingPublisher) SwitchModesSnapshot() []awtrix.SwitchMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]awtrix.SwitchMode, len(p.switchModes))
	copy(out, p.switchModes)
	return out
}

func (p *recordingPublisher) SwitchesSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.switches))
	copy(out, p.switches)
	return out
}

func (p *recordingPublisher) CustomAppsSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.customApps))
	copy(out, p.customApps)
	return out
}

func (p *recordingPublisher) IndicatorSnapshot() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, len(p.indicator))
	copy(out, p.indicator)
	return out
}

func (p *recordingPublisher) RTTTLsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.rtttls))
	copy(out, p.rtttls)
	return out
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
	if _, hasText := customs[0]["text"]; hasText {
		t.Errorf("idle frame has text key; want robot-only dim frame")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

func newRawTestServer(t *testing.T, logger *slog.Logger) *httptest.Server {
	t.Helper()
	cfg := defaultConfig()
	cfg.AWTRIX.HTTPBaseURL = "http://x"
	cfg.applyDefaults()
	cfg.Auth.StatusToken = testToken
	app := NewApp(cfg, nil, logger)
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)
	return srv
}

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

type noopPublisher struct{}

func (noopPublisher) CustomApp(context.Context, string, map[string]any) error { return nil }
func (noopPublisher) ClearApp(context.Context, string) error                  { return nil }
func (noopPublisher) ListIcons(context.Context) ([]string, error)             { return nil, nil }
func (noopPublisher) PutIcon(context.Context, string, []byte) error           { return nil }
func (noopPublisher) ListApps(context.Context) ([]string, error)              { return nil, nil }
func (noopPublisher) Notify(context.Context, map[string]any) error            { return nil }
func (noopPublisher) DismissNotifyByName(context.Context, string) error       { return nil }
func (noopPublisher) PlayRTTTL(context.Context, string) error                 { return nil }
func (noopPublisher) Indicator(context.Context, int, map[string]any) error    { return nil }
func (noopPublisher) ClearIndicator(context.Context, int) error               { return nil }
func (noopPublisher) Settings(context.Context, map[string]any) error          { return nil }
func (noopPublisher) Switch(context.Context, string, awtrix.SwitchMode) error { return nil }
func (noopPublisher) ReadSettings(context.Context) (map[string]any, error)    { return nil, nil }

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func strPtr(s string) *string { return &s }
