package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type statuslineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       int64   `json:"resets_at"`
		} `json:"five_hour"`
		// SevenDay is the weekly rate-limit window, shaped like five_hour and parsed leniently.
		SevenDay *struct {
			UsedPercentage float64 `json:"used_percentage"`
			ResetsAt       int64   `json:"resets_at"`
		} `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow *struct {
		// UsedPercentage is null early in a session: unknown, not 0 %.
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
}

func parseStatusline(b []byte) (statuslineInput, bool) {
	var in statuslineInput
	if json.Unmarshal(b, &in) != nil {
		return statuslineInput{}, false
	}
	return in, true
}

func extractRatePct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil {
		return nil, false
	}
	pct := int(math.Round(in.RateLimits.FiveHour.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

func extractRateResetAt(in statuslineInput) (int64, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil || in.RateLimits.FiveHour.ResetsAt <= 0 {
		return 0, false
	}
	return in.RateLimits.FiveHour.ResetsAt, true
}

func extractRateResetLabel(in statuslineInput) (string, bool) {
	at, ok := extractRateResetAt(in)
	if !ok {
		return "", false
	}
	return time.Unix(at, 0).Local().Format("15:04"), true
}

func extractWeekPct(in statuslineInput) (*int, bool) {
	if in.RateLimits == nil || in.RateLimits.SevenDay == nil {
		return nil, false
	}
	pct := int(math.Round(in.RateLimits.SevenDay.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

func extractWeekResetAt(in statuslineInput) (int64, bool) {
	if in.RateLimits == nil || in.RateLimits.SevenDay == nil || in.RateLimits.SevenDay.ResetsAt <= 0 {
		return 0, false
	}
	return in.RateLimits.SevenDay.ResetsAt, true
}

func extractWeekResetLabel(in statuslineInput) (string, bool) {
	at, ok := extractWeekResetAt(in)
	if !ok {
		return "", false
	}
	return dayLabel(time.Unix(at, 0), time.Local), true
}

func extractContextPct(in statuslineInput) (*int, bool) {
	if in.ContextWindow == nil || in.ContextWindow.UsedPercentage == nil {
		return nil, false
	}
	pct := int(math.Round(*in.ContextWindow.UsedPercentage))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct, true
}

func contextPctEnabled() bool {
	path, err := envFilePath()
	if err != nil {
		return true
	}
	data, err := producer.ReadEnvFile(path)
	if err != nil {
		return true
	}
	return producer.Bool(data["EMBER_CONTEXT_PCT_ENABLED"], true)
}

func wrappedStatuslinePath(home string) string {
	return filepath.Join(home, ".config", "ember", "wrapped-statusline.json")
}

func readWrappedCommand(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, t != ""
	case map[string]any:
		if c, ok := t["command"].(string); ok && c != "" {
			return c, true
		}
	}
	return "", false
}

func runWrapped(command string, stdin []byte) ([]byte, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Output()
}

func ourStatuslineCommand(binPath string) string {
	return binPath + ` statusline 2>>` + producer.LogDirShell() + `/ember-claude-producer.log`
}

func statusLineIsOurs(v any) bool {
	raw, err := json.Marshal(v)
	if err != nil {
		return false
	}
	s := string(raw)
	return strings.Contains(s, producerName+" statusline") ||
		strings.Contains(s, legacyProducerName+" statusline")
}

func runStatusline() {
	buf, _ := io.ReadAll(os.Stdin)
	if in, ok := parseStatusline(buf); ok {
		var ratePct, ctxPct, weekPct *int
		var resetAt, weekResetAt *int64
		var resetLabel, weekResetLabel string
		if p, ok := extractRatePct(in); ok {
			ratePct = p
		}
		if r, ok := extractRateResetAt(in); ok {
			resetAt = &r
		}
		if l, ok := extractRateResetLabel(in); ok {
			resetLabel = l
		}
		if p, ok := extractWeekPct(in); ok {
			weekPct = p
		}
		if r, ok := extractWeekResetAt(in); ok {
			weekResetAt = &r
		}
		if l, ok := extractWeekResetLabel(in); ok {
			weekResetLabel = l
		}
		if contextPctEnabled() {
			if p, ok := extractContextPct(in); ok {
				ctxPct = p
			}
		}
		if ratePct != nil || ctxPct != nil || resetAt != nil || resetLabel != "" ||
			weekPct != nil || weekResetAt != nil || weekResetLabel != "" {
			if dir, err := stateDir(); err == nil {
				_ = enrichMarker(dir, sanitizeSessionID(in.SessionID, in.Cwd),
					ratePct, ctxPct, resetAt, resetLabel, weekPct, weekResetAt, weekResetLabel)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if cmd, ok := readWrappedCommand(wrappedStatuslinePath(home)); ok {
			if out, err := runWrapped(cmd, buf); err == nil {
				_, _ = os.Stdout.Write(out)
			}
		}
	}
	os.Exit(0)
}

const statuslineLockWait = 250 * time.Millisecond

func enrichMarker(stateDir, sessionID string, ratePct, ctxPct *int, resetAt *int64, resetLabel string,
	weekPct *int, weekResetAt *int64, weekResetLabel string) error {
	mp := markerPath(stateDir, sessionID)
	lp := lockPath(stateDir, sessionID)
	// The status line renders synchronously: drop this sample rather than
	// wait behind a hook's in-flight POST; the next refresh carries it.
	return withLockExWait(lp, statuslineLockWait, func() error {
		body, err := readMarker(mp)
		if err != nil {
			return nil
		}
		var m marker
		if json.Unmarshal(body, &m) != nil {
			return nil
		}
		if ratePct != nil {
			m.RateWindowPct = ratePct
		}
		if ctxPct != nil {
			m.ContextPct = ctxPct
		}
		if resetAt != nil {
			m.RateResetAt = *resetAt
		}
		if resetLabel != "" {
			m.RateResetLabel = resetLabel
		}
		if weekPct != nil {
			m.RateWeekPct = weekPct
		}
		if weekResetAt != nil {
			m.RateWeekResetAt = *weekResetAt
		}
		if weekResetLabel != "" {
			m.RateWeekResetLabel = weekResetLabel
		}
		out, err := json.Marshal(m)
		if err != nil {
			return nil
		}
		return writeMarker(mp, out)
	})
}
