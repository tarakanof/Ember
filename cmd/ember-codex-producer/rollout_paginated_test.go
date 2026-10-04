package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The fixture is a sanitized codex-cli 0.160.0 rollout (history_mode "paginated"):
// state and tool activity arrive only as item_completed TurnItems.
func foldFixture(t *testing.T, name string) (derived, []derived) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var d derived
	var steps []derived
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		d.foldEvent([]byte(ln), true, true, true)
		steps = append(steps, d)
	}
	return d, steps
}

func TestFold_PaginatedRolloutFixture(t *testing.T) {
	d, steps := foldFixture(t, "rollout-paginated-0.160.jsonl")
	if s := steps[3]; s.state != "running" {
		t.Errorf("after task_started state = %q, want running", s.state)
	}
	if s := steps[7]; s.state != "running" || s.message != "Looking at the watcher." {
		t.Errorf("after commentary AgentMessage = state %q message %q", s.state, s.message)
	}
	if d.state != "done" {
		t.Errorf("final state = %q, want done", d.state)
	}
	if d.message != "Fixed; vet still fails." {
		t.Errorf("final message = %q", d.message)
	}
	if d.contextPct == nil || *d.contextPct != 50 {
		t.Errorf("contextPct = %v, want 50", d.contextPct)
	}
	if d.rateWindowPct == nil || *d.rateWindowPct != 13 {
		t.Errorf("rateWindowPct = %v, want 13 (premium limit ignored)", d.rateWindowPct)
	}
	if d.weeklyPct == nil || *d.weeklyPct != 34 || d.weeklyResetAt != 1791700000 {
		t.Errorf("weekly = %v reset %d, want 34 kept when secondary is null", d.weeklyPct, d.weeklyResetAt)
	}
	want := "compact · exec: go vet ./... (failed) · web: codex rollout · mcp: search_docs"
	if d.activity != want {
		t.Errorf("activity = %q, want %q", d.activity, want)
	}
}

func TestFold_PaginatedTrailIntermediate(t *testing.T) {
	_, steps := foldFixture(t, "rollout-paginated-0.160.jsonl")
	// The oldest item (exec: go test ./...) falls off the 80-char cap.
	want := "web: codex rollout · mcp: search_docs · edit: rollout.go +1"
	if got := steps[12].activity; got != want {
		t.Errorf("activity = %q, want %q", got, want)
	}
}

func itemLine(item string) string {
	return `{"type":"event_msg","payload":{"type":"item_completed","turn_id":"t","item":` + item + `}}`
}

func TestFold_ItemCompletedAnyItemIsRunning(t *testing.T) {
	for _, item := range []string{
		`{"type":"UserMessage","id":"1","content":[]}`,
		`{"type":"Reasoning","id":"1","summary_text":[]}`,
		`{"type":"SomethingNew","id":"1"}`,
	} {
		d := foldAll([]string{itemLine(item)}, true)
		if d.state != "running" {
			t.Errorf("%s → state %q, want running", item, d.state)
		}
	}
}

func TestFold_ItemsAfterTaskCompleteKeepDone(t *testing.T) {
	done := `{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"ok"}}`
	d := foldAll([]string{evStarted, done,
		itemLine(`{"type":"SubAgentActivity","id":"1","kind":"completed","agent_path":"a","agent_thread_id":"t"}`),
		itemLine(`{"type":"CollabAgentToolCall","id":"2","tool":"wait","status":"completed"}`),
	}, true)
	if d.state != "done" {
		t.Errorf("items after task_complete → %q, want done", d.state)
	}
	d = foldAll([]string{evStarted, itemLine(`{"type":"SubAgentActivity","id":"1","kind":"spawned"}`)}, true)
	if d.state != "running" {
		t.Errorf("SubAgentActivity mid-turn → %q, want running", d.state)
	}
	d = foldAll([]string{evStarted, done, evStarted, itemLine(`{"type":"UserMessage","id":"3","content":[]}`)}, true)
	if d.state != "running" {
		t.Errorf("next turn → %q, want running", d.state)
	}
}

func TestFold_ItemCompletedEmptyAgentMessageKeepsMessage(t *testing.T) {
	d := foldAll([]string{
		itemLine(`{"type":"AgentMessage","id":"1","content":[{"type":"Text","text":"first"}]}`),
		itemLine(`{"type":"AgentMessage","id":"2","content":[{"type":"Text","text":"  "}]}`),
	}, true)
	if d.message != "first" {
		t.Errorf("message = %q, want first", d.message)
	}
}

func TestFold_TaskCompleteLastAgentMessage(t *testing.T) {
	d := foldAll([]string{evStarted, `{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"All done"}}`}, true)
	if d.state != "done" || d.message != "All done" {
		t.Errorf("state %q message %q", d.state, d.message)
	}
	d = foldAll([]string{evAgent, `{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":null}}`}, true)
	if d.message != "Doing the thing" {
		t.Errorf("null last_agent_message must keep message, got %q", d.message)
	}
}

func TestLabelForItem(t *testing.T) {
	cases := []struct {
		name, item, want string
		ok               bool
	}{
		{"shell wrapped", `{"type":"CommandExecution","command":["/bin/zsh","-lc","go test ./..."],"status":"completed"}`, "exec: go test ./...", true},
		{"bash -c", `{"type":"CommandExecution","command":["bash","-c","make\nmake test"],"status":"completed"}`, "exec: make", true},
		{"plain argv", `{"type":"CommandExecution","command":["git","status"],"status":"completed"}`, "exec: git status", true},
		{"string command", `{"type":"CommandExecution","command":"ls -la","status":"completed"}`, "exec: ls -la", true},
		{"failed", `{"type":"CommandExecution","command":["/bin/zsh","-lc","false"],"status":"failed"}`, "exec: false (failed)", true},
		{"no command", `{"type":"CommandExecution"}`, "exec", true},
		{"file one", `{"type":"FileChange","changes":{"/r/a/main.go":{"type":"update"}}}`, "edit: main.go", true},
		{"file many", `{"type":"FileChange","changes":{"/r/x.go":{"type":"add"},"/r/y.go":{"type":"delete"}}}`, "edit: x.go +1", true},
		{"file none", `{"type":"FileChange"}`, "edit", true},
		{"mcp", `{"type":"McpToolCall","server":"docs","tool":"search"}`, "mcp: search", true},
		{"mcp no tool", `{"type":"McpToolCall","server":"docs"}`, "mcp", true},
		{"web", `{"type":"WebSearch","query":"hooks"}`, "web: hooks", true},
		{"web no query", `{"type":"WebSearch"}`, "web", true},
		{"compaction", `{"type":"ContextCompaction","id":"1"}`, "compact", true},
		{"extension web search", `{"type":"Extension","kind":"web.search","query":"codex hooks","action":{"type":"search","query":"codex hooks","queries":null}}`, "web: codex hooks", true},
		{"extension web other", `{"type":"Extension","kind":"web.search","query":"","action":{"type":"other"}}`, "web", true},
		{"extension sleep", `{"type":"Extension","kind":"clock.sleep"}`, "sleep", true},
		{"extension unknown", `{"type":"Extension","kind":"x.y"}`, "", false},
		{"collab agent", `{"type":"CollabAgentToolCall","tool":"wait","status":"completed"}`, "agent: wait", true},
		{"agent message", `{"type":"AgentMessage","content":[]}`, "", false},
		{"reasoning", `{"type":"Reasoning"}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var it turnItem
			if err := json.Unmarshal([]byte(tc.item), &it); err != nil {
				t.Fatal(err)
			}
			got, ok := labelForItem(it)
			if ok != tc.ok || got != tc.want {
				t.Errorf("labelForItem = (%q,%v), want (%q,%v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFold_TurnAbortedReasons(t *testing.T) {
	cases := map[string]string{
		"interrupted":    "done",
		"replaced":       "done",
		"review_ended":   "done",
		"budget_limited": "error",
	}
	for reason, want := range cases {
		d := foldAll([]string{evStarted, `{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"t","reason":"` + reason + `"}}`}, true)
		if d.state != want {
			t.Errorf("turn_aborted %s → %q, want %q", reason, d.state, want)
		}
	}
}

func TestFold_StreamErrorIsRetryNotError(t *testing.T) {
	d := foldAll([]string{evStarted, `{"type":"event_msg","payload":{"type":"stream_error","message":"Reconnecting... 1/5"}}`}, true)
	if d.state != "running" {
		t.Errorf("stream_error → %q, want running", d.state)
	}
	d = foldAll([]string{evStarted, `{"type":"event_msg","payload":{"type":"error","message":"boom"}}`}, true)
	if d.state != "error" {
		t.Errorf("error → %q, want error", d.state)
	}
}

func TestFold_RateLimitsOptionalWindowsAndLimitID(t *testing.T) {
	base := `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":10,"resets_at":100},"secondary":{"used_percent":20,"resets_at":200}}}}`
	cases := []struct {
		name               string
		line               string
		rate, weekly       int
		rateReset, wkReset int64
	}{
		{"primary only", `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":11,"resets_at":101},"secondary":null}}}`, 11, 20, 101, 200},
		{"secondary only", `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":null,"secondary":{"used_percent":21,"resets_at":201}}}}`, 10, 21, 100, 201},
		{"both null", `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":null,"secondary":null}}}`, 10, 20, 100, 200},
		{"other limit", `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"premium","primary":{"used_percent":99,"resets_at":999},"secondary":{"used_percent":99,"resets_at":999}}}}`, 10, 20, 100, 200},
		{"no limit id (older codex)", `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":12,"resets_at":102},"secondary":{"used_percent":22,"resets_at":202}}}}`, 12, 22, 102, 202},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := foldAll([]string{base, tc.line}, false)
			if d.rateWindowPct == nil || *d.rateWindowPct != tc.rate || d.rateResetAt != tc.rateReset {
				t.Errorf("rate = %v reset %d, want %d reset %d", d.rateWindowPct, d.rateResetAt, tc.rate, tc.rateReset)
			}
			if d.weeklyPct == nil || *d.weeklyPct != tc.weekly || d.weeklyResetAt != tc.wkReset {
				t.Errorf("weekly = %v reset %d, want %d reset %d", d.weeklyPct, d.weeklyResetAt, tc.weekly, tc.wkReset)
			}
		})
	}
}

func TestExpireWindows(t *testing.T) {
	five, wk := 40, 50
	d := derived{rateWindowPct: &five, rateResetAt: 100, primaryRaw: 40, weeklyPct: &wk, weeklyResetAt: 300, weeklyRaw: 50}
	d.expireWindows(time.Unix(200, 0))
	if d.rateWindowPct != nil || d.rateResetAt != 0 {
		t.Errorf("passed 5h window kept: %v reset %d", d.rateWindowPct, d.rateResetAt)
	}
	if d.weeklyPct == nil || d.weeklyResetAt != 300 {
		t.Errorf("future weekly window dropped")
	}
}

func TestFold_RateAtFromLineTimestamp(t *testing.T) {
	d := foldAll([]string{`{"timestamp":"2026-10-05T08:00:06.100Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":1,"resets_at":5}}}}`}, false)
	if want := time.Date(2026, 10, 5, 8, 0, 6, 100e6, time.UTC); !d.rateAt.Equal(want) {
		t.Errorf("rateAt = %v, want %v", d.rateAt, want)
	}
}

func TestBuildUsageRequest_FiveHourOnly(t *testing.T) {
	req, ok := buildUsageRequest(derived{rateResetAt: 100, primaryRaw: 7})
	if !ok || req.FiveHour == nil || req.SevenDay != nil {
		t.Fatalf("five-hour-only usage = %+v ok=%v", req, ok)
	}
}

func TestParseSessionMeta_Sources(t *testing.T) {
	cases := map[string]string{
		`"cli"`:                 "cli",
		`"vscode"`:              "vscode",
		`"exec"`:                "exec",
		`"mcp"`:                 "mcp",
		`{"subagent":"review"}`: "subagent",
		`{"subagent":{"thread_spawn":{"parent_thread_id":"p","depth":1}}}`: "subagent",
		`{"custom":"x"}`: "custom",
		`null`:           "",
		`7`:              "",
	}
	for raw, want := range cases {
		m, ok := parseSessionMeta([]byte(`{"type":"session_meta","payload":{"id":"u-1","source":` + raw + `}}`))
		if !ok || m.source != want {
			t.Errorf("source %s → %+v ok=%v, want %q", raw, m, ok, want)
		}
	}
	if _, ok := parseSessionMeta([]byte(`{"type":"session_meta","payload":{"id":"u-1"}}`)); !ok {
		t.Error("missing source must still parse")
	}
}

func TestConfigTracks(t *testing.T) {
	m := func(source, originator string) sessionMeta {
		return sessionMeta{id: "u", source: source, originator: originator}
	}
	def := Config{}
	for _, s := range []string{"cli", "vscode"} {
		if !def.tracks(m(s, "codex-tui")) {
			t.Errorf("default should track %q", s)
		}
	}
	for _, s := range []string{"exec", "mcp", "custom", "subagent", "internal", "unknown", ""} {
		if def.tracks(m(s, "codex_exec")) {
			t.Errorf("default should not track %q", s)
		}
	}
	if def.tracks(m("vscode", "Claude Code")) {
		t.Error("Claude Code plugin sessions must be skipped by default")
	}
	opt := Config{Sources: parseSources(" cli, Exec ,mcp"), IncludeClaude: true}
	if !opt.tracks(m("exec", "codex_exec")) || !opt.tracks(m("mcp", "")) || opt.tracks(m("vscode", "")) {
		t.Errorf("EMBER_CODEX_SOURCES not honoured: %v", opt.Sources)
	}
	if !opt.tracks(m("cli", "Claude Code")) {
		t.Error("IncludeClaude should track Claude Code sessions")
	}
	if parseSources(" , ") != nil {
		t.Error("an empty list must fall back to the default")
	}
}
