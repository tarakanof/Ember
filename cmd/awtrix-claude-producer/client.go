package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type StatusRequest struct {
	Source      string  `json:"source"`
	Tool        string  `json:"tool"`
	Session     string  `json:"session"`
	State       string  `json:"state"`
	Message     string  `json:"message,omitempty"`
	TokensToday int64   `json:"tokens_today,omitempty"`
	ContextPct  *int    `json:"context_pct,omitempty"`
	SourceColor *string `json:"source_color,omitempty"`
}

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

func NewClient(cfg Config) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.HookTimeoutMs) * time.Millisecond,
		},
		serverURL: cfg.ServerURL,
		token:     cfg.Token,
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
