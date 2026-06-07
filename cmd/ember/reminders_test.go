package main

import (
	"context"
	"testing"
	"time"
)

func TestParseHHMM(t *testing.T) {
	cases := map[string]struct {
		h, m int
		ok   bool
	}{
		"09:30": {9, 30, true},
		"00:00": {0, 0, true},
		"23:59": {23, 59, true},
		"24:00": {0, 0, false},
		"9:5":   {9, 5, true},
		"bad":   {0, 0, false},
		"12:60": {0, 0, false},
	}
	for in, want := range cases {
		h, m, ok := parseHHMM(in)
		if ok != want.ok || (ok && (h != want.h || m != want.m)) {
			t.Errorf("parseHHMM(%q) = (%d,%d,%v), want (%d,%d,%v)", in, h, m, ok, want.h, want.m, want.ok)
		}
	}
}

func TestValidateReminders(t *testing.T) {
	good := RemindersConfig{
		Enabled: true, Timezone: "Europe/Amsterdam", PopupDurationSeconds: 8,
		Items: []Reminder{{ID: "a", Time: "09:00", Text: "Standup", Days: []int{1, 2, 3, 4, 5}, Enabled: true}},
	}
	if err := validateReminders(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	dupe := good
	dupe.Items = []Reminder{{ID: "x", Time: "09:00", Text: "a"}, {ID: "x", Time: "10:00", Text: "b"}}
	if err := validateReminders(dupe); err == nil {
		t.Error("duplicate ids should be rejected")
	}
	badTime := RemindersConfig{PopupDurationSeconds: 8, Items: []Reminder{{ID: "a", Time: "9pm", Text: "x"}}}
	if err := validateReminders(badTime); err == nil {
		t.Error("bad time should be rejected")
	}
	badTZ := RemindersConfig{PopupDurationSeconds: 8, Timezone: "Mars/Phobos"}
	if err := validateReminders(badTZ); err == nil {
		t.Error("bad timezone should be rejected")
	}
}

func TestReminderMatchesDay(t *testing.T) {
	everyDay := Reminder{Days: nil}
	if !reminderMatchesDay(everyDay, 3) {
		t.Error("empty days = every day")
	}
	weekdays := Reminder{Days: []int{1, 2, 3, 4, 5}}
	if reminderMatchesDay(weekdays, 0) {
		t.Error("Sunday should not match weekday set")
	}
	if !reminderMatchesDay(weekdays, 5) {
		t.Error("Friday should match")
	}
}

func TestReminderStoreOncePerDay(t *testing.T) {
	s := newReminderStore()
	if !s.shouldFire("a", "2026-06-07") {
		t.Error("first fire should be allowed")
	}
	if s.shouldFire("a", "2026-06-07") {
		t.Error("second fire same day should be suppressed")
	}
	if !s.shouldFire("a", "2026-06-08") {
		t.Error("next day should fire again")
	}
}

func TestEvalRemindersFiresAndDedupes(t *testing.T) {
	pub := &recordingPublisher{}
	cfg := defaultConfig()
	cfg.Reminders = RemindersConfig{
		Enabled: true, Timezone: "UTC", PopupDurationSeconds: 8,
		Items: []Reminder{
			{ID: "standup", Time: "09:00", Text: "Standup", Enabled: true, Sound: true},
			{ID: "off", Time: "09:00", Text: "Disabled", Enabled: false},
			{ID: "weekend", Time: "09:00", Text: "Weekend", Days: []int{0, 6}, Enabled: true},
		},
	}
	app := NewApp(cfg, pub, testLogger())

	// 2026-06-07 is a Sunday (weekday 0) at 09:00 UTC: standup + weekend fire,
	// the disabled one does not.
	sun := time.Date(2026, 6, 7, 9, 0, 30, 0, time.UTC)
	app.evalReminders(context.Background(), sun)
	pub.mu.Lock()
	n1 := len(pub.notify)
	hasSound := false
	for _, p := range pub.notify {
		if p["sound"] != nil {
			hasSound = true
		}
	}
	pub.mu.Unlock()
	if n1 != 2 {
		t.Fatalf("expected 2 popups (standup+weekend), got %d", n1)
	}
	if !hasSound {
		t.Error("standup opted into sound; none seen")
	}

	// Second tick same minute: dedupe → no new popups.
	app.evalReminders(context.Background(), sun.Add(30*time.Second))
	pub.mu.Lock()
	n2 := len(pub.notify)
	pub.mu.Unlock()
	if n2 != n1 {
		t.Errorf("same-day re-eval fired again: %d → %d", n1, n2)
	}

	// A weekday at the same time: only standup (weekend excluded).
	mon := time.Date(2026, 6, 8, 9, 0, 5, 0, time.UTC)
	app.evalReminders(context.Background(), mon)
	pub.mu.Lock()
	n3 := len(pub.notify)
	pub.mu.Unlock()
	if n3 != n2+1 {
		t.Errorf("monday should fire standup only: %d → %d", n2, n3)
	}
}

// TestEvalRemindersGraceWindow guards the review fix: a reminder still fires when
// the only tick in its minute was missed/coalesced (lands a bit late), but a
// stale alarm (well past the grace window) is not fired.
func TestEvalRemindersGraceWindow(t *testing.T) {
	mk := func() *App {
		cfg := defaultConfig()
		cfg.Reminders = RemindersConfig{
			Enabled: true, Timezone: "UTC", PopupDurationSeconds: 8,
			Items: []Reminder{{ID: "a", Time: "09:00", Text: "x", Enabled: true}},
		}
		return NewApp(cfg, &recordingPublisher{}, testLogger())
	}
	// A tick 60s after the target (the on-the-minute tick was coalesced away):
	// still within grace → fires.
	app := mk()
	app.evalReminders(context.Background(), time.Date(2026, 6, 8, 9, 1, 0, 0, time.UTC))
	pub := app.publisher.(*recordingPublisher)
	pub.mu.Lock()
	late := len(pub.notify)
	pub.mu.Unlock()
	if late != 1 {
		t.Errorf("a 60s-late tick should still fire (grace window): got %d, want 1", late)
	}

	// A tick 2 minutes past the target is stale → does not fire.
	app2 := mk()
	app2.evalReminders(context.Background(), time.Date(2026, 6, 8, 9, 2, 0, 0, time.UTC))
	pub2 := app2.publisher.(*recordingPublisher)
	pub2.mu.Lock()
	stale := len(pub2.notify)
	pub2.mu.Unlock()
	if stale != 0 {
		t.Errorf("a 2min-stale tick should not fire: got %d, want 0", stale)
	}
}

func TestReminderStorePruneTo(t *testing.T) {
	s := newReminderStore()
	s.shouldFire("keep", "2026-06-07")
	s.shouldFire("drop", "2026-06-07")
	s.pruneTo(map[string]bool{"keep": true})
	if _, ok := s.lastFired["drop"]; ok {
		t.Error("pruneTo should drop ids not in the keep set")
	}
	if _, ok := s.lastFired["keep"]; !ok {
		t.Error("pruneTo dropped a kept id")
	}
}
