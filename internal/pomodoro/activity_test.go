package pomodoro

import (
	"testing"
	"time"
)

// beats builds heartbeats for one session every step from start through end
// (inclusive), all in the given state.
func beats(source, tool, session, state string, start, end time.Time, step time.Duration) []ActivityRecord {
	var out []ActivityRecord
	for t := start; !t.After(end); t = t.Add(step) {
		out = append(out, ActivityRecord{At: t, Source: source, Tool: tool, SessionKey: session, State: state})
	}
	return out
}

func byTool(a ActivityRecord) string { return a.Tool }

func TestSummarizeActivityActiveTimeIsWallClockUnionPerGroup(t *testing.T) {
	// Two claude sessions overlap 10:00-10:20 and 10:10-10:30: the tool was
	// active 30 wall-clock minutes, not 40. codex is a separate 10-min span.
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
	// A 30-min silence inside one session is idle time, not activity.
	var acts []ActivityRecord
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 9, 0), utc(2026, 6, 10, 9, 10), 2*time.Minute)...)
	acts = append(acts, beats("m4", "claude", "s1", "running", utc(2026, 6, 10, 9, 40), utc(2026, 6, 10, 9, 50), 2*time.Minute)...)

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)
	if c := got["claude"]; c.ActiveSec != 20*60 || c.Sessions != 1 {
		t.Errorf("claude = %+v, want 1200s over 1 session", c)
	}
}

// Producers keep re-posting a waiting marker for hours while a permission
// prompt sits unanswered; that is the agent idling, not working.
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
	// running → waiting → waiting → running → waiting: two separate episodes
	// where the agent asked for attention. Consecutive waiting rows are one.
	states := []string{"running", "waiting", "waiting", "running", "waiting"}
	var acts []ActivityRecord
	for i, st := range states {
		acts = append(acts, ActivityRecord{
			At: utc(2026, 6, 10, 9, 2*i), Source: "m4", Tool: "claude", SessionKey: "s1", State: st,
		})
	}
	// A waiting row from a different session is its own episode.
	acts = append(acts, ActivityRecord{At: utc(2026, 6, 10, 9, 3), Source: "m5", Tool: "claude", SessionKey: "s2", State: "waiting"})

	got := SummarizeActivity(acts, byTool, 5*time.Minute, 0, time.UTC)
	if a := got["claude"].Attention; a != 3 {
		t.Errorf("attention = %d, want 3", a)
	}
}

func TestDailyActiveSecSplitsByLogicalDay(t *testing.T) {
	// With a 04:00 day start, 02:00 activity belongs to the previous day.
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
