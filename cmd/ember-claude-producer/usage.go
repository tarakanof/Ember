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

// usageClientTimeout bounds fetchUsage's TLS+request round trip. Without it,
// one stalled connection to the endpoint hangs until the daemon restarts —
// http.DefaultClient has no Timeout.
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
	return strings.ToUpper(t.In(loc).Format("Mon")) // MON, TUE, ...
}

var claudeVersionOnce sync.Once
var claudeVersionCached string

func claudeUA() string {
	claudeVersionOnce.Do(func() {
		claudeVersionCached = defaultClaudeVersion
		if out, err := exec.Command("claude", "--version").Output(); err == nil {
			for _, f := range strings.Fields(string(out)) {
				if strings.Count(f, ".") == 2 { // first x.y.z token
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

// win converts an endpoint window into the wire window, formatting the reset
// label in loc. A nil/zero ResetsAt yields a window with just the percent.
func win(w usageWindow, loc *time.Location, label func(time.Time, *time.Location) string) *producer.UsageWindow {
	out := &producer.UsageWindow{UsedPercent: w.Utilization}
	if ts, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		out.ResetsAt = ts.Unix()
		out.ResetLabel = label(ts, loc)
	}
	return out
}

// usageModelsCache holds the most recent per-model usage breakdown
// (opus/sonnet) fetched from the OAuth endpoint. Claude Code's statusline JSON
// carries no per-model figures, so the statusline-driven /v1/usage POST
// (dispatchTick's postStatuslineUsage) forwards this cached snapshot instead
// of leaving Models nil — otherwise the server's last-write-wins storage
// (UsageStore.Put replaces the whole per-tool entry) would blank the
// per-model breakdown on the very next heartbeat, since that heartbeat runs
// every 10s while the OAuth endpoint is only polled every usagePollInterval.
//
// Process-lifetime singleton: shared state across usagePollLoop (writer) and
// dispatchTick (reader) is the whole point, same rationale as tickFailLog above.
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

// reset clears cached state; test-only (mirrors tickFailLog.Reset()).
func (c *usageModelsCache) reset() {
	c.set(nil)
}

// usagePollOnce reads creds, fetches the endpoint, and posts to the server.
// On 401 it logs and returns (never refreshes the token). On success it also
// caches the per-model breakdown in usageModels, so the more-frequent
// statusline-driven /v1/usage POST (see tick.go) can carry it forward.
func usagePollOnce(ctx context.Context, cfg Config, client *Client) {
	creds, err := readClaudeCreds()
	if err != nil || creds.AccessToken == "" {
		return // no creds — open Claude Code
	}
	u, code, err := fetchUsage(ctx, creds.AccessToken)
	if err != nil || code != http.StatusOK {
		return // 401 => open Claude Code; transient errors retried next tick
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
	if err := client.Usage(ctx, req); err != nil { // Client is producer.Client (see client.go alias)
		tickFailLog.Warn(slog.Default(), "claude_usage", "usage POST failed", "err", err)
	}
}

// postStatuslineUsage relays the freshest statusline-derived 5h and/or weekly
// windows to /v1/usage with source "statusline", merging in usageModels' last
// per-model snapshot (see usageModelsCache above) so the per-model breakdown
// survives a POST that otherwise only carries statusline data. A no-op when
// snap has neither window (nothing to relay).
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

// usagePollLoop polls the usage endpoint every usagePollInterval until ctx is
// cancelled. It reloads config each pass (like heartbeatPass) so producer.env
// edits take effect live, and polls immediately before the first tick.
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
