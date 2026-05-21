package main

import (
	"encoding/json"
	"math"
	"strings"
)

// derived is the state + metrics folded from a session's rollout events.
type derived struct {
	state         string // "", running, waiting, done, error
	message       string
	contextPct    *int
	rateWindowPct *int
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
		} `json:"primary"`
	} `json:"rate_limits"`
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
// gates context_pct only; rate_window_pct is always captured.
func (d *derived) foldEvent(line []byte, contextPctEnabled bool) {
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
		if p.RateLimits != nil {
			r := clampPct(int(math.Round(p.RateLimits.Primary.UsedPercent)))
			d.rateWindowPct = &r
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

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}
