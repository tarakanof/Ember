package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

const usageEndpoint = "https://api.anthropic.com/api/oauth/usage"
const usagePollInterval = 5 * time.Minute
const defaultClaudeVersion = "2.1.0"

const usageClientTimeout = 30 * time.Second

var usageHTTPClient = &http.Client{Timeout: usageClientTimeout}

type usageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type usageResponse struct {
	FiveHour       usageWindow `json:"five_hour"`
	SevenDay       usageWindow `json:"seven_day"`
	SevenDayOpus   usageWindow `json:"seven_day_opus"`
	SevenDaySonnet usageWindow `json:"seven_day_sonnet"`
}

func parseUsageResponse(b []byte) (usageResponse, error) {
	var u usageResponse
	err := json.Unmarshal(b, &u)
	return u, err
}

func clockLabel(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }

func dayLabel(t time.Time, loc *time.Location) string {
	return strings.ToUpper(t.In(loc).Format("Mon"))
}

var claudeVersionOnce sync.Once
var claudeVersionCached string

func claudeUA() string {
	claudeVersionOnce.Do(func() {
		claudeVersionCached = defaultClaudeVersion
		if out, err := exec.Command("claude", "--version").Output(); err == nil {
			for _, f := range strings.Fields(string(out)) {
				if strings.Count(f, ".") == 2 {
					claudeVersionCached = f
					break
				}
			}
		}
	})
	return "claude-code/" + claudeVersionCached
}

func fetchUsage(ctx context.Context, token string) (usageResponse, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", claudeUA())
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := usageHTTPClient.Do(req)
	if err != nil {
		return usageResponse{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usageResponse{}, resp.StatusCode, nil
	}
	b, _ := io.ReadAll(resp.Body)
	u, err := parseUsageResponse(b)
	return u, resp.StatusCode, err
}

func win(w usageWindow, loc *time.Location, label func(time.Time, *time.Location) string) *producer.UsageWindow {
	out := &producer.UsageWindow{UsedPercent: w.Utilization}
	if ts, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		out.ResetsAt = ts.Unix()
		out.ResetLabel = label(ts, loc)
	}
	return out
}

type usageModelsCache struct {
	mu     sync.Mutex
	models map[string]*producer.UsageWindow
}

var usageModels = &usageModelsCache{}

func (c *usageModelsCache) set(m map[string]*producer.UsageWindow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = m
}

func (c *usageModelsCache) get() map[string]*producer.UsageWindow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models
}

func (c *usageModelsCache) reset() {
	c.set(nil)
}

func usagePollOnce(ctx context.Context, cfg Config, client *Client) {
	creds, err := readClaudeCreds()
	if err != nil || creds.AccessToken == "" {
		return
	}
	u, code, err := fetchUsage(ctx, creds.AccessToken)
	if err != nil || code != http.StatusOK {
		return
	}
	loc := time.Now().Location()
	req := producer.UsageRequest{
		Tool:     "claude",
		Source:   "endpoint",
		FiveHour: win(u.FiveHour, loc, clockLabel),
		SevenDay: win(u.SevenDay, loc, dayLabel),
		Models: map[string]*producer.UsageWindow{
			"opus":   win(u.SevenDayOpus, loc, dayLabel),
			"sonnet": win(u.SevenDaySonnet, loc, dayLabel),
		},
	}
	usageModels.set(req.Models)
	if err := client.Usage(ctx, req); err != nil {
		tickFailLog.Warn(slog.Default(), "claude_usage", "usage POST failed", "err", err)
	}
}

func postStatuslineUsage(ctx context.Context, client *Client, snap statuslineUsageSnapshot) {
	req := producer.UsageRequest{
		Tool:   "claude",
		Source: "statusline",
		Models: usageModels.get(),
	}
	if snap.fiveHourPct != nil {
		req.FiveHour = &producer.UsageWindow{
			UsedPercent: float64(*snap.fiveHourPct),
			ResetsAt:    snap.fiveHourResetAt,
			ResetLabel:  snap.fiveHourResetLabel,
		}
	}
	if snap.sevenDayPct != nil {
		req.SevenDay = &producer.UsageWindow{
			UsedPercent: float64(*snap.sevenDayPct),
			ResetsAt:    snap.sevenDayResetAt,
			ResetLabel:  snap.sevenDayResetLabel,
		}
	}
	if req.FiveHour == nil && req.SevenDay == nil {
		return
	}
	if err := client.Usage(ctx, req); err != nil {
		tickFailLog.Warn(slog.Default(), "claude_usage_statusline", "usage POST failed", "err", err)
	}
}

func usagePollLoop(ctx context.Context) {
	t := time.NewTicker(usagePollInterval)
	defer t.Stop()
	for {
		if cfg, err := loadConfig(); err == nil && cfg.ServerURL != "" {
			usagePollOnce(ctx, cfg, NewDaemonClient(cfg))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
