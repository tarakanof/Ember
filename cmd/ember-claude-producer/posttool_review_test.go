package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Review follow-ups for #76 (PR #173).

func writeSpikeLogFixture(t *testing.T, home string) string {
	t.Helper()
	p := spikeLogPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"event":"post-tool-use-failure","error":"Exit code 1\nSECRET output"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The spike log holds full failed-command output: configure and deconfigure
// (and so install/uninstall, which call them) delete it.
func TestConfigureAndDeconfigure_DeleteSpikeLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := "/Applications/Ember.app/Contents/MacOS/ember-claude-producer"

	p := writeSpikeLogFixture(t, home)
	if err := configureAt(home, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("configure left the spike log: %v", err)
	}

	p = writeSpikeLogFixture(t, home)
	if err := deconfigureAt(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("deconfigure left the spike log: %v", err)
	}
	// Absent log is fine both ways.
	if err := configureAt(home, bin); err != nil {
		t.Fatal(err)
	}
	if err := deconfigureAt(home); err != nil {
		t.Fatal(err)
	}
}

func setHookNow(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	orig := hookNow
	hookNow = func() time.Time { return cur }
	t.Cleanup(func() { hookNow = orig })
	return &cur
}

const promptBash = `{"session_id":"s1","cwd":"/repo","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`

// The dialog's permission_prompt Notification landing after the approved
// call already ran must not put the session back in waiting.
func TestLatePermissionPromptAfterResumeIsIgnored(t *testing.T) {
	stubOwner(t)
	now := setHookNow(t, time.Unix(1_800_000_000, 0))
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	*now = now.Add(3 * time.Second)
	dispatchHookForTest(t, "notification", []byte(promptBash))
	if m := h.marker(t, "s1"); m.State != "running" {
		t.Errorf("late prompt re-stuck the session: state=%q", m.State)
	}
	if h.posts.Load() != 3 {
		t.Errorf("posts = %d, want 3 (late prompt dropped)", h.posts.Load())
	}
	// The next call's PreToolUse doesn't reopen the window either.
	dispatchHookForTest(t, "pre-tool-use", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Read","tool_input":{"file_path":"/repo/a.go"},"tool_use_id":"toolu_09"}`))
	dispatchHookForTest(t, "notification", []byte(promptBash))
	if m := h.marker(t, "s1"); m.State != "running" {
		t.Errorf("late prompt after next PreToolUse: state=%q", m.State)
	}
}

func TestPermissionPromptAfterGraceStillWaits(t *testing.T) {
	stubOwner(t)
	now := setHookNow(t, time.Unix(1_800_000_000, 0))
	h := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	*now = now.Add(resumeGrace + time.Second)
	dispatchHookForTest(t, "notification", []byte(promptBash))
	if m := h.marker(t, "s1"); m.State != "waiting" {
		t.Errorf("prompt past the grace window ignored: state=%q", m.State)
	}
}

// A prompt for another tool, or a new dialog's prompt (PermissionRequest
// first), is never dropped.
func TestPermissionPromptForOtherDialogStillWaits(t *testing.T) {
	stubOwner(t)
	setHookNow(t, time.Unix(1_800_000_000, 0))
	h := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	dispatchHookForTest(t, "notification", []byte(`{"session_id":"s1","cwd":"/repo","notification_type":"permission_prompt","message":"Claude needs your permission to use WebFetch"}`))
	if m := h.marker(t, "s1"); m.State != "waiting" {
		t.Errorf("prompt for another tool dropped: state=%q", m.State)
	}

	h2 := newHookHarness(t)
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", []byte(fixturePostToolUse))
	dispatchHookForTest(t, "permission-request", []byte(`{"session_id":"s1","cwd":"/repo","tool_name":"Bash","tool_input":{"command":"make"}}`))
	dispatchHookForTest(t, "notification", []byte(promptBash))
	if m := h2.marker(t, "s1"); m.State != "waiting" || m.Message != "Claude needs your permission to use Bash" {
		t.Errorf("new dialog's prompt dropped: state=%q message=%q", m.State, m.Message)
	}
}

// An identical call retried while the first one's (async) outcome is still
// in flight: the first call's late PostToolUse has the same fingerprint but a
// different tool_use_id, and must not end the second call's wait.
func TestIdenticalRetriedCall_ToolUseIDKeepsWait(t *testing.T) {
	stubOwner(t)
	setHookNow(t, time.Unix(1_800_000_000, 0))
	h := newHookHarness(t)
	pre := func(id string) []byte {
		return []byte(strings.Replace(fixturePreToolUse, `"toolu_01"`, `"`+id+`"`, 1))
	}
	post := func(id string) []byte {
		return []byte(strings.Replace(fixturePostToolUse, `"toolu_01"`, `"`+id+`"`, 1))
	}
	dispatchHookForTest(t, "pre-tool-use", pre("toolu_A"))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	dispatchHookForTest(t, "post-tool-use", post("toolu_A"))
	dispatchHookForTest(t, "pre-tool-use", pre("toolu_B"))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	if m := h.marker(t, "s1"); m.PendingToolUseID != "toolu_B" {
		t.Fatalf("pending tool_use_id = %q, want toolu_B", m.PendingToolUseID)
	}
	dispatchHookForTest(t, "post-tool-use", post("toolu_A")) // late duplicate of A's outcome
	if m := h.marker(t, "s1"); m.State != "waiting" {
		t.Fatalf("A's outcome ended B's wait: state=%q", m.State)
	}
	dispatchHookForTest(t, "post-tool-use", post("toolu_B"))
	if m := h.marker(t, "s1"); m.State != "running" {
		t.Errorf("B's own outcome didn't end its wait: state=%q", m.State)
	}
}

// PostToolUse's tool_response can be megabytes and precedes tool_use_id: the
// hook must still decode everything it needs, and never keep the response.
func TestDecodeHookInput_LargeToolResponse(t *testing.T) {
	big := strings.Repeat("x", 5<<20)
	payload := `{"session_id":"s1","cwd":"/repo","hook_event_name":"PostToolUse","tool_name":"Read",` +
		`"tool_input":{"file_path":"/repo/big.txt"},` +
		`"tool_response":{"file":{"content":"` + big + `","lines":[1,2,{"n":[3]}]},"type":"text"},` +
		`"tool_use_id":"toolu_Z","duration_ms":7}`
	in, err := decodeHookInput(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if in.ToolUseID != "toolu_Z" || in.ToolName != "Read" || in.SessionID != "s1" ||
		activityString(in.ToolName, in.ToolInput) != "Read: big.txt" {
		t.Errorf("decoded wrong: session=%q tool=%q id=%q", in.SessionID, in.ToolName, in.ToolUseID)
	}
	if bytes.Contains(in.ToolInput, []byte("xxxx")) {
		t.Error("tool_response bled into tool_input")
	}
	for _, bad := range []string{``, `[]`, `{"session_id":`, `{"tool_response":[1,2}`} {
		if _, err := decodeHookInput(strings.NewReader(bad)); err == nil {
			t.Errorf("decodeHookInput(%q) = nil error", bad)
		}
	}
}

// End to end through the runHook reader path: a >1 MiB PostToolUse still
// ends the wait (the old 1 MiB cap cut it off).
func TestLargePostToolUseStillEndsWait(t *testing.T) {
	stubOwner(t)
	setHookNow(t, time.Unix(1_800_000_000, 0))
	h := newHookHarness(t)
	dispatchHookForTest(t, "pre-tool-use", []byte(fixturePreToolUse))
	dispatchHookForTest(t, "permission-request", []byte(fixturePermissionRequest))
	big := strings.Replace(fixturePostToolUse, `"SECRET_STDOUT sk-ant-xyz"`, `"`+strings.Repeat("y", 3<<20)+`"`, 1)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dispatchHookFrom(context.Background(), "post-tool-use", strings.NewReader(big), cfg)
	if m := h.marker(t, "s1"); m.State != "running" {
		t.Errorf("large PostToolUse ignored: state=%q", m.State)
	}
}
