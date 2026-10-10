package main

import (
	"context"
	"sync"
	"testing"
)

var takeoverKeyEdit = map[string]any{"autoTransition": true, "blockNavigation": false, "brightness": 40.0}

func recordWrites(into *[]map[string]any) func(map[string]any) error {
	return func(m map[string]any) error {
		*into = append(*into, m)
		return nil
	}
}

func TestMenuEditDuringTakeoverIsAppliedOnRestore(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}

	*pomo = true
	c.publish(*snap)

	var written []map[string]any
	deferred, err := c.applyMenuSettings(context.Background(), takeoverKeyEdit, recordWrites(&written))
	if err != nil {
		t.Fatal(err)
	}
	if len(deferred) != 2 {
		t.Fatalf("deferred = %v, want both takeover keys", deferred)
	}
	if len(written) != 1 || len(written[0]) != 1 || written[0]["brightness"] != 40.0 {
		t.Fatalf("device writes = %v, want only brightness", written)
	}
	if p, ok := priorView(c); !ok || !p.AutoTransition || p.BlockNavigation {
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

func TestMenuEditWithoutTakeoverGoesToDevice(t *testing.T) {
	c, _, _, _ := holdFixture(t, "running")
	var written []map[string]any
	deferred, err := c.applyMenuSettings(context.Background(), takeoverKeyEdit, recordWrites(&written))
	if err != nil || len(deferred) != 0 {
		t.Fatalf("deferred=%v err=%v, want none", deferred, err)
	}
	if len(written) != 1 || len(written[0]) != 3 {
		t.Fatalf("device writes = %v, want the whole edit", written)
	}
	if _, ok := priorView(c); ok {
		t.Fatal("prior view reported with no takeover")
	}
}

func TestMenuEditOfOnlyTakeoverKeysSkipsTheDevice(t *testing.T) {
	c, pub, snap, pomo := holdFixture(t, "running")
	pub.deviceSettings = map[string]any{"autoTransition": true, "blockNavigation": false}
	*pomo = true
	c.publish(*snap)

	var written []map[string]any
	if _, err := c.applyMenuSettings(context.Background(), map[string]any{"autoTransition": false}, recordWrites(&written)); err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("device writes = %v, want none", written)
	}
	if p, _ := priorView(c); p.AutoTransition || p.BlockNavigation {
		t.Fatalf("prior = %+v, want autoTransition:false blockNavigation:false", p)
	}
}

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

func TestMenuEditRacesTakeoverEdges(t *testing.T) {
	c, clk, snap, pomo := modelFixture(t)
	write := func(m map[string]any) error { return clk.Settings(context.Background(), m) }
	done := make(chan struct{})
	const edits = 50
	go func() {
		defer close(done)
		for i := range edits {
			if _, err := c.applyMenuSettings(context.Background(), map[string]any{"autoTransition": i%2 == 0, "brightness": 10.0}, write); err != nil {
				t.Error(err)
			}
			priorView(c)
		}
	}()
	for i := range 20 {
		*pomo = i%2 == 0
		c.publish(*snap)
	}
	<-done
	*pomo = false
	c.publish(*snap)

	if p, ok := priorView(c); ok {
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
		held, err := c.applyMenuSettings(context.Background(), map[string]any{"autoTransition": true}, write)
		res <- result{held, err}
	}()
	waitEntered(t, entered, "menu edit")
	*pomo = true
	c.publish(*snap)
	close(release)
	r := <-res
	if r.err != nil || len(r.held) != 1 || r.held[0] != "autoTransition" {
		t.Fatalf("held=%v err=%v, want [autoTransition]", r.held, r.err)
	}
	if p, ok := priorView(c); !ok || !p.AutoTransition {
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

func TestMenuEditDuringTakeoverSurvivesCrash(t *testing.T) {
	kv := &memKV{}
	c, pub, snap, pomo := holdFixture(t, "running")
	c.setSettingsKV(kv)
	pub.deviceSettings = map[string]any{"autoTransition": false, "blockNavigation": false}
	*pomo = true
	c.publish(*snap)
	var written []map[string]any
	if _, err := c.applyMenuSettings(context.Background(), map[string]any{"autoTransition": true}, recordWrites(&written)); err != nil {
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

type gatedWrite struct {
	clk              *modelClock
	entered, release chan struct{}
	failAt, calls    int
	mu               sync.Mutex
}

func newGatedWrite(clk *modelClock) *gatedWrite {
	return &gatedWrite{clk: clk, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gatedWrite) write(m map[string]any) error {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	if n == 1 {
		close(g.entered)
		<-g.release
	}
	if n == g.failAt {
		return errUnreachableDevice
	}
	return g.clk.Settings(context.Background(), m)
}

func (g *gatedWrite) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

type editResult struct {
	held []string
	err  error
}

func startEdit(t *testing.T, c *coordinator, m map[string]any, g *gatedWrite) chan editResult {
	res := make(chan editResult, 1)
	go func() {
		held, err := c.applyMenuSettings(context.Background(), m, g.write)
		res <- editResult{held, err}
	}()
	waitEntered(t, g.entered, "menu edit")
	return res
}

func directWrite(clk *modelClock) func(map[string]any) error {
	return func(m map[string]any) error { return clk.Settings(context.Background(), m) }
}

func TestOlderInFlightEditDoesNotReplaceNewer(t *testing.T) {
	for _, endBlockFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "during focus", true: "after focus"}[endBlockFirst], func(t *testing.T) {
			c, clk, snap, pomo := modelFixture(t)
			g := newGatedWrite(clk)
			e1 := startEdit(t, c, map[string]any{"autoTransition": true}, g)

			*pomo = true
			c.publish(*snap)
			if _, err := c.applyMenuSettings(context.Background(), map[string]any{"autoTransition": false}, directWrite(clk)); err != nil {
				t.Fatal(err)
			}
			if endBlockFirst {
				*pomo = false
				c.publish(*snap)
			}
			close(g.release)
			if r := <-e1; r.err != nil {
				t.Fatal(r.err)
			}
			if !endBlockFirst {
				if p, _ := priorView(c); p.AutoTransition {
					t.Fatalf("snapshot = %+v, want E2's autoTransition:false", p)
				}
				if got := clk.get("autoTransition"); got != false {
					t.Fatalf("clock mid-focus autoTransition = %v, want the takeover's false", got)
				}
				*pomo = false
				c.publish(*snap)
			}
			if got := clk.get("autoTransition"); got != false {
				t.Fatalf("clock autoTransition = %v, want E2's false", got)
			}
		})
	}
}

func TestMenuEditInFlightAcrossWholeTakeoverIsRewritten(t *testing.T) {
	c, clk, snap, pomo := modelFixture(t)
	g := newGatedWrite(clk)
	e := startEdit(t, c, map[string]any{"autoTransition": true}, g)

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)
	close(g.release)
	r := <-e
	if r.err != nil || len(r.held) != 0 {
		t.Fatalf("held=%v err=%v, want none and no error", r.held, r.err)
	}
	if got := clk.get("autoTransition"); got != true {
		t.Fatalf("clock autoTransition = %v, want the edit", got)
	}
	if n := g.callCount(); n != 2 {
		t.Fatalf("menu writes = %d, want the edit and its re-write", n)
	}
}

func TestMenuEditRewriteLostAnswersError(t *testing.T) {
	c, clk, snap, pomo := modelFixture(t)
	g := newGatedWrite(clk)
	g.failAt = 2
	e := startEdit(t, c, map[string]any{"autoTransition": true}, g)

	*pomo = true
	c.publish(*snap)
	*pomo = false
	c.publish(*snap)
	close(g.release)
	if r := <-e; r.err == nil {
		t.Fatal("lost re-write answered success")
	}
	if got := clk.get("autoTransition"); got != true {
		t.Fatalf("clock autoTransition = %v, want the first write's true", got)
	}
}

func priorView(c *coordinator) (takeoverPrior, bool) {
	p, ok, _ := c.takeoverPriorViewContext(context.Background())
	return p, ok
}
