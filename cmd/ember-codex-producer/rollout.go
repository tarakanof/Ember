package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tarakanof/ember/internal/producer"
)

// derived is the state + metrics folded from a session's rollout events.
type derived struct {
	state         string // "", running, waiting, done, error
	message       string
	contextPct    *int
	rateWindowPct *int
	activity      string
	rateResetAt   int64
	// weekly (secondary) rate window, surfaced to the usage widget. weeklyPct is
	// the clamped int; weeklyRaw/primaryRaw keep float precision for the usage
	// payload (the status card uses the clamped ints).
	weeklyPct     *int
	weeklyResetAt int64
	weeklyRaw     float64
	primaryRaw    float64
}

type sessionMeta struct {
	id     string
	source string
}

type rolloutLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type metaPayload struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type tokenInfo struct {
	LastTokenUsage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"last_token_usage"`
	ModelContextWindow int `json:"model_context_window"`
}

type eventPayload struct {
	Type       string     `json:"type"`
	Message    string     `json:"message"`
	Info       *tokenInfo `json:"info"`
	RateLimits *struct {
		Primary struct {
			UsedPercent float64 `json:"used_percent"`
			ResetsAt    int64   `json:"resets_at"`
		} `json:"primary"`
		Secondary struct {
			UsedPercent float64 `json:"used_percent"`
			ResetsAt    int64   `json:"resets_at"`
		} `json:"secondary"`
	} `json:"rate_limits"`
	Command    []string                   `json:"command,omitempty"`
	Changes    map[string]json.RawMessage `json:"changes,omitempty"`
	Query      string                     `json:"query,omitempty"`
	Invocation *struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
	} `json:"invocation,omitempty"`
}

// parseSessionMeta extracts the session UUID + source from a session_meta line.
func parseSessionMeta(line []byte) (sessionMeta, bool) {
	var rl rolloutLine
	if json.Unmarshal(line, &rl) != nil || rl.Type != "session_meta" {
		return sessionMeta{}, false
	}
	var p metaPayload
	if json.Unmarshal(rl.Payload, &p) != nil || p.ID == "" {
		return sessionMeta{}, false
	}
	return sessionMeta{id: p.ID, source: p.Source}, true
}

func isRunningEvent(t string) bool {
	switch t {
	case "task_started", "user_message", "agent_message",
		"exec_command_end", "web_search_end", "mcp_tool_call_end",
		"patch_apply_end", "context_compacted":
		return true
	}
	return false
}

// foldEvent applies one rollout line to d. Non-event_msg lines are ignored.
// token_count is data-only (updates metrics, never state). contextPctEnabled
// gates context_pct; ratePctEnabled gates rate_window_pct; trailEnabled gates
// activity trail accumulation.
func (d *derived) foldEvent(line []byte, contextPctEnabled, ratePctEnabled, trailEnabled bool) {
	var rl rolloutLine
	if json.Unmarshal(line, &rl) != nil || rl.Type != "event_msg" {
		return
	}
	var p eventPayload
	if json.Unmarshal(rl.Payload, &p) != nil {
		return
	}
	switch {
	case p.Type == "token_count":
		if contextPctEnabled && p.Info != nil && p.Info.ModelContextWindow > 0 {
			pct := clampPct(int(math.Round(100 * float64(p.Info.LastTokenUsage.InputTokens) / float64(p.Info.ModelContextWindow))))
			d.contextPct = &pct
		}
		if ratePctEnabled && p.RateLimits != nil {
			r := clampPct(int(math.Round(p.RateLimits.Primary.UsedPercent)))
			d.rateWindowPct = &r
			d.rateResetAt = p.RateLimits.Primary.ResetsAt
			d.primaryRaw = p.RateLimits.Primary.UsedPercent
			wk := clampPct(int(math.Round(p.RateLimits.Secondary.UsedPercent)))
			d.weeklyPct = &wk
			d.weeklyResetAt = p.RateLimits.Secondary.ResetsAt
			d.weeklyRaw = p.RateLimits.Secondary.UsedPercent
		}
	case p.Type == "task_complete":
		d.state = "done"
	case strings.HasSuffix(p.Type, "_approval_request"):
		d.state = "waiting"
	case strings.Contains(p.Type, "error") || p.Type == "turn_aborted":
		d.state = "error"
	case isRunningEvent(p.Type):
		d.state = "running"
		if p.Type == "agent_message" {
			if m := strings.TrimSpace(p.Message); m != "" {
				d.message = truncate(m, 80)
			}
		}
	}
	if trailEnabled {
		if p.Type == "task_started" {
			d.activity = ""
		} else if label, ok := labelForEvent(p); ok {
			d.activity = producer.PrependTrail(label, d.activity)
		}
	}
}

func clampPct(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// truncate is rune-safe: see producer.Truncate.
func truncate(s string, n int) string {
	return producer.Truncate(s, n)
}

// labelForEvent returns a short trail label for a detail-bearing action event,
// or ("", false) for non-action events. Missing detail fields degrade to a
// coarse verb so payload-shape drift never drops an action entirely.
func labelForEvent(p eventPayload) (string, bool) {
	switch p.Type {
	case "exec_command_begin":
		if cmd := strings.TrimSpace(strings.Join(p.Command, " ")); cmd != "" {
			return "exec: " + cmd, true
		}
		return "exec", true
	case "patch_apply_end":
		if len(p.Changes) > 0 {
			keys := make([]string, 0, len(p.Changes))
			for k := range p.Changes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			label := "edit: " + filepath.Base(keys[0])
			if len(keys) > 1 {
				label += " +" + strconv.Itoa(len(keys)-1)
			}
			return label, true
		}
		return "edit", true
	case "web_search_end":
		if q := strings.TrimSpace(p.Query); q != "" {
			return "web: " + q, true
		}
		return "web", true
	case "mcp_tool_call_end":
		if p.Invocation != nil && p.Invocation.Tool != "" {
			return "mcp: " + p.Invocation.Tool, true
		}
		return "mcp", true
	}
	return "", false
}
