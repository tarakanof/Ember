package main

import (
	"testing"
)

// takeoverKeyEdit is the menu edit the #162 tests make mid-focus: the user's
// prior was rotation off with navigation free; they turn rotation on, keep
// navigation free, and change the brightness in the same save.
var takeoverKeyEdit = map[string]any{"autoTransition": true, "blockNavigation": false, "brightness": 40.0}

// recordWrites is a device-write func that records what it was given.
func recordWrites(into *[]map[string]any) func(map[string]any) error {
	return func(m map[string]any) error {
		*into = append(*into, m)
		return nil
	}
}

// A menu edit of a takeover key during a focus block must not reach the
// device (it would resume rotation mid-focus); it becomes the value the
// restore writes. Other keys in the same edit go to the device at once.
func TestMenuEditDuringTakeoverIsAppliedOnRestore(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}

	*pomo = true
	c.publish(*snap)

	var written []map[string]any
	deferred, err := c.applyMenuSettings(takeoverKeyEdit, recordWrites(&written))
	if err != nil {
		t.Fatal(err)
	}
	if len(deferred) != 2 {
		t.Fatalf("deferred = %v, want both takeover keys", deferred)
	}
	if len(written) != 1 || len(written[0]) != 1 || written[0]["brightness"] != 40.0 {
		t.Fatalf("device writes = %v, want only brightness", written)
	}
	if p, ok := c.takeoverPriorView(); !ok || !p.AutoTransition || p.BlockNavigation {
		t.Fatalf("prior view = %+v/%v, want the edit", p, ok)
	}
	if s := pub.SettingsSnapshot(); len(s) != 1 {
		t.Fatalf("settings writes mid-focus = %+v, want only the takeover", s)
	}

	*pomo = false
	c.publish(*snap)
	s := pub.SettingsSnapshot()
	if len(s) != 2 {
		t.Fatalf("settings writes = %+v, want takeover + restore", s)
	}
	wantTakeoverSettings(t, s[1], true, false)
}

// With no takeover in force the edit goes straight to the device, whole.
func TestMenuEditWithoutTakeoverGoesToDevice(t *testing.T) {
	c, _, _, _ := holdFixture(t, "running")
	var written []map[string]any
	deferred, err := c.applyMenuSettings(takeoverKeyEdit, recordWrites(&written))
	if err != nil || len(deferred) != 0 {
		t.Fatalf("deferred=%v err=%v, want none", deferred, err)
	}
	if len(written) != 1 || len(written[0]) != 3 {
		t.Fatalf("device writes = %v, want the whole edit", written)
	}
	if _, ok := c.takeoverPriorView(); ok {
		t.Fatal("prior view reported with no takeover")
	}
}

// An edit made only of takeover keys during a focus block writes nothing.
func TestMenuEditOfOnlyTakeoverKeysSkipsTheDevice(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": true, "blockNavigation": false}
	*pomo = true
	c.publish(*snap)

	var written []map[string]any
	if _, err := c.applyMenuSettings(map[string]any{"autoTransition": false}, recordWrites(&written)); err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("device writes = %v, want none", written)
	}
	if p, _ := c.takeoverPriorView(); p.AutoTransition || p.BlockNavigation {
		t.Fatalf("prior = %+v, want autoTransition:false blockNavigation:false", p)
	}
}

// Menu edits arrive on HTTP goroutines while the coordinator takes and
// restores the snapshot on its own; -race keeps the two honest.
func TestMenuEditRacesTakeoverEdges(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": true, "blockNavigation": false}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			_, _ = c.applyMenuSettings(map[string]any{"autoTransition": i%2 == 0, "brightness": 10.0},
				func(map[string]any) error { return nil })
			c.takeoverPriorView()
		}
	}()
	for i := range 20 {
		*pomo = i%2 == 0
		c.publish(*snap)
	}
	<-done
}

// The edit is persisted with the snapshot, so a server that dies mid-focus
// restores the user's latest choice on its next start.
func TestMenuEditDuringTakeoverSurvivesCrash(t *testing.T) {
	kv := &memKV{}
	c, pub, snap, pomo := holdFixture(t, "running")
	c.setSettingsKV(kv)
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}
	*pomo = true
	c.publish(*snap)
	var written []map[string]any
	if _, err := c.applyMenuSettings(map[string]any{"autoTransition": true}, recordWrites(&written)); err != nil {
		t.Fatal(err)
	}

	c2, pub2, snap2, _ := holdFixture(t, "running")
	c2.setSettingsKV(kv)
	c2.publish(*snap2)
	s := pub2.SettingsSnapshot()
	if len(s) != 1 {
		t.Fatalf("settings on the first publish after restart = %+v, want one restore", s)
	}
	wantTakeoverSettings(t, s[0], true, false)
}
