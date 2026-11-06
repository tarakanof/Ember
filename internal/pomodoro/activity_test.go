package pomodoro

import (
	"testing"
	"time"
)

func beats(source, tool, session, state string, start, end time.Time, step time.Duration) []ActivityRecord {
	var out []ActivityRecord
	for t := start; !t.After(end); t = t.Add(step) {
		out = append(out, ActivityRecord{At: t, Source: source, Tool: tool, SessionKey: session, State: state})
	}
	return out
}

func byTool(a ActivityRecord) string { return a.Tool }

func TestSummarizeActivityActiveTimeIsWallClockUnionPerGroup(t *testing.T) {
	var acts []ActivityRecord
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 10, 0), utc(2026, 6, 10, 10, 20), 2*time.Minute)...)
	acts = append(acts, beats("m5", "claude", "s2", "running", utc(2026, 6, 10, 10, 10), utc(2026, 6, 10, 10, 30), 2*time.Minute)...)
	acts = append(acts, beats("m4", "codex", "s3", "running", utc(2026, 6, 10, 11, 0), utc(2026, 6, 10, 11, 10), 2*time.Minute)...)

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)

	if c := got["claude"]; c.ActiveSec != 30*60 || c.Sessions != 2 {
		t.Errorf("claude = %+v, want 1800s over 2 sessions", c)
	}
	if c := got["codex"]; c.ActiveSec != 10*60 || c.Sessions != 1 {
		t.Errorf("codex = %+v, want 600s over 1 session", c)
	}
}

func TestSummarizeActivityGapSplitsSpans(t *testing.T) {
	var acts []ActivityRecord
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 9, 0), utc(2026, 6, 10, 9, 10), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 9, 40), utc(2026, 6, 10, 9, 50), 2*time.Minute)...)

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)
	if c := got["claude"]; c.ActiveSec != 20*60 || c.Sessions != 1 {
		t.Errorf("claude = %+v, want 1200s over 1 session", c)
	}
}

func TestSummarizeActivityWaitingIsNotActiveTime(t *testing.T) {
	var acts []ActivityRecord
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 9, 0), utc(2026, 6, 10, 9, 10), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "waiting", utc(2026, 6, 10, 9, 12), utc(2026, 6, 10, 10, 10), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 10, 12), utc(2026, 6, 10, 10, 22), 2*time.Minute)...)

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)
	if c := got["claude"]; c.ActiveSec != 20*60 || c.Sessions != 1 || c.Attention != 1 {
		t.Errorf("claude = %+v, want 1200s active, 1 session, 1 attention", c)
	}
}

func TestSummarizeActivityCountsWaitingEpisodes(t *testing.T) {
	states := []string{"running", "waiting", "waiting", "running", "waiting"}
	var acts []ActivityRecord
	for i, st := range states {
		acts = append(acts, ActivityRecord{
			At: utc(2026, 6, 10, 9, 2*i), Source: "m4", Tool: "claude", SessionKey: "s1", State: st,
		})
	}
	acts = append(acts, ActivityRecord{At: utc(2026, 6, 10, 9, 3), Source: "m5", Tool: "claude", SessionKey: "s2", State: "waiting"})

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)
	if a := got["claude"].Attention; a != 3 {
		t.Errorf("attention = %d, want 3", a)
	}
}

func TestDailyActiveSecSplitsByLogicalDay(t *testing.T) {
	var acts []ActivityRecord
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 22, 0), utc(2026, 6, 10, 22, 10), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 11, 2, 0), utc(2026, 6, 11, 2, 20), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 11, 9, 0), utc(2026, 6, 11, 9, 6), 2*time.Minute)...)

	got := DailyActiveSec(acts, byTool, 5*time.Minute, 4, time.UTC)
	if s := got["2026-06-10"]["claude"]; s != 30*60 {
		t.Errorf("2026-06-10 = %d, want 1800", s)
	}
	if s := got["2026-06-11"]["claude"]; s != 6*60 {
		t.Errorf("2026-06-11 = %d, want 360", s)
	}
}
