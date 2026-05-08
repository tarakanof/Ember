package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type AuthConfig struct {
	StatusToken    string `json:"status_token"`
	StatusTokenEnv string `json:"status_token_env"`
}

type Config struct {
	HTTP    HTTPConfig    `json:"http"`
	AWTRIX  AWTRIXConfig  `json:"awtrix"`
	Auth    AuthConfig    `json:"auth"`
	Display DisplayConfig `json:"display"`
}

type HTTPConfig struct {
	Addr string `json:"addr"`
}

type AWTRIXConfig struct {
	HTTPBaseURL    string `json:"http_base_url"`
	AppName        string `json:"app_name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type DisplayConfig struct {
	IdleText         string `json:"idle_text"`
	StaleSeconds     int    `json:"stale_seconds"`
	DoneTTLSeconds   int    `json:"done_ttl_seconds"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	RefreshSeconds   int    `json:"refresh_seconds"`
	NotifyOnWaiting  bool   `json:"notify_on_waiting"`
}

func defaultConfig() Config {
	return Config{
		HTTP: HTTPConfig{
			Addr: ":8080",
		},
		AWTRIX: AWTRIXConfig{
			HTTPBaseURL:    "http://192.168.0.14",
			AppName:        "ai_status",
			TimeoutSeconds: 5,
		},
		Auth: AuthConfig{
			StatusTokenEnv: "STATUS_TOKEN",
		},
		Display: DisplayConfig{
			IdleText:         "AI idle",
			StaleSeconds:     25,
			DoneTTLSeconds:   30,
			HeartbeatSeconds: 10,
			RefreshSeconds:   5,
			NotifyOnWaiting:  false,
		},
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path == "" {
		path = os.Getenv("CONFIG_PATH")
	}
	if path == "" {
		if _, err := os.Stat("config.json"); err == nil {
			path = "config.json"
		}
	}
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":8080"
	}
	if c.AWTRIX.HTTPBaseURL == "" {
		c.AWTRIX.HTTPBaseURL = "http://192.168.0.14"
	}
	if c.AWTRIX.AppName == "" {
		c.AWTRIX.AppName = "ai_status"
	}
	if c.AWTRIX.TimeoutSeconds <= 0 {
		c.AWTRIX.TimeoutSeconds = 5
	}
	if c.Display.IdleText == "" {
		c.Display.IdleText = "AI idle"
	}
	if c.Display.StaleSeconds <= 0 {
		c.Display.StaleSeconds = 25
	}
	if c.Display.DoneTTLSeconds <= 0 {
		c.Display.DoneTTLSeconds = 30
	}
	if c.Display.HeartbeatSeconds <= 0 {
		c.Display.HeartbeatSeconds = 10
	}
	if c.Display.RefreshSeconds <= 0 {
		c.Display.RefreshSeconds = 5
	}
	if c.Auth.StatusTokenEnv == "" {
		c.Auth.StatusTokenEnv = "STATUS_TOKEN"
	}
	if c.Auth.StatusToken == "" {
		c.Auth.StatusToken = os.Getenv(c.Auth.StatusTokenEnv)
	}
}

type StatusRequest struct {
	Source      string `json:"source"`
	Tool        string `json:"tool"`
	Session     string `json:"session"`
	State       string `json:"state"`
	Message     string `json:"message"`
	TokensToday int64  `json:"tokens_today"`
}

type Session struct {
	Source      string    `json:"source"`
	Tool        string    `json:"tool"`
	Session     string    `json:"session"`
	State       string    `json:"state"`
	Message     string    `json:"message"`
	TokensToday int64     `json:"tokens_today,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (r StatusRequest) normalized() Session {
	source := strings.TrimSpace(r.Source)
	if source == "" {
		source = "unknown"
	}
	tool := strings.ToLower(strings.TrimSpace(r.Tool))
	if tool == "" {
		tool = "ai"
	}
	session := strings.TrimSpace(r.Session)
	if session == "" {
		session = "default"
	}

	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		state = "idle"
	}
	if !validState(state) {
		state = "idle"
	}

	return Session{
		Source:      source,
		Tool:        tool,
		Session:     session,
		State:       state,
		Message:     strings.TrimSpace(r.Message),
		TokensToday: r.TokensToday,
		UpdatedAt:   time.Now(),
	}
}

func (r StatusRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	state := strings.ToLower(strings.TrimSpace(r.State))
	if state == "" {
		return errors.New("state is required")
	}
	if !validState(state) {
		return fmt.Errorf("invalid state %q (must be one of idle, running, waiting, done, error)", state)
	}
	return nil
}

func validState(state string) bool {
	switch state {
	case "idle", "running", "waiting", "done", "error":
		return true
	default:
		return false
	}
}

func (s Session) key() string {
	return s.Source + "/" + s.Tool + "/" + s.Session
}

type Snapshot struct {
	Now      time.Time `json:"now"`
	Sessions []Session `json:"sessions"`
	Render   Render    `json:"render"`
}

type Render struct {
	Text        string `json:"text"`
	Color       string `json:"color"`
	Waiting     int    `json:"waiting"`
	Running     int    `json:"running"`
	Errors      int    `json:"errors"`
	Done        int    `json:"done"`
	ActiveTotal int    `json:"active_total"`
	Message     string `json:"message,omitempty"`
}

type App struct {
	cfg       Config
	publisher Publisher
	logger    *slog.Logger

	mu            sync.Mutex
	sessions      map[string]Session
	lastWaitKey   string // last waiting-notification Render.Text, for notify dedupe (not a session key)
	lastPublished Render
}

func NewApp(cfg Config, publisher Publisher, logger *slog.Logger) *App {
	return &App{
		cfg:       cfg,
		publisher: publisher,
		logger:    logger,
		sessions:  make(map[string]Session),
	}
}

func (a *App) Upsert(req StatusRequest) Render {
	session := req.normalized()
	a.mu.Lock()
	a.sessions[session.key()] = session
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Clear() Render {
	a.mu.Lock()
	clear(a.sessions)
	a.lastWaitKey = ""
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Delete(key string) Render {
	a.mu.Lock()
	delete(a.sessions, key)
	render := a.renderLocked(time.Now())
	a.mu.Unlock()
	return render
}

func (a *App) Snapshot() Snapshot {
	now := time.Now()
	a.mu.Lock()
	render := a.renderLocked(now)
	sessions := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return Snapshot{
		Now:      now,
		Sessions: sessions,
		Render:   render,
	}
}

func (a *App) renderLocked(now time.Time) Render {
	staleAfter := time.Duration(a.cfg.Display.StaleSeconds) * time.Second
	doneTTL := time.Duration(a.cfg.Display.DoneTTLSeconds) * time.Second
	for key, session := range a.sessions {
		age := now.Sub(session.UpdatedAt)
		var reaped bool
		switch session.State {
		case "done", "error":
			reaped = age > doneTTL
		default:
			reaped = age > staleAfter
		}
		if reaped {
			a.logger.Warn("session reaped",
				"source", session.Source,
				"tool", session.Tool,
				"session", session.Session,
				"state", session.State,
				"age_seconds", int(age.Seconds()),
			)
			delete(a.sessions, key)
		}
	}

	var waiting, running, errored, done []Session
	for _, session := range a.sessions {
		switch session.State {
		case "waiting":
			waiting = append(waiting, session)
		case "running":
			running = append(running, session)
		case "error":
			errored = append(errored, session)
		case "done":
			done = append(done, session)
		}
		// idle sessions are intentionally not bucketed — they never win a render slot
	}

	sortSessions(waiting)
	sortSessions(running)
	sortSessions(errored)
	sortSessions(done)

	activeTotal := len(waiting) + len(running) + len(errored)

	// Pick winning group by priority.
	var winningGroup []Session
	var color string
	switch {
	case len(waiting) > 0:
		winningGroup = waiting
		color = "#FF3300"
	case len(errored) > 0:
		winningGroup = errored
		color = "#FF3300"
	case len(running) > 0:
		winningGroup = running
		color = "#00A3FF"
	case len(done) > 0:
		winningGroup = done
		color = "#707070"
	default:
		return Render{
			Text:        a.cfg.Display.IdleText,
			Color:       "#707070",
			ActiveTotal: activeTotal,
		}
	}

	text := perSessionLabel(winningGroup[0])
	if len(winningGroup) >= 2 {
		text = aggregateLabel(len(waiting), len(running), len(errored), len(done))
	}

	return Render{
		Text:        compactText(text),
		Color:       color,
		Waiting:     len(waiting),
		Running:     len(running),
		Errors:      len(errored),
		Done:        len(done),
		ActiveTotal: activeTotal,
		Message:     winningGroup[0].Message,
	}
}

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

func firstMessage(session Session, fallback string) string {
	if session.Message != "" {
		return session.Message
	}
	return fallback
}

func labelFor(session Session) string {
	switch strings.ToLower(session.Tool) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	default:
		if session.Tool == "" {
			return "AI"
		}
		return strings.ToUpper(session.Tool[:1]) + session.Tool[1:]
	}
}

func compactText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 80 {
		return text
	}
	return text[:77] + "..."
}

func perSessionLabel(s Session) string {
	switch s.State {
	case "waiting":
		return "WAIT " + firstMessage(s, "approval")
	case "error":
		return "ERR " + labelFor(s) + " " + firstMessage(s, "error")
	case "running":
		return labelFor(s) + " run"
	case "done":
		msg := firstMessage(s, "")
		if msg != "" {
			return labelFor(s) + " done " + msg
		}
		return labelFor(s) + " done"
	default:
		return labelFor(s)
	}
}

func aggregateLabel(waiting, running, errored, done int) string {
	parts := []string{"AI"}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("W%d", waiting))
	}
	if errored > 0 {
		parts = append(parts, fmt.Sprintf("E%d", errored))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("R%d", running))
	}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("D%d", done))
	}
	return strings.Join(parts, " ")
}

func (a *App) Publish(ctx context.Context) error {
	snapshot := a.Snapshot()
	payload := map[string]any{
		"text":     snapshot.Render.Text,
		"color":    snapshot.Render.Color,
		"textCase": 2,
		"duration": max(5, a.cfg.Display.RefreshSeconds+2),
		"lifetime": a.cfg.Display.StaleSeconds + a.cfg.Display.DoneTTLSeconds + 10,
		"center":   len(snapshot.Render.Text) <= 10,
	}

	if err := a.publisher.CustomApp(ctx, a.cfg.AWTRIX.AppName, payload); err != nil {
		return fmt.Errorf("publish custom app: %w", err)
	}
	if err := a.publishIndicators(ctx, snapshot.Render); err != nil {
		return err
	}
	if a.cfg.Display.NotifyOnWaiting {
		if err := a.maybeNotifyWaiting(ctx, snapshot); err != nil {
			return err
		}
	}

	a.mu.Lock()
	a.lastPublished = snapshot.Render
	a.mu.Unlock()
	return nil
}

func (a *App) publishIndicators(ctx context.Context, render Render) error {
	waitingPayload := map[string]any{"color": "0"}
	if render.Waiting > 0 {
		waitingPayload = map[string]any{"color": "#FF0000", "blink": 500}
	}
	if err := a.publisher.Indicator(ctx, 1, waitingPayload); err != nil {
		return fmt.Errorf("publish waiting indicator: %w", err)
	}

	runningPayload := map[string]any{"color": "0"}
	if render.Running > 0 {
		runningPayload = map[string]any{"color": "#00A3FF"}
	}
	if err := a.publisher.Indicator(ctx, 2, runningPayload); err != nil {
		return fmt.Errorf("publish running indicator: %w", err)
	}

	lingerCount := render.Done + render.Errors
	lingerPayload := map[string]any{"color": "0"}
	if lingerCount > 0 {
		lingerPayload = map[string]any{"color": "#707070"}
	}
	if err := a.publisher.Indicator(ctx, 3, lingerPayload); err != nil {
		return fmt.Errorf("publish linger indicator: %w", err)
	}
	return nil
}

func (a *App) maybeNotifyWaiting(ctx context.Context, snapshot Snapshot) error {
	if snapshot.Render.Waiting == 0 {
		a.mu.Lock()
		a.lastWaitKey = ""
		a.mu.Unlock()
		return nil
	}

	key := snapshot.Render.Text
	a.mu.Lock()
	if key == a.lastWaitKey {
		a.mu.Unlock()
		return nil
	}
	a.lastWaitKey = key
	a.mu.Unlock()

	return a.publisher.Notify(ctx, map[string]any{
		"text":     snapshot.Render.Text,
		"color":    "#FF3300",
		"duration": 10,
		"wakeup":   true,
		"stack":    false,
	})
}

func (a *App) StartPublisher(ctx context.Context) {
	refresh := time.Duration(a.cfg.Display.RefreshSeconds) * time.Second
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()

	if err := a.Publish(ctx); err != nil {
		a.logger.Warn("initial publish failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.Publish(ctx); err != nil {
				a.logger.Warn("publish failed", "err", err)
			}
		}
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Snapshot())
	})

	writeMux := http.NewServeMux()
	writeMux.HandleFunc("POST /v1/status", a.handleStatus)
	writeMux.HandleFunc("DELETE /v1/status", a.handleDeleteStatus)
	writeMux.HandleFunc("POST /v1/clear", a.handleClear)
	writeMux.HandleFunc("POST /v1/notify", a.handleNotify)
	mux.Handle("/v1/", requireAuth(a.cfg.Auth.StatusToken, writeMux))

	return loggingMiddleware(a.logger, mux)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	var req StatusRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	render := a.Upsert(req)
	if err := a.Publish(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

func (a *App) handleClear(w http.ResponseWriter, r *http.Request) {
	render := a.Clear()
	if err := a.Publish(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "render": render})
}

func (a *App) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if err := a.Publish(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type NotifyRequest struct {
	Text     string `json:"text"`
	Color    string `json:"color"`
	Duration int    `json:"duration"`
	Hold     bool   `json:"hold"`
}

type DeleteRequest struct {
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
}

func (r DeleteRequest) validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if strings.TrimSpace(r.Session) == "" {
		return errors.New("session is required")
	}
	return nil
}

func (r DeleteRequest) key() string {
	return strings.TrimSpace(r.Source) + "/" + strings.ToLower(strings.TrimSpace(r.Tool)) + "/" + strings.TrimSpace(r.Session)
}

func (a *App) handleDeleteStatus(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.Delete(req.key())
	if err := a.Publish(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleNotify(w http.ResponseWriter, r *http.Request) {
	var req NotifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	if req.Color == "" {
		req.Color = "#FFFFFF"
	}
	if req.Duration <= 0 {
		req.Duration = 5
	}
	if err := a.publisher.Notify(r.Context(), map[string]any{
		"text":     req.Text,
		"color":    req.Color,
		"duration": req.Duration,
		"hold":     req.Hold,
		"wakeup":   true,
		"stack":    false,
	}); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func requireAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

type Publisher interface {
	CustomApp(ctx context.Context, name string, payload map[string]any) error
	Notify(ctx context.Context, payload map[string]any) error
	Indicator(ctx context.Context, index int, payload map[string]any) error
}

type HTTPPublisher struct {
	baseURL string
	client  *http.Client
}

func NewHTTPPublisher(cfg AWTRIXConfig) (*HTTPPublisher, error) {
	base := strings.TrimRight(cfg.HTTPBaseURL, "/")
	if base == "" {
		return nil, errors.New("awtrix.http_base_url is required for http transport")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid awtrix.http_base_url: %w", err)
	}
	return &HTTPPublisher{
		baseURL: base,
		client:  &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
	}, nil
}

func (p *HTTPPublisher) CustomApp(ctx context.Context, name string, payload map[string]any) error {
	endpoint := p.baseURL + "/api/custom?name=" + url.QueryEscape(name)
	return p.postJSON(ctx, endpoint, payload)
}

func (p *HTTPPublisher) Notify(ctx context.Context, payload map[string]any) error {
	return p.postJSON(ctx, p.baseURL+"/api/notify", payload)
}

func (p *HTTPPublisher) Indicator(ctx context.Context, index int, payload map[string]any) error {
	if index < 1 || index > 3 {
		return fmt.Errorf("indicator index must be 1-3, got %d", index)
	}
	return p.postJSON(ctx, p.baseURL+"/api/indicator"+strconv.Itoa(index), payload)
}

func (p *HTTPPublisher) postJSON(ctx context.Context, endpoint string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("awtrix http %s: %s", resp.Status, strings.TrimSpace(string(limited)))
	}
	return nil
}

func newPublisher(cfg Config) (Publisher, error) {
	return NewHTTPPublisher(cfg.AWTRIX)
}

func main() {
	configPath := flag.String("config", "", "path to config JSON file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := loadConfig(*configPath)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	publisher, err := newPublisher(cfg)
	if err != nil {
		logger.Error("create publisher failed", "err", err)
		os.Exit(1)
	}

	app := NewApp(cfg, publisher, logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go app.StartPublisher(ctx)

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("server shutdown failed", "err", err)
	}
}
