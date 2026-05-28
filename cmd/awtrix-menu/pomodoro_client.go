package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// pomodoroClient talks to the awtrix-ai-status Pomodoro HTTP API. Server URL and
// bearer token come from producer.env (STATUS_SERVER_URL / STATUS_TOKEN).
type pomodoroClient struct {
	baseURL string
	token   string
	hc      *http.Client
}

func newPomodoroClient(serverURL, token string) *pomodoroClient {
	return &pomodoroClient{
		baseURL: strings.TrimRight(serverURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 4 * time.Second},
	}
}

type pomoState struct {
	Phase        string `json:"phase"`
	Running      bool   `json:"running"`
	Paused       bool   `json:"paused"`
	RemainingSec int    `json:"remaining_sec"`
	PlannedSec   int    `json:"planned_sec"`
	Round        int    `json:"round"`
}

type pomoDayStat struct {
	Date           string `json:"date"`
	CompletedFocus int    `json:"completed_focus"`
	FocusMin       int    `json:"focus_min"`
}

type pomoStats struct {
	Today   pomoDayStat   `json:"today"`
	History []pomoDayStat `json:"history"`
	Streak  int           `json:"streak"`
}

// pomoConfig mirrors the server's pomodoroSettingsDTO.
type pomoConfig struct {
	FocusMinutes          int    `json:"focus_minutes"`
	ShortBreakMinutes     int    `json:"short_break_minutes"`
	LongBreakMinutes      int    `json:"long_break_minutes"`
	RoundsBeforeLongBreak int    `json:"rounds_before_long_break"`
	AutoStartNext         bool   `json:"auto_start_next"`
	Sound                 bool   `json:"sound"`
	SoundMelody           string `json:"sound_melody"`
	FocusColor            string `json:"focus_color"`
	BreakColor            string `json:"break_color"`
}

func (c *pomodoroClient) do(method, path string, body any, out any) error {
	if c.baseURL == "" {
		return fmt.Errorf("server URL not configured")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(limited)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// State fetches the current engine status (open endpoint).
func (c *pomodoroClient) State() (pomoState, error) {
	var st pomoState
	err := c.do(http.MethodGet, "/v1/pomodoro/state", nil, &st)
	return st, err
}

// Stats fetches the today/history/streak rollup (open endpoint).
func (c *pomodoroClient) Stats() (pomoStats, error) {
	var s pomoStats
	err := c.do(http.MethodGet, "/v1/pomodoro/stats", nil, &s)
	return s, err
}

// GetConfig fetches the runtime settings (bearer auth).
func (c *pomodoroClient) GetConfig() (pomoConfig, error) {
	var cfg pomoConfig
	err := c.do(http.MethodGet, "/v1/pomodoro/config", nil, &cfg)
	return cfg, err
}

// PutConfig writes the runtime settings (bearer auth).
func (c *pomodoroClient) PutConfig(cfg pomoConfig) error {
	return c.do(http.MethodPut, "/v1/pomodoro/config", cfg, nil)
}

// Action posts a control command: start, pause, resume, stop, skip (bearer auth).
func (c *pomodoroClient) Action(name string) error {
	switch name {
	case "start", "pause", "resume", "stop", "skip":
		return c.do(http.MethodPost, "/v1/pomodoro/"+name, nil, nil)
	default:
		return fmt.Errorf("unknown pomodoro action %q", name)
	}
}
