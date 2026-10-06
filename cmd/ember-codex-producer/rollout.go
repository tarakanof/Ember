package main

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/producer"
)

type derived struct {
	state         string
	message       string
	contextPct    *int
	rateWindowPct *int
	activity      string
	rateResetAt   int64
	weeklyPct     *int
	weeklyResetAt int64
	weeklyRaw     float64
	primaryRaw    float64
	rateAt        time.Time
}

func (d *derived) expireWindows(now time.Time) {
	if d.rateResetAt != 0 && d.rateResetAt < now.Unix() {
		d.rateWindowPct, d.rateResetAt, d.primaryRaw = nil, 0, 0
	}
	if d.weeklyResetAt != 0 && d.weeklyResetAt < now.Unix() {
		d.weeklyPct, d.weeklyResetAt, d.weeklyRaw = nil, 0, 0
	}
}

type sessionMeta struct {
	id         string
	source     string
	originator string
}

const claudeOriginator = "Claude Code"

type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type metaPayload struct {
	ID         string          `json:"id"`
	Originator string          `json:"originator"`
	Source     json.RawMessage `json:"source"`
}

type tokenInfo struct {
	LastTokenUsage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"last_token_usage"`
	ModelContextWindow int `json:"model_context_window"`
}

type rateWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
}

type eventPayload struct {
	Type       string     `json:"type"`
	Message    string     `json:"message"`
	Info       *tokenInfo `json:"info"`
	RateLimits *struct {
		LimitID   string      `json:"limit_id"`
		Primary   *rateWindow `json:"primary"`
		Secondary *rateWindow `json:"secondary"`
	} `json:"rate_limits"`
	Reason           string                     `json:"reason,omitempty"`
	LastAgentMessage string                     `json:"last_agent_message,omitempty"`
	Item             *turnItem                  `json:"item,omitempty"`
	Command          []string                   `json:"command,omitempty"`
	Changes          map[string]json.RawMessage `json:"changes,omitempty"`
	Query            string                     `json:"query,omitempty"`
	Invocation       *struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
	} `json:"invocation,omitempty"`
}

func parseSessionMeta(line []byte) (sessionMeta, bool) {
	var rl rolloutLine
	if json.Unmarshal(line, &rl) != nil || rl.Type != "session_meta" {
		return sessionMeta{}, false
	}
	var p metaPayload
	if json.Unmarshal(rl.Payload, &p) != nil || p.ID == "" {
		return sessionMeta{}, false
	}
	return sessionMeta{id: p.ID, source: sourceKind(p.Source), originator: p.Originator}, true
}

func sourceKind(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && len(obj) == 1 {
		for k := range obj {
			return k
		}
	}
	return ""
}

func isRunningEvent(t string) bool {
	switch t {
	case "task_started", "user_message", "agent_message", "stream_error",
		"exec_command_end", "web_search_end", "mcp_tool_call_end",
		"patch_apply_end", "context_compacted":
		return true
	}
	return false
}

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
			pct := producer.Pct(100 * float64(p.Info.LastTokenUsage.InputTokens) / float64(p.Info.ModelContextWindow))
			d.contextPct = &pct
		}
		if lim := p.RateLimits; ratePctEnabled && lim != nil && (lim.LimitID == "" || lim.LimitID == "codex") {
			if w := lim.Primary; w != nil {
				r := producer.Pct(w.UsedPercent)
				d.rateWindowPct = &r
				d.rateResetAt = w.ResetsAt
				d.primaryRaw = w.UsedPercent
			}
			if w := lim.Secondary; w != nil {
				wk := producer.Pct(w.UsedPercent)
				d.weeklyPct = &wk
				d.weeklyResetAt = w.ResetsAt
				d.weeklyRaw = w.UsedPercent
			}
			if ts, err := time.Parse(time.RFC3339Nano, rl.Timestamp); err == nil {
				d.rateAt = ts
			}
		}
	case p.Type == "task_complete":
		d.state = "done"
		if m := strings.TrimSpace(p.LastAgentMessage); m != "" {
			d.message = truncate(m, 80)
		}
	case strings.HasSuffix(p.Type, "_approval_request"):
		d.state = "waiting"
	case p.Type == "turn_aborted":
		if p.Reason == "budget_limited" {
			d.state = "error"
		} else {
			d.state = "done"
		}
	case p.Type == "error":
		d.state = "error"
	case p.Type == "item_completed" && p.Item != nil:
		if p.Item.Type != "SubAgentActivity" && d.state != "done" && d.state != "error" {
			d.state = "running"
		}
		if p.Item.Type == "AgentMessage" {
			if m := strings.TrimSpace(p.Item.text()); m != "" {
				d.message = truncate(m, 80)
			}
		}
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
		} else if p.Type == "item_completed" && p.Item != nil {
			if label, ok := labelForItem(*p.Item); ok {
				d.activity = producer.PrependTrail(label, d.activity)
			}
		} else if label, ok := labelForEvent(p); ok {
			d.activity = producer.PrependTrail(label, d.activity)
		}
	}
}

func truncate(s string, n int) string {
	return producer.Truncate(s, n)
}

func labelForEvent(p eventPayload) (string, bool) {
	switch p.Type {
	case "exec_command_begin":
		return execLabel(commandText(p.Command)), true
	case "patch_apply_end":
		return editLabel(p.Changes), true
	case "web_search_end":
		return prefixed("web", p.Query), true
	case "mcp_tool_call_end":
		if p.Invocation != nil {
			return prefixed("mcp", p.Invocation.Tool), true
		}
		return "mcp", true
	}
	return "", false
}

type turnItem struct {
	Type    string `json:"type"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
	Command json.RawMessage            `json:"command,omitempty"`
	Status  string                     `json:"status,omitempty"`
	Changes map[string]json.RawMessage `json:"changes,omitempty"`
	Tool    string                     `json:"tool,omitempty"`
	Query   string                     `json:"query,omitempty"`
	Kind    string                     `json:"kind,omitempty"`
}

func (it turnItem) text() string {
	var parts []string
	for _, c := range it.Content {
		if t := strings.TrimSpace(c.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func labelForItem(it turnItem) (string, bool) {
	switch it.Type {
	case "CommandExecution":
		var argv []string
		var cmd string
		if json.Unmarshal(it.Command, &argv) == nil {
			cmd = commandText(argv)
		} else if json.Unmarshal(it.Command, &cmd) == nil {
			cmd = firstLine(cmd)
		}
		label := execLabel(cmd)
		if it.Status == "failed" {
			label += " (failed)"
		}
		return label, true
	case "FileChange":
		return editLabel(it.Changes), true
	case "McpToolCall":
		return prefixed("mcp", it.Tool), true
	case "WebSearch":
		return prefixed("web", it.Query), true
	case "Extension":
		// Codex 0.160 records web search as an Extension item, not WebSearch.
		switch it.Kind {
		case "web.search":
			return prefixed("web", it.Query), true
		case "clock.sleep":
			return "sleep", true
		}
		return "", false
	case "CollabAgentToolCall":
		return prefixed("agent", it.Tool), true
	case "ContextCompaction":
		return "compact", true
	}
	return "", false
}

func commandText(argv []string) string {
	if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") {
		switch filepath.Base(argv[0]) {
		case "sh", "bash", "zsh", "dash", "fish":
			return firstLine(argv[2])
		}
	}
	return strings.TrimSpace(strings.Join(argv, " "))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func execLabel(cmd string) string {
	return prefixed("exec", cmd)
}

func prefixed(kind, detail string) string {
	if d := strings.TrimSpace(detail); d != "" {
		return kind + ": " + d
	}
	return kind
}

func editLabel(changes map[string]json.RawMessage) string {
	if len(changes) == 0 {
		return "edit"
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	label := "edit: " + filepath.Base(keys[0])
	if len(keys) > 1 {
		label += " +" + strconv.Itoa(len(keys)-1)
	}
	return label
}
