package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Fixtures shaped like the hooks reference's examples
// (https://code.claude.com/docs/en/hooks#posttooluse-input and siblings),
// extended with the fields we must ignore (tool_response, permission_mode,
// tool_use_id, duration_ms, reason). The secrets are there to prove they never
// reach a POST body or the marker.
const (
	fixturePreToolUse = `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","permission_mode":"default",
		"hook_event_name":"PreToolUse","tool_name":"Bash",
		"tool_input":{"command":"npm test","description":"Run test suite"},"tool_use_id":"toolu_01"}`
	fixturePermissionRequest = `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","permission_mode":"default",
		"hook_event_name":"PermissionRequest","tool_name":"Bash",
		"tool_input":{"description":"Run test suite","command":"npm test"},
		"permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test"}],"behavior":"allow","destination":"localSettings"}]}`
	fixturePostToolUse = `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","permission_mode":"default",
		"hook_event_name":"PostToolUse","tool_name":"Bash",
		"tool_input":{"command":"npm test","description":"Run test suite"},
		"tool_response":{"stdout":"SECRET_STDOUT sk-ant-xyz","stderr":"","interrupted":false},
		"tool_use_id":"toolu_01","duration_ms":12}`
	fixturePostToolUseFailure = `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","permission_mode":"default",
		"hook_event_name":"PostToolUseFailure","tool_name":"Bash",
		"tool_input":{"command":"npm test","description":"Run test suite"},"tool_use_id":"toolu_01",
		"error":"Exit code 1\nError: Cannot find module 'express' SECRET_STDERR sk-ant-xyz",
		"is_interrupt":false,"duration_ms":4187}`
	fixturePermissionDenied = `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","permission_mode":"auto",
		"hook_event_name":"PermissionDenied","tool_name":"Bash",
		"tool_input":{"command":"rm -rf /tmp/build","description":"Clean build directory"},
		"tool_use_id":"toolu_02","reason":"[Irreversible Local Destruction]"}`
	fixturePreToolUseRm = `{"session_id":"s1","cwd":"/repo","permission_mode":"auto","hook_event_name":"PreToolUse",
		"tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/build","description":"Clean build directory"},"tool_use_id":"toolu_02"}`
)

func stubOwner(t *testing.T) {
	t.Helper()
	orig := detectOwner
	detectOwner = func() (int, string) { return 0, "" }
	t.Cleanup(func() { detectOwner = orig })
}

func (h *hookHarness) marker(t *testing.T, session string) marker {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.sessionsDir(), session+".json"))
	if err != nil {
		t.Fatalf("marker %s: %v", session, err)
	}
	var m marker
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (h *hookHarness) setEnv(t *testing.T, extra string) {
	t.Helper()
	env := "EMBER_SOURCE=test-mbp\nEMBER_SERVER_URL=" + h.srv.URL + "\nEMBER_TOKEN=tok\n" + extra
	if err := os.WriteFile(filepath.Join(h.home, ".config", "ember", "producer.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFixtures_DecodeDocumentedPayloads(t *testing.T) {
	var in hookInput
	if err := json.Unmarshal([]byte(fixturePostToolUseFailure), &in); err != nil {
		t.Fatal(err)
	}
	if in.HookEventName != "PostToolUseFailure" || in.ToolName != "Bash" || in.IsInterrupt ||
		!strings.HasPrefix(in.Error, "Exit code 1\n") || activityString(in.ToolName, in.ToolInput) != "Bash: npm test" {
		t.Errorf("PostToolUseFailure decoded wrong: %+v", in)
	}
	in = hookInput{}
	if err := json.Unmarshal([]byte(`{"session_id":"s","tool_name":"Bash","error":"x","is_interrupt":true}`), &in); err != nil || !in.IsInterrupt {
		t.Errorf("is_interrupt not decoded: %+v %v", in, err)
	}
	in = hookInput{}
	if err := json.Unmarshal([]byte(fixturePermissionDenied), &in); err != nil {
		t.Fatal(err)
	}
	if in.ToolName != "Bash" || activityString(in.ToolName, in.ToolInput) != "Bash: rm -rf /tmp/build" {
		t.Errorf("PermissionDenied decoded wrong: %+v", in)
	}
}

func TestFailureOutcome(t *testing.T) {
	cases := []struct {
		in   hookInput
		want string
	}{
		{hookInput{Error: "Exit code 1\nError: Cannot find module"}, "exit 1"},
		{hookInput{Error: "Exit code 127"}, "exit 127"},
		{hookInput{Error: "Exit code 1\r\nwindows"}, "exit 1"},
		{hookInput{Error: "Exit code 1 and more"}, "failed"},
		{hookInput{Error: "This agent is isolated in the worktree /x, refusing"}, "failed"},
		{hookInput{Error: "MCP error -32602: Input validation"}, "failed"},
		{hookInput{Error: ""}, "failed"},
		{hookInput{Error: "Exit code 1\nboom", IsInterrupt: true}, "aborted"},
	}
	for _, c := range cases {
		if got := failureOutcome(c.in); got != c.want {
			t.Errorf("failureOutcome(%q, interrupt=%v) = %q, want %q", c.in.Error, c.in.IsInterrupt, got, c.want)
		}
	}
}

func TestAnnotatedActivity_RuneSafeAndCapped(t *testing.T) {
	act := activityString("Bash", json.RawMessage(`{"command":"`+strings.Repeat("я", 120)+`"}`))
	got := annotatedActivity(act, "exit 1")
	if n := utf8.RuneCountInString(got); n > 80 {
		t.Errorf("annotated = %d runes, want <= 80", n)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, " (exit 1)") {
		t.Errorf("annotated activity malformed: %q", got)
	}
}

func TestPermissionFingerprint_KeyOrderInsensitive(t *testing.T) {
	a := permissionFingerprint("Bash", json.RawMessage(`{"command":"npm test","description":"d"}`))
	b := permissionFingerprint("Bash", json.RawMessage(`{ "description":"d", "command":"npm test" }`))
	if a != b {
		t.Errorf("same input, different key order: %s vs %s", a, b)
	}
	if a == permissionFingerprint("Bash", json.RawMessage(`{"command":"npm test2","description":"d"}`)) {
		t.Error("different input, same fingerprint")
	}
	if a == permissionFingerprint("Edit", json.RawMessage(`{"command":"npm test","description":"d"}`)) {
		t.Error("different tool, same fingerprint")
	}
	if strings.Contains(a, "npm") {
		t.Error("fingerprint leaks the input")
	}
}

// A successful call on a running session changes nothing: no POST, no marker
// write. This is the PostToolUse common case (~97% of outcome events).
func TestPostToolUse_RunningIsNoOp(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	mp := filepath.Join(h.sessionsDir(), "s1.json")
	before, _ := os.ReadFile(mp)
	infoBefore, _ := os.Stat(mp)
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	after, _ := os.ReadFile(mp)
	infoAfter, _ := os.Stat(mp)
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1 (the PreToolUse only)", h.posts.Load())
	}
	if string(before) != string(after) || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Errorf("post-tool-use rewrote a running marker:\n%s\n%s", before, after)
	}
}

// Outcome hooks never create a session.
func TestOutcomeHooks_NoMarkerDoesNothing(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	for ev, body := range map[string]string{
		"post-tool-use": fixturePostToolUse, "post-tool-use-failure": fixturePostToolUseFailure,
		"permission-denied": fixturePermissionDenied,
	} {
		dispatchHookForTest(t, ev, []byte(body))
	}
	if h.posts.Load() != 0 || h.deletes.Load() != 0 {
		t.Errorf("posts=%d deletes=%d, want 0", h.posts.Load(), h.deletes.Load())
	}
	if _, err := os.Stat(filepath.Join(h.sessionsDir(), "s1.json")); !os.IsNotExist(err) {
		t.Error("outcome hook created a marker")
	}
}

// The approved call's PostToolUse ends the wait. Before #76 the session kept
// showing "approve Bash" until the next hook.
func TestPermissionApproved_PostToolUseEndsWaiting(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	if m := h.marker(t, "s1"); m.State != "waiting" || m.PendingPermission == "" {
		t.Fatalf("after permission-request: state=%q pending=%q", m.State, m.PendingPermission)
	}
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	if h.posts.Load() != 3 {
		t.Fatalf("posts = %d, want 3", h.posts.Load())
	}
	last := (*h.bodies)[2]
	for _, want := range []string{`"state":"running"`, `"message":"Bash"`, `"activity":"Bash: npm test"`} {
		if !strings.Contains(last, want) {
			t.Errorf("resume POST missing %s: %s", want, last)
		}
	}
	if strings.Contains(last, "pending_permission") || strings.Contains(last, "rate_week") {
		t.Errorf("marker-only fields on the wire: %s", last)
	}
	if m := h.marker(t, "s1"); m.State != "running" || m.PendingPermission != "" {
		t.Errorf("marker after resume: state=%q pending=%q", m.State, m.PendingPermission)
	}
}

// The permission_prompt Notification (~6 s into the dialog) re-upserts
// "waiting" without tool info; the recorded call must survive it.
func TestPermissionApproved_AfterNotificationStillEndsWaiting(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"s1","cwd":"/repo","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(fixturePostToolUseFailure))
	m := h.marker(t, "s1")
	if m.State != "running" {
		t.Errorf("state = %q, want running", m.State)
	}
	if h.posts.Load() != 3 {
		t.Errorf("posts = %d, want 3", h.posts.Load())
	}
}

// A parallel call finishing while another waits for approval must not end
// that wait.
func TestParallelCallDoesNotEndWaiting(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(`{"session_id":"s1","cwd":"/repo","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/repo/a.go"},"tool_response":{}}`))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"ls"},"error":"Exit code 2"}`))
	if m := h.marker(t, "s1"); m.State != "waiting" || m.Message != "approve Bash" {
		t.Errorf("wait ended by another call: state=%q message=%q", m.State, m.Message)
	}
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", h.posts.Load())
	}
}

// A failure marks the call's trail item and rides the next POST instead of
// sending its own. The heartbeat then carries it, with no tool output.
func TestPostToolUseFailure_AnnotatesTrailWithoutPosting(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Read","tool_input":{"file_path":"/repo/a.go"}}`))
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(fixturePostToolUseFailure))
	if h.posts.Load() != 2 {
		t.Fatalf("posts = %d, want 2 (failure must not POST)", h.posts.Load())
	}
	m := h.marker(t, "s1")
	if m.State != "running" {
		t.Errorf("state = %q, want running (a failed tool is not a session error)", m.State)
	}
	if m.Activity != "Bash: npm test (exit 1) · Read: a.go" {
		t.Errorf("activity = %q", m.Activity)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dispatchTick(context.Background(), cfg)
	if h.posts.Load() != 3 {
		t.Fatalf("heartbeat posts = %d, want 3", h.posts.Load())
	}
	if !strings.Contains((*h.bodies)[2], `"activity":"Bash: npm test (exit 1) · Read: a.go"`) {
		t.Errorf("heartbeat body lacks the annotation: %s", (*h.bodies)[2])
	}
}

// Auto mode's PreToolUse → PermissionDenied: the item is marked denied, the
// session stays running, nothing extra is sent and nothing blinks.
func TestPermissionDenied_AutoModeMarksDenied(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUseRm))
	dispatchHookForTest(t, "permission-denied", []byte(fixturePermissionDenied))
	m := h.marker(t, "s1")
	if m.State != "running" || m.Activity != "Bash: rm -rf /tmp/build (denied)" {
		t.Errorf("state=%q activity=%q", m.State, m.Activity)
	}
	if h.posts.Load() != 1 {
		t.Errorf("posts = %d, want 1", h.posts.Load())
	}
	if strings.Contains(string(mustRead(t, filepath.Join(h.sessionsDir(), "s1.json"))), "Irreversible") {
		t.Error("denial reason stored in the marker")
	}
}

// If a PermissionRequest did precede the auto-denial, the denial ends that wait.
func TestPermissionDenied_EndsItsOwnWait(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/build","description":"Clean build directory"}}`))
	dispatchHookForTest(t, "permission-denied", []byte(fixturePermissionDenied))
	if h.posts.Load() != 2 {
		t.Fatalf("posts = %d, want 2", h.posts.Load())
	}
	last := (*h.bodies)[1]
	if !strings.Contains(last, `"state":"running"`) || !strings.Contains(last, `"activity":"Bash: rm -rf /tmp/build (denied)"`) {
		t.Errorf("denial POST = %s", last)
	}
}

func TestPostToolUseFailure_ActivityDetailDisabled(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	h.setEnv(t, "EMBER_ACTIVITY_DETAIL_ENABLED=false\n")
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	mp := filepath.Join(h.sessionsDir(), "s1.json")
	before := mustRead(t, mp)
	dispatchHookForTest(t, "post-tool-use-failure", []byte(fixturePostToolUseFailure))
	if string(before) != string(mustRead(t, mp)) {
		t.Error("marker changed with activity detail off")
	}
}

func TestPostToolUseFailure_TrailDisabled(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	h.setEnv(t, "EMBER_ACTIVITY_TRAIL_ENABLED=false\n")
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(fixturePostToolUseFailure))
	if a := h.marker(t, "s1").Activity; a != "Bash: npm test (exit 1)" {
		t.Errorf("single-item activity = %q", a)
	}
	// A late outcome for an older call must not overwrite the newer one.
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Read","tool_input":{"file_path":"/repo/a.go"}}`))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"old"},"error":"Exit code 3"}`))
	if a := h.marker(t, "s1").Activity; a != "Read: a.go" {
		t.Errorf("activity = %q, want the newer call untouched", a)
	}
}

// Privacy: tool_response and the failure's error text (the tool's output)
// never reach a POST body or the marker, across hooks and the heartbeat.
func TestOutcomeHooks_NoToolOutputLeaks(t *testing.T) {
	stubOwner(t)
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "post-tool-use-failure", []byte(fixturePostToolUseFailure))
	cfg, _ := loadConfig()
	dispatchTick(context.Background(), cfg)
	all := strings.Join(*h.bodies, "\n") + string(mustRead(t, filepath.Join(h.sessionsDir(), "s1.json")))
	for _, secret := range []string{"SECRET", "sk-ant", "Cannot find module", "express", "stdout", "Run test suite"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q leaked into a POST body or the marker:\n%s", secret, all)
		}
	}
}

// Server load: outcome hooks add no requests to a turn's tool calls. N tool
// calls POST N times with PostToolUse/Failure/Denied as they did without.
func TestOutcomeHooks_RequestsPerToolCallUnchanged(t *testing.T) {
	const n = 100
	run := func(withOutcomes bool) int32 {
		stubOwner(t)
		h := newHookHarness(t)
		for i := 0; i < n; i++ {
			pre := `{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"step ` + itoa(i) + `"}}`
			dispatchHookForTest(t, "pre-tool-use", []byte(pre))
			if !withOutcomes {
				continue
			}
			post := strings.Replace(pre, `}}`, `},"tool_response":{"stdout":"ok"}}`, 1)
			switch {
			case i%10 == 3:
				dispatchHookForTest(t, "post-tool-use-failure", []byte(strings.Replace(pre, `}}`, `},"error":"Exit code 1\nboom"}`, 1)))
			case i == 50:
				dispatchHookForTest(t, "permission-denied", []byte(strings.Replace(pre, `}}`, `},"reason":"[x]"}`, 1)))
			default:
				dispatchHookForTest(t, "post-tool-use", []byte(post))
			}
		}
		return h.posts.Load()
	}
	before, after := run(false), run(true)
	t.Logf("POSTs per %d tool calls: before=%d after=%d", n, before, after)
	if before != n || after != before {
		t.Errorf("POSTs per %d tool calls: before=%d after=%d, want equal to %d", n, before, after, n)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// Install: the outcome hooks are registered async, an install from the #76
// spike (same commands, synchronous) is upgraded in place without
// duplicates, a user's own hook on the same event survives, re-running is a
// no-op, and uninstall removes ours.
func TestMergeSettings_OutcomeHooksAsyncAndUpgradeFromSpike(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	bin := "/usr/local/bin/ember-claude-producer"
	dir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	spikeEntry := func(sub string) string {
		return `{"hooks":[{"type":"command","command":"[ -x \"` + bin + `\" ] && \"` + bin + `\" hook ` + sub +
			` >>$HOME/Library/Logs/ember-claude-producer.log 2>&1 || true"}]}`
	}
	spike := `{"hooks":{` +
		`"PostToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"prettier --write"}]},` + spikeEntry("post-tool-use") + `],` +
		`"PostToolUseFailure":[` + spikeEntry("post-tool-use-failure") + `],` +
		`"PermissionDenied":[` + spikeEntry("permission-denied") + `]}}`
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(spike), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeSettingsJSON(tmp, bin); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, settings)
	var root struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(first, &root); err != nil {
		t.Fatal(err)
	}
	for ev, sub := range map[string]string{
		"PostToolUse": "post-tool-use", "PostToolUseFailure": "post-tool-use-failure", "PermissionDenied": "permission-denied",
	} {
		ours := 0
		for _, e := range root.Hooks[ev] {
			for _, hc := range e.Hooks {
				if strings.Contains(hc.Command, "hook "+sub+" ") {
					ours++
					if !hc.Async || e.Matcher != "" {
						t.Errorf("%s: async=%v matcher=%q, want async, match-all", ev, hc.Async, e.Matcher)
					}
				}
			}
		}
		if ours != 1 {
			t.Errorf("%s: %d producer entries, want 1", ev, ours)
		}
	}
	if !strings.Contains(string(first), "prettier --write") {
		t.Error("user's PostToolUse hook dropped")
	}
	if strings.Count(string(first), `"async": true`) != 3 {
		t.Errorf("want exactly the 3 outcome hooks async:\n%s", first)
	}
	if err := mergeSettingsJSON(tmp, bin); err != nil {
		t.Fatal(err)
	}
	if string(first) != string(mustRead(t, settings)) {
		t.Error("re-running configure changed settings.json")
	}
	if err := uninstallSettings(tmp); err != nil {
		t.Fatal(err)
	}
	after := string(mustRead(t, settings))
	if strings.Contains(after, producerName) || !strings.Contains(after, "prettier --write") {
		t.Errorf("uninstall left ours or dropped the user's:\n%s", after)
	}
}
