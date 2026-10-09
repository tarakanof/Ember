package main

import (
	"encoding/json"
	"testing"
)

func TestMoodConsumersReadOneSessionProjection(t *testing.T) {
	f := newViewFixture(t)
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running"})
	f.app.Upsert(StatusRequest{Source: "MINI", Tool: "codex", Session: "s2", State: "waiting", Message: "approve"})
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "codex", Session: "s3", State: "done"})
	f.app.setAppHidden("codex", true)

	snap := f.app.Snapshot()
	body, _, err := f.app.knobView(f.m.ID, f.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	var v knobView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	r := snap.Render
	want := knobMood{Waiting: 1, Running: 1, Done: 1, Source: "MINI", Lead: "", Tool: "codex"}
	if v.Mood != want {
		t.Errorf("knob mood = %+v, want %+v", v.Mood, want)
	}
	if v.Mood.Waiting != r.Waiting || v.Mood.Running != r.Running || v.Mood.Done != r.Done || v.Mood.Errors != r.Errors || v.Mood.Source != r.Source {
		t.Errorf("knob mood %+v disagrees with /state render %+v", v.Mood, r)
	}
	if r.Text != "WAIT approve" || r.Tool != "codex" {
		t.Errorf("render = %+v", r)
	}

	clock := f.app.coord.filteredSnapshot()
	if len(clock.Sessions) != 1 || clock.Sessions[0].Tool != "claude" {
		t.Errorf("clock sessions = %+v, want only the visible claude", clock.Sessions)
	}
	if clock.Render != r {
		t.Errorf("clock render %+v differs from the unfiltered render %+v", clock.Render, r)
	}
	if len(snap.Sessions) != 3 {
		t.Errorf("snapshot sessions = %d, want all 3 (hidden apps are the clock's)", len(snap.Sessions))
	}
}
