package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
	"github.com/tarakanof/ember/internal/sessions"
)

func leadView(ss ...render.Session) sessions.View {
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for i := range ss {
		ss[i].UpdatedAt = t0.Add(-time.Duration(i) * time.Second)
	}
	return sessions.View{Now: t0, Sessions: ss}
}

func TestLeadOf(t *testing.T) {
	purple, bad := "#b48cff", "purple"
	cases := []struct {
		name string
		v    sessions.View
		want moodLead
	}{
		{"none", leadView(), moodLead{}},
		{"idle only", leadView(render.Session{Source: "M4", State: "idle"}), moodLead{}},
		{"one host", leadView(render.Session{Source: "M4", Tool: "claude", State: "running", SourceColor: &purple}),
			moodLead{Lead: "M4", Hosts: 1, Color: "#B48CFF", Tool: "claude"}},
		{"most sessions wins over newest", leadView(
			render.Session{Source: "MINI", Tool: "codex", Session: "a", State: "running"},
			render.Session{Source: "M4", Tool: "claude", Session: "b", State: "running"},
			render.Session{Source: "M4", Tool: "claude", Session: "c", State: "running"}),
			moodLead{Lead: "M4", Hosts: 2, Tool: "claude"}},
		{"tie goes to the smaller name", leadView(
			render.Session{Source: "MINI", Tool: "codex", State: "running"},
			render.Session{Source: "M4", Tool: "claude", State: "running"}),
			moodLead{Lead: "M4", Hosts: 2, Tool: "claude"}},
		{"only the winning state counts", leadView(
			render.Session{Source: "MINI", State: "running"},
			render.Session{Source: "MINI", State: "running"},
			render.Session{Source: "M4", Tool: "codex", State: "waiting"}),
			moodLead{Lead: "M4", Hosts: 1, Tool: "codex"}},
		{"tools disagree", leadView(
			render.Session{Source: "M4", Tool: "claude", Session: "a", State: "running"},
			render.Session{Source: "M4", Tool: "codex", Session: "b", State: "running"}),
			moodLead{Lead: "M4", Hosts: 1}},
		{"invalid colour skipped", leadView(
			render.Session{Source: "M4", Session: "a", State: "error", SourceColor: &bad},
			render.Session{Source: "M4", Session: "b", State: "error", SourceColor: &purple}),
			moodLead{Lead: "M4", Hosts: 1, Color: "#B48CFF"}},
		{"no source", leadView(render.Session{Tool: "claude", State: "running"}), moodLead{}},
		{"case-insensitive hosts", leadView(
			render.Session{Source: "m4", Tool: "claude", Session: "a", State: "running"},
			render.Session{Source: "M4", Tool: "claude", Session: "b", State: "running"}),
			moodLead{Lead: "M4", Hosts: 1, Tool: "claude"}},
	}
	for _, c := range cases {
		if got := leadOf(c.v); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestKnobViewMoodNamesTheLeadOfSeveralHosts(t *testing.T) {
	f := newViewFixture(t)
	purple := "#B48CFF"
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running", SourceColor: &purple})
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s2", State: "running"})
	f.app.Upsert(StatusRequest{Source: "MINI", Tool: "codex", Session: "s3", State: "running"})
	resp, body := f.get(t, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var v struct {
		Mood json.RawMessage `json:"mood"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	want := `{"waiting":0,"errors":0,"running":3,"done":0,"source":"","lead":"M4","hosts":2,"lead_color":"#B48CFF","tool":"claude"}`
	if string(v.Mood) != want {
		t.Fatalf("mood = %s\nwant   %s", v.Mood, want)
	}
}

func TestUpsertNotifiesWhenOnlyTheLeadMoves(t *testing.T) {
	f := newViewFixture(t)
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running"})
	seq, _ := f.app.changes.subscribe()
	purple := "#B48CFF"
	f.app.Upsert(StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "running", SourceColor: &purple})
	if got := f.app.changes.since(seq); got&topicSessions == 0 {
		t.Fatal("a lead colour change did not notify")
	}
}

func TestUpsertStaysQuietOnAnUnchangedHeartbeat(t *testing.T) {
	f := newViewFixture(t)
	purple := "#B48CFF"
	req := StatusRequest{Source: "M4", Tool: "claude", Session: "s1", State: "waiting", Message: "approve", SourceColor: &purple}
	f.app.Upsert(req)
	seq, _ := f.app.changes.subscribe()
	f.app.Upsert(req)
	if got := f.app.changes.since(seq); got&topicSessions != 0 {
		t.Fatal("an unchanged heartbeat notified")
	}
}

func TestKnobDisplayFastLinkDefaultsOnAndMerges(t *testing.T) {
	var s knobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.fillDefaults()
	if s.Display.FastLink == nil || !*s.Display.FastLink {
		t.Fatal("a stored record without display reads fast_link on")
	}
	cur := defaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"display":{"fast_link":false}}`))
	if err != nil || *off.Display.FastLink || !*cur.Display.FastLink {
		t.Fatalf("merge: %v %+v (original %+v)", err, off.Display, cur.Display)
	}
	if _, err := mergeKnobSettings(cur.clone(), []byte(`{"display":{"turbo":true}}`)); err == nil {
		t.Fatal("unknown display field accepted")
	}
}

func TestKnobBotFlagsDefaultOnAndMerge(t *testing.T) {
	var s knobSettings
	if err := json.Unmarshal([]byte(`{"bot":{"sleepy_after_s":300,"demo_hold_s":20}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.fillDefaults()
	if s.Bot.SourceLabel == nil || !*s.Bot.SourceLabel || s.Bot.WorkingRing == nil || !*s.Bot.WorkingRing {
		t.Fatalf("a stored record without the flags must read as on: %+v", s.Bot)
	}
	cur := defaultKnobSettings()
	off, err := mergeKnobSettings(cur.clone(), []byte(`{"bot":{"working_ring":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if *off.Bot.WorkingRing || !*off.Bot.SourceLabel {
		t.Fatalf("merge: %+v", off.Bot)
	}
	if !*cur.Bot.WorkingRing {
		t.Fatal("merging into a clone wrote through to the original")
	}
	null, err := mergeKnobSettings(off.clone(), []byte(`{"bot":{"working_ring":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if null.Bot.WorkingRing == nil || !*null.Bot.WorkingRing {
		t.Fatalf("null must read as on: %+v", null.Bot)
	}
}

func TestNewKnobMoodLeavesOutALeadThatOnlyDiffersInCase(t *testing.T) {
	m := newKnobMood(Render{Running: 1, Source: "m4"}, moodLead{Lead: "M4", Hosts: 1, Tool: "codex"})
	if m.Lead != "" || m.Hosts != 0 || m.Tool != "codex" {
		t.Fatalf("mood = %+v", m)
	}
}

func TestMoodStateProjectsOneView(t *testing.T) {
	idle := newMoodState(leadView(), "zzz")
	if idle.Render.Text != "zzz" || idle.Render.Color != "#707070" || idle.Lead != (moodLead{}) || len(idle.Sessions) != 0 {
		t.Fatalf("idle = %+v", idle)
	}
	v := leadView(
		render.Session{Source: "M4", Tool: "claude", Session: "a", State: "error", Message: "boom"},
		render.Session{Source: "MINI", Tool: "codex", Session: "b", State: "running"},
	)
	s := newMoodState(v, "zzz")
	if s.Render.Text != "ERR Claude boom" || s.Render.Errors != 1 || s.Render.Running != 1 || s.Render.Source != "M4" {
		t.Errorf("render = %+v", s.Render)
	}
	if s.Lead != (moodLead{Lead: "M4", Hosts: 1, Tool: "claude"}) {
		t.Errorf("lead = %+v", s.Lead)
	}
	snap := s.snapshot()
	if !snap.Now.Equal(v.Now) || len(snap.Sessions) != 2 || snap.Render != s.Render {
		t.Errorf("snapshot = %+v", snap)
	}
}
