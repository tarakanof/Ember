package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type v2Fixture struct{ stmts []string }

func sqlText(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (f *v2Fixture) thread(id, payload string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_threads (thread_id, project_id, title, default_provider, runtime_mode, interaction_mode, created_at, updated_at, payload_json, provider_instance_id) VALUES (%s, 'p', %s, 'codex', 'full-access', 'default', '2026-11-01T10:00:00.000Z', '2026-11-01T10:00:00.000Z', %s, 'codex')`, sqlText(id), sqlText(id), sqlText(payload)))
}

func (f *v2Fixture) run(id, thread string, ordinal int, status, completed, payload string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_runs (run_id, thread_id, ordinal, provider, status, requested_at, completed_at, payload_json) VALUES (%s, %s, %d, 'codex', %s, '2026-11-01T10:0%d:00.000Z', %s, %s)`, sqlText(id), sqlText(thread), ordinal, sqlText(status), ordinal, sqlText(completed), sqlText(payload)))
}

func (f *v2Fixture) item(id, thread, run, node, typ, status, updated, payload string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_turn_items (turn_item_id, thread_id, run_id, node_id, ordinal, type, status, updated_at, payload_json) VALUES (%s, %s, %s, %s, 1, %s, %s, %s, %s)`, sqlText(id), sqlText(thread), sqlText(run), sqlText(node), sqlText(typ), sqlText(status), sqlText(updated), sqlText(payload)))
}

func (f *v2Fixture) request(id, thread, kind, created, payload string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_runtime_requests (runtime_request_id, thread_id, node_id, kind, status, created_at, payload_json) VALUES (%s, %s, 'n', %s, 'pending', %s, %s)`, sqlText(id), sqlText(thread), sqlText(kind), sqlText(created), sqlText(payload)))
}

func (f *v2Fixture) message(id, thread, run, role, updated, payload string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_messages (message_id, thread_id, run_id, role, streaming, created_at, updated_at, payload_json) VALUES (%s, %s, %s, %s, 0, %s, %s, %s)`, sqlText(id), sqlText(thread), sqlText(run), sqlText(role), sqlText(updated), sqlText(updated), sqlText(payload)))
}

func (f *v2Fixture) roster(id, thread, tasks string) {
	f.stmts = append(f.stmts, fmt.Sprintf(`INSERT INTO orchestration_v2_projection_provider_threads (provider_thread_id, thread_id, provider, status, updated_at, payload_json) VALUES (%s, %s, 'claudeAgent', 'active', '2026-11-01T10:09:00.000Z', '{"pendingBackgroundTasks":%s}')`, sqlText(id), sqlText(thread), tasks))
}

func (f *v2Fixture) read(t *testing.T, migration int) map[string]thread {
	t.Helper()
	home := t.TempDir()
	stmts := append([]string{fmt.Sprintf(`INSERT INTO effect_sql_migrations (migration_id, name) VALUES (%d, 'x')`, migration)}, f.stmts...)
	makeDB(t, home, "statev2.sqlite", "schema_v2.sql", stmts...)
	snap, err := readSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Migration != migration {
		t.Fatalf("migration = %d, want %d", snap.Migration, migration)
	}
	return byID(snap.Threads)
}

type mapped struct {
	state, message string
	reportable     bool
}

func mappedOf(th thread) mapped {
	s, m, r := mapThread(th)
	return mapped{s, m, r}
}

const done = `2026-11-01T10:05:00.000Z`

func TestQueryV2WaitingCards(t *testing.T) {
	var f v2Fixture
	f.thread("link", `{}`)
	f.run("link-r", "link", 1, "completed", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("link-i", "link", "link-r", "n1", "link_request", "waiting", "2026-11-01T10:04:00.000Z", `{"startedAt":"2026-11-01T10:03:30.000Z"}`)
	f.message("link-m", "link", "link-r", "user", "2026-11-01T10:03:00.000Z", `{"createdBy":"user","text":"link my vps"}`)

	f.thread("link-answered", `{}`)
	f.run("la-r", "link-answered", 1, "completed", done, `{}`)
	f.item("la-i", "link-answered", "la-r", "n1", "link_request", "waiting", "2026-11-01T10:04:00.000Z", `{"startedAt":"2026-11-01T10:03:30.000Z"}`)
	f.message("la-m", "link-answered", "la-r", "user", "2026-11-01T10:06:00.000Z", `{"createdBy":"user","text":"never mind"}`)

	f.thread("link-agent-msg", `{}`)
	f.run("lg-r", "link-agent-msg", 1, "completed", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("lg-i", "link-agent-msg", "lg-r", "n1", "link_request", "waiting", "2026-11-01T10:04:00.000Z", `{}`)
	f.message("lg-m", "link-agent-msg", "lg-r", "user", "2026-11-01T10:06:00.000Z", `{"createdBy":"agent","text":"wake"}`)

	f.thread("link-nonode", `{}`)
	f.run("ln-r", "link-nonode", 1, "completed", done, `{}`)
	f.item("ln-i", "link-nonode", "ln-r", "", "link_request", "waiting", "2026-11-01T10:04:00.000Z", `{}`)

	f.thread("link-over-secret", `{}`)
	f.run("los-r", "link-over-secret", 1, "running", "", `{}`)
	f.item("los-s", "link-over-secret", "los-r", "", "secret_request", "waiting", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("los-l", "link-over-secret", "los-r", "n1", "link_request", "waiting", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("approval-first", `{}`)
	f.run("af-r", "approval-first", 1, "running", "", `{}`)
	f.request("af-q1", "approval-first", "command", "2026-11-01T10:02:00.000Z", `{}`)
	f.request("af-q2", "approval-first", "user_input", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("card-vs-approval", `{}`)
	f.run("cva-r", "card-vs-approval", 1, "running", "", `{}`)
	f.request("cva-q", "card-vs-approval", "command", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("cva-s", "card-vs-approval", "cva-r", "n1", "secret_request", "waiting", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("card-vs-older-input", `{}`)
	f.run("cvi-r", "card-vs-older-input", 1, "running", "", `{}`)
	f.request("cvi-q", "card-vs-older-input", "user_input", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("cvi-s", "card-vs-older-input", "cvi-r", "n1", "secret_request", "waiting", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("card-vs-newer-input", `{}`)
	f.run("cvn-r", "card-vs-newer-input", 1, "running", "", `{}`)
	f.request("cvn-q", "card-vs-newer-input", "user_input", "2026-11-01T10:04:00.000Z", `{}`)
	f.item("cvn-s", "card-vs-newer-input", "cvn-r", "n1", "secret_request", "waiting", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("card-vs-auth", `{}`)
	f.run("cvau-r", "card-vs-auth", 1, "running", "", `{}`)
	f.request("cvau-q", "card-vs-auth", "auth_refresh", "2026-11-01T10:02:00.000Z", `{}`)
	f.item("cvau-s", "card-vs-auth", "cvau-r", "n1", "secret_request", "waiting", "2026-11-01T10:03:00.000Z", `{}`)

	f.thread("soft-input", `{}`)
	f.run("si-r", "soft-input", 1, "running", "", `{}`)
	f.request("si-q", "soft-input", "user_input", "2026-11-01T10:02:00.000Z", `{"responseCapability":{"type":"message"}}`)

	got := f.read(t, 81)
	want := map[string]struct {
		pending, stamp string
		m              mapped
	}{
		"link":                {"user_input", "10:03:30", mapped{"waiting", "needs input", true}},
		"link-answered":       {"", "", mapped{"done", "", true}},
		"link-agent-msg":      {"user_input", "10:04:00", mapped{"waiting", "needs input", true}},
		"link-nonode":         {"", "", mapped{"done", "", true}},
		"link-over-secret":    {"user_input", "10:03:00", mapped{"waiting", "needs input", true}},
		"approval-first":      {"command", "", mapped{"waiting", "approve command", true}},
		"card-vs-approval":    {"command", "", mapped{"waiting", "approve command", true}},
		"card-vs-older-input": {"user_input", "10:03:00", mapped{"waiting", "needs input", true}},
		"card-vs-newer-input": {"user_input", "10:04:00", mapped{"waiting", "needs input", true}},
		"card-vs-auth":        {"", "", mapped{"running", "", true}},
		"soft-input":          {"user_input", "", mapped{"waiting", "needs input", true}},
	}
	for id, w := range want {
		th, ok := got[id]
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if th.PendingKind != w.pending {
			t.Errorf("%s pending = %q, want %q", id, th.PendingKind, w.pending)
		}
		if w.stamp != "" && th.ChangedAt.Format("15:04:05") != w.stamp {
			t.Errorf("%s changed = %s, want %s", id, th.ChangedAt.Format("15:04:05"), w.stamp)
		}
		if m := mappedOf(th); m != w.m {
			t.Errorf("%s mapped = %+v, want %+v", id, m, w.m)
		}
	}
}

func TestQueryV2PresentedRun(t *testing.T) {
	var f v2Fixture
	idle := `{"createdBy":"agent","creationSource":"server","text":" /Compact "}`
	f.thread("silent-done", `{}`)
	f.run("sd-r1", "silent-done", 1, "completed", "2026-11-01T10:02:00.000Z", `{}`)
	f.run("sd-r2", "silent-done", 2, "completed", "2026-11-01T10:08:00.000Z", `{"userMessageId":"sd-m"}`)
	f.message("sd-m", "silent-done", "sd-r2", "user", "2026-11-01T10:07:00.000Z", idle)

	f.thread("silent-running", `{}`)
	f.run("sr-r1", "silent-running", 1, "completed", "2026-11-01T10:02:00.000Z", `{}`)
	f.run("sr-r2", "silent-running", 2, "running", "", `{"userMessageId":"sr-m"}`)
	f.message("sr-m", "silent-running", "sr-r2", "user", "2026-11-01T10:07:00.000Z", idle)

	f.thread("compact-failed", `{}`)
	f.run("cf-r1", "compact-failed", 1, "completed", "2026-11-01T10:02:00.000Z", `{}`)
	f.run("cf-r2", "compact-failed", 2, "running", "", `{"userMessageId":"cf-m"}`)
	f.message("cf-m", "compact-failed", "cf-r2", "user", "2026-11-01T10:07:00.000Z", idle)
	f.item("cf-i", "compact-failed", "cf-r2", "n", "compaction", "failed", "2026-11-01T10:07:30.000Z", `{}`)

	f.thread("user-compact", `{}`)
	f.run("uc-r1", "user-compact", 1, "running", "", `{"userMessageId":"uc-m"}`)
	f.message("uc-m", "user-compact", "uc-r1", "user", "2026-11-01T10:07:00.000Z", `{"createdBy":"user","text":"/compact"}`)

	f.thread("usage-limit", `{}`)
	f.run("ul-r1", "usage-limit", 1, "failed", done, `{"rootNodeId":"root"}`)
	f.run("ul-r2", "usage-limit", 2, "queued", "", `{}`)
	f.item("ul-i", "usage-limit", "ul-r1", "root", "error", "failed", done, `{"failure":{"class":"usage_limit","message":"usage limit reached"}}`)

	f.thread("other-failure", `{}`)
	f.run("of-r1", "other-failure", 1, "failed", done, `{"rootNodeId":"root"}`)
	f.run("of-r2", "other-failure", 2, "queued", "", `{}`)
	f.item("of-i", "other-failure", "of-r1", "root", "error", "failed", done, `{"failure":{"class":"provider_error","message":"boom"}}`)

	f.thread("side", `{"lineage":{"sideChat":true}}`)
	f.run("side-r", "side", 1, "running", "", `{}`)

	got := f.read(t, 81)
	if _, ok := got["side"]; ok {
		t.Error("side chats must not be read")
	}
	want := map[string]mapped{
		"silent-done":    {"done", "", true},
		"silent-running": {"done", "", true},
		"compact-failed": {"running", "", true},
		"user-compact":   {"running", "", true},
		"usage-limit":    {"error", "usage limit reached", true},
		"other-failure":  {"", "", false},
	}
	for id, w := range want {
		if m := mappedOf(got[id]); m != w {
			t.Errorf("%s mapped = %+v, want %+v (%+v)", id, m, w, got[id])
		}
	}
	if c := got["silent-done"].ChangedAt.Format("15:04"); c != "10:02" {
		t.Errorf("silent-done changed = %s, want the real run's 10:02", c)
	}
}

func TestQueryV2CompletionHolds(t *testing.T) {
	var f v2Fixture
	completed := func(id, payload string) {
		f.thread(id, payload)
		f.run(id+"-r", id, 1, "completed", done, `{}`)
	}
	completed("monitor", `{}`)
	f.roster("monitor-pt", "monitor", `[{"taskId":"m","kind":"monitor"}]`)
	completed("awaited-monitor", `{}`)
	f.roster("am-pt", "awaited-monitor", `[{"taskId":"m","kind":"monitor","awaited":true}]`)
	completed("bg-task", `{}`)
	f.roster("bg-pt", "bg-task", `[{"taskId":"b","kind":"background_task"}]`)
	completed("unknown-kind", `{}`)
	f.roster("uk-pt", "unknown-kind", `[{"taskId":"u","kind":"weird"}]`)
	completed("dyn-item", `{}`)
	f.item("dyn-i", "dyn-item", "dyn-item-r", "n", "dynamic_tool", "running", done, `{}`)
	completed("dyn-dup", `{}`)
	f.roster("dd-pt", "dyn-dup", `[{"taskId":"native-1","kind":"monitor"}]`)
	f.item("dd-i", "dyn-dup", "dyn-dup-r", "n", "dynamic_tool", "running", done, `{"nativeItemRef":{"nativeId":"native-1"}}`)
	completed("pr-open", `{"pullRequests":[{"number":1,"source":"manual","watch":{}}]}`)
	completed("pr-merged", `{"pullRequests":[{"number":2,"source":"manual","watch":{},"snapshot":{"state":"merged"}}]}`)

	wake := func(id, payload, kind string) {
		f.thread(id, payload)
		f.run(id+"-r", id, 1, "completed", done, `{"userMessageId":"`+id+`-m"}`)
		f.message(id+"-m", id, "", "user", "2026-11-01T10:04:00.000Z", `{"createdBy":"agent","notification":{"source":{"kind":"`+kind+`"},"outcome":"updated"}}`)
		f.roster(id+"-pt", id, `[{"taskId":"m","kind":"monitor"}]`)
	}
	wake("wake-monitor", `{}`, "monitor")
	wake("wake-settled", `{"settledOverride":"settled"}`, "monitor")
	wake("wake-steered", `{}`, "monitor")
	f.message("ws-steer", "wake-steered", "wake-steered-r", "user", "2026-11-01T10:04:30.000Z", `{"createdBy":"user","text":"also"}`)
	f.thread("wake-cmd-item", `{}`)
	f.run("wci-r", "wake-cmd-item", 1, "completed", done, `{"userMessageId":"wci-m"}`)
	f.message("wci-m", "wake-cmd-item", "", "user", "2026-11-01T10:04:00.000Z", `{"notification":{"source":{"kind":"background_command"},"outcome":"updated"}}`)
	f.item("wci-i", "wake-cmd-item", "wci-r", "n", "command_execution", "running", done, `{"monitor":true}`)

	completed("stopped", `{}`)
	f.item("stop-i", "stopped", "stopped-r", "n", "run_interrupt_request", "completed", done, `{}`)
	completed("stopped-held", `{}`)
	f.item("sh-i", "stopped-held", "stopped-held-r", "n", "run_interrupt_request", "completed", done, `{}`)
	f.roster("sh-pt", "stopped-held", `[{"taskId":"s","kind":"subagent"}]`)
	completed("settled-held", `{"settledOverride":"settled"}`)
	f.roster("seh-pt", "settled-held", `[{"taskId":"s","kind":"subagent"}]`)
	f.thread("settled-failed", `{"settledOverride":"settled"}`)
	f.run("sf-r", "settled-failed", 1, "failed", done, `{}`)
	f.thread("settled-failed-held", `{"settledOverride":"settled"}`)
	f.run("sfh-r", "settled-failed-held", 1, "failed", done, `{}`)
	f.roster("sfh-pt", "settled-failed-held", `[{"taskId":"s","kind":"background_task"}]`)

	got := f.read(t, 81)
	running, finished, hidden := mapped{"running", "", true}, mapped{"done", "", true}, mapped{"", "", false}
	want := map[string]mapped{
		"monitor":             finished,
		"awaited-monitor":     running,
		"bg-task":             running,
		"unknown-kind":        finished,
		"dyn-item":            running,
		"dyn-dup":             finished,
		"pr-open":             running,
		"pr-merged":           finished,
		"wake-monitor":        running,
		"wake-settled":        hidden,
		"wake-steered":        finished,
		"wake-cmd-item":       running,
		"stopped":             hidden,
		"stopped-held":        running,
		"settled-held":        running,
		"settled-failed":      hidden,
		"settled-failed-held": {"error", "failed", true},
	}
	for id, w := range want {
		th, ok := got[id]
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if m := mappedOf(th); m != w {
			t.Errorf("%s mapped = %+v, want %+v (%+v)", id, m, w, th)
		}
	}
}

func TestQueryV2SubagentApprovalReachesTopLevel(t *testing.T) {
	var f v2Fixture
	parent := func(id string) {
		f.thread(id, `{}`)
		f.run(id+"-r", id, 1, "completed", done, `{}`)
	}
	child := func(id, parentID, source, runStatus, kind string, minute int) {
		f.thread(id, `{"lineage":{"relationshipToParent":"subagent","parentThreadId":"`+parentID+`"},"creationSource":"`+source+`"}`)
		f.run(id+"-r", id, 1, runStatus, "", `{}`)
		f.request(id+"-q", id, kind, fmt.Sprintf("2026-11-01T10:%02d:00.000Z", minute), `{}`)
	}
	parent("top")
	child("kid", "top", "mcp", "running", "command", 6)
	child("grandkid", "kid", "mcp", "running", "file-change", 7)
	parent("native-top")
	child("native-kid", "native-top", "provider", "running", "command", 6)
	parent("idle-top")
	child("idle-kid", "idle-top", "mcp", "completed", "command", 6)
	parent("input-top")
	child("input-kid", "input-top", "mcp", "running", "user_input", 6)
	parent("own-input")
	f.request("own-q", "own-input", "user_input", "2026-11-01T10:01:00.000Z", `{}`)
	child("own-kid", "own-input", "mcp", "running", "command", 6)

	got := f.read(t, 81)
	for _, id := range []string{"kid", "grandkid", "native-kid"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s: subagent threads must not be read", id)
		}
	}
	want := map[string]struct {
		pending string
		m       mapped
	}{
		"top":        {"file-change", mapped{"waiting", "approve file-change", true}},
		"native-top": {"", mapped{"done", "", true}},
		"idle-top":   {"", mapped{"done", "", true}},
		"input-top":  {"", mapped{"done", "", true}},
		"own-input":  {"user_input", mapped{"waiting", "needs input", true}},
	}
	for id, w := range want {
		if got[id].PendingKind != w.pending {
			t.Errorf("%s pending = %q, want %q", id, got[id].PendingKind, w.pending)
		}
		if m := mappedOf(got[id]); m != w.m {
			t.Errorf("%s mapped = %+v, want %+v", id, m, w.m)
		}
	}
}

func TestQueryV2FollowsAwarenessMigration(t *testing.T) {
	var f v2Fixture
	f.thread("soft", `{}`)
	f.run("soft-r", "soft", 1, "running", "", `{}`)
	f.request("soft-q", "soft", "user_input", "2026-11-01T10:02:00.000Z", `{"responseCapability":{"type":"message"}}`)
	f.thread("monitor", `{}`)
	f.run("mon-r", "monitor", 1, "completed", done, `{}`)
	f.roster("mon-pt", "monitor", `[{"taskId":"m","kind":"monitor"}]`)
	for mig, want := range map[int][2]string{67: {"running", "running"}, 68: {"waiting", "done"}} {
		got := f.read(t, mig)
		if s := mappedOf(got["soft"]).state; s != want[0] {
			t.Errorf("migration %d soft = %q, want %q", mig, s, want[0])
		}
		if s := mappedOf(got["monitor"]).state; s != want[1] {
			t.Errorf("migration %d monitor = %q, want %q", mig, s, want[1])
		}
	}
}
