package main

import "testing"

const (
	metaCLI   = `{"type":"session_meta","payload":{"id":"u-123","source":"cli","originator":"codex-tui"}}`
	metaExec  = `{"type":"session_meta","payload":{"id":"u-9","source":"exec","originator":"codex_exec"}}`
	evStarted = `{"type":"event_msg","payload":{"type":"task_started","model_context_window":258400}}`
	evAgent   = `{"type":"event_msg","payload":{"type":"agent_message","message":"Doing the thing","phase":"commentary"}}`
	evToken   = `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":129200},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":42.0,"window_minutes":300}}}}`
	evDone    = `{"type":"event_msg","payload":{"type":"task_complete"}}`
	evExecEnd = `{"type":"event_msg","payload":{"type":"exec_command_end"}}`
)

func TestParseSessionMeta(t *testing.T) {
	m, ok := parseSessionMeta([]byte(metaCLI))
	if !ok || m.id != "u-123" || m.source != "cli" {
		t.Fatalf("cli meta = %+v ok=%v", m, ok)
	}
	m2, ok := parseSessionMeta([]byte(metaExec))
	if !ok || m2.source != "exec" {
		t.Fatalf("exec meta = %+v ok=%v", m2, ok)
	}
	if _, ok := parseSessionMeta([]byte(evStarted)); ok {
		t.Error("event_msg line should not parse as session_meta")
	}
	if _, ok := parseSessionMeta([]byte("not json")); ok {
		t.Error("garbage should not parse")
	}
}

func foldAll(lines []string, ctxEnabled bool) derived {
	var d derived
	for _, l := range lines {
		d.foldEvent([]byte(l), ctxEnabled, true)
	}
	return d
}

func TestFold_RunningThenDone(t *testing.T) {
	d := foldAll([]string{evStarted, evAgent}, true)
	if d.state != "running" {
		t.Errorf("state = %q, want running", d.state)
	}
	if d.message != "Doing the thing" {
		t.Errorf("message = %q", d.message)
	}
	d = foldAll([]string{evStarted, evAgent, evDone}, true)
	if d.state != "done" {
		t.Errorf("state = %q, want done", d.state)
	}
}

func TestFold_TokenCountIsDataOnly(t *testing.T) {
	d := foldAll([]string{evStarted, evToken}, true)
	if d.state != "running" {
		t.Errorf("token_count must not change state; got %q", d.state)
	}
	if d.contextPct == nil || *d.contextPct != 50 { // 129200/258400 = 0.5
		t.Errorf("contextPct = %v, want 50", d.contextPct)
	}
	if d.rateWindowPct == nil || *d.rateWindowPct != 42 {
		t.Errorf("rateWindowPct = %v, want 42", d.rateWindowPct)
	}
}

// TestFold_ContextGatedOff_RateStillCaptured proves the two metric gates are
// independent: with context capture disabled (and rate enabled via foldAll),
// contextPct is dropped while rateWindowPct is still recorded.
func TestFold_ContextGatedOff_RateStillCaptured(t *testing.T) {
	d := foldAll([]string{evStarted, evToken}, false)
	if d.contextPct != nil {
		t.Errorf("contextPct should be nil when disabled, got %v", *d.contextPct)
	}
	if d.rateWindowPct == nil || *d.rateWindowPct != 42 {
		t.Errorf("rateWindowPct should still be set, got %v", d.rateWindowPct)
	}
}

func TestFold_NoTokenCountLeavesMetricsNil(t *testing.T) {
	d := foldAll([]string{evStarted, evExecEnd}, true)
	if d.state != "running" || d.contextPct != nil || d.rateWindowPct != nil {
		t.Errorf("got state=%q ctx=%v rate=%v", d.state, d.contextPct, d.rateWindowPct)
	}
}

func TestFold_ApprovalAndError(t *testing.T) {
	d := foldAll([]string{`{"type":"event_msg","payload":{"type":"exec_approval_request"}}`}, true)
	if d.state != "waiting" {
		t.Errorf("approval → state %q, want waiting", d.state)
	}
	d = foldAll([]string{`{"type":"event_msg","payload":{"type":"stream_error"}}`}, true)
	if d.state != "error" {
		t.Errorf("stream_error → state %q, want error", d.state)
	}
}

func TestFold_IgnoresNonEventLines(t *testing.T) {
	d := foldAll([]string{metaCLI, `{"type":"response_item","payload":{}}`, `{"type":"turn_context","payload":{}}`}, true)
	if d.state != "" {
		t.Errorf("non-event lines must not set state, got %q", d.state)
	}
}

func TestFoldEvent_RateToggle(t *testing.T) {
	line := []byte(`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":62.4}}}}`)

	var on derived
	on.foldEvent(line, true, true)
	if on.rateWindowPct == nil || *on.rateWindowPct != 62 {
		t.Errorf("ratePctEnabled=true: rateWindowPct = %v, want 62", on.rateWindowPct)
	}

	var off derived
	off.foldEvent(line, true, false)
	if off.rateWindowPct != nil {
		t.Errorf("ratePctEnabled=false: rateWindowPct = %v, want nil", off.rateWindowPct)
	}
}
