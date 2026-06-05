// Package producer holds the shared HTTP client, env-file parser, and log
// rotation used by both the Claude and Codex ember producers.
package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// StatusRequest is the POST /v1/status body. Pointer fields are omitted when nil.
type StatusRequest struct {
	Source        string  `json:"source"`
	Tool          string  `json:"tool"`
	Session       string  `json:"session"`
	State         string  `json:"state"`
	Message       string  `json:"message,omitempty"`
	TokensToday   int64   `json:"tokens_today,omitempty"`
	ContextPct    *int    `json:"context_pct,omitempty"`
	SourceColor   *string `json:"source_color,omitempty"`
	RateWindowPct *int    `json:"rate_window_pct,omitempty"`
	Activity      string  `json:"activity,omitempty"`
	ContextNumber bool    `json:"context_number,omitempty"`
	RateBottomBar bool    `json:"rate_bottom_bar,omitempty"`
	RateResetAt   int64   `json:"rate_reset_at,omitempty"`
	RateReset     bool    `json:"rate_reset,omitempty"`
	// RateResetLabel is the host-local "HH:MM" 5h-reset label set by the Claude
	// statusline path. It exists so the server (which runs UTC in the container)
	// can render a correct local label in the usage-widget 5h fallback without
	// doing timezone math itself.
	RateResetLabel string `json:"rate_reset_label,omitempty"`
}

// DeleteRequest is the DELETE /v1/status body.
type DeleteRequest struct {
	Source  string `json:"source"`
	Tool    string `json:"tool"`
	Session string `json:"session"`
}

type Client struct {
	httpClient *http.Client
	serverURL  string
	token      string
}

// NewClient builds a Client. token may be empty (no Authorization header sent).
func NewClient(serverURL, token string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		serverURL:  serverURL,
		token:      token,
	}
}

func (c *Client) Post(ctx context.Context, req StatusRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.send(ctx, http.MethodPost, "/v1/status", body)
}

func (c *Client) Delete(ctx context.Context, req DeleteRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.send(ctx, http.MethodDelete, "/v1/status", body)
}

// UsageWindow is one usage window (5h or weekly). ResetsAt is unix epoch
// seconds; ResetLabel is the host-local display string ("14:25" or "MON").
type UsageWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
	ResetLabel  string  `json:"reset_label,omitempty"`
}

// UsageRequest is the POST /v1/usage body. Tool is "claude" or "codex".
// Source is "endpoint" | "statusline" | "codex_stream". Models is Claude-only.
type UsageRequest struct {
	Tool     string                  `json:"tool"`
	Source   string                  `json:"source"`
	FiveHour *UsageWindow            `json:"five_hour,omitempty"`
	SevenDay *UsageWindow            `json:"seven_day,omitempty"`
	Models   map[string]*UsageWindow `json:"models,omitempty"` // "opus","sonnet"
}

func (c *Client) Usage(ctx context.Context, req UsageRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.send(ctx, http.MethodPost, "/v1/usage", body)
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) error {
	if c.serverURL == "" {
		return errors.New("server URL not configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.serverURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("server returned %s", resp.Status)
}
