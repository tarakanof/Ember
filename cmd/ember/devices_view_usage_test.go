package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var knobCapsUsage = map[string]any{
	"view":     []int{1, 1},
	"pages":    []string{"bot"},
	"features": []string{"usage", "view_wait"},
	"limits":   map[string]any{"view_bytes": 16383, "config_bytes": 1024},
}

func putUsageAt(t *testing.T, f *viewFixture, tool string, u ToolUsage) {
	t.Helper()
	f.app.usage.Put(tool, u)
	if err := f.app.usageSeen.observe(usageKey{tool, u.Account}, u, u.Principal, u.UpdatedAt); err != nil {
		t.Fatal(err)
	}
}

func usageViewScenario(t *testing.T, f *viewFixture) {
	t.Helper()
	now := f.clk.Now()
	reset5h := now.Add(108 * time.Minute).Unix()
	week := now.Add(3 * 24 * time.Hour).Unix()
	putUsageAt(t, f, "codex", ToolUsage{Account: keyCodex, Plan: "plus", Source: "codex_stream",
		FiveHour:  &UsageWindow{UsedPercent: 12.4, ResetsAt: now.Add(4 * time.Hour).Unix()},
		SevenDay:  &UsageWindow{UsedPercent: 5, ResetsAt: week},
		UpdatedAt: now.Add(-50 * time.Minute)})
	putUsageAt(t, f, "claude", ToolUsage{Account: keyMaxA, Plan: "max", Source: "endpoint",
		FiveHour: &UsageWindow{UsedPercent: 64.4, ResetsAt: reset5h, ResetLabel: "13:48"},
		SevenDay: &UsageWindow{UsedPercent: 41, ResetsAt: week, ResetLabel: "WED"},
		Models: map[string]*UsageWindow{
			"opus": {UsedPercent: 38, ResetsAt: week}, "sonnet": {UsedPercent: 12.2, ResetsAt: week},
		},
		UpdatedAt: now.Add(-40 * time.Minute)})
	putUsageAt(t, f, "claude", ToolUsage{Account: keyMaxB, Plan: "max", Source: "endpoint",
		FiveHour:  &UsageWindow{UsedPercent: 100, ResetsAt: now.Add(-10 * time.Minute).Unix()},
		SevenDay:  &UsageWindow{UsedPercent: 77, ResetsAt: week},
		UpdatedAt: now.Add(-30 * time.Minute)})
	putUsageAt(t, f, "claude", ToolUsage{Account: keyPro, Plan: "pro", Source: "endpoint",
		FiveHour:  &UsageWindow{UsedPercent: 3},
		UpdatedAt: now.Add(-20 * time.Minute)})
	putUsageAt(t, f, "codex", ToolUsage{Account: keyCodex, Plan: "plus", Source: "codex_stream",
		FiveHour:  &UsageWindow{UsedPercent: 12.4, ResetsAt: now.Add(4 * time.Hour).Unix()},
		SevenDay:  &UsageWindow{UsedPercent: 5, ResetsAt: week},
		UpdatedAt: now.Add(-time.Minute)})
	f.app.usage.Put("claude", ToolUsage{Account: keyMaxA, Plan: "max", Source: "endpoint",
		FiveHour: &UsageWindow{UsedPercent: 64.4, ResetsAt: reset5h, ResetLabel: "13:48"},
		SevenDay: &UsageWindow{UsedPercent: 41, ResetsAt: week, ResetLabel: "WED"},
		Models: map[string]*UsageWindow{
			"opus": {UsedPercent: 38, ResetsAt: week}, "sonnet": {UsedPercent: 12.2, ResetsAt: week},
		},
		UpdatedAt: now.Add(-2 * time.Minute)})
	f.app.updateConfig(func(c *Config) {
		c.Pomodoro.Enabled, c.Weather.Enabled = false, false
		c.UsageAccounts = map[string]usageAccountPrefs{idPro: {Hidden: true}}
	})
}

func viewUsage(t *testing.T, body []byte) *knobUsage {
	t.Helper()
	var v struct {
		Usage *knobUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.Usage
}

func TestKnobViewUsageGolden(t *testing.T) {
	f := goldenViewFixture(t)
	usageViewScenario(t, f)
	postCapsCheckin(t, f, "0.10.0", knobCapsUsage)
	body := goldenView(t, f)
	assertDeviceGolden(t, "view_usage", body)
	u := viewUsage(t, body)
	var names []string
	for _, a := range u.Accounts {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "Max 1,Max 2,Plus" {
		t.Fatalf("accounts = %v, want Claude first by first seen, the hidden Pro left out", names)
	}
	a, b := u.Accounts[0], u.Accounts[1]
	if a.Stale || a.UpdatedAt != 0 || a.FiveHour.StartsAt != a.FiveHour.ResetsAt-5*3600 || len(a.Models) != 2 || a.Models[0].Name != "Opus" {
		t.Errorf("fresh account = %+v", a)
	}
	if !b.Stale || b.UpdatedAt == 0 || *b.FiveHour != (knobUsageWindow{}) {
		t.Errorf("stale account past its 5h reset = %+v, want stale, updated_at and a zeroed 5h window", b)
	}
}

func TestKnobViewWithoutTheUsageCapGolden(t *testing.T) {
	f := goldenViewFixture(t)
	usageViewScenario(t, f)
	postCapsCheckin(t, f, "0.10.0", map[string]any{
		"view": []int{1, 1}, "pages": []string{"bot"}, "features": []string{"view_wait"},
		"limits": map[string]any{"view_bytes": 16383, "config_bytes": 1024},
	})
	assertDeviceGolden(t, "view_usage_uncapped", goldenView(t, f))
}

func TestKnobViewSendsUsageOnlyWithTheUsageFeature(t *testing.T) {
	f := goldenViewFixture(t)
	usageViewScenario(t, f)
	if u := viewUsage(t, goldenView(t, f)); u != nil {
		t.Fatalf("a legacy knob got usage: %+v", u)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsCover)
	if u := viewUsage(t, goldenView(t, f)); u != nil {
		t.Fatalf("a caps knob without the usage token got usage: %+v", u)
	}
	postCapsCheckin(t, f, "0.10.0", knobCapsUsage)
	if u := viewUsage(t, goldenView(t, f)); u == nil || len(u.Accounts) != 3 {
		t.Fatalf("usage knob got %+v", u)
	}
}

func TestKnobViewLeavesOutUsageWithNoAccounts(t *testing.T) {
	f := goldenViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", knobCapsUsage)
	if _, ok := viewKeys(t, goldenView(t, f))["usage"]; ok {
		t.Fatal("usage block sent with no accounts")
	}
}

func TestKnobViewUsageModelsFollowUsagePerModel(t *testing.T) {
	f := goldenViewFixture(t)
	usageViewScenario(t, f)
	postCapsCheckin(t, f, "0.10.0", knobCapsUsage)
	off := false
	f.app.updateConfig(func(c *Config) { c.UsagePerModel = &off })
	for _, a := range viewUsage(t, goldenView(t, f)).Accounts {
		if a.Models != nil {
			t.Fatalf("models sent with usage_per_model off: %+v", a)
		}
	}
}

func TestKnobViewUsageETagIgnoresAFreshAccountsUpdateTime(t *testing.T) {
	f := goldenViewFixture(t)
	postCapsCheckin(t, f, "0.10.0", knobCapsUsage)
	now := f.clk.Now()
	u := ToolUsage{Account: keyMaxA, Plan: "max", FiveHour: &UsageWindow{UsedPercent: 20, ResetsAt: now.Add(time.Hour).Unix()}, UpdatedAt: now}
	f.app.usage.Put("claude", u)
	_, etag, err := f.app.knobView(f.m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	u.UpdatedAt = now.Add(time.Minute)
	f.app.usage.Put("claude", u)
	if _, etag2, _ := f.app.knobView(f.m.ID, now.Add(time.Minute)); etag2 != etag {
		t.Fatal("an unchanged snapshot posted again moved the ETag")
	}
	if _, etag3, _ := f.app.knobView(f.m.ID, now.Add(time.Minute+usageStaleTTL+time.Second)); etag3 == etag {
		t.Fatal("going stale left the ETag alone")
	}
}

func TestKnobViewUsageWorstCaseStaysSmall(t *testing.T) {
	f := goldenViewFixture(t)
	worstCaseView(t, f)
	plantCaps(t, f, &deviceCaps{View: []int{1, 1}, Pages: []string{"bot", "pomodoro", "weather", "nowplaying"},
		Features: []string{featureNPCover, featureWxFaces, featureUsage}, Limits: &deviceCapsLimits{ViewBytes: 16383}})
	now := f.clk.Now()
	prefs := map[string]usageAccountPrefs{}
	for i := range knobUsageMaxAccounts + 2 {
		key := strings.Repeat(string(rune('a'+i)), 16)
		tool := []string{"claude", "codex"}[i%2]
		f.app.usage.Put(tool, ToolUsage{Account: key, Plan: "enterprise",
			FiveHour: &UsageWindow{UsedPercent: 100, ResetsAt: now.Add(time.Hour).Unix()},
			SevenDay: &UsageWindow{UsedPercent: 100, ResetsAt: now.Add(time.Hour).Unix()},
			Models: map[string]*UsageWindow{
				"opus": {UsedPercent: 100}, "sonnet": {UsedPercent: 100}, "haiku": {UsedPercent: 100},
				strings.Repeat("m", 16): {UsedPercent: 100}, "<script>": {UsedPercent: 100},
			},
			UpdatedAt: now.Add(-time.Hour)})
		prefs[tool+":"+key] = usageAccountPrefs{Name: strings.Repeat("<", usageAccountNameMax)}
	}
	f.app.updateConfig(func(c *Config) { c.UsageAccounts = prefs })
	body := goldenView(t, f)
	u := viewUsage(t, body)
	if len(u.Accounts) != knobUsageMaxAccounts || len(u.Accounts[0].Models) != knobUsageMaxModels {
		t.Fatalf("worst case = %d accounts, %d models", len(u.Accounts), len(u.Accounts[0].Models))
	}
	block := len(viewKeys(t, body)["usage"])
	t.Logf("worst-case usage block: %d B, whole view %d B", block, len(body))
	if block > 4096 {
		t.Fatalf("worst-case usage block is %d B, want at most 4 KB", block)
	}
	if _, ok := viewKeys(t, body)["nowplaying"]; !ok || len(body) >= 12288 {
		t.Fatalf("worst-case view with usage is %d B (nowplaying kept: %v), want every block under 12 KB", len(body), ok)
	}
}

func TestKnobUsageFiveHourStartFollowsTheReportedWindowLength(t *testing.T) {
	app, srv := newDevicesApp(t, "")
	reset := time.Now().Add(time.Hour).Unix()
	postUsage(t, srv, fmt.Sprintf(`{"tool":"codex","account_key":"%s","five_hour":{"used_percent":12,"resets_at":%d,"window_minutes":240}}`, keyCodex, reset))
	postUsage(t, srv, fmt.Sprintf(`{"tool":"claude","account_key":"%s","five_hour":{"used_percent":12,"resets_at":%d}}`, keyMaxA, reset))
	postUsage(t, srv, fmt.Sprintf(`{"tool":"claude","account_key":"%s","five_hour":{"used_percent":12,"resets_at":%d,"window_minutes":-5}}`, keyMaxB, reset))
	got := map[string]int64{}
	for _, a := range app.knobUsage(time.Now()).Accounts {
		got[a.ID] = a.FiveHour.ResetsAt - a.FiveHour.StartsAt
	}
	if got[idCodex] != 4*3600 || got[idMaxA] != 5*3600 || got[idMaxB] != 5*3600 {
		t.Fatalf("window lengths = %v, want codex 4h from window_minutes, the rest the 5h fallback", got)
	}
}
