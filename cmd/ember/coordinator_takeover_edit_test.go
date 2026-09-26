package main

import (
	"context"
	"sync"
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

// modelClock is a recordingPublisher whose settings behave like the clock's:
// a write merges into the state a later read returns. The tests route the
// menu's writes through it too, so coordinator and menu writes share one
// ordered device state.
type modelClock struct {
	*recordingPublisher
	mu    sync.Mutex
	state map[string]any
}

func (m *modelClock) Settings(ctx context.Context, payload map[string]any) error {
	m.mu.Lock()
	for k, v := range payload {
		m.state[k] = v
	}
	m.mu.Unlock()
	return m.recordingPublisher.Settings(ctx, payload)
}

func (m *modelClock) ReadSettings(context.Context) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]any, len(m.state))
	for k, v := range m.state {
		out[k] = v
	}
	return out, nil
}

func (m *modelClock) get(k string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state[k]
}

func modelFixture(t *testing.T) (*coordinator, *modelClock, *Snapshot, *bool) {
	t.Helper()
	c, pub, snap, pomo := holdFixture(t, "running")
	clk := &modelClock{recordingPublisher: pub, state: map[string]any{"autoTransition": false, "blockNavigation": false}}
	c.publisher = clk
	return c, clk, snap, pomo
}

// Menu edits arrive on HTTP goroutines while the coordinator takes and
// restores the snapshot on its own. Whatever the interleaving, once the
// last block has ended the clock holds the user's last choice and no
// snapshot is left (and -race sees every access).
func TestMenuEditRacesTakeoverEdges(t *testing.T) {
	c, clk, snap, pomo := modelFixture(t)
	write := func(m map[string]any) error { return clk.Settings(context.Background(), m) }
	done := make(chan struct{})
	const edits = 50
	go func() {
		defer close(done)
		for i := range edits {
			if _, err := c.applyMenuSettings(map[string]any{"autoTransition": i%2 == 0, "brightness": 10.0}, write); err != nil {
				t.Error(err)
			}
			c.takeoverPriorView()
		}
	}()
	for i := range 20 {
		*pomo = i%2 == 0
		c.publish(*snap)
	}
	<-done
	*pomo = false
	c.publish(*snap)

	if p, ok := c.takeoverPriorView(); ok {
		t.Fatalf("snapshot %+v left after the last block", p)
	}
	last := (edits-1)%2 == 0
	if got := clk.get("autoTransition"); got != last {
		t.Fatalf("clock autoTransition = %v, want the last edit %v", got, last)
	}
	if got := clk.get("blockNavigation"); got != false {
		t.Fatalf("clock blockNavigation = %v, want the user's false", got)
	}
}

// An edit whose device write is in flight when a Pomodoro starts: the
// snapshot read may miss it, so the edit is folded into the snapshot, the
// takeover's value is put back for the focus block, and the restore applies
// the edit.
func TestMenuEditInFlightWhenTakeoverStartsIsRecorded(t *testing.T) {
	c, clk, snap, pomo := modelFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	first := true
	write := func(m map[string]any) error {
		if first {
			first = false
			close(entered)
			<-release
		}
		return clk.Settings(context.Background(), m)
	}
	type result struct {
		held []string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		held, err := c.applyMenuSettings(map[string]any{"autoTransition": true}, write)
		res <- result{held, err}
	}()
	<-entered
	*pomo = true
	c.publish(*snap) // snapshot reads autoTransition:false; takeover lands
	close(release)
	r := <-res
	if r.err != nil || len(r.held) != 1 || r.held[0] != "autoTransition" {
		t.Fatalf("held=%v err=%v, want [autoTransition]", r.held, r.err)
	}
	if p, ok := c.takeoverPriorView(); !ok || !p.AutoTransition {
		t.Fatalf("snapshot = %+v/%v, want the in-flight edit recorded", p, ok)
	}
	if got := clk.get("autoTransition"); got != false {
		t.Fatalf("clock autoTransition mid-focus = %v, want the takeover's false", got)
	}

	*pomo = false
	c.publish(*snap)
	if got := clk.get("autoTransition"); got != true {
		t.Fatalf("clock autoTransition after the block = %v, want the edit", got)
	}
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
