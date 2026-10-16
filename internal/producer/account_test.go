package producer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountKeyIsShortHexAndHidesTheIDs(t *testing.T) {
	k := AccountKey("claude", "acct-uuid-1", "org-uuid-1")
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(k) {
		t.Fatalf("key = %q, want 16 hex characters", k)
	}
	if strings.Contains(k, "acct") || strings.Contains(k, "org") {
		t.Fatalf("key %q leaks an id", k)
	}
	if AccountKey("claude", "acct-uuid-1", "org-uuid-1") != k {
		t.Fatal("key is not stable")
	}
	for _, other := range []string{
		AccountKey("codex", "acct-uuid-1", "org-uuid-1"),
		AccountKey("claude", "acct-uuid-1", "org-uuid-2"),
		AccountKey("claude", "acct-uuid-1"),
	} {
		if other == k {
			t.Fatalf("different input gave the same key %q", k)
		}
	}
}

func TestAccountKeyNeedsAnID(t *testing.T) {
	if k := AccountKey("claude"); k != "" {
		t.Fatalf("no ids gave %q", k)
	}
	if k := AccountKey("claude", " ", "org"); k != "" {
		t.Fatalf("blank account id gave %q", k)
	}
}

func TestPlanNormalisesOrDrops(t *testing.T) {
	cases := map[string]string{
		"Max": "max", " pro ": "pro", "team": "team", "max-20x": "max_20x",
		"": "", "a@b.c": "", strings.Repeat("x", 25): "",
	}
	for in, want := range cases {
		if got := Plan(in); got != want {
			t.Errorf("Plan(%q) = %q, want %q", in, got, want)
		}
	}
}

type strictWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
	ResetLabel  string  `json:"reset_label"`
}

type strictUsage struct {
	Tool     string                   `json:"tool"`
	Source   string                   `json:"source"`
	FiveHour *strictWindow            `json:"five_hour"`
	SevenDay *strictWindow            `json:"seven_day"`
	Models   map[string]*strictWindow `json:"models"`
}

func resetOptionalUsageFields(t *testing.T) {
	t.Helper()
	reset := func() {
		optionalUsageFields.mu.Lock()
		optionalUsageFields.until = map[string]time.Time{}
		optionalUsageFields.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestUsageResendsWithoutAccountFieldsToAStrictOldServer(t *testing.T) {
	resetOptionalUsageFields(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var u strictUsage
		err := dec.Decode(&u)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			seen = append(seen, "400")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		seen = append(seen, "204")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "", time.Second)
	req := UsageRequest{Tool: "claude", Source: "endpoint", AccountKey: "0123456789abcdef", Plan: "max",
		FiveHour: &UsageWindow{UsedPercent: 10}}
	if err := c.Usage(context.Background(), req); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if err := c.Usage(context.Background(), req); err != nil {
		t.Fatalf("second usage: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "400,400,204,204" {
		t.Fatalf("requests = %v, want two 400s (one per new field), then 204s with both remembered", seen)
	}
}

func TestUsageResendsWithoutWindowMinutesToAnOldServer(t *testing.T) {
	resetOptionalUsageFields(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var u struct {
			strictUsage
			AccountKey string `json:"account_key"`
			Plan       string `json:"plan"`
		}
		err := dec.Decode(&u)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			seen = append(seen, "400")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		seen = append(seen, "204")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	five := &UsageWindow{UsedPercent: 10, ResetsAt: 1782049680, WindowMinutes: 240}
	req := UsageRequest{Tool: "codex", Source: "codex_stream", FiveHour: five,
		SevenDay: &UsageWindow{UsedPercent: 3, WindowMinutes: 10080}}
	if err := NewClient(srv.URL, "", time.Second).Usage(context.Background(), req); err != nil {
		t.Fatalf("usage: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "400,204" {
		t.Fatalf("requests = %v, want one 400 then a 204 without window_minutes", seen)
	}
	if five.WindowMinutes != 240 {
		t.Fatal("dropping the field changed the caller's window")
	}
}
